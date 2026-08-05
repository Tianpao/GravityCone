package paperconnect

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"syscall"
	"time"

	"gravitycone/core/easytier"
	"gravitycone/core/protocol/common"
)

// --- GUEST methods ---

func (s *PaperConnectService) CancelJoin() {
	s.joinCancelled.Store(true)

	s.guestMu.Lock()
	running := s.guestRunning
	s.guestMu.Unlock()
	if running {
		_ = s.LeaveRoom()
	}
}

// ConfirmMinecraftEnded permits one new local NetherNet discovery bind attempt
// after the caller has released UDP port 7551 by closing Minecraft.
func (s *PaperConnectService) ConfirmMinecraftEnded() error {
	s.guestMu.Lock()
	defer s.guestMu.Unlock()

	if !s.guestRunning || !s.guestPortBusy || s.guestPortBusyConfirm == nil {
		return fmt.Errorf("未等待Minecraft结束确认")
	}

	select {
	case s.guestPortBusyConfirm <- struct{}{}:
		return nil
	default:
		return fmt.Errorf("Minecraft结束确认已提交")
	}
}

func (s *PaperConnectService) JoinRoom(code string, playerName string, vendorPrefix string, motd string) (*PaperConnectConnectionStatus, error) {
	s.joinCancelled.Store(false)
	s.guestMu.Lock()
	if s.guestRunning {
		s.guestMu.Unlock()
		return nil, fmt.Errorf("已在一个房间中")
	}
	s.guestMu.Unlock()

	rc, err := ParsePaperConnectRoomCode(code)
	if err != nil {
		return nil, err
	}

	// 房客 peers 只计算一次（proxy 模式两阶段共用；uptime 地址换取只做一遍）
	guestPeers := s.relay.GuestPeers(s.resolvePeers(), rc.NodeID())

	// Phase 1: start EasyTier without port forwards to discover host.
	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	_, err = manager.Start(easytier.StartOptions{
		NetworkName:        rc.EasyTierNetworkName(),
		NetworkSecret:      rc.EasyTierNetworkSecret(),
		IsHost:             false,
		Peers:              guestPeers,
		UpstreamCompatible: true,
		DisableP2P:         s.relay.P2PDisabled(),
	})
	if err != nil {
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}

	if s.joinCancelled.Load() {
		manager.Stop()
		return nil, fmt.Errorf("加入已取消")
	}

	hostname, hostIP, err := s.pcDiscoverHost(manager, 60*time.Second)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("发现主机失败: %w", err)
	}

	parsed, err := parseHostname(hostname)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("解析主机名失败: %w", err)
	}

	protocol := parsed.Protocol
	serverPort := parsed.TCPPort
	gamePort := parsed.GamePort

	if s.joinCancelled.Load() {
		manager.Stop()
		return nil, fmt.Errorf("加入已取消")
	}

	// Phase 2: proxy 模式重启 EasyTier 并携带静态端口转发（FFI 无运行时转发 RPC）。
	// direct 模式（TUN）无需本地端口，直接拨 host 虚拟 IP。
	dialMode := manager.DialMode()
	var tcpLocalPort, rakLocalPort uint16
	if dialMode == easytier.DialModeProxy {
		tcpLocalLn, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			manager.Stop()
			return nil, fmt.Errorf("分配本地TCP端口失败: %w", err)
		}
		tcpLocalPort = uint16(tcpLocalLn.Addr().(*net.TCPAddr).Port)
		_ = tcpLocalLn.Close()

		rakLocalConn, err := net.ListenPacket("udp", pcUDPBindHost(protocol)+":0")
		if err != nil {
			manager.Stop()
			return nil, fmt.Errorf("分配本地UDP端口失败: %w", err)
		}
		rakLocalPort = uint16(rakLocalConn.LocalAddr().(*net.UDPAddr).Port)
		_ = rakLocalConn.Close()
	}

	if dialMode == easytier.DialModeProxy {
		if err := manager.Stop(); err != nil {
			return nil, fmt.Errorf("停止虚拟网络(发现阶段)失败: %w", err)
		}
		slog.Info("PaperConnect discovery phase stopped; starting static forwarding phase",
			"protocol", protocol, "host", hostIP, "control_port", serverPort, "game_port", gamePort)
		manager, err = easytier.NewEasyTierManager()
		if err != nil {
			return nil, err
		}
		_, err = manager.Start(easytier.StartOptions{
			NetworkName:        rc.EasyTierNetworkName(),
			NetworkSecret:      rc.EasyTierNetworkSecret(),
			IsHost:             false,
			PortForwards:       pcGuestPortForwards(dialMode, protocol, hostIP, serverPort, gamePort, tcpLocalPort, rakLocalPort),
			Peers:              guestPeers,
			UpstreamCompatible: true,
			DisableP2P:         s.relay.P2PDisabled(),
		})
		if err != nil {
			return nil, fmt.Errorf("启动虚拟网络(端口转发)失败: %w", err)
		}
		slog.Info("PaperConnect static forwarding phase started",
			"tcp_local_port", tcpLocalPort, "udp_local_port", rakLocalPort)
	}

	// Wait for TCP ping to succeed.
	var pingOk bool
	for attempt := 0; attempt < 30; attempt++ {
		if s.joinCancelled.Load() {
			manager.Stop()
			return nil, fmt.Errorf("加入已取消")
		}

		pingAddr := pcDialAddr(dialMode, hostIP, serverPort, tcpLocalPort)
		conn, err := net.DialTimeout("tcp", pingAddr, 2*time.Second)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		pingReq := PCPingRequest{Time: time.Now().UnixMilli()}
		if err := WritePCRequest(conn, PCPing, pingReq); err != nil {
			conn.Close()
			time.Sleep(1 * time.Second)
			continue
		}

		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		conn.Close()

		if err != nil || n == 0 {
			time.Sleep(1 * time.Second)
			continue
		}

		var pingResp PCPingResponse
		if err := json.Unmarshal(buf[:n], &pingResp); err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		pingOk = true
		_ = pingResp
		break
	}
	if !pingOk {
		manager.Stop()
		return nil, fmt.Errorf("连接主机超时，TCP端口转发似乎未生效")
	}

	clientId := common.MakeVendor(vendorPrefix)

	controlAddr := pcDialAddr(dialMode, hostIP, serverPort, tcpLocalPort)
	controlHost, controlPort, err := net.SplitHostPort(controlAddr)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("解析本地控制地址失败: %w", err)
	}
	controlPortNumber, err := strconv.ParseUint(controlPort, 10, 16)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("解析本地控制端口失败: %w", err)
	}
	s.pcGuestRegister(controlHost, uint16(controlPortNumber), clientId, playerName)

	s.guestMu.Lock()
	s.guestManager = manager
	s.guestStopCh = make(chan struct{})
	s.guestRunning = true
	s.guestHeartbeating = true
	s.guestRoomCode = rc
	s.guestPlayerName = playerName
	s.guestHostVirtualIP = hostIP
	s.guestTCPLocalPort = tcpLocalPort
	s.guestProtocol = protocol
	s.guestGamePort = gamePort
	s.guestMotd = motd
	if s.guestMotd == "" {
		s.guestMotd = "GravityCone 联机房间"
	}
	s.pcResetGuestPortBusyLocked()
	s.guestMu.Unlock()

	go s.pcGuestHeartbeatLoop(clientId, playerName, controlHost, uint16(controlPortNumber))
	go s.pcGuestSetupConnection(manager, playerName, protocol, rakLocalPort)

	status := s.pcBuildConnectionStatus()
	s.eventEmitter.Emit("paperconnect.room.info", status)
	return status, nil
}

