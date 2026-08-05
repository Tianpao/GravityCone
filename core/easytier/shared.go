package easytier

// 本文件存放桌面（!et_ffi）与 Android FFI（et_ffi）两个构建变体共用的
// 纯类型与常量。不要在此引入任何构建标签，也不要 import 平台相关代码。

// AppVersion 是 GravityCone 自身版本号（带 v 前缀），用于 uptime User-Agent、
// 房间广播 vendor 字符串与 FFI 元数据。发版时只需改这一处。
const AppVersion = "v0.1.4-alpha"

// EasyTierVersion 是 EasyTier 二进制/FFI 库的版本号，用于下载 URL 与房间广播。
const EasyTierVersion = "v2.6.4"

const (
	EventDownloadProgress = "download.progress"
	EventDownloadError    = "download.error"
)

type DownloadProgressData struct {
	Step      string `json:"step"`
	Percent   int    `json:"percent"`
	TotalSize int64  `json:"total_size"`
	Speed     int64  `json:"speed"`
}

type DownloadErrorData struct {
	Error string `json:"error"`
}

// StartOptions 是启动 EasyTier 网络的统一参数。
// MachineID 与 TunFdProvider 仅 Android FFI 使用，桌面端忽略。
type StartOptions struct {
	NetworkName        string
	NetworkSecret      string
	Hostname           string // HOST only; GUEST leaves empty
	IsHost             bool
	TCPPort            uint16   // HOST only: scaffolding TCP port, used for whitelist
	MCPort             uint16   // HOST only: MC server port, used for whitelist
	ConfigPath         string   // Path to TOML ACL config file (adds -c flag)
	PortForwards       []string // Port forward entries (e.g. "tcp://0.0.0.0:12345/10.144.144.1:12345")
	Peers              []string // Public peer addresses passed as -p arguments.
	UpstreamCompatible bool     // Use the original PaperConnect EasyTier argument profile.
	DisableP2P         bool     // Force relay-only mode (--disable-p2p true). Applies to both profiles.
	MachineID          string   // Optional machine identifier (Android TOML config).
	TunFdProvider      func(instName string, virtualIP string, cidr string) (int, error) // optional TUN fd injection (Android)
}

// StunResult 是 NAT 探测结果，桌面由 easytier-cli stun 输出，Android 由
// FFI 的 collect_network_infos 提供（proto NatType 整数，编号与桌面一致）。
type StunResult struct {
	UdpNatType     int      `json:"udp_nat_type"`
	TcpNatType     int      `json:"tcp_nat_type"`
	LastUpdateTime int64    `json:"last_update_time"`
	PublicIP       []string `json:"public_ip"`
	MinPort        int      `json:"min_port"`
	MaxPort        int      `json:"max_port"`
}
