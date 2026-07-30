// Package tomlconfig provides pure-Go TOML config generation for EasyTier FFI.
//
// This package is a testable subset of ffi/easytier that has no CGo dependency.
// The parent package ffi/easytier wraps these functions for the actual Android build.
package tomlconfig

import (
	"fmt"
	"strings"
)

// StartOptions mirrors easytier.StartOptions from core/easytier.
// Duplicated here to keep this package self-contained (no dependency on core/easytier
// which imports os/exec and is desktop-only).
type StartOptions struct {
	NetworkName        string   // EasyTier network name
	NetworkSecret      string   // EasyTier network secret
	Hostname           string   // HOST only: advertised hostname
	IsHost             bool     // true = host, false = guest (DHCP)
	TCPPort            uint16   // HOST only: scaffolding TCP port for whitelist
	MCPort             uint16   // HOST only: MC server port for whitelist
	PortForwards       []string // "tcp://local_addr/remote_addr"
	Peers              []string // Public peer addresses
	UpstreamCompatible bool     // PaperConnect-style (no-tun, no p2p restrictions)
	MachineID          string   // Optional machine identifier
}

// HostVirtualIP is the fixed virtual IP assigned to the host node.
const HostVirtualIP = "10.144.144.1"

// BuildTOMLConfig generates an EasyTier TOML config from StartOptions.
//
// This mirrors the CLI argument building in core/easytier/easytiermanager.go
// but outputs TOML format (required by libeasytier_ffi's parse_config/run_network_instance).
//
// Example output for Scaffolding host:
//
//	instance_name = "scaffolding-host-12345"
//	[network_identity]
//	network_name = "..."
//	network_secret = "..."
//	[flags]
//	no_tun = true
//	enable_kcp_proxy = true
//	...
//	listeners = ["tcp://0.0.0.0:0", "udp://0.0.0.0:0"]
func BuildTOMLConfig(opts StartOptions) string {
	var b strings.Builder

	// Instance name: unique identifier for this instance in the FFI instance cache.
	// Used by set_tun_fd, delete_network_instance, collect_network_infos, etc.
	instName := opts.Hostname
	if instName == "" {
		instName = fmt.Sprintf("gravitycone-%s", opts.NetworkName)
	}
	b.WriteString(fmt.Sprintf("instance_name = \"%s\"\n", instName))

	// Network identity
	b.WriteString("\n[network_identity]\n")
	b.WriteString(fmt.Sprintf("network_name = \"%s\"\n", opts.NetworkName))
	b.WriteString(fmt.Sprintf("network_secret = \"%s\"\n", opts.NetworkSecret))

	// Flags
	b.WriteString("\n[flags]\n")
	if opts.UpstreamCompatible {
		// PaperConnect mode: no TUN, peer-to-peer allowed
		b.WriteString("no_tun = true\n")
		b.WriteString(fmt.Sprintf("disable_p2p = false\n"))
	} else {
		// ScaffoldingMC mode: private P2P network
		b.WriteString("no_tun = true\n")
		b.WriteString("enable_kcp_proxy = true\n")
		b.WriteString("enable_quic_proxy = true\n")
		b.WriteString("latency_first = true\n")
		b.WriteString("encryption_algorithm = \"aes-gcm\"\n")
		b.WriteString("data_compress_algo = \"zstd\"\n")
		b.WriteString("default_protocol = \"tcp\"\n")
		b.WriteString("private_mode = true\n")
		b.WriteString("p2p_only = true\n")
	}
	b.WriteString("multi_thread = true\n")

	// Host-specific
	if opts.IsHost {
		b.WriteString(fmt.Sprintf("\nipv4 = \"%s\"\n", HostVirtualIP))
		if opts.Hostname != "" {
			b.WriteString(fmt.Sprintf("hostname = \"%s\"\n", opts.Hostname))
		}
		if !opts.UpstreamCompatible {
			// Whitelist only the ports we use
			b.WriteString(fmt.Sprintf("tcp_whitelist = [\"%d\"", opts.TCPPort))
			if opts.MCPort != 0 && opts.MCPort != opts.TCPPort {
				b.WriteString(fmt.Sprintf(", \"%d\"", opts.MCPort))
			}
			b.WriteString("]\n")
			b.WriteString(fmt.Sprintf("udp_whitelist = [\"%d\"", opts.TCPPort))
			if opts.MCPort != 0 && opts.MCPort != opts.TCPPort {
				b.WriteString(fmt.Sprintf(", \"%d\"", opts.MCPort))
			}
			b.WriteString("]\n")
		}
	} else {
		// Guest: DHCP for IP assignment
		b.WriteString("\ndhcp = true\n")
		if !opts.UpstreamCompatible {
			b.WriteString("tcp_whitelist = [\"0\"]\n")
			b.WriteString("udp_whitelist = [\"0\"]\n")
		}
	}

	// Listeners
	b.WriteString("\nlisteners = [\"tcp://0.0.0.0:0\", \"udp://0.0.0.0:0\"]\n")

	// Port forwards
	if len(opts.PortForwards) > 0 {
		b.WriteString("\nport_forward = [\n")
		for i, pf := range opts.PortForwards {
			proto, local, remote := ParsePortForward(pf)
			if i > 0 {
				b.WriteString(",\n")
			}
			b.WriteString(fmt.Sprintf("  { proto = \"%s\", bind_addr = \"%s\", dst_addr = \"%s\" }",
				proto, local, remote))
		}
		b.WriteString("\n]\n")
	}

	// Peers
	if len(opts.Peers) > 0 {
		b.WriteString("\n[[peer]]\n")
		for _, p := range opts.Peers {
			b.WriteString(fmt.Sprintf("uri = \"%s\"\n", p))
		}
	}

	// Machine ID
	if opts.MachineID != "" {
		b.WriteString(fmt.Sprintf("\nmachine_id = \"%s\"\n", opts.MachineID))
	}

	return b.String()
}

// ParsePortForward parses a port forward string like "tcp://0.0.0.0:12345/10.144.144.1:12345"
// into proto, localAddr, remoteAddr.
func ParsePortForward(pf string) (proto, local, remote string) {
	// Format: proto://local/remote
	protoEnd := strings.Index(pf, "://")
	if protoEnd < 0 {
		return "tcp", pf, ""
	}
	proto = pf[:protoEnd]
	rest := pf[protoEnd+3:]

	slash := strings.Index(rest, "/")
	if slash < 0 {
		return proto, rest, ""
	}
	local = rest[:slash]
	remote = rest[slash+1:]

	// Strip brackets from IPv6 addresses in local/remote for TOML compatibility
	return proto, CleanAddr(local), CleanAddr(remote)
}

// CleanAddr strips square brackets from IPv6 addresses for TOML compatibility.
func CleanAddr(addr string) string {
	return strings.Trim(addr, "[]")
}

// StripCIDR removes CIDR suffix from IP (e.g. "10.144.0.1/24" → "10.144.0.1").
func StripCIDR(ip string) string {
	if i := strings.IndexByte(ip, '/'); i >= 0 {
		return ip[:i]
	}
	return ip
}

// ProtoToInt converts protocol string to EasyTier proto enum.
// SocketType: 0 = TCP, 1 = UDP.
func ProtoToInt(proto string) int {
	switch strings.ToLower(proto) {
	case "udp":
		return 1
	default:
		return 0
	}
}
