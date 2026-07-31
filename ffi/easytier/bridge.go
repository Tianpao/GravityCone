//go:build et_ffi

// Package easytier provides goffi bindings to libeasytier_ffi (EasyTier's C ABI).
//
// On desktop platforms, GravityCone launches easytier-core as a subprocess.
// On Android, subprocesses are unreliable — instead we dynamically load
// libeasytier_ffi and run EasyTier in-process via its C API.
//
// Uses goffi (github.com/go-webgpu/goffi) — zero-CGO FFI. The .so is loaded
// at runtime via dlopen, so there is no compile-time link dependency on
// libeasytier_ffi. The library must already be loaded before init (e.g., by
// Java's System.loadLibrary) so goffi can obtain a handle.
//
// Build: CGO_ENABLED=1 GOOS=android go build -tags et_ffi ...
// (CGO is still required by ffi/common/ for //export and JNI, but this
// package no longer needs #cgo LDFLAGS.)
package easytier

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/go-webgpu/goffi/ffi"
	"github.com/go-webgpu/goffi/types"
)

// --- Global initialization (one-time, lazy) ---

var (
	etHandle unsafe.Pointer // libeasytier_ffi handle

	// Cached CIFs (Call Interfaces) — prepared once, reused across calls.
	etCIFParseConfig         types.CallInterface
	etCIFRunNetworkInstance  types.CallInterface
	etCIFDeleteNetworkInst   types.CallInterface
	etCIFListInstance        types.CallInterface
	etCIFCollectNetworkInfos types.CallInterface
	etCIFSetTunFd            types.CallInterface
	etCIFCallJSONRPC         types.CallInterface
	etCIFGetErrorMsg         types.CallInterface
	etCIFFreeString          types.CallInterface

	// Cached function pointers.
	etFnParseConfig         unsafe.Pointer
	etFnRunNetworkInstance  unsafe.Pointer
	etFnDeleteNetworkInst   unsafe.Pointer
	etFnListInstance        unsafe.Pointer
	etFnCollectNetworkInfos unsafe.Pointer
	etFnSetTunFd            unsafe.Pointer
	etFnCallJSONRPC         unsafe.Pointer
	etFnGetErrorMsg         unsafe.Pointer
	etFnFreeString          unsafe.Pointer

	etInitOnce sync.Once
	etInitErr  error
)

