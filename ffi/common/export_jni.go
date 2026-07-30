//go:build et_ffi && linux

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

// --- Reverse JNI helpers (Go calls Java) ---

// Get the JavaVM pointer from a JNIEnv.
static jint jni_GetJavaVM(JNIEnv *env, JavaVM **vm) {
	return (*env)->GetJavaVM(env, vm);
}

// Attach the current thread to the JVM and get a JNIEnv.
static jint jni_AttachCurrentThread(JavaVM *vm, JNIEnv **env) {
	JavaVMAttachArgs args;
	args.version = JNI_VERSION_1_6;
	args.name = NULL;
	args.group = NULL;
	return (*vm)->AttachCurrentThread(vm, (void**)env, &args);
}

// Create a global reference to a class (survives across JNI calls).
static jobject jni_NewGlobalRef(JNIEnv *env, jobject obj) {
	return (*env)->NewGlobalRef(env, obj);
}

// Delete a global reference.
static void jni_DeleteGlobalRef(JNIEnv *env, jobject obj) {
	if (obj != NULL) (*env)->DeleteGlobalRef(env, obj);
}

// Get a static method ID.
static jmethodID jni_GetStaticMethodID(JNIEnv *env, jclass clazz, const char *name, const char *sig) {
	return (*env)->GetStaticMethodID(env, clazz, name, sig);
}

