//go:build et_ffi

// Package easytier provides purego bindings to libeasytier_ffi (EasyTier's C ABI).
//
// On desktop platforms, GravityCone launches easytier-core as a subprocess.
// On Android, subprocesses are unreliable — instead we dynamically load
// libeasytier_ffi and run EasyTier in-process via its C API.
//
// Uses purego (github.com/ebitengine/purego) for zero-overhead C function
// calls. libeasytier_ffi.so must already be loaded before init (e.g., by
// Java's System.loadLibrary). A small CGO helper (dl_android.go) handles
// dlopen/dlsym; purego.RegisterFunc wraps the function pointers.
//
// Build: CGO_ENABLED=1 GOOS=android go build -tags et_ffi ...
package easytier

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// --- C function type declarations (registered at init) ---

var (
	etFnParseConfig         func(cfg *byte) int32
	etFnRunNetworkInstance  func(cfg *byte) int32
	etFnDeleteNetworkInst   func(instNames **byte, length uint64) int32
	etFnListInstance        func(infos unsafe.Pointer, maxLength uint64) int32
	etFnCollectNetworkInfos func(infos unsafe.Pointer, maxLength uint64) int32
	etFnSetTunFd            func(instName *byte, fd int32) int32
	etFnCallJSONRPC         func(service, method, domain, payload *byte, outResponse **byte) int32
	etFnGetErrorMsg         func(out **byte)
	etFnFreeString          func(s *byte)

	etInitOnce sync.Once
	etInitErr  error
)

// etInit performs one-time library loading and function registration.
// Safe to call concurrently; subsequent calls are no-ops.
func etInit() {
	etInitOnce.Do(func() {
		// On Android, libeasytier_ffi.so is pre-loaded by System.loadLibrary()
		// before our libgravitycone.so is loaded. dlopen returns the existing
		// handle (reference-counted on Bionic).
		etHandle := dlopenLib()
		if etHandle == nil {
			etInitErr = fmt.Errorf("dlopen libeasytier_ffi.so failed")
			return
		}

		purego.RegisterFunc(&etFnParseConfig, dlsymLib(etHandle, "parse_config"))
		purego.RegisterFunc(&etFnRunNetworkInstance, dlsymLib(etHandle, "run_network_instance"))
		purego.RegisterFunc(&etFnDeleteNetworkInst, dlsymLib(etHandle, "delete_network_instance"))
		purego.RegisterFunc(&etFnListInstance, dlsymLib(etHandle, "list_instance"))
		purego.RegisterFunc(&etFnCollectNetworkInfos, dlsymLib(etHandle, "collect_network_infos"))
		purego.RegisterFunc(&etFnSetTunFd, dlsymLib(etHandle, "set_tun_fd"))
		purego.RegisterFunc(&etFnCallJSONRPC, dlsymLib(etHandle, "call_json_rpc"))
		purego.RegisterFunc(&etFnGetErrorMsg, dlsymLib(etHandle, "get_error_msg"))
		purego.RegisterFunc(&etFnFreeString, dlsymLib(etHandle, "free_string"))
	})
}

// --- String helpers ---

// cstrBytes converts a Go string to a null-terminated byte slice.
func cstrBytes(s string) []byte {
	b := make([]byte, len(s)+1)
	copy(b, s)
	return b
}

// ptr takes a pointer to the first element of b.
func ptr(b []byte) *byte {
	if len(b) == 0 {
		return nil
	}
	return &b[0]
}

// cGoString converts a null-terminated C string pointer to a Go string.
func cGoString(p *byte) string {
	if p == nil {
		return ""
	}
	// Scan for null terminator.
	b := unsafe.Slice(p, 1<<30)
	n := 0
	for b[n] != 0 {
		n++
	}
	return string(b[:n])
}

// --- Error handling ---

// getFFIError returns the last FFI error message, or empty string if none.
func getFFIError() string {
	etInit()
	if etInitErr != nil {
		return fmt.Sprintf("init: %v", etInitErr)
	}

	var outPtr *byte
	etFnGetErrorMsg(&outPtr)
	if outPtr == nil {
		return ""
	}
	defer etFnFreeString(outPtr)
	return cGoString(outPtr)
}

// --- Config ---

// ParseConfig validates a TOML config string without starting an instance.
func ParseConfig(tomlCfg string) error {
	etInit()
	if etInitErr != nil {
		return fmt.Errorf("parse_config: %w", etInitErr)
	}

	b := cstrBytes(tomlCfg)
	ret := etFnParseConfig(ptr(b))
	runtime.KeepAlive(b)
	if ret != 0 {
		if msg := getFFIError(); msg != "" {
			return fmt.Errorf("parse_config: %s", msg)
		}
		return fmt.Errorf("parse_config: unknown error (ret=%d)", ret)
	}
	return nil
}

// RunNetworkInstance starts an EasyTier network instance from a TOML config.
func RunNetworkInstance(tomlCfg string) error {
	etInit()
	if etInitErr != nil {
		return fmt.Errorf("run_network_instance: %w", etInitErr)
	}

	b := cstrBytes(tomlCfg)
	ret := etFnRunNetworkInstance(ptr(b))
	runtime.KeepAlive(b)
	if ret != 0 {
		if msg := getFFIError(); msg != "" {
			return fmt.Errorf("run_network_instance: %s", msg)
		}
		return fmt.Errorf("run_network_instance: unknown error (ret=%d)", ret)
	}
	return nil
}

