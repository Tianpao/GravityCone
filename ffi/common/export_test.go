//go:build !et_ffi

package main

// Export internal symbols for external test packages in test/ffi/.
// This file is only compiled during testing (not in production FFI builds).

// State constants
const (
	FFIStateIdle            = StateIdle
	FFIStateHostScanning    = StateHostScanning
	FFIStateHostStarting    = StateHostStarting
	FFIStateHostReady       = StateHostReady
	FFIStateGuestConnecting = StateGuestConnecting
	FFIStateGuestReady      = StateGuestReady
	FFIStateError           = StateError
)

// State name constants
const (
	FFIStateNameWaiting         = StateNameWaiting
	FFIStateNameHostScanning    = StateNameHostScanning
	FFIStateNameHostStarting    = StateNameHostStarting
	FFIStateNameHostOk          = StateNameHostOk
	FFIStateNameGuestConnecting = StateNameGuestConnecting
	FFIStateNameGuestOk         = StateNameGuestOk
	FFIStateNameException       = StateNameException
)

// Protocol constants
const (
	FFIProtocolScaffolding  = ProtocolScaffolding
	FFIProtocolPaperConnect = ProtocolPaperConnect
	FFIRoomCodeInvalid      = RoomCodeInvalid
	FFIRoomCodeScaffolding  = RoomCodeScaffolding
	FFIRoomCodePaperConnect = RoomCodePaperConnect
)

// State machine functions
func FFIResetState()                                         { resetState() }
func FFIIsInState(s AppState) bool                           { return isInState(s) }
func FFICanTransition() bool                                 { return canTransition() }
func FFITransitionTo(s AppState, e interface{})                  { transitionTo(s, e) }
func FFIBeginTransition(s AppState, e interface{}) bool          { return beginTransition(s, e) }
func FFITransitionToIfOwner(ctx interface{}, s AppState, e interface{}) bool {
	return transitionToIfOwner(ctx, s, e)
}
func FFITransitionToError(msg string)                        { transitionToError(msg) }
func FFIGoBackToIdle()                                       { goBackToIdle() }
func FFIUpdateExtra(e interface{})                           { updateExtra(e) }
func FFISetBaseDir(dir string)                               { setBaseDir(dir) }
func FFIGetBaseDir() string                                  { return getBaseDir() }
func FFIGetStateJSON() string                                { return getStateJSON() }
func FFIBuildStateJSON(s AppState, idx uint32, e interface{}, err string) string {
	return buildStateJSON(s, idx, e, err)
}

// Bridge functions
func FFIIsPaperConnectCode(code string) bool       { return isPaperConnectCode(code) }
func FFIVerifyRoomCode(code string) int            { return verifyRoomCode(code) }
func FFISetWaiting()                               { setWaiting() }
func FFISetScanning(room, player, protocol string) { setScanning(room, player, protocol) }
func FFISetGuesting(roomCode, player string) bool  { return setGuesting(roomCode, player) }

// Global state accessor (for testing index values)
func FFIGetGlobalStateIndex() uint32 {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()
	return globalState.index
}

func FFIGetGlobalStateLastError() string {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()
	return globalState.lastError
}

func FFIGetGlobalStateExtra() interface{} {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()
	return globalState.extra
}
