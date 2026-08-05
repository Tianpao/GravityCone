package paperconnect

import (
	"context"
	"fmt"
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
	lanpc "gravitycone/core/lan/paperconnect"
	"gravitycone/core/protocol/common"
	"gravitycone/core/utils"
)

const pcHostnamePrefix = "pcs-"
const pcPlayerTimeout = 10 * time.Second

var paperConnectBuiltinPeers = []string{
	"wss://center.node.1tmc.top",
}

type PaperConnectRoomStatus struct {
	Code        string          `json:"code"`
	GamePort    int             `json:"game_port"`
	SubProtocol string          `json:"sub_protocol"`
	OnlineCount int             `json:"online_count"`
	Players     []PCPlayerEntry `json:"players"`
	Running     bool            `json:"running"`
}

type PaperConnectConnectionStatus struct {
	RoomCode         string          `json:"room_code"`
	HostAddress      string          `json:"host_address"`
	GamePort         int             `json:"game_port"`
	SubProtocol      string          `json:"sub_protocol"`
	Connected        bool            `json:"connected"`
	OnlineCount      int             `json:"online_count"`
	Players          []PCPlayerEntry `json:"players"`
	Heartbeating     bool            `json:"heartbeating"`
	DisconnectReason string          `json:"disconnect_reason"`
}

type PaperConnectService struct {
	eventEmitter utils.EventEmitter
	peerConfig   easytier.PeerConfig
	relay        *easytier.RelayManager

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
	hostProtocol   string            // ProtocolNetherNet or ProtocolRakNet
	hostGamePort   uint16            // RakNet listener port (NetherNet) or scanned MC port (RakNet)
	hostRakNetInfo *lanpc.RakNetServerInfo // Server info from RakNet scan (for guest broadcast)

	guestManager          *easytier.EasyTierManager
	guestRakConn          *raknet.Conn
	guestDisc             *discovery.Listener
	guestNnLn             *nethernet.Listener
	guestRakRelayLn       *raknet.Listener // direct 模式本机 RakNet 中继监听
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
	guestRakNetFakeStop   chan struct{}
	guestPortBusy         bool
	guestPortBusyConfirm  chan struct{}
	guestMotd             string // custom MOTD for LAN broadcast

	joinCancelled atomic.Bool
}

