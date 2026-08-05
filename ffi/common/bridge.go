package main

import (
	"encoding/json"
	"fmt"
	"sync"

	"gravitycone/core/easytier"
	"gravitycone/core/protocol/paperconnect"
	"gravitycone/core/protocol/scaffolding"
)

// 启动器指定的中继节点（CLI/FFI 模式）：nodeID 编码进房间码（房主端），
// url 直接作为 EasyTier peer（房主与房客两端）。setRelay 在 setScanning /
// setGuesting 前调用；设置后持久保持，直到再次调用（url 为空则清除，
// 恢复 uptime 自动获取）。
var (
	relayMu     sync.Mutex
	relayNodeID int
	relayURL    string
)

// setRelay 设置启动器提供的中继节点；url 为空时清除覆盖。
func setRelay(nodeID int, url string) {
	relayMu.Lock()
	defer relayMu.Unlock()
	relayNodeID = nodeID
	relayURL = url
}

// relayParams 返回当前启动器指定的中继配置。
func relayParams() (nodeID int, url string) {
	relayMu.Lock()
	defer relayMu.Unlock()
	return relayNodeID, relayURL
}

// applyRelayToScaffolding 把启动器指定的中继注入 Scaffolding 服务
// （url 为空时清除覆盖，恢复 uptime 自动获取）。
func applyRelayToScaffolding(svc *scaffolding.ScaffoldingService) {
	nodeID, url := relayParams()
	scaffolding.ConfigureExternalRelay(svc, nodeID, url)
}

// applyRelayToPaperConnect 把启动器指定的中继注入 PaperConnect 服务
// （url 为空时清除覆盖，恢复 uptime 自动获取）。
func applyRelayToPaperConnect(svc *paperconnect.PaperConnectService) {
	nodeID, url := relayParams()
	paperconnect.ConfigureExternalRelay(svc, nodeID, url)
}

// ffiEventEmitter bridges asynchronous core-service events into the state
// JSON that Android polls. PaperConnect establishes its game bridge after
// JoinRoom returns, so dropping these events made an Android join look
// successful even when the forwarding path had already failed.
type ffiEventEmitter struct {
	guest *guestContext
}

func (e ffiEventEmitter) Emit(event string, data interface{}) {
	if e.guest == nil {
		return
	}

	globalState.mu.Lock()
	defer globalState.mu.Unlock()
	if globalState.extra != e.guest {
		return
	}

	switch event {
	case "paperconnect.connection.ready":
		e.guest.connectionState = "ready"
		e.guest.connectionError = ""
	case "paperconnect.connection.error":
		e.guest.connectionState = "error"
		if values, ok := data.(map[string]string); ok {
			e.guest.connectionError = values["message"]
		}
	case "paperconnect.room.disconnected":
		e.guest.connectionState = "disconnected"
		if values, ok := data.(map[string]string); ok {
			e.guest.disconnectReason = values["reason"]
		}
	default:
		return
	}
	globalState.index++
}

// --- Public API called from export.go ---

// setWaiting transitions to idle state, cleaning up any active room.
func setWaiting() {
	goBackToIdle()
}

// setScanning starts scanning for a local Minecraft server and creates a room.
// room: optional room code (empty = generate new). player: player name.
// protocol: "scaffolding" (default) or "paperconnect".
// The idle check and the transition to HostScanning happen atomically, so two
// concurrent calls cannot both start a room.
func setScanning(room, player, protocol string) {
	if protocol == "" {
		protocol = ProtocolScaffolding
	}

	if player == "" {
		player = "Player"
	}

	var ctx *hostContext
	switch protocol {
	case ProtocolPaperConnect:
		ctx = &hostContext{protocol: ProtocolPaperConnect}
	default:
		ctx = &hostContext{protocol: ProtocolScaffolding}
	}
	if !beginTransition(StateHostScanning, ctx) {
		return
	}

	switch protocol {
	case ProtocolPaperConnect:
		go startPaperConnectHost(player, ctx)
	default:
		go startScaffoldingHost(player, ctx)
	}
}

// setGuesting starts connecting to a remote room.
// Returns false if the room code is empty or a room is already active.
func setGuesting(roomCode, player string) bool {
	if roomCode == "" {
		return false
	}

	if player == "" {
		player = "Player"
	}

	// Route based on room code prefix.
	if isPaperConnectCode(roomCode) {
		ctx := &guestContext{protocol: ProtocolPaperConnect, roomCode: roomCode}
		if !beginTransition(StateGuestConnecting, ctx) {
			return false
		}
		go joinPaperConnectRoom(roomCode, player, ctx)
		return true
	}

	// Scaffolding (U/ prefix or legacy Terracotta/PCL2CE codes).
	ctx := &guestContext{protocol: ProtocolScaffolding, roomCode: roomCode}
	if !beginTransition(StateGuestConnecting, ctx) {
		return false
	}
	go joinScaffoldingRoom(roomCode, player, ctx)
	return true
}

// verifyRoomCode checks the room code type.
// Returns RoomCodeScaffolding (3), RoomCodePaperConnect (4), or RoomCodeInvalid (-1).
func verifyRoomCode(code string) int {
	if isPaperConnectCode(code) {
		if _, err := paperconnect.ParsePaperConnectRoomCode(code); err == nil {
			return RoomCodePaperConnect
		}
		return RoomCodeInvalid
	}

	// Scaffolding room code (U/ prefix)
	if _, err := scaffolding.ParseRoomCode(code); err == nil {
		return RoomCodeScaffolding
	}

	return RoomCodeInvalid
}

