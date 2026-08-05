package main

import (
	"encoding/json"
	"log"
	"sync/atomic"
	"testing"
)

// resetState resets the global state to Idle for test isolation.
func resetState() {
	globalState.mu.Lock()
	globalState.state = StateIdle
	globalState.extra = nil
	globalState.lastError = ""
	globalState.index = 0
	globalState.mu.Unlock()
}

// --- Test-only state transition helpers ---
//
// Production code transitions exclusively through beginTransition /
// transitionToIfOwner / transitionToErrorIfOwner (ownership-checked).
// These unguarded variants exist so tests can drive the state machine
// directly; they live here rather than in state.go to keep them out of
// the production binary.

// transitionTo atomically transitions to a new state without ownership
// checking.
func transitionTo(newState AppState, extra any) {
	globalState.mu.Lock()
	globalState.index++
	globalState.state = newState
	globalState.extra = extra
	globalState.lastError = ""
	globalState.mu.Unlock()
}

// transitionToError transitions to the error state without ownership
// checking.
func transitionToError(errMsg string) {
	log.Printf("[状态错误] %s", errMsg)
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

// --- Initial state ---

func TestInitialState(t *testing.T) {
	resetState()

	if !isInState(StateIdle) {
		t.Error("initial state should be Idle")
	}
	if !canTransition() {
		t.Error("should be able to transition from Idle")
	}
}

// --- Host lifecycle ---

func TestHostLifecycle(t *testing.T) {
	resetState()

	ctx := &hostContext{
		protocol: ProtocolScaffolding,
		roomCode: "U/TEST-CODE",
		mcPort:   25565,
	}

	transitionTo(StateHostScanning, ctx)
	if !isInState(StateHostScanning) {
		t.Error("should be in HostScanning")
	}
	if canTransition() {
		t.Error("should NOT be able to transition from HostScanning")
	}

	transitionTo(StateHostStarting, ctx)
	if !isInState(StateHostStarting) {
		t.Error("should be in HostStarting")
	}

	transitionTo(StateHostReady, ctx)
	if !isInState(StateHostReady) {
		t.Error("should be in HostReady")
	}

	goBackToIdle()
	if !isInState(StateIdle) {
		t.Error("should be back to Idle after goBackToIdle")
	}
	if !canTransition() {
		t.Error("should be able to transition from Idle again")
	}
}

// --- Guest lifecycle ---

func TestGuestLifecycle(t *testing.T) {
	resetState()

	ctx := &guestContext{
		protocol: ProtocolScaffolding,
		roomCode: "U/TEST-CODE",
		mcURL:    "127.0.0.1:25565",
	}

	transitionTo(StateGuestConnecting, ctx)
	if !isInState(StateGuestConnecting) {
		t.Error("should be in GuestConnecting")
	}
	if canTransition() {
		t.Error("should NOT be able to transition from GuestConnecting")
	}

	transitionTo(StateGuestReady, ctx)
	if !isInState(StateGuestReady) {
		t.Error("should be in GuestReady")
	}

	goBackToIdle()
	if !isInState(StateIdle) {
		t.Error("should be back to Idle")
	}
}

// --- Error state ---

func TestErrorState(t *testing.T) {
	resetState()

	transitionToError("test error")
	if !isInState(StateError) {
		t.Error("should be in Error state")
	}
	if globalState.lastError != "test error" {
		t.Errorf("lastError = %q, want %q", globalState.lastError, "test error")
	}

	// Error state should not allow transitions
	if canTransition() {
		t.Error("should NOT be able to transition from Error")
	}

	// goBackToIdle should work from Error
	goBackToIdle()
	if !isInState(StateIdle) {
		t.Error("should be back to Idle")
	}
}

// --- canTransition guard ---

func TestCanTransitionGuard(t *testing.T) {
	resetState()

	states := []AppState{
		StateHostScanning,
		StateHostStarting,
		StateHostReady,
		StateGuestConnecting,
		StateGuestReady,
		StateError,
	}

	for _, s := range states {
		resetState()
		transitionTo(s, nil)
		if canTransition() {
			t.Errorf("canTransition() should be false in state %d", s)
		}
	}
}

// --- buildStateJSON ---

func TestBuildStateJSON(t *testing.T) {
	tests := []struct {
		name      string
		state     AppState
		index     uint32
		extra     any
		wantState string // expected "state" field value
		wantRoom  string // expected "room" field (empty if absent)
	}{
		{
			name:      "Idle/Waiting",
			state:     StateIdle,
			index:     0,
			extra:     nil,
			wantState: StateNameWaiting,
		},
		{
			name:      "HostScanning",
			state:     StateHostScanning,
			index:     1,
			extra:     nil,
			wantState: StateNameHostScanning,
		},
		{
			name:      "HostStarting with room code",
			state:     StateHostStarting,
			index:     2,
			extra:     &hostContext{roomCode: "U/ABCD-1234-EFGH-5678"},
			wantState: StateNameHostStarting,
			wantRoom:  "U/ABCD-1234-EFGH-5678",
		},
		{
			name:      "HostStarting without room code",
			state:     StateHostStarting,
			index:     3,
			extra:     &hostContext{},
			wantState: StateNameHostStarting,
			wantRoom:  "", // room should be empty string (omitempty)
		},
		{
			name:  "HostReady Scaffolding",
			state: StateHostReady,
			index: 4,
			extra: &hostContext{
				protocol: ProtocolScaffolding,
				roomCode: "U/ABCD-1234-EFGH-5678",
				mcPort:   25565,
			},
			wantState: StateNameHostOk,
			wantRoom:  "U/ABCD-1234-EFGH-5678",
		},
		{
			name:  "HostReady PaperConnect",
			state: StateHostReady,
			index: 5,
			extra: &hostContext{
				protocol:    ProtocolPaperConnect,
				roomCode:    "P/ABCD-1234-EFGH-5678",
				gamePort:    45678,
				subProtocol: "nethernet",
			},
			wantState: StateNameHostOk,
			wantRoom:  "P/ABCD-1234-EFGH-5678",
		},
		{
			name:      "GuestConnecting",
			state:     StateGuestConnecting,
			index:     6,
			extra:     &guestContext{roomCode: "U/TEST"},
			wantState: StateNameGuestConnecting,
			wantRoom:  "U/TEST",
		},
		{
			name:  "GuestReady Scaffolding",
			state: StateGuestReady,
			index: 7,
			extra: &guestContext{
				protocol: ProtocolScaffolding,
				mcURL:    "127.0.0.1:25565",
			},
			wantState: StateNameGuestOk,
		},
		{
			name:  "GuestReady PaperConnect",
			state: StateGuestReady,
			index: 8,
			extra: &guestContext{
				protocol:    ProtocolPaperConnect,
				subProtocol: "raknet",
				mcURL:       "127.0.0.1:45678",
			},
			wantState: StateNameGuestOk,
		},
		{
			name:      "Error",
			state:     StateError,
			index:     9,
			extra:     nil,
			wantState: StateNameException,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildStateJSON(tt.state, tt.index, tt.extra, "")

			// Parse as generic map to check fields
			var m map[string]any
			if err := json.Unmarshal([]byte(got), &m); err != nil {
				t.Fatalf("invalid JSON: %s, err: %v", got, err)
			}

			// Check state field
			if m["state"] != tt.wantState {
				t.Errorf("state = %v, want %v", m["state"], tt.wantState)
			}

			// Check index field
			idx, ok := m["index"].(float64)
			if !ok || uint32(idx) != tt.index {
				t.Errorf("index = %v, want %d", m["index"], tt.index)
			}

			// Check room field if expected
			if tt.wantRoom != "" {
				if m["room"] != tt.wantRoom {
					t.Errorf("room = %v, want %v", m["room"], tt.wantRoom)
				}
			}
		})
	}
}

