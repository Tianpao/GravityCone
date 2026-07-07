package core

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const pcHostnamePrefix = "paper-connect-server-"
const pcPlayerTimeout = 10 * time.Second

// PaperConnectRoomStatus is the host-side room status.
type PaperConnectRoomStatus struct {
	Code        string          `json:"code"`
	GamePort    int             `json:"game_port"`
	OnlineCount int             `json:"online_count"`
	Players     []PCPlayerEntry `json:"players"`
	Running     bool            `json:"running"`
}

// PaperConnectConnectionStatus is the guest-side connection status.
type PaperConnectConnectionStatus struct {
	RoomCode         string          `json:"room_code"`
	HostAddress      string          `json:"host_address"`
	GamePort         int             `json:"game_port"`
	Connected        bool            `json:"connected"`
	OnlineCount      int             `json:"online_count"`
	Players          []PCPlayerEntry `json:"players"`
	Heartbeating     bool            `json:"heartbeating"`
	DisconnectReason string          `json:"disconnect_reason"`
}

type PaperConnectService struct {
	eventEmitter EventEmitter

	// HOST state
	hostManager     *EasyTierManager
	hostListener    net.Listener
	hostTCPPort     uint16
	gamePort        uint16
	roomCode        *PaperConnectRoomCode
	hostPlayers     map[string]*PCPlayerEntry // keyed by playerName
	hostPlayerMu    sync.Mutex
	hostStopCh      chan struct{}
	hostRunning     bool
	hostMu          sync.Mutex
	hostPlayerName  string
	hostStopReason  string
	hostConns       map[net.Conn]struct{}
	hostConnMu      sync.Mutex

	// GUEST state
	guestManager          *EasyTierManager
	guestStopCh           chan struct{}
	guestMu               sync.Mutex
	guestRunning          bool
	guestHeartbeating     bool
	guestRoomCode         *PaperConnectRoomCode
	guestPlayerName       string
	guestDisconnectReason string
	guestGamePort         uint16
	guestHostVirtualIP    string
	guestHostTCPPort      uint16
	guestTCPLocalPort     uint16
	guestMCLocalPort      uint16
	guestFakeServer       *FakeServer
	guestPlayers          []PCPlayerEntry

	joinCancelled atomic.Bool
}

func NewPaperConnectService(emitter EventEmitter) *PaperConnectService {
	if emitter == nil {
		emitter = NilEventEmitter{}
	}
	return &PaperConnectService{
		eventEmitter: emitter,
	}
}

func (s *PaperConnectService) setEventEmitter(emitter EventEmitter) {
	if emitter != nil {
		s.eventEmitter = emitter
	}
}

func InitPaperConnectEmitter(svc *PaperConnectService, emitter EventEmitter) {
	svc.setEventEmitter(emitter)
}

// --- HOST methods ---

func (s *PaperConnectService) CreateRoom(playerName string, vendorPrefix string) (*PaperConnectRoomStatus, error) {
	s.hostMu.Lock()
	if s.hostRunning {
		s.hostMu.Unlock()
		return nil, fmt.Errorf("已有房间在运行")
	}
	s.hostMu.Unlock()

	gamePort := uint16(19132)

	// Generate room code
	rc, err := GeneratePaperConnectRoomCode()
	if err != nil {
		return nil, fmt.Errorf("生成房间代码失败: %w", err)
	}

	// Allocate TCP port for PaperConnect control protocol
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("分配TCP端口失败: %w", err)
	}
	tcpPort := uint16(listener.Addr().(*net.TCPAddr).Port)

	if tcpPort <= 1024 || tcpPort > 65535 {
		listener.Close()
		return nil, fmt.Errorf("分配的TCP端口 %d 不合法", tcpPort)
	}

	// Generate ACL config (hostProtocolPort is nil, matching C# behavior)
	aclConfig := BuildPaperConnectACL(true, PCHostVIP, nil)
	if err := WritePaperConnectACL(aclConfig, "./config.toml"); err != nil {
		listener.Close()
		return nil, fmt.Errorf("写入ACL配置失败: %w", err)
	}

	// Start EasyTier
	manager, err := NewEasyTierManager()
	if err != nil {
		listener.Close()
		return nil, err
	}

	hostname := fmt.Sprintf("%s%d", pcHostnamePrefix, tcpPort)
	virtualIP, err := manager.Start(StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		Hostname:      hostname,
		IsHost:        true,
		TCPPort:       tcpPort,
		MCPort:        gamePort,
		ConfigPath:    "./config.toml",
	})
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}

	// Store state
	s.hostMu.Lock()
	s.hostManager = manager
	s.hostListener = listener
	s.hostTCPPort = tcpPort
	s.gamePort = gamePort
	s.roomCode = rc
	s.hostPlayers = make(map[string]*PCPlayerEntry)
	s.hostStopCh = make(chan struct{})
	s.hostRunning = true
	s.hostStopReason = ""
	s.hostPlayerName = playerName
	s.hostConns = make(map[net.Conn]struct{})
	s.hostMu.Unlock()

	// clientId is the vendor prefix identifying the client application (e.g. "GravityCone-1.0.0")
	// Per PaperConnect spec: clientId contains client name and version number.
	clientId := vendorPrefix
	if clientId == "" {
		clientId = "GravityCone-1.0.0"
	}

	// Add HOST as a player
	s.hostPlayerMu.Lock()
	s.hostPlayers[playerName] = &PCPlayerEntry{
		PlayerName: playerName,
		ClientId:   clientId,
		IsRoomHost: true,
		lastHeartbeat: time.Now(),
	}
	s.hostPlayerMu.Unlock()

	go s.pcHostServerLoop()
	go s.pcHostPlayerCleanupLoop()

	return s.pcBuildRoomStatus(virtualIP), nil
}

