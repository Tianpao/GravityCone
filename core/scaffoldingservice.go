package core

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

type RoomStatus struct {
	Code        string       `json:"code"`
	MCAddress   string       `json:"mc_address"`
	MCPort      uint16       `json:"mc_port"`
	OnlineCount int          `json:"online_count"`
	Players     []PlayerInfo `json:"players"`
	Running     bool         `json:"running"`
}

type ConnectionStatus struct {
	RoomCode         string       `json:"room_code"`
	HostAddress      string       `json:"host_address"`
	MCAddress        string       `json:"mc_address"`
	MCPort           uint16       `json:"mc_port"`
	Connected        bool         `json:"connected"`
	OnlineCount      int          `json:"online_count"`
	Players          []PlayerInfo `json:"players"`
	Heartbeating     bool         `json:"heartbeating"`
	DisconnectReason string       `json:"disconnect_reason"`
}

type playerEntry struct {
	info     *PlayerInfo
	lastSeen time.Time
}

type ScaffoldingService struct {
	// HOST state
	hostManager    *EasyTierManager
	hostListener   net.Listener
	hostTCPPort    uint16
	mcPort         uint16
	roomCode       *RoomCode
	hostPlayers    map[string]*playerEntry
	hostPlayerMu   sync.Mutex
	hostStopCh     chan struct{}
	hostRunning    bool
	hostMu         sync.Mutex
	hostPlayerName string

	// GUEST state
	guestManager       *EasyTierManager
	guestConn          net.Conn
	guestPlayers       []PlayerInfo
	guestStopCh        chan struct{}
	guestMu            sync.Mutex
	guestRunning       bool
	guestMCAddr        string
	guestMCPort        uint16
	guestHeartbeating  bool
	guestRoomCode      *RoomCode
	guestPlayerName    string
	guestNegotiatedEasyTierID   bool
	guestScaffoldingLocalPort   uint16 // local port forwarded to host's scaffolding port
	guestDisconnectReason       string // set when connection is lost (e.g. host closed room)
	guestMCListener             net.Listener // local listener for MC proxy connections
	guestDirectLocal            bool // true when guest and host are on the same machine
}

// --- HOST methods ---

func (s *ScaffoldingService) CreateRoom(mcPort uint16, playerName string) (*RoomStatus, error) {
	s.hostMu.Lock()
	if s.hostRunning {
		s.hostMu.Unlock()
		return nil, fmt.Errorf("已有房间在运行")
	}
	s.hostMu.Unlock()

	// 1. Generate room code
	rc, err := GenerateRoomCode()
	if err != nil {
		return nil, fmt.Errorf("生成房间代码失败: %w", err)
	}

	// 2. Allocate TCP port for Scaffolding protocol
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("分配TCP端口失败: %w", err)
	}
	tcpPort := uint16(listener.Addr().(*net.TCPAddr).Port)

	// Validate: port must be > 1024 and <= 65535
	if tcpPort <= 1024 {
		listener.Close()
		return nil, fmt.Errorf("分配的TCP端口 %d 不合法（需大于1024）", tcpPort)
	}

	// 3. Start EasyTier
	manager, err := NewEasyTierManager()
	if err != nil {
		listener.Close()
		return nil, err
	}

	hostname := fmt.Sprintf("scaffolding-mc-server-%d", tcpPort)
	virtualIP, err := manager.Start(StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		Hostname:      hostname,
		IsHost:        true,
		TCPPort:       tcpPort,
		MCPort:        mcPort,
	})
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}

	// 4. Store state and start TCP server
	s.hostMu.Lock()
	s.hostManager = manager
	s.hostListener = listener
	s.hostTCPPort = tcpPort
	s.mcPort = mcPort
	s.roomCode = rc
	s.hostPlayers = make(map[string]*playerEntry)
	s.hostStopCh = make(chan struct{})
	s.hostRunning = true
	s.hostPlayerName = playerName
	s.hostMu.Unlock()

	// Add HOST as a player
	machineID, _ := GetMachineID()
	s.hostPlayerMu.Lock()
	s.hostPlayers[machineID] = &playerEntry{
		info: &PlayerInfo{
			Name:      playerName,
			MachineID: machineID,
			Vendor:    "gravitycone",
			Kind:      "HOST",
		},
		lastSeen: time.Now(),
	}
	s.hostPlayerMu.Unlock()

	go s.hostServerLoop()
	go s.hostPlayerCleanupLoop()

	return s.buildRoomStatus(virtualIP), nil
}

