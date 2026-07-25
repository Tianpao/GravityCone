package scaffolding

import (
	"fmt"
	"strings"

	"gravitycone/core/utils"
)

type RoomCode struct {
	NetworkPart string // 8 chars: NNNN-NNNN (without dash)
	SecretPart  string // 8 chars: SSSS-SSSS (without dash)
}

// isValidChecksum checks that the weighted sum of char values (position i
// contributes value*6^(i%2)) is divisible by 7.
func isValidChecksum(chars [16]byte) bool {
	sum := 0
	for i := 0; i < 16; i++ {
		v, ok := utils.Value(chars[i])
		if !ok {
			return false
		}
		if i%2 == 1 {
			v *= 6
		}
		sum += v
	}
	return sum%7 == 0
}

// GenerateRoomCode generates a valid room code deterministically:
// the first 15 characters are random, and the 16th is computed so
// the checksum passes, eliminating rejection sampling.
func GenerateRoomCode() (*RoomCode, error) {
	var chars [16]byte
	for i := 0; i < 15; i++ {
		c, err := utils.RandomChar()
		if err != nil {
			return nil, fmt.Errorf("failed to generate random char: %w", err)
		}
		chars[i] = c
	}

	// Compute partial checksum for positions 0..14, then find chars[15].
	sum := 0
	for i := 0; i < 15; i++ {
		v, _ := utils.Value(chars[i])
		if i%2 == 1 {
			v *= 6
		}
		sum += v
	}
	// Position 15 is odd, so its weight is 6. We need (sum + v*6) % 7 == 0.
	// v*6 mod 7 == (-v) mod 7, so we need v ≡ sum (mod 7).
	rem := sum % 7
	for ci := range utils.Charset {
		if ci%7 == rem {
			chars[15] = utils.Charset[ci]
			break
		}
	}

	return &RoomCode{
		NetworkPart: string(chars[:8]),
		SecretPart:  string(chars[8:]),
	}, nil
}

func ParseRoomCode(s string) (*RoomCode, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "U/")
	s = strings.TrimPrefix(s, "u/")

	clean := strings.ReplaceAll(s, "-", "")
	if len(clean) != 16 {
		return nil, fmt.Errorf("房间代码格式错误：应为16个字符，实际为%d", len(clean))
	}

	clean = strings.ToUpper(clean)
	var chars [16]byte
	copy(chars[:], clean)

	if !isValidChecksum(chars) {
		// Find the first invalid char for a better error message.
		for i := 0; i < 16; i++ {
			if _, ok := utils.Value(chars[i]); !ok {
				return nil, fmt.Errorf("房间代码包含无效字符: %c", chars[i])
			}
		}
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