func (s *PaperConnectService) StopRoom() error {
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

	reason := s.hostStopReason
	if reason == "" {
		reason = "room stopped by host"
	}
	s.eventEmitter.Emit("paperconnect.room.closed", map[string]string{"reason": reason})

	s.hostConnMu.Lock()
	for conn := range s.hostConns {
		conn.Close()
	}
	s.hostConns = nil
	s.hostConnMu.Unlock()

	if s.hostManager != nil {
		s.hostManager.Stop()
	}

	s.hostPlayerMu.Lock()
	s.hostPlayers = nil
	s.hostPlayerMu.Unlock()

	return nil
}

func (s *PaperConnectService) GetRoomStatus() (*PaperConnectRoomStatus, error) {
	s.hostMu.Lock()
	if !s.hostRunning {
		reason := s.hostStopReason
		s.hostMu.Unlock()
		if reason != "" {
			return nil, fmt.Errorf("%s", reason)
		}
		return nil, fmt.Errorf("没有正在运行的房间")
	}
	virtualIP := ""
	if s.hostManager != nil {
		virtualIP = s.hostManager.SelfVirtualIP()
	}
	s.hostMu.Unlock()

	_ = virtualIP
	return s.pcBuildRoomStatus(""), nil
}

func (s *PaperConnectService) pcBuildRoomStatus(virtualIP string) *PaperConnectRoomStatus {
	s.hostPlayerMu.Lock()
	players := make([]PCPlayerEntry, 0, len(s.hostPlayers))
	for _, e := range s.hostPlayers {
		players = append(players, *e)
	}
	s.hostPlayerMu.Unlock()

	code := ""
	if s.roomCode != nil {
		code = s.roomCode.Format()
	}

	return &PaperConnectRoomStatus{
		Code:        code,
		GamePort:    int(s.gamePort),
		OnlineCount: len(players),
		Players:     players,
		Running:     s.hostRunning,
	}
}

func (s *PaperConnectService) pcHostServerLoop() {
	for {
		conn, err := s.hostListener.Accept()
		if err != nil {
			select {
			case <-s.hostStopCh:
				return
			default:
				log.Printf("[PCHostServer] Accept error: %v", err)
				continue
			}
		}
		log.Printf("[PCHostServer] Accepted connection from %s", conn.RemoteAddr())
		go s.pcHandleHostConnection(conn)
	}
}

func (s *PaperConnectService) pcHandleHostConnection(conn net.Conn) {
	s.hostConnMu.Lock()
	if s.hostConns != nil {
		s.hostConns[conn] = struct{}{}
	}
	s.hostConnMu.Unlock()

	defer func() {
		conn.Close()
		s.hostConnMu.Lock()
		if s.hostConns != nil {
			delete(s.hostConns, conn)
		}
		s.hostConnMu.Unlock()
	}()

	for {
		s.hostMu.Lock()
		running := s.hostRunning
		s.hostMu.Unlock()
		if !running {
			return
		}

		namespace, rawJson, err := ReadPCRequest(conn)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}

		switch namespace {
		case PCPing:
			s.pcHandlePing(conn, rawJson)
		case PCPlayer:
			s.pcHandlePlayer(conn, rawJson)
		default:
			WritePCError(conn, fmt.Sprintf("Unknown namespace: %s", namespace))
		}
	}
}

