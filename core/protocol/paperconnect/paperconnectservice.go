package paperconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/df-mc/go-nethernet"
	"github.com/df-mc/go-nethernet/discovery"
	raknet "github.com/sandertv/go-raknet"

	"gravitycone/core/easytier"
	"gravitycone/core/protocol/scaffolding"
	"gravitycone/core/utils"
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
	eventEmitter utils.EventEmitter

	// HOST state
	hostManager    *easytier.EasyTierManager
	hostRakLn      *raknet.Listener
	hostTcpLn      net.Listener
	hostTCPPort    uint16
	roomCode       *PaperConnectRoomCode
	hostPlayers    map[string]*PCPlayerEntry
	hostPlayerMu   sync.Mutex
	hostStopCh     chan struct{}
	hostRunning    bool
	hostMu         sync.Mutex
	hostPlayerName string
	hostStopReason string
	hostSessions   chan struct{}
	hostCancelFunc context.CancelFunc

	// GUEST state
	guestManager          *easytier.EasyTierManager
	guestRakConn          *raknet.Conn
	guestDisc             *discovery.Listener
	guestNnLn             *nethernet.Listener
	guestStopCh           chan struct{}
	guestMu               sync.Mutex
	guestRunning          bool
	guestHeartbeating     bool
	guestRoomCode         *PaperConnectRoomCode
	guestPlayerName       string
	guestDisconnectReason string
	guestHostVirtualIP    string
	guestPlayers          []PCPlayerEntry
	guestCancelFunc       context.CancelFunc

	joinCancelled atomic.Bool
}

func NewPaperConnectService(emitter utils.EventEmitter) *PaperConnectService {
	if emitter == nil {
		emitter = utils.NilEventEmitter{}
	}
	return &PaperConnectService{
		eventEmitter: emitter,
	}
}

func (s *PaperConnectService) setEventEmitter(emitter utils.EventEmitter) {
	if emitter != nil {
		s.eventEmitter = emitter
	}
}

func InitPaperConnectEmitter(svc *PaperConnectService, emitter utils.EventEmitter) {
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

	// Generate room code
	rc, err := GeneratePaperConnectRoomCode()
	if err != nil {
		return nil, fmt.Errorf("生成房间代码失败: %w", err)
	}

	// Allocate TCP port for PaperConnect control protocol
	tcpLn, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("分配TCP端口失败: %w", err)
	}
	tcpPort := uint16(tcpLn.Addr().(*net.TCPAddr).Port)

	if tcpPort <= 1024 || tcpPort > 65535 {
		tcpLn.Close()
		return nil, fmt.Errorf("分配的TCP端口 %d 不合法", tcpPort)
	}

	// Generate ACL config
	aclConfig := BuildPaperConnectACL(true, PCHostVIP, &tcpPort)
	if err := WritePaperConnectACL(aclConfig, "./config.toml"); err != nil {
		tcpLn.Close()
		return nil, fmt.Errorf("写入ACL配置失败: %w", err)
	}

	// Start EasyTier
	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		tcpLn.Close()
		return nil, err
	}

	hostname := fmt.Sprintf("%s%d", pcHostnamePrefix, tcpPort)
	virtualIP, err := manager.Start(easytier.StartOptions{
		NetworkName:        rc.EasyTierNetworkName(),
		NetworkSecret:      rc.EasyTierNetworkSecret(),
		Hostname:           hostname,
		IsHost:             true,
		TCPPort:            tcpPort,
		MCPort:             PCRakNetPort,
		ConfigPath:         "./config.toml",
		UpstreamCompatible: true,
	})
	if err != nil {
		tcpLn.Close()
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}

	// Start RakNet listener for game traffic proxy
	rakLn, err := (raknet.ListenConfig{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).Listen(fmt.Sprintf("0.0.0.0:%d", PCRakNetPort))
	if err != nil {
		tcpLn.Close()
		manager.Stop()
		return nil, fmt.Errorf("启动RakNet监听失败: %w", err)
	}

	// Store state
	s.hostMu.Lock()
	s.hostManager = manager
	s.hostRakLn = rakLn
	s.hostTcpLn = tcpLn
	s.hostTCPPort = tcpPort
	s.roomCode = rc
	s.hostPlayers = make(map[string]*PCPlayerEntry)
	s.hostStopCh = make(chan struct{})
	s.hostRunning = true
	s.hostStopReason = ""
	s.hostPlayerName = playerName
	s.hostSessions = make(chan struct{}, maxHostSessions)
	s.hostMu.Unlock()

	clientId := scaffolding.MakeVendor(vendorPrefix)

	// Add HOST as a player
	s.hostPlayerMu.Lock()
	s.hostPlayers[playerName] = &PCPlayerEntry{
		PlayerName:    playerName,
		ClientId:      clientId,
		IsRoomHost:    true,
		lastHeartbeat: time.Now(),
	}
	s.hostPlayerMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	s.hostMu.Lock()
	s.hostCancelFunc = cancel
	s.hostMu.Unlock()

	go s.pcHostRakNetAcceptLoop(ctx)
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
	if s.hostRakLn != nil {
		s.hostRakLn.Close()
	}
	if s.hostTcpLn != nil {
		s.hostTcpLn.Close()
	}
	if s.hostCancelFunc != nil {
		s.hostCancelFunc()
	}
	s.hostRunning = false
	s.hostMu.Unlock()

	reason := s.hostStopReason
	if reason == "" {
		reason = "room stopped by host"
	}
	s.eventEmitter.Emit("paperconnect.room.closed", map[string]string{"reason": reason})

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
		GamePort:    PCRakNetPort,
		OnlineCount: len(players),
		Players:     players,
		Running:     s.hostRunning,
	}
}

