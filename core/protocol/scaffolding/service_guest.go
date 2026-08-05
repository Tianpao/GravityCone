package scaffolding

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strings"
	"time"

	"gravitycone/core/easytier"
	lansca "gravitycone/core/lan/scaffolding"
	"gravitycone/core/utils"
)

// CancelJoin aborts a running JoinRoom call. Safe to call even if no join is in progress.
func (s *ScaffoldingService) CancelJoin() {
	s.joinCancelled.Store(true)
}

// Only needed by CLI mode; Wails mode ignores it.
func (s *ScaffoldingService) setJoinProgressCallback(cb func(step string)) {
	s.joinProgressCb = cb
}

// Package-level helper so the CLI handler can call it without the method
// appearing in Wails bindings.
func SetScaffoldingJoinProgress(svc *ScaffoldingService, cb func(step string)) {
	svc.setJoinProgressCallback(cb)
}

func (s *ScaffoldingService) reportJoinProgress(step string) {
	if cb := s.joinProgressCb; cb != nil {
		cb(step)
	}
}

func (s *ScaffoldingService) JoinRoom(code string, playerName string, vendorPrefix string, motd string) (*ConnectionStatus, error) {
	s.joinCancelled.Store(false)
	s.guestMu.Lock()
	if s.guestRunning {
		s.guestMu.Unlock()
		return nil, fmt.Errorf("已在一个房间中")
	}
	s.guestMu.Unlock()

	rc, err := ParseRoomCode(code)
	if err != nil {
		return nil, err
	}

	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	machineID, _ := utils.GetMachineID()
	if _, err := manager.Start(easytier.StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		IsHost:        false,
		Peers:         s.relay.GuestPeers(s.resolvePeers(), rc.NodeID()),
		DisableP2P:    s.relay.P2PDisabled(),
	}); err != nil {
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}
	s.reportJoinProgress("connecting")

	// Retry until we can actually connect via TCP (P2P may take time to establish).
	if s.joinCancelled.Load() {
		manager.Stop()
		return nil, fmt.Errorf("加入已取消")
	}
	hostIP, _, dc, err := s.discoverHostAndConnect(manager, 60*time.Second)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("连接主机失败: %w", err)
	}

	conn := dc.conn

	easytierID := ""
	if peerID, err := manager.GetPeerID(); err == nil {
		easytierID = peerID
	}

	negotiatedEasyTierID, mcPort, err := s.joinHandshake(conn, manager, machineID, playerName, easytierID, vendorPrefix)
	if err != nil {
		return nil, err
	}

	s.guestMu.Lock()
	s.guestManager = manager
	s.guestConn = conn
	s.guestStopCh = make(chan struct{})
	s.guestRunning = true
	s.guestMCAddr = hostIP
	s.guestMCPort = mcPort
	s.guestHeartbeating = true
	s.guestRoomCode = rc
	s.guestPlayerName = playerName
	s.guestNegotiatedEasyTierID = negotiatedEasyTierID
	s.guestMotd = motd
	s.guestScaffoldingLocalPort = dc.localPort
	s.guestDirectLocal = dc.directLocal
	s.guestMu.Unlock()

	// Set up MC port-forward via EasyTier (compatible with both GravityCone and Terracotta hosts)
	if mcPort != 0 {
		s.setupMCPortForward(hostIP, mcPort)
	}

	// Background reader: like Rust's ClientSession background thread.
	// Continuously reads responses from the TCP connection and delivers
	// them via guestReadCh. When the connection breaks, the channel closes.
	s.guestReadCh = make(chan readResult, 32)
	go s.guestReadLoop(conn)

	go s.guestHeartbeatLoop(machineID, easytierID, playerName, vendorPrefix)

	s.reportJoinProgress("ready")
	s.refreshGuestPlayerList()
	return s.buildConnectionStatus(), nil
}

type discoveredConn struct {
	conn        net.Conn
	localPort   uint16
	directLocal bool
}

