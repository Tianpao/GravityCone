package scaffolding

import (
	"fmt"
	"strings"

	"gravitycone/core/utils"
)

// pow34Mod7[i] = 34^i mod 7. Since 34 mod 7 = 6, the pattern is 1,6,1,6,...
var pow34Mod7 = [16]int{1, 6, 1, 6, 1, 6, 1, 6, 1, 6, 1, 6, 1, 6, 1, 6}

type RoomCode struct {
	NetworkPart string // 8 chars: NNNN-NNNN (without dash)
	SecretPart  string // 8 chars: SSSS-SSSS (without dash)
}

func charToValue(c byte) int {
	value, ok := utils.Value(c)
	if !ok {
		return -1
	}
	return value
}

func isValidChecksum(chars [16]byte) bool {
	sum := 0
	for i := 0; i < 16; i++ {
		v := charToValue(chars[i])
		if v < 0 {
			return false
		}
		sum += v * pow34Mod7[i]
	}
	return sum%7 == 0
}

func GenerateRoomCode() (*RoomCode, error) {
	for {
		var chars [16]byte
		for i := range chars {
			c, err := utils.RandomChar()
			if err != nil {
				return nil, fmt.Errorf("failed to generate random char: %w", err)
			}
			chars[i] = c
		}
		if isValidChecksum(chars) {
			return &RoomCode{
				NetworkPart: string(chars[:8]),
				SecretPart:  string(chars[8:]),
			}, nil
		}
	}
}

func ParseRoomCode(s string) (*RoomCode, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "U/")
	s = strings.TrimPrefix(s, "u/")

	// Remove all dashes for validation
	clean := strings.ReplaceAll(s, "-", "")
	if len(clean) != 16 {
		return nil, fmt.Errorf("房间代码格式错误：应为16个字符，实际为%d", len(clean))
	}

	clean = strings.ToUpper(clean)
	var chars [16]byte
	for i := 0; i < 16; i++ {
		c := clean[i]
		if charToValue(c) < 0 {
			return nil, fmt.Errorf("房间代码包含无效字符: %c", c)
		}
		chars[i] = c
	}

	if !isValidChecksum(chars) {
		return nil, fmt.Errorf("房间代码校验失败，请检查输入")
	}

	return &RoomCode{
		NetworkPart: string(chars[:8]),
		SecretPart:  string(chars[8:]),
	}, nil
}

func (r *RoomCode) Format() string {
	n := r.NetworkPart
	s := r.SecretPart
	return fmt.Sprintf("U/%s-%s-%s-%s", n[:4], n[4:], s[:4], s[4:])
}

func (r *RoomCode) EasyTierNetworkName() string {
	n := r.NetworkPart
	return fmt.Sprintf("scaffolding-mc-%s-%s", n[:4], n[4:])
}

func (r *RoomCode) EasyTierNetworkSecret() string {
	s := r.SecretPart
	return fmt.Sprintf("%s-%s", s[:4], s[4:])
}
