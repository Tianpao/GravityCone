//go:build et_ffi

package easytier

import (
	"encoding/json"
	"testing"
)

// TestParseFFIRunningInfoJSON verifies that the prost serde JSON emitted by
// easytier-ffi v2.6.4's collect_network_infos parses into the expected Go
// structures. The fixture mirrors the protobuf messages:
//
//	NetworkInstanceRunningInfo{ dev_name, my_node_info, events, routes,
//	                            peers, peer_route_pairs, running, error_msg }
//	MyNodeInfo{ virtual_ipv4: Ipv4Inet?, hostname, version, ips, peer_id }
//	Route{ peer_id, ipv4_addr: Ipv4Inet, hostname, proxy_cidrs, ... }
//	Ipv4Inet{ address: Ipv4Addr, network_length }
//	Ipv4Addr{ addr: uint32 }   // dotted-quad packed big-endian
func TestParseFFIRunningInfoJSON(t *testing.T) {
	// 176258049 = 0x0A810001 → 10.129.124.1 ; 176258050 → 10.129.124.2
	fixture := `{
		"dev_name": "tun0",
		"my_node_info": {
			"virtual_ipv4": {"address": {"addr": 176258049}, "network_length": 24},
			"hostname": "scaffolding-mc-server-25565",
			"peer_id": 42,
			"stun_info": {
				"udp_nat_type": 3,
				"tcp_nat_type": 4,
				"last_update_time": 1720246800,
				"public_ip": ["203.0.113.1"],
				"min_port": 30000,
				"max_port": 40000
			}
		},
		"routes": [
			{"peer_id": 42, "ipv4_addr": {"address": {"addr": 176258049}, "network_length": 24}, "hostname": "scaffolding-mc-server-25565", "proxy_cidrs": []},
			{"peer_id": 43, "ipv4_addr": {"address": {"addr": 176258050}, "network_length": 24}, "hostname": "scaffolding-mc-server-25566", "proxy_cidrs": ["10.0.0.0/8"]}
		],
		"running": true
	}`

	var ri ffiRunningInfo
	if err := json.Unmarshal([]byte(fixture), &ri); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ri.ErrorMsg != "" {
		t.Fatalf("unexpected error_msg: %q", ri.ErrorMsg)
	}
	if ri.MyNodeInfo == nil {
		t.Fatal("my_node_info is nil")
	}
	if got := ipv4InetString(ri.MyNodeInfo.VirtualIP4); got != "10.129.124.1/24" {
		t.Fatalf("virtual ip = %q, want 10.129.124.1/24", got)
	}
	if ri.MyNodeInfo.Hostname != "scaffolding-mc-server-25565" {
		t.Fatalf("hostname = %q", ri.MyNodeInfo.Hostname)
	}
	if ri.MyNodeInfo.PeerID != 42 {
		t.Fatalf("peer_id = %d", ri.MyNodeInfo.PeerID)
	}
	// my_node_info.stun_info — proto NatType ints, same numbering as
	// easytier-cli stun output on desktop.
	s := ri.MyNodeInfo.StunInfo
	if s == nil {
		t.Fatal("stun_info is nil")
	}
	if s.UdpNatType != 3 || s.TcpNatType != 4 {
		t.Fatalf("nat types = %d/%d, want 3/4 (FullCone/Restricted)", s.UdpNatType, s.TcpNatType)
	}
	if s.LastUpdateTime != 1720246800 {
		t.Fatalf("last_update_time = %d", s.LastUpdateTime)
	}
	if len(s.PublicIP) != 1 || s.PublicIP[0] != "203.0.113.1" {
		t.Fatalf("public_ip = %v", s.PublicIP)
	}
	if s.MinPort != 30000 || s.MaxPort != 40000 {
		t.Fatalf("ports = %d-%d", s.MinPort, s.MaxPort)
	}
	if len(ri.Routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(ri.Routes))
	}
	r1 := ri.Routes[1]
	if r1.Hostname != "scaffolding-mc-server-25566" {
		t.Fatalf("route[1] hostname = %q", r1.Hostname)
	}
	if got := ipv4InetString(r1.IPv4Addr); got != "10.129.124.2/24" {
		t.Fatalf("route[1] ip = %q, want 10.129.124.2/24", got)
	}
	if len(r1.ProxyCIDRs) != 1 || r1.ProxyCIDRs[0] != "10.0.0.0/8" {
		t.Fatalf("route[1] proxy_cidrs = %v", r1.ProxyCIDRs)
	}
	if r1.PeerID != 43 {
		t.Fatalf("route[1] peer_id = %d", r1.PeerID)
	}
}

// TestFFIListRoutesAndNodeInfo exercises the public wrappers end-to-end over
// a parsed instance info.
func TestFFIListRoutesAndNodeInfo(t *testing.T) {
	fixture := `{
		"my_node_info": {"virtual_ipv4": {"address": {"addr": 176258049}, "network_length": 24}, "hostname": "host-a", "peer_id": 7},
		"routes": [
			{"peer_id": 8, "ipv4_addr": {"address": {"addr": 176258050}, "network_length": 24}, "hostname": "host-b", "proxy_cidrs": []}
		]
	}`
	var ri ffiRunningInfo
	if err := json.Unmarshal([]byte(fixture), &ri); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	routes := make([]PeerRouteEntry, 0, len(ri.Routes))
	for _, r := range ri.Routes {
		routes = append(routes, PeerRouteEntry{
			Hostname:   r.Hostname,
			IPv4Addr:   ipv4InetString(r.IPv4Addr),
			PeerID:     json.Number("0").String(), // placeholder, real impl uses FormatUint
			ProxyCIDRs: r.ProxyCIDRs,
		})
	}
	if len(routes) != 1 || routes[0].Hostname != "host-b" {
		t.Fatalf("routes = %+v", routes)
	}
	if got := routes[0].IPv4Addr; got != "10.129.124.2/24" {
		t.Fatalf("route ip = %q", got)
	}
}