func (s *ScaffoldingService) discoverHostAndConnect(manager *easytier.EasyTierManager, timeout time.Duration) (string, uint16, *discoveredConn, error) {
	deadline := time.Now().Add(timeout)

	var lastErr error
	var prevForwardLocal string
	var prevForwardRemote string

	for time.Now().Before(deadline) {
		s.reportJoinProgress("waiting_peer")
		if s.joinCancelled.Load() {
			return "", 0, nil, fmt.Errorf("加入已取消")
		}
		if !manager.IsRunning() {
			return "", 0, nil, fmt.Errorf("easytier-core 进程已退出")
		}

		hostIP, scaffoldingPort, err := manager.FindPeerByHostnamePrefix(hostnamePrefix)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}

		if dc := s.tryDirectLocalhost(scaffoldingPort); dc != nil {
			slog.Info("connected via direct localhost", "port", scaffoldingPort)
			return hostIP, scaffoldingPort, dc, nil
		}

		localPort, conn, err := s.tryP2PConnect(manager, hostIP, scaffoldingPort)
		if err != nil {
			lastErr = err
			prevForwardLocal = fmt.Sprintf("0.0.0.0:%d", localPort)
			prevForwardRemote = fmt.Sprintf("%s:%d", hostIP, scaffoldingPort)
			time.Sleep(2 * time.Second)
			continue
		}

		if prevForwardLocal != "" {
			manager.RemovePortForward("tcp", prevForwardLocal, prevForwardRemote)
		}

		return hostIP, scaffoldingPort, &discoveredConn{conn: conn, localPort: localPort}, nil
	}

	return "", 0, nil, lastErr
}

// verifyP2PTunnel 通过 ping 验证隧道连通性；失败时关闭连接并返回错误。
func verifyP2PTunnel(conn net.Conn) error {
	if err := WriteProtocolRequest(conn, ProtocolPing, nil); err != nil {
		conn.Close()
		return err
	}
	if _, _, err := ReadProtocolResponse(conn); err != nil {
		conn.Close()
		return err
	}
	return nil
}

func (s *ScaffoldingService) tryDirectLocalhost(scaffoldingPort uint16) *discoveredConn {
	directConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", scaffoldingPort), 2*time.Second)
	if err != nil {
		return nil
	}
	if err := verifyP2PTunnel(directConn); err != nil {
		return nil
	}
	return &discoveredConn{conn: directConn, localPort: scaffoldingPort, directLocal: true}
}

func (s *ScaffoldingService) tryP2PConnect(manager *easytier.EasyTierManager, hostIP string, scaffoldingPort uint16) (uint16, net.Conn, error) {
	if manager.HasTUN() {
		// TUN 模式（Android VpnService）：虚拟 IP 直达 host 的 Scaffolding
		// 端口，无需端口转发。
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", hostIP, scaffoldingPort), 5*time.Second)
		if err != nil {
			return 0, nil, fmt.Errorf("TUN直连失败 (%s:%d): %w", hostIP, scaffoldingPort, err)
		}
		if err := verifyP2PTunnel(conn); err != nil {
			return 0, nil, fmt.Errorf("P2P隧道验证失败: %w", err)
		}
		return 0, conn, nil
	}

	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, nil, fmt.Errorf("分配本地端口失败: %w", err)
	}
	localPort := uint16(localListener.Addr().(*net.TCPAddr).Port)
	localListener.Close()

	if err := manager.AddPortForward("tcp",
		fmt.Sprintf("0.0.0.0:%d", localPort),
		fmt.Sprintf("%s:%d", hostIP, scaffoldingPort),
	); err != nil {
		return 0, nil, fmt.Errorf("添加Scaffolding端口转发失败: %w", err)
	}

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 5*time.Second)
	if err != nil {
		return localPort, nil, fmt.Errorf("TCP连接失败 (127.0.0.1:%d -> %s:%d): %w", localPort, hostIP, scaffoldingPort, err)
	}

	if err := verifyP2PTunnel(conn); err != nil {
		return localPort, nil, fmt.Errorf("P2P隧道验证失败: %w", err)
	}

	return localPort, conn, nil
}

