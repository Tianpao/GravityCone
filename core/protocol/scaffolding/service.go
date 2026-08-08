package scaffolding

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	mcstatus "github.com/andre-carbajal/go-mcstatus"

	"gravitycone/core/easytier"
	lansca "gravitycone/core/lan/scaffolding"
	"gravitycone/core/protocol/common"
	"gravitycone/core/utils"
)

const hostnamePrefix = "scaffolding-mc-server-"

var scaffoldingBuiltinPeers = []string{
	"https://etnode.zkitefly.eu.org/node1",
	"wss://center.node.1tmc.top",
}

// serverProtocols is the list of protocols this host supports (excluding
// ProtocolPlayerEasyTierID, which is negotiated separately).
var serverProtocols = []string{
	ProtocolPing,
	ProtocolProtocols,
	ProtocolServerPort,
	ProtocolPlayerPing,
	ProtocolPlayerProfilesList,
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

// Caller must NOT hold hostPlayerMu.
func (s *ScaffoldingService) copyPlayers() []PlayerInfo {
	s.hostPlayerMu.Lock()
	players := make([]PlayerInfo, 0, len(s.hostPlayers))
	for _, e := range s.hostPlayers {
		players = append(players, *e.info)
	}
	s.hostPlayerMu.Unlock()
	return players
}

func newGuestPlayerInfo(machineID, playerName, easytierID, vendorPrefix string) PlayerInfo {
	return PlayerInfo{
		Name:       playerName,
		MachineID:  machineID,
		EasyTierID: easytierID,
		Vendor:     common.MakeVendor(vendorPrefix),
		Kind:       KindGuest,
	}
}

func NewScaffoldingService(emitter utils.EventEmitter) *ScaffoldingService {
	if emitter == nil {
		emitter = utils.NilEventEmitter{}
	}
	return &ScaffoldingService{
		eventEmitter: emitter,
		relay:        easytier.NewRelayManager(),
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

// This is a package-level helper so main.go can call it without the method
// appearing in Wails bindings.
func InitScaffoldingEmitter(svc *ScaffoldingService, emitter utils.EventEmitter) {
	svc.setEventEmitter(emitter)
}

type ScaffoldingService struct {
	eventEmitter   utils.EventEmitter
	joinProgressCb func(string) // set by CLI mode for progress notifications
	peerConfig     easytier.PeerConfig
	relay          *easytier.RelayManager

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
	guestScaffoldingLocalPort uint16             // local port forwarded to host's scaffolding port
	guestDisconnectReason     string             // set when connection is lost (e.g. host closed room)
	guestDirectLocal          bool               // true when guest and host are on the same machine
	guestIOMu                 sync.Mutex         // serializes writes on guestConn
	guestReadCh               chan readResult    // background reader delivers responses here
	guestFakeServer           *lansca.FakeServer // LAN broadcaster for Minecraft discovery
	guestMCLocalPort          uint16             // local port forwarded to host's MC server via EasyTier
	guestMCRemoteAddr         string             // remote addr for port-forward cleanup (host_virtual_ip:mc_port)
	guestMotd                 string             // custom MOTD for LAN broadcast

	joinCancelled atomic.Bool // set to true to abort a running JoinRoom
}

type readResult struct {
	status uint8
	body   []byte
	err    error
}

func (s *ScaffoldingService) CreateRoom(mcPort uint16, playerName string, vendorPrefix string, motd string) (*RoomStatus, error) {
	s.hostMu.Lock()
	if s.hostRunning {
		s.hostMu.Unlock()
		return nil, fmt.Errorf("已有房间在运行")
	}
	s.hostMu.Unlock()

	if mcPort <= 1024 || mcPort > 65535 {
		return nil, fmt.Errorf("端口号必须在 1025~65535 之间")
	}
	server := mcstatus.JavaServer{Host: "127.0.0.1", Port: mcPort}
	if _, err := server.Status(); err != nil {
		return nil, fmt.Errorf("端口 %d 上未检测到 Minecraft 服务器，请确认服务器已启动", mcPort)
	}

	// 1. 拉取 uptime 节点并选定发现节点 nodeID（须在生成房间码之前，房客据此定向取同一个节点）
	hostPeers, nodeID := s.relay.HostPeersAndNodeID(s.resolvePeers())

	rc, err := GenerateRoomCodeWithNodeID(nodeID)
	if err != nil {
		return nil, fmt.Errorf("生成房间代码失败: %w", err)
	}

	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, fmt.Errorf("分配TCP端口失败: %w", err)
	}
	tcpPort := uint16(listener.Addr().(*net.TCPAddr).Port)

	if tcpPort <= 1024 || tcpPort > 65535 {
		listener.Close()
		return nil, fmt.Errorf("分配的TCP端口 %d 不合法（需大于1024）", tcpPort)
	}

	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		listener.Close()
		return nil, err
	}

	hostname := fmt.Sprintf("%s%d", hostnamePrefix, tcpPort)
	virtualIP, err := manager.Start(easytier.StartOptions{
		NetworkName:   rc.EasyTierNetworkName(),
		NetworkSecret: rc.EasyTierNetworkSecret(),
		Hostname:      hostname,
		IsHost:        true,
		TCPPort:       tcpPort,
		MCPort:        mcPort,
		Peers:         hostPeers,
		DisableP2P:    s.relay.P2PDisabled(),
	})
	if err != nil {
		listener.Close()
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}

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

	machineID, _ := utils.GetMachineID()
	s.hostPlayerMu.Lock()
	s.hostPlayers[machineID] = &playerEntry{
		info: &PlayerInfo{
			Name:      playerName,
			MachineID: machineID,
			Vendor:    common.MakeVendor(vendorPrefix),
			Kind:      KindHost,
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
			return nil, errors.New(reason)
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
	players := s.copyPlayers()

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

func (s *ScaffoldingService) Cleanup() {
	s.StopRoom()
	s.LeaveRoom()
}

func (s *ScaffoldingService) ServiceShutdown() error {
	s.Cleanup()
	return nil
}