// etInit performs one-time library loading and CIF preparation.
// Safe to call concurrently; subsequent calls are no-ops.
func etInit() {
	etInitOnce.Do(func() {
		// 1. Load the library.
		// On Android, libeasytier_ffi.so is pre-loaded by System.loadLibrary()
		// before our libgravitycone.so is loaded. dlopen returns the existing
		// handle (reference-counted on Bionic).
		etHandle, etInitErr = ffi.LoadLibrary("libeasytier_ffi.so")
		if etInitErr != nil {
			return
		}

		// 2. Get all function pointers.
		etFnParseConfig, etInitErr = ffi.GetSymbol(etHandle, "parse_config")
		if etInitErr != nil {
			return
		}
		etFnRunNetworkInstance, etInitErr = ffi.GetSymbol(etHandle, "run_network_instance")
		if etInitErr != nil {
			return
		}
		etFnDeleteNetworkInst, etInitErr = ffi.GetSymbol(etHandle, "delete_network_instance")
		if etInitErr != nil {
			return
		}
		etFnListInstance, etInitErr = ffi.GetSymbol(etHandle, "list_instance")
		if etInitErr != nil {
			return
		}
		etFnCollectNetworkInfos, etInitErr = ffi.GetSymbol(etHandle, "collect_network_infos")
		if etInitErr != nil {
			return
		}
		etFnSetTunFd, etInitErr = ffi.GetSymbol(etHandle, "set_tun_fd")
		if etInitErr != nil {
			return
		}
		etFnCallJSONRPC, etInitErr = ffi.GetSymbol(etHandle, "call_json_rpc")
		if etInitErr != nil {
			return
		}
		etFnGetErrorMsg, etInitErr = ffi.GetSymbol(etHandle, "get_error_msg")
		if etInitErr != nil {
			return
		}
		etFnFreeString, etInitErr = ffi.GetSymbol(etHandle, "free_string")
		if etInitErr != nil {
			return
		}

		// 3. Prepare all CIFs (zero-allocation reusable call interfaces).
		// All functions use the platform's default C calling convention
		// (AAPCS64 on Android arm64, System V AMD64 on Android x86_64).

		// parse_config(const char* cfg_str) → int
		etInitErr = ffi.PrepareCallInterface(&etCIFParseConfig, types.DefaultCall,
			types.SInt32TypeDescriptor,
			[]*types.TypeDescriptor{types.PointerTypeDescriptor})
		if etInitErr != nil {
			return
		}

		// run_network_instance(const char* cfg_str) → int
		etInitErr = ffi.PrepareCallInterface(&etCIFRunNetworkInstance, types.DefaultCall,
			types.SInt32TypeDescriptor,
			[]*types.TypeDescriptor{types.PointerTypeDescriptor})
		if etInitErr != nil {
			return
		}

		// delete_network_instance(const char** inst_names, unsigned long length) → int
		etInitErr = ffi.PrepareCallInterface(&etCIFDeleteNetworkInst, types.DefaultCall,
			types.SInt32TypeDescriptor,
			[]*types.TypeDescriptor{types.PointerTypeDescriptor, types.UInt64TypeDescriptor})
		if etInitErr != nil {
			return
		}

		// list_instance(void* infos, unsigned long max_length) → int
		etInitErr = ffi.PrepareCallInterface(&etCIFListInstance, types.DefaultCall,
			types.SInt32TypeDescriptor,
			[]*types.TypeDescriptor{types.PointerTypeDescriptor, types.UInt64TypeDescriptor})
		if etInitErr != nil {
			return
		}

		// collect_network_infos(void* infos, unsigned long max_length) → int
		etInitErr = ffi.PrepareCallInterface(&etCIFCollectNetworkInfos, types.DefaultCall,
			types.SInt32TypeDescriptor,
			[]*types.TypeDescriptor{types.PointerTypeDescriptor, types.UInt64TypeDescriptor})
		if etInitErr != nil {
			return
		}

		// set_tun_fd(const char* inst_name, int fd) → int
		etInitErr = ffi.PrepareCallInterface(&etCIFSetTunFd, types.DefaultCall,
			types.SInt32TypeDescriptor,
			[]*types.TypeDescriptor{types.PointerTypeDescriptor, types.SInt32TypeDescriptor})
		if etInitErr != nil {
			return
		}

		// call_json_rpc(const char* service, const char* method,
		//               const char* domain, const char* payload,
		//               char** out_response) → int
		etInitErr = ffi.PrepareCallInterface(&etCIFCallJSONRPC, types.DefaultCall,
			types.SInt32TypeDescriptor,
			[]*types.TypeDescriptor{
				types.PointerTypeDescriptor, // service_name (const char*)
				types.PointerTypeDescriptor, // method_name (const char*)
				types.PointerTypeDescriptor, // domain_name (const char*, may be NULL)
				types.PointerTypeDescriptor, // payload_json (const char*)
				types.PointerTypeDescriptor, // out_response_json (char**)
			})
		if etInitErr != nil {
			return
		}

		// get_error_msg(const char** out) → void
		etInitErr = ffi.PrepareCallInterface(&etCIFGetErrorMsg, types.DefaultCall,
			types.VoidTypeDescriptor,
			[]*types.TypeDescriptor{types.PointerTypeDescriptor}) // const char**
		if etInitErr != nil {
			return
		}

		// free_string(const char* s) → void
		etInitErr = ffi.PrepareCallInterface(&etCIFFreeString, types.DefaultCall,
			types.VoidTypeDescriptor,
			[]*types.TypeDescriptor{types.PointerTypeDescriptor}) // const char*
	})
}

