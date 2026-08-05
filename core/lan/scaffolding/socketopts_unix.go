//go:build !windows

package scaffolding

import unixsock "gravitycone/core/utils/unix"

// 平台分派：非 Windows 平台从 utils/unix 取 socket 选项。
func SetBroadcast(fd uintptr) error { return unixsock.SetBroadcast(fd) }
func SetReuseAddr(fd uintptr) error { return unixsock.SetReuseAddr(fd) }
