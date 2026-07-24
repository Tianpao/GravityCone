package paperconnect

import (
	"fmt"
	"strings"

	"gravitycone/core/utils"
)

func charToValue(c byte) (int, bool) {
	return utils.Value(c)
}

const pcRoomCodeHeader = "P/"
const pcRoomName = "paper-connect"

// PaperConnectRoomCode represents a Bedrock Edition room code.
// Format: P/NNNN-NNNN-SSSS-SSSS
// N-part: 8 random chars for the EasyTier network name.
// S-part: 8 chars adjusted so the little-endian base-34 value is divisible by 7.
type PaperConnectRoomCode struct {
	NetworkPart string // 8 chars
	SecretPart  string // 8 chars
}

// pcConvertToLong converts a char sequence to a little-endian base-34 integer.
// Position 0 is the least significant digit.
func pcConvertToLong(chars []byte) int64 {
	var result int64
	var multiplier int64 = 1
	for i := 0; i < len(chars); i++ {
		v, ok := charToValue(chars[i])
		if !ok {
			return -1
		}
		result += int64(v) * multiplier
		multiplier *= 34
	}
	return result
}

// pcIsDivisibleBySeven checks if the little-endian base-34 value of chars is divisible by 7.
func pcIsDivisibleBySeven(chars []byte) bool {
	return pcConvertToLong(chars)%7 == 0
}

// pcAdjustForDivisibilityBySeven adjusts the S-part chars so the little-endian base-34 value is divisible by 7.
func pcAdjustForDivisibilityBySeven(chars []byte) {
	rem := pcConvertToLong(chars) % 7
	if rem == 0 {
		return
	}

	// Walk from low-order position (index 0) upward, reducing character indices
	for i := 0; i < len(chars) && rem != 0; i++ {
		v, _ := charToValue(chars[i])
		if v >= int(rem) {
			chars[i] = utils.Charset[v-int(rem)]
			rem = 0
		} else {
			chars[i] = utils.Charset[0]
			rem -= int64(v)
		}
	}

	// Fine-adjustment on last character if still not divisible
	if rem != 0 {
		lastIdx := len(chars) - 1
		v, _ := charToValue(chars[lastIdx])
		newV := (v + int(rem)) % 34
		chars[lastIdx] = utils.Charset[newV]
	}

	// Brute-force fallback: increment last character until divisible
	for !pcIsDivisibleBySeven(chars) {
		lastIdx := len(chars) - 1
		v, _ := charToValue(chars[lastIdx])
		v++
		if v >= 34 {
			chars[lastIdx] = utils.Charset[0]
			// Cascade carry
			for j := lastIdx - 1; j >= 0; j-- {
				vj, _ := charToValue(chars[j])
				vj++
				if vj < 34 {
					chars[j] = utils.Charset[vj]
					break
				}
				chars[j] = utils.Charset[0]
			}
		} else {
			chars[lastIdx] = utils.Charset[v]
		}
	}
}

func GeneratePaperConnectRoomCode() (*PaperConnectRoomCode, error) {
	// Generate N-part (8 random chars, no checksum constraint)
	var nPart [8]byte
	for i := range nPart {
		c, err := utils.RandomChar()
		if err != nil {
			return nil, fmt.Errorf("failed to generate random char: %w", err)
		}
		nPart[i] = c
	}

	// Generate S-part (8 random chars, then adjust for divisibility by 7)
	var sPart [8]byte
	for i := range sPart {
		c, err := utils.RandomChar()
		if err != nil {
			return nil, fmt.Errorf("failed to generate random char: %w", err)
		}
		sPart[i] = c
	}
	pcAdjustForDivisibilityBySeven(sPart[:])

	return &PaperConnectRoomCode{
		NetworkPart: string(nPart[:]),
		SecretPart:  string(sPart[:]),
	}, nil
}

func ParsePaperConnectRoomCode(s string) (*PaperConnectRoomCode, error) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, pcRoomCodeHeader)
	s = strings.TrimPrefix(s, "p/")

	clean := strings.ReplaceAll(s, "-", "")
	if len(clean) != 16 {
		return nil, fmt.Errorf("房间代码格式错误：应为16个字符，实际为%d", len(clean))
	}

	clean = strings.ToUpper(clean)
	var chars [16]byte
	for i := 0; i < 16; i++ {
		c := clean[i]
		if _, ok := charToValue(c); !ok {
			return nil, fmt.Errorf("房间代码包含无效字符: %c", c)
		}
		chars[i] = c
	}

	// Validate S-part checksum (little-endian base-34 divisible by 7)
	if !pcIsDivisibleBySeven(chars[8:]) {
		return nil, fmt.Errorf("房间代码校验失败，请检查输入")
	}

	return &PaperConnectRoomCode{
		NetworkPart: string(chars[:8]),
		SecretPart:  string(chars[8:]),
	}, nil
}

func (r *PaperConnectRoomCode) Format() string {
	n := r.NetworkPart
	s := r.SecretPart
	return fmt.Sprintf("%s%s-%s-%s-%s", pcRoomCodeHeader, n[:4], n[4:], s[:4], s[4:])
}

func (r *PaperConnectRoomCode) EasyTierNetworkName() string {
	n := r.NetworkPart
	return fmt.Sprintf("%s-%s-%s", pcRoomName, n[:4], n[4:])
}

func (r *PaperConnectRoomCode) EasyTierNetworkSecret() string {
	s := r.SecretPart
	return fmt.Sprintf("%s-%s", s[:4], s[4:])
}
