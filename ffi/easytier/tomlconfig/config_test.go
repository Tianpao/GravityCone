package tomlconfig

import (
	"strings"
	"testing"
)

// --- BuildTOMLConfig ---

func TestBuildTOMLConfigScaffoldingHost(t *testing.T) {
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		Hostname:      "scaffolding-mc-server-25565",
		IsHost:        true,
		TCPPort:       25565,
		MCPort:        25565,
	}

	got := BuildTOMLConfig(opts)

	// Check instance name
	if !strings.Contains(got, `instance_name = "scaffolding-mc-server-25565"`) {
		t.Error("missing instance_name with hostname")
	}
	// Check network identity
	if !strings.Contains(got, `network_name = "test-net"`) {
		t.Error("missing network_name")
	}
	if !strings.Contains(got, `network_secret = "test-secret"`) {
		t.Error("missing network_secret")
	}
	// Check Scaffolding-specific flags
	if !strings.Contains(got, "no_tun = true") {
		t.Error("missing no_tun")
	}
	if !strings.Contains(got, "enable_kcp_proxy = true") {
		t.Error("missing enable_kcp_proxy for Scaffolding")
	}
	if !strings.Contains(got, "enable_quic_proxy = true") {
		t.Error("missing enable_quic_proxy for Scaffolding")
	}
	if !strings.Contains(got, "encryption_algorithm = \"aes-gcm\"") {
		t.Error("missing encryption_algorithm for Scaffolding")
	}
	if !strings.Contains(got, "data_compress_algo = \"zstd\"") {
		t.Error("missing data_compress_algo for Scaffolding")
	}
	if !strings.Contains(got, "default_protocol = \"tcp\"") {
		t.Error("missing default_protocol for Scaffolding")
	}
	if !strings.Contains(got, "private_mode = true") {
		t.Error("missing private_mode for Scaffolding")
	}
	if !strings.Contains(got, "disable_p2p = false") {
		t.Error("missing disable_p2p = false for Scaffolding")
	}
	// Check host-specific
	if !strings.Contains(got, `ipv4 = "10.144.144.1"`) {
		t.Error("missing host virtual IP")
	}
	if !strings.Contains(got, `hostname = "scaffolding-mc-server-25565"`) {
		t.Error("missing hostname")
	}
	// Check whitelist
	if !strings.Contains(got, "tcp_whitelist") {
		t.Error("missing tcp_whitelist for Scaffolding host")
	}
	if !strings.Contains(got, "udp_whitelist") {
		t.Error("missing udp_whitelist for Scaffolding host")
	}
	// Check listeners
	if !strings.Contains(got, `listeners = ["tcp://0.0.0.0:0", "udp://0.0.0.0:0"]`) {
		t.Error("missing listeners")
	}
	// Should NOT have p2p_only (replaced by disable_p2p)
	if strings.Contains(got, "p2p_only") {
		t.Error("should not have p2p_only for Scaffolding")
	}
	// Should NOT have dhcp
	if strings.Contains(got, "dhcp") {
		t.Error("should not have dhcp for host")
	}
}

func TestBuildTOMLConfigScaffoldingGuest(t *testing.T) {
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        false,
	}

	got := BuildTOMLConfig(opts)

	// Guest should have dhcp
	if !strings.Contains(got, "dhcp = true") {
		t.Error("missing dhcp for guest")
	}
	// Guest should have whitelist "0"
	if !strings.Contains(got, `tcp_whitelist = ["0"]`) {
		t.Error("missing tcp_whitelist for Scaffolding guest")
	}
	if !strings.Contains(got, `udp_whitelist = ["0"]`) {
		t.Error("missing udp_whitelist for Scaffolding guest")
	}
	// Guest should NOT have ipv4
	if strings.Contains(got, "ipv4") {
		t.Error("guest should not have ipv4")
	}
	// Instance name should fall back to gravitycone-{network}
	if !strings.Contains(got, `instance_name = "gravitycone-test-net"`) {
		t.Error("missing fallback instance_name for guest")
	}
}

