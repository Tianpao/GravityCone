package easytier

import (
	"slices"
	"testing"
)

const (
	testBasePeer  = "wss://center.node.1tmc.top"
	testRelayPeer = "tcp://1.2.3.4:5678"
)

func basePeers() []string { return []string{testBasePeer} }

// 房主不携带外部中继且未启用 uptime（CLI/FFI）时：仅 base 节点，
// nodeID 编码保留 ID PP（不使用公共节点），且不触发 uptime 拉取。
func TestHostPeersWithoutExternalRelay(t *testing.T) {
	r := NewRelayManager()

	peers, nodeID := r.HostPeersAndNodeID(basePeers())

	if nodeID != NodeIDReservedNoPublic {
		t.Fatalf("nodeID = %d, want %d（不使用公共节点）", nodeID, NodeIDReservedNoPublic)
	}
	if !slices.Equal(peers, basePeers()) {
		t.Fatalf("peers = %v, want 仅内置节点 %v", peers, basePeers())
	}
}

// 房客不携带外部中继时：仅使用内置节点，不按房间码 nodeID 定向获取。
func TestGuestPeersWithoutExternalRelay(t *testing.T) {
	r := NewRelayManager()

	peers := r.GuestPeers(basePeers(), NodeIDReservedNoPublic)

	if !slices.Equal(peers, basePeers()) {
		t.Fatalf("peers = %v, want 仅内置节点 %v", peers, basePeers())
	}
}

// 房主携带外部中继时：nodeID 与地址直接生效，不依赖 uptime。
func TestHostPeersWithExternalRelay(t *testing.T) {
	r := NewRelayManager()
	r.SetExternal(123, testRelayPeer)

	peers, nodeID := r.HostPeersAndNodeID(basePeers())

	if nodeID != 123 {
		t.Fatalf("nodeID = %d, want 123", nodeID)
	}
	if !slices.Contains(peers, testRelayPeer) {
		t.Fatalf("peers = %v, want 包含外部中继地址", peers)
	}
}

// 外部中继清除后恢复纯 P2P 行为。
func TestExternalRelayClear(t *testing.T) {
	r := NewRelayManager()
	r.SetExternal(123, testRelayPeer)
	r.SetExternal(123, "") // url 为空即清除

	peers, nodeID := r.HostPeersAndNodeID(basePeers())

	if nodeID != NodeIDReservedNoPublic {
		t.Fatalf("nodeID = %d, want %d（清除后不使用公共节点）", nodeID, NodeIDReservedNoPublic)
	}
	if slices.Contains(peers, testRelayPeer) {
		t.Fatalf("peers = %v, want 不含已清除的中继地址", peers)
	}
}

// nodeID 编解码往返。
func TestNodeIDRoundTrip(t *testing.T) {
	for id := 0; id <= NodeIDMax; id += 17 {
		lo, hi := NodeIDChars(id)
		if got := NodeIDFromChars(lo, hi); got != id {
			t.Fatalf("NodeID roundtrip: want %d, got %d", id, got)
		}
	}
}
