package scaffolding

import (
	"testing"

	"gravitycone/core/easytier"
	"gravitycone/core/protocol/common"
)

// TestNodeIDRoundTrip 验证 nodeID 编码/解码往返（解析通过即校验满足，无需再单独断言）。
func TestNodeIDRoundTrip(t *testing.T) {
	ids := []int{1, 2, 3, 100, 1000, easytier.NodeIDReservedSelfRelay, easytier.NodeIDReservedNoPublic, easytier.NodeIDMax}
	for _, id := range ids {
		rc, err := GenerateRoomCodeWithNodeID(id)
		if err != nil {
			t.Fatalf("GenerateRoomCodeWithNodeID(%d) failed: %v", id, err)
		}

		parsed, err := ParseRoomCode(rc.Format())
		if err != nil {
			t.Fatalf("Parse(%d) failed: %v", id, err)
		}
		if got := parsed.NodeID(); got != id {
			t.Fatalf("NodeID roundtrip: want %d, got %d (code %s)", id, got, rc.Format())
		}
	}
}

// TestNodeIDSpecialEncodings 验证特殊 ID 的字符组合。
func TestNodeIDSpecialEncodings(t *testing.T) {
	rc, err := GenerateRoomCodeWithNodeID(easytier.NodeIDReservedSelfRelay)
	if err != nil {
		t.Fatal(err)
	}
	if rc.NetworkPart[7] != '0' || rc.SecretPart[7] != '0' {
		t.Fatalf("self relay: want '0'+'0', got %c+%c", rc.NetworkPart[7], rc.SecretPart[7])
	}

	rc, err = GenerateRoomCodeWithNodeID(easytier.NodeIDReservedNoPublic)
	if err != nil {
		t.Fatal(err)
	}
	if rc.NetworkPart[7] != 'P' || rc.SecretPart[7] != 'P' {
		t.Fatalf("no public: want 'P'+'P', got %c+%c", rc.NetworkPart[7], rc.SecretPart[7])
	}
}

// TestNodeIDRange 验证越界拒绝。
func TestNodeIDRange(t *testing.T) {
	if _, err := GenerateRoomCodeWithNodeID(easytier.NodeIDMax + 1); err == nil {
		t.Fatal("expected error for nodeID > NodeIDMax")
	}
	if _, err := GenerateRoomCodeWithNodeID(-1); err == nil {
		t.Fatal("expected error for negative nodeID")
	}
}

// TestLegacyRoomCodeCompatibility 验证旧格式房间码（未内嵌 nodeID）仍可被
// 新解析器解析：校验通过、NodeID() 读出随机值。
func TestLegacyRoomCodeCompatibility(t *testing.T) {
	rc, err := GenerateRoomCode()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseRoomCode(rc.Format())
	if err != nil {
		t.Fatalf("legacy code parse failed: %v", err)
	}
	id := parsed.NodeID()
	if id < 0 || id > easytier.NodeIDMax {
		t.Fatalf("legacy code NodeID() out of range: %d", id)
	}
}

// TestNodeIDCharset 验证编码字符均在 34 字符集内。
func TestNodeIDCharset(t *testing.T) {
	for id := 0; id <= easytier.NodeIDMax; id++ {
		lo, hi := easytier.NodeIDChars(id)
		if _, ok := common.Value(common.Charset[lo]); !ok {
			t.Fatalf("low char not in charset for id %d", id)
		}
		if _, ok := common.Value(common.Charset[hi]); !ok {
			t.Fatalf("high char not in charset for id %d", id)
		}
	}
}
