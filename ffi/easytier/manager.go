//go:build et_ffi

package easytier

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	ffi_toml "gravitycone/ffi/easytier/tomlconfig"
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
	mu            sync.Mutex
	instName      string // instance name in FFI name cache
	virtualIP     string // cached self virtual IP
	isRunning     bool
	startOpts     StartOptions
	TunFdProvider func(instName string, virtualIP string, cidr string) (int, error) // optional TUN fd injection callback
}

// NOTE: Runtime info is parsed from collect_network_infos' prost serde JSON
// (see ffiRunningInfo in bridge.go) — there is no top-level ipv4_addr field
// as in the desktop easytier-cli output.

// NewFFIManager creates a new FFIManager (does not start EasyTier).
func NewFFIManager() *FFIManager {
	return &FFIManager{}
}

// DefaultTunFdProvider is a package-level TUN fd provider.
// On Android, ffi/common/bridge.go sets this to callJavaVpnServiceCallback
// (the reverse-JNI bridge that requests a VpnService TUN fd from Java).
// When FFIManager.TunFdProvider is nil, Start() falls back to this.
//
// Even in no_tun=true mode, EasyTier's mobile build (tun_mobile.rs) still
// expects a TUN fd to be injected via set_tun_fd(), so this must be set
// for Android builds. Desktop builds leave it nil.
var DefaultTunFdProvider func(instName string, virtualIP string, cidr string) (int, error)