func (s *ScaffoldingService) StopRoom() error {
	s.hostMu.Lock()
	if !s.hostRunning {
		s.hostMu.Unlock()
		return nil
	}
	close(s.hostStopCh)
	if s.hostListener != nil {
		s.hostListener.Close()
	}
	s.hostRunning = false
	s.hostMu.Unlock()

	if s.hostManager != nil {
		s.hostManager.Stop()
	}

	s.hostPlayerMu.Lock()
	s.hostPlayers = nil
	s.hostPlayerMu.Unlock()

	return nil
}

func (s *ScaffoldingService) GetRoomStatus() (*RoomStatus, error) {
	s.hostMu.Lock()
	if !s.hostRunning {
		s.hostMu.Unlock()
		return nil, fmt.Errorf("没有正在运行的房间")
	}
	virtualIP := ""
	if s.hostManager != nil {
		virtualIP = s.hostManager.SelfVirtualIP()
	}
	s.hostMu.Unlock()

	status := s.buildRoomStatus(virtualIP)
	return status, nil
}

func (s *ScaffoldingService) buildRoomStatus(virtualIP string) *RoomStatus {
	s.hostPlayerMu.Lock()
	players := make([]PlayerInfo, 0, len(s.hostPlayers))
	for _, e := range s.hostPlayers {
		players = append(players, *e.info)
	}
	s.hostPlayerMu.Unlock()

	code := ""
	if s.roomCode != nil {
		code = s.roomCode.Format()
	}

	return &RoomStatus{
		Code:        code,
		MCAddress:   virtualIP,
		MCPort:      s.mcPort,
		OnlineCount: len(players),
		Players:     players,
		Running:     s.hostRunning,
	}
}

const guestTimeout = 15 * time.Second // 3 missed heartbeats (5s each)

func (s *ScaffoldingService) hostPlayerCleanupLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.hostPlayerMu.Lock()
			now := time.Now()
			for id, e := range s.hostPlayers {
				if e.info.Kind == "GUEST" && now.Sub(e.lastSeen) > guestTimeout {
					delete(s.hostPlayers, id)
				}
			}
			s.hostPlayerMu.Unlock()
		case <-s.hostStopCh:
			return
		}
	}
}

func (s *ScaffoldingService) hostServerLoop() {
	for {
		conn, err := s.hostListener.Accept()
		if err != nil {
			select {
			case <-s.hostStopCh:
				return
			default:
				log.Printf("Accept error: %v", err)
				continue
			}
		}
		go s.handleHostConnection(conn)
	}
}

func (s *ScaffoldingService) handleHostConnection(conn net.Conn) {
	defer conn.Close()

	for {
		typeName, body, err := ReadProtocolRequest(conn)
		if err == ErrMCProxyConnection {
			s.handleMCProxyConnection(conn)
			return
		}
		if err != nil {
			return
		}

		switch typeName {
		case ProtocolPing:
			s.handlePing(conn, body)
		case ProtocolProtocols:
			s.handleProtocols(conn, body)
		case ProtocolServerPort:
			s.handleServerPort(conn)
		case ProtocolPlayerPing:
			s.handlePlayerPing(conn, body)
		case ProtocolPlayerProfilesList:
			s.handlePlayerProfilesList(conn)
		default:
			WriteProtocolResponse(conn, StatusUnknownProtocol, nil)
		}
	}
}

func (s *ScaffoldingService) handlePing(conn net.Conn, body []byte) {
	if len(body) > 32 {
		WriteProtocolResponse(conn, StatusBadRequest, nil)
		return
	}
	WriteProtocolResponse(conn, StatusOK, body)
}

func (s *ScaffoldingService) handleProtocols(conn net.Conn, body []byte) {
	clientProtocols := strings.Split(string(body), "\x00")
	clientSet := make(map[string]bool)
	for _, p := range clientProtocols {
		p = strings.TrimSpace(p)
		if p != "" {
			clientSet[p] = true
		}
	}

	serverProtocols := []string{
		ProtocolPing,
		ProtocolProtocols,
		ProtocolServerPort,
		ProtocolPlayerPing,
		ProtocolPlayerProfilesList,
	}

	var common []string
	for _, p := range serverProtocols {
		if clientSet[p] {
			common = append(common, p)
		}
	}
	if clientSet[ProtocolPlayerEasyTierID] {
		common = append(common, ProtocolPlayerEasyTierID)
	}

	WriteProtocolResponse(conn, StatusOK, []byte(strings.Join(common, "\x00")))
}