// DeleteNetworkInstance stops named instances.
func DeleteNetworkInstance(instNames []string) error {
	etInit()
	if etInitErr != nil {
		return fmt.Errorf("delete_network_instance: %w", etInitErr)
	}

	if len(instNames) == 0 {
		return nil
	}

	// Build array of C string pointers.
	strPtrs := make([]*byte, len(instNames))
	for i, name := range instNames {
		b := cstrBytes(name)
		strPtrs[i] = ptr(b)
	}

	// const char** is a pointer to the first element.
	ret := etFnDeleteNetworkInst(&strPtrs[0], uint64(len(instNames)))
	runtime.KeepAlive(strPtrs)
	if ret != 0 {
		if msg := getFFIError(); msg != "" {
			return fmt.Errorf("delete_network_instance: %s", msg)
		}
		return fmt.Errorf("delete_network_instance: unknown error (ret=%d)", ret)
	}
	return nil
}

// --- Instance Info ---

// cKeyValuePair mirrors the C struct { char *key; char *value; } (16 bytes).
type cKeyValuePair struct {
	key   *byte
	value *byte
}

// InstanceInfo holds a key-value pair returned by collect_network_infos.
type InstanceInfo struct {
	Name string
	Info string // JSON string with instance details
}

// CollectNetworkInfos returns information about running instances.
func CollectNetworkInfos(maxCount int) ([]InstanceInfo, error) {
	etInit()
	if etInitErr != nil {
		return nil, fmt.Errorf("collect_network_infos: %w", etInitErr)
	}

	if maxCount <= 0 {
		maxCount = 32
	}

	infos := make([]cKeyValuePair, maxCount)
	ret := etFnCollectNetworkInfos(unsafe.Pointer(&infos[0]), uint64(maxCount))
	runtime.KeepAlive(infos)
	if ret < 0 {
		if msg := getFFIError(); msg != "" {
			return nil, fmt.Errorf("collect_network_infos: %s", msg)
		}
		return nil, fmt.Errorf("collect_network_infos: unknown error (ret=%d)", ret)
	}

	count := int(ret)
	result := make([]InstanceInfo, 0, count)
	for i := 0; i < count; i++ {
		info := InstanceInfo{
			Name: cGoString(infos[i].key),
			Info: cGoString(infos[i].value),
		}
		etFnFreeString(infos[i].key)
		etFnFreeString(infos[i].value)
		result = append(result, info)
	}
	return result, nil
}

// ListInstances returns running instance names and IDs.
func ListInstances(maxCount int) ([]InstanceInfo, error) {
	etInit()
	if etInitErr != nil {
		return nil, fmt.Errorf("list_instance: %w", etInitErr)
	}

	if maxCount <= 0 {
		maxCount = 32
	}

	infos := make([]cKeyValuePair, maxCount)
	ret := etFnListInstance(unsafe.Pointer(&infos[0]), uint64(maxCount))
	runtime.KeepAlive(infos)
	if ret < 0 {
		if msg := getFFIError(); msg != "" {
			return nil, fmt.Errorf("list_instance: %s", msg)
		}
		return nil, fmt.Errorf("list_instance: unknown error (ret=%d)", ret)
	}

	count := int(ret)
	result := make([]InstanceInfo, 0, count)
	for i := 0; i < count; i++ {
		info := InstanceInfo{
			Name: cGoString(infos[i].key),
			Info: cGoString(infos[i].value),
		}
		etFnFreeString(infos[i].key)
		etFnFreeString(infos[i].value)
		result = append(result, info)
	}
	return result, nil
}

// --- TUN fd ---

// SetTunFd attaches a TUN file descriptor to a named instance.
// This is how Android VpnService fd gets injected into EasyTier.
func SetTunFd(instName string, fd int) error {
	etInit()
	if etInitErr != nil {
		return fmt.Errorf("set_tun_fd: %w", etInitErr)
	}

	b := cstrBytes(instName)
	ret := etFnSetTunFd(ptr(b), int32(fd))
	runtime.KeepAlive(b)
	if ret != 0 {
		if msg := getFFIError(); msg != "" {
			return fmt.Errorf("set_tun_fd: %s", msg)
		}
		return fmt.Errorf("set_tun_fd: unknown error (ret=%d)", ret)
	}
	return nil
}

// --- JSON RPC ---

