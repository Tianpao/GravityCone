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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"gravitycone/core/utils"
)

const hostVirtualIP = "10.144.144.1"

var publicPeers = []string{
	"https://etnode.zkitefly.eu.org/node1",
}

// SetPublicPeers replaces the default public peer list used when starting EasyTier.
func SetPublicPeers(peers []string) {
	if len(peers) > 0 {
		publicPeers = peers
	}
}

// AddPublicPeers appends peer addresses to the public peer list.
func AddPublicPeers(peers []string) {
	publicPeers = append(publicPeers, peers...)
}

// easytierLogOutput controls where easytier-core stdout/stderr is written.
// Defaults to os.Stdout/os.Stderr. Override with SetEasyTierLogOutput.
var (
	easytierStdout io.Writer = os.Stdout
	easytierStderr io.Writer = os.Stderr
)

// SetEasyTierLogOutput redirects easytier-core process output to the given file path.
// Pass empty string to reset to default (os.Stdout/os.Stderr).
func SetEasyTierLogOutput(path string) {
	if path == "" {
		easytierStdout = os.Stdout
		easytierStderr = os.Stderr
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		slog.Warn("failed to open easytier log file", "path", path, "error", err)
		return
	}
	easytierStdout = f
	easytierStderr = f
}

type EasyTierManager struct {
	corePath  string
	cliPath   string
	cmd       *exec.Cmd
	rpcPortal string // e.g. "127.0.0.1:15888"
	virtualIP string
	mu        sync.Mutex
	running   bool
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
	exeName := name
	if runtime.GOOS == "windows" {
		exeName = name + ".exe"
	}

	if p, err := exec.LookPath(exeName); err == nil {
		return p, nil
	}

	// Check config directory (shared across installations)
	if configDir, err := os.UserConfigDir(); err == nil {
		p := filepath.Join(configDir, "GravityCone", "easytier", exeName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	// Fallback: next to executable
	if exeDir, err := os.Executable(); err == nil {
		p := filepath.Join(filepath.Dir(exeDir), "easytier", exeName)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("%s not found", exeName)
}

// allocateRPCPort finds a free TCP port on localhost.
func allocateRPCPort() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return fmt.Sprintf("127.0.0.1:%d", port), nil
}

type StartOptions struct {
	NetworkName        string
	NetworkSecret      string
	Hostname           string // HOST only; GUEST leaves empty
	IsHost             bool
	TCPPort            uint16   // HOST only: scaffolding TCP port, used for whitelist
	MCPort             uint16   // HOST only: MC server port, used for whitelist
	ConfigPath         string   // Path to TOML ACL config file (adds -c flag)
	PortForwards       []string // Port forward entries (e.g. "tcp://0.0.0.0:12345/10.144.144.1:12345")
	UpstreamCompatible bool     // Use the original PaperConnect EasyTier argument profile.
}

func (m *EasyTierManager) Start(opts StartOptions) (string, error) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return "", fmt.Errorf("EasyTier 已在运行")
	}
	m.mu.Unlock()

	// Allocate a dedicated RPC port for this instance
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
		args = append(args, "--no-tun", "--disable-p2p", "false")
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
			"--p2p-only",
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

	for _, p := range publicPeers {
		args = append(args, "-p", p)
	}

	machineID, err := utils.GetMachineID()
	if err == nil {
		args = append(args, "--machine-id", machineID)
	}

	cmd := exec.Command(m.corePath, args...)
	cmd.Stdout = easytierStdout
	cmd.Stderr = easytierStderr

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("启动 easytier-core 失败: %w", err)
	}

	m.mu.Lock()
	m.cmd = cmd
	m.rpcPortal = rpcPortal
	m.running = true
	m.mu.Unlock()

	// Poll cli until the virtual IP is available
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
	out, err := m.runCli("-o", "json", "-p", m.rpcPortal, "node", "info")
	if err != nil {
		return "", err
	}

	var info struct {
		VirtualIP string `json:"ipv4_addr"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", err
	}
	// Remove CIDR suffix if present (e.g. "10.144.0.1/24" -> "10.144.0.1")
	ip, _, _ := strings.Cut(info.VirtualIP, "/")
	return ip, nil
}

func (m *EasyTierManager) Stop() error {
	m.mu.Lock()
	if !m.running || m.cmd == nil || m.cmd.Process == nil {
		m.mu.Unlock()
		return nil
	}
	pid := m.cmd.Process.Pid
	m.mu.Unlock()

	// Kill the process tree outside of the lock to avoid holding it during I/O.
	if runtime.GOOS == "windows" {
		killCmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
		if out, err := killCmd.CombinedOutput(); err != nil {
			slog.Error("taskkill failed", "pid", pid, "error", err, "output", string(out))
		}
	} else {
		m.mu.Lock()
		_ = m.cmd.Process.Signal(os.Interrupt)
		m.mu.Unlock()
	}

	// Wait for the process to actually exit.
	done := make(chan struct{})
	go func() {
		m.mu.Lock()
		if m.cmd != nil {
			m.cmd.Wait()
		}
		m.mu.Unlock()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		m.mu.Lock()
		if m.cmd != nil && m.cmd.Process != nil {
			slog.Warn("easytier-core did not exit after 5s, force-killing", "pid", pid)
			m.cmd.Process.Kill()
		}
		m.mu.Unlock()
		<-done
	}

	m.mu.Lock()
	m.cmd = nil
	m.running = false
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
		m.running = false
		return false
	}
	return m.running
}

func (m *EasyTierManager) SelfVirtualIP() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.virtualIP
}

type peerInfo struct {
	PeerID    json.RawMessage `json:"id"`
	VirtualIP string          `json:"ipv4"`
	Hostname  string          `json:"hostname"`
}

func (m *EasyTierManager) DiscoverPeer(hostname string) (string, error) {
	out, err := m.runCli("-o", "json", "-p", m.rpcPortal, "peer", "list")
	if err != nil {
		return "", fmt.Errorf("查询对等节点失败: %w", err)
	}

	var peers []peerInfo
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		return "", fmt.Errorf("解析对等节点列表失败: %w", err)
	}

	for _, p := range peers {
		if p.Hostname == hostname && p.VirtualIP != "" {
			// Remove CIDR suffix if present (e.g. "10.144.0.1/24" -> "10.144.0.1")
			ip, _, _ := strings.Cut(p.VirtualIP, "/")
			return ip, nil
		}
	}

	return "", fmt.Errorf("未找到主机 (%s)，请确认房间代码正确", hostname)
}

// DiscoverPeerByPrefix finds a peer whose hostname starts with the given prefix.
// Returns the matching hostname and virtual IP.
func (m *EasyTierManager) DiscoverPeerByPrefix(hostnamePrefix string) (hostname string, virtualIP string, err error) {
	out, err := m.runCli("-o", "json", "-p", m.rpcPortal, "peer", "list")
	if err != nil {
		return "", "", fmt.Errorf("查询对等节点失败: %w", err)
	}

	var peers []peerInfo
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		return "", "", fmt.Errorf("解析对等节点列表失败: %w", err)
	}

	for _, p := range peers {
		if strings.HasPrefix(p.Hostname, hostnamePrefix) && p.VirtualIP != "" {
			ip, _, _ := strings.Cut(p.VirtualIP, "/")
			return p.Hostname, ip, nil
		}
	}

	return "", "", fmt.Errorf("未找到主机 (前缀 %s)，请确认房间代码正确", hostnamePrefix)
}

func (m *EasyTierManager) GetPeerID() (string, error) {
	out, err := m.runCli("-o", "json", "-p", m.rpcPortal, "node", "info")
	if err != nil {
		return "", err
	}

	var info struct {
		PeerID json.RawMessage `json:"peer_id"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", err
	}
	return string(info.PeerID), nil
}

