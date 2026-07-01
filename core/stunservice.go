package core

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
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
	exePath, err := getEasytierCliPath()
	if err != nil {
		return nil, err
	}

	out, err := exec.Command(exePath, "-o", "json", "stun").Output()
	if err != nil {
		return nil, fmt.Errorf("easytier-cli stun failed: %w", err)
	}

	var result StunResult
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parse stun result failed: %w", err)
	}

	return &result, nil
}

func getEasytierCliPath() (string, error) {
	if runtime.GOOS == "windows" {
		exe, err := exec.LookPath("easytier-cli.exe")
		if err == nil {
			return exe, nil
		}
		return filepath.Join("easytier", "easytier-cli.exe"), nil
	}
	exe, err := exec.LookPath("easytier-cli")
	if err == nil {
		return exe, nil
	}
	return "", fmt.Errorf("easytier-cli not found")
}
