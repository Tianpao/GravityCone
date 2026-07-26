package main

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"
)

// AppState represents the current application state (mirrors Terracotta's AppState enum).
type AppState int

const (
	StateIdle AppState = iota
	StateHostScanning
	StateHostStarting
	StateHostReady
	StateGuestConnecting
	StateGuestReady
	StateError
)

// stateHolder is the global state container protected by a mutex.
type stateHolder struct {
	mu        sync.Mutex
	index     uint32       // incremented on each state change
	state     AppState
	extra     interface{}  // protocol-specific state data
	lastError string
}

var globalState = &stateHolder{
	index: 0,
	state: StateIdle,
}

// getStateJSON returns the current state serialized as a JSON string.
func getStateJSON() string {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()

	return buildStateJSON(globalState.state, globalState.index, globalState.extra, globalState.lastError)
}

func buildStateJSON(state AppState, index uint32, extra interface{}, lastError string) string {
	switch state {
	case StateIdle:
		return mustJSON(WaitingState{State: StateNameWaiting, Index: index})

	case StateHostScanning:
		return mustJSON(HostScanningState{State: StateNameHostScanning, Index: index})

	case StateHostStarting:
		hs := HostStartingState{State: StateNameHostStarting, Index: index}
		if s, ok := extra.(*hostContext); ok && s.roomCode != "" {
			hs.Room = s.roomCode
		}
		return mustJSON(hs)

	case StateHostReady:
		ho := HostOkState{State: StateNameHostOk, Index: index}
		if s, ok := extra.(*hostContext); ok {
			ho.Room = s.roomCode
			ho.Protocol = s.protocol
			ho.MCPort = s.mcPort
			ho.GamePort = s.gamePort
			ho.SubProtocol = s.subProtocol
		}
		return mustJSON(ho)

	case StateGuestConnecting:
		gc := GuestConnectingState{State: StateNameGuestConnecting, Index: index}
		if s, ok := extra.(*guestContext); ok {
			gc.Room = s.roomCode
		}
		return mustJSON(gc)

	case StateGuestReady:
		gr := GuestOkState{State: StateNameGuestOk, Index: index}
		if s, ok := extra.(*guestContext); ok {
			gr.Protocol = s.protocol
			gr.SubProtocol = s.subProtocol
			gr.URL = s.mcURL
		}
		return mustJSON(gr)

	case StateError:
		es := ExceptionState{State: StateNameException, Index: index}
		return mustJSON(es)

	default:
		return mustJSON(WaitingState{State: StateNameWaiting, Index: index})
	}
}

// hostContext holds protocol-agnostic host state.
type hostContext struct {
	protocol    string // "scaffolding" or "paperconnect"
	subProtocol string // "nethernet" or "raknet" (PaperConnect only)
	roomCode    string
	mcPort      uint16 // ScaffoldingMC
	gamePort    int    // PaperConnect
	// Service references for cleanup
	scaffoldingSvc interface{}
	paperConnectSvc interface{}
	stopFn         func()
}

// guestContext holds protocol-agnostic guest state.
type guestContext struct {
	protocol    string
	subProtocol string // "nethernet" or "raknet" (PaperConnect only)
	roomCode    string
	mcURL       string // "127.0.0.1:port" or "127.0.0.1"
	// Service references for cleanup
	scaffoldingSvc  interface{}
	paperConnectSvc interface{}
	leaveFn         func()
}

// --- State transition helpers ---

type stateCapture struct {
	index uint32
}

// transitionTo atomically transitions to a new state.
// Returns a capture that the caller can use to verify it still owns the state.
func transitionTo(newState AppState, extra interface{}) stateCapture {
	globalState.mu.Lock()
	globalState.index++
	globalState.state = newState
	globalState.extra = extra
	globalState.lastError = ""
	capture := stateCapture{index: globalState.index}
	globalState.mu.Unlock()
	return capture
}

// transitionToError transitions to the error state.
func transitionToError(errMsg string) {
	globalState.mu.Lock()
	globalState.index++
	globalState.state = StateError
	globalState.lastError = errMsg
	globalState.mu.Unlock()
}

// canTransition returns true if the current state allows a transition.
// Only transitions from Idle are allowed (single room at a time).
func canTransition() bool {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()
	return globalState.state == StateIdle
}

// isInState checks if we are currently in the given state.
func isInState(s AppState) bool {
	globalState.mu.Lock()
	defer globalState.mu.Unlock()
	return globalState.state == s
}

// goBackToIdle returns to idle, stopping any active EasyTier instances.
func goBackToIdle() {
	globalState.mu.Lock()
	oldExtra := globalState.extra
	globalState.state = StateIdle
	globalState.extra = nil
	globalState.index++
	globalState.mu.Unlock()

	// Cleanup old state outside the lock.
	if oldExtra != nil {
		switch ctx := oldExtra.(type) {
		case *hostContext:
			if ctx.stopFn != nil {
				ctx.stopFn()
			}
		case *guestContext:
			if ctx.leaveFn != nil {
				ctx.leaveFn()
			}
		}
	}
}

// updateExtra atomically replaces the extra context on the current state.
// Used by async operations to update progress.
func updateExtra(extra interface{}) {
	globalState.mu.Lock()
	globalState.extra = extra
	globalState.index++
	globalState.mu.Unlock()
}

// mustJSON marshals a value to JSON string, panicking on error.
func mustJSON(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		// Should never happen with our simple structs.
		return `{"error":"json marshal failed"}`
	}
	return string(data)
}

// CompileTime is set via ldflags at build time.
var CompileTime atomic.Int64

// SetCompileTime records the build timestamp for metadata.
func SetCompileTime(t int64) {
	CompileTime.Store(t)
}

func init() {
	CompileTime.Store(time.Now().UnixMilli())
}
