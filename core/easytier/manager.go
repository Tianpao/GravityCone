//go:build !et_ffi

package easytier

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gravitycone/core/utils"
)

const hostVirtualIP = "10.144.144.1"

// easytierLogOutput controls where easytier-core stdout/stderr is written.
// Defaults to os.Stdout/os.Stderr. Override with SetEasyTierLogOutput.
var (
	easytierStdout io.Writer = os.Stdout
	easytierStderr io.Writer = os.Stderr

	customEasyTierDir string
)

// SetCustomEasyTierDir sets a custom directory for locating EasyTier binaries.
// When set, resolveEasyTierBinary searches this directory instead of the default path.
func SetCustomEasyTierDir(dir string) {
	customEasyTierDir = dir
}

// SetEasyTierLogOutput redirects easytier-core process output to the given file path.
// Pass empty string to reset to default (os.Stdout/os.Stderr).
func SetEasyTierLogOutput(path string) {
	if path == "" {
		if f, ok := easytierStdout.(*os.File); ok && f != os.Stdout {
			f.Close()
		}
		easytierStdout = os.Stdout
		easytierStderr = os.Stderr
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		slog.Warn("failed to open easytier log file", "path", path, "error", err)
		return
	}
	if old, ok := easytierStdout.(*os.File); ok && old != os.Stdout {
		old.Close()
	}
	easytierStdout = f
	easytierStderr = f
}

type EasyTierManager struct {
	corePath  string
	cliPath   string
	cmd       *exec.Cmd
	rpcPortal string
	virtualIP string
	mu        sync.Mutex
}

func NewEasyTierManager() (*EasyTierManager, error) {
	corePath, err := resolveEasyTierBinary("easytier-core")
	if err != nil {
		return nil, fmt.Errorf("未找到 easytier-core: %w", err)
	}
	cliPath, err := resolveEasyTierBinary("easytier-cli")
	if err != nil {
		return nil, fmt.Errorf("未找到 easytier-cli: %w", err)
	}
	return &EasyTierManager{corePath: corePath, cliPath: cliPath}, nil
}

