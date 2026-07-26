//go:build cgo

// CGo exported functions — the public C ABI surface of GravityCone FFI.
//
// These functions mirror Terracotta's JNI API with a state-machine design:
//   - gc_init() initializes the engine
//   - gc_get_state() polls current state as JSON
//   - gc_set_waiting / gc_set_scanning / gc_set_guesting() trigger state transitions
//   - gc_shutdown() cleans up
//
// All functions are thread-safe and can be called from any thread.
package main

/*
#include <stdlib.h>
*/
import "C"
import (
	"encoding/json"
	"unsafe"
)

// --- Init / Shutdown ---

//export gc_init
// gc_init initializes the GravityCone engine.
// baseDir: writable directory for logs, machine-id, and EasyTier binaries.
// Returns 0 on success.
func gc_init(baseDir *C.char) C.int {
	dir := C.GoString(baseDir)
	_ = dir // Reserved for future use (EasyTier dir, logging config, machine-id storage)
	return 0
}

//export gc_shutdown
// gc_shutdown stops all active rooms and connections and resets to idle.
func gc_shutdown() {
	goBackToIdle()
}

// --- State Polling ---

//export gc_get_state
// gc_get_state returns the current state as a JSON string.
// The caller MUST free the returned string with gc_free_string().
// Returns NULL if the engine has not been initialized.
func gc_get_state() *C.char {
	jsonStr := getStateJSON()
	return C.CString(jsonStr)
}

// --- State Transitions ---

//export gc_set_waiting
// gc_set_waiting transitions to the idle/waiting state, stopping any active room.
func gc_set_waiting() {
	setWaiting()
}

//export gc_set_scanning
// gc_set_scanning starts scanning for a local Minecraft server and creates a room.
// room: optional room code (pass NULL or empty string to auto-generate).
// player: player name (pass NULL for default "Player").
// protocol: "scaffolding" for Java Edition, "paperconnect" for Bedrock Edition
//           (pass NULL or empty string for default "scaffolding").
func gc_set_scanning(room, player, protocol *C.char) {
	r := ""
	if room != nil {
		r = C.GoString(room)
	}
	p := ""
	if player != nil {
		p = C.GoString(player)
	}
	proto := ""
	if protocol != nil {
		proto = C.GoString(protocol)
	}
	setScanning(r, p, proto)
}

//export gc_set_guesting
// gc_set_guesting starts connecting to a remote room.
// room: room code (required).
// player: player name (pass NULL for default "Player").
// Returns 1 if the room code is valid and the connection process started,
// 0 if the room code is invalid or the engine is not idle.
func gc_set_guesting(room, player *C.char) C.int {
	r := C.GoString(room)
	p := ""
	if player != nil {
		p = C.GoString(player)
	}
	if setGuesting(r, p) {
		return 1
	}
	return 0
}

// --- STUN (NAT Probing) ---

//export gc_stun_probe
// gc_stun_probe runs a STUN NAT type probe via easytier-cli.
// Returns a JSON string with NAT type info.
// The caller MUST free the returned string with gc_free_string().
// This is a blocking call that takes 3-10 seconds.
//
// Response format:
//
//	{
//	  "udp_nat_type": 1,
//	  "tcp_nat_type": 2,
//	  "last_update_time": 1720246800,
//	  "public_ip": ["203.0.113.1"],
//	  "min_port": 30000,
//	  "max_port": 40000
//	}
//
// NAT type values:
//
//	1 = NoPAT (开放型互联网)
//	2 = SymmetricFirewall (对称型防火墙)
//	3 = FullCone (完全圆锥型NAT)
//	4 = RestrictedCone (受限圆锥型NAT)
//	5 = PortRestricted (端口受限圆锥型NAT)
//	6 = SymmetricIncrement (对称型递增NAT)
//	7 = Symmetric (对称型NAT)
//
// Returns JSON with an "error" field on failure:
//
//	{"error": "stun failed: ..."}
func gc_stun_probe() *C.char {
	result := stunProbe()
	return C.CString(result)
}

// --- Utilities ---

//export gc_verify_room_code
// gc_verify_room_code checks the room code type without connecting.
// Returns:
//   -1: invalid
//    3: Scaffolding (Java Edition, compatible with Terracotta)
//    4: PaperConnect (Bedrock Edition)
func gc_verify_room_code(code *C.char) C.int {
	c := C.GoString(code)
	return C.int(verifyRoomCode(c))
}

//export gc_get_metadata
// gc_get_metadata returns version metadata as a JSON string.
// Format: {"version":"0.1.3","compile_time":1720246800000,"easytier_version":"v2.6.4"}
// The caller MUST free the returned string with gc_free_string().
func gc_get_metadata() *C.char {
	meta := Metadata{
		Version:         "0.1.3-alpha",
		CompileTime:     CompileTime.Load(),
		EasyTierVersion: "v2.6.4",
	}
	data, _ := json.Marshal(meta)
	return C.CString(string(data))
}

//export gc_free_string
// gc_free_string frees a string returned by gc_get_state() or gc_get_metadata().
func gc_free_string(s *C.char) {
	C.free(unsafe.Pointer(s))
}

// --- Version ---

//export gc_version
// gc_version returns the FFI ABI version as a simple integer.
// Increment this when the API changes incompatibly.
func gc_version() C.int {
	return 1
}