func TestBuildTOMLConfigPaperConnectHost(t *testing.T) {
	opts := StartOptions{
		NetworkName:        "pc-net",
		NetworkSecret:      "pc-secret",
		Hostname:           "pcs-45678--g--19132",
		IsHost:             true,
		UpstreamCompatible: true,
		TCPPort:            45678,
	}

	got := BuildTOMLConfig(opts)

	// PaperConnect flags
	if !strings.Contains(got, "no_tun = true") {
		t.Error("missing no_tun for PaperConnect")
	}
	if !strings.Contains(got, "disable_p2p = false") {
		t.Error("missing disable_p2p = false for PaperConnect")
	}
	// Should NOT have Scaffolding-specific flags
	if strings.Contains(got, "enable_kcp_proxy") {
		t.Error("PaperConnect should not have enable_kcp_proxy")
	}
	if strings.Contains(got, "private_mode") {
		t.Error("PaperConnect should not have private_mode")
	}
	if strings.Contains(got, "encryption_algorithm") {
		t.Error("PaperConnect should not have encryption_algorithm")
	}
	// Should NOT have whitelist (UpstreamCompatible skips it)
	if strings.Contains(got, "tcp_whitelist") {
		t.Error("PaperConnect host should not have tcp_whitelist")
	}
}

func TestBuildTOMLConfigPaperConnectGuest(t *testing.T) {
	opts := StartOptions{
		NetworkName:        "pc-net",
		NetworkSecret:      "pc-secret",
		IsHost:             false,
		UpstreamCompatible: true,
	}

	got := BuildTOMLConfig(opts)

	// Guest should have dhcp
	if !strings.Contains(got, "dhcp = true") {
		t.Error("missing dhcp for PaperConnect guest")
	}
	// Should NOT have whitelist
	if strings.Contains(got, "tcp_whitelist") {
		t.Error("PaperConnect guest should not have tcp_whitelist")
	}
}

func TestBuildTOMLConfigPortForwards(t *testing.T) {
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        true,
		TCPPort:       25565,
		PortForwards: []string{
			"tcp://0.0.0.0:25565/10.144.144.1:25565",
			"udp://0.0.0.0:25565/10.144.144.1:25565",
		},
	}

	got := BuildTOMLConfig(opts)

	if !strings.Contains(got, "port_forward") {
		t.Error("missing port_forward section")
	}
	if !strings.Contains(got, `proto = "tcp"`) {
		t.Error("missing tcp port forward")
	}
	if !strings.Contains(got, `proto = "udp"`) {
		t.Error("missing udp port forward")
	}
	if !strings.Contains(got, `bind_addr = "0.0.0.0:25565"`) {
		t.Error("missing bind_addr")
	}
	if !strings.Contains(got, `dst_addr = "10.144.144.1:25565"`) {
		t.Error("missing dst_addr")
	}
}

