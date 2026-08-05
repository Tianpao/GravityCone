package paperconnect

import (
	"slices"
	"testing"
)

// 房主不携带外部中继（CLI/FFI 未传 relay_node_id/relay_url）时：
// 房间仍可创建（peers 仅内置节点），nodeID 编码保留 ID 805（不使用公共节点），
// 且不触发 uptime 拉取。
func TestHostPeersAndNodeIDWithoutExternalRelay(t *testing.T) {
	svc := NewPaperConnectService(nil)

	peers, nodeID := svc.hostPeersAndNodeID()

	if nodeID != NodeIDReservedNoPublic {
		t.Fatalf("nodeID = %d, want %d（不使用公共节点）", nodeID, NodeIDReservedNoPublic)
	}
	if len(peers) != len(paperConnectBuiltinPeers) {
		t.Fatalf("peers = %v, want 仅内置节点 %v", peers, paperConnectBuiltinPeers)
	}
}

// 房客不携带外部中继时：仅使用内置节点，不按房间码 nodeID 定向获取。
func TestGuestPeersWithoutExternalRelay(t *testing.T) {
	svc := NewPaperConnectService(nil)

	peers := svc.guestPeers(NodeIDReservedNoPublic)

	if len(peers) != len(paperConnectBuiltinPeers) {
		t.Fatalf("peers = %v, want 仅内置节点 %v", peers, paperConnectBuiltinPeers)
	}
}

// 房主携带外部中继时：nodeID 与地址直接生效，不依赖 uptime。
func TestHostPeersAndNodeIDWithExternalRelay(t *testing.T) {
	svc := NewPaperConnectService(nil)
	ConfigureExternalRelay(svc, 123, "tcp://1.2.3.4:5678")

	peers, nodeID := svc.hostPeersAndNodeID()

	if nodeID != 123 {
		t.Fatalf("nodeID = %d, want 123", nodeID)
	}
	if !slices.Contains(peers, "tcp://1.2.3.4:5678") {
		t.Fatalf("peers = %v, want 包含外部中继地址", peers)
	}
}

// 外部中继清除后恢复纯 P2P 行为。
func TestExternalRelayClear(t *testing.T) {
	svc := NewPaperConnectService(nil)
	ConfigureExternalRelay(svc, 123, "tcp://1.2.3.4:5678")
	ConfigureExternalRelay(svc, 123, "") // url 为空即清除

	peers, nodeID := svc.hostPeersAndNodeID()

	if nodeID != NodeIDReservedNoPublic {
		t.Fatalf("nodeID = %d, want %d（清除后不使用公共节点）", nodeID, NodeIDReservedNoPublic)
	}
	if slices.Contains(peers, "tcp://1.2.3.4:5678") {
		t.Fatalf("peers = %v, want 不含已清除的中继地址", peers)
	}
}
