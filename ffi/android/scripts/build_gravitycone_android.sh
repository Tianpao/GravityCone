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
NDK_VERSION=r26d

# ---------- Windows 原生 shell 检测（Git Bash / MSYS） ----------
case "$(uname -s)" in
    MINGW*|MSYS*|CYGWIN*)
        echo "错误: 请在 WSL/Linux/macOS 运行本脚本，Windows 原生 shell 请用 build_gravitycone_android.bat" >&2
        exit 1
        ;;
esac

# ---------- 宿主标签（NDK 工具链目录名） ----------
case "$(uname -s)" in
    Darwin) HOST_TAG="darwin-$(uname -m)" ;;   # darwin-arm64 / darwin-x86_64
    *)      HOST_TAG="linux-x86_64" ;;
esac

# ---------- [1/3] 定位 NDK：环境变量 → 常见路径 → 自动下载 ----------
NDK_DIR="${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-${NDK_HOME:-}}}"
if [ -n "$NDK_DIR" ] && [ ! -x "$NDK_DIR/toolchains/llvm/prebuilt/$HOST_TAG/bin/clang" ]; then
    echo "[1/3] 警告: $NDK_DIR 不是有效的 NDK（$HOST_TAG 工具链缺失），忽略该环境变量" >&2
    NDK_DIR=""
fi

if [ -z "$NDK_DIR" ]; then
    for cand in \
        "$HOME/android-ndk-$NDK_VERSION" \
        /usr/local/lib/android-ndk \
        /usr/local/android-ndk \
        "$HOME/Android/Sdk/ndk"/* \
        "$HOME/Library/Android/sdk/ndk"/* ; do
        if [ -x "$cand/toolchains/llvm/prebuilt/$HOST_TAG/bin/clang" ]; then
            NDK_DIR="$cand"
            break
        fi
    done
fi

# 自动下载仅 Linux（macOS 请自行安装：brew install --cask android-ndk 或手动）
if [ -z "$NDK_DIR" ] && [ "$(uname -s)" = "Linux" ]; then
    echo "[1/3] 未找到 NDK，正在下载 android-ndk-$NDK_VERSION-linux.zip（约 500 MB）到 $HOME ..."
    curl -fSL -o /tmp/android-ndk-$NDK_VERSION-linux.zip "https://dl.google.com/android/repository/android-ndk-$NDK_VERSION-linux.zip"
    unzip -q /tmp/android-ndk-$NDK_VERSION-linux.zip -d "$HOME"
    rm /tmp/android-ndk-$NDK_VERSION-linux.zip
    NDK_DIR="$HOME/android-ndk-$NDK_VERSION"
fi

if [ -z "$NDK_DIR" ] || [ ! -x "$NDK_DIR/toolchains/llvm/prebuilt/$HOST_TAG/bin/clang" ]; then
    echo "*** 错误: 未找到 Android NDK。请安装 NDK r26+ 或设置 ANDROID_NDK_HOME" >&2
    exit 1
fi
echo "[1/3] 使用 NDK: $NDK_DIR"

# 覆盖环境变量（供 Go cgo 及下游工具读取，忽略无效旧值）
export ANDROID_NDK_HOME="$NDK_DIR"
export ANDROID_NDK_ROOT="$NDK_DIR"
export NDK_HOME="$NDK_DIR"

# ---------- Go 检查 ----------
command -v go >/dev/null 2>&1 || { echo "*** 错误: 未找到 go，请从 https://go.dev/dl 安装" >&2; exit 1; }

TOOLCHAIN="$NDK_DIR/toolchains/llvm/prebuilt/$HOST_TAG"

# ---------- [2/3] 构建 arm64-v8a ----------
echo "[2/3] 构建 libgravitycone.so（arm64-v8a，Go cgo 交叉编译）..."
export GOOS=android
export GOARCH=arm64
export CGO_ENABLED=1
export CC="$TOOLCHAIN/bin/clang --target=aarch64-linux-android$MIN_SDK"
export AR="$TOOLCHAIN/bin/llvm-ar"
mkdir -p "$JNILIBS/arm64-v8a"
(
    cd "$REPO_ROOT"
    go build -tags et_ffi -buildmode=c-shared -trimpath \
        -ldflags="-w -s" \
        -o ffi/android/jniLibs/arm64-v8a/libgravitycone.so ./ffi/common/
)

# ---------- [3/3] 构建 x86_64 ----------
echo "[3/3] 构建 libgravitycone.so（x86_64，Go cgo 交叉编译）..."
export GOARCH=amd64
export CC="$TOOLCHAIN/bin/clang --target=x86_64-linux-android$MIN_SDK"
mkdir -p "$JNILIBS/x86_64"
(
    cd "$REPO_ROOT"
    go build -tags et_ffi -buildmode=c-shared -trimpath \
        -ldflags="-w -s" \
        -o ffi/android/jniLibs/x86_64/libgravitycone.so ./ffi/common/
)

echo
echo "============================================================"
echo "  完成。libgravitycone.so 已安装到:"
echo "    $JNILIBS/arm64-v8a/libgravitycone.so"
echo "    $JNILIBS/x86_64/libgravitycone.so"
echo "  下一步：重新构建测试 APK"
echo "    cd test && ./gradlew :app:assembleDebug"
echo "============================================================"