func (s *ScaffoldingService) handleServerPort(conn net.Conn) {
	if s.mcPort == 0 {
		WriteProtocolResponse(conn, StatusServerNotStarted, nil)
		return
	}
	buf := make([]byte, 2)
	buf[0] = byte(s.mcPort >> 8)
	buf[1] = byte(s.mcPort)
	WriteProtocolResponse(conn, StatusOK, buf)
}

func (s *ScaffoldingService) handlePlayerPing(conn net.Conn, body []byte) {
	var player PlayerInfo
	if err := json.Unmarshal(body, &player); err != nil {
		WriteProtocolResponse(conn, StatusBadRequest, nil)
		return
	}

	s.hostPlayerMu.Lock()
	if player.Kind == "" {
		player.Kind = "GUEST"
	}
	s.hostPlayers[player.MachineID] = &playerEntry{
		info:     &player,
		lastSeen: time.Now(),
	}
	s.hostPlayerMu.Unlock()

	WriteProtocolResponse(conn, StatusOK, nil)
}

func (s *ScaffoldingService) handlePlayerProfilesList(conn net.Conn) {
	s.hostPlayerMu.Lock()
	players := make([]PlayerInfo, 0, len(s.hostPlayers))
	for _, e := range s.hostPlayers {
		players = append(players, *e.info)
	}
	s.hostPlayerMu.Unlock()

	data, err := json.Marshal(players)
	if err != nil {
		WriteProtocolResponse(conn, StatusUnknownError, []byte(err.Error()))
		return
	}
	WriteProtocolResponse(conn, StatusOK, data)
}

// --- GUEST methods ---

func (s *ScaffoldingService) JoinRoom(code string, playerName string) (*ConnectionStatus, error) {
	s.guestMu.Lock()
	if s.guestRunning {
		s.guestMu.Unlock()
		return nil, fmt.Errorf("已在一个房间中")
	}
	s.guestMu.Unlock()

	// 1. Parse room code
	rc, err := ParseRoomCode(code)
	if err != nil {
		return nil, err
	}

	// 2. Start EasyTier
	manager, err := NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	machineID, _ := GetMachineID()
	virtualIP, err := manager.Start(StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		IsHost:        false,
	})
	if err != nil {
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}
	_ = virtualIP // used indirectly via DiscoverPeer

	// 3. Discover HOST and wait for P2P connection
	// The hostname format is scaffolding-mc-server-{port}, scan peers for matching hostname.
	// Retry until we can actually connect via TCP (P2P may take time to establish).
	hostIP, _, err := s.discoverHostAndConnect(manager, 60*time.Second)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("连接主机失败: %w", err)
	}

	// 4. We already have a working TCP connection from discoverHostAndConnect
	conn := s.guestConn

	// 5. Send c:player_ping immediately
	easytierID := ""
	if peerID, err := manager.GetPeerID(); err == nil {
		easytierID = peerID
	}

	pingData, _ := json.Marshal(PlayerInfo{
		Name:       playerName,
		MachineID:  machineID,
		EasyTierID: easytierID,
		Vendor:     "gravitycone",
		Kind:       "GUEST",
	})
	if err := WriteProtocolRequest(conn, ProtocolPlayerPing, pingData); err != nil {
		conn.Close()
		manager.Stop()
		return nil, fmt.Errorf("发送心跳失败: %w", err)
	}
	if _, _, err := ReadProtocolResponse(conn); err != nil {
		conn.Close()
		manager.Stop()
		return nil, fmt.Errorf("心跳响应失败: %w", err)
	}

	// 6. Protocol negotiation
	supportedProtocols := strings.Join([]string{
		ProtocolPing,
		ProtocolProtocols,
		ProtocolServerPort,
		ProtocolPlayerPing,
		ProtocolPlayerProfilesList,
		ProtocolPlayerEasyTierID,
	}, "\x00")
	if err := WriteProtocolRequest(conn, ProtocolProtocols, []byte(supportedProtocols)); err != nil {
		conn.Close()
		manager.Stop()
		return nil, fmt.Errorf("协议协商失败: %w", err)
	}
	status, respBody, err := ReadProtocolResponse(conn)
	if err != nil || status != StatusOK {
		conn.Close()
		manager.Stop()
		return nil, fmt.Errorf("协议协商失败")
	}
	negotiated := strings.Split(string(respBody), "\x00")
	negotiatedEasyTierID := false
	for _, p := range negotiated {
		if p == ProtocolPlayerEasyTierID {
			negotiatedEasyTierID = true
		}
	}

	// 7. Get MC server port
	if err := WriteProtocolRequest(conn, ProtocolServerPort, nil); err != nil {
		conn.Close()
		manager.Stop()
		return nil, fmt.Errorf("获取服务器端口失败: %w", err)
	}
	status, respBody, err = ReadProtocolResponse(conn)
	if err != nil {
		conn.Close()
		manager.Stop()
		return nil, fmt.Errorf("获取服务器端口失败: %w", err)
	}
	if status == StatusServerNotStarted {
		// Server not started yet, we'll keep trying later
	} else if status != StatusOK || len(respBody) < 2 {
		conn.Close()
		manager.Stop()
		return nil, fmt.Errorf("获取服务器端口失败: 状态=%d", status)
	}

	var mcPort uint16
	if status == StatusOK && len(respBody) >= 2 {
		mcPort = uint16(respBody[0])<<8 | uint16(respBody[1])
	}

	// 8. Store state and start heartbeat (MC port-forward is set up asynchronously)
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
	s.guestMu.Unlock()

	// Set up MC local proxy listener (uses scaffolding port-forward, no new rule)
	if mcPort != 0 {
		go s.setupMCLocalListener(hostIP, mcPort)
	}

	go s.guestHeartbeatLoop(machineID, easytierID, playerName)

	return s.buildConnectionStatus(), nil
}