func (s *PaperConnectService) pcHostRakNetAcceptLoop(ctx context.Context) {
	var sessionID atomic.Uint64
	for {
		conn, err := s.hostRakLn.Accept()
		if err != nil {
			select {
			case <-s.hostStopCh:
				return
			default:
				slog.Error("RakNet accept error", "err", err)
				continue
			}
		}

		rkConn, ok := conn.(*raknet.Conn)
		if !ok {
			slog.Error("unexpected RakNet connection type", "type", fmt.Sprintf("%T", conn))
			_ = conn.Close()
			continue
		}

		select {
		case s.hostSessions <- struct{}{}:
			id := sessionID.Add(1)
			go func() {
				defer func() { <-s.hostSessions }()
				s.pcHostSession(ctx, slog.With("session", id), rkConn)
			}()
		case <-s.hostStopCh:
			_ = rkConn.Close()
			return
		case <-ctx.Done():
			_ = rkConn.Close()
			return
		}
	}
}

func (s *PaperConnectService) pcHostSession(ctx context.Context, log *slog.Logger, rkConn *raknet.Conn) {
	defer rkConn.Close()
	log.Info("tenant proxy connected", "remote", rkConn.RemoteAddr())

	nnConn, err := dialLocalNetherNet(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Error("failed to dial local Bedrock world", "err", err)
		}
		return
	}
	defer nnConn.Close()
	log.Info("connected to local Bedrock world", "latency", nnConn.Latency())

	proxyPackets(ctx, log, nnConn, rkConn)
}

