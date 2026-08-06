//go:build et_ffi && android

// Package easytier provides dlopen/dlsym for Android via CGO.
// libeasytier_ffi.so is pre-loaded by Java's System.loadLibrary;
// dlopen returns the existing handle (ref-counted on Bionic).
package easytier

/*
#include <dlfcn.h>
#include <stdlib.h>
*/
import "C"
import "unsafe"

// dlopenLib loads a shared library that has already been opened by the JVM.
// On Android/Bionic, dlopen on an already-loaded library returns the existing
// handle with reference count incremented.
func dlopenLib() unsafe.Pointer {
	name := C.CString("libeasytier_ffi.so")
	defer C.free(unsafe.Pointer(name))
	return C.dlopen(name, C.RTLD_NOW|C.RTLD_GLOBAL)
}

// dlsymLib resolves a symbol in the loaded library.
func dlsymLib(handle unsafe.Pointer, name string) uintptr {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	return uintptr(C.dlsym(handle, cname))
}