func (s *ScaffoldingService) discoverHostAndConnect(manager *EasyTierManager, timeout time.Duration) (string, uint16, error) {
	deadline := time.Now().Add(timeout)
	prefix := "scaffolding-mc-server-"

	var lastErr error
	var prevForwardProto string
	var prevForwardLocal string
	var prevForwardRemote string

	for time.Now().Before(deadline) {
		// Check if process exited
		if !manager.IsRunning() {
			return "", 0, fmt.Errorf("easytier-core 进程已退出")
		}

		// Find HOST by scanning peers
		hostIP, scaffoldingPort, err := findHostPeer(manager, prefix)
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}

		// Try direct localhost first (same-machine shortcut, bypasses P2P).
		directConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", scaffoldingPort), 2*time.Second)
		if err == nil {
			if WriteProtocolRequest(directConn, ProtocolPing, nil) == nil {
				if _, _, err := ReadProtocolResponse(directConn); err == nil {
					s.guestMu.Lock()
					s.guestConn = directConn
					s.guestScaffoldingLocalPort = scaffoldingPort
					s.guestDirectLocal = true
					s.guestMu.Unlock()
					return hostIP, scaffoldingPort, nil
				}
			}
			directConn.Close()
		}

		// Direct failed, fall back to EasyTier port-forward.
		// Allocate a local port for port-forwarding to the host's scaffolding port
		localListener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", 0, fmt.Errorf("分配本地端口失败: %w", err)
		}
		localPort := uint16(localListener.Addr().(*net.TCPAddr).Port)
		localListener.Close()

		// Remove previous failed port-forward before adding a new one.
		if prevForwardProto != "" {
			manager.RemovePortForward(prevForwardProto, prevForwardLocal, prevForwardRemote)
			prevForwardProto = ""
		}

		// Set up port-forward: local 0.0.0.0:localPort -> virtualIP:scaffoldingPort
		if err := manager.AddPortForward("tcp",
			fmt.Sprintf("0.0.0.0:%d", localPort),
			fmt.Sprintf("%s:%d", hostIP, scaffoldingPort),
		); err != nil {
			return "", 0, fmt.Errorf("添加脚手架端口转发失败: %w", err)
		}

		// Connect via localhost through the port-forward
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", localPort), 5*time.Second)
		if err != nil {
			lastErr = fmt.Errorf("TCP连接失败 (127.0.0.1:%d -> %s:%d): %w", localPort, hostIP, scaffoldingPort, err)
			prevForwardProto = "tcp"
			prevForwardLocal = fmt.Sprintf("0.0.0.0:%d", localPort)
			prevForwardRemote = fmt.Sprintf("%s:%d", hostIP, scaffoldingPort)
			time.Sleep(2 * time.Second)
			continue
		}

		// Verify the connection is actually usable. TCP handshake can succeed
		// even when the underlying P2P tunnel isn't ready yet.
		if err := WriteProtocolRequest(conn, ProtocolPing, nil); err != nil {
			conn.Close()
			lastErr = fmt.Errorf("P2P隧道验证失败: %w", err)
			prevForwardProto = "tcp"
			prevForwardLocal = fmt.Sprintf("0.0.0.0:%d", localPort)
			prevForwardRemote = fmt.Sprintf("%s:%d", hostIP, scaffoldingPort)
			time.Sleep(2 * time.Second)
			continue
		}
		if _, _, err := ReadProtocolResponse(conn); err != nil {
			conn.Close()
			lastErr = fmt.Errorf("P2P隧道验证失败: %w", err)
			prevForwardProto = "tcp"
			prevForwardLocal = fmt.Sprintf("0.0.0.0:%d", localPort)
			prevForwardRemote = fmt.Sprintf("%s:%d", hostIP, scaffoldingPort)
			time.Sleep(2 * time.Second)
			continue
		}

		// Connected and verified! Clean up any previously-failed forward.
		if prevForwardProto != "" {
			manager.RemovePortForward(prevForwardProto, prevForwardLocal, prevForwardRemote)
		}

		s.guestMu.Lock()
		s.guestConn = conn
		s.guestScaffoldingLocalPort = localPort
		s.guestMu.Unlock()
		return hostIP, scaffoldingPort, nil
	}

	return "", 0, lastErr
}

