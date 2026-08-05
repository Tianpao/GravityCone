package easytier

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Uptime 节点分发服务配置。
// API Key 通过环境变量 UPTIME_API_KEY 提供，请求需携带固定 User-Agent 供服务端校验绑定。
const (
	UptimeBaseURL   = "https://uptime.1tmc.top"
	UptimeUserAgent = "GravityCone/v0.1.4-alpha"
	UptimeAPIKeyEnv = "UPTIME_API_KEY"
)

// uptimeNode 是 /api/node 响应中的单个节点。
type uptimeNode struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	GetKey string `json:"getKey"`
}

// UptimeClient 从 EasyTierMC 节点分发服务拉取公共 relay/P2P 节点，
// 解析为 EasyTier -p 参数（中继节点提供中继兜底，P2P 节点提供发现/打洞）。
type UptimeClient struct {
	baseURL   string
	apiKey    string
	userAgent string
	http      *http.Client
}

// NewUptimeClient 创建客户端，API Key 从环境变量 UPTIME_API_KEY 读取；
// 未配置时拉取会失败并降级为内置节点。
func NewUptimeClient() *UptimeClient {
	return &UptimeClient{
		baseURL:   UptimeBaseURL,
		apiKey:    os.Getenv(UptimeAPIKeyEnv),
		userAgent: UptimeUserAgent,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

// UptimeNodeResult 是拉取到并换取地址成功的节点。
type UptimeNodeResult struct {
	ID      int    // uptime 节点 ID（房主可编码进房间码）
	Name    string // 节点名称
	IsRelay bool   // 中继节点；false 为 P2P 发现节点
	URL     string // EasyTier -p 地址
}

// FetchNodes 拉取节点列表并换取全部连接地址，返回结构化结果。
// 单个节点换取失败会被跳过；所有节点都失败时返回错误。
func (c *UptimeClient) FetchNodes(ctx context.Context) ([]UptimeNodeResult, error) {
	relay, p2p, err := c.fetchNodeList(ctx)
	if err != nil {
		return nil, err
	}
	all := make([]uptimeNode, 0, len(relay)+len(p2p))
	all = append(all, relay...)
	all = append(all, p2p...)
	if len(all) == 0 {
		return nil, fmt.Errorf("Uptime 服务器未返回可用节点")
	}

	// 并行换取各节点连接地址；getKey 有效期仅数秒，须立即使用。
	results := make([]UptimeNodeResult, len(all))
	var wg sync.WaitGroup
	for i, n := range all {
		wg.Add(1)
		go func(idx int, node uptimeNode, isRelay bool) {
			defer wg.Done()
			url, err := c.fetchNodeURL(ctx, node.GetKey)
			if err != nil {
				slog.Warn("换取 Uptime 节点地址失败，跳过", "name", node.Name, "id", node.ID, "err", err)
				return
			}
			results[idx] = UptimeNodeResult{ID: node.ID, Name: node.Name, IsRelay: isRelay, URL: url}
		}(i, n, i < len(relay))
	}
	wg.Wait()

	filtered := results[:0]
	for _, r := range results {
		if r.URL != "" {
			filtered = append(filtered, r)
		}
	}
	if len(filtered) == 0 {
		return nil, fmt.Errorf("所有 Uptime 节点地址换取失败")
	}
	return filtered, nil
}

// FetchNodesForJoin 一次列表拉取并换取房客所需节点地址。getKey 有效期仅数秒，
// 列表与换取必须紧邻，故不分两次请求：
//   - targetID > 0 且列表中存在该节点：targetURL 返回其地址，nodes 为 P2P 发现节点
//   - targetID > 0 且列表中无该节点（旧房间码随机值/节点失效）：targetURL 为空，
//     nodes 为全部节点（降级兜底）
//   - targetID == 0（自用中继）：targetURL 为空，nodes 为 P2P 发现节点，不换取中继地址
func (c *UptimeClient) FetchNodesForJoin(ctx context.Context, targetID int) (targetURL string, nodes []UptimeNodeResult, err error) {
	relay, p2p, err := c.fetchNodeList(ctx)
	if err != nil {
		return "", nil, err
	}
	all := make([]uptimeNode, 0, len(relay)+len(p2p))
	all = append(all, relay...)
	all = append(all, p2p...)
	if len(all) == 0 {
		return "", nil, fmt.Errorf("Uptime 服务器未返回可用节点")
	}

	// 定向目标存在与否决定换取范围；目标缺失时降级为全部节点
	exchangeAll := false
	var target *uptimeNode
	if targetID > 0 {
		exchangeAll = true
		for i := range all {
			if all[i].ID == targetID {
				target = &all[i]
				exchangeAll = false
				break
			}
		}
	}

	results := make([]UptimeNodeResult, len(all))
	var wg sync.WaitGroup
	for i := range all {
		n := all[i]
		isRelay := i < len(relay)
		if isRelay && !exchangeAll && (target == nil || n.ID != target.ID) {
			continue // 非降级：中继只换取定向目标一个
		}
		wg.Add(1)
		go func(idx int, node uptimeNode, isRelay bool) {
			defer wg.Done()
			url, err := c.fetchNodeURL(ctx, node.GetKey)
			if err != nil {
				slog.Warn("换取 Uptime 节点地址失败，跳过", "name", node.Name, "id", node.ID, "err", err)
				return
			}
			results[idx] = UptimeNodeResult{ID: node.ID, Name: node.Name, IsRelay: isRelay, URL: url}
		}(i, n, isRelay)
	}
	wg.Wait()

	// 过滤换取失败的节点；定向目标从 nodes 中分离，避免重复
	targetURL = ""
	filtered := results[:0]
	for _, r := range results {
		if r.URL == "" {
			continue
		}
		if target != nil && r.ID == target.ID {
			targetURL = r.URL
			continue
		}
		filtered = append(filtered, r)
	}
	return targetURL, filtered, nil
}

// fetchNodeList 请求 GET /api/node 获取 relay 与 P2P 节点列表。
func (c *UptimeClient) fetchNodeList(ctx context.Context) (relay, p2p []uptimeNode, err error) {
	body, err := c.getBody(ctx, "/api/node?relay=true&p2pnode=3", "节点列表")
	if err != nil {
		return nil, nil, err
	}

	var parsed struct {
		Data struct {
			Relay []uptimeNode `json:"relay"`
			P2P   []uptimeNode `json:"p2p"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil, fmt.Errorf("解析 Uptime 节点列表失败: %w", err)
	}
	return parsed.Data.Relay, parsed.Data.P2P, nil
}

// fetchNodeURL 请求 GET /api/node/get/:getKey 换取节点连接地址。
// 返回形式为 "txt://<connectUrl>"：若 connectUrl 本身就是 EasyTier 连接
// 地址（tcp/udp/ws/wss），剥掉前缀直接使用；否则原样保留交由 EasyTier 解析。
func (c *UptimeClient) fetchNodeURL(ctx context.Context, getKey string) (string, error) {
	body, err := c.getBody(ctx, "/api/node/get/"+getKey, "节点地址")
	if err != nil {
		return "", err
	}

	raw := strings.TrimSpace(string(body))
	url := strings.TrimPrefix(raw, "txt://")
	if strings.HasPrefix(url, "tcp://") || strings.HasPrefix(url, "udp://") ||
		strings.HasPrefix(url, "ws://") || strings.HasPrefix(url, "wss://") {
		return url, nil
	}
	return raw, nil
}

// getBody 发起带鉴权的 GET 请求并返回响应体（限制 1MB），desc 用于错误文案。
func (c *UptimeClient) getBody(ctx context.Context, path, desc string) ([]byte, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("未配置环境变量 %s", UptimeAPIKeyEnv)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 Uptime %s失败: %w", desc, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Uptime %s返回 %d: %s", desc, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func (c *UptimeClient) setAuthHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("x-api-key", c.apiKey)
}