func (s *PaperConnectService) pcHandlePing(conn net.Conn, rawJson []byte) {
	var req PCPingRequest
	if err := json.Unmarshal(rawJson, &req); err != nil {
		WritePCError(conn, "Invalid ping request")
		return
	}

	resp := PCPingResponse{
		Time:             req.Time,
		ReturnTime:       time.Now().UnixMilli(),
		GameType:         "MinecraftBedrock",
		GameProtocolType: "UDP",
		GamePort:         int(s.gamePort),
	}
	WritePCResponse(conn, resp)
}

func (s *PaperConnectService) pcHandlePlayer(conn net.Conn, rawJson []byte) {
	var req PCPlayerRequest
	if err := json.Unmarshal(rawJson, &req); err != nil {
		WritePCError(conn, "Invalid player request")
		return
	}

	if req.PlayerName == "" || req.ClientId == "" {
		WritePCError(conn, "Missing playerName or clientId")
		return
	}

	isNew := false
	s.hostPlayerMu.Lock()
	if _, exists := s.hostPlayers[req.PlayerName]; !exists {
		isNew = true
	}
	s.hostPlayers[req.PlayerName] = &PCPlayerEntry{
		PlayerName:    req.PlayerName,
		ClientId:      req.ClientId,
		IsRoomHost:    false,
		lastHeartbeat: time.Now(),
	}

	// Build response player list (host always included, guests only if heartbeat within timeout)
	activePlayers := make([]PCPlayerEntry, 0)
	for _, p := range s.hostPlayers {
		if p.IsRoomHost || time.Since(p.lastHeartbeat) <= pcPlayerTimeout {
			activePlayers = append(activePlayers, PCPlayerEntry{
				PlayerName: p.PlayerName,
				ClientId:   p.ClientId,
				IsRoomHost: p.IsRoomHost,
			})
		}
	}
	s.hostPlayerMu.Unlock()

	if isNew {
		s.eventEmitter.Emit("paperconnect.room.player_joined", PCPlayerEntry{
			PlayerName: req.PlayerName,
			ClientId:   req.ClientId,
			IsRoomHost: false,
		})
	}

	resp := PCPlayerResponse{
		ReturnTime: time.Now().UnixMilli(),
		Players:    activePlayers,
	}
	WritePCResponse(conn, resp)
}

func (s *PaperConnectService) pcHostPlayerCleanupLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.hostPlayerMu.Lock()
			now := time.Now()
			for name, p := range s.hostPlayers {
				if !p.IsRoomHost && now.Sub(p.lastHeartbeat) > pcPlayerTimeout {
					s.eventEmitter.Emit("paperconnect.room.player_left", *p)
					delete(s.hostPlayers, name)
				}
			}
			s.hostPlayerMu.Unlock()
		case <-s.hostStopCh:
			return
		}
	}
}

// --- GUEST methods ---

func (s *PaperConnectService) CancelJoin() {
	s.joinCancelled.Store(true)
}

