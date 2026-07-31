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
	"strconv"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// --- C function type declarations (registered at init) ---

// IMPORTANT: This table must match the symbols actually exported by
// easytier-ffi v2.6.4 (see ffi/android/.cache/easytier/easytier-contrib/
// easytier-ffi/src/lib.rs). Registering a symbol that dlsym cannot resolve
// makes purego panic ("cfn is nil") on the first call — an unrecoverable
// crash in the c-shared build. If a symbol is missing here, etInit fails
// gracefully and all FFI calls return an error instead.
//
// v2.6.4 exports: parse_config, run_network_instance, retain_network_instance,
// collect_network_infos, set_tun_fd, get_error_msg, free_string.
var (
	etFnParseConfig         func(cfg *byte) int32
	etFnRunNetworkInstance  func(cfg *byte) int32
	etFnRetainNetworkInst   func(instNames **byte, length uint64) int32
	etFnCollectNetworkInfos func(infos unsafe.Pointer, maxLength uint64) int32
	etFnSetTunFd            func(instName *byte, fd int32) int32
	etFnGetErrorMsg         func(out **byte)
	etFnFreeString          func(s *byte)

	etInitOnce sync.Once
	etInitErr  error
)

// registerFFIFunc resolves one symbol and registers it with purego.
// Returns an error (instead of panicking inside purego) when the symbol
// does not exist in the loaded library.
func registerFFIFunc(fptr interface{}, handle unsafe.Pointer, name string) error {
	cfn := dlsymLib(handle, name)
	if cfn == 0 {
		return fmt.Errorf("dlsym %s: symbol not found in libeasytier_ffi.so", name)
	}
	purego.RegisterFunc(fptr, cfn)
	return nil
}

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

		var err error
		if err = registerFFIFunc(&etFnParseConfig, etHandle, "parse_config"); err == nil {
			err = registerFFIFunc(&etFnRunNetworkInstance, etHandle, "run_network_instance")
		}
		if err == nil {
			err = registerFFIFunc(&etFnRetainNetworkInst, etHandle, "retain_network_instance")
		}
		if err == nil {
			err = registerFFIFunc(&etFnCollectNetworkInfos, etHandle, "collect_network_infos")
		}
		if err == nil {
			err = registerFFIFunc(&etFnSetTunFd, etHandle, "set_tun_fd")
		}
		if err == nil {
			err = registerFFIFunc(&etFnGetErrorMsg, etHandle, "get_error_msg")
		}
		if err == nil {
			err = registerFFIFunc(&etFnFreeString, etHandle, "free_string")
		}
		if err != nil {
			etInitErr = err
			return
		}
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
//
// easytier-ffi v2.6.4 has no delete_network_instance export; the equivalent
// primitive is retain_network_instance (keep only the given names). Deleting
// a set of instances therefore means retaining everything else, computed by
// first enumerating the running instances via collect_network_infos.
func DeleteNetworkInstance(instNames []string) error {
	etInit()
	if etInitErr != nil {
		return fmt.Errorf("delete_network_instance: %w", etInitErr)
	}

	// Go semantics: deleting nothing is a no-op. (retain_network_instance
	// with an empty list would clear ALL instances, so guard against it.)
	if len(instNames) == 0 {
		return nil
	}

	toDelete := make(map[string]struct{}, len(instNames))
	for _, n := range instNames {
		toDelete[n] = struct{}{}
	}

	infos, err := CollectNetworkInfos(64)
	if err != nil {
		return fmt.Errorf("delete_network_instance: 枚举实例失败: %w", err)
	}

	retain := make([]string, 0, len(infos))
	for _, info := range infos {
		if _, del := toDelete[info.Name]; !del {
			retain = append(retain, info.Name)
		}
	}

	if len(infos) == 0 {
		// Nothing running; nothing to do.
		return nil
	}

	if err := callRetainNetworkInstance(retain); err != nil {
		return fmt.Errorf("delete_network_instance: %w", err)
	}
	return nil
}

