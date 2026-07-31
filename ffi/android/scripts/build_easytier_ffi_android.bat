@echo off
setlocal enabledelayedexpansion
title GravityCone - build libeasytier_ffi.so for Android

rem =====================================================================
rem  build_easytier_ffi_android.bat
rem
rem  Cross-compiles libeasytier_ffi.so for Android (arm64-v8a + x86_64)
rem  from the EasyTier source tree on a Windows host (cargo-ndk).
rem
rem  WHY: qteasytier/easytier-ffi-bin only ships glibc desktop builds,
rem  which Android cannot dlopen ("parse_config: dlopen libeasytier_ffi.so
rem  faied"). The Android .so must be built from source via cargo-ndk,
rem  same as the official easytier-contrib/easytier-android-jni build.sh.
rem
rem  Prerequisites:
rem    - Rust via rustup (https://rustup.rs), stable toolchain
rem    - git, curl (bundled with Windows 10+)
rem    - Android NDK r26d -- auto-downloaded to %USERPROFILE% if missing
rem    - cargo-ndk -- auto-installed via "cargo install cargo-ndk" if missing
rem
rem  Network note: if GitHub / crates.io / dl.google.com are blocked,
rem  set a proxy in THIS console first, then run:
rem      set http_proxy=http://127.0.0.1:7890
rem      set https_proxy=http://127.0.0.1:7890
rem
rem  Usage:  build_easytier_ffi_android.bat
rem  Output: <repo>\ffi\android\jniLibs\{arm64-v8a,x86_64}\libeasytier_ffi.so
rem =====================================================================

set "SCRIPT_DIR=%~dp0"
for %%I in ("%SCRIPT_DIR%..\..\..") do set "REPO_ROOT=%%~fI"

set "JNILIBS=%REPO_ROOT%\ffi\android\jniLibs"
set "CACHE_DIR=%REPO_ROOT%\ffi\android\.cache\easytier"
set "FFI_DIR=%CACHE_DIR%\easytier-contrib\easytier-ffi"
set "EASYTIER_VERSION=v2.6.4"
set "EASYTIER_REPO=https://github.com/EasyTier/EasyTier.git"

set "NDK_VERSION=r26d"
set "NDK_DIR=%ANDROID_NDK_HOME%"
if not defined NDK_DIR set "NDK_DIR=%ANDROID_NDK_ROOT%"
if not defined NDK_DIR set "NDK_DIR=%NDK_HOME%"
set "NDK_CLANG=%NDK_DIR%\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe"
if not exist "%NDK_CLANG%" set "NDK_DIR="
if not defined NDK_DIR if exist "E:\NDK\windows\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe" set "NDK_DIR=E:\NDK\windows"
if not defined NDK_DIR if exist "%USERPROFILE%\android-ndk-%NDK_VERSION%\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe" set "NDK_DIR=%USERPROFILE%\android-ndk-%NDK_VERSION%"
if not defined NDK_DIR (
    for /d %%D in ("%LOCALAPPDATA%\Android\Sdk\ndk\*") do (
        if exist "%%D\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe" set "NDK_DIR=%%D"
    )
)
set "NDK_CLANG=%NDK_DIR%\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe"

if not defined http_proxy (
    echo [info] http_proxy is not set. If GitHub / crates.io / dl.google.com are
    echo        blocked, abort now, run the two "set ...proxy" lines above, then re-run.
    echo.
)

rem ---------------- toolchain checks ----------------
where git >nul 2>&1
if errorlevel 1 (echo *** ERROR: git not found & goto :error)
where curl >nul 2>&1
if errorlevel 1 (echo *** ERROR: curl not found & goto :error)
where rustup >nul 2>&1
if errorlevel 1 (echo *** ERROR: rustup not found - install from https://rustup.rs & goto :error)
where cargo >nul 2>&1
if errorlevel 1 (echo *** ERROR: cargo not found & goto :error)

rem ---------------- [1/6] NDK ----------------
rem Locate NDK: env vars first, then common paths, then auto-download.
rem Invalid env vars (e.g. NDK_HOME pointing to an empty dir) are ignored.
if not exist "%NDK_CLANG%" (
    echo [1/6] Android NDK not found anywhere - downloading
    echo        android-ndk-%NDK_VERSION%-windows.zip ^(~500 MB^) to %USERPROFILE%...
    curl -fSL -o "%TEMP%\android-ndk-%NDK_VERSION%-windows.zip" "https://dl.google.com/android/repository/android-ndk-%NDK_VERSION%-windows.zip"
    if errorlevel 1 goto :error
    echo        Extracting ^(takes a few minutes^)...
    tar -xf "%TEMP%\android-ndk-%NDK_VERSION%-windows.zip" -C "%USERPROFILE%"
    if errorlevel 1 goto :error
    del "%TEMP%\android-ndk-%NDK_VERSION%-windows.zip"
    set "NDK_DIR=%USERPROFILE%\android-ndk-%NDK_VERSION%"
    set "NDK_CLANG=%NDK_DIR%\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe"
    if not exist "%NDK_CLANG%" (echo *** ERROR: NDK extraction incomplete & goto :error)
)
echo [1/6] Using NDK: %NDK_DIR%

rem Make the detected NDK visible to cargo-ndk, which reads these env vars
rem itself. Overwrites invalid values (e.g. NDK_HOME pointing to an empty dir).
set "ANDROID_NDK_HOME=%NDK_DIR%"
set "ANDROID_NDK_ROOT=%NDK_DIR%"
set "NDK_HOME=%NDK_DIR%"

rem ---------------- [1/6] protoc (required by prost-build) ----------------
set "PROTOC_DIR=%REPO_ROOT%\ffi\android\.cache\protoc"
set "PROTOC_BIN=%PROTOC_DIR%\bin\protoc.exe"
where protoc >nul 2>&1
if errorlevel 1 (
    if not exist "%PROTOC_BIN%" (
        echo [1/6] protoc not found - downloading to %PROTOC_DIR% ...
        mkdir "%PROTOC_DIR%" 2>nul
        curl -fSL -o "%TEMP%\protoc.zip" "https://github.com/protocolbuffers/protobuf/releases/download/v29.3/protoc-29.3-win64.zip"
        if errorlevel 1 goto :error
        tar -xf "%TEMP%\protoc.zip" -C "%PROTOC_DIR%"
        if errorlevel 1 goto :error
        del "%TEMP%\protoc.zip"
        if not exist "%PROTOC_BIN%" (echo *** ERROR: protoc extraction incomplete & goto :error)
    )
    set "PROTOC=!PROTOC_BIN!"
    echo [1/6] Using protoc: !PROTOC!
)

rem ---------------- [1/6] cargo-ndk ----------------
cargo ndk --version >nul 2>&1
if errorlevel 1 (
    echo [1/6] cargo-ndk missing - installing via cargo ^(~2 min^)...
    call cargo install cargo-ndk
    if errorlevel 1 goto :error
)

rem ---------------- [2/6] EasyTier source ----------------
if not exist "%CACHE_DIR%\.git" (
    echo [2/6] Cloning EasyTier %EASYTIER_VERSION% ...
    mkdir "%CACHE_DIR%" 2>nul
    git clone --depth 1 --branch %EASYTIER_VERSION% %EASYTIER_REPO% "%CACHE_DIR%"
    if errorlevel 1 goto :error
) else (
    echo [2/6] Using cached source: %CACHE_DIR%
)
if not exist "%FFI_DIR%" (echo *** ERROR: easytier-ffi dir missing in clone & goto :error)

rem ---------------- [3/6] Rust Android targets ----------------
rem Run inside the easytier tree so rust-toolchain.toml (pinned version)
rem is honored and the targets land on the right toolchain.
echo [3/6] Installing Rust Android targets ...
pushd "%FFI_DIR%"
call rustup target add aarch64-linux-android x86_64-linux-android
if errorlevel 1 (popd & goto :error)
popd

rem ---------------- [4/6] Build arm64-v8a ----------------
echo [4/6] Building libeasytier_ffi.so ^(arm64-v8a^) - first build compiles all
echo        dependencies, ~10-20 min...
pushd "%FFI_DIR%"
call cargo ndk -t arm64-v8a build --release -p easytier-ffi
if errorlevel 1 (popd & goto :error)
popd

rem ---------------- [5/6] Build x86_64 ----------------
echo [5/6] Building libeasytier_ffi.so ^(x86_64^) ...
pushd "%FFI_DIR%"
call cargo ndk -t x86_64 build --release -p easytier-ffi
if errorlevel 1 (popd & goto :error)
popd

rem ---------------- [6/6] Copy artifacts ----------------
echo [6/6] Copying artifacts to %JNILIBS% ...
mkdir "%JNILIBS%\arm64-v8a" 2>nul
mkdir "%JNILIBS%\x86_64" 2>nul
copy /y "%CACHE_DIR%\target\aarch64-linux-android\release\libeasytier_ffi.so" "%JNILIBS%\arm64-v8a\libeasytier_ffi.so" >nul
if errorlevel 1 (echo *** ERROR: copy arm64 failed & goto :error)
copy /y "%CACHE_DIR%\target\x86_64-linux-android\release\libeasytier_ffi.so" "%JNILIBS%\x86_64\libeasytier_ffi.so" >nul
if errorlevel 1 (echo *** ERROR: copy x86_64 failed & goto :error)

rem ---------------- sanity check: bionic must not depend on glibc ----------------
set "READELF=%NDK_DIR%\toolchains\llvm\prebuilt\windows-x86_64\bin\llvm-readelf.exe"
if exist "%READELF%" (
    "%READELF%" -d "%JNILIBS%\arm64-v8a\libeasytier_ffi.so" | findstr /C:"libc.so.6" >nul && (
        echo *** WARNING: arm64-v8a lib depends on libc.so.6 - this is a glibc desktop build!
        goto :error
    )
    "%READELF%" -d "%JNILIBS%\x86_64\libeasytier_ffi.so" | findstr /C:"libc.so.6" >nul && (
        echo *** WARNING: x86_64 lib depends on libc.so.6 - this is a glibc desktop build!
        goto :error
    )
    echo        Sanity check passed: no glibc ^(libc.so.6^) dependency found.
)

echo.
echo ============================================================
echo  DONE. Android bionic libeasytier_ffi.so installed to:
echo    %JNILIBS%\arm64-v8a\libeasytier_ffi.so
echo    %JNILIBS%\x86_64\libeasytier_ffi.so
echo.
echo  Next: run build_gravitycone_android.bat ^(or build_android_sdk.bat
echo  for the full SDK + zip package^), then rebuild the test APK:
echo    cd test ^&^& gradlew.bat :app:assembleDebug
echo ============================================================
exit /b 0

:error
echo.
echo *** FAILED. See messages above.
exit /b 1
