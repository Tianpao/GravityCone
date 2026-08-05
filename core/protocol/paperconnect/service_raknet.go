package paperconnect

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"sync/atomic"
	"time"

	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	raknet "github.com/sandertv/go-raknet"

	"gravitycone/core/easytier"
)

// dialRakNet 建立到 addr 的 RakNet 连接（30 秒超时）。
func dialRakNet(addr string) (*raknet.Conn, error) {
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dialCancel()
	return (raknet.Dialer{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).DialContext(dialCtx, addr)
}

// pcGuestSetupConnection sets up the NetherNet or RakNet game connection asynchronously.
func (s *PaperConnectService) pcGuestSetupConnection(manager *easytier.EasyTierManager, playerName string, protocol string, rakLocalPort uint16) {
	if s.joinCancelled.Load() {
		return
	}

	// Establish forwarding before the local discovery or broadcast phase.
	s.guestMu.Lock()
	hostIP := s.guestHostVirtualIP
	gamePort := s.guestGamePort
	s.guestMu.Unlock()

	// proxy 模式的转发已在 Phase 2 装好。
	rakDialAddr := pcDialAddr(manager.DialMode(), hostIP, gamePort, rakLocalPort)

	if protocol == ProtocolNetherNet {
		// 仅本地 discovery 阶段受 MC 占用 7551 限制；隧道已就绪。

		rkConn, err := dialRakNet(rakDialAddr)
		if err != nil {
			slog.Error("RakNet dial to host failed", "err", err, "addr", rakDialAddr)
			s.pcGuestSetupError(manager, protocol)
			return
		}
		if !pcAttachGuest(s, manager, &s.guestRakConn, rkConn) {
			rkConn.Close()
			return
		}

		discCfg := discovery.ListenConfig{
			NetworkID: randomID(),
			Log:       slog.Default(),
		}
		var disc *discovery.Listener
		for {
			disc, err = discCfg.Listen("0.0.0.0:7551")
			if err == nil {
				break
			}
			if !isAddressInUse(err) {
				slog.Error("NetherNet discovery listen failed", "err", err)
				s.pcGuestSetupError(manager, protocol)
				return
			}
			slog.Warn("NetherNet discovery port is occupied", "port", 7551, "err", err)
			if !s.pcWaitForMinecraftEnded(manager) {
				return
			}
		}
		if !pcAttachGuest(s, manager, &s.guestDisc, disc) {
			disc.Close()
			return
		}
		s.pcClearGuestPortBusy(manager)

		disc.ServerData(&discovery.ServerData{
			ServerName:            s.guestMotd,
			LevelName:             "Join",
			GameType:              discovery.GameTypeSurvival,
			PlayerCount:           1,
			MaxPlayerCount:        20,
			AcceptsOnlineAuth:     true,
			AcceptsSelfSignedAuth: true,
			TransportLayer:        discovery.TransportLayerNetherNet,
			ConnectionType:        4,
		})

		nnCfg := nethernet.ListenConfig{
			AllowAnonymous:    true,
			DisableTrickleICE: true,
		}
		nnLn, err := nnCfg.Listen(disc)
		if err != nil {
			slog.Error("NetherNet listen failed", "err", err)
			s.pcGuestSetupError(manager, protocol)
			return
		}
		if !pcAttachGuest(s, manager, &s.guestNnLn, nnLn) {
			nnLn.Close()
			return
		}

		slog.Info("NetherNet listening for local client", "network_id", disc.NetworkID())
		go s.pcAdvertiseLoop(disc)
		s.pcGuestConnectionReady(manager, protocol)

		for {
			nnConn, err := nnLn.Accept()
			if err != nil {
				if s.pcGuestActive(manager) {
					slog.Error("NetherNet accept failed", "err", err)
					s.pcGuestSetupError(manager, protocol)
				}
				return
			}
			slog.Info("local MC client connected via NetherNet", "remote", nnConn.RemoteAddr())

			proxyCtx, proxyCancel := context.WithCancel(context.Background())
			if !pcAttachGuest(s, manager, &s.guestCancelFunc, proxyCancel) {
				proxyCancel()
				_ = nnConn.Close()
				return
			}
			proxyPackets(proxyCtx, slog.Default(), nnConn.(*nethernet.Conn), rkConn)

			s.guestMu.Lock()
			active := s.pcGuestActiveLocked(manager)
			if active {
				s.guestCancelFunc = nil
				if s.guestRakConn == rkConn {
					s.guestRakConn = nil
				}
			}
			s.guestMu.Unlock()
			if !active {
				return
			}

			rkConn, err = dialRakNet(rakDialAddr)
			if err != nil {
				slog.Error("RakNet re-dial to host failed", "err", err, "addr", rakDialAddr)
				s.pcGuestSetupError(manager, protocol)
				return
			}
			if !pcAttachGuest(s, manager, &s.guestRakConn, rkConn) {
				_ = rkConn.Close()
				return
			}
			slog.Info("RakNet tunnel re-established, waiting for local Minecraft")
		}
	}

	// ---- RakNet path ----
	serverName := s.guestMotd
	readyCh := make(chan error, 1)
	fakeStop := make(chan struct{})

	// direct 模式(TUN)没有端口转发：本地起一个 RakNet 监听，接受本机
	// MC 客户端连接后经隧道中继到 host 虚拟 IP 的游戏端口。
	// 必须绑 0.0.0.0 而不是 127.0.0.1：RakNet Pong 不携带 IP，客户端把
	// Pong 的来源 IP 当作房间 IP，而 127.0.0.1 无法作为广播来源，广播/单播
	// pong 的来源是本机局域网 IP，只绑回环会导致客户端连局域网 IP 被拒。
	motdQueryAddr := fmt.Sprintf("127.0.0.1:%d", rakLocalPort)
	if manager.DialMode() == easytier.DialModeDirect {
		relayLn, err := (raknet.ListenConfig{
			MaxMTU:   rakNetMTU,
			ErrorLog: slog.Default(),
		}).Listen("0.0.0.0:0")
		if err != nil {
			slog.Error("RakNet relay listen failed", "err", err)
			s.pcGuestSetupError(manager, protocol)
			return
		}
		relayPort := uint16(relayLn.Addr().(*net.UDPAddr).Port)
		if !pcAttachGuest(s, manager, &s.guestRakRelayLn, relayLn) {
			relayLn.Close()
			return
		}
		go s.pcRakNetRelayLoop(relayLn, manager, hostIP, gamePort)
		// MOTD 从 host 直查（direct 模式直连可达），广播端口指向本机中继。
		motdQueryAddr = fmt.Sprintf("%s:%d", hostIP, gamePort)
		rakLocalPort = relayPort
	}

	go broadcastRakNetFakeServer(context.Background(), fakeStop, serverName, rakLocalPort, motdQueryAddr, readyCh)
	if err := <-readyCh; err != nil {
		slog.Error("RakNet fake server failed to start", "err", err, "proxyPort", rakLocalPort)
		close(fakeStop)
		s.pcGuestSetupError(manager, protocol)
		return
	}
	if !pcAttachGuest(s, manager, &s.guestRakNetFakeStop, fakeStop) {
		close(fakeStop)
		return
	}
	slog.Info("RakNet fake server ready", "proxyPort", rakLocalPort, "serverName", serverName, "dialMode", manager.DialMode())
	s.pcGuestConnectionReady(manager, protocol)
}

// pcRakNetRelayLoop accepts local Minecraft clients on the relay listener and
// forwards their RakNet packets to the host through the EasyTier virtual
// network. Used only in direct mode (Android TUN), where no local port
// forward exists.
func (s *PaperConnectService) pcRakNetRelayLoop(ln *raknet.Listener, manager *easytier.EasyTierManager, hostIP string, gamePort uint16) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if s.pcGuestActive(manager) {
				slog.Error("RakNet relay accept failed", "err", err)
				s.pcGuestSetupError(manager, ProtocolRakNet)
			}
			return
		}
		localConn := conn.(*raknet.Conn)
		slog.Info("local MC client connected via RakNet relay", "remote", localConn.RemoteAddr())

		remoteAddr := fmt.Sprintf("%s:%d", hostIP, gamePort)
		remoteConn, err := dialRakNet(remoteAddr)
		if err != nil {
			slog.Error("RakNet relay dial to host failed", "err", err, "addr", remoteAddr)
			_ = localConn.Close()
			// 隧道暂时不可达，保留监听等待重试；玩家重进即可再连。
			continue
		}

		proxyCtx, proxyCancel := context.WithCancel(context.Background())
		if !pcAttachGuest(s, manager, &s.guestCancelFunc, proxyCancel) {
			proxyCancel()
			_ = localConn.Close()
			_ = remoteConn.Close()
			return
		}
		relayRakNetPackets(proxyCtx, slog.Default(), localConn, remoteConn)
	}
}