func (s *PaperConnectService) pcGuestRegister(hostIP string, tcpPort uint16, clientId string, playerName string) {
	for attempt := 0; attempt < 10; attempt++ {
		if s.joinCancelled.Load() {
			return
		}

		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", hostIP, tcpPort), 5*time.Second)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		req := PCPlayerRequest{
			ClientId:   clientId,
			PlayerName: playerName,
		}
		if err := WritePCRequest(conn, PCPlayer, req); err != nil {
			conn.Close()
			time.Sleep(1 * time.Second)
			continue
		}

		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		buf := make([]byte, 4096)
		n, err := conn.Read(buf)
		conn.Close()

		if err != nil || n == 0 {
			time.Sleep(1 * time.Second)
			continue
		}

		var resp PCPlayerResponse
		if err := json.Unmarshal(buf[:n], &resp); err == nil {
			s.guestMu.Lock()
			s.guestPlayers = resp.Players
			s.guestMu.Unlock()
		}
		return
	}
}

func (s *PaperConnectService) pcDiscoverHost(manager *easytier.EasyTierManager, timeout time.Duration) (hostname string, virtualIP string, err error) {
	deadline := time.Now().Add(timeout)
	var lastErr error

	for time.Now().Before(deadline) {
		if s.joinCancelled.Load() {
			return "", "", fmt.Errorf("加入已取消")
		}
		if !manager.IsRunning() {
			return "", "", fmt.Errorf("easytier-core 进程已退出")
		}

		hn, ip, err := manager.DiscoverPeerByPrefix(pcHostnamePrefix)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}

		return hn, ip, nil
	}

	return "", "", fmt.Errorf("发现主机超时: %w", lastErr)
}

