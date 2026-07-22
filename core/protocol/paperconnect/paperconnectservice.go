package paperconnect

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net"
	"strconv"
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

const pcHostnamePrefix = "pcs-"
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
	hostProtocol   string           // ProtocolNetherNet or ProtocolRakNet
	hostGamePort   uint16           // RakNet listener port (NetherNet) or scanned MC port (RakNet)
	hostRakNetInfo *RakNetServerInfo // Server info from RakNet scan (for guest broadcast)

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
	guestTCPLocalPort     uint16
	guestPlayers          []PCPlayerEntry
	guestCancelFunc       context.CancelFunc
	guestProtocol         string // ProtocolNetherNet or ProtocolRakNet
	guestGamePort         uint16
	guestRakNetFakeStop   chan struct{} // Stop channel for fake RakNet broadcaster

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

	// Detect protocol: scan both NetherNet and RakNet LAN lists.
	ctx, cancelScan := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancelScan()

	var nnFound, rkFound bool
	var rakNetInfo *RakNetServerInfo

	nnCh := make(chan bool, 1)
	rkCh := make(chan *RakNetServerInfo, 1)

	go func() { nnCh <- detectNetherNet(ctx) }()
	go func() {
		if info, err := scanRakNetLAN(ctx, 5*time.Second); err == nil {
			rkCh <- info
		} else {
			rkCh <- nil
		}
	}()

	nnFound = <-nnCh
	rakNetInfo = <-rkCh
	rkFound = rakNetInfo != nil

	if !nnFound && !rkFound {
		return nil, fmt.Errorf("未检测到本地Minecraft基岩版房间，请先在Minecraft中开启局域网游戏")
	}

	protocol := ProtocolNetherNet
	gamePort := uint16(0)
	if rkFound && !nnFound {
		protocol = ProtocolRakNet
		gamePort = rakNetInfo.GamePort
	}
	// If both found, prefer NetherNet (newer version)

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

	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		tcpLn.Close()
		return nil, err
	}

	var hostname string
	var startOpts easytier.StartOptions
	var virtualIP string
	var rakLn *raknet.Listener

	if protocol == ProtocolRakNet {
		hostname = buildHostnameRakNet(tcpPort, gamePort)
		startOpts = easytier.StartOptions{
			NetworkName:        rc.EasyTierNetworkName(),
			NetworkSecret:      rc.EasyTierNetworkSecret(),
			Hostname:           hostname,
			IsHost:             true,
			TCPPort:            tcpPort,
			MCPort:             gamePort,
			UpstreamCompatible: true,
		}
		virtualIP, err = manager.Start(startOpts)
		if err != nil {
			tcpLn.Close()
			return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
		}
	} else {
		// NetherNet: start RakNet listener first (random port), then encode its port.
		rakLn, err = (raknet.ListenConfig{
			MaxMTU:        rakNetMTU,
			ErrorLog:      slog.Default(),
			BlockDuration: -1,
		}).Listen(":0")
		if err != nil {
			tcpLn.Close()
			return nil, fmt.Errorf("启动RakNet监听失败: %w", err)
		}
		_, portStr, _ := net.SplitHostPort(rakLn.Addr().String())
		rakPort, _ := strconv.ParseUint(portStr, 10, 16)
		gamePort = uint16(rakPort)

		hostname = buildHostname(tcpPort, gamePort)
		startOpts = easytier.StartOptions{
			NetworkName:        rc.EasyTierNetworkName(),
			NetworkSecret:      rc.EasyTierNetworkSecret(),
			Hostname:           hostname,
			IsHost:             true,
			TCPPort:            tcpPort,
			MCPort:             gamePort,
			UpstreamCompatible: true,
		}

		virtualIP, err = manager.Start(startOpts)
		if err != nil {
			rakLn.Close()
			tcpLn.Close()
			return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
		}
	}

	// Store state
	s.hostMu.Lock()
	s.hostManager = manager
	if rakLn != nil {
		s.hostRakLn = rakLn
	}
	s.hostTcpLn = tcpLn
	s.hostTCPPort = tcpPort
	s.roomCode = rc
	s.hostPlayers = make(map[string]*PCPlayerEntry)
	s.hostStopCh = make(chan struct{})
	s.hostRunning = true
	s.hostStopReason = ""
	s.hostPlayerName = playerName
	s.hostSessions = make(chan struct{}, maxHostSessions)
	s.hostProtocol = protocol
	s.hostGamePort = gamePort
	s.hostRakNetInfo = rakNetInfo
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

	hostCtx, cancel := context.WithCancel(context.Background())
	s.hostMu.Lock()
	s.hostCancelFunc = cancel
	s.hostMu.Unlock()

	if protocol == ProtocolNetherNet {
		go s.pcHostRakNetAcceptLoop(hostCtx)
	}
	go s.pcHostServerLoop()
	go s.pcHostPlayerCleanupLoop()

	slog.Info("PaperConnect room created", "protocol", protocol, "gamePort", gamePort, "tcpPort", tcpPort, "hostname", hostname)
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
		GamePort:    int(s.hostGamePort),
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

		slog.Info("RakNet game connection accepted", "remote", rkConn.RemoteAddr())
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
		GamePort:         int(s.hostGamePort),
		Protocol:         s.hostProtocol,
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

	rc, err := ParsePaperConnectRoomCode(code)
	if err != nil {
		return nil, err
	}

	// Phase 1: start EasyTier without port forwards to discover host.
	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	_, err = manager.Start(easytier.StartOptions{
		NetworkName:        rc.EasyTierNetworkName(),
		NetworkSecret:      rc.EasyTierNetworkSecret(),
		IsHost:             false,
		UpstreamCompatible: true,
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
	slog.Info("hostname parsed", "hostname", hostname, "protocol", protocol, "tcpPort", serverPort, "gamePort", gamePort)

	if s.joinCancelled.Load() {
		manager.Stop()
		return nil, fmt.Errorf("加入已取消")
	}

	// Allocate local ports for port forwarding.
	tcpLocalLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("分配本地TCP端口失败: %w", err)
	}
	tcpLocalPort := uint16(tcpLocalLn.Addr().(*net.TCPAddr).Port)
	tcpLocalLn.Close()

	// Allocate UDP port for game data forwarding.
	rakLocalConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("分配本地UDP端口失败: %w", err)
	}
	rakLocalPort := uint16(rakLocalConn.LocalAddr().(*net.UDPAddr).Port)
	rakLocalConn.Close()

	// Phase 2: restart EasyTier with port forwards (TCP for control only, UDP added at runtime).
	manager.Stop()
	manager, err = easytier.NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	portForwards := []string{
		fmt.Sprintf("tcp://127.0.0.1:%d/%s:%d", tcpLocalPort, hostIP, serverPort),
	}

	_, err = manager.Start(easytier.StartOptions{
		NetworkName:        rc.EasyTierNetworkName(),
		NetworkSecret:      rc.EasyTierNetworkSecret(),
		IsHost:             false,
		PortForwards:       portForwards,
		UpstreamCompatible: true,
	})
	if err != nil {
		return nil, fmt.Errorf("启动虚拟网络(端口转发)失败: %w", err)
	}

	// Wait for TCP ping to succeed.
	var pingOk bool
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

	clientId := scaffolding.MakeVendor(vendorPrefix)

	s.pcGuestRegister("127.0.0.1", tcpLocalPort, clientId, playerName)

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
	s.guestMu.Unlock()

	go s.pcGuestHeartbeatLoop(clientId, playerName, "127.0.0.1", tcpLocalPort)
	go s.pcGuestSetupConnection(manager, clientId, playerName, protocol, rakLocalPort)

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
	if s.guestRakNetFakeStop != nil {
		close(s.guestRakNetFakeStop)
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
	s.guestRakNetFakeStop = nil
	s.guestDisconnectReason = ""
	s.guestPlayers = nil
	s.guestHostVirtualIP = ""
	s.guestTCPLocalPort = 0
	s.guestRoomCode = nil
	s.guestPlayerName = ""
	s.guestProtocol = ""
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

// pcGuestSetupConnection sets up the NetherNet or RakNet game connection asynchronously.
func (s *PaperConnectService) pcGuestSetupConnection(manager *easytier.EasyTierManager, clientId string, playerName string, protocol string, rakLocalPort uint16) {
	var rkConn *raknet.Conn
	var disc *discovery.Listener
	var nnLn *nethernet.Listener
	var proxyCancel context.CancelFunc
	var rakNetFakeStop chan struct{}

	defer func() {
		s.guestMu.Lock()
		s.guestRakConn = rkConn
		s.guestDisc = disc
		s.guestNnLn = nnLn
		s.guestCancelFunc = proxyCancel
		s.guestRakNetFakeStop = rakNetFakeStop
		s.guestMu.Unlock()

		if rkConn != nil || nnLn != nil || disc != nil || rakNetFakeStop != nil {
			slog.Info("PaperConnect game connection ready", "protocol", protocol)
			s.eventEmitter.Emit("paperconnect.connection.ready", map[string]string{"protocol": protocol})
		} else {
			slog.Warn("PaperConnect game connection setup failed, only control channel active", "protocol", protocol)
			s.eventEmitter.Emit("paperconnect.connection.error", map[string]string{"message": "游戏连接建立失败，仅控制通道可用"})
		}
	}()

	if s.joinCancelled.Load() {
		return
	}

	if protocol == ProtocolNetherNet {
		// ---- NetherNet path ----
		// 1. Add UDP port forward + dial RakNet FIRST (before broadcasting NetherNet).
		s.guestMu.Lock()
		hostIP := s.guestHostVirtualIP
		gamePort := s.guestGamePort
		s.guestMu.Unlock()
		slog.Info("pcGuestSetupConnection adding forward", "hostIP", hostIP, "gamePort", gamePort, "rakLocalPort", rakLocalPort)

		localAddr := fmt.Sprintf("127.0.0.1:%d", rakLocalPort)
		remoteAddr := fmt.Sprintf("%s:%d", hostIP, gamePort)
		if err := manager.AddPortForward("udp", localAddr, remoteAddr); err != nil {
			slog.Error("add UDP port forward failed", "err", err, "local", localAddr, "remote", remoteAddr)
			return
		}
		slog.Info("UDP port forward added", "local", localAddr, "remote", remoteAddr)

		rakAddr := fmt.Sprintf("127.0.0.1:%d", rakLocalPort)
		dialCtx, dialCancel := context.WithTimeout(context.Background(), 30*time.Second)
		rkConn, err := (raknet.Dialer{
			MaxMTU:   rakNetMTU,
			ErrorLog: slog.Default(),
		}).DialContext(dialCtx, rakAddr)
		dialCancel()
		if err != nil {
			slog.Error("RakNet dial to host failed", "err", err)
			return
		}
		slog.Info("connected to host RakNet proxy", "addr", rakAddr)

		if s.joinCancelled.Load() {
			return
		}

		// 2. Now start NetherNet discovery + listener (RakNet tunnel is ready).
		discCfg := discovery.ListenConfig{
			NetworkID: randomID(),
			Log:       slog.Default(),
		}
		disc, err = discCfg.Listen("0.0.0.0:7551")
		if err != nil {
			slog.Error("NetherNet discovery listen failed", "err", err)
			return
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

		nnCfg := nethernet.ListenConfig{
			AllowAnonymous:    true,
			DisableTrickleICE: true,
		}
		nnLn, err = nnCfg.Listen(disc)
		if err != nil {
			disc.Close()
			disc = nil
			slog.Error("NetherNet listen failed", "err", err)
			return
		}
		slog.Info("NetherNet listening for local client", "network_id", disc.NetworkID())

		if s.joinCancelled.Load() {
			return
		}

		// 3. Wait for local MC client to connect via NetherNet.
		nnConn, err := nnLn.Accept()
		if err != nil {
			rkConn.Close()
			rkConn = nil
			nnLn.Close()
			nnLn = nil
			disc.Close()
			disc = nil
			slog.Error("NetherNet accept failed", "err", err)
			return
		}
		slog.Info("local MC client connected via NetherNet", "remote", nnConn.RemoteAddr())

		proxyCtx, pc := context.WithCancel(context.Background())
		proxyCancel = pc
		go proxyPackets(proxyCtx, slog.Default(), nnConn.(*nethernet.Conn), rkConn)
	} else {
		// ---- RakNet path ----
		rakNetFakeStop = make(chan struct{})

		serverName := "GravityCone Proxy"
		if playerName != "" {
			serverName = playerName
		}

		go broadcastRakNetFakeServer(context.Background(), rakNetFakeStop, serverName, rakLocalPort)
		slog.Info("RakNet fake server broadcasting", "proxyPort", rakLocalPort, "serverName", serverName)
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
	if s.guestRakNetFakeStop != nil {
		close(s.guestRakNetFakeStop)
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
	s.guestRakNetFakeStop = nil
	s.guestPlayers = nil
	s.guestHostVirtualIP = ""
	s.guestTCPLocalPort = 0
	s.guestRoomCode = nil
	s.guestPlayerName = ""
	s.guestProtocol = ""
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
