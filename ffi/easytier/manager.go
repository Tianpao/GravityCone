//go:build et_ffi

package easytier

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FFIManager is the FFI-based replacement for core/easytier.EasyTierManager.
//
// Instead of launching easytier-core as a subprocess (desktop approach),
// FFIManager calls libeasytier_ffi directly via CGo, running EasyTier
// in-process. This is required on Android where subprocesses are unreliable.
//
// The API surface mirrors core/easytier.EasyTierManager so it can be used
// as a drop-in replacement on Android builds.
type FFIManager struct {
	mu          sync.Mutex
	instName    string // instance name in FFI name cache
	virtualIP   string // cached self virtual IP
	isRunning   bool
	startOpts   StartOptions
	runningInfo *RunningInfo // latest collected info
}

// RunningInfo holds runtime information collected from EasyTier FFI.
type RunningInfo struct {
	VirtualIP  string   `json:"ipv4_addr"`
	PeerID     string   `json:"peer_id"`
	Hostname   string   `json:"hostname"`
	ErrorMsg   string   `json:"error_msg,omitempty"`
}

// NewFFIManager creates a new FFIManager (does not start EasyTier).
func NewFFIManager() *FFIManager {
	return &FFIManager{}
}

// Start builds a TOML config and starts an EasyTier instance in-process.
// Returns the virtual IP once the instance is ready.
func (m *FFIManager) Start(opts StartOptions) (string, error) {
	m.mu.Lock()
	if m.isRunning {
		m.mu.Unlock()
		return "", fmt.Errorf("EasyTier 已在运行")
	}
	m.mu.Unlock()

	tomlCfg := BuildTOMLConfig(opts)

	// Validate config first
	if err := ParseConfig(tomlCfg); err != nil {
		return "", fmt.Errorf("配置验证失败: %w", err)
	}

	// Start the instance
	if err := RunNetworkInstance(tomlCfg); err != nil {
		return "", fmt.Errorf("启动虚拟网络失败: %w", err)
	}

	// Extract instance name from config
	instName := opts.Hostname
	if instName == "" {
		instName = fmt.Sprintf("gravitycone-%s", opts.NetworkName)
	}

	m.mu.Lock()
	m.instName = instName
	m.startOpts = opts
	m.isRunning = true
	m.mu.Unlock()

	// Wait for virtual IP to become available (poll collect_network_infos)
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

// Stop terminates the EasyTier instance.
func (m *FFIManager) Stop() error {
	m.mu.Lock()
	if !m.isRunning {
		m.mu.Unlock()
		return nil
	}
	instName := m.instName
	m.isRunning = false
	m.virtualIP = ""
	m.instName = ""
	m.mu.Unlock()

	// Clean up port forwards if needed (they're auto-cleaned when instance stops)
	return DeleteNetworkInstance([]string{instName})
}

// IsRunning returns true if the instance is active.
func (m *FFIManager) IsRunning() bool {
	m.mu.Lock()
	isRunning := m.isRunning
	instName := m.instName
	m.mu.Unlock()

	if !isRunning {
		return false
	}

	// Double-check: ask FFI if the instance still exists (outside lock to avoid blocking other operations).
	infos, err := ListInstances(32)
	if err != nil {
		return false
	}
	for _, info := range infos {
		if info.Name == instName {
			return true
		}
	}
	return false
}

// SelfVirtualIP returns the cached virtual IP.
func (m *FFIManager) SelfVirtualIP() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.virtualIP
}

// GetPeerID returns this node's peer ID.
func (m *FFIManager) GetPeerID() (string, error) {
	nodeInfo, err := GetNodeInfo()
	if err != nil {
		return "", fmt.Errorf("查询节点信息失败: %w", err)
	}
	if nodeInfo.NodeInfo == nil {
		return "", fmt.Errorf("节点信息为空")
	}
	return nodeInfo.NodeInfo.PeerID, nil
}

// DiscoverPeer finds a peer by exact hostname match.
// Returns the peer's virtual IP.
func (m *FFIManager) DiscoverPeer(hostname string) (string, error) {
	routes, err := ListRoutes()
	if err != nil {
		return "", fmt.Errorf("查询对等节点失败: %w", err)
	}

	for _, r := range routes.Routes {
		if r.Hostname == hostname && r.IPv4Addr != "" {
			return stripCIDR(r.IPv4Addr), nil
		}
	}

	return "", fmt.Errorf("未找到主机 (%s)，请确认房间代码正确", hostname)
}