func (s *PaperConnectService) pcHostServerLoop() {
	for {
		conn, err := s.hostTcpLn.Accept()
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
	defer conn.Close()

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
		GamePort:         PCRakNetPort,
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

	// 3. Start EasyTier (no port forwards — dial directly through TUN)
	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	_, err = manager.Start(easytier.StartOptions{
		NetworkName:        rc.EasyTierNetworkName(),
		NetworkSecret:      rc.EasyTierNetworkSecret(),
		IsHost:             false,
		ConfigPath:         "./config.toml",
		UpstreamCompatible: true,
	})
	if err != nil {
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}

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

	if s.joinCancelled.Load() {
		manager.Stop()
		return nil, fmt.Errorf("加入已取消")
	}

	// 5. Start NetherNet discovery listener for local MC client
	discCfg := discovery.ListenConfig{
		NetworkID: randomID(),
		Log:       slog.Default(),
	}
	disc, err := discCfg.Listen("0.0.0.0:7551")
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("启动NetherNet发现监听失败: %w", err)
	}

	disc.ServerData(&discovery.ServerData{
		ServerName:            "GravityCone Proxy",
		LevelName:             "Join",
		GameType:              discovery.GameTypeSurvival,
		PlayerCount:           0,
		MaxPlayerCount:        20,
		AcceptsOnlineAuth:     true,
		AcceptsSelfSignedAuth: true,
		TransportLayer:        discovery.TransportLayerNetherNet,
		ConnectionType:        4,
	})

	// 6. Start NetherNet listener
	nnCfg := nethernet.ListenConfig{
		AllowAnonymous:    true,
		DisableTrickleICE: true,
	}
	nnLn, err := nnCfg.Listen(disc)
	if err != nil {
		disc.Close()
		manager.Stop()
		return nil, fmt.Errorf("启动NetherNet监听失败: %w", err)
	}
	slog.Info("NetherNet listening for local client", "network_id", disc.NetworkID())

	// 7. Dial host RakNet through TUN
	rakAddr := fmt.Sprintf("%s:%d", hostIP, PCRakNetPort)
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer dialCancel()
	rkConn, err := (raknet.Dialer{
		MaxMTU:   rakNetMTU,
		ErrorLog: slog.Default(),
	}).DialContext(dialCtx, rakAddr)
	if err != nil {
		nnLn.Close()
		disc.Close()
		manager.Stop()
		return nil, fmt.Errorf("连接主机RakNet失败: %w", err)
	}
	defer rkConn.Close()
	slog.Info("connected to host RakNet proxy", "addr", rakAddr)

	// 8. Accept one local NetherNet client connection
	nnConn, err := nnLn.Accept()
	if err != nil {
		rkConn.Close()
		nnLn.Close()
		disc.Close()
		manager.Stop()
		return nil, fmt.Errorf("等待本地MC客户端连接失败: %w", err)
	}
	slog.Info("local MC client connected via NetherNet", "remote", nnConn.RemoteAddr())

	// 9. Start proxy
	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	go proxyPackets(proxyCtx, slog.Default(), nnConn.(*nethernet.Conn), rkConn)

	// 10. Register with host via TCP control protocol
	clientId := scaffolding.MakeVendor(vendorPrefix)
	s.pcGuestRegister(hostIP, uint16(serverPort), clientId, playerName)

	// Store state
	s.guestMu.Lock()
	s.guestManager = manager
	s.guestRakConn = rkConn
	s.guestDisc = disc
	s.guestNnLn = nnLn
	s.guestStopCh = make(chan struct{})
	s.guestRunning = true
	s.guestHeartbeating = true
	s.guestRoomCode = rc
	s.guestPlayerName = playerName
	s.guestHostVirtualIP = hostIP
	s.guestCancelFunc = proxyCancel
	s.guestMu.Unlock()

	go s.pcGuestHeartbeatLoop(clientId, playerName, hostIP, uint16(serverPort))

	return s.pcBuildConnectionStatus(), nil
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

	log.Printf("[PCHeartbeat] started")

	consecutiveFailures := 0
	const maxFailures = 3

	for {
		select {
		case <-ticker.C:
			s.guestMu.Lock()
			running := s.guestRunning
			s.guestMu.Unlock()

			if !running {
				log.Printf("[PCHeartbeat] exiting: not running")
				return
			}

			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", hostIP, tcpPort), 5*time.Second)
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
	}
	if s.guestCancelFunc != nil {
		s.guestCancelFunc()
	}
	if s.guestRakConn != nil {
		s.guestRakConn.Close()
	}
	if s.guestNnLn != nil {
		s.guestNnLn.Close()
	}
	if s.guestDisc != nil {
		s.guestDisc.Close()
	}
	manager := s.guestManager
	s.guestManager = nil
	s.guestRakConn = nil
	s.guestNnLn = nil
	s.guestDisc = nil
	s.guestCancelFunc = nil
	s.guestDisconnectReason = ""
	s.guestPlayers = nil
	s.guestHostVirtualIP = ""
	s.guestRoomCode = nil
	s.guestPlayerName = ""
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
		GamePort:         PCRakNetPort,
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
	if s.guestCancelFunc != nil {
		s.guestCancelFunc()
	}
	if s.guestRakConn != nil {
		s.guestRakConn.Close()
	}
	if s.guestNnLn != nil {
		s.guestNnLn.Close()
	}
	if s.guestDisc != nil {
		s.guestDisc.Close()
	}
	manager := s.guestManager
	s.guestManager = nil
	s.guestRakConn = nil
	s.guestNnLn = nil
	s.guestDisc = nil
	s.guestCancelFunc = nil
	s.guestPlayers = nil
	s.guestHostVirtualIP = ""
	s.guestRoomCode = nil
	s.guestPlayerName = ""
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
	easytier.AddPublicPeers(addrs)
}
