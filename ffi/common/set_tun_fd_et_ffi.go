//go:build et_ffi

package main

import (
	ffi_et "gravitycone/ffi/easytier"
)

// setTunFdBridge delegates to ffi/easytier.SetTunFd on Android (et_ffi) builds.
func setTunFdBridge(instName string, fd int) error {
	return ffi_et.SetTunFd(instName, fd)
}