// Call onVpnServiceStateChanged(BBBBLjava/lang/String;)I on the API class.
// This invokes the Java-side method which blocks until VpnService is established.
static jint jni_CallVpnServiceCallback(JNIEnv *env, jclass clazz, jmethodID methodID,
	jbyte ip1, jbyte ip2, jbyte ip3, jbyte ip4, jshort networkLength, jstring cidr) {
	return (*env)->CallStaticIntMethod(env, clazz, methodID,
		ip1, ip2, ip3, ip4, networkLength, cidr);
}
*/
import "C"
import (
	"encoding/json"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	ffi_et "gravitycone/ffi/easytier"
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
// JVM caching for reverse JNI calls (Go → Java)
// =========================================================================

var (
	cachedJVM       *C.JavaVM
	cachedClassRef  C.jclass // global reference to GravityConeAndroidAPI class
	jvmInitialized  bool
)

// =========================================================================
// JNI native methods — called by GravityConeAndroidAPI.java
// =========================================================================

//export Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeInit
func Java_net_gravitycone_ffi_GravityConeAndroidAPI_nativeInit(
	env *C.JNIEnv, clazz C.jclass, baseDir C.jstring, loggingFd C.jint,
) C.jint {
	dir := jniToGoString(env, baseDir)
	_ = loggingFd // TODO: wire up logging fd to Go log output
	cDir := C.CString(dir)
	defer C.free(unsafe.Pointer(cDir))
	ret := gc_init(cDir)

	// Cache JavaVM and class reference for reverse JNI calls.
	// This enables Go code to call Java methods (e.g., onVpnServiceStateChanged)
	// from goroutines that were not invoked from Java.
	var jvm *C.JavaVM
	if vmRet := C.jni_GetJavaVM(env, &jvm); vmRet != 0 {
		// JVM caching failure is non-fatal: port-forward mode will still work,
		// but TUN fd injection via VpnService won't be available.
		jvmInitialized = false
	} else {
		cachedJVM = jvm
		// Create a global reference to the class so it survives after nativeInit returns.
		// Local references are only valid for the duration of the JNI call.
		globalRef := C.jni_NewGlobalRef(env, C.jobject(clazz))
		cachedClassRef = C.jclass(globalRef)
		jvmInitialized = true
	}

	return ret
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
	// Called by Java after VpnService establishes the TUN interface.
	// Inject the fd into EasyTier via the FFI bridge.
	name := jniToGoString(env, instName)
	if err := ffi_et.SetTunFd(name, int(fd)); err != nil {
		return -1
	}
	return 0
}

// =========================================================================
// Reverse JNI: callJavaVpnServiceCallback
//
// This function is called by FFIManager (via DefaultTunFdProvider) when
// EasyTier needs a TUN fd. It uses the cached JavaVM to attach to the JVM
// and call the Java-side onVpnServiceStateChanged method, which blocks
// until the Android app establishes (or rejects) the VpnService.
// =========================================================================

// callJavaVpnServiceCallback calls Java's GravityConeAndroidAPI.onVpnServiceStateChanged
// via reverse JNI to request a TUN fd from VpnService.
//
// The Java method blocks until the host app calls VpnServiceRequest.startVpnService()
// or VpnServiceRequest.reject(), or until the 30-second timeout expires.
//
// Parameters:
//   - instName: EasyTier instance name (for logging)
//   - virtualIP: the virtual IP address (e.g., "10.144.144.1")
//   - cidr: the CIDR route string (e.g., "10.144.144.0/24")
//
// Returns the TUN file descriptor, or an error.
func callJavaVpnServiceCallback(instName string, virtualIP string, cidr string) (int, error) {
	if !jvmInitialized {
		return -1, fmt.Errorf("JVM not initialized, cannot request VpnService TUN fd")
	}

	// Parse virtual IP into 4 bytes for the JNI call.
	parts := strings.SplitN(virtualIP, ".", 4)
	if len(parts) != 4 {
		return -1, fmt.Errorf("invalid virtual IP format: %q", virtualIP)
	}
	var ipBytes [4]byte
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || v > 255 {
			return -1, fmt.Errorf("invalid IP octet %q in %q", p, virtualIP)
		}
		ipBytes[i] = byte(v)
	}

	// Determine network length from CIDR (default /24).
	networkLength := 24
	if idx := strings.IndexByte(cidr, '/'); idx >= 0 {
		if n, err := strconv.Atoi(cidr[idx+1:]); err == nil && n > 0 && n <= 32 {
			networkLength = n
		}
	}

	// Lock the goroutine to its current OS thread.
	// JNI requires that a thread attached to the JVM stays on the same OS thread
	// for the duration of the JNI calls. Go's scheduler may migrate goroutines
	// between OS threads, which would break JNI.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Attach the current OS thread to the JVM to get a JNIEnv.
	var env *C.JNIEnv
	if ret := C.jni_AttachCurrentThread(cachedJVM, &env); ret != 0 {
		return -1, fmt.Errorf("JNI AttachCurrentThread failed (ret=%d)", int(ret))
	}
	// Note: We intentionally do NOT call DetachCurrentThread here.
	// Android's ART VM manages thread attachment gracefully; detaching could
	// cause issues if Go reuses this OS thread for another JNI call later.
	// The thread will be cleaned up when the process exits.

	// Get the method ID for onVpnServiceStateChanged.
	// Signature: (byte, byte, byte, byte, short, String) -> int
	methodName := C.CString("onVpnServiceStateChanged")
	defer C.free(unsafe.Pointer(methodName))
	methodSig := C.CString("(BBBBLjava/lang/String;)I")
	defer C.free(unsafe.Pointer(methodSig))
	methodID := C.jni_GetStaticMethodID(env, cachedClassRef, methodName, methodSig)
	if methodID == nil {
		return -1, fmt.Errorf("JNI GetStaticMethodID failed: onVpnServiceStateChanged not found")
	}

	// Create jstring for CIDR.
	cidrC := C.CString(cidr)
	defer C.free(unsafe.Pointer(cidrC))
	jcidr := C.jni_NewStringUTF(env, cidrC)

	// Call the Java method. This BLOCKS until VpnService is established or rejected.
	fd := int(C.jni_CallVpnServiceCallback(env, cachedClassRef, methodID,
		C.jbyte(int8(ipBytes[0])),
		C.jbyte(int8(ipBytes[1])),
		C.jbyte(int8(ipBytes[2])),
		C.jbyte(int8(ipBytes[3])),
		C.jshort(int16(networkLength)),
		jcidr))

	if fd < 0 {
		return -1, fmt.Errorf("VpnService callback returned error fd: %d", fd)
	}

	return fd, nil
}

// Ensure unused imports are fine.
var _ = fmt.Sprintf
