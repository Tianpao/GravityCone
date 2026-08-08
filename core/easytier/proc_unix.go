//go:build unix

package easytier

import proc "gravitycone/core/utils/unix"

// 平台分派：将 utils/unix 的进程工具统一暴露为包内符号，跨平台代码直接调用。
var (
	NewHiddenCmd     = proc.NewHiddenCmd
	PlatformExeName  = proc.PlatformExeName
	SetDetachedFlags = proc.SetDetachedFlags
	KillProcessTree  = proc.KillProcessTree
	AssignJobObject  = proc.AssignJobObject
)
