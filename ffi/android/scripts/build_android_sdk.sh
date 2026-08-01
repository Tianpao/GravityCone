#!/usr/bin/env bash
#
# build_android_sdk.sh — 一键构建 Android FFI SDK（Linux/macOS/WSL）：
#   第 1 步：libeasytier_ffi.so   （EasyTier 源码，cargo-ndk）
#   第 2 步：libgravitycone.so    （GravityCone Go cgo，NDK clang）
#   第 3 步：打包 gravitycone-android-sdk.zip
# 与 build_android_sdk.bat 对称。
#
# 前置条件：Rust(rustup) + cargo-ndk + protoc（第 1 步自动处理）、
# Go 1.22+、Android NDK（自动探测 / 自动下载）。
# 产物：<repo>/ffi/android/dist/gravitycone-android-sdk.zip
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"

case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        echo "错误: 请在 WSL/Linux/macOS 运行本脚本，Windows 原生 shell 请用 build_android_sdk.bat" >&2
        exit 1
        ;;
esac

echo "============================================================"
echo "  第 1/3 步: libeasytier_ffi.so（EasyTier 源码，cargo-ndk）"
echo "  两个架构并行编译（各 10-20 分钟，并行后总耗时约减半）"
echo "============================================================"
bash "$SCRIPT_DIR/build_easytier_ffi_android.sh" arm64 "$REPO_ROOT/ffi/android/jniLibs/arm64-v8a" &
p1=$!
bash "$SCRIPT_DIR/build_easytier_ffi_android.sh" amd64 "$REPO_ROOT/ffi/android/jniLibs/x86_64" &
p2=$!
wait "$p1"
wait "$p2"

echo
echo "============================================================"
echo "  第 2/3 步: libgravitycone.so（GravityCone，Go 交叉编译）"
echo "============================================================"
bash "$SCRIPT_DIR/build_gravitycone_android.sh"

echo
echo "============================================================"
echo "  第 3/3 步: 打包 gravitycone-android-sdk.zip"
echo "============================================================"
mkdir -p "$REPO_ROOT/ffi/android/dist"
rm -f "$REPO_ROOT/ffi/android/dist/gravitycone-android-sdk.zip"
(
    cd "$REPO_ROOT/ffi/android"
    zip -r dist/gravitycone-android-sdk.zip \
        jniLibs/arm64-v8a/libeasytier_ffi.so \
        jniLibs/arm64-v8a/libgravitycone.so \
        jniLibs/arm64-v8a/libgravitycone.h \
        jniLibs/x86_64/libeasytier_ffi.so \
        jniLibs/x86_64/libgravitycone.so \
        jniLibs/x86_64/libgravitycone.h \
        java/net/gravitycone/ffi/GravityConeAndroidAPI.java
)

echo
echo "============================================================"
echo "  完成。SDK 包:"
echo "    $REPO_ROOT/ffi/android/dist/gravitycone-android-sdk.zip"
echo "  下一步：重新构建测试 APK"
echo "    cd test && ./gradlew :app:assembleDebug"
echo "============================================================"
