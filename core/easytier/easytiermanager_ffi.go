//go:build et_ffi

// FFI-based EasyTierManager for Android.
// Replaces the subprocess-based implementation (easytiermanager.go) when
// building with -tags et_ffi (GOOS=android, with libeasytier_ffi linked).
//
// Build: CGO_ENABLED=1 GOOS=android go build -tags et_ffi ...
package easytier

import (
	"fmt"
	"sync"

	ffi_et "gravitycone/ffi/easytier"
)

// EasyTierVersion mirrors the constant from easytierdownload.go
// (which is excluded when et_ffi is set).
const EasyTierVersion = "v2.6.4"

// No-op stubs for desktop-only configuration functions.
func SetCustomEasyTierDir(dir string)  {}
func SetSkipEasyTierDownload(skip bool) {}
func SetEasyTierLogOutput(path string)  {}

// EnsureEasyTier is a no-op on Android — EasyTier is statically linked.
func EnsureEasyTier() error { return nil }

// resolveEasyTierBinary is unused on Android.
func resolveEasyTierBinary(name string) (string, error) {
	return name, nil
}

// allocateRPCPort is unused on Android — FFI has no RPC portal socket.
func allocateRPCPort() (string, error) {
	return "", fmt.Errorf("not available in FFI mode")
}

// --- StartOptions (mirrors desktop) ---

type StartOptions struct {
	NetworkName        string
	NetworkSecret      string
	Hostname           string
	IsHost             bool
	TCPPort            uint16
	MCPort             uint16
	ConfigPath         string
	PortForwards       []string
	Peers              []string
	UpstreamCompatible bool
}

// --- EasyTierManager (wraps ffi/easytier.FFIManager) ---

type EasyTierManager struct {
	ffi       *ffi_et.FFIManager
	virtualIP string
	mu        sync.Mutex
}

func NewEasyTierManager() (*EasyTierManager, error) {
	return &EasyTierManager{ffi: ffi_et.NewFFIManager()}, nil
}

func (m *EasyTierManager) Start(opts StartOptions) (string, error) {
	ffiOpts := ffi_et.StartOptions{
		NetworkName:        opts.NetworkName,
		NetworkSecret:      opts.NetworkSecret,
		Hostname:           opts.Hostname,
		IsHost:             opts.IsHost,
		TCPPort:            opts.TCPPort,
		MCPort:             opts.MCPort,
		PortForwards:       opts.PortForwards,
		Peers:              opts.Peers,
		UpstreamCompatible: opts.UpstreamCompatible,
	}
	virtualIP, err := m.ffi.Start(ffiOpts)
	if err != nil {
		return "", err
	}
	m.virtualIP = virtualIP
	return virtualIP, nil
}

func (m *EasyTierManager) Stop() error {
	m.virtualIP = ""
	return m.ffi.Stop()
}

func (m *EasyTierManager) IsRunning() bool                { return m.ffi.IsRunning() }
func (m *EasyTierManager) SelfVirtualIP() string           { return m.virtualIP }
func (m *EasyTierManager) RPCPortal() string               { return "" }
func (m *EasyTierManager) GetPeerID() (string, error)      { return m.ffi.GetPeerID() }
func (m *EasyTierManager) DiscoverPeer(h string) (string, error) {
	return m.ffi.DiscoverPeer(h)
}
func (m *EasyTierManager) DiscoverPeerByPrefix(p string) (string, string, error) {
	return m.ffi.DiscoverPeerByPrefix(p)
}
func (m *EasyTierManager) FindPeerByHostnamePrefix(p string) (string, uint16, error) {
	return m.ffi.FindPeerByHostnamePrefix(p)
}
func (m *EasyTierManager) AddPortForward(proto, local, remote string) error {
	return m.ffi.AddPortForward(proto, local, remote)
}
func (m *EasyTierManager) RemovePortForward(proto, local, remote string) error {
	return m.ffi.RemovePortForward(proto, local, remote)
}

// --- StunService (Android stub — calls RPC, not easytier-cli) ---

type StunResult struct {
	UdpNatType     int      `json:"udp_nat_type"`
	TcpNatType     int      `json:"tcp_nat_type"`
	LastUpdateTime int64    `json:"last_update_time"`
	PublicIP       []string `json:"public_ip"`
	MinPort        int      `json:"min_port"`
	MaxPort        int      `json:"max_port"`
}

type StunService struct{}

func (s *StunService) TestStun() (*StunResult, error) {
	// STUN is handled internally by EasyTier FFI.
	// The caller should query show_node_info or collect_network_infos instead.
	return &StunResult{}, fmt.Errorf("STUN probe not available in FFI mode: use show_node_info RPC")
}