// CallJSONRPC calls an EasyTier RPC method with protobuf JSON payload.
//
// serviceName examples:
//   - "api.manage.PeerManageService"
//   - "api.instance.InstanceService"
//   - "api.config.ConfigService"
//
// domainName is only used by TcpProxyRpcService; pass "" for most calls.
//
// payloadJSON must include any "instance" selector required by the RPC.
// Returns the response JSON string. Caller should parse it.
func CallJSONRPC(serviceName, methodName, domainName, payloadJSON string) (string, error) {
	etInit()
	if etInitErr != nil {
		return "", fmt.Errorf("call_json_rpc(%s.%s): %w", serviceName, methodName, etInitErr)
	}

	sb := cstrBytes(serviceName)
	mb := cstrBytes(methodName)
	pb := cstrBytes(payloadJSON)

	var db []byte
	if domainName != "" {
		db = cstrBytes(domainName)
	}

	var outJSON *byte

	ret := etFnCallJSONRPC(ptr(sb), ptr(mb), ptr(db), ptr(pb), &outJSON)
	runtime.KeepAlive(sb)
	runtime.KeepAlive(mb)
	runtime.KeepAlive(pb)
	runtime.KeepAlive(db)
	if ret != 0 {
		if msg := getFFIError(); msg != "" {
			return "", fmt.Errorf("call_json_rpc(%s.%s): %s", serviceName, methodName, msg)
		}
		return "", fmt.Errorf("call_json_rpc(%s.%s): unknown error (ret=%d)", serviceName, methodName, ret)
	}

	if outJSON == nil {
		return "", fmt.Errorf("call_json_rpc(%s.%s): null response", serviceName, methodName)
	}
	defer etFnFreeString(outJSON)

	return cGoString(outJSON), nil
}

// --- Node info helpers (commonly used RPCs) ---

// NodeInfoResponse is the JSON response from show_node_info RPC.
type NodeInfoResponse struct {
	NodeInfo *struct {
		VirtualIP string `json:"ipv4_addr"`
		PeerID    string `json:"peer_id"`
		Hostname  string `json:"hostname"`
	} `json:"node_info,omitempty"`
	Error string `json:"error,omitempty"`
}

// PeerRouteEntry is a single route entry from list_route RPC.
type PeerRouteEntry struct {
	Hostname   string   `json:"hostname"`
	IPv4Addr   string   `json:"ipv4_addr"`
	PeerID     string   `json:"peer_id"`
	ProxyCIDRs []string `json:"proxy_cidrs"`
}

// ListRouteResponse is the JSON response from list_route RPC.
type ListRouteResponse struct {
	Routes []PeerRouteEntry `json:"routes"`
}

// PatchConfigRequest is the request for patch_config RPC.
type PatchConfigRequest struct {
	Patch *PatchConfig `json:"patch"`
}

// PatchConfig is the config patch payload.
type PatchConfig struct {
	PortForwards []PortForwardPatch `json:"port_forwards"`
}

// PortForwardPatch is a single port forward entry in a config patch.
type PortForwardPatch struct {
	Action int               `json:"action"` // 1=Add, 2=Remove
	Cfg    PortForwardConfig `json:"cfg"`
}

// PortForwardConfig is a port forward configuration.
type PortForwardConfig struct {
	BindAddr string `json:"bind_addr"`
	DstAddr  string `json:"dst_addr"`
	Proto    int    `json:"socket_type"` // 0=TCP, 1=UDP
}

// GetNodeInfo returns the local node's virtual IP and peer ID.
func GetNodeInfo() (*NodeInfoResponse, error) {
	respJSON, err := CallJSONRPC(
		"api.manage.PeerManageService",
		"show_node_info",
		"",
		`{}`,
	)
	if err != nil {
		return nil, err
	}

	var resp NodeInfoResponse
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		return nil, fmt.Errorf("parse node_info response: %w", err)
	}
	return &resp, nil
}

// ListRoutes returns all peer routes (includes hostname, IP, peer_id).
func ListRoutes() (*ListRouteResponse, error) {
	respJSON, err := CallJSONRPC(
		"api.instance.InstanceService",
		"list_route",
		"",
		`{}`,
	)
	if err != nil {
		return nil, err
	}

	var resp ListRouteResponse
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		return nil, fmt.Errorf("parse list_route response: %w", err)
	}
	return &resp, nil
}

// AddPortForwardRPC adds a port forward via the config RPC.
func AddPortForwardRPC(proto int, bindAddr, dstAddr string) error {
	req := PatchConfigRequest{
		Patch: &PatchConfig{
			PortForwards: []PortForwardPatch{
				{
					Action: 1, // Add
					Cfg: PortForwardConfig{
						BindAddr: bindAddr,
						DstAddr:  dstAddr,
						Proto:    proto,
					},
				},
			},
		},
	}
	payload, _ := json.Marshal(req)

	_, err := CallJSONRPC(
		"api.config.ConfigService",
		"patch_config",
		"",
		string(payload),
	)
	return err
}

// RemovePortForwardRPC removes a port forward via the config RPC.
func RemovePortForwardRPC(proto int, bindAddr, dstAddr string) error {
	req := PatchConfigRequest{
		Patch: &PatchConfig{
			PortForwards: []PortForwardPatch{
				{
					Action: 2, // Remove
					Cfg: PortForwardConfig{
						BindAddr: bindAddr,
						DstAddr:  dstAddr,
						Proto:    proto,
					},
				},
			},
		},
	}
	payload, _ := json.Marshal(req)

	_, err := CallJSONRPC(
		"api.config.ConfigService",
		"patch_config",
		"",
		string(payload),
	)
	return err
}