func TestBuildStateJSONHostOkFields(t *testing.T) {
	// Verify Scaffolding host-ok has mc_port and no game_port
	got := buildStateJSON(StateHostReady, 1, &hostContext{
		protocol: ProtocolScaffolding,
		roomCode: "U/TEST",
		mcPort:   25565,
	}, "")

	var m map[string]any
	json.Unmarshal([]byte(got), &m)

	if m["protocol"] != ProtocolScaffolding {
		t.Errorf("protocol = %v, want %v", m["protocol"], ProtocolScaffolding)
	}
	if m["mc_port"] == nil {
		t.Error("mc_port should be present for Scaffolding host")
	}
	if m["game_port"] != nil {
		t.Error("game_port should be absent for Scaffolding host (omitempty)")
	}

	// Verify PaperConnect host-ok has game_port and sub_protocol
	got = buildStateJSON(StateHostReady, 2, &hostContext{
		protocol:    ProtocolPaperConnect,
		roomCode:    "P/TEST",
		gamePort:    45678,
		subProtocol: "nethernet",
	}, "")

	json.Unmarshal([]byte(got), &m)

	if m["protocol"] != ProtocolPaperConnect {
		t.Errorf("protocol = %v, want %v", m["protocol"], ProtocolPaperConnect)
	}
	if m["game_port"] == nil {
		t.Error("game_port should be present for PaperConnect host")
	}
	if m["sub_protocol"] != "nethernet" {
		t.Errorf("sub_protocol = %v, want nethernet", m["sub_protocol"])
	}
}