// DiscoverPeerByPrefix finds a peer whose hostname starts with the given prefix.
// Returns the matching hostname and virtual IP.
func (m *FFIManager) DiscoverPeerByPrefix(hostnamePrefix string) (string, string, error) {
	routes, err := ListRoutes()
	if err != nil {
		return "", "", fmt.Errorf("查询对等节点失败: %w", err)
	}

	for _, r := range routes.Routes {
		if strings.HasPrefix(r.Hostname, hostnamePrefix) && r.IPv4Addr != "" {
			return r.Hostname, stripCIDR(r.IPv4Addr), nil
		}
	}

	return "", "", fmt.Errorf("未找到主机 (前缀 %s)，请确认房间代码正确", hostnamePrefix)
}

// FindPeerByHostnamePrefix scans peers for hostname prefix and extracts port from hostname.
// Used by ScaffoldingMC: hostname = "scaffolding-mc-server-{tcpPort}"
// Returns the peer's virtual IP and the TCP port parsed from the hostname suffix.
func (m *FFIManager) FindPeerByHostnamePrefix(hostnamePrefix string) (string, uint16, error) {
	routes, err := ListRoutes()
	if err != nil {
		return "", 0, fmt.Errorf("查询对等节点失败: %w", err)
	}

	for _, r := range routes.Routes {
		if !strings.HasPrefix(r.Hostname, hostnamePrefix) || r.IPv4Addr == "" {
			continue
		}
		portStr := r.Hostname[len(hostnamePrefix):]
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil || port <= 1024 || port > 65535 {
			continue
		}
		return stripCIDR(r.IPv4Addr), uint16(port), nil
	}

	return "", 0, fmt.Errorf("未找到联机中心，请确认房间代码正确且房主已开启房间")
}

// AddPortForward adds a TCP or UDP port forward.
func (m *FFIManager) AddPortForward(proto string, localAddr string, remoteAddr string) error {
	protoVal := protoToInt(proto)
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		err := AddPortForwardRPC(protoVal, localAddr, remoteAddr)
		if err == nil {
			return nil
		}
		lastErr = err
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	return fmt.Errorf("添加端口转发失败 (%s %s -> %s): %w", proto, localAddr, remoteAddr, lastErr)
}

// RemovePortForward removes a port forward.
func (m *FFIManager) RemovePortForward(proto string, localAddr string, remoteAddr string) error {
	protoVal := protoToInt(proto)
	return RemovePortForwardRPC(protoVal, localAddr, remoteAddr)
}

// --- Internal helpers ---

func (m *FFIManager) waitForVirtualIP(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !m.IsRunning() {
			return "", fmt.Errorf("EasyTier 实例已退出")
		}

		ip, err := m.fetchSelfVirtualIP()
		if err == nil && ip != "" {
			return ip, nil
		}

		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("等待获取虚拟IP超时")
}

// fetchSelfVirtualIP gets the local node's virtual IP from collect_network_infos.
func (m *FFIManager) fetchSelfVirtualIP() (string, error) {
	m.mu.Lock()
	instName := m.instName
	m.mu.Unlock()

	infos, err := CollectNetworkInfos(32)
	if err != nil {
		return "", err
	}

	for _, info := range infos {
		if info.Name != instName {
			continue
		}
		var ri RunningInfo
		if err := json.Unmarshal([]byte(info.Info), &ri); err != nil {
			continue
		}
		if ri.ErrorMsg != "" {
			return "", fmt.Errorf("实例运行错误: %s", ri.ErrorMsg)
		}
		return stripCIDR(ri.VirtualIP), nil
	}

	return "", fmt.Errorf("实例 %s 尚未就绪", instName)
}

// protoToInt converts protocol string to EasyTier proto enum.
// SocketType: 0 = TCP, 1 = UDP.
func protoToInt(proto string) int {
	switch strings.ToLower(proto) {
	case "udp":
		return 1
	default:
		return 0
	}
}

// stripCIDR removes CIDR suffix from IP (e.g. "10.144.0.1/24" → "10.144.0.1").
func stripCIDR(ip string) string {
	if i := strings.IndexByte(ip, '/'); i >= 0 {
		return ip[:i]
	}
	return ip
}