func NewPaperConnectService(emitter utils.EventEmitter) *PaperConnectService {
	if emitter == nil {
		emitter = utils.NilEventEmitter{}
	}
	return &PaperConnectService{
		eventEmitter: emitter,
		relay:        easytier.NewRelayManager(),
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

func (s *PaperConnectService) CreateRoom(playerName string, vendorPrefix string) (*PaperConnectRoomStatus, error) {
	s.hostMu.Lock()
	if s.hostRunning {
		s.hostMu.Unlock()
		return nil, fmt.Errorf("已有房间在运行")
	}
	s.hostRunning = true
	s.hostMu.Unlock()

	// Ensure hostRunning is reset on any early return from this function.
	var setupFailed bool
	defer func() {
		if setupFailed {
			s.hostMu.Lock()
			s.hostRunning = false
			s.hostMu.Unlock()
		}
	}()

	// 提前并行拉取 uptime 节点（与下面的 LAN 扫描重叠，房主开房可省最多 8 秒）。
	// HostPeersAndNodeID 内部降级、无错误返回，扫描失败时丢弃结果即可。
	hostPeersCh := make(chan []string, 1)
	nodeIDCh := make(chan int, 1)
	go func() {
		hostPeers, nodeID := s.relay.HostPeersAndNodeID(s.resolvePeers())
		hostPeersCh <- hostPeers
		nodeIDCh <- nodeID
	}()

	ctx, cancelScan := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancelScan()

	var nnFound, rkFound bool
	var rakNetInfo *lanpc.RakNetServerInfo

	nnCh := make(chan bool, 1)
	rkCh := make(chan *lanpc.RakNetServerInfo, 1)

	go func() { nnCh <- lanpc.DetectNetherNet(ctx) }()
	go func() {
		if info, err := lanpc.ScanRakNetLAN(ctx, 5*time.Second); err == nil {
			rkCh <- info
		} else {
			rkCh <- nil
		}
	}()

	nnFound = <-nnCh
	rakNetInfo = <-rkCh
	rkFound = rakNetInfo != nil

	if !nnFound && !rkFound {
		setupFailed = true
		return nil, fmt.Errorf("未检测到本地Minecraft基岩版房间，请先在Minecraft中开启局域网游戏")
	}

	protocol := ProtocolNetherNet
	gamePort := uint16(0)
	if rkFound && !nnFound {
		protocol = ProtocolRakNet
		gamePort = rakNetInfo.GamePort
	}
	// If both found, prefer NetherNet (newer version)

	// 拉取 uptime 节点并选定中继 nodeID（须在生成房间码之前，房客据此定向取同一个中继）
	hostPeers := <-hostPeersCh
	nodeID := <-nodeIDCh

	rc, err := GeneratePaperConnectRoomCodeWithNodeID(nodeID)
	if err != nil {
		setupFailed = true
		return nil, fmt.Errorf("生成房间代码失败: %w", err)
	}

	tcpLn, err := net.Listen("tcp", ":0")
	if err != nil {
		setupFailed = true
		return nil, fmt.Errorf("分配TCP端口失败: %w", err)
	}
	tcpPort := uint16(tcpLn.Addr().(*net.TCPAddr).Port)

	if tcpPort <= 1024 || tcpPort > 65535 {
		tcpLn.Close()
		setupFailed = true
		return nil, fmt.Errorf("分配的TCP端口 %d 不合法", tcpPort)
	}

	manager, err := easytier.NewEasyTierManager()
	if err != nil {
		tcpLn.Close()
		setupFailed = true
		return nil, err
	}

	var hostname string
	var rakLn *raknet.Listener

	if protocol == ProtocolRakNet {
		hostname = buildHostnameRakNet(tcpPort, gamePort)
	} else {
		// NetherNet: start RakNet listener first (random port), then encode its port.
		rakLn, err = (raknet.ListenConfig{
			MaxMTU:        rakNetMTU,
			ErrorLog:      slog.Default(),
			BlockDuration: -1,
		}).Listen(":0")
		if err != nil {
			tcpLn.Close()
			setupFailed = true
			return nil, fmt.Errorf("启动RakNet监听失败: %w", err)
		}
		_, portStr, _ := net.SplitHostPort(rakLn.Addr().String())
		rakPort, _ := strconv.ParseUint(portStr, 10, 16)
		gamePort = uint16(rakPort)
		hostname = buildHostname(tcpPort, gamePort)
	}

	virtualIP, err := manager.Start(easytier.StartOptions{
		NetworkName:        rc.EasyTierNetworkName(),
		NetworkSecret:      rc.EasyTierNetworkSecret(),
		Hostname:           hostname,
		IsHost:             true,
		TCPPort:            tcpPort,
		MCPort:             gamePort,
		Peers:              hostPeers,
		UpstreamCompatible: true,
		DisableP2P:         s.relay.P2PDisabled(),
	})
	if err != nil {
		if rakLn != nil {
			rakLn.Close()
		}
		tcpLn.Close()
		setupFailed = true
		return nil, fmt.Errorf("启动虚拟网络失败: %w", err)
	}

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
	s.hostStopReason = ""
	s.hostPlayerName = playerName
	s.hostSessions = make(chan struct{}, maxHostSessions)
	s.hostProtocol = protocol
	s.hostGamePort = gamePort
	s.hostRakNetInfo = rakNetInfo
	s.hostMu.Unlock()

	clientId := common.MakeVendor(vendorPrefix)

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

	status := s.pcBuildRoomStatus(virtualIP)
	s.eventEmitter.Emit("paperconnect.room.info", status)
	return status, nil
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
	s.hostMu.Unlock()

	return s.pcBuildRoomStatus(""), nil
}

func (s *PaperConnectService) pcBuildRoomStatus(virtualIP string) *PaperConnectRoomStatus {
	s.hostPlayerMu.Lock()
	players := make([]PCPlayerEntry, 0, len(s.hostPlayers))
	for _, e := range s.hostPlayers {
		players = append(players, *e)
	}
	s.hostPlayerMu.Unlock()

	s.hostMu.Lock()
	code := ""
	if s.roomCode != nil {
		code = s.roomCode.Format()
	}
	status := &PaperConnectRoomStatus{
		Code:        code,
		GamePort:    int(s.hostGamePort),
		SubProtocol: s.hostProtocol,
		OnlineCount: len(players),
		Players:     players,
		Running:     s.hostRunning,
	}
	s.hostMu.Unlock()

	return status
}

func (s *PaperConnectService) Cleanup() {
	s.StopRoom()
	s.LeaveRoom()
}
