package utils

import "crypto/rand"

const Charset = "0123456789ABCDEFGHJKLMNPQRSTUVWXYZ"

var values = func() [256]int8 {
	var table [256]int8
	for i := range table {
		table[i] = -1
	}
	for i := 0; i < len(Charset); i++ {
		table[Charset[i]] = int8(i)
	}
	return table
}()

func Value(c byte) (int, bool) {
	value := int(values[c])
	return value, value >= 0
}

// IsPaperConnectCode 判断房间码是否为 PaperConnect 前缀（"P/" 或 "p/"）。
func IsPaperConnectCode(code string) bool {
	return len(code) >= 2 && (code[0] == 'P' || code[0] == 'p') && code[1] == '/'
}

func RandomChar() (byte, error) {
	var buf [1]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	return Charset[buf[0]%byte(len(Charset))], nil
}

// RandomChars 用随机字符填满 dst 的所有位置。
func RandomChars(dst []byte) error {
	buf := make([]byte, len(dst))
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	for i, b := range buf {
		dst[i] = Charset[b%byte(len(Charset))]
	}
	return nil
}
