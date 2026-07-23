package scaffolding

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mcstatus "github.com/andre-carbajal/go-mcstatus"

	"gravitycone/core/easytier"
	"gravitycone/core/minecraft"
	"gravitycone/core/utils"
)

// BaseVendor is the default vendor suffix. Call MakeVendor to append optional prefixes.
const BaseVendor = "GVC v0.1.0, EasyTier " + easytier.EasyTierVersion

var scaffoldingBuiltinPeers = []string{
	"https://etnode.zkitefly.eu.org/node1",
	"wss://center.node.1tmc.top",
}

func MakeVendor(prefixes ...string) string {
	parts := make([]string, 0, len(prefixes)+1)
	for _, p := range prefixes {
		if p != "" {
			parts = append(parts, p)
		}
	}
	parts = append(parts, BaseVendor)
	return strings.Join(parts, ", ")
}

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

func NewScaffoldingService(emitter utils.EventEmitter) *ScaffoldingService {
	if emitter == nil {
		emitter = utils.NilEventEmitter{}
	}
	return &ScaffoldingService{
		eventEmitter: emitter,
	}
}

// SetEventEmitter replaces the event emitter. Used by Wails to inject
// the app emitter after the service is created and registered.
// Not exported to avoid Wails binding generation.
func (s *ScaffoldingService) setEventEmitter(emitter utils.EventEmitter) {
	if emitter != nil {
		s.eventEmitter = emitter
	}
}

// InitScaffoldingEmitter sets the event emitter on a ScaffoldingService.
// This is a package-level helper so main.go can call it without the method
// appearing in Wails bindings.
func InitScaffoldingEmitter(svc *ScaffoldingService, emitter utils.EventEmitter) {
	svc.setEventEmitter(emitter)
}

type ScaffoldingService struct {
	eventEmitter    utils.EventEmitter
	joinProgressCb  func(string) // set by CLI mode for progress notifications
	peersMu         sync.RWMutex
	peersOverride   []string
	additionalPeers []string
	settingsSvc     *easytier.SettingsService

	// HOST state
	hostManager    *easytier.EasyTierManager
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
	hostStopReason string                // reason the room was auto-stopped (e.g. MC server gone)
	hostConns      map[net.Conn]struct{} // track active connections for shutdown
	hostConnMu     sync.Mutex

	// GUEST state
	guestManager              *easytier.EasyTierManager
	guestConn                 net.Conn
	guestPlayers              []PlayerInfo
	guestStopCh               chan struct{}
	guestMu                   sync.Mutex
	guestRunning              bool
	guestMCAddr               string
	guestMCPort               uint16
	guestHeartbeating         bool
	guestRoomCode             *RoomCode
	guestPlayerName           string
	guestNegotiatedEasyTierID bool
	guestScaffoldingLocalPort uint16                // local port forwarded to host's scaffolding port
	guestDisconnectReason     string                // set when connection is lost (e.g. host closed room)
	guestDirectLocal          bool                  // true when guest and host are on the same machine
	guestIOMu                 sync.Mutex            // serializes writes on guestConn
	guestReadCh               chan readResult       // background reader delivers responses here
	guestFakeServer           *minecraft.FakeServer // LAN broadcaster for Minecraft discovery
	guestMCLocalPort          uint16                // local port forwarded to host's MC server via EasyTier
	guestMCRemoteAddr         string                // remote addr for port-forward cleanup (host_virtual_ip:mc_port)
	guestMotd                 string                // custom MOTD for LAN broadcast

	joinCancelled atomic.Bool // set to true to abort a running JoinRoom
}

type readResult struct {
	status uint8
	body   []byte
	err    error
}

// --- HOST methods ---