// Start builds a TOML config and starts an EasyTier instance in-process.
// Returns the virtual IP once the instance is ready.
func (m *FFIManager) Start(opts ffi_toml.StartOptions) (string, error) {
	m.mu.Lock()
	if m.isRunning {
		m.mu.Unlock()
		return "", fmt.Errorf("EasyTier 已在运行")
	}
	m.mu.Unlock()

	tomlCfg := ffi_toml.BuildTOMLConfig(opts)
	log.Printf("[easytier] Start network=%s host=%s isHost=%v", opts.NetworkName, opts.Hostname, opts.IsHost)

	// Validate config first
	if err := ParseConfig(tomlCfg); err != nil {
		return "", fmt.Errorf("配置验证失败: %w", err)
	}
	log.Printf("[easytier] parse_config 通过")

	// Start the instance
	if err := RunNetworkInstance(tomlCfg); err != nil {
		return "", fmt.Errorf("启动虚拟网络失败: %w", err)
	}
	log.Printf("[easytier] run_network_instance 成功")

	// Extract instance name from config
	instName := opts.Hostname
	if instName == "" {
		instName = fmt.Sprintf("gravitycone-%s", opts.NetworkName)
	}

	// --- TUN fd injection (Android VpnService) ---
	// On Android, EasyTier's mobile build (tun_mobile.rs) expects a TUN fd
	// to be injected via set_tun_fd(), even when no_tun=true is set in config.
	// This triggers the VpnService callback on the Java side, which blocks
	// until the host app establishes the VPN connection.
	provider := m.TunFdProvider
	if provider == nil {
		provider = DefaultTunFdProvider
	}
	var virtualIP string
	if provider != nil {
		log.Printf("[easytier] 请求 TUN fd（VpnService 回调）...")
		vip, err := m.injectTunFd(instName, opts, provider)
		if err != nil {
			// Clean up the instance if TUN fd injection fails.
			DeleteNetworkInstance([]string{instName})
			return "", fmt.Errorf("TUN fd注入失败: %w", err)
		}
		// The Java ParcelFileDescriptor retains ownership of the fd and must
		// close it after the corresponding EasyTier instance has stopped.
		virtualIP = vip
	} else {
		log.Printf("[easytier] 无 TUN fd provider，跳过注入")
	}
	// --- End TUN fd injection ---

	m.mu.Lock()
	m.instName = instName
	m.startOpts = opts
	m.isRunning = true
	m.mu.Unlock()

	// Wait for the virtual IP to become available (poll collect_network_infos).
	// Guests already confirmed the IP while injecting the TUN fd (DHCP assigns
	// it before the VpnService callback); hosts must poll because the fixed
	// IP only appears once the instance is fully up.
	if opts.IsHost || virtualIP == "" {
		vip, err := m.waitForVirtualIP(30 * time.Second)
		if err != nil {
			m.Stop()
			return "", err
		}
		virtualIP = vip
	}
	log.Printf("[easytier] 虚拟IP就绪: %s", virtualIP)

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
//
// NOT SUPPORTED in FFI mode: easytier-ffi v2.6.4 has no RPC export, so port
// forwards cannot be added at runtime. Configure them statically in the TOML
// config (StartOptions.PortForwards) instead. On Android with VpnService the
// TUN device makes forwarding unnecessary anyway (virtual IPs are directly
// reachable), so callers should use the direct virtual-IP path.
func (m *FFIManager) AddPortForward(proto string, localAddr string, remoteAddr string) error {
	return fmt.Errorf("FFI 模式不支持动态端口转发 (%s %s -> %s)；请在 TOML 配置中静态声明", proto, localAddr, remoteAddr)
}

// RemovePortForward removes a port forward.
//
// NOT SUPPORTED in FFI mode — see AddPortForward.
func (m *FFIManager) RemovePortForward(proto string, localAddr string, remoteAddr string) error {
	return fmt.Errorf("FFI 模式不支持动态端口转发移除 (%s %s -> %s)", proto, localAddr, remoteAddr)
}

// --- Internal helpers ---

// injectTunFd determines the virtual IP, calls the provider to get a TUN fd
// from VpnService, and injects it into EasyTier via SetTunFd.
// Returns the virtual IP the TUN is bound to.
//
// For HOST: the virtual IP is known upfront (10.144.144.1 from config).
// For GUEST: the IP is assigned by DHCP; we poll collect_network_infos briefly.
func (m *FFIManager) injectTunFd(instName string, opts StartOptions, provider func(string, string, string) (int, error)) (string, error) {
	var virtualIP string
	var cidr string

	if opts.IsHost {
		// Host: fixed virtual IP, known from config
		virtualIP = hostVirtualIP // "10.144.144.1"
		cidr = "10.144.144.0/24"
	} else {
		// Guest: DHCP assigns the virtual IP before the Android VPN can be
		// established. The VPN address must match that assignment: routing a
		// guessed address into the TUN makes phase-two port forwarding appear
		// healthy while game traffic is black-holed.
		ip, err := m.pollVirtualIP(instName, 30*time.Second)
		if err != nil {
			return "", fmt.Errorf("等待 guest 虚拟IP失败: %w", err)
		}
		virtualIP = ip
		cidr = "10.144.144.0/24"
	}

	// Call the provider (JNI → Java onVpnServiceStateChanged → VpnService).
	// This BLOCKS until the Android app establishes or rejects the VPN.
	fd, err := provider(instName, virtualIP, cidr)
	if err != nil {
		return "", fmt.Errorf("TUN fd provider回调失败: %w", err)
	}

	// Inject the fd into EasyTier.
	if err := SetTunFd(instName, fd); err != nil {
		return "", fmt.Errorf("set_tun_fd失败: %w", err)
	}

	return virtualIP, nil
}

// selfVirtualIP queries collect_network_infos for this instance's virtual IP.
// Returns an error if the instance is gone or not ready yet.
func (m *FFIManager) selfVirtualIP(instName string) (string, error) {
	infos, err := CollectNetworkInfos(32)
	if err != nil {
		return "", err
	}

	for _, info := range infos {
		if info.Name != instName {
			continue
		}
		var ri ffiRunningInfo
		if err := json.Unmarshal([]byte(info.Info), &ri); err != nil {
			continue
		}
		if ri.ErrorMsg != "" {
			return "", fmt.Errorf("实例运行错误: %s", ri.ErrorMsg)
		}
		if ri.MyNodeInfo != nil {
			if ip := ipv4InetString(ri.MyNodeInfo.VirtualIP4); ip != "" {
				return stripCIDR(ip), nil
			}
		}
	}

	return "", fmt.Errorf("实例 %s 尚未就绪", instName)
}

// pollVirtualIP polls collect_network_infos for the virtual IP assigned to this instance.
// Used by GUEST mode (DHCP) to get the IP before calling the VpnService callback.
func (m *FFIManager) pollVirtualIP(instName string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ip, err := m.selfVirtualIP(instName); err == nil && ip != "" {
			return ip, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("轮询虚拟IP超时 (%v)", timeout)
}

func (m *FFIManager) waitForVirtualIP(timeout time.Duration) (string, error) {
	m.mu.Lock()
	instName := m.instName
	m.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ip, err := m.selfVirtualIP(instName); err == nil && ip != "" {
			return ip, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("等待获取虚拟IP超时")
}

// stripCIDR removes CIDR suffix from IP (e.g. "10.144.0.1/24" → "10.144.0.1").
// Delegates to the pure-Go tomlconfig package.
func stripCIDR(ip string) string {
	return ffi_toml.StripCIDR(ip)
}
