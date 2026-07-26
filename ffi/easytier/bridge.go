//go:build et_ffi

// Package easytier provides CGo bindings to libeasytier_ffi (EasyTier's C ABI).
//
// On desktop platforms, GravityCone launches easytier-core as a subprocess.
// On Android, subprocesses are unreliable — instead we statically link
// libeasytier_ffi and run EasyTier in-process via its C API.
//
// Build: CGO_ENABLED=1 go build -tags cgo -ldflags "-L/path/to/easytier-ffi -leasytier_ffi"
package easytier

/*
#cgo LDFLAGS: -leasytier_ffi
#cgo linux,arm64 LDFLAGS: -L${SRCDIR}/../../build/android/app/src/main/jniLibs/arm64-v8a
#cgo linux,amd64 LDFLAGS: -L${SRCDIR}/../../build/android/app/src/main/jniLibs/x86_64

#include <stdlib.h>

// EasyTier FFI C API declarations.
// Sourced from: https://github.com/EasyTier/EasyTier/blob/main/easytier-contrib/easytier-ffi/src/lib.rs
//
// Network management:
int parse_config(const char *cfg_str);
int run_network_instance(const char *cfg_str);
int retain_network_instance(const char **inst_names, unsigned long length);
int delete_network_instance(const char **inst_names, unsigned long length);
int list_instance(void *infos, unsigned long max_length);
int collect_network_infos(void *infos, unsigned long max_length);
int set_tun_fd(const char *inst_name, int fd);

// JSON RPC bridge:
int call_json_rpc(const char *service_name, const char *method_name,
                  const char *domain_name, const char *payload_json,
                  char **out_response_json);

// Error handling:
void get_error_msg(const char **out);
void free_string(const char *s);

// Types used by collect_network_infos / list_instance:
typedef struct {
	char *key;
	char *value;
} KeyValuePair;
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// --- Error handling ---

// getFFIError returns the last FFI error message, or empty string if none.
func getFFIError() string {
	var out *C.char
	C.get_error_msg(&out)
	if out == nil {
		return ""
	}
	defer C.free_string(out)
	return C.GoString(out)
}

// --- Config ---

// ParseConfig validates a TOML config string without starting an instance.
func ParseConfig(tomlCfg string) error {
	cCfg := C.CString(tomlCfg)
	defer C.free(unsafe.Pointer(cCfg))

	ret := C.parse_config(cCfg)
	if ret != 0 {
		errMsg := getFFIError()
		if errMsg != "" {
			return fmt.Errorf("parse_config: %s", errMsg)
		}
		return fmt.Errorf("parse_config: unknown error (ret=%d)", int(ret))
	}
	return nil
}

// RunNetworkInstance starts an EasyTier network instance from a TOML config.
func RunNetworkInstance(tomlCfg string) error {
	cCfg := C.CString(tomlCfg)
	defer C.free(unsafe.Pointer(cCfg))

	ret := C.run_network_instance(cCfg)
	if ret != 0 {
		errMsg := getFFIError()
		if errMsg != "" {
			return fmt.Errorf("run_network_instance: %s", errMsg)
		}
		return fmt.Errorf("run_network_instance: unknown error (ret=%d)", int(ret))
	}
	return nil
}

// DeleteNetworkInstance stops named instances.
func DeleteNetworkInstance(instNames []string) error {
	if len(instNames) == 0 {
		return nil
	}

	// Build null-terminated C string array
	cStrings := make([]*C.char, len(instNames))
	for i, name := range instNames {
		cStrings[i] = C.CString(name)
	}
	defer func() {
		for _, s := range cStrings {
			C.free(unsafe.Pointer(s))
		}
	}()

	ret := C.delete_network_instance(&cStrings[0], C.ulong(len(instNames)))
	if ret != 0 {
		errMsg := getFFIError()
		if errMsg != "" {
			return fmt.Errorf("delete_network_instance: %s", errMsg)
		}
		return fmt.Errorf("delete_network_instance: unknown error (ret=%d)", int(ret))
	}
	return nil
}

// --- Instance Info ---

// InstanceInfo holds a key-value pair returned by collect_network_infos.
type InstanceInfo struct {
	Name string
	Info string // JSON string with instance details
}

// CollectNetworkInfos returns information about running instances.
func CollectNetworkInfos(maxCount int) ([]InstanceInfo, error) {
	if maxCount <= 0 {
		maxCount = 32
	}

	infos := make([]C.KeyValuePair, maxCount)
	ret := C.collect_network_infos(unsafe.Pointer(&infos[0]), C.ulong(maxCount))
	if ret < 0 {
		errMsg := getFFIError()
		if errMsg != "" {
			return nil, fmt.Errorf("collect_network_infos: %s", errMsg)
		}
		return nil, fmt.Errorf("collect_network_infos: unknown error (ret=%d)", int(ret))
	}

	result := make([]InstanceInfo, 0, int(ret))
	for i := 0; i < int(ret); i++ {
		info := InstanceInfo{
			Name: C.GoString(infos[i].key),
			Info: C.GoString(infos[i].value),
		}
		C.free_string(infos[i].key)
		C.free_string(infos[i].value)
		result = append(result, info)
	}
	return result, nil
}

// ListInstances returns running instance names and IDs.
func ListInstances(maxCount int) ([]InstanceInfo, error) {
	if maxCount <= 0 {
		maxCount = 32
	}

	infos := make([]C.KeyValuePair, maxCount)
	ret := C.list_instance(unsafe.Pointer(&infos[0]), C.ulong(maxCount))
	if ret < 0 {
		errMsg := getFFIError()
		if errMsg != "" {
			return nil, fmt.Errorf("list_instance: %s", errMsg)
		}
		return nil, fmt.Errorf("list_instance: unknown error (ret=%d)", int(ret))
	}

	result := make([]InstanceInfo, 0, int(ret))
	for i := 0; i < int(ret); i++ {
		info := InstanceInfo{
			Name: C.GoString(infos[i].key),
			Info: C.GoString(infos[i].value),
		}
		C.free_string(infos[i].key)
		C.free_string(infos[i].value)
		result = append(result, info)
	}
	return result, nil
}

// --- TUN fd ---

// SetTunFd attaches a TUN file descriptor to a named instance.
// This is how Android VpnService fd gets injected into EasyTier.
func SetTunFd(instName string, fd int) error {
	cName := C.CString(instName)
	defer C.free(unsafe.Pointer(cName))

	ret := C.set_tun_fd(cName, C.int(fd))
	if ret != 0 {
		errMsg := getFFIError()
		if errMsg != "" {
			return fmt.Errorf("set_tun_fd: %s", errMsg)
		}
		return fmt.Errorf("set_tun_fd: unknown error (ret=%d)", int(ret))
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
	cService := C.CString(serviceName)
	defer C.free(unsafe.Pointer(cService))
	cMethod := C.CString(methodName)
	defer C.free(unsafe.Pointer(cMethod))
	cPayload := C.CString(payloadJSON)
	defer C.free(unsafe.Pointer(cPayload))

	var cDomain *C.char
	if domainName != "" {
		cDomain = C.CString(domainName)
		defer C.free(unsafe.Pointer(cDomain))
	}

	var outJSON *C.char
	ret := C.call_json_rpc(cService, cMethod, cDomain, cPayload, &outJSON)
	if ret != 0 {
		errMsg := getFFIError()
		if errMsg != "" {
			return "", fmt.Errorf("call_json_rpc(%s.%s): %s", serviceName, methodName, errMsg)
		}
		return "", fmt.Errorf("call_json_rpc(%s.%s): unknown error (ret=%d)", serviceName, methodName, int(ret))
	}

	if outJSON == nil {
		return "", fmt.Errorf("call_json_rpc(%s.%s): null response", serviceName, methodName)
	}
	defer C.free_string(outJSON)

	return C.GoString(outJSON), nil
}

// --- Node info helpers (commonly used RPCs) ---

// NodeInfoResponse is the JSON response from show_node_info RPC.
type NodeInfoResponse struct {
	NodeInfo *struct {
		VirtualIP    string `json:"ipv4_addr"`
		PeerID       string `json:"peer_id"`
		Hostname     string `json:"hostname"`
	} `json:"node_info,omitempty"`
	Error string `json:"error,omitempty"`
}

// PeerRouteEntry is a single route entry from list_route RPC.
type PeerRouteEntry struct {
	Hostname    string `json:"hostname"`
	IPv4Addr    string `json:"ipv4_addr"`
	PeerID      string `json:"peer_id"`
	ProxyCIDRs  []string `json:"proxy_cidrs"`
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
	Action int             `json:"action"` // 1=Add, 2=Remove
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