func TestBuildStateJSONGuestOkFields(t *testing.T) {
	// Verify Scaffolding guest-ok has url
	got := buildStateJSON(StateGuestReady, 1, &guestContext{
		protocol: ProtocolScaffolding,
		mcURL:    "127.0.0.1:25565",
	}, "")

	var m map[string]any
	json.Unmarshal([]byte(got), &m)

	if m["url"] != "127.0.0.1:25565" {
		t.Errorf("url = %v, want 127.0.0.1:25565", m["url"])
	}
	if m["sub_protocol"] != nil {
		t.Error("sub_protocol should be absent for Scaffolding guest (omitempty)")
	}

	// Verify PaperConnect guest-ok has sub_protocol
	got = buildStateJSON(StateGuestReady, 2, &guestContext{
		protocol:    ProtocolPaperConnect,
		subProtocol: "raknet",
		mcURL:       "127.0.0.1:45678",
	}, "")

	json.Unmarshal([]byte(got), &m)

	if m["sub_protocol"] != "raknet" {
		t.Errorf("sub_protocol = %v, want raknet", m["sub_protocol"])
	}
}

// --- goBackToIdle cleanup callbacks ---

func TestGoBackToIdleCallsStopFn(t *testing.T) {
	resetState()

	called := false
	ctx := &hostContext{
		protocol: ProtocolScaffolding,
		stopFn: func() {
			called = true
		},
	}
	transitionTo(StateHostReady, ctx)

	goBackToIdle()
	if !called {
		t.Error("stopFn should have been called")
	}
	if !isInState(StateIdle) {
		t.Error("should be in Idle after goBackToIdle")
	}
}

func TestGoBackToIdleCallsLeaveFn(t *testing.T) {
	resetState()

	called := false
	ctx := &guestContext{
		protocol: ProtocolScaffolding,
		leaveFn: func() {
			called = true
		},
	}
	transitionTo(StateGuestReady, ctx)

	goBackToIdle()
	if !called {
		t.Error("leaveFn should have been called")
	}
}

func TestGoBackToIdleNilCallbacks(t *testing.T) {
	resetState()

	// Should not panic with nil stopFn/leaveFn
	ctx := &hostContext{protocol: ProtocolScaffolding}
	transitionTo(StateHostReady, ctx)
	goBackToIdle()

	ctx2 := &guestContext{protocol: ProtocolScaffolding}
	transitionTo(StateGuestReady, ctx2)
	goBackToIdle()
}

// --- updateExtra ---

func TestUpdateExtra(t *testing.T) {
	resetState()

	ctx := &hostContext{protocol: ProtocolScaffolding}
	transitionTo(StateHostScanning, ctx)

	initialIndex := globalState.index

	ctx.roomCode = "U/NEW-CODE"
	updateExtra(ctx)

	if globalState.index <= initialIndex {
		t.Error("updateExtra should increment index")
	}
	if globalState.extra != ctx {
		t.Error("updateExtra should replace extra")
	}
}

// --- beginTransition atomicity ---

func TestBeginTransitionFromIdle(t *testing.T) {
	resetState()

	ctx := &hostContext{protocol: ProtocolScaffolding}
	if !beginTransition(StateHostScanning, ctx) {
		t.Error("beginTransition should succeed from Idle")
	}
	if !isInState(StateHostScanning) {
		t.Error("should be in HostScanning after beginTransition")
	}
}

