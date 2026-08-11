package utils

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// FixCommandLineMojibake 修复 Windows 上 launcher 传入命令行参数时的中文乱码。
//
// 背景：部分启动器（如 PCL）用系统 ANSI 码页（GBK）拼装 --motd 命令行，却把它当作
// UTF-16 传给 CreateProcessW。GBK 双字节 0xC1 0xAA（"联"）会被错解成一个 UTF-16
// 码元 U+AAC1，在 Go 的 os.Args 里变成 "꿻" 这类方块乱码，且无法用普通译码恢复。
//
// 该变换是可逆的：GBK 双字节映射成的码元必然落在 U+8140–U+FEFE（高字节 0x81–0xFE、
// 低字节 0x40–0xFE 且非 0x7F），与正常中文 U+4E00–U+9FFF 不重叠。因此可逐字符判断：
// 命中该区间的字符还原为 GBK 字节对并译回 UTF-8，其余字符原样保留。
//
// 为降低误判，仅当"疑似乱码字符占非 ASCII 字符的多数"时才执行还原，否则原样返回。
func FixCommandLineMojibake(s string) string {
	if !utf8.ValidString(s) {
		return s
	}
	runes := []rune(s)
	nonASCII, baked := 0, 0
	for _, r := range runes {
		if r >= utf8.RuneSelf {
			nonASCII++
			if _, ok := gbkBakedBytes(r); ok {
				baked++
			}
		}
	}
	// 需要确有乱码，且乱码占非 ASCII 字符的多数，才值得还原
	if baked == 0 || baked*2 < nonASCII {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range runes {
		if bl, ok := gbkBakedBytes(r); ok {
			if dec, err := simplifiedchinese.GBK.NewDecoder().Bytes(bl); err == nil {
				b.Write(dec)
				continue
			}
		}
		b.WriteRune(r)
	}
	return b.String()
}

// gbkBakedBytes 判断 r 是否像"GBK 双字节被错解为 UTF-16 码元"的字符，
// 是则返回还原出的 GBK 字节对。
func gbkBakedBytes(r rune) ([]byte, bool) {
	if r < 0x8140 || r > 0xFEFE {
		return nil, false
	}
	hi, lo := byte(r>>8), byte(r)
	if hi < 0x81 || hi > 0xFE {
		return nil, false
	}
	if lo < 0x40 || lo > 0xFE || lo == 0x7F {
		return nil, false
	}
	// r 是 UTF-16 LE 码元：低字节(byte(r))在低地址，对应 GBK 的 lead byte；
	// 高字节(byte(r>>8))对应 GBK 的 trail byte。还原时先 lead 后 trail。
	return []byte{lo, hi}, true
}