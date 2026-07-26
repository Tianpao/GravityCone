// GravityCone FFI — Platform-Agnostic C API
//
// This package implements a state-machine API following Terracotta's pattern:
//
//	Idle -> HostScanning -> HostStarting -> HostReady (host)
//	Idle -> GuestConnecting -> GuestReady          (guest)
//
// All state is communicated as JSON strings, compatible with the CLI protocol
// format where applicable.
//
// This directory is named "common" (shared across platforms) but uses package
// main because Go's -buildmode=c-shared requires package main for exported
// C symbols. The C ABI surface is defined in export.go.
//
// Build: go build -buildmode=c-shared -tags cgo -o libgravitycone.so .
package main

// State names (matching Terracotta's state identifiers).
const (
	StateNameWaiting          = "waiting"
	StateNameHostScanning     = "host-scanning"
	StateNameHostStarting     = "host-starting"
	StateNameHostOk           = "host-ok"
	StateNameGuestConnecting  = "guest-connecting"
	StateNameGuestStarting    = "guest-starting"
	StateNameGuestOk          = "guest-ok"
	StateNameException        = "exception"
)

// Protocol constants for room.create protocol parameter.
const (
	ProtocolScaffolding = "scaffolding"
	ProtocolPaperConnect = "paperconnect"
)

// Room code prefixes.
const (
	PrefixScaffolding  = "U/"
	PrefixPaperConnect = "P/"
)

// Room code verification results (mirrors Terracotta's JNI return values).
const (
	RoomCodeInvalid    = -1
	RoomCodeScaffolding = 3 // Terracotta-compatible value for Scaffolding
	RoomCodePaperConnect = 4 // GravityCone extension for Bedrock
)

// Player info for ScaffoldingMC (Java Edition).
type ScaffoldingPlayerInfo struct {
	Name       string `json:"name"`
	MachineID  string `json:"machine_id"`
	EasyTierID string `json:"easytier_id,omitempty"`
	Vendor     string `json:"vendor"`
	Kind       string `json:"kind"` // "HOST" or "GUEST"
}

// Player info for PaperConnect (Bedrock Edition).
type PCPlayerEntry struct {
	Player     string `json:"player"`
	ClientID   string `json:"clientId"`
	IsRoomHost bool   `json:"isRoomHost"`
}

// --- State JSON structures ---

// WaitingState represents the idle/waiting state.
type WaitingState struct {
	State string `json:"state"`
	Index uint32 `json:"index"`
}

// HostScanningState represents scanning for local Minecraft server.
type HostScanningState struct {
	State string `json:"state"`
	Index uint32 `json:"index"`
}

// HostStartingState represents EasyTier is starting for host.
type HostStartingState struct {
	State string `json:"state"`
	Index uint32 `json:"index"`
	Room  string `json:"room"`
}

// HostOkState represents a successfully created room.
type HostOkState struct {
	State        string      `json:"state"`
	Index        uint32      `json:"index"`
	Protocol     string      `json:"protocol"`
	Room         string      `json:"room"`
	MCPort       uint16      `json:"mc_port,omitempty"`    // Java Edition
	GamePort     int         `json:"game_port,omitempty"`  // Bedrock Edition
	SubProtocol  string      `json:"sub_protocol,omitempty"` // "nethernet" or "raknet" (Bedrock)
}

// GuestConnectingState represents connecting to a remote room.
type GuestConnectingState struct {
	State string `json:"state"`
	Index uint32 `json:"index"`
	Room  string `json:"room"`
	Step  string `json:"step,omitempty"` // "connecting", "waiting_peer", "handshaking", "ready"
}

// GuestStartingState represents EasyTier is starting for guest.
type GuestStartingState struct {
	State      string `json:"state"`
	Index      uint32 `json:"index"`
	Room       string `json:"room"`
	Difficulty string `json:"difficulty,omitempty"` // "EASIEST", "SIMPLE", "MEDIUM", "TOUGH"
}

// GuestOkState represents a successful connection.
type GuestOkState struct {
	State       string `json:"state"`
	Index       uint32 `json:"index"`
	Protocol    string `json:"protocol"`
	SubProtocol string `json:"sub_protocol,omitempty"` // "nethernet" or "raknet" (Bedrock)
	URL         string `json:"url"`                    // "127.0.0.1:port" or "127.0.0.1"
}

// ExceptionState represents an error state.
type ExceptionState struct {
	State    string `json:"state"`
	Index    uint32 `json:"index"`
	Type     int    `json:"type"`
}

// Metadata holds version information (mirrors Terracotta's Metadata).
type Metadata struct {
	Version      string `json:"version"`
	CompileTime  int64  `json:"compile_time"`
	EasyTierVersion string `json:"easytier_version"`
}
