package scaffolding

import (
	"fmt"
	"strings"

	"gravitycone/core/easytier"
	"gravitycone/core/protocol/common"
)

type RoomCode struct {
	NetworkPart string // 8 chars: NNNN-NNNN (without dash)
	SecretPart  string // 8 chars: SSSS-SSSS (without dash)
}

// checksumSum 按加权公式求和：位置 i 的字符值乘以 6^(i%2)，跳过 skip 位置（-1 表示不跳过）。
func checksumSum(chars [16]byte, skip int) int {
	sum := 0
	for i := 0; i < 16; i++ {
		if i == skip {
			continue
		}
		v, _ := common.Value(chars[i])
		if i%2 == 1 {
			v *= 6
		}
		sum += v
	}
	return sum
}

func isValidChecksum(chars [16]byte) bool {
	for i := 0; i < 16; i++ {
		if _, ok := common.Value(chars[i]); !ok {
			return false
		}
	}
	return checksumSum(chars, -1)%7 == 0
}

// generateRoomCode 生成房间码；nodeID < 0 时为旧格式（不内嵌 nodeID，
// 校验字符在位置 15），否则内嵌 uptime 节点 ID。
// nodeID 按小端 base-34 编码：N 部分最后一位（位置 7）为低位、S 部分最后一位
// （位置 15）为高位；校验微调从位置 15 移到位置 14（权重 1，任何字符值都可微调）。
func generateRoomCode(nodeID int) (*RoomCode, error) {
	if nodeID > easytier.NodeIDMax {
		return nil, fmt.Errorf("nodeID 超出可编码范围: %d", nodeID)
	}
	withNodeID := nodeID >= 0

	var chars [16]byte
	common.RandomChars(chars[:])

	if withNodeID {
		lo, hi := easytier.NodeIDChars(nodeID)
		chars[7] = common.Charset[lo]
		chars[15] = common.Charset[hi]

		// 微调位置 14（权重 1）使加权和 % 7 == 0：v14 ≡ -sum (mod 7)。
		// rem 恒在 [0,6]，Charset[rem] 就是值为 rem 的字符。
		rem := (-checksumSum(chars, 14)) % 7
		if rem < 0 {
			rem += 7
		}
		chars[14] = common.Charset[rem]
	} else {
		// 旧格式：Position 15 is odd, so its weight is 6. We need (sum + v*6) % 7 == 0.
		// v*6 mod 7 == (-v) mod 7, so we need v ≡ sum (mod 7)。rem 恒在 [0,6]。
		chars[15] = common.Charset[checksumSum(chars, 15)%7]
	}

	return &RoomCode{
		NetworkPart: string(chars[:8]),
		SecretPart:  string(chars[8:]),
	}, nil
}

func GenerateRoomCodeWithNodeID(nodeID int) (*RoomCode, error) {
	if nodeID < 0 {
		return nil, fmt.Errorf("nodeID 超出可编码范围: %d", nodeID)
	}
	return generateRoomCode(nodeID)
}

// GenerateRoomCode generates a valid room code deterministically:
// the first 15 characters are random, and the 16th is computed so
// the checksum passes, eliminating rejection sampling.
func GenerateRoomCode() (*RoomCode, error) {
	return generateRoomCode(-1)
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
			if _, ok := common.Value(chars[i]); !ok {
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
	lo, _ := common.Value(r.NetworkPart[7])
	hi, _ := common.Value(r.SecretPart[7])
	return easytier.NodeIDFromChars(lo, hi)
}