func (s *PaperConnectService) pcGuestHeartbeatLoop(clientId string, playerName string, hostIP string, tcpPort uint16) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	consecutiveFailures := 0
	const maxFailures = 3

	for {
		select {
		case <-ticker.C:
			s.guestMu.Lock()
			running := s.guestRunning
			s.guestMu.Unlock()

			if !running {
				return
			}

			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", hostIP, tcpPort), 5*time.Second)
			if err != nil {
				consecutiveFailures++
				if consecutiveFailures >= maxFailures {
					s.pcAutoDisconnect("房主已关闭房间")
					return
				}
				continue
			}

			req := PCPlayerRequest{
				ClientId:   clientId,
				PlayerName: playerName,
			}
			if err := WritePCRequest(conn, PCPlayer, req); err != nil {
				conn.Close()
				consecutiveFailures++
				if consecutiveFailures >= maxFailures {
					s.pcAutoDisconnect("房主已关闭房间")
					return
				}
				continue
			}

			conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			buf := make([]byte, 4096)
			n, err := conn.Read(buf)
			conn.Close()

			if err != nil {
				consecutiveFailures++
				if consecutiveFailures >= maxFailures {
					s.pcAutoDisconnect("房主已关闭房间")
					return
				}
				continue
			}

			consecutiveFailures = 0

			var resp PCPlayerResponse
			if err := json.Unmarshal(buf[:n], &resp); err == nil {
				s.guestMu.Lock()
				s.guestPlayers = resp.Players
				s.guestMu.Unlock()
				s.eventEmitter.Emit("paperconnect.room.info", s.pcBuildConnectionStatus())
			}

		case <-s.guestStopCh:
			return
		}
	}
}

func (s *PaperConnectService) LeaveRoom() error {
	s.guestMu.Lock()
	if s.guestRunning {
		close(s.guestStopCh)
		s.guestRunning = false
		s.guestHeartbeating = false
	}
	manager := s.pcCleanupGuestLocked()
	s.guestDisconnectReason = ""
	s.guestMu.Unlock()

	if manager != nil {
		manager.Stop()
	}

	return nil
}

func (s *PaperConnectService) pcCleanupGuestGameResourcesLocked() {
	if s.guestCancelFunc != nil {
		s.guestCancelFunc()
	}
	if s.guestRakNetFakeStop != nil {
		close(s.guestRakNetFakeStop)
	}
	if s.guestRakConn != nil {
		s.guestRakConn.Close()
	}
	if s.guestNnLn != nil {
		s.guestNnLn.Close()
	}
	if s.guestRakRelayLn != nil {
		s.guestRakRelayLn.Close()
	}
	if s.guestDisc != nil {
		s.guestDisc.Close()
	}
	s.guestRakConn = nil
	s.guestNnLn = nil
	s.guestRakRelayLn = nil
	s.guestDisc = nil
	s.guestCancelFunc = nil
	s.guestRakNetFakeStop = nil
}

func (s *PaperConnectService) pcCleanupGuestLocked() *easytier.EasyTierManager {
	s.pcCleanupGuestGameResourcesLocked()
	manager := s.guestManager
	s.guestManager = nil
	s.pcResetGuestPortBusyLocked()
	s.guestPlayers = nil
	s.guestHostVirtualIP = ""
	s.guestTCPLocalPort = 0
	s.guestRoomCode = nil
	s.guestPlayerName = ""
	s.guestProtocol = ""
	return manager
}

func (s *PaperConnectService) GetConnectionStatus() (*PaperConnectConnectionStatus, error) {
	s.guestMu.Lock()
	running := s.guestRunning
	s.guestMu.Unlock()

	if !running {
		s.guestMu.Lock()
		reason := s.guestDisconnectReason
		s.guestMu.Unlock()
		if reason != "" {
			return s.pcBuildConnectionStatus(), nil
		}
		return nil, fmt.Errorf("未连接到任何房间")
	}

	return s.pcBuildConnectionStatus(), nil
}