// relayRakNetPackets forwards application-layer packets between two RakNet
// connections until either side closes or the context is cancelled. Both
// directions are forwarded without transformation; the tunnel protocol is
// only needed on the host link of the NetherNet path, not here.
func relayRakNetPackets(parentCtx context.Context, log *slog.Logger, local, remote *raknet.Conn) {
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()
	go func() {
		<-ctx.Done()
		_ = local.Close()
		_ = remote.Close()
	}()

	var l2r, r2l atomic.Int64

	go func() {
		defer cancel()
		forwardOneWay(ctx, log, local, remote, &l2r, "local raknet read error", "remote raknet write error")
	}()

	go func() {
		defer cancel()
		forwardOneWay(ctx, log, remote, local, &r2l, "remote raknet read error", "local raknet write error")
	}()

	<-ctx.Done()
}

// forwardOneWay 把 src 的 RakNet 包转发到 dst，直至任一侧出错或 ctx 结束。
func forwardOneWay(ctx context.Context, log *slog.Logger, src, dst *raknet.Conn, forwarded *atomic.Int64, readLabel, writeLabel string) {
	for {
		pk, err := src.ReadPacket()
		if err != nil {
			if !isClosedErr(err) && ctx.Err() == nil {
				log.Error(readLabel, "err", err, "forwarded", forwarded.Load())
			}
			return
		}
		forwarded.Add(1)
		if _, err := dst.Write(pk); err != nil {
			if !isClosedErr(err) && ctx.Err() == nil {
				log.Error(writeLabel, "err", err, "forwarded", forwarded.Load())
			}
			return
		}
	}
}