func (s *ScaffoldingService) CreateRoom(mcPort uint16, playerName string, vendorPrefix string, motd string) (*RoomStatus, error) {
	s.hostMu.Lock()
	if s.hostRunning {
		s.hostMu.Unlock()
		return nil, fmt.Errorf("已有房间在运行")
	}
	s.hostMu.Unlock()

	// 0. Verify the port has a Minecraft server running
	if mcPort <= 1024 || mcPort > 65535 {
		return nil, fmt.Errorf("端口号必须在 1025~65535 之间")
	}
	server := mcstatus.JavaServer{Host: "127.0.0.1", Port: mcPort}
	if _, err := server.Status(); err != nil {
		return nil, fmt.Errorf("端口 %d 上未检测到 Minecraft 服务器，请确认服务器已启动", mcPort)
	}

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
	if tcpPort <= 1024 || tcpPort > 65535 {
		listener.Close()
		return nil, fmt.Errorf("分配的TCP端口 %d 不合法（需大于1024）", tcpPort)
	}

	// 3. Start EasyTier
	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		listener.Close()
		return nil, err
	}

	hostname := fmt.Sprintf("scaffolding-mc-server-%d", tcpPort)
	virtualIP, err := manager.Start(easytier.StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		Hostname:      hostname,
		IsHost:        true,
		TCPPort:       tcpPort,
		MCPort:        mcPort,
		Peers:         s.resolvePeers(),
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
	s.hostStopReason = ""
	s.hostPlayerName = playerName
	s.hostConns = make(map[net.Conn]struct{})
	s.guestMotd = motd
	s.hostMu.Unlock()

	// Add HOST as a player
	machineID, _ := utils.GetMachineID()
	s.hostPlayerMu.Lock()
	s.hostPlayers[machineID] = &playerEntry{
		info: &PlayerInfo{
			Name:      playerName,
			MachineID: machineID,
			Vendor:    MakeVendor(vendorPrefix),
			Kind:      "HOST",
		},
		lastSeen: time.Now(),
	}
	s.hostPlayerMu.Unlock()

	go s.hostServerLoop()
	go s.hostPlayerCleanupLoop()
	go s.hostMCHealthCheckLoop()

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

	// Emit room.closed event
	reason := s.hostStopReason
	if reason == "" {
		reason = "room stopped by host"
	}
	s.eventEmitter.Emit("room.closed", map[string]string{"reason": reason})

	// Close all active guest connections so they detect disconnection immediately.
	s.hostConnMu.Lock()
	slog.Info("StopRoom closing host connections", "count", len(s.hostConns))
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

func (s *ScaffoldingService) GetRoomStatus() (*RoomStatus, error) {
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
					s.eventEmitter.Emit("room.player_left", *e.info)
					delete(s.hostPlayers, id)
				}
			}
			s.hostPlayerMu.Unlock()
		case <-s.hostStopCh:
			return
		}
	}
}

func (s *ScaffoldingService) hostMCHealthCheckLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.hostMu.Lock()
			mcPort := s.mcPort
			running := s.hostRunning
			s.hostMu.Unlock()

			if !running {
				return
			}

			server := mcstatus.JavaServer{Host: "127.0.0.1", Port: mcPort}
			if _, err := server.Status(); err != nil {
				slog.Warn("MC server not responding, stopping room", "port", mcPort)
				s.hostMu.Lock()
				s.hostStopReason = "Minecraft 服务器已关闭，房间已自动销毁"
				s.hostMu.Unlock()
				s.StopRoom()
				return
			}
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
				slog.Warn("Accept error", "error", err)
				continue
			}
		}
		slog.Info("accepted connection", "remote", conn.RemoteAddr())
		go s.handleHostConnection(conn)
	}
}