func (s *PaperConnectService) pcBuildConnectionStatus() *PaperConnectConnectionStatus {
	s.guestMu.Lock()
	defer s.guestMu.Unlock()

	code := ""
	if s.guestRoomCode != nil {
		code = s.guestRoomCode.Format()
	}

	return &PaperConnectConnectionStatus{
		RoomCode:         code,
		HostAddress:      s.guestHostVirtualIP,
		GamePort:         int(s.guestGamePort),
		SubProtocol:      s.guestProtocol,
		Connected:        s.guestRunning,
		OnlineCount:      len(s.guestPlayers),
		Players:          s.guestPlayers,
		Heartbeating:     s.guestHeartbeating,
		DisconnectReason: s.guestDisconnectReason,
	}
}

// --- Guest port conflict handling (UDP 7551) ---

func isAddressInUse(err error) bool {
	// Windows wraps WSAEADDRINUSE (10048), which is distinct from the
	// compatibility EADDRINUSE value exposed by syscall on that platform.
	return errors.Is(err, syscall.EADDRINUSE) || errors.Is(err, syscall.Errno(10048))
}

func (s *PaperConnectService) pcGuestActiveLocked(manager *easytier.EasyTierManager) bool {
	return s.guestRunning && s.guestManager == manager
}

func (s *PaperConnectService) pcGuestActive(manager *easytier.EasyTierManager) bool {
	s.guestMu.Lock()
	defer s.guestMu.Unlock()
	return s.pcGuestActiveLocked(manager)
}

func pcAttachGuest[T any](s *PaperConnectService, manager *easytier.EasyTierManager, target *T, value T) bool {
	s.guestMu.Lock()
	defer s.guestMu.Unlock()
	if !s.pcGuestActiveLocked(manager) {
		return false
	}
	*target = value
	return true
}

func (s *PaperConnectService) pcResetGuestPortBusyLocked() {
	s.guestPortBusy = false
	s.guestPortBusyConfirm = nil
}

func (s *PaperConnectService) pcWaitForMinecraftEnded(manager *easytier.EasyTierManager) bool {
	s.guestMu.Lock()
	if !s.pcGuestActiveLocked(manager) {
		s.guestMu.Unlock()
		return false
	}
	confirmCh := make(chan struct{}, 1)
	s.guestPortBusy = true
	s.guestPortBusyConfirm = confirmCh
	stopCh := s.guestStopCh
	s.guestMu.Unlock()

	s.eventEmitter.Emit("paperconnect.connection.port_busy", map[string]string{
		"port":    "7551",
		"message": "Minecraft 正在占用 UDP 端口 7551。请结束 Minecraft 后确认。",
	})

	select {
	case <-confirmCh:
		s.guestMu.Lock()
		if s.guestPortBusyConfirm == confirmCh {
			s.pcResetGuestPortBusyLocked()
		}
		active := s.pcGuestActiveLocked(manager)
		s.guestMu.Unlock()
		return active
	case <-stopCh:
		return false
	}
}

func (s *PaperConnectService) pcClearGuestPortBusy(manager *easytier.EasyTierManager) {
	s.guestMu.Lock()
	defer s.guestMu.Unlock()
	if s.pcGuestActiveLocked(manager) {
		s.pcResetGuestPortBusyLocked()
	}
}

func (s *PaperConnectService) pcGuestConnectionReady(manager *easytier.EasyTierManager, protocol string) {
	if !s.pcGuestActive(manager) {
		return
	}
	slog.Info("PaperConnect game connection ready", "protocol", protocol)
	s.eventEmitter.Emit("paperconnect.connection.ready", map[string]string{"protocol": protocol})
}

func (s *PaperConnectService) pcGuestSetupError(manager *easytier.EasyTierManager, protocol string) {
	s.guestMu.Lock()
	active := s.pcGuestActiveLocked(manager)
	if active {
		s.pcCleanupGuestGameResourcesLocked()
		s.pcResetGuestPortBusyLocked()
	}
	s.guestMu.Unlock()
	if active {
		slog.Warn("PaperConnect game connection setup failed, only control channel active", "protocol", protocol)
		s.eventEmitter.Emit("paperconnect.connection.error", map[string]string{"message": "游戏连接建立失败，仅控制通道可用"})
	}
}

func (s *PaperConnectService) pcAutoDisconnect(reason string) {
	s.guestMu.Lock()
	s.guestRunning = false
	s.guestHeartbeating = false
	s.guestDisconnectReason = reason
	manager := s.pcCleanupGuestLocked()
	s.guestMu.Unlock()

	s.eventEmitter.Emit("paperconnect.room.disconnected", map[string]string{"reason": reason})

	if manager != nil {
		manager.Stop()
	}
}
