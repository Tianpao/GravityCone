package utils

import (
	"bytes"
	"io"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// NewDecodedReader wraps r with encoding auto-detection and transparent
// conversion to UTF-8. It reads a few initial bytes to detect the encoding
// (BOM or heuristic), then returns a reader that yields UTF-8 output.
func NewDecodedReader(r io.Reader) io.Reader {
	header := make([]byte, 3)
	n, err := io.ReadAtLeast(r, header, 1)
	if err != nil && n == 0 {
		return r
	}

	data := header[:n]
	enc, skip := detectEncoding(data)

	remainder := data[skip:]
	src := io.MultiReader(bytes.NewReader(remainder), r)
	return transform.NewReader(src, enc.NewDecoder())
}

// detectEncoding examines initial bytes and returns the detected
// encoding and the number of leading BOM bytes to skip.
func detectEncoding(data []byte) (encoding.Encoding, int) {
	// BOM-based detection
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return unicode.UTF8, 3
	}
	if len(data) >= 2 {
		if data[0] == 0xFF && data[1] == 0xFE {
			return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), 2
		}
		if data[0] == 0xFE && data[1] == 0xFF {
			return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), 2
		}
	}

	// Heuristic: UTF-16 without BOM
	if len(data) >= 2 {
		if data[1] == 0 && data[0] != 0 {
			return unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), 0
		}
		if data[0] == 0 && data[1] != 0 {
			return unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), 0
		}
	}

	// Heuristic: GBK — has high bytes and is not valid UTF-8
	if hasHighBytes(data) && !isValidUTF8Prefix(data) {
		return simplifiedchinese.GBK, 0
	}

	return unicode.UTF8, 0
}

func hasHighBytes(data []byte) bool {
	for _, b := range data {
		if b > 0x7F {
			return true
		}
	}
	return false
}

func isValidUTF8Prefix(data []byte) bool {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			return false
		}
		i += size
	}
	return true
}