// callMu protects CallFunction calls, satisfying goffi's requirement that
// a single CallInterface must not be used concurrently from multiple goroutines.
// On Android, JNI threads and Go poll goroutines may call these functions
// simultaneously. Cost is negligible (calls are infrequent, < 1 per 200ms).
var callMu sync.Mutex

// --- Low-level helpers ---

// toCString creates a null-terminated C string in Go-managed memory.
// Returns a pointer to the data and the backing byte slice (must be kept
// alive via runtime.KeepAlive until the C call returns).
func toCString(s string) (unsafe.Pointer, []byte) {
	b := append([]byte(s), 0)
	return unsafe.Pointer(unsafe.SliceData(b)), b
}

// cGoString reads a null-terminated C string from a raw pointer.
func cGoString(ptr unsafe.Pointer) string {
	if ptr == nil {
		return ""
	}
	// Scan for null terminator.
	b := (*[1 << 30]byte)(ptr)
	n := 0
	for b[n] != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(ptr), n))
}

// callVoid calls a void-returning C function under callMu.
// cif and fn must be pre-initialized. args follows goffi convention
// (pointers to argument values).
func callVoid(cif *types.CallInterface, fn unsafe.Pointer, args ...unsafe.Pointer) error {
	callMu.Lock()
	_, err := ffi.CallFunction(cif, fn, nil, args)
	callMu.Unlock()
	return err
}

// callInt32 calls a C function returning int32_t (Go int32) under callMu.
func callInt32(cif *types.CallInterface, fn unsafe.Pointer, args ...unsafe.Pointer) (int32, error) {
	var result int32
	callMu.Lock()
	_, err := ffi.CallFunction(cif, fn, unsafe.Pointer(&result), args)
	callMu.Unlock()
	return result, err
}

// --- Error handling ---

// getFFIError returns the last FFI error message, or empty string if none.
func getFFIError() string {
	etInit()
	if etInitErr != nil {
		return fmt.Sprintf("goffi.init: %v", etInitErr)
	}

	var outPtr unsafe.Pointer
	if err := callVoid(&etCIFGetErrorMsg, etFnGetErrorMsg,
		unsafe.Pointer(&outPtr)); err != nil {
		return fmt.Sprintf("goffi.call: %v", err)
	}
	if outPtr == nil {
		return ""
	}
	defer freeString(outPtr)
	return cGoString(outPtr)
}

// freeString frees a C string allocated by libeasytier_ffi.
func freeString(ptr unsafe.Pointer) {
	if ptr == nil {
		return
	}
	// Pass pointer-to-pointer: goffi convention for PointerTypeDescriptor.
	_ = callVoid(&etCIFFreeString, etFnFreeString, unsafe.Pointer(&ptr))
}

// --- Config ---

// ParseConfig validates a TOML config string without starting an instance.
func ParseConfig(tomlCfg string) error {
	etInit()
	if etInitErr != nil {
		return fmt.Errorf("parse_config: %w", etInitErr)
	}

	dataPtr, keep := toCString(tomlCfg)

	ret, err := callInt32(&etCIFParseConfig, etFnParseConfig,
		unsafe.Pointer(&dataPtr))
	runtime.KeepAlive(keep)
	if err != nil {
		return fmt.Errorf("parse_config: %w", err)
	}
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

	dataPtr, keep := toCString(tomlCfg)

	ret, err := callInt32(&etCIFRunNetworkInstance, etFnRunNetworkInstance,
		unsafe.Pointer(&dataPtr))
	runtime.KeepAlive(keep)
	if err != nil {
		return fmt.Errorf("run_network_instance: %w", err)
	}
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

	// Build array of C strings: each element is a pointer to null-terminated data.
	var (
		strDataPtrs []unsafe.Pointer
		strBytes    [][]byte // for GC lifetime
	)
	for _, name := range instNames {
		dataPtr, keep := toCString(name)
		strDataPtrs = append(strDataPtrs, dataPtr)
		strBytes = append(strBytes, keep)
	}

	// const char** is a pointer to the first element of the pointer array.
	arrayPtr := unsafe.Pointer(unsafe.SliceData(strDataPtrs))
	length := uint64(len(instNames))

	ret, err := callInt32(&etCIFDeleteNetworkInst, etFnDeleteNetworkInst,
		unsafe.Pointer(&arrayPtr), unsafe.Pointer(&length))
	runtime.KeepAlive(strBytes)
	runtime.KeepAlive(strDataPtrs)
	if err != nil {
		return fmt.Errorf("delete_network_instance: %w", err)
	}
	if ret != 0 {
		if msg := getFFIError(); msg != "" {
			return fmt.Errorf("delete_network_instance: %s", msg)
		}
		return fmt.Errorf("delete_network_instance: unknown error (ret=%d)", ret)
	}
	return nil
}