func TestBuildTOMLConfigPaperConnectGuestDualForwards(t *testing.T) {
	opts := StartOptions{
		NetworkName:        "pc-net",
		NetworkSecret:      "pc-secret",
		IsHost:             false,
		UpstreamCompatible: true,
		PortForwards: []string{
			"tcp://127.0.0.1:41001/10.144.144.1:41002",
			"udp://127.0.0.1:41003/10.144.144.1:41004",
		},
	}

	got := BuildTOMLConfig(opts)
	for _, want := range []string{
		`proto = "tcp", bind_addr = "127.0.0.1:41001", dst_addr = "10.144.144.1:41002"`,
		`proto = "udp", bind_addr = "127.0.0.1:41003", dst_addr = "10.144.144.1:41004"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing port forward %q in:\n%s", want, got)
		}
	}
}

func TestBuildTOMLConfigNoPortForwards(t *testing.T) {
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        true,
		TCPPort:       25565,
	}

	got := BuildTOMLConfig(opts)

	if strings.Contains(got, "port_forward") {
		t.Error("should not have port_forward when empty")
	}
}

func TestBuildTOMLConfigPeers(t *testing.T) {
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        true,
		TCPPort:       25565,
		Peers:         []string{"tcp://1.2.3.4:11010", "udp://5.6.7.8:11010"},
	}

	got := BuildTOMLConfig(opts)

	if !strings.Contains(got, "[[peer]]") {
		t.Error("missing [[peer]] section")
	}
	if !strings.Contains(got, `uri = "tcp://1.2.3.4:11010"`) {
		t.Error("missing first peer")
	}
	if !strings.Contains(got, `uri = "udp://5.6.7.8:11010"`) {
		t.Error("missing second peer")
	}
}

func TestBuildTOMLConfigNoPeers(t *testing.T) {
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        true,
		TCPPort:       25565,
	}

	got := BuildTOMLConfig(opts)

	if strings.Contains(got, "[[peer]]") {
		t.Error("should not have [[peer]] when empty")
	}
}

func TestBuildTOMLConfigMachineID(t *testing.T) {
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        true,
		TCPPort:       25565,
		MachineID:     "my-machine-123",
	}

	got := BuildTOMLConfig(opts)

	if !strings.Contains(got, `machine_id = "my-machine-123"`) {
		t.Error("missing machine_id")
	}
}

func TestBuildTOMLConfigNoMachineID(t *testing.T) {
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        true,
		TCPPort:       25565,
	}

	got := BuildTOMLConfig(opts)

	if strings.Contains(got, "machine_id") {
		t.Error("should not have machine_id when empty")
	}
}

func TestBuildTOMLConfigInstanceNameFallback(t *testing.T) {
	// When Hostname is empty, instance_name should be "gravitycone-{NetworkName}"
	opts := StartOptions{
		NetworkName:   "my-network",
		NetworkSecret: "secret",
		IsHost:        false,
	}

	got := BuildTOMLConfig(opts)

	if !strings.Contains(got, `instance_name = "gravitycone-my-network"`) {
		t.Error("instance_name should fall back to gravitycone-{NetworkName}")
	}
}

func TestBuildTOMLConfigWhitelistWithDifferentPorts(t *testing.T) {
	// When TCPPort != MCPort, both should appear in whitelist
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        true,
		TCPPort:       12345,
		MCPort:        25565,
	}

	got := BuildTOMLConfig(opts)

	if !strings.Contains(got, `"12345", "25565"`) {
		t.Error("whitelist should contain both TCPPort and MCPort when different")
	}
}

func TestBuildTOMLConfigWhitelistWithSamePorts(t *testing.T) {
	// When TCPPort == MCPort, only one should appear
	opts := StartOptions{
		NetworkName:   "test-net",
		NetworkSecret: "test-secret",
		IsHost:        true,
		TCPPort:       25565,
		MCPort:        25565,
	}

	got := BuildTOMLConfig(opts)

	// Should have the port but not duplicated
	if strings.Count(got, `"25565"`) < 2 { // at least tcp + udp whitelist
		t.Error("whitelist should contain port 25565")
	}
	// Should not have "25565, 25565" (duplicate in same list)
	if strings.Contains(got, `"25565", "25565"`) {
		t.Error("whitelist should not duplicate port when TCPPort == MCPort")
	}
}

// --- ParsePortForward ---

func TestParsePortForward(t *testing.T) {
	tests := []struct {
		input      string
		wantProto  string
		wantLocal  string
		wantRemote string
	}{
		{
			input:      "tcp://0.0.0.0:12345/10.144.144.1:12345",
			wantProto:  "tcp",
			wantLocal:  "0.0.0.0:12345",
			wantRemote: "10.144.144.1:12345",
		},
		{
			input:      "udp://0.0.0.0:25565/10.144.144.1:25565",
			wantProto:  "udp",
			wantLocal:  "0.0.0.0:25565",
			wantRemote: "10.144.144.1:25565",
		},
		{
			input:      "0.0.0.0:12345/10.144.144.1:12345", // no proto
			wantProto:  "tcp",                              // defaults to tcp
			wantLocal:  "0.0.0.0:12345/10.144.144.1:12345",
			wantRemote: "",
		},
		{
			input:      "tcp://0.0.0.0:12345", // no remote
			wantProto:  "tcp",
			wantLocal:  "0.0.0.0:12345",
			wantRemote: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			proto, local, remote := ParsePortForward(tt.input)
			if proto != tt.wantProto {
				t.Errorf("proto = %q, want %q", proto, tt.wantProto)
			}
			if local != tt.wantLocal {
				t.Errorf("local = %q, want %q", local, tt.wantLocal)
			}
			if remote != tt.wantRemote {
				t.Errorf("remote = %q, want %q", remote, tt.wantRemote)
			}
		})
	}
}

// --- CleanAddr ---

func TestCleanAddr(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"[::1]", "::1"},
		{"[fe80::1]", "fe80::1"},
		{"10.144.144.1", "10.144.144.1"},
		{"0.0.0.0:12345", "0.0.0.0:12345"},
		{"", ""},
	}

	for _, tt := range tests {
		got := CleanAddr(tt.input)
		if got != tt.want {
			t.Errorf("CleanAddr(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- StripCIDR ---

func TestStripCIDR(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"10.144.0.1/24", "10.144.0.1"},
		{"10.144.0.1/32", "10.144.0.1"},
		{"10.144.0.1", "10.144.0.1"},
		{"", ""},
	}

	for _, tt := range tests {
		got := StripCIDR(tt.input)
		if got != tt.want {
			t.Errorf("StripCIDR(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// --- ProtoToInt ---

func TestProtoToInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"tcp", 0},
		{"udp", 1},
		{"TCP", 0},
		{"UDP", 1},
		{"", 0},
		{"Tcp", 0},
		{"Udp", 1},
	}

	for _, tt := range tests {
		got := ProtoToInt(tt.input)
		if got != tt.want {
			t.Errorf("ProtoToInt(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
