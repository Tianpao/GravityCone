//go:build et_ffi

// JNI exports for Android. These functions are registered by the JVM when
// GravityConeAndroidAPI.java calls System.loadLibrary("gravitycone").
//
// Each function follows the JNI naming convention:
//
//	Java_<package>_<Class>_<method>
//
// The JVM resolves them automatically via dlsym. We use JNI helpers in the
// CGo preamble to convert between Go strings and jstring.
package main

/*
#include <jni.h>
#include <stdlib.h>

// JNI helper: create a jstring from a C string.
static jstring jni_NewStringUTF(JNIEnv *env, const char *str) {
	if (str == NULL) return NULL;
	return (*env)->NewStringUTF(env, str);
}

// JNI helper: get a C string from a jstring.
// The caller must call jni_ReleaseStringUTFChars to release.
static const char* jni_GetStringUTFChars(JNIEnv *env, jstring str) {
	if (str == NULL) return NULL;
	return (*env)->GetStringUTFChars(env, str, NULL);
}

// JNI helper: release a C string obtained from jni_GetStringUTFChars.
static void jni_ReleaseStringUTFChars(JNIEnv *env, jstring str, const char *cstr) {
	if (cstr != NULL) (*env)->ReleaseStringUTFChars(env, str, cstr);
}
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// JNI string helpers: convert between Go string and C string via JNI.

func jniToGoString(env *C.JNIEnv, jstr C.jstring) string {
	cstr := C.jni_GetStringUTFChars(env, jstr)
	if cstr == nil || *cstr == 0 {
		return ""
	}
	defer C.jni_ReleaseStringUTFChars(env, jstr, cstr)
	return C.GoString(cstr)
}

func jniFromGoString(env *C.JNIEnv, s string) C.jstring {
	cStr := C.CString(s)
	defer C.free(unsafe.Pointer(cStr))
	return C.jni_NewStringUTF(env, cStr)
}

// =========================================================================
// JNI native methods — called by GravityConeAndroidAPI.java
// =========================================================================

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeInit
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeInit(
	env *C.JNIEnv, clazz C.jclass, baseDir C.jstring, loggingFd C.jint,
) C.jint {
	dir := jniToGoString(env, baseDir)
	_ = dir
	_ = loggingFd
	return gc_init(nil) // baseDir is unused for now
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeGetState
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeGetState(
	env *C.JNIEnv, clazz C.jclass,
) C.jstring {
	return jniFromGoString(env, getStateJSON())
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeSetWaiting
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeSetWaiting(
	env *C.JNIEnv, clazz C.jclass,
) {
	setWaiting()
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeSetScanning
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeSetScanning(
	env *C.JNIEnv, clazz C.jclass,
	room C.jstring, player C.jstring, protocol C.jstring,
) {
	r := jniToGoString(env, room)
	p := jniToGoString(env, player)
	proto := jniToGoString(env, protocol)
	setScanning(r, p, proto)
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeSetGuesting
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeSetGuesting(
	env *C.JNIEnv, clazz C.jclass,
	room C.jstring, player C.jstring,
) C.jboolean {
	r := jniToGoString(env, room)
	p := jniToGoString(env, player)
	if setGuesting(r, p) {
		return C.JNI_TRUE
	}
	return C.JNI_FALSE
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeVerifyRoomCode
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeVerifyRoomCode(
	env *C.JNIEnv, clazz C.jclass, code C.jstring,
) C.jint {
	c := jniToGoString(env, code)
	return C.jint(verifyRoomCode(c))
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeStunProbe
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeStunProbe(
	env *C.JNIEnv, clazz C.jclass,
) C.jstring {
	result := stunProbe()
	return jniFromGoString(env, result)
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeGetMetadata
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeGetMetadata(
	env *C.JNIEnv, clazz C.jclass,
) C.jstring {
	meta := Metadata{
		Version:         "0.1.3-alpha",
		CompileTime:     CompileTime.Load(),
		EasyTierVersion: "v2.6.4",
	}
	data, _ := json.Marshal(meta)
	return jniFromGoString(env, string(data))
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeShutdown
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeShutdown(
	env *C.JNIEnv, clazz C.jclass,
) {
	goBackToIdle()
}

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeSetTunFd
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeSetTunFd(
	env *C.JNIEnv, clazz C.jclass,
	instName C.jstring, fd C.jint,
) C.jint {
	// This is called after the VpnService establishes the TUN interface.
	// Currently a no-op because GravityCone uses no_tun=true (port-forward mode).
	// When TUN mode is needed, call ffi/easytier.SetTunFd() here.
	name := jniToGoString(env, instName)
	_ = name
	_ = fd
	return 0
}

// =========================================================================
// JNI callback: onVpnServiceStateChanged
//
// Called from native when EasyTier needs a TUN fd. This triggers the
// VpnServiceCallback on the Java side. Currently not used because
// GravityCone operates in no_tun mode (port-forward only).
// =========================================================================

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_onVpnServiceStateChanged
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_onVpnServiceStateChanged(
	env *C.JNIEnv, clazz C.jclass,
	ip1, ip2, ip3, ip4 C.jbyte,
	networkLength C.jshort,
	cidr C.jstring,
) C.jint {
	// Reserved for future TUN mode support.
	// See Terracotta's onVpnServiceStateChanged for the full implementation.
	return -1
}

// Ensure unused imports are fine.
var _ = fmt.Sprintf
