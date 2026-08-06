//go:build windows

package paperconnect

import winsock "gravitycone/core/utils/windows"

// 平台分派：Windows 平台从 utils/windows 取 socket 选项。
func SetBroadcast(fd uintptr) error { return winsock.SetBroadcast(fd) }
func SetReuseAddr(fd uintptr) error { return winsock.SetReuseAddr(fd) }