func (s *PaperConnectService) JoinRoom(code string, playerName string, vendorPrefix string) (*PaperConnectConnectionStatus, error) {
	s.joinCancelled.Store(false)
	s.guestMu.Lock()
	if s.guestRunning {
		s.guestMu.Unlock()
		return nil, fmt.Errorf("已在一个房间中")
	}
	s.guestMu.Unlock()

	// 1. Parse room code
	rc, err := ParsePaperConnectRoomCode(code)
	if err != nil {
		return nil, err
	}

	// 2. Generate client ACL config
	aclConfig := BuildPaperConnectACL(false, PCHostVIP, nil)
	if err := WritePaperConnectACL(aclConfig, "./config.toml"); err != nil {
		return nil, fmt.Errorf("写入ACL配置失败: %w", err)
	}

	// 3. Start EasyTier (Phase 1: no port forwards yet, just discover the host)
	manager, err := NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	virtualIP, err := manager.Start(StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		IsHost:        false,
		ConfigPath:    "./config.toml",
	})
	if err != nil {
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}
	_ = virtualIP

	if s.joinCancelled.Load() {
		manager.Stop()
		return nil, fmt.Errorf("加入已取消")
	}

	// 4. Discover host peer
	hostname, hostIP, err := s.pcDiscoverHost(manager, 60*time.Second)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("发现主机失败: %w", err)
	}

	// Extract server port from hostname
	serverPortStr := strings.TrimPrefix(hostname, pcHostnamePrefix)
	serverPort, err := strconv.ParseUint(serverPortStr, 10, 16)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("解析主机端口失败: %w", err)
	}

	// 5. Phase 1: TCP-only connection
	// Allocate local port for TCP forwarding to host's control port
	localListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("分配本地端口失败: %w", err)
	}
	tcpLocalPort := uint16(localListener.Addr().(*net.TCPAddr).Port)
	localListener.Close()

	// Stop the initial EasyTier and restart with TCP port-forward
	manager.Stop()

	manager, err = NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	tcpForward := fmt.Sprintf("tcp://0.0.0.0:%d/%s:%d", tcpLocalPort, PCHostVIP, serverPort)
	_, err = manager.Start(StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		IsHost:        false,
		ConfigPath:    "./config.toml",
		PortForwards:  []string{tcpForward},
	})
	if err != nil {
		return nil, fmt.Errorf("启动虚拟网络(TCP)失败: %w", err)
	}

	// Ping the server via TCP to verify connectivity (per-request connection)
	var gamePort uint16
	for attempt := 0; attempt < 30; attempt++ {
		if s.joinCancelled.Load() {
			manager.Stop()
			return nil, fmt.Errorf("加入已取消")
		}

		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", tcpLocalPort), 2*time.Second)
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

		// Read response
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

		gamePort = uint16(pingResp.GamePort)
		break
	}

	if gamePort == 0 {
		gamePort = 19132 // Default Bedrock port
	}

	// 6. Phase 2: TCP+UDP connection
	manager.Stop()

	manager, err = NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	// Allocate local port for UDP game traffic forwarding
	mcListener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", gamePort))
	if err != nil {
		mcListener, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("分配本地MC端口失败: %w", err)
		}
	}
	mcLocalPort := uint16(mcListener.Addr().(*net.TCPAddr).Port)
	mcListener.Close()

	portForwards := []string{
		fmt.Sprintf("tcp://0.0.0.0:%d/%s:%d", tcpLocalPort, PCHostVIP, serverPort),
		fmt.Sprintf("udp://0.0.0.0:%d/%s:%d", mcLocalPort, PCHostVIP, gamePort),
	}

	_, err = manager.Start(StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		IsHost:        false,
		ConfigPath:    "./config.toml",
		PortForwards:  portForwards,
	})
	if err != nil {
		return nil, fmt.Errorf("启动虚拟网络(TCP+UDP)失败: %w", err)
	}

	// Verify Phase 2 connectivity with another ping (per-request connection)
	for attempt := 0; attempt < 30; attempt++ {
		if s.joinCancelled.Load() {
			manager.Stop()
			return nil, fmt.Errorf("加入已取消")
		}

		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", tcpLocalPort), 2*time.Second)
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

		break
	}

	// clientId is the vendor prefix identifying the client application
	clientId := vendorPrefix
	if clientId == "" {
		clientId = "GravityCone-1.0.0"
	}

	playerReq := PCPlayerRequest{
		ClientId:   clientId,
		PlayerName: playerName,
	}

	var playerResp PCPlayerResponse
	for attempt := 0; attempt < 10; attempt++ {
		if s.joinCancelled.Load() {
			manager.Stop()
			return nil, fmt.Errorf("加入已取消")
		}

		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", tcpLocalPort), 3*time.Second)
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}

		if err := WritePCRequest(conn, PCPlayer, playerReq); err != nil {
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

		json.Unmarshal(buf[:n], &playerResp)
		break
	}

	s.guestPlayers = playerResp.Players

	// Store state
	s.guestMu.Lock()
	s.guestManager = manager
	s.guestStopCh = make(chan struct{})
	s.guestRunning = true
	s.guestHeartbeating = true
	s.guestRoomCode = rc
	s.guestPlayerName = playerName
	s.guestGamePort = gamePort
	s.guestHostVirtualIP = hostIP
	s.guestHostTCPPort = uint16(serverPort)
	s.guestTCPLocalPort = tcpLocalPort
	s.guestMCLocalPort = mcLocalPort
	s.guestFakeServer = NewFakeServer(mcLocalPort, "§6§l双击进入基岩版联机房间")
	s.guestMu.Unlock()

	go s.pcGuestHeartbeatLoop(clientId, playerName)

	return s.pcBuildConnectionStatus(), nil
}