func findHostPeer(manager *EasyTierManager, prefix string) (string, uint16, error) {
	out, err := manager.runCli("-o", "json", "-p", manager.RPCPortal(), "peer", "list")
	if err != nil {
		return "", 0, fmt.Errorf("查询对等节点失败: %w", err)
	}

	var peers []peerInfo
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		return "", 0, fmt.Errorf("解析对等节点列表失败: %w", err)
	}

	for _, p := range peers {
		if !strings.HasPrefix(p.Hostname, prefix) || p.VirtualIP == "" {
			continue
		}
		portStr := p.Hostname[len(prefix):]
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil || port <= 1024 || port > 65535 {
			continue
		}
		return p.VirtualIP, uint16(port), nil
	}

	return "", 0, fmt.Errorf("未找到联机中心，请确认房间代码正确且房主已开启房间")
}

func (s *ScaffoldingService) LeaveRoom() error {
	s.guestMu.Lock()
	alreadyStopped := !s.guestRunning
	if !alreadyStopped {
		close(s.guestStopCh)
		s.guestRunning = false
		s.guestHeartbeating = false
		if s.guestConn != nil {
			s.guestConn.Close()
			s.guestConn = nil
		}
		if s.guestMCListener != nil {
			s.guestMCListener.Close()
			s.guestMCListener = nil
		}
	}
	manager := s.guestManager
	s.guestManager = nil

	// Reset all guest state so a future JoinRoom starts from a clean slate.
	s.guestDisconnectReason = ""
	s.guestPlayers = nil
	s.guestMCAddr = ""
	s.guestMCPort = 0
	s.guestRoomCode = nil
	s.guestPlayerName = ""
	s.guestNegotiatedEasyTierID = false
	s.guestScaffoldingLocalPort = 0
	s.guestDirectLocal = false
	s.guestMu.Unlock()

	if manager != nil {
		manager.Stop()
	}

	return nil
}

func (s *ScaffoldingService) GetConnectionStatus() (*ConnectionStatus, error) {
	s.guestMu.Lock()
	if !s.guestRunning {
		reason := s.guestDisconnectReason
		s.guestMu.Unlock()
		if reason != "" {
			return s.buildConnectionStatus(), nil
		}
		return nil, fmt.Errorf("未连接到任何房间")
	}
	s.guestMu.Unlock()

	// Try to refresh player list
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

func (s *ScaffoldingService) guestHeartbeatLoop(machineID, easytierID, playerName string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

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

			pingData, _ := json.Marshal(PlayerInfo{
				Name:       playerName,
				MachineID:  machineID,
				EasyTierID: easytierID,
				Vendor:     "gravitycone",
				Kind:       "GUEST",
			})

			if err := WriteProtocolRequest(conn, ProtocolPlayerPing, pingData); err != nil {
				s.autoDisconnect("房主已关闭房间")
				return
			}
			if _, _, err := ReadProtocolResponse(conn); err != nil {
				s.autoDisconnect("房主已关闭房间")
				return
			}

		case <-s.guestStopCh:
			return
		}
	}
}

