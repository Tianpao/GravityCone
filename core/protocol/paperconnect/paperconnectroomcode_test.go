package paperconnect

import (
	"testing"

	"gravitycone/core/utils"
)

// TestNodeIDRoundTrip 验证 nodeID 编码/解码往返与校验。
func TestNodeIDRoundTrip(t *testing.T) {
	ids := []int{1, 2, 3, 100, 1000, NodeIDReservedSelfRelay, NodeIDReservedNoPublic, NodeIDMax}
	for _, id := range ids {
		rc, err := GeneratePaperConnectRoomCodeWithNodeID(id)
		if err != nil {
			t.Fatalf("GeneratePaperConnectRoomCodeWithNodeID(%d) failed: %v", id, err)
		}

		parsed, err := ParsePaperConnectRoomCode(rc.Format())
		if err != nil {
			t.Fatalf("Parse(%d) failed: %v", id, err)
		}
		if got := parsed.NodeID(); got != id {
			t.Fatalf("NodeID roundtrip: want %d, got %d (code %s)", id, got, rc.Format())
		}

		// S 部分校验必须仍满足（旧解析逻辑兼容）
		if !pcIsDivisibleBySeven([]byte(parsed.SecretPart)) {
			t.Fatalf("checksum failed for nodeID %d: %s", id, rc.Format())
		}
	}
}

// TestNodeIDSpecialEncodings 验证特殊 ID 的字符组合。
func TestNodeIDSpecialEncodings(t *testing.T) {
	rc, err := GeneratePaperConnectRoomCodeWithNodeID(NodeIDReservedSelfRelay)
	if err != nil {
		t.Fatal(err)
	}
	if rc.NetworkPart[7] != '0' || rc.SecretPart[7] != '0' {
		t.Fatalf("self relay: want '0'+'0', got %c+%c", rc.NetworkPart[7], rc.SecretPart[7])
	}

	rc, err = GeneratePaperConnectRoomCodeWithNodeID(NodeIDReservedNoPublic)
	if err != nil {
		t.Fatal(err)
	}
	if rc.NetworkPart[7] != 'P' || rc.SecretPart[7] != 'P' {
		t.Fatalf("no public: want 'P'+'P', got %c+%c", rc.NetworkPart[7], rc.SecretPart[7])
	}
}

// TestNodeIDRange 验证越界拒绝。
func TestNodeIDRange(t *testing.T) {
	if _, err := GeneratePaperConnectRoomCodeWithNodeID(NodeIDMax + 1); err == nil {
		t.Fatal("expected error for nodeID > NodeIDMax")
	}
	if _, err := GeneratePaperConnectRoomCodeWithNodeID(-1); err == nil {
		t.Fatal("expected error for negative nodeID")
	}
}

// TestLegacyRoomCodeCompatibility 验证旧格式房间码（未内嵌 nodeID）仍可被
// 新解析器解析：校验通过、NodeID() 读出随机值。
func TestLegacyRoomCodeCompatibility(t *testing.T) {
	rc, err := GeneratePaperConnectRoomCode()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePaperConnectRoomCode(rc.Format())
	if err != nil {
		t.Fatalf("legacy code parse failed: %v", err)
	}
	id := parsed.NodeID()
	if id < 0 || id > NodeIDMax {
		t.Fatalf("legacy code NodeID() out of range: %d", id)
	}
	if !pcIsDivisibleBySeven([]byte(parsed.SecretPart)) {
		t.Fatal("legacy code checksum broken")
	}
}

// TestNodeIDCharset 验证编码字符均在 34 字符集内。
func TestNodeIDCharset(t *testing.T) {
	for id := 0; id <= NodeIDMax; id++ {
		if _, ok := utils.Value(utils.Charset[id%34]); !ok {
			t.Fatalf("low char not in charset for id %d", id)
		}
		if _, ok := utils.Value(utils.Charset[(id/34)%34]); !ok {
			t.Fatalf("high char not in charset for id %d", id)
		}
	}
}
