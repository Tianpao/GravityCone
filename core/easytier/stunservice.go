//go:build !et_ffi

package easytier

import (
	"encoding/json"
	"fmt"

	"gravitycone/core/utils/process"
)

type StunService struct{}

func (s *StunService) TestStun() (*StunResult, error) {
	exePath, err := resolveEasyTierBinary("easytier-cli")
	if err != nil {
		return nil, err
	}

	cmd := process.NewHiddenCmd(exePath, "-o", "json", "stun")
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
