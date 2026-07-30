//go:build et_ffi && linux

package main

import (
	ffi_et "gravitycone/ffi/easytier"
)

// init wires the reverse-JNI TUN fd provider into the easytier package.
//
// callJavaVpnServiceCallback is defined in export_jni.go (same package, et_ffi tag).
// It performs the Go→Java JNI call to GravityConeAndroidAPI.onVpnServiceStateChanged,
// which blocks until the Android app establishes a VpnService and returns the TUN fd.
//
// This wiring is only compiled when the et_ffi tag is active (Android FFI builds).
// On non-et_ffi builds (desktop, CLI), this file is excluded and
// DefaultTunFdProvider remains nil (no VpnService needed).
func init() {
	ffi_et.DefaultTunFdProvider = callJavaVpnServiceCallback
}
