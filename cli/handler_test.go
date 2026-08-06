//go:build !et_ffi

package cli

import (
	"testing"

	"gravitycone/core/protocol/paperconnect"
	"gravitycone/core/protocol/scaffolding"
)

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
		in   any
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

func relayParams(nodeID any, url string) Request {
	relay := map[string]any{"url": url}
	if nodeID != nil {
		relay["node_id"] = nodeID
	}
	return Request{Params: map[string]any{"relay": relay}}
}

// 中继参数解析：合法输入不报错（字符串/数字 node_id 等效、缺省 node_id、
// 不携带 relay），非法输入报错（非对象、node_id 非数字、url 非字符串）。
func TestApplyRelayParams(t *testing.T) {
	tests := []struct {
		name    string
		req     Request
		wantErr bool
	}{
		{"string node_id", relayParams("3", "tcp://1.2.3.4:5678"), false},
		{"numeric node_id", relayParams(float64(3), "tcp://1.2.3.4:5678"), false},
		{"url only", relayParams(nil, "tcp://1.2.3.4:5678"), false},
		{"no relay", Request{Params: map[string]any{}}, false},
		{"not object", Request{Params: map[string]any{"relay": "tcp://1.2.3.4:5678"}}, true},
		{"invalid node_id", relayParams("abc", "tcp://1.2.3.4:5678"), true},
		{"invalid url", Request{Params: map[string]any{
			"relay": map[string]any{"node_id": float64(3), "url": float64(123)},
		}}, true},
	}
	for _, tt := range tests {
		err := testHandler().applyRelayParams(tt.req)
		if (err != nil) != tt.wantErr {
			t.Errorf("%s: applyRelayParams err = %v, wantErr = %v", tt.name, err, tt.wantErr)
		}
	}
}
