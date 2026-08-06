package paperconnect

import (
	"fmt"
	"strings"

	"gravitycone/core/easytier"
	"gravitycone/core/protocol/common"
)

func charToValue(c byte) (int, bool) {
	return common.Value(c)
}

const pcRoomCodeHeader = "P/"
const pcRoomName = "paper-connect"

// PaperConnectRoomCode represents a Bedrock Edition room code.
// Format: P/NNNN-NNNN-SSSS-SSSS
// N-part: 8 random chars for the EasyTier network name; last char encodes the
// low digit of the uptime node ID.
// S-part: 8 chars adjusted so the little-endian base-34 value is divisible by 7;
// last char encodes the high digit of the uptime node ID (checksum adjusts the
// second-to-last char instead).
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

func pcIsDivisibleBySeven(chars []byte) bool {
	return pcConvertToLong(chars)%7 == 0
}

// pcAdjustForDivisibilityBySevenAt 调整 pos 位置使小端 base-34 值整除 7。
// 仅适用于权重 ≡ 1 (mod 7) 的位置（如 0、6），调整量直接等量改变余数。
func pcAdjustForDivisibilityBySevenAt(chars []byte, pos int) {
	rem := pcConvertToLong(chars) % 7
	if rem == 0 {
		return
	}
	v, _ := charToValue(chars[pos])
	newV := v - int(rem)
	if newV < 0 {
		newV += 7
	}
	chars[pos] = common.Charset[newV]
}

// generatePaperConnectRoomCode 生成房间码；nodeID < 0 时为旧格式（不内嵌
// nodeID，S 部分校验微调位置 0），否则内嵌 uptime 节点 ID。
// nodeID 按小端 base-34 编码：N 部分最后一位为低位、S 部分最后一位为高位；
// 校验微调移到 S 部分倒数第二位（最后一位被 nodeID 占用）。
func generatePaperConnectRoomCode(nodeID int) (*PaperConnectRoomCode, error) {
	if nodeID > easytier.NodeIDMax {
		return nil, fmt.Errorf("nodeID 超出可编码范围: %d", nodeID)
	}
	withNodeID := nodeID >= 0

	var nPart, sPart [8]byte
	common.RandomChars(nPart[:])
	common.RandomChars(sPart[:])

	if withNodeID {
		lo, hi := easytier.NodeIDChars(nodeID)
		nPart[7] = common.Charset[lo]
		sPart[7] = common.Charset[hi]
		// 位置 6 用作校验微调：调整函数会读取它的当前值计算新值。
		pcAdjustForDivisibilityBySevenAt(sPart[:], 6)
	} else {
		// 旧格式：位置 0 的权重 34^0 ≡ 1 (mod 7)，微调该位即可
		pcAdjustForDivisibilityBySevenAt(sPart[:], 0)
	}

	return &PaperConnectRoomCode{
		NetworkPart: string(nPart[:]),
		SecretPart:  string(sPart[:]),
	}, nil
}

func GeneratePaperConnectRoomCodeWithNodeID(nodeID int) (*PaperConnectRoomCode, error) {
	if nodeID < 0 {
		return nil, fmt.Errorf("nodeID 超出可编码范围: %d", nodeID)
	}
	return generatePaperConnectRoomCode(nodeID)
}

func GeneratePaperConnectRoomCode() (*PaperConnectRoomCode, error) {
	return generatePaperConnectRoomCode(-1)
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

// NodeID 解码内嵌的 uptime 节点 ID（N 部分最后一位为低位、S 部分最后一位为高位，
// 小端 base-34）。旧格式房间码未内嵌 nodeID，解析结果为随机值，调用方需验证有效性。
func (r *PaperConnectRoomCode) NodeID() int {
	lo, _ := charToValue(r.NetworkPart[7])
	hi, _ := charToValue(r.SecretPart[7])
	return easytier.NodeIDFromChars(lo, hi)
}
