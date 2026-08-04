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

// FetchNodes 拉取节点列表并换取连接地址，返回结构化结果。
// 单个节点换取失败会被跳过；所有节点都失败时返回错误。
func (c *UptimeClient) FetchNodes(ctx context.Context) ([]UptimeNodeResult, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("未配置环境变量 %s", UptimeAPIKeyEnv)
	}

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

// fetchNodeList 请求 GET /api/node 获取 relay 与 P2P 节点列表。
func (c *UptimeClient) fetchNodeList(ctx context.Context) (relay, p2p []uptimeNode, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/node?relay=true&p2pnode=3", nil)
	if err != nil {
		return nil, nil, err
	}
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("请求 Uptime 节点列表失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("Uptime 节点列表返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Status  int  `json:"status"`
		Success bool `json:"success"`
		Data    struct {
			Relay []uptimeNode `json:"relay"`
			P2P   []uptimeNode `json:"p2p"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil, fmt.Errorf("解析 Uptime 节点列表失败: %w", err)
	}
	return parsed.Data.Relay, parsed.Data.P2P, nil
}

// FetchNodeByID 按节点 ID 定向获取节点连接地址（GET /api/node/connect/:nodeId），
// 用于房客跟随房主编码在房间码里的中继节点。节点不存在或不可用时返回错误，
// 调用方据此降级。
func (c *UptimeClient) FetchNodeByID(ctx context.Context, nodeID int) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("未配置环境变量 %s", UptimeAPIKeyEnv)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/node/connect/%d", c.baseURL, nodeID), nil)
	if err != nil {
		return "", err
	}
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 Uptime 节点(%d)失败: %w", nodeID, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Uptime 节点(%d)返回 %d: %s", nodeID, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Status  int  `json:"status"`
		Success bool `json:"success"`
		Data    struct {
			GetKey string `json:"getKey"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("解析 Uptime 节点(%d)响应失败: %w", nodeID, err)
	}
	if parsed.Data.GetKey == "" {
		return "", fmt.Errorf("Uptime 节点(%d)未返回 getKey", nodeID)
	}

	// getKey 有效期仅数秒，立即换取连接地址。
	return c.fetchNodeURL(ctx, parsed.Data.GetKey)
}

// fetchNodeURL 请求 GET /api/node/get/:getKey 换取节点连接地址。
// 返回形式为 "txt://<connectUrl>"：若 connectUrl 本身就是 EasyTier 连接
// 地址（tcp/udp/ws/wss），剥掉前缀直接使用；否则原样保留交由 EasyTier 解析。
func (c *UptimeClient) fetchNodeURL(ctx context.Context, getKey string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/node/get/"+getKey, nil)
	if err != nil {
		return "", err
	}
	c.setAuthHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 Uptime 节点地址失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Uptime 节点地址返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	raw := strings.TrimSpace(string(body))
	url := strings.TrimPrefix(raw, "txt://")
	if strings.HasPrefix(url, "tcp://") || strings.HasPrefix(url, "udp://") ||
		strings.HasPrefix(url, "ws://") || strings.HasPrefix(url, "wss://") {
		return url, nil
	}
	return raw, nil
}

func (c *UptimeClient) setAuthHeaders(req *http.Request) {
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("x-api-key", c.apiKey)
}