func (s *PaperConnectService) pcDiscoverHost(manager *EasyTierManager, timeout time.Duration) (hostname string, virtualIP string, err error) {
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

// pcGuestHeartbeatLoop sends a heartbeat every 5 seconds using per-request TCP connections,
// matching the C# PaperConnectClient pattern where each heartbeat creates a new TcpClient.
func (s *PaperConnectService) pcGuestHeartbeatLoop(clientId string, playerName string) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	log.Printf("[PCHeartbeat] started")

	consecutiveFailures := 0
	const maxFailures = 3

	for {
		select {
		case <-ticker.C:
			s.guestMu.Lock()
			running := s.guestRunning
			tcpLocalPort := s.guestTCPLocalPort
			s.guestMu.Unlock()

			if !running {
				log.Printf("[PCHeartbeat] exiting: not running")
				return
			}

			// Create a new TCP connection for this heartbeat (matching C# pattern)
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", tcpLocalPort), 5*time.Second)
			if err != nil {
				consecutiveFailures++
				log.Printf("[PCHeartbeat] dial failed (%d/%d): %v", consecutiveFailures, maxFailures, err)
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
				log.Printf("[PCHeartbeat] write failed (%d/%d): %v", consecutiveFailures, maxFailures, err)
				if consecutiveFailures >= maxFailures {
					s.pcAutoDisconnect("房主已关闭房间")
					return
				}
				continue
			}

			// Read response
			conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			buf := make([]byte, 4096)
			n, err := conn.Read(buf)
			conn.Close()

			if err != nil {
				consecutiveFailures++
				log.Printf("[PCHeartbeat] read failed (%d/%d): %v", consecutiveFailures, maxFailures, err)
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
			}

		case <-s.guestStopCh:
			log.Printf("[PCHeartbeat] stopCh, exiting")
			return
		}
	}
}

func (s *PaperConnectService) LeaveRoom() error {
	s.guestMu.Lock()
	alreadyStopped := !s.guestRunning
	if !alreadyStopped {
		close(s.guestStopCh)
		s.guestRunning = false
		s.guestHeartbeating = false
		if s.guestFakeServer != nil {
			s.guestFakeServer.Stop()
			s.guestFakeServer = nil
		}
	}
	manager := s.guestManager
	s.guestManager = nil
	s.guestDisconnectReason = ""
	s.guestPlayers = nil
	s.guestGamePort = 0
	s.guestHostVirtualIP = ""
	s.guestHostTCPPort = 0
	s.guestRoomCode = nil
	s.guestPlayerName = ""
	s.guestTCPLocalPort = 0
	s.guestMCLocalPort = 0
	s.guestMu.Unlock()

	if manager != nil {
		manager.Stop()
	}

	return nil
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
		Connected:        s.guestRunning,
		OnlineCount:      len(s.guestPlayers),
		Players:          s.guestPlayers,
		Heartbeating:     s.guestHeartbeating,
		DisconnectReason: s.guestDisconnectReason,
	}
}

func (s *PaperConnectService) pcAutoDisconnect(reason string) {
	log.Printf("[PCAutoDisconnect] reason=%q", reason)
	s.guestMu.Lock()
	s.guestRunning = false
	s.guestHeartbeating = false
	s.guestDisconnectReason = reason
	if s.guestFakeServer != nil {
		s.guestFakeServer.Stop()
		s.guestFakeServer = nil
	}
	manager := s.guestManager
	s.guestManager = nil
	s.guestPlayers = nil
	s.guestHostVirtualIP = ""
	s.guestGamePort = 0
	s.guestHostTCPPort = 0
	s.guestRoomCode = nil
	s.guestPlayerName = ""
	s.guestTCPLocalPort = 0
	s.guestMCLocalPort = 0
	s.guestMu.Unlock()

	s.eventEmitter.Emit("paperconnect.room.disconnected", map[string]string{"reason": reason})

	if manager != nil {
		manager.Stop()
	}
}

func (s *PaperConnectService) Cleanup() {
	s.StopRoom()
	s.LeaveRoom()
}

func (s *PaperConnectService) AddPeers(addrs []string) {
	AddPublicPeers(addrs)
}
