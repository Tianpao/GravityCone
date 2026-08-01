package main

import (
	"testing"

	"gravitycone/core/protocol/paperconnect"
	"gravitycone/core/protocol/scaffolding"
)

// --- isPaperConnectCode ---

func TestIsPaperConnectCode(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"P/ABCD-1234-EFGH-5678", true},
		{"p/ABCD-1234-EFGH-5678", true},
		{"U/ABCD-1234-EFGH-5678", false},
		{"u/ABCD-1234-EFGH-5678", false},
		{"X/ABCD-1234-EFGH-5678", false},
		{"P/", true},      // minimum valid prefix
		{"p/", true},      // minimum valid prefix (lowercase)
		{"P", false},      // too short
		{"", false},       // empty
		{"/P/xxx", false}, // slash before P
		{"AP/xxx", false}, // P not at position 0
	}

	for _, tt := range tests {
		got := isPaperConnectCode(tt.code)
		if got != tt.want {
			t.Errorf("isPaperConnectCode(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

// --- verifyRoomCode ---

func TestVerifyRoomCodeInvalid(t *testing.T) {
	tests := []struct {
		name string
		code string
	}{
		{"empty string", ""},
		{"random garbage", "NOTACODE"},
		{"wrong prefix", "X/1234-5678-9012-3456"},
		{"too short", "U/AB"},
		{"invalid chars", "U/IIII-IIII-IIII-IIII"}, // I is not in charset
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := verifyRoomCode(tt.code)
			if got != RoomCodeInvalid {
				t.Errorf("verifyRoomCode(%q) = %d, want %d (RoomCodeInvalid)", tt.code, got, RoomCodeInvalid)
			}
		})
	}
}

func TestVerifyRoomCodeScaffolding(t *testing.T) {
	// Generate a valid Scaffolding room code
	rc, err := scaffolding.GenerateRoomCode()
	if err != nil {
		t.Fatalf("GenerateRoomCode failed: %v", err)
	}
	code := rc.Format()

	got := verifyRoomCode(code)
	if got != RoomCodeScaffolding {
		t.Errorf("verifyRoomCode(%q) = %d, want %d (RoomCodeScaffolding)", code, got, RoomCodeScaffolding)
	}

	// Also test without U/ prefix (ParseRoomCode accepts bare codes)
	bareCode := code[2:] // strip "U/"
	got2 := verifyRoomCode(bareCode)
	if got2 != RoomCodeScaffolding {
		t.Errorf("verifyRoomCode(%q) = %d, want %d (RoomCodeScaffolding without prefix)", bareCode, got2, RoomCodeScaffolding)
	}
}

func TestVerifyRoomCodePaperConnect(t *testing.T) {
	// Generate a valid PaperConnect room code
	rc, err := paperconnect.GeneratePaperConnectRoomCode()
	if err != nil {
		t.Fatalf("GeneratePaperConnectRoomCode failed: %v", err)
	}
	code := rc.Format()

	got := verifyRoomCode(code)
	if got != RoomCodePaperConnect {
		t.Errorf("verifyRoomCode(%q) = %d, want %d (RoomCodePaperConnect)", code, got, RoomCodePaperConnect)
	}
}

func TestVerifyRoomCodePaperConnectInvalidChecksum(t *testing.T) {
	// Generate a valid code and corrupt it
	rc, err := paperconnect.GeneratePaperConnectRoomCode()
	if err != nil {
		t.Fatalf("GeneratePaperConnectRoomCode failed: %v", err)
	}
	code := rc.Format()

	// Corrupt the last character (change checksum)
	corrupted := code[:len(code)-1] + "0"
	if corrupted == code {
		corrupted = code[:len(code)-1] + "1"
	}

	got := verifyRoomCode(corrupted)
	if got != RoomCodeInvalid {
		t.Errorf("verifyRoomCode(corrupted %q) = %d, want %d (RoomCodeInvalid)", corrupted, got, RoomCodeInvalid)
	}
}

// --- setWaiting ---

func TestSetWaiting(t *testing.T) {
	resetState()

	// Transition to a non-Idle state first
	transitionTo(StateHostScanning, &hostContext{protocol: ProtocolScaffolding})
	if isInState(StateIdle) {
		t.Fatal("precondition: should not be in Idle")
	}

	setWaiting()
	if !isInState(StateIdle) {
		t.Error("setWaiting should transition to Idle")
	}
}

func TestSetWaitingFromIdle(t *testing.T) {
	resetState()

	// setWaiting from Idle should be a no-op (not panic)
	setWaiting()
	if !isInState(StateIdle) {
		t.Error("should still be in Idle")
	}
}

// --- setScanning guard ---

func TestSetScanningGuard(t *testing.T) {
	resetState()

	// First call should succeed (state is Idle)
	// We can't fully test setScanning without real services,
	// but we can test the canTransition guard
	transitionTo(StateHostScanning, nil)

	// setScanning should be a no-op when not Idle
	// (it checks canTransition() and returns early)
	setScanning("", "Player", ProtocolScaffolding)
	// State should still be HostScanning (not changed by setScanning)
	if !isInState(StateHostScanning) {
		t.Error("setScanning should be no-op when not Idle")
	}
}

// --- setGuesting guard ---

func TestSetGuestingGuard(t *testing.T) {
	resetState()

	transitionTo(StateGuestConnecting, nil)

	// setGuesting should return false when not Idle
	ok := setGuesting("U/TEST", "Player")
	if ok {
		t.Error("setGuesting should return false when not Idle")
	}
}
