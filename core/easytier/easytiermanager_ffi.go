//go:build et_ffi

// FFI-based EasyTierManager for Android.
// Replaces the subprocess-based implementation (easytiermanager.go) when
// building with -tags et_ffi (GOOS=android, with libeasytier_ffi linked).
//
// Build: CGO_ENABLED=1 GOOS=android go build -tags et_ffi ...
package easytier

import (
	"sync"

	ffi_et "gravitycone/ffi/easytier"
)

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
		DisableP2P:         opts.DisableP2P,
		MachineID:          opts.MachineID,
	}
	// Pass through the TUN fd provider if set (for Android VpnService).
	// When nil, FFIManager.Start() falls back to DefaultTunFdProvider
	// (set by vpn_init.go via the et_ffi build tag).
	if opts.TunFdProvider != nil {
		m.ffi.TunFdProvider = opts.TunFdProvider
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

// HasTUN reports whether an Android VpnService TUN is available. It is used by
// Scaffolding; PaperConnect selects its own transport through DialMode.
func (m *EasyTierManager) HasTUN() bool { return true }

// DialMode reports the transport used by PaperConnect. Android's VpnService
// exposes the EasyTier virtual network directly, so PaperConnect dials the
// host virtual IP for both the control and game channels. Desktop-only static
// port forwarding remains selected by the non-FFI manager.
func (m *EasyTierManager) DialMode() DialMode { return DialModeDirect }

func (m *EasyTierManager) IsRunning() bool            { return m.ffi.IsRunning() }
func (m *EasyTierManager) SelfVirtualIP() string      { return m.virtualIP }
func (m *EasyTierManager) RPCPortal() string          { return "" }
func (m *EasyTierManager) GetPeerID() (string, error) { return m.ffi.GetPeerID() }
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

// --- StunService (Android — reads NAT type from the running instance) ---
//
// easytier-ffi has no stun export and Android has no easytier-cli binary.
// Instead, the running EasyTier instance collects STUN info internally
// (stun.rs's StunInfoCollector) and publishes it via collect_network_infos
// as my_node_info.stun_info. Values are proto NatType integers — the same
// numbering easytier-cli stun prints on desktop.

type StunService struct{}

func (s *StunService) TestStun() (*StunResult, error) {
	info, err := ffi_et.GetStunInfo()
	if err != nil {
		return nil, err
	}
	return &StunResult{
		UdpNatType:     info.UdpNatType,
		TcpNatType:     info.TcpNatType,
		LastUpdateTime: info.LastUpdateTime,
		PublicIP:       info.PublicIP,
		MinPort:        info.MinPort,
		MaxPort:        info.MaxPort,
	}, nil
}