func resolveEasyTierBinary(name string) (string, error) {
	exeName := PlatformExeName(name)

	if p, err := exec.LookPath(exeName); err == nil {
		return p, nil
	}

	// Custom directory takes priority over the default base dir
	if customEasyTierDir != "" {
		p := filepath.Join(customEasyTierDir, exeName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	baseDir := easyTierBaseDir()
	if baseDir != "" {
		p := filepath.Join(baseDir, exeName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("%s not found", exeName)
}

func allocateRPCPort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

func (m *EasyTierManager) Start(opts StartOptions) (string, error) {
	m.mu.Lock()
	if m.cmd != nil && m.cmd.Process != nil {
		if m.cmd.ProcessState == nil || !m.cmd.ProcessState.Exited() {
			m.mu.Unlock()
			return "", fmt.Errorf("EasyTier 已在运行")
		}
	}
	m.mu.Unlock()

	rpcPortal, err := allocateRPCPort()
	if err != nil {
		return "", fmt.Errorf("分配RPC端口失败: %w", err)
	}

	args := []string{
		"--network-name", opts.NetworkName,
		"--network-secret", opts.NetworkSecret,
		"--multi-thread",
		"--rpc-portal", rpcPortal,
		"--console-log-level", "info",
	}
	if opts.UpstreamCompatible {
		args = append(args, "--no-tun", "--disable-p2p", strconv.FormatBool(opts.DisableP2P))
	} else {
		args = append(args,
			"--no-tun",
			"--enable-kcp-proxy",
			"--enable-quic-proxy",
			"--latency-first",
			"--encryption-algorithm", "aes-gcm",
			"--compression", "zstd",
			"--default-protocol", "tcp",
			"--private-mode", "true",
			"--disable-p2p", strconv.FormatBool(opts.DisableP2P),
		)
	}

	if opts.IsHost {
		args = append(args,
			"-i", hostVirtualIP,
			"--hostname", opts.Hostname,
		)
		if !opts.UpstreamCompatible {
			args = append(args,
				"--tcp-whitelist", fmt.Sprintf("%d", opts.TCPPort),
				"--udp-whitelist", fmt.Sprintf("%d", opts.TCPPort),
			)
			if opts.MCPort != 0 {
				args = append(args,
					"--tcp-whitelist", fmt.Sprintf("%d", opts.MCPort),
					"--udp-whitelist", fmt.Sprintf("%d", opts.MCPort),
				)
			}
		}
	} else {
		args = append(args, "--dhcp")
		if !opts.UpstreamCompatible {
			args = append(args,
				"--tcp-whitelist", "0",
				"--udp-whitelist", "0",
			)
		}
	}

	args = append(args, "-l=tcp://0.0.0.0:0", "-l=udp://0.0.0.0:0")

	if opts.ConfigPath != "" {
		args = append(args, "-c", opts.ConfigPath)
	}

	for _, pf := range opts.PortForwards {
		args = append(args, "--port-forward", pf)
	}

	for _, p := range opts.Peers {
		args = append(args, "-p", p)
	}

	machineID, err := utils.GetMachineID()
	if err == nil {
		args = append(args, "--machine-id", machineID)
	}

	cmd := NewHiddenCmd(m.corePath, args...)
	cmd.Stdout = easytierStdout
	cmd.Stderr = easytierStderr

	SetDetachedFlags(cmd)

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动 easytier-core 失败: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.rpcPortal = rpcPortal
	m.mu.Unlock()

	virtualIP, err := m.waitForVirtualIP(30 * time.Second)
	if err != nil {
		m.Stop()
		return "", err
	}

	m.mu.Lock()
	m.virtualIP = virtualIP
	m.mu.Unlock()

	return virtualIP, nil
}

func (m *EasyTierManager) waitForVirtualIP(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		exited := m.cmd != nil && m.cmd.ProcessState != nil && m.cmd.ProcessState.Exited()
		m.mu.Unlock()
		if exited {
			return "", fmt.Errorf("easytier-core 进程已退出")
		}

		ip, err := m.getSelfVirtualIP()
		if err == nil && ip != "" {
			return ip, nil
		}

		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("等待获取虚拟IP超时")
}

func (m *EasyTierManager) getSelfVirtualIP() (string, error) {
	out, err := m.runCli("-o", "json", "-p", m.RPCPortal(), "node", "info")
	if err != nil {
		return "", err
	}

	var info struct {
		VirtualIP string `json:"ipv4_addr"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", err
	}
	return stripCIDR(info.VirtualIP), nil
}

func (m *EasyTierManager) Stop() error {
	m.mu.Lock()
	if m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}
	cmd := m.cmd
	m.mu.Unlock()

	KillProcessTree(cmd.Process)

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		slog.Warn("easytier-core did not exit after 5s, force-killing", "pid", cmd.Process.Pid)
		cmd.Process.Kill()
		<-done
	}

	m.mu.Lock()
	m.cmd = nil
	m.virtualIP = ""
	m.rpcPortal = ""
	m.mu.Unlock()
	return nil
}

func (m *EasyTierManager) IsRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cmd == nil || m.cmd.Process == nil {
		return false
	}
	if m.cmd.ProcessState != nil && m.cmd.ProcessState.Exited() {
		return false
	}
	return true
}

func (m *EasyTierManager) SelfVirtualIP() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.virtualIP
}

func (m *EasyTierManager) RPCPortal() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rpcPortal
}

// HasTUN 是否提供 TUN 直通虚拟 IP。桌面虽有 TUN 但流程围绕端口转发设计，
// 故返回 false 保持原路径；FFI/Android（VpnService）返回 true。
func (m *EasyTierManager) HasTUN() bool {
	return false
}

// DialMode reports the transport used by PaperConnect. Desktop PaperConnect
// uses EasyTier port forwards instead of dialing virtual IPs directly.
func (m *EasyTierManager) DialMode() DialMode {
	return DialModeProxy
}

// stripCIDR removes the CIDR suffix from an IP address (e.g. "10.144.0.1/24" -> "10.144.0.1").
func stripCIDR(ip string) string {
	if i := strings.IndexByte(ip, '/'); i >= 0 {
		return ip[:i]
	}
	return ip
}

// easyTierBaseDir returns the shared easytier binary directory, or empty string if unavailable.
func easyTierBaseDir() string {
	if configDir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(configDir, "GravityCone", "easytier")
	}
	if exeDir, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exeDir), "easytier")
	}
	return ""
}