// setupMCLocalListener creates a local TCP listener for Minecraft connections.
// MC traffic is tunneled through the EXISTING scaffolding port-forward (no new EasyTier rule),
// so it does not trigger the SOCKS5 port-forward bug.
func (s *ScaffoldingService) setupMCLocalListener(hostIP string, mcPort uint16) {
	mcListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", mcPort))
	if err != nil {
		mcListener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			log.Printf("创建MC本地监听失败: %v", err)
			return
		}
	}
	mcLocalPort := uint16(mcListener.Addr().(*net.TCPAddr).Port)

	s.guestMu.Lock()
	if s.guestRunning {
		s.guestMCAddr = "127.0.0.1"
		s.guestMCPort = mcLocalPort
		s.guestMCListener = mcListener
	} else {
		mcListener.Close()
		s.guestMu.Unlock()
		return
	}
	s.guestMu.Unlock()

	for {
		clientConn, err := mcListener.Accept()
		if err != nil {
			return
		}
		go s.proxyMCToHost(clientConn, mcPort)
	}
}

// proxyMCToHost proxies a single Minecraft client connection to the host
// through the scaffolding port-forward. It opens a new TCP connection
// through the existing port-forward and sends a 0x00 marker byte
// to indicate MC proxy mode to the host.
func (s *ScaffoldingService) proxyMCToHost(clientConn net.Conn, mcPort uint16) {
	defer clientConn.Close()

	s.guestMu.Lock()
	scLocalPort := s.guestScaffoldingLocalPort
	s.guestMu.Unlock()

	proxyConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", scLocalPort), 5*time.Second)
	if err != nil {
		log.Printf("MC代理连接失败: %v", err)
		return
	}
	defer proxyConn.Close()

	// Send 0x00 marker to indicate MC proxy mode
	if _, err := proxyConn.Write([]byte{0}); err != nil {
		return
	}

	// Read status byte from host
	status := make([]byte, 1)
	if _, err := io.ReadFull(proxyConn, status); err != nil {
		return
	}
	if status[0] != 0 {
		log.Printf("MC代理连接被拒: status=%d", status[0])
		return
	}

	// Bridge data bidirectionally
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(proxyConn, clientConn)
		proxyConn.Close()
		done <- struct{}{}
	}()
	go func() {
		io.Copy(clientConn, proxyConn)
		clientConn.Close()
		done <- struct{}{}
	}()
	<-done
}

// handleMCProxyConnection bridges an incoming MC proxy connection
// (identified by the 0x00 first byte) to the local Minecraft server.
func (s *ScaffoldingService) handleMCProxyConnection(conn net.Conn) {
	defer conn.Close()

	mcConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", s.mcPort), 5*time.Second)
	if err != nil {
		conn.Write([]byte{1}) // Status: failure
		return
	}
	defer mcConn.Close()

	// Send success status
	if _, err := conn.Write([]byte{0}); err != nil {
		return
	}

	// Bridge data bidirectionally
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(mcConn, conn)
		mcConn.Close()
		done <- struct{}{}
	}()
	go func() {
		io.Copy(conn, mcConn)
		conn.Close()
		done <- struct{}{}
	}()
	<-done
}

func (s *ScaffoldingService) autoDisconnect(reason string) {
	s.guestMu.Lock()
	if s.guestConn != nil {
		s.guestConn.Close()
		s.guestConn = nil
	}
	s.guestRunning = false
	s.guestHeartbeating = false
	s.guestDisconnectReason = reason
	if s.guestMCListener != nil {
		s.guestMCListener.Close()
		s.guestMCListener = nil
	}
	manager := s.guestManager
	s.guestManager = nil
	// Reset remaining guest state to allow a clean re-join.
	s.guestPlayers = nil
	s.guestMCAddr = ""
	s.guestMCPort = 0
	s.guestRoomCode = nil
	s.guestPlayerName = ""
	s.guestNegotiatedEasyTierID = false
	s.guestScaffoldingLocalPort = 0
	s.guestDirectLocal = false
	s.guestMu.Unlock()

	if manager != nil {
		manager.Stop()
	}
}

func (s *ScaffoldingService) refreshGuestPlayerList() {
	s.guestMu.Lock()
	conn := s.guestConn
	running := s.guestRunning
	s.guestMu.Unlock()

	if !running || conn == nil {
		return
	}

	if err := WriteProtocolRequest(conn, ProtocolPlayerProfilesList, nil); err != nil {
		return
	}
	status, body, err := ReadProtocolResponse(conn)
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
}

// Cleanup stops any running room or connection (called on app shutdown)
func (s *ScaffoldingService) Cleanup() {
	s.StopRoom()
	s.LeaveRoom()
}