func isPaperConnectCode(code string) bool {
	return len(code) >= 2 && (code[0] == 'P' || code[0] == 'p') && code[1] == '/'
}

// --- ScaffoldingMC (Java Edition) host ---

func startScaffoldingHost(playerName string, ctx *hostContext) {
	// 状态已在 setScanning 的 beginTransition 中转移到 HostScanning。

	// Create scaffolding service.
	svc := scaffolding.NewScaffoldingService(ffiEventEmitter{})
	applyRelayToScaffolding(svc)

	// Scan for available MC port. For now, use default 25565.
	// In the future, we can integrate MinecraftScanner like Terracotta does.
	mcPort := uint16(25565)

	if !transitionToIfOwner(ctx, StateHostStarting, ctx) {
		return
	}

	result, err := svc.CreateRoom(mcPort, playerName, "", "")
	if err != nil {
		transitionToErrorIfOwner(ctx, fmt.Sprintf("创建房间失败: %v", err))
		return
	}

	ctx.roomCode = result.Code
	ctx.mcPort = result.MCPort
	var stopOnce sync.Once
	ctx.stopFn = func() {
		stopOnce.Do(func() {
			svc.StopRoom()
		})
	}
	// goBackToIdle 可能已在 stopFn 设置前执行；若所有权已失去，
	// 自行清理，避免房间实例泄漏。
	if !transitionToIfOwner(ctx, StateHostReady, ctx) {
		ctx.stopFn()
		return
	}
}

// --- PaperConnect (Bedrock Edition) host ---

func startPaperConnectHost(playerName string, ctx *hostContext) {
	// 状态已在 setScanning 的 beginTransition 中转移到 HostScanning。

	svc := paperconnect.NewPaperConnectService(ffiEventEmitter{})
	applyRelayToPaperConnect(svc)

	if !transitionToIfOwner(ctx, StateHostStarting, ctx) {
		return
	}

	result, err := svc.CreateRoom(playerName, "")
	if err != nil {
		transitionToErrorIfOwner(ctx, fmt.Sprintf("创建房间失败: %v", err))
		return
	}

	ctx.roomCode = result.Code
	ctx.gamePort = result.GamePort
	ctx.subProtocol = result.SubProtocol
	var stopOnce sync.Once
	ctx.stopFn = func() {
		stopOnce.Do(func() {
			svc.StopRoom()
		})
	}
	if !transitionToIfOwner(ctx, StateHostReady, ctx) {
		ctx.stopFn()
		return
	}
}

// --- ScaffoldingMC (Java Edition) guest ---

func joinScaffoldingRoom(roomCode, playerName string, ctx *guestContext) {
	// 状态已在 setGuesting 的 beginTransition 中转移到 GuestConnecting。

	// Set up progress callback.
	progress := func(step string) {
		ctx.roomCode = roomCode
		updateExtra(ctx)
	}

	svc := scaffolding.NewScaffoldingService(ffiEventEmitter{})
	applyRelayToScaffolding(svc)
	// Inject progress callback equivalent to CLI mode.
	scaffolding.SetScaffoldingJoinProgress(svc, progress)

	result, err := svc.JoinRoom(roomCode, playerName, "", "")
	if err != nil {
		transitionToErrorIfOwner(ctx, fmt.Sprintf("加入房间失败: %v", err))
		return
	}

	ctx.mcURL = fmt.Sprintf("%s:%d", result.MCAddress, result.MCPort)
	var leaveOnce sync.Once
	ctx.leaveFn = func() {
		leaveOnce.Do(func() {
			// CancelJoin 让进行中的加入流程尽快退出（LeaveRoom 只关
			// guestStopCh，不置 joinCancelled）。
			svc.CancelJoin()
			svc.LeaveRoom()
		})
	}
	// goBackToIdle 可能已在 leaveFn 设置前执行；若所有权已失去，
	// 自行清理，避免加入流程残留。
	if !transitionToIfOwner(ctx, StateGuestReady, ctx) {
		ctx.leaveFn()
		return
	}
}

// --- PaperConnect (Bedrock Edition) guest ---

func joinPaperConnectRoom(roomCode, playerName string, ctx *guestContext) {
	// 状态已在 setGuesting 的 beginTransition 中转移到 GuestConnecting。

	svc := paperconnect.NewPaperConnectService(ffiEventEmitter{guest: ctx})
	applyRelayToPaperConnect(svc)

	result, err := svc.JoinRoom(roomCode, playerName, "", "")
	if err != nil {
		transitionToErrorIfOwner(ctx, fmt.Sprintf("加入房间失败: %v", err))
		return
	}

	ctx.subProtocol = result.SubProtocol
	ctx.mcURL = fmt.Sprintf("127.0.0.1:%d", result.GamePort)
	var leaveOnce sync.Once
	ctx.leaveFn = func() {
		leaveOnce.Do(func() {
			svc.CancelJoin()
			svc.LeaveRoom()
		})
	}
	if !transitionToIfOwner(ctx, StateGuestReady, ctx) {
		ctx.leaveFn()
		return
	}
}

// --- STUN (NAT Probing) ---

// stunProbe runs a STUN NAT type probe and returns the result as JSON.
// This is a blocking call that takes 3-10 seconds.
// Returns JSON with "error" field on failure.
func stunProbe() string {
	svc := &easytier.StunService{}
	result, err := svc.TestStun()
	if err != nil {
		return fmt.Sprintf(`{"error":"stun probe failed: %v"}`, err)
	}
	data, _ := json.Marshal(result)
	return string(data)
}
