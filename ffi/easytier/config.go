//go:build et_ffi

package easytier

import (
	ffi_toml "gravitycone/ffi/easytier/tomlconfig"
)

// StartOptions mirrors easytier.StartOptions from core/easytier.
// Duplicated here to keep ffi/easytier self-contained (no dependency on core/easytier
// which imports os/exec and is desktop-only).
type StartOptions = ffi_toml.StartOptions

// BuildTOMLConfig generates an EasyTier TOML config from StartOptions.
// Delegates to the pure-Go tomlconfig package.
func BuildTOMLConfig(opts StartOptions) string {
	return ffi_toml.BuildTOMLConfig(opts)
}

const hostVirtualIP = ffi_toml.HostVirtualIP
