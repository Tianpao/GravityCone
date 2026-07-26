package main

import (
	"encoding/json"
	"fmt"

	"gravitycone/core/easytier"
	"gravitycone/core/protocol/paperconnect"
	"gravitycone/core/protocol/scaffolding"
)

// ffiEventEmitter bridges events from core services to the FFI layer.
// In the current state-machine design (mirroring Terracotta), events are
// reflected in the state JSON returned by getState(), so we use a minimal
// emitter that just logs for debugging.
type ffiEventEmitter struct{}

func (ffiEventEmitter) Emit(event string, data interface{}) {
	// Events are reflected through state polling (getState).
	// The caller polls getState() periodically to detect changes.
}

// --- Public API called from export.go ---

// setWaiting transitions to idle state, cleaning up any active room.
func setWaiting() {
	goBackToIdle()
}

// setScanning starts scanning for a local Minecraft server and creates a room.
// room: optional room code (empty = generate new). player: player name.
// protocol: "scaffolding" (default) or "paperconnect".
func setScanning(room, player, protocol string) {
	if !canTransition() {
		return
	}

	if protocol == "" {
		protocol = ProtocolScaffolding
	}

	if player == "" {
		player = "Player"
	}

	switch protocol {
	case ProtocolPaperConnect:
		go startPaperConnectHost(player)
	default:
		go startScaffoldingHost(player)
	}
}

// setGuesting starts connecting to a remote room.
// Returns false if the room code is invalid.
func setGuesting(roomCode, player string) bool {
	if !canTransition() {
		return false
	}

	if player == "" {
		player = "Player"
	}

	// Route based on room code prefix.
	if isPaperConnectCode(roomCode) {
		go joinPaperConnectRoom(roomCode, player)
		return true
	}

	// Scaffolding (U/ prefix or legacy Terracotta/PCL2CE codes).
	go joinScaffoldingRoom(roomCode, player)
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

func startScaffoldingHost(playerName string) {
	ctx := &hostContext{protocol: ProtocolScaffolding}
	transitionTo(StateHostScanning, ctx)

	// Create scaffolding service.
	svc := scaffolding.NewScaffoldingService(ffiEventEmitter{})

	// Scan for available MC port. For now, use default 25565.
	// In the future, we can integrate MinecraftScanner like Terracotta does.
	mcPort := uint16(25565)

	transitionTo(StateHostStarting, ctx)

	result, err := svc.CreateRoom(mcPort, playerName, "", "")
	if err != nil {
		transitionToError(fmt.Sprintf("创建房间失败: %v", err))
		return
	}

	ctx.roomCode = result.Code
	ctx.mcPort = result.MCPort
	ctx.stopFn = func() {
		svc.StopRoom()
	}

	transitionTo(StateHostReady, ctx)
}

// --- PaperConnect (Bedrock Edition) host ---

func startPaperConnectHost(playerName string) {
	ctx := &hostContext{protocol: ProtocolPaperConnect}
	transitionTo(StateHostScanning, ctx)

	svc := paperconnect.NewPaperConnectService(ffiEventEmitter{})

	transitionTo(StateHostStarting, ctx)

	result, err := svc.CreateRoom(playerName, "")
	if err != nil {
		transitionToError(fmt.Sprintf("创建房间失败: %v", err))
		return
	}

	ctx.roomCode = result.Code
	ctx.gamePort = result.GamePort
	ctx.subProtocol = result.SubProtocol
	ctx.stopFn = func() {
		svc.StopRoom()
	}

	transitionTo(StateHostReady, ctx)
}

// --- ScaffoldingMC (Java Edition) guest ---

func joinScaffoldingRoom(roomCode, playerName string) {
	ctx := &guestContext{
		protocol: ProtocolScaffolding,
		roomCode: roomCode,
	}
	transitionTo(StateGuestConnecting, ctx)

	// Set up progress callback.
	progress := func(step string) {
		ctx.roomCode = roomCode
		updateExtra(ctx)
	}

	svc := scaffolding.NewScaffoldingService(ffiEventEmitter{})
	// Inject progress callback equivalent to CLI mode.
	scaffolding.SetScaffoldingJoinProgress(svc, progress)

	result, err := svc.JoinRoom(roomCode, playerName, "", "")
	if err != nil {
		transitionToError(fmt.Sprintf("加入房间失败: %v", err))
		return
	}

	ctx.mcURL = fmt.Sprintf("%s:%d", result.MCAddress, result.MCPort)
	ctx.leaveFn = func() {
		svc.LeaveRoom()
	}

	transitionTo(StateGuestReady, ctx)
}

// --- PaperConnect (Bedrock Edition) guest ---

func joinPaperConnectRoom(roomCode, playerName string) {
	ctx := &guestContext{
		protocol: ProtocolPaperConnect,
		roomCode: roomCode,
	}
	transitionTo(StateGuestConnecting, ctx)

	svc := paperconnect.NewPaperConnectService(ffiEventEmitter{})

	result, err := svc.JoinRoom(roomCode, playerName, "", "")
	if err != nil {
		transitionToError(fmt.Sprintf("加入房间失败: %v", err))
		return
	}

	ctx.subProtocol = result.SubProtocol
	ctx.mcURL = fmt.Sprintf("127.0.0.1:%d", result.GamePort)
	ctx.leaveFn = func() {
		svc.LeaveRoom()
	}

	transitionTo(StateGuestReady, ctx)
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

// --- Peer management ---

// ffAddPeers adds peer addresses to both protocol services.
func ffAddPeers(scaffoldingSvc *scaffolding.ScaffoldingService, paperConnectSvc *paperconnect.PaperConnectService, addrs []string) {
	if scaffoldingSvc != nil {
		scaffoldingSvc.AddPeers(addrs)
	}
	if paperConnectSvc != nil {
		paperConnectSvc.AddPeers(addrs)
	}
}

// --- Cleanup ---

// ffCleanup stops all active rooms and connections.
func ffCleanup(scaffoldingSvc *scaffolding.ScaffoldingService, paperConnectSvc *paperconnect.PaperConnectService) {
	if scaffoldingSvc != nil {
		scaffoldingSvc.Cleanup()
	}
	if paperConnectSvc != nil {
		paperConnectSvc.Cleanup()
	}
	goBackToIdle()
}