func TestBeginTransitionRejectedWhenActive(t *testing.T) {
	resetState()

	ctx := &hostContext{protocol: ProtocolScaffolding}
	beginTransition(StateHostScanning, ctx)

	// A second beginTransition while a room is active must fail atomically.
	ctx2 := &hostContext{protocol: ProtocolPaperConnect}
	if beginTransition(StateHostScanning, ctx2) {
		t.Error("beginTransition should fail while a room is active")
	}
	if !isInState(StateHostScanning) {
		t.Error("state should be untouched by rejected beginTransition")
	}
	if globalState.extra != ctx {
		t.Error("extra should remain owned by the first session")
	}
}

func TestBeginTransitionRejectedFromError(t *testing.T) {
	resetState()

	transitionToError("boom")
	if beginTransition(StateHostScanning, &hostContext{}) {
		t.Error("beginTransition should fail from Error state")
	}
}

// --- transitionToIfOwner ownership checks ---

func TestTransitionToIfOwnerStaleRejected(t *testing.T) {
	resetState()

	ctx := &hostContext{protocol: ProtocolScaffolding}
	beginTransition(StateHostScanning, ctx)

	// goBackToIdle revokes ownership; a late async transition must not
	// resurrect the cancelled room's state.
	goBackToIdle()

	if transitionToIfOwner(ctx, StateHostReady, ctx) {
		t.Error("stale transition after goBackToIdle should be rejected")
	}
	if !isInState(StateIdle) {
		t.Error("state must remain Idle after stale transition was rejected")
	}
}

func TestTransitionToIfOwnerSucceedsWhileOwner(t *testing.T) {
	resetState()

	ctx := &hostContext{protocol: ProtocolScaffolding}
	beginTransition(StateHostScanning, ctx)

	if !transitionToIfOwner(ctx, StateHostStarting, ctx) {
		t.Error("owner transition should succeed")
	}
	if !isInState(StateHostStarting) {
		t.Error("should be in HostStarting")
	}
}

func TestTransitionToErrorIfOwnerStaleRejected(t *testing.T) {
	resetState()

	ctx := &guestContext{protocol: ProtocolScaffolding}
	beginTransition(StateGuestConnecting, ctx)
	goBackToIdle()

	if transitionToErrorIfOwner(ctx, "late failure") {
		t.Error("stale error transition should be rejected")
	}
	if isInState(StateError) {
		t.Error("state must not become Error from a stale goroutine")
	}
}

func TestUpdateExtraStaleRejected(t *testing.T) {
	resetState()

	ctx := &guestContext{protocol: ProtocolScaffolding}
	beginTransition(StateGuestConnecting, ctx)
	goBackToIdle()

	before := globalState.index
	updateExtra(ctx)
	if globalState.index != before {
		t.Error("stale updateExtra should not touch the state")
	}
	if globalState.extra != nil {
		t.Error("extra should stay nil after stale updateExtra")
	}
}

// --- index increments ---

func TestIndexIncrements(t *testing.T) {
	resetState()

	prev := globalState.index
	transitionTo(StateHostScanning, nil)
	if globalState.index <= prev {
		t.Error("index should increment on transitionTo")
	}

	prev = globalState.index
	transitionToError("err")
	if globalState.index <= prev {
		t.Error("index should increment on transitionToError")
	}

	prev = globalState.index
	goBackToIdle()
	if globalState.index <= prev {
		t.Error("index should increment on goBackToIdle")
	}
}

// --- CompileTime ---

func TestCompileTime(t *testing.T) {
	var ct atomic.Int64
	ct.Store(1234567890)
	SetCompileTime(1234567890)
	if CompileTime.Load() != 1234567890 {
		t.Errorf("CompileTime = %d, want 1234567890", CompileTime.Load())
	}
}

// --- mustJSON ---

func TestMustJSON(t *testing.T) {
	got := mustJSON(WaitingState{State: StateNameWaiting, Index: 42})
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("mustJSON produced invalid JSON: %s", got)
	}
	if m["state"] != StateNameWaiting {
		t.Errorf("state = %v, want %v", m["state"], StateNameWaiting)
	}
	if m["index"] != float64(42) {
		t.Errorf("index = %v, want 42", m["index"])
	}
}

// --- getStateJSON integration ---

func TestGetStateJSON(t *testing.T) {
	resetState()

	got := getStateJSON()
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("getStateJSON produced invalid JSON: %s", got)
	}
	if m["state"] != StateNameWaiting {
		t.Errorf("state = %v, want %v", m["state"], StateNameWaiting)
	}
}