func (s *ScaffoldingService) joinHandshake(conn net.Conn, manager *easytier.EasyTierManager, machineID, playerName, easytierID, vendorPrefix string) (bool, uint16, error) {
	var handshakeErr error
	defer func() {
		if handshakeErr != nil {
			conn.Close()
			manager.Stop()
		}
	}()

	pingData, _ := json.Marshal(newGuestPlayerInfo(machineID, playerName, easytierID, vendorPrefix))
	if err := WriteProtocolRequest(conn, ProtocolPlayerPing, pingData); err != nil {
		handshakeErr = fmt.Errorf("发送心跳失败: %w", err)
		return false, 0, handshakeErr
	}
	if _, _, err := ReadProtocolResponse(conn); err != nil {
		handshakeErr = fmt.Errorf("心跳响应失败: %w", err)
		return false, 0, handshakeErr
	}

	supportedProtocols := strings.Join([]string{
		ProtocolPing,
		ProtocolProtocols,
		ProtocolServerPort,
		ProtocolPlayerPing,
		ProtocolPlayerProfilesList,
		ProtocolPlayerEasyTierID,
	}, "\x00")
	if err := WriteProtocolRequest(conn, ProtocolProtocols, []byte(supportedProtocols)); err != nil {
		handshakeErr = fmt.Errorf("协议协商失败: %w", err)
		return false, 0, handshakeErr
	}
	status, respBody, err := ReadProtocolResponse(conn)
	if err != nil || status != StatusOK {
		handshakeErr = fmt.Errorf("协议协商失败")
		return false, 0, handshakeErr
	}
	negotiatedEasyTierID := slices.Contains(strings.Split(string(respBody), "\x00"), ProtocolPlayerEasyTierID)

	s.reportJoinProgress("handshaking")

	if err := WriteProtocolRequest(conn, ProtocolServerPort, nil); err != nil {
		handshakeErr = fmt.Errorf("获取服务器端口失败: %w", err)
		return false, 0, handshakeErr
	}
	status, respBody, err = ReadProtocolResponse(conn)
	if err != nil {
		handshakeErr = fmt.Errorf("获取服务器端口失败: %w", err)
		return false, 0, handshakeErr
	}
	if status != StatusOK && status != StatusServerNotStarted {
		handshakeErr = fmt.Errorf("获取服务器端口失败: 状态=%d", status)
		return false, 0, handshakeErr
	}

	var mcPort uint16
	if status == StatusOK && len(respBody) >= 2 {
		mcPort = binary.BigEndian.Uint16(respBody[:2])
	}
	return negotiatedEasyTierID, mcPort, nil
}

func (s *ScaffoldingService) LeaveRoom() error {
	s.guestMu.Lock()
	if s.guestRunning {
		close(s.guestStopCh)
	}
	manager, mcLocalPort, mcRemoteAddr := s.resetGuestStateLocked("")
	s.guestMu.Unlock()

	s.cleanupGuestPortForwards(manager, mcLocalPort, mcRemoteAddr)
	return nil
}

func (s *ScaffoldingService) cleanupGuestPortForwards(manager *easytier.EasyTierManager, mcLocalPort uint16, mcRemoteAddr string) {
	if manager != nil && mcLocalPort != 0 && mcRemoteAddr != "" {
		localAddr := fmt.Sprintf("0.0.0.0:%d", mcLocalPort)
		manager.RemovePortForward("tcp", localAddr, mcRemoteAddr)
		manager.RemovePortForward("udp", localAddr, mcRemoteAddr)
	}
	if manager != nil {
		manager.Stop()
	}
}

func (s *ScaffoldingService) GetConnectionStatus() (*ConnectionStatus, error) {
	s.guestMu.Lock()
	running := s.guestRunning
	s.guestMu.Unlock()

	if !running {
		s.guestMu.Lock()
		reason := s.guestDisconnectReason
		s.guestMu.Unlock()
		if reason != "" {
			return s.buildConnectionStatus(), nil
		}
		return nil, fmt.Errorf("未连接到任何房间")
	}

	s.refreshGuestPlayerList()

	return s.buildConnectionStatus(), nil
}

