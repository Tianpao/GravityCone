//go:build unix

package utils

import (
	"os"
	"os/exec"
)

// NewHiddenCmd creates an exec.Cmd. On Unix platforms there is
// no console popup concern, so this is equivalent to exec.Command.
func NewHiddenCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}

// SetDetachedFlags is a no-op on Unix platforms.
func SetDetachedFlags(_ *exec.Cmd) {}

// PlatformExeName returns the name unchanged on Unix platforms.
func PlatformExeName(name string) string {
	return name
}

// KillProcessTree sends SIGINT to the process on Unix.
func KillProcessTree(proc *os.Process) {
	_ = proc.Signal(os.Interrupt)
}
