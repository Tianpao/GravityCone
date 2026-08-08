//go:build windows

package utils

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
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

var (
	killOnCloseJobOnce sync.Once
	killOnCloseJob     windows.Handle
)

func AssignJobObject(proc *os.Process) error {
	ph, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(proc.Pid),
	)
	if err != nil {
		return fmt.Errorf("打开进程句柄失败: %w", err)
	}
	defer windows.CloseHandle(ph)
	return windows.AssignProcessToJobObject(killOnCloseJobHandle(), ph)
}

func killOnCloseJobHandle() windows.Handle {
	killOnCloseJobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			slog.Error("创建 Job Object 失败", "error", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		if _, err := windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			slog.Error("设置 Job Object 失败", "error", err)
			windows.CloseHandle(h)
			return
		}
		killOnCloseJob = h
	})
	return killOnCloseJob
}