// callRetainNetworkInstance invokes retain_network_instance with the given
// names (empty = clear all instances).
func callRetainNetworkInstance(retainNames []string) error {
	// Build array of C string pointers.
	strPtrs := make([]*byte, len(retainNames))
	for i, name := range retainNames {
		b := cstrBytes(name)
		strPtrs[i] = ptr(b)
	}

	// const char** is a pointer to the first element. With zero elements we
	// pass a non-nil pointer to a zero-length slice (safe: Rust checks length
	// before dereferencing).
	if len(strPtrs) == 0 {
		empty := make([]*byte, 1)
		strPtrs = empty
	}
	ret := etFnRetainNetworkInst(&strPtrs[0], uint64(len(retainNames)))
	runtime.KeepAlive(strPtrs)
	if ret != 0 {
		if msg := getFFIError(); msg != "" {
			return fmt.Errorf("retain_network_instance: %s", msg)
		}
		return fmt.Errorf("retain_network_instance: unknown error (ret=%d)", ret)
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

// ListInstances returns running instance names and their info JSON.
//
// easytier-ffi v2.6.4 has no list_instance export; collect_network_infos
// returns the same name → info map, so we reuse it.
func ListInstances(maxCount int) ([]InstanceInfo, error) {
	return CollectNetworkInfos(maxCount)
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

// --- Instance info (collect_network_infos JSON) ---
//
// easytier-ffi v2.6.4 has no JSON-RPC export (no call_json_rpc), so all
// runtime queries go through collect_network_infos. Its value JSON is the
// prost-generated serde serialization of NetworkInstanceRunningInfo:
//
//	NetworkInstanceRunningInfo {
//	  dev_name: string, my_node_info: MyNodeInfo, events: [string],
//	  routes: [Route], peers: [...], peer_route_pairs: [...],
//	  running: bool, error_msg: string?
//	}
//	MyNodeInfo { virtual_ipv4: Ipv4Inet?, hostname, version, ips, peer_id: uint32 }
//	Route { peer_id: uint32, ipv4_addr: Ipv4Inet, hostname, proxy_cidrs: [string], ... }
//	Ipv4Inet { address: Ipv4Addr, network_length: uint32 }
//	Ipv4Addr { addr: uint32 }   // dotted-quad packed big-endian

type ffiIPv4Addr struct {
	Addr uint32 `json:"addr"`
}

type ffiIPv4Inet struct {
	Address       ffiIPv4Addr `json:"address"`
	NetworkLength uint32      `json:"network_length"`
}

type ffiMyNodeInfo struct {
	VirtualIP4 *ffiIPv4Inet `json:"virtual_ipv4"`
	Hostname   string       `json:"hostname"`
	PeerID     uint32       `json:"peer_id"`
}

type ffiRoute struct {
	PeerID     uint32       `json:"peer_id"`
	IPv4Addr   *ffiIPv4Inet `json:"ipv4_addr"`
	Hostname   string       `json:"hostname"`
	ProxyCIDRs []string     `json:"proxy_cidrs"`
}

type ffiRunningInfo struct {
	DevName    string         `json:"dev_name"`
	MyNodeInfo *ffiMyNodeInfo `json:"my_node_info"`
	Routes     []ffiRoute     `json:"routes"`
	Running    bool           `json:"running"`
	ErrorMsg   string         `json:"error_msg"`
}

// ipv4InetString renders an Ipv4Inet as "a.b.c.d/nn" (CIDR suffix included
// when network_length is set), or "" when nil.
func ipv4InetString(in *ffiIPv4Inet) string {
	if in == nil {
		return ""
	}
	addr := in.Address.Addr
	s := fmt.Sprintf("%d.%d.%d.%d", byte(addr>>24), byte(addr>>16), byte(addr>>8), byte(addr))
	if in.NetworkLength > 0 && in.NetworkLength <= 32 {
		return fmt.Sprintf("%s/%d", s, in.NetworkLength)
	}
	return s
}

// firstRunningInfo fetches the collect_network_infos payload of the first
// running instance (FFI mode runs a single EasyTier instance at a time).
func firstRunningInfo() (*ffiRunningInfo, error) {
	etInit()
	if etInitErr != nil {
		return nil, etInitErr
	}
	infos, err := CollectNetworkInfos(32)
	if err != nil {
		return nil, err
	}
	for _, info := range infos {
		var ri ffiRunningInfo
		if err := json.Unmarshal([]byte(info.Info), &ri); err != nil {
			continue
		}
		if ri.ErrorMsg != "" {
			return nil, fmt.Errorf("实例运行错误: %s", ri.ErrorMsg)
		}
		return &ri, nil
	}
	return nil, fmt.Errorf("无运行中的 EasyTier 实例")
}

// --- Node info helpers (from collect_network_infos, no RPC) ---

// NodeInfoResponse mirrors the shape of show_node_info for compatibility.
type NodeInfoResponse struct {
	NodeInfo *struct {
		VirtualIP string `json:"ipv4_addr"`
		PeerID    string `json:"peer_id"`
		Hostname  string `json:"hostname"`
	} `json:"node_info,omitempty"`
	Error string `json:"error,omitempty"`
}

// PeerRouteEntry is a single route entry (from the instance's route table).
type PeerRouteEntry struct {
	Hostname   string   `json:"hostname"`
	IPv4Addr   string   `json:"ipv4_addr"`
	PeerID     string   `json:"peer_id"`
	ProxyCIDRs []string `json:"proxy_cidrs"`
}

// ListRouteResponse is the parsed route table.
type ListRouteResponse struct {
	Routes []PeerRouteEntry `json:"routes"`
}

// GetNodeInfo returns the local node's virtual IP, peer ID and hostname.
func GetNodeInfo() (*NodeInfoResponse, error) {
	ri, err := firstRunningInfo()
	if err != nil {
		return nil, err
	}
	if ri.MyNodeInfo == nil {
		return nil, fmt.Errorf("节点信息为空")
	}
	ni := ri.MyNodeInfo
	return &NodeInfoResponse{
		NodeInfo: &struct {
			VirtualIP string `json:"ipv4_addr"`
			PeerID    string `json:"peer_id"`
			Hostname  string `json:"hostname"`
		}{
			VirtualIP: ipv4InetString(ni.VirtualIP4),
			PeerID:    strconv.FormatUint(uint64(ni.PeerID), 10),
			Hostname:  ni.Hostname,
		},
	}, nil
}

// ListRoutes returns all peer routes (includes hostname, IP, peer_id).
func ListRoutes() (*ListRouteResponse, error) {
	ri, err := firstRunningInfo()
	if err != nil {
		return nil, err
	}
	routes := make([]PeerRouteEntry, 0, len(ri.Routes))
	for _, r := range ri.Routes {
		routes = append(routes, PeerRouteEntry{
			Hostname:   r.Hostname,
			IPv4Addr:   ipv4InetString(r.IPv4Addr),
			PeerID:     strconv.FormatUint(uint64(r.PeerID), 10),
			ProxyCIDRs: r.ProxyCIDRs,
		})
	}
	return &ListRouteResponse{Routes: routes}, nil
}
