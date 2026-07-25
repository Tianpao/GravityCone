//go:build windows

package process

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
)

const (
	winDetachedProcess = 0x00000008
	winCreateNoWindow  = 0x08000000
)

// NewHiddenCmd creates an exec.Cmd with CREATE_NO_WINDOW on Windows
// to suppress console popups.
func NewHiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winCreateNoWindow,
	}
	return cmd
}

// SetDetachedFlags sets DETACHED_PROCESS creation flags on the cmd
// for starting subprocesses in a detached process group on Windows.
func SetDetachedFlags(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winDetachedProcess,
	}
}

// PlatformExeName appends .exe on Windows.
func PlatformExeName(name string) string {
	return name + ".exe"
}

// KillProcessTree kills the process and its entire process tree on Windows
// using taskkill /T /F.
func KillProcessTree(proc *os.Process) {
	killCmd := NewHiddenCmd("taskkill", "/PID", fmt.Sprintf("%d", proc.Pid), "/T", "/F")
	if out, err := killCmd.CombinedOutput(); err != nil {
		slog.Error("taskkill failed", "pid", proc.Pid, "error", err, "output", string(out))
	}
}
