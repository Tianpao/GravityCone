//go:build !et_ffi

package easytier

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"gravitycone/core/utils/process"
)

type peerInfo struct {
	PeerID    json.RawMessage `json:"id"`
	VirtualIP string          `json:"ipv4"`
	Hostname  string          `json:"hostname"`
}

func (m *EasyTierManager) listPeers() ([]peerInfo, error) {
	out, err := m.runCli("-o", "json", "-p", m.RPCPortal(), "peer", "list")
	if err != nil {
		return nil, fmt.Errorf("查询对等节点失败: %w", err)
	}

	var peers []peerInfo
	if err := json.Unmarshal([]byte(out), &peers); err != nil {
		return nil, fmt.Errorf("解析对等节点列表失败: %w", err)
	}
	return peers, nil
}

func (m *EasyTierManager) DiscoverPeer(hostname string) (string, error) {
	peers, err := m.listPeers()
	if err != nil {
		return "", err
	}

	for _, p := range peers {
		if p.Hostname == hostname && p.VirtualIP != "" {
			return stripCIDR(p.VirtualIP), nil
		}
	}

	return "", fmt.Errorf("未找到主机 (%s)，请确认房间代码正确", hostname)
}

// DiscoverPeerByPrefix finds a peer whose hostname starts with the given prefix.
// Returns the matching hostname and virtual IP.
func (m *EasyTierManager) DiscoverPeerByPrefix(hostnamePrefix string) (hostname string, virtualIP string, err error) {
	peers, err := m.listPeers()
	if err != nil {
		return "", "", err
	}

	for _, p := range peers {
		if strings.HasPrefix(p.Hostname, hostnamePrefix) && p.VirtualIP != "" {
			return p.Hostname, stripCIDR(p.VirtualIP), nil
		}
	}

	return "", "", fmt.Errorf("未找到主机 (前缀 %s)，请确认房间代码正确", hostnamePrefix)
}

func (m *EasyTierManager) GetPeerID() (string, error) {
	out, err := m.runCli("-o", "json", "-p", m.RPCPortal(), "node", "info")
	if err != nil {
		return "", err
	}

	var info struct {
		PeerID json.RawMessage `json:"peer_id"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		return "", err
	}
	return string(info.PeerID), nil
}

func (m *EasyTierManager) FindPeerByHostnamePrefix(hostnamePrefix string) (string, uint16, error) {
	peers, err := m.listPeers()
	if err != nil {
		return "", 0, err
	}

	for _, p := range peers {
		if !strings.HasPrefix(p.Hostname, hostnamePrefix) || p.VirtualIP == "" {
			continue
		}
		portStr := p.Hostname[len(hostnamePrefix):]
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil || port <= 1024 || port > 65535 {
			continue
		}
		return stripCIDR(p.VirtualIP), uint16(port), nil
	}

	return "", 0, fmt.Errorf("未找到联机中心，请确认房间代码正确且房主已开启房间")
}

func (m *EasyTierManager) runCli(args ...string) (string, error) {
	cmd := process.NewHiddenCmd(m.cliPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("easytier-cli failed", "path", m.cliPath, "args", args, "error", err, "output", string(out))
		return "", err
	}
	return string(out), nil
}