// --- Instance Info ---

// cKeyValuePair mirrors the C struct { char *key; char *value; }.
// sizeof = 16 bytes on 64-bit (two 8-byte pointers).
type cKeyValuePair struct {
	key   unsafe.Pointer
	value unsafe.Pointer
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
	infosPtr := unsafe.Pointer(unsafe.SliceData(infos))
	length := uint64(maxCount)

	ret, err := callInt32(&etCIFCollectNetworkInfos, etFnCollectNetworkInfos,
		unsafe.Pointer(&infosPtr), unsafe.Pointer(&length))
	runtime.KeepAlive(infos)
	if err != nil {
		return nil, fmt.Errorf("collect_network_infos: %w", err)
	}
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
		freeString(infos[i].key)
		freeString(infos[i].value)
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
	infosPtr := unsafe.Pointer(unsafe.SliceData(infos))
	length := uint64(maxCount)

	ret, err := callInt32(&etCIFListInstance, etFnListInstance,
		unsafe.Pointer(&infosPtr), unsafe.Pointer(&length))
	runtime.KeepAlive(infos)
	if err != nil {
		return nil, fmt.Errorf("list_instance: %w", err)
	}
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
		freeString(infos[i].key)
		freeString(infos[i].value)
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

	dataPtr, keep := toCString(instName)
	fdVal := int32(fd)

	ret, err := callInt32(&etCIFSetTunFd, etFnSetTunFd,
		unsafe.Pointer(&dataPtr), unsafe.Pointer(&fdVal))
	runtime.KeepAlive(keep)
	if err != nil {
		return fmt.Errorf("set_tun_fd: %w", err)
	}
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

	sDataPtr, sKeep := toCString(serviceName)
	mDataPtr, mKeep := toCString(methodName)
	pDataPtr, pKeep := toCString(payloadJSON)

	// domainName: pass NULL (nil pointer) when empty.
	var dDataPtr unsafe.Pointer
	var dKeep []byte
	if domainName != "" {
		dDataPtr, dKeep = toCString(domainName)
	}

	var outJSON unsafe.Pointer

	ret, err := callInt32(&etCIFCallJSONRPC, etFnCallJSONRPC,
		unsafe.Pointer(&sDataPtr),
		unsafe.Pointer(&mDataPtr),
		unsafe.Pointer(&dDataPtr),
		unsafe.Pointer(&pDataPtr),
		unsafe.Pointer(&outJSON),
	)
	runtime.KeepAlive(sKeep)
	runtime.KeepAlive(mKeep)
	runtime.KeepAlive(pKeep)
	runtime.KeepAlive(dKeep)
	if err != nil {
		return "", fmt.Errorf("call_json_rpc(%s.%s): %w", serviceName, methodName, err)
	}
	if ret != 0 {
		if msg := getFFIError(); msg != "" {
			return "", fmt.Errorf("call_json_rpc(%s.%s): %s", serviceName, methodName, msg)
		}
		return "", fmt.Errorf("call_json_rpc(%s.%s): unknown error (ret=%d)", serviceName, methodName, ret)
	}

	if outJSON == nil {
		return "", fmt.Errorf("call_json_rpc(%s.%s): null response", serviceName, methodName)
	}
	defer freeString(outJSON)

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
