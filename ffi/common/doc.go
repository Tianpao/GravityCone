// GravityCone FFI — Platform-Agnostic C API
//
// State flow:
//
//	Idle ──→ HostScanning ──→ HostStarting ──→ HostReady (host creates room)
//	 │
//	 └──→ GuestConnecting ──→ GuestReady (guest joins room)
//	                ↓
//	           Error (exception)
//
// All state is communicated as JSON strings via gc_get_state(). The caller
// polls gc_get_state() periodically (or after triggering a transition) to
// monitor progress.
//
// # Thread Safety
//
// All exported functions are safe to call from any thread.
//
// # Memory Management
//
// Strings returned by gc_get_state() and gc_get_metadata() must be freed
// with gc_free_string(). These strings are allocated by the C runtime and
// are NOT managed by Go's garbage collector.
//
// # Build
//
//	go build -buildmode=c-shared -tags cgo -o libgravitycone.so .
//
// This produces libgravitycone.so (dynamic library) and gravitycone.h
// (auto-generated, but prefer ffi/gravitycone.h for the documented version).
//
// # Integration
//
// For Android: compile with GOOS=android GOARCH=arm64 CGO_ENABLED=1,
// then wrap with JNI. See Terracotta's TerracottaAndroidAPI.java for the
// recommended pattern.
//
// For iOS: compile with GOOS=ios GOARCH=arm64 CGO_ENABLED=1 -buildmode=c-archive.
package main
