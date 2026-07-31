//go:build et_ffi && !android

// Stub for gopls/static analysis. Real implementation is in dl_android.go
// (which requires CGO and is only used during actual Android builds).
package easytier

import "unsafe"

func dlopenLib() unsafe.Pointer                    { return nil }
func dlsymLib(handle unsafe.Pointer, name string) uintptr { return 0 }