func (s *ScaffoldingService) buildConnectionStatus() *ConnectionStatus {
	s.guestMu.Lock()
	defer s.guestMu.Unlock()

	code := ""
	if s.guestRoomCode != nil {
		code = s.guestRoomCode.Format()
	}

	return &ConnectionStatus{
		RoomCode:         code,
		HostAddress:      s.guestMCAddr,
		MCAddress:        s.guestMCAddr,
		MCPort:           s.guestMCPort,
		Connected:        s.guestRunning,
		OnlineCount:      len(s.guestPlayers),
		Players:          s.guestPlayers,
		Heartbeating:     s.guestHeartbeating,
		DisconnectReason: s.guestDisconnectReason,
	}
}

// guestReadLoop runs in a background goroutine. It continuously reads responses
// from the TCP connection (like Rust's ClientSession background thread).
// When the read fails, it closes guestReadCh to signal all waiters.
func (s *ScaffoldingService) guestReadLoop(conn net.Conn) {
	slog.Info("ReadLoop started")
	for {
		status, body, err := ReadProtocolResponse(conn)
		if err != nil {
			slog.Warn("ReadLoop read failed", "error", err)
			select {
			case s.guestReadCh <- readResult{err: err}:
			default:
			}
			close(s.guestReadCh)
			return
		}
		s.guestReadCh <- readResult{status: status, body: body}
	}
}

func (s *ScaffoldingService) writeAndWait(conn net.Conn, typeName string, body []byte) (uint8, []byte, error) {
	s.guestIOMu.Lock()
	if err := WriteProtocolRequest(conn, typeName, body); err != nil {
		s.guestIOMu.Unlock()
		return 0, nil, fmt.Errorf("write %s: %w", typeName, err)
	}
	s.guestIOMu.Unlock()

	select {
	case result, ok := <-s.guestReadCh:
		if !ok {
			return 0, nil, fmt.Errorf("连接已断开")
		}
		return result.status, result.body, result.err
	case <-time.After(5 * time.Second):
		return 0, nil, fmt.Errorf("等待响应超时")
	}
}

func (s *ScaffoldingService) guestHeartbeatLoop(machineID, easytierID, playerName, vendorPrefix string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	pingData, _ := json.Marshal(newGuestPlayerInfo(machineID, playerName, easytierID, vendorPrefix))

	for {
		select {
		case <-ticker.C:
			s.guestMu.Lock()
			conn := s.guestConn
			running := s.guestRunning
			s.guestMu.Unlock()

			if !running || conn == nil {
				return
			}

			status, _, err := s.writeAndWait(conn, ProtocolPlayerPing, pingData)
			if err != nil {
				slog.Warn("Heartbeat failed", "error", err)
				s.autoDisconnect("房主已关闭房间")
				return
			}
			if status != 0 {
				slog.Warn("Heartbeat server error", "status", status)
				s.autoDisconnect("房主已关闭房间")
				return
			}
			s.refreshGuestPlayerList()

		case <-s.guestStopCh:
			return
		}
	}
}

