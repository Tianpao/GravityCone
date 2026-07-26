//go:build cgo

package main

// main is required for -buildmode=c-shared. It runs once when the library
// is loaded by the host process. Currently a no-op — initialization is done
// explicitly via gc_init().
func main() {}
