package utils

import (
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// gbkToUTF16Baked 模拟启动器乱码：构造一段 UTF-16 命令行字节流，其中 ASCII
// 字符是正常码元，中文字符的 GBK 双字节被错误合并成一个 UTF-16 LE 码元
// （即 launcher 把 GBK 字节当作 UTF-16 传参后的结果，也是 os.Args 里的乱码）。
func gbkToUTF16Baked(t *testing.T, s string) string {
	t.Helper()
	units := []uint16{}
	for _, r := range s {
		if r < utf8.RuneSelf {
			units = append(units, uint16(r))
			continue
		}
		gbkBytes, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(string(r)))
		if err != nil || len(gbkBytes) != 2 {
			t.Fatalf("encode gbk %q: %v", r, err)
		}
		// GBK 字节 [hi, lo] 作为 UTF-16 LE 码元的低、高字节 → lo<<8 | hi
		units = append(units, uint16(gbkBytes[1])<<8|uint16(gbkBytes[0]))
	}
	return string(utf16.Decode(units))
}

func TestFixCommandLineMojibake(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "纯中文乱码还原",
			in:   gbkToUTF16Baked(t, "联机房间"),
			want: "联机房间",
		},
		{
			name: "ASCII+中文乱码混合还原",
			in:   gbkToUTF16Baked(t, "PCL 联机房间"),
			want: "PCL 联机房间",
		},
		{
			name: "正常UTF-8中文不受影响",
			in:   "我的服务器",
			want: "我的服务器",
		},
		{
			name: "纯ASCII不受影响",
			in:   "PCL CE Server",
			want: "PCL CE Server",
		},
		{
			name: "空串",
			in:   "",
			want: "",
		},
		{
			name: "英文+正常UTF-8单字不误伤（无多数乱码时原样返回）",
			in:   "PCL 好",
			want: "PCL 好",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := FixCommandLineMojibake(c.in)
			if got != c.want {
				t.Errorf("FixCommandLineMojibake(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}