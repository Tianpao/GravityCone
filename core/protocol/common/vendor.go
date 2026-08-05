package common

import (
	"strings"

	"gravitycone/core/easytier"
)

// BaseVendor is the default vendor suffix. Call MakeVendor to append optional prefixes.
const BaseVendor = "GVC " + easytier.AppVersion + ", EasyTier " + easytier.EasyTierVersion

// MakeVendor 拼接厂商前缀（可选）与默认后缀，两个协议共用。
func MakeVendor(prefixes ...string) string {
	parts := make([]string, 0, len(prefixes)+1)
	for _, p := range prefixes {
		if p != "" {
			parts = append(parts, p)
		}
	}
	parts = append(parts, BaseVendor)
	return strings.Join(parts, ", ")
}
