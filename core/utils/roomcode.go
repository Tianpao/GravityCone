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

func RandomChar() (byte, error) {
	var buf [1]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return 0, err
	}
	return Charset[buf[0]%byte(len(Charset))], nil
}
