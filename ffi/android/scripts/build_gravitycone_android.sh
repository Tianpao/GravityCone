#!/usr/bin/env bash
#
# build_gravitycone_android.sh — 交叉编译 libgravitycone.so（Go + cgo/JNI）
# 到 Android（arm64-v8a + x86_64）。与 build_gravitycone_android.bat 对称。
#
# 前置条件：
#   - Go 1.22+
#   - Android NDK r26+（自动探测：环境变量 → 常见路径 → 自动下载；macOS 需自行安装）
#   - Linux/macOS（Windows 请用同目录下的 .bat，或 WSL 里跑本脚本）
#
# 用法：./build_gravitycone_android.sh
# 产物：<repo>/ffi/android/jniLibs/{arm64-v8a,x86_64}/libgravitycone.so
#       （含 c-shared 生成的 libgravitycone.h，SDK 打包用）
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
JNILIBS="$REPO_ROOT/ffi/android/jniLibs"
MIN_SDK=21
source "$SCRIPT_DIR/build_common.sh"

# ---------- Windows 原生 shell 检测（Git Bash / MSYS） ----------
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        echo "错误: 请在 WSL/Linux/macOS 运行本脚本，Windows 原生 shell 请用 build_gravitycone_android.bat" >&2
        exit 1
        ;;
esac

# ---------- [1/3] 定位 NDK（环境变量 → 常见路径 → 自动下载） ----------
locate_ndk

# ---------- Go 检查 ----------
command -v go >/dev/null 2>&1 || { echo "*** 错误: 未找到 go，请从 https://go.dev/dl 安装" >&2; exit 1; }

TOOLCHAIN="$NDK_DIR/toolchains/llvm/prebuilt/$HOST_TAG"

# ---------- [2/3] 构建 arm64-v8a + x86_64（并行；子 shell 隔离环境变量） ----------
build_arch() {
    local arch="$1" abi="$2" cc_target="$3"
    echo "[2/3] 构建 libgravitycone.so（$abi，Go cgo 交叉编译）..."
    export GOOS=android
    export GOARCH="$arch"
    export CGO_ENABLED=1
    export CC="$TOOLCHAIN/bin/clang --target=$cc_target"
    export AR="$TOOLCHAIN/bin/llvm-ar"
    mkdir -p "$JNILIBS/$abi"
    (
        cd "$REPO_ROOT"
        go build -tags et_ffi -buildmode=c-shared -trimpath \
            -ldflags="-w -s" \
            -o "ffi/android/jniLibs/$abi/libgravitycone.so" ./ffi/common/
    )
}
build_arch arm64 arm64-v8a "aarch64-linux-android$MIN_SDK" &
build_arch amd64 x86_64 "x86_64-linux-android$MIN_SDK" &
wait

echo
echo "============================================================"
echo "  完成。libgravitycone.so 已安装到:"
echo "    $JNILIBS/arm64-v8a/libgravitycone.so"
echo "    $JNILIBS/x86_64/libgravitycone.so"
echo "  下一步：重新构建测试 APK"
echo "    cd test && ./gradlew :app:assembleDebug"
echo "============================================================"
