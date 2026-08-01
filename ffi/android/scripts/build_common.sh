#!/usr/bin/env bash
#
# build_common.sh — shared helpers for the Android FFI build scripts
# (build_easytier_ffi_android.sh / build_gravitycone_android.sh).
#
# Source from a sibling script:
#   SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
#   source "$SCRIPT_DIR/build_common.sh"

# NDK version used when auto-downloading.
NDK_VERSION=r26d

# NDK toolchain host tag: darwin-arm64 / darwin-x86_64 / linux-x86_64.
case "$(uname -s)" in
    Darwin) HOST_TAG="darwin-$(uname -m)" ;;
    *)      HOST_TAG="linux-x86_64" ;;
esac

# locate_ndk resolves NDK_DIR via env vars → common paths → auto-download,
# then exports ANDROID_NDK_HOME / ANDROID_NDK_ROOT / NDK_HOME so downstream
# tools (Go cgo, cargo-ndk) see the resolved value.
# Exits the script with an error message if no NDK could be found.
locate_ndk() {
    local ndk_dir="${ANDROID_NDK_HOME:-${ANDROID_NDK_ROOT:-${NDK_HOME:-}}}"
    if [ -n "$ndk_dir" ] && [ ! -x "$ndk_dir/toolchains/llvm/prebuilt/$HOST_TAG/bin/clang" ]; then
        echo "警告: $ndk_dir 不是有效的 NDK（$HOST_TAG 工具链缺失），忽略该环境变量" >&2
        ndk_dir=""
    fi

    if [ -z "$ndk_dir" ]; then
        for cand in \
            "$HOME/android-ndk-$NDK_VERSION" \
            /usr/local/lib/android-ndk \
            /usr/local/android-ndk \
            "$HOME/Android/Sdk/ndk"/* \
            "$HOME/Library/Android/sdk/ndk"/* ; do
            if [ -x "$cand/toolchains/llvm/prebuilt/$HOST_TAG/bin/clang" ]; then
                ndk_dir="$cand"
                break
            fi
        done
    fi

    # Auto-download only on Linux (macOS: brew install --cask android-ndk or manual).
    if [ -z "$ndk_dir" ] && [ "$(uname -s)" = "Linux" ]; then
        echo "未找到 NDK，正在下载 android-ndk-$NDK_VERSION-linux.zip（约 500 MB）到 $HOME ..."
        curl -fSL -o /tmp/android-ndk-$NDK_VERSION-linux.zip "https://dl.google.com/android/repository/android-ndk-$NDK_VERSION-linux.zip"
        unzip -q /tmp/android-ndk-$NDK_VERSION-linux.zip -d "$HOME"
        rm /tmp/android-ndk-$NDK_VERSION-linux.zip
        ndk_dir="$HOME/android-ndk-$NDK_VERSION"
    fi

    if [ -z "$ndk_dir" ] || [ ! -x "$ndk_dir/toolchains/llvm/prebuilt/$HOST_TAG/bin/clang" ]; then
        echo "*** 错误: 未找到 Android NDK。请安装 NDK r26+ 或设置 ANDROID_NDK_HOME" >&2
        exit 1
    fi
    echo "使用 NDK: $ndk_dir"

    NDK_DIR="$ndk_dir"
    # Override the env vars (ignoring stale invalid values) for downstream tools.
    export ANDROID_NDK_HOME="$NDK_DIR"
    export ANDROID_NDK_ROOT="$NDK_DIR"
    export NDK_HOME="$NDK_DIR"
}
