package easytier

import (
	"encoding/json"
	"fmt"
)

type StunResult struct {
	UdpNatType     int      `json:"udp_nat_type"`
	TcpNatType     int      `json:"tcp_nat_type"`
	LastUpdateTime int64    `json:"last_update_time"`
	PublicIP       []string `json:"public_ip"`
	MinPort        int      `json:"min_port"`
	MaxPort        int      `json:"max_port"`
}

type StunService struct{}

func (s *StunService) TestStun() (*StunResult, error) {
	exePath, err := resolveEasyTierBinary("easytier-cli")
	if err != nil {
		return nil, err
	}

	cmd := newCmd(exePath, "-o", "json", "stun")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("easytier-cli stun failed: %w", err)
	}

	var result StunResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse stun result failed: %w", err)
	}

	return &result, nil
}