func (m *EasyTierManager) RPCPortal() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rpcPortal
}

func (m *EasyTierManager) AddPortForward(proto string, localAddr string, remoteAddr string) error {
	for attempt := 0; attempt < 3; attempt++ {
		out, err := m.runCli(
			"-p", m.rpcPortal,
			"port-forward", "add",
			proto, localAddr, remoteAddr,
		)
		if err != nil {
			if attempt < 2 {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return fmt.Errorf("添加端口转发失败 (%s %s -> %s): %w, output: %s", proto, localAddr, remoteAddr, err, out)
		}
		return nil
	}
	return nil
}

func (m *EasyTierManager) RemovePortForward(proto string, localAddr string, remoteAddr string) error {
	for attempt := 0; attempt < 3; attempt++ {
		out, err := m.runCli(
			"-p", m.rpcPortal,
			"port-forward", "remove",
			proto, localAddr, remoteAddr,
		)
		if err != nil {
			if attempt < 2 {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return fmt.Errorf("删除端口转发失败 (%s %s -> %s): %w, output: %s", proto, localAddr, remoteAddr, err, out)
		}
		return nil
	}
	return nil
}

// FindPeerByHostnamePrefix scans the EasyTier peer list and returns the virtual IP
// and port of the first peer whose hostname starts with the given prefix.
func (m *EasyTierManager) FindPeerByHostnamePrefix(hostnamePrefix string) (string, uint16, error) {
	out, err := m.runCli("-o", "json", "-p", m.RPCPortal(), "peer", "list")
	if err != nil {
		return "", 0, fmt.Errorf("查询对等节点失败: %w", err)
	}

	var peers []peerInfo
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		return "", 0, fmt.Errorf("解析对等节点列表失败: %w", err)
	}

	for _, p := range peers {
		if !strings.HasPrefix(p.Hostname, hostnamePrefix) || p.VirtualIP == "" {
			continue
		}
		portStr := p.Hostname[len(hostnamePrefix):]
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil || port <= 1024 || port > 65535 {
			continue
		}
		return p.VirtualIP, uint16(port), nil
	}

	return "", 0, fmt.Errorf("未找到联机中心，请确认房间代码正确且房主已开启房间")
}

func (m *EasyTierManager) runCli(args ...string) (string, error) {
	cmd := exec.Command(m.cliPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("easytier-cli failed", "path", m.cliPath, "args", args, "error", err, "output", string(out))
		return "", err
	}
	return string(out), nil
}
