package scaffolding

import (
	"fmt"
	"strings"

	"gravitycone/core/utils"
)

// uptime 节点 ID 的保留编码值（nodeID 编码占 N 部分最后一位 + S 部分最后一位）：
//   - 00: 值 0，房主使用自用中继节点（不追加 uptime 公共节点）
//   - PP: 值 23+34*23=805，不使用公共节点（纯 P2P，不追加 uptime 节点）
const (
	NodeIDReservedSelfRelay = 0
	NodeIDReservedNoPublic  = 23 + 34*23 // "PP"
	NodeIDMax               = 33 + 34*33 // 1155
)

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
// GenerateRoomCodeWithNodeID 生成携带 uptime 节点 ID 的房间码。
// nodeID 按小端 base-34 编码：N 部分最后一位（位置 7）为低位、S 部分最后一位
// （位置 15）为高位；校验微调从位置 15 移到位置 14（权重 1，任何字符值都可微调）。
func GenerateRoomCodeWithNodeID(nodeID int) (*RoomCode, error) {
	if nodeID < 0 || nodeID > NodeIDMax {
		return nil, fmt.Errorf("nodeID 超出可编码范围: %d", nodeID)
	}

	var chars [16]byte
	// 位置 7：nodeID 低位字符；位置 15：nodeID 高位字符
	chars[7] = utils.Charset[nodeID%34]
	chars[15] = utils.Charset[(nodeID/34)%34]

	// 其余位置随机（位置 14 除外，留作校验微调）
	for i := 0; i < 16; i++ {
		if i == 7 || i == 14 || i == 15 {
			continue
		}
		c, err := utils.RandomChar()
		if err != nil {
			return nil, fmt.Errorf("failed to generate random char: %w", err)
		}
		chars[i] = c
	}

	// 微调位置 14（权重 1）使加权和 % 7 == 0：v14 ≡ -sum (mod 7)
	sum := 0
	for i := 0; i < 16; i++ {
		if i == 14 {
			continue
		}
		v, _ := utils.Value(chars[i])
		if i%2 == 1 {
			v *= 6
		}
		sum += v
	}
	rem := (-sum) % 7
	if rem < 0 {
		rem += 7
	}
	for ci := range utils.Charset {
		if ci%7 == rem {
			chars[14] = utils.Charset[ci]
			break
		}
	}

	return &RoomCode{
		NetworkPart: string(chars[:8]),
		SecretPart:  string(chars[8:]),
	}, nil
}

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

// NodeID 解码内嵌的 uptime 节点 ID（N 部分最后一位为低位、S 部分最后一位为高位，
// 小端 base-34）。旧格式房间码未内嵌 nodeID，解析结果为随机值，调用方需验证有效性。
func (r *RoomCode) NodeID() int {
	v7, _ := utils.Value(r.NetworkPart[7])
	v15, _ := utils.Value(r.SecretPart[7])
	return v7 + 34*v15
}