// setupMCPortForward creates an EasyTier port-forward for the MC server port
// so that Minecraft clients on the local machine can connect directly via
// 127.0.0.1:localPort. This is compatible with both GravityCone and Terracotta hosts.
func (s *ScaffoldingService) setupMCPortForward(hostIP string, mcPort uint16) {
	s.guestMu.Lock()
	manager := s.guestManager
	running := s.guestRunning
	s.guestMu.Unlock()

	if !running || manager == nil {
		return
	}

	// TUN 模式（Android VpnService）：MC 客户端直接连 host 虚拟 IP:mcPort，
	// 无需本地转发（动态 AddPortForward 在 FFI 模式下也不可用）。
	if manager.HasTUN() {
		s.guestMu.Lock()
		if s.guestRunning {
			s.guestMCAddr = hostIP
			s.guestMCPort = mcPort
			s.guestMCRemoteAddr = fmt.Sprintf("%s:%d", hostIP, mcPort)
		}
		s.guestMu.Unlock()
		return
	}

	// Try to use the same port as the MC server for convenience
	localListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", mcPort))
	if err != nil {
		localListener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			slog.Warn("分配本地端口失败", "error", err)
			return
		}
	}
	mcLocalPort := uint16(localListener.Addr().(*net.TCPAddr).Port)
	localListener.Close()

	remoteAddr := fmt.Sprintf("%s:%d", hostIP, mcPort)
	localAddr := fmt.Sprintf("0.0.0.0:%d", mcLocalPort)

	if err := manager.AddPortForward("tcp", localAddr, remoteAddr); err != nil {
		slog.Warn("TCP端口转发失败", "error", err)
		return
	}
	// UDP port-forward (for voice chat etc.)
	manager.AddPortForward("udp", localAddr, remoteAddr)

	slog.Info("端口转发已建立", "local", fmt.Sprintf("0.0.0.0:%d", mcLocalPort), "remote", remoteAddr, "mc_port", mcPort)

	s.guestMu.Lock()
	if s.guestRunning {
		s.guestMCAddr = "127.0.0.1"
		s.guestMCPort = mcLocalPort
		s.guestMCLocalPort = mcLocalPort
		s.guestMCRemoteAddr = remoteAddr
		// Start LAN broadcast so other MC clients on the same network can discover this room
		motd := s.guestMotd
		if motd == "" {
			motd = "§6§l双击进入联机房间（请保持GravityCone运行）"
		}
		s.guestFakeServer = lansca.NewFakeServer(mcLocalPort, motd)
	}
	s.guestMu.Unlock()
}

func (s *ScaffoldingService) autoDisconnect(reason string) {
	slog.Info("autoDisconnect", "reason", reason)
	s.guestMu.Lock()
	manager, mcLocalPort, mcRemoteAddr := s.resetGuestStateLocked(reason)
	s.guestMu.Unlock()

	s.eventEmitter.Emit("room.disconnected", map[string]string{"reason": reason})
	s.cleanupGuestPortForwards(manager, mcLocalPort, mcRemoteAddr)
}

func (s *ScaffoldingService) resetGuestStateLocked(reason string) (*easytier.EasyTierManager, uint16, string) {
	if s.guestConn != nil {
		s.guestConn.Close()
		s.guestConn = nil
	}
	s.guestRunning = false
	s.guestHeartbeating = false
	s.guestDisconnectReason = reason
	if s.guestFakeServer != nil {
		s.guestFakeServer.Stop()
		s.guestFakeServer = nil
	}
	manager := s.guestManager
	s.guestManager = nil
	mcLocalPort := s.guestMCLocalPort
	mcRemoteAddr := s.guestMCRemoteAddr

	s.guestPlayers = nil
	s.guestMCAddr = ""
	s.guestMCPort = 0
	s.guestRoomCode = nil
	s.guestPlayerName = ""
	s.guestNegotiatedEasyTierID = false
	s.guestScaffoldingLocalPort = 0
	s.guestDirectLocal = false
	s.guestMCLocalPort = 0
	s.guestMCRemoteAddr = ""
	s.guestMotd = ""

	return manager, mcLocalPort, mcRemoteAddr
}

func (s *ScaffoldingService) refreshGuestPlayerList() {
	s.guestMu.Lock()
	conn := s.guestConn
	running := s.guestRunning
	s.guestMu.Unlock()

	if !running || conn == nil {
		return
	}

	status, body, err := s.writeAndWait(conn, ProtocolPlayerProfilesList, nil)
	if err != nil || status != StatusOK {
		return
	}

	var players []PlayerInfo
	if err := json.Unmarshal(body, &players); err != nil {
		return
	}

	s.guestMu.Lock()
	s.guestPlayers = players
	s.guestMu.Unlock()

	s.eventEmitter.Emit("room.guest_player_list_updated", players)
}
