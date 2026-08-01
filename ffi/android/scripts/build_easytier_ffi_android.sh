#!/usr/bin/env bash
#
# build_easytier_ffi_android.sh — 从 EasyTier 官方源码交叉编译 Android 版 libeasytier_ffi.so
#
# 背景：libeasytier_ffi.so 是 easytier-contrib/easytier-ffi 这个 cdylib crate 的产物。
# 此前从 qteasytier/easytier-ffi-bin 下载的 "linux-aarch64"/"linux-amd64" 资产是
# 桌面 glibc 库（NEEDED libc.so.6），Android bionic 无法 dlopen，导致
# "parse_config: dlopen libeasytier_ffi.so faied"。Android 版必须从源码
# 用 cargo-ndk 交叉编译（官方 easytier-contrib/easytier-android-jni/build.sh 同款方式）。
#
# 用法：
#   ./build_easytier_ffi_android.sh <arm64|amd64> <output_dir>
#     arch:        arm64 → jniLibs/arm64-v8a；amd64 → jniLibs/x86_64
#     output_dir:  复制目标目录（如 ffi/android/jniLibs/arm64-v8a）
#
# 前置条件（Linux/WSL/macOS）：
#   - Rust（rustup）+ cargo-ndk：cargo install cargo-ndk
#   - Android NDK，环境变量之一：ANDROID_NDK_HOME / ANDROID_NDK_ROOT / NDK_HOME
#   - 能访问 GitHub（clone + crates.io + thunk-rs git 依赖；国内可配 http_proxy/https_proxy）
#
# 源码缓存目录可用 EASYTIER_SRC_DIR 覆盖（默认 <repo>/ffi/android/.cache/easytier），
# CI 里配合 rust-cache 复用编译产物。
set -euo pipefail

ARCH="${1:-}"
OUTPUT_DIR="${2:-}"
if [ -z "$ARCH" ] || [ -z "$OUTPUT_DIR" ]; then
    echo "用法: $0 <arm64|amd64> <output_dir>" >&2
    echo "  arm64 → arm64-v8a，amd64 → x86_64" >&2
    exit 1
fi

EASYTIER_VERSION="v2.6.4"
EASYTIER_REPO="https://github.com/EasyTier/EasyTier.git"
CACHE_DIR="${EASYTIER_SRC_DIR:-$(pwd)/ffi/android/.cache/easytier}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/build_common.sh"

case "$ARCH" in
    arm64) ABI="arm64-v8a";  RUST_TARGET="aarch64-linux-android" ;;
    amd64) ABI="x86_64";     RUST_TARGET="x86_64-linux-android" ;;
    *) echo "不支持的架构: $ARCH（仅支持 arm64 / amd64）" >&2; exit 1 ;;
esac

# Windows 本机无法交叉编译（需要 NDK + cargo-ndk 工具链），提示用 WSL
if [ "$(uname -s)" = "MINGW"* ] || [ "$(uname -s)" = "CYGWIN"* ] || [ "$(uname -s)" = "MSYS"* ]; then
    echo "错误: 请勿在 Windows 原生 shell 运行，请在 WSL/Linux/macOS 或 CI 上执行（Windows 不能交叉编译到 Android）。" >&2
    exit 1
fi

# ---------- 工具链检查 ----------
command -v cargo >/dev/null 2>&1 || { echo "错误: 未找到 cargo，请先安装 Rust（rustup）：curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh" >&2; exit 1; }
if ! command -v protoc >/dev/null 2>&1; then
    echo "错误: 未找到 protoc（easytier 的 prost-build 需要它生成 protobuf 代码），请先安装：" >&2
    echo "  Ubuntu/Debian: sudo apt install protobuf-compiler" >&2
    echo "  macOS: brew install protobuf" >&2
    echo "  或从 https://github.com/protocolbuffers/protobuf/releases 下载并设置 PROTOC 环境变量指向 protoc" >&2
    exit 1
fi
if ! cargo ndk --version >/dev/null 2>&1; then
    echo "错误: 未找到 cargo-ndk，请先执行: cargo install cargo-ndk" >&2
    exit 1
fi
locate_ndk

# ---------- 拉取 EasyTier 源码（带缓存） ----------
if [ ! -d "$CACHE_DIR/.git" ]; then
    mkdir -p "$(dirname "$CACHE_DIR")"
    echo "克隆 EasyTier $EASYTIER_VERSION ..."
    # 容忍并发克隆竞态：SDK 编排脚本并行构建两个架构时，两个实例可能
    # 同时尝试克隆同一目录，失败方在对方克隆成功后可继续。
    git clone --depth 1 --branch "$EASYTIER_VERSION" "$EASYTIER_REPO" "$CACHE_DIR" 2>/dev/null \
        || [ -d "$CACHE_DIR/.git" ] \
        || { echo "错误: 克隆 EasyTier 失败" >&2; exit 1; }
else
    echo "使用缓存源码: $CACHE_DIR（如需更新请删除后重跑）"
fi

# ---------- 交叉编译 easytier-ffi ----------
FFI_DIR="$CACHE_DIR/easytier-contrib/easytier-ffi"
if [ ! -d "$FFI_DIR" ]; then
    echo "错误: 未找到 easytier-ffi 目录: $FFI_DIR" >&2
    exit 1
fi

# 在 easytier 目录内解析 rust-toolchain.toml（可能有固定版本），并把
# Android target 装到该工具链上
(
    cd "$FFI_DIR"
    rustup target add "$RUST_TARGET" 2>/dev/null || { echo "错误: rustup target add $RUST_TARGET 失败" >&2; exit 1; }
)

echo "构建 easytier-ffi（$ABI / $RUST_TARGET）...（首次需编译全部依赖，约 10-20 分钟）"
# 注意：easytier-ffi 的 build.rs 只在 Windows 目标上做 thunk，Android 直接导出全部
# #[no_mangle] extern "C" 符号（parse_config / run_network_instance / set_tun_fd ...），
# 无需额外导出配置；也**不要**在这里启用 easytier-android-jni 的 exports.map。
(
    cd "$FFI_DIR"
    cargo ndk -t "$ABI" build --release -p easytier-ffi
)

# ---------- 复制产物 ----------
SO="$CACHE_DIR/target/$RUST_TARGET/release/libeasytier_ffi.so"
if [ ! -f "$SO" ]; then
    echo "错误: 构建产物不存在: $SO" >&2
    exit 1
fi

mkdir -p "$OUTPUT_DIR"
cp "$SO" "$OUTPUT_DIR/libeasytier_ffi.so"
echo "✅ libeasytier_ffi.so 已复制到: $OUTPUT_DIR/libeasytier_ffi.so"
ls -lh "$OUTPUT_DIR/libeasytier_ffi.so"

# 快速自检：Android bionic 库不应依赖 libc.so.6（桌面 glibc 标志）
if command -v readelf >/dev/null 2>&1; then
    if readelf -d "$OUTPUT_DIR/libeasytier_ffi.so" | grep -q "libc.so.6"; then
        echo "⚠️  警告: 产物依赖 libc.so.6（glibc），不是 Android bionic 库，请检查构建环境！" >&2
        exit 1
    fi
    echo "自检通过: 未发现 glibc 依赖（libc.so.6）"
fi