func (s *ScaffoldingService) handleHostConnection(conn net.Conn) {
	// Register connection so StopRoom can close it.
	s.hostConnMu.Lock()
	if s.hostConns != nil {
		s.hostConns[conn] = struct{}{}
		slog.Info("HostConn registered", "total", len(s.hostConns))
	} else {
		slog.Warn("HostConn not registered, hostConns is nil")
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
		// Check if room is still running before blocking on read.
		s.hostMu.Lock()
		running := s.hostRunning
		s.hostMu.Unlock()
		if !running {
			return
		}

		typeName, body, err := ReadProtocolRequest(conn)
		if err != nil {
			slog.Warn("ReadProtocolRequest error", "error", err)
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
	isNew := false
	if _, exists := s.hostPlayers[player.MachineID]; !exists && player.Kind == "GUEST" {
		isNew = true
	}
	s.hostPlayers[player.MachineID] = &playerEntry{
		info:     &player,
		lastSeen: time.Now(),
	}
	s.hostPlayerMu.Unlock()

	if isNew {
		s.eventEmitter.Emit("room.player_joined", player)
	}

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

// CancelJoin aborts a running JoinRoom call. Safe to call even if no join is in progress.
func (s *ScaffoldingService) CancelJoin() {
	s.joinCancelled.Store(true)
}

// setJoinProgressCallback sets an optional progress callback for JoinRoom.
// Only needed by CLI mode; Wails mode ignores it.
func (s *ScaffoldingService) setJoinProgressCallback(cb func(step string)) {
	s.joinProgressCb = cb
}

// SetScaffoldingJoinProgress sets the progress callback on a ScaffoldingService.
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

	// 1. Parse room code
	rc, err := ParseRoomCode(code)
	if err != nil {
		return nil, err
	}

	// 2. Start EasyTier
	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		return nil, err
	}

	machineID, _ := utils.GetMachineID()
	if _, err := manager.Start(easytier.StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		IsHost:        false,
		Peers:         s.resolvePeers(),
	}); err != nil {
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}
	s.reportJoinProgress("connecting")

	// 3. Discover HOST and wait for P2P connection
	// The hostname format is scaffolding-mc-server-{port}, scan peers for matching hostname.
	// Retry until we can actually connect via TCP (P2P may take time to establish).
	if s.joinCancelled.Load() {
		manager.Stop()
		return nil, fmt.Errorf("加入已取消")
	}
	hostIP, _, err := s.discoverHostAndConnect(manager, 60*time.Second)
	if err != nil {
		manager.Stop()
		return nil, fmt.Errorf("连接主机失败: %w", err)
	}

	// 4. We already have a working TCP connection from discoverHostAndConnect
	conn := s.guestConn

	easytierID := ""
	if peerID, err := manager.GetPeerID(); err == nil {
		easytierID = peerID
	}

	negotiatedEasyTierID, mcPort, err := s.joinHandshake(conn, manager, machineID, playerName, easytierID, vendorPrefix)
	if err != nil {
		return nil, err
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
	s.guestMotd = motd
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

func (s *ScaffoldingService) discoverHostAndConnect(manager *easytier.EasyTierManager, timeout time.Duration) (string, uint16, error) {
	deadline := time.Now().Add(timeout)

	var lastErr error
	var prevForwardProto string
	var prevForwardLocal string
	var prevForwardRemote string

	for time.Now().Before(deadline) {
		s.reportJoinProgress("waiting_peer")
		if s.joinCancelled.Load() {
			return "", 0, fmt.Errorf("加入已取消")
		}
		if !manager.IsRunning() {
			return "", 0, fmt.Errorf("easytier-core 进程已退出")
		}

		hostIP, scaffoldingPort, err := manager.FindPeerByHostnamePrefix("scaffolding-mc-server-")
		if err != nil {
			lastErr = err
			time.Sleep(2 * time.Second)
			continue
		}

		if s.tryDirectLocalhost(scaffoldingPort) {
			slog.Info("connected via direct localhost", "port", scaffoldingPort)
			return hostIP, scaffoldingPort, nil
		}

		localPort, conn, err := s.tryP2PConnect(manager, hostIP, scaffoldingPort)
		if err != nil {
			lastErr = err
			prevForwardProto = "tcp"
			prevForwardLocal = fmt.Sprintf("0.0.0.0:%d", localPort)
			prevForwardRemote = fmt.Sprintf("%s:%d", hostIP, scaffoldingPort)
			time.Sleep(2 * time.Second)
			continue
		}

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

func (s *ScaffoldingService) tryDirectLocalhost(scaffoldingPort uint16) bool {
	directConn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", scaffoldingPort), 2*time.Second)
	if err != nil {
		return false
	}
	if WriteProtocolRequest(directConn, ProtocolPing, nil) != nil {
		directConn.Close()
		return false
	}
	if _, _, err := ReadProtocolResponse(directConn); err != nil {
		directConn.Close()
		return false
	}
	s.guestMu.Lock()
	s.guestConn = directConn
	s.guestScaffoldingLocalPort = scaffoldingPort
	s.guestDirectLocal = true
	s.guestMu.Unlock()
	return true
}

func (s *ScaffoldingService) tryP2PConnect(manager *easytier.EasyTierManager, hostIP string, scaffoldingPort uint16) (uint16, net.Conn, error) {
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

	if err := WriteProtocolRequest(conn, ProtocolPing, nil); err != nil {
		conn.Close()
		return localPort, nil, fmt.Errorf("P2P隧道验证失败: %w", err)
	}
	if _, _, err := ReadProtocolResponse(conn); err != nil {
		conn.Close()
		return localPort, nil, fmt.Errorf("P2P隧道验证失败: %w", err)
	}

	return localPort, conn, nil
}

func (s *ScaffoldingService) joinHandshake(conn net.Conn, manager *easytier.EasyTierManager, machineID, playerName, easytierID, vendorPrefix string) (bool, uint16, error) {
	// Send c:player_ping
	pingData, _ := json.Marshal(PlayerInfo{
		Name:       playerName,
		MachineID:  machineID,
		EasyTierID: easytierID,
		Vendor:     MakeVendor(vendorPrefix),
		Kind:       "GUEST",
	})
	if err := WriteProtocolRequest(conn, ProtocolPlayerPing, pingData); err != nil {
		conn.Close()
		manager.Stop()
		return false, 0, fmt.Errorf("发送心跳失败: %w", err)
	}
	if _, _, err := ReadProtocolResponse(conn); err != nil {
		conn.Close()
		manager.Stop()
		return false, 0, fmt.Errorf("心跳响应失败: %w", err)
	}

	// Protocol negotiation
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
		return false, 0, fmt.Errorf("协议协商失败: %w", err)
	}
	status, respBody, err := ReadProtocolResponse(conn)
	if err != nil || status != StatusOK {
		conn.Close()
		manager.Stop()
		return false, 0, fmt.Errorf("协议协商失败")
	}
	negotiated := strings.Split(string(respBody), "\x00")
	negotiatedEasyTierID := false
	for _, p := range negotiated {
		if p == ProtocolPlayerEasyTierID {
			negotiatedEasyTierID = true
		}
	}

	s.reportJoinProgress("handshaking")

	// Get MC server port
	if err := WriteProtocolRequest(conn, ProtocolServerPort, nil); err != nil {
		conn.Close()
		manager.Stop()
		return false, 0, fmt.Errorf("获取服务器端口失败: %w", err)
	}
	status, respBody, err = ReadProtocolResponse(conn)
	if err != nil {
		conn.Close()
		manager.Stop()
		return false, 0, fmt.Errorf("获取服务器端口失败: %w", err)
	}
	if status != StatusOK && status != StatusServerNotStarted {
		conn.Close()
		manager.Stop()
		return false, 0, fmt.Errorf("获取服务器端口失败: 状态=%d", status)
	}

	var mcPort uint16
	if status == StatusOK && len(respBody) >= 2 {
		mcPort = uint16(respBody[0])<<8 | uint16(respBody[1])
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

	slog.Info("GetConnectionStatus", "running", running)

	if !running {
		s.guestMu.Lock()
		reason := s.guestDisconnectReason
		s.guestMu.Unlock()
		slog.Info("GetConnectionStatus not running", "reason", reason)
		if reason != "" {
			return s.buildConnectionStatus(), nil
		}
		return nil, fmt.Errorf("未连接到任何房间")
	}

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

// guestReadLoop runs in a background goroutine. It continuously reads responses
// from the TCP connection (like Rust's ClientSession background thread).
// When the read fails, it closes guestReadCh to signal all waiters.
func (s *ScaffoldingService) guestReadLoop(conn net.Conn) {
	slog.Info("ReadLoop started")
	for {
		status, body, err := ReadProtocolResponse(conn)
		if err != nil {
			slog.Warn("ReadLoop read failed", "error", err)
			// Drain any pending read result, then close the channel.
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

// writeAndWait writes a request then waits for the background reader to deliver
// the response. Returns an error if the write fails, the reader fails, or a
// timeout occurs.
func (s *ScaffoldingService) writeAndWait(conn net.Conn, typeName string, body []byte) (uint8, []byte, error) {
	s.guestIOMu.Lock()
	if err := WriteProtocolRequest(conn, typeName, body); err != nil {
		s.guestIOMu.Unlock()
		return 0, nil, fmt.Errorf("write %s: %w", typeName, err)
	}
	s.guestIOMu.Unlock()

	// Wait for the background reader to deliver the response.
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

	slog.Info("Heartbeat started")

	for {
		select {
		case <-ticker.C:
			s.guestMu.Lock()
			conn := s.guestConn
			running := s.guestRunning
			s.guestMu.Unlock()

			if !running || conn == nil {
				slog.Info("Heartbeat exiting", "running", running)
				return
			}

			pingData, _ := json.Marshal(PlayerInfo{
				Name:       playerName,
				MachineID:  machineID,
				EasyTierID: easytierID,
				Vendor:     MakeVendor(vendorPrefix),
				Kind:       "GUEST",
			})

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
			slog.Info("Heartbeat exiting on stopCh")
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

	// Set up EasyTier port-forward: local -> HOST virtual IP:mcPort
	remoteAddr := fmt.Sprintf("%s:%d", hostIP, mcPort)
	localAddr := fmt.Sprintf("0.0.0.0:%d", mcLocalPort)

	// TCP port-forward
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
		s.guestFakeServer = minecraft.NewFakeServer(mcLocalPort, motd)
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

// Cleanup stops any running room or connection (called on app shutdown)
func (s *ScaffoldingService) Cleanup() {
	s.StopRoom()
	s.LeaveRoom()
}

// ConfigureSettingsPeers provides GUI custom peers for future EasyTier starts.
func ConfigureSettingsPeers(s *ScaffoldingService, settingsSvc *easytier.SettingsService) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	s.settingsSvc = settingsSvc
}

// ConfigureCLIPeers replaces the built-in peers for CLI starts.
func ConfigureCLIPeers(s *ScaffoldingService, peers []string) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	s.peersOverride = append([]string(nil), peers...)
}

func (s *ScaffoldingService) resolvePeers() []string {
	s.peersMu.RLock()
	override := append([]string(nil), s.peersOverride...)
	additional := append([]string(nil), s.additionalPeers...)
	settingsSvc := s.settingsSvc
	s.peersMu.RUnlock()

	if len(override) > 0 {
		return append(override, additional...)
	}

	peers := append([]string(nil), scaffoldingBuiltinPeers...)
	if settingsSvc != nil {
		peers = append(peers, settingsSvc.GetCustomPeers()...)
	}
	return append(peers, additional...)
}

// AddPeers appends peer addresses for future EasyTier starts.
func (s *ScaffoldingService) AddPeers(addrs []string) {
	s.peersMu.Lock()
	defer s.peersMu.Unlock()
	s.additionalPeers = append(s.additionalPeers, addrs...)
}
