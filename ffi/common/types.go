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

import (
	"encoding/json"
	"strings"

	"gravitycone/core/easytier"
)

// State names (matching Terracotta's state identifiers).
const (
	StateNameWaiting         = "waiting"
	StateNameHostScanning    = "host-scanning"
	StateNameHostStarting    = "host-starting"
	StateNameHostOk          = "host-ok"
	StateNameGuestConnecting = "guest-connecting"
	StateNameGuestOk         = "guest-ok"
	StateNameException       = "exception"
)

// Protocol constants for room.create protocol parameter.
const (
	ProtocolScaffolding  = "scaffolding"
	ProtocolPaperConnect = "paperconnect"
)

// Room code verification results (mirrors Terracotta's JNI return values).
const (
	RoomCodeInvalid      = -1
	RoomCodeScaffolding  = 3 // Terracotta-compatible value for Scaffolding
	RoomCodePaperConnect = 4 // GravityCone extension for Bedrock
)

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
	State       string `json:"state"`
	Index       uint32 `json:"index"`
	Protocol    string `json:"protocol"`
	Room        string `json:"room"`
	MCPort      uint16 `json:"mc_port,omitempty"`      // Java Edition
	GamePort    int    `json:"game_port,omitempty"`    // Bedrock Edition
	SubProtocol string `json:"sub_protocol,omitempty"` // "nethernet" or "raknet" (Bedrock)
}

// GuestConnectingState represents connecting to a remote room.
type GuestConnectingState struct {
	State string `json:"state"`
	Index uint32 `json:"index"`
	Room  string `json:"room"`
}

// GuestOkState represents a successful connection.
type GuestOkState struct {
	State            string `json:"state"`
	Index            uint32 `json:"index"`
	Protocol         string `json:"protocol"`
	SubProtocol      string `json:"sub_protocol,omitempty"` // "nethernet" or "raknet" (Bedrock)
	URL              string `json:"url"`                    // "127.0.0.1:port" or "127.0.0.1"
	ConnectionState  string `json:"connection_state,omitempty"`
	ConnectionError  string `json:"connection_error,omitempty"`
	DisconnectReason string `json:"disconnect_reason,omitempty"`
}

// ExceptionState represents an error state.
// Error carries the human-readable failure message (Chinese), so callers can
// surface why a room create/join failed instead of only seeing type=0.
type ExceptionState struct {
	State string `json:"state"`
	Index uint32 `json:"index"`
	Type  int    `json:"type"`
	Error string `json:"error,omitempty"`
}

// Metadata holds version information (mirrors Terracotta's Metadata).
type Metadata struct {
	Version         string `json:"version"`
	CompileTime     int64  `json:"compile_time"`
	EasyTierVersion string `json:"easytier_version"`
}

// currentMetadataJSON renders the version metadata as JSON.
// Shared by the C ABI (gc_get_metadata) and JNI (nativeGetMetadata) exports.
func currentMetadataJSON() string {
	data, _ := json.Marshal(Metadata{
		Version:         strings.TrimPrefix(easytier.AppVersion, "v"),
		CompileTime:     CompileTime.Load(),
		EasyTierVersion: easytier.EasyTierVersion,
	})
	return string(data)
}
