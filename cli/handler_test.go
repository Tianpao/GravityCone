package cli

import (
	"testing"

	"gravitycone/core/protocol/paperconnect"
	"gravitycone/core/protocol/scaffolding"
)

// testHandler returns a Handler with fresh protocol services.
func testHandler() *Handler {
	return NewHandler(
		nil, nil,
		scaffolding.NewScaffoldingService(nil),
		paperconnect.NewPaperConnectService(nil),
		NewStdioWriter(),
		make(chan struct{}),
		"", "",
	)
}

// toInt 必须接受数字与数字字符串——启动器常以字符串形式发送整数参数，
// 此前 relay 的 node_id 传 "3" 会静默丢失并编码 0（自用中继 00）。
func TestToInt(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		want int
		ok   bool
	}{
		{"json number", float64(3), 3, true},
		{"int", 3, 3, true},
		{"numeric string", "3", 3, true},
		{"numeric string with space", " 3 ", 3, true},
		{"string zero", "0", 0, true},
		{"non-numeric string", "abc", 0, false},
		{"float string", "3.5", 0, false},
		{"bool", true, 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		got, ok := toInt(tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("toInt(%v) = (%d, %v), want (%d, %v)", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func relayParams(nodeID interface{}, url string) Request {
	relay := map[string]interface{}{"url": url}
	if nodeID != nil {
		relay["node_id"] = nodeID
	}
	return Request{Params: map[string]interface{}{"relay": relay}}
}

// 字符串形式的 node_id（"3"）必须与数字形式等效。
func TestApplyRelayParamsStringNodeID(t *testing.T) {
	h := testHandler()
	if err := h.applyRelayParams(relayParams("3", "tcp://1.2.3.4:5678")); err != nil {
		t.Fatalf("applyRelayParams failed: %v", err)
	}
}

// 数字形式的 node_id。
func TestApplyRelayParamsNumericNodeID(t *testing.T) {
	h := testHandler()
	if err := h.applyRelayParams(relayParams(float64(3), "tcp://1.2.3.4:5678")); err != nil {
		t.Fatalf("applyRelayParams failed: %v", err)
	}
}

// relay 对象缺省 node_id：不报错（默认 0 自用中继）。
func TestApplyRelayParamsURLOnly(t *testing.T) {
	h := testHandler()
	if err := h.applyRelayParams(relayParams(nil, "tcp://1.2.3.4:5678")); err != nil {
		t.Fatalf("applyRelayParams failed: %v", err)
	}
}

// 不携带 relay：不报错（房间可正常创建，仅走 P2P）。
func TestApplyRelayParamsNone(t *testing.T) {
	h := testHandler()
	if err := h.applyRelayParams(Request{Params: map[string]interface{}{}}); err != nil {
		t.Fatalf("applyRelayParams failed: %v", err)
	}
}

// relay 传了但无法解析为对象：参数错误。
func TestApplyRelayParamsNotObject(t *testing.T) {
	h := testHandler()
	req := Request{Params: map[string]interface{}{"relay": "tcp://1.2.3.4:5678"}}
	if err := h.applyRelayParams(req); err == nil {
		t.Fatal("applyRelayParams should fail for non-object relay")
	}
}

// node_id 无法解析为数字：参数错误，而不是静默编码 0。
func TestApplyRelayParamsInvalidNodeID(t *testing.T) {
	h := testHandler()
	if err := h.applyRelayParams(relayParams("abc", "tcp://1.2.3.4:5678")); err == nil {
		t.Fatal("applyRelayParams should fail for non-numeric node_id")
	}
}

// url 类型错误：参数错误。
func TestApplyRelayParamsInvalidURL(t *testing.T) {
	h := testHandler()
	req := Request{Params: map[string]interface{}{
		"relay": map[string]interface{}{"node_id": float64(3), "url": float64(123)},
	}}
	if err := h.applyRelayParams(req); err == nil {
		t.Fatal("applyRelayParams should fail for non-string url")
	}
}
