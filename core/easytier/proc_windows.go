//go:build windows

package easytier

import proc "gravitycone/core/utils/windows"

// 平台分派：将 utils/windows 的进程工具统一暴露为包内符号，跨平台代码直接调用。
var (
	NewHiddenCmd     = proc.NewHiddenCmd
	PlatformExeName  = proc.PlatformExeName
	SetDetachedFlags = proc.SetDetachedFlags
	KillProcessTree  = proc.KillProcessTree
)
