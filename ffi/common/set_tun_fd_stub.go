//go:build !et_ffi

package main

// setTunFdBridge is a no-op on non-Android builds (desktop, CLI).
// TUN fd injection via VpnService is only needed on Android.
func setTunFdBridge(instName string, fd int) error {
	return nil
}
