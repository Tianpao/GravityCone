package easytier

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// 中继节点 ID 的保留编码值（nodeID 以两位 base-34 字符编码进房间码）：
//   - 00: 值 0，房主使用自用中继节点（不追加 uptime 公共节点）
//   - PP: 值 23+34*23=805，不使用公共节点（纯 P2P，不追加 uptime 节点）
const (
	NodeIDReservedSelfRelay = 0
	NodeIDReservedNoPublic  = 23 + 34*23 // "PP"
	NodeIDMax               = 33 + 34*33 // 1155
)

// NodeIDChars 把节点 ID 拆为两个 base-34 字符值（低位、高位），用于编码进房间码。
func NodeIDChars(nodeID int) (lo, hi int) {
	return nodeID % 34, (nodeID / 34) % 34
}

// NodeIDFromChars 组合两个 base-34 字符值还原节点 ID。
func NodeIDFromChars(lo, hi int) int {
	return lo + 34*hi
}

// RelayManager 集中管理中继节点选择，两种协议服务共享同一份实现：
// 启动器指定（CLI/FFI 的 ConfigureExternalRelay）或 Uptime 自动分发（GUI 的 EnableUptime）。
type RelayManager struct {
	client   *UptimeClient
	settings *SettingsService

	mu     sync.RWMutex
	extID  int
	extURL string

	uptimeOn bool
}

// NewRelayManager 创建中继管理器（默认未启用 uptime、未设置外部中继）。
func NewRelayManager() *RelayManager {
	return &RelayManager{client: NewUptimeClient()}
}

// SetSettingsService 注入 GUI 设置（仅 GUI 使用；CLI/FFI 不注入，P2P 开关恒为 false）。
func (r *RelayManager) SetSettingsService(s *SettingsService) {
	r.settings = s
}

// EnableUptime 启用 Uptime 节点自动分发。仅 GUI 调用；CLI/FFI 不启用，
// 中继由启动器传入，不传时使用内置节点，绝不拉取 uptime。
func (r *RelayManager) EnableUptime() {
	r.uptimeOn = true
}

// SetExternal 设置启动器指定的中继节点（CLI/FFI 模式）：nodeID 编码进房间码
// （房主端），url 直接作为 EasyTier peer（两端）。url 为空或 nodeID 为负时
// 清除覆盖，恢复自动分发。
func (r *RelayManager) SetExternal(nodeID int, url string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if url == "" || nodeID < 0 {
		r.extID = 0
		r.extURL = ""
		return
	}
	r.extID = nodeID
	r.extURL = url
}

// external 返回启动器指定的中继（nodeID, url）及是否已设置（url 非空即已设置）。
func (r *RelayManager) external() (nodeID int, url string, ok bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.extID, r.extURL, r.extURL != ""
}

// P2PDisabled 返回是否禁止 P2P 直连（强制走中继）。仅 GUI 设置（CLI 不注入
// SettingsService，恒为 false）。
func (r *RelayManager) P2PDisabled() bool {
	if r.settings != nil {
		return r.settings.GetP2PDisabled()
	}
	return false
}

// HostPeersAndNodeID 组装房主的 peers 并返回要编码进房间码的中继节点 ID。
// 启动器指定了中继（CLI/FFI）时直接用其 nodeID 与地址；否则未启用 uptime
// （CLI/FFI）时房间标记为"不使用公共节点"，仅用内置节点；仅 GUI 拉取
// uptime 节点（P2P 发现节点同时作为发现兜底），拉取失败同样降级内置节点。
func (r *RelayManager) HostPeersAndNodeID(base []string) ([]string, int) {
	peers := base

	if nodeID, url, ok := r.external(); ok {
		slog.Info("使用启动器指定的中继节点", "nodeID", nodeID, "url", url)
		return append(peers, url), nodeID
	}

	if !r.uptimeOn {
		return peers, NodeIDReservedNoPublic
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nodes, err := r.client.FetchNodes(ctx)
	if err != nil {
		slog.Warn("拉取 Uptime 节点失败，房间标记为不使用公共节点", "err", err)
		return peers, NodeIDReservedNoPublic
	}

	relayID := NodeIDReservedNoPublic
	urls := make([]string, 0, len(nodes))
	for _, n := range nodes {
		urls = append(urls, n.URL)
		// 跳过保留 ID：把哨兵值编进房间码会让房客走错分支
		if n.IsRelay && relayID == NodeIDReservedNoPublic &&
			n.ID != NodeIDReservedSelfRelay && n.ID != NodeIDReservedNoPublic {
			relayID = n.ID
		}
	}
	slog.Info("已从 Uptime 拉取节点", "count", len(urls), "relayNodeID", relayID)
	return append(peers, urls...), relayID
}

// GuestPeers 组装房客的 peers。启动器指定了中继时，节点地址已由其
// 获取处理完毕，直接使用传入地址（不按房间码 nodeID 定向获取）；
// 未启用 uptime（CLI/FFI）时仅用内置节点；GUI 按房间码内嵌 nodeID 分支：
//   - 保留 ID 00（自用中继）：不追加 uptime 公共中继，仅保留列表 P2P 发现节点
//   - 保留 ID PP（不使用公共节点）：纯 P2P，仅内置节点
//   - 其他：从列表定向换取房主的中继；列表无此 ID（旧房间码随机值/节点失效）
//     则降级为完整列表节点
func (r *RelayManager) GuestPeers(base []string, nodeID int) []string {
	peers := base

	if _, url, ok := r.external(); ok {
		slog.Info("使用启动器指定的中继节点", "url", url)
		return append(peers, url)
	}

	if !r.uptimeOn {
		return peers
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	switch {
	case nodeID == NodeIDReservedNoPublic:
		return peers

	case nodeID == NodeIDReservedSelfRelay:
		_, nodes, err := r.client.FetchNodesForJoin(ctx, 0)
		if err != nil {
			slog.Warn("拉取 Uptime 节点失败", "err", err)
			return peers
		}
		return append(peers, nodeURLs(nodes)...)

	default:
		targetURL, nodes, err := r.client.FetchNodesForJoin(ctx, nodeID)
		if err != nil {
			slog.Warn("拉取 Uptime 节点失败", "err", err)
			return peers
		}
		urls := nodeURLs(nodes)
		if targetURL == "" {
			slog.Warn("按房间码获取中继节点失败，降级为列表节点", "nodeID", nodeID)
			return append(peers, urls...)
		}
		slog.Info("已按房间码定向获取中继节点", "nodeID", nodeID, "url", targetURL)
		return append(append(peers, targetURL), urls...)
	}
}

// nodeURLs 提取节点结果中的连接地址。
func nodeURLs(nodes []UptimeNodeResult) []string {
	urls := make([]string, 0, len(nodes))
	for _, n := range nodes {
		urls = append(urls, n.URL)
	}
	return urls
}
