@echo off
setlocal enabledelayedexpansion
title GravityCone - build libgravitycone.so for Android

rem =====================================================================
rem  build_gravitycone_android.bat
rem
rem  Cross-compiles libgravitycone.so (Go + cgo/JNI) for Android
rem  (arm64-v8a + x86_64) on a Windows host using the NDK's clang as
rem  the cgo cross compiler.
rem
rem  Prerequisites:
rem    - Go 1.22+ (https://go.dev/dl)
rem    - git, curl (bundled with Windows 10+)
rem    - Android NDK r26+ -- auto-detected (env vars / common paths /
rem      E:\NDK\windows), auto-downloaded to %USERPROFILE% if missing
rem
rem  Output: <repo>\ffi\android\jniLibs\{arm64-v8a,x86_64}\libgravitycone.so
rem          (+ generated libgravitycone.h for SDK packaging)
rem =====================================================================

set "SCRIPT_DIR=%~dp0"
for %%I in ("%SCRIPT_DIR%..\..\..") do set "REPO_ROOT=%%~fI"
set "JNILIBS=%REPO_ROOT%\ffi\android\jniLibs"
set "MIN_SDK=21"

rem ---------------- [1/3] NDK ----------------
rem Locate NDK: env vars first, then common paths, then auto-download.
rem Invalid env vars (e.g. NDK_HOME pointing to an empty dir) are ignored.
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

if not exist "%NDK_CLANG%" (
    echo [1/3] Android NDK not found anywhere - downloading
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
echo [1/3] Using NDK: %NDK_DIR%

rem Make the detected NDK visible to Go's cgo and any tooling reading these
rem env vars. Overwrites invalid values (e.g. NDK_HOME pointing to an empty dir).
set "ANDROID_NDK_HOME=%NDK_DIR%"
set "ANDROID_NDK_ROOT=%NDK_DIR%"
set "NDK_HOME=%NDK_DIR%"

rem ---------------- Go check ----------------
where go >nul 2>&1
if errorlevel 1 (echo *** ERROR: go not found - install from https://go.dev/dl & goto :error)

set "TOOLCHAIN=%NDK_DIR%\toolchains\llvm\prebuilt\windows-x86_64"

rem ---------------- [2/3] Build arm64-v8a ----------------
echo [2/3] Building libgravitycone.so ^(arm64-v8a, Go cgo cross-compile^) ...
set "GOOS=android"
set "GOARCH=arm64"
set "CGO_ENABLED=1"
set "CC=%TOOLCHAIN%\bin\clang.exe --target=aarch64-linux-android%MIN_SDK%"
set "AR=%TOOLCHAIN%\bin\llvm-ar.exe"
mkdir "%JNILIBS%\arm64-v8a" 2>nul
pushd "%REPO_ROOT%"
go build -tags et_ffi -buildmode=c-shared -trimpath -ldflags="-w -s" -o ffi\android\jniLibs\arm64-v8a\libgravitycone.so ./ffi/common/
if errorlevel 1 (popd & goto :error)
popd

rem ---------------- [3/3] Build x86_64 ----------------
echo [3/3] Building libgravitycone.so ^(x86_64, Go cgo cross-compile^) ...
set "GOARCH=amd64"
set "CC=%TOOLCHAIN%\bin\clang.exe --target=x86_64-linux-android%MIN_SDK%"
mkdir "%JNILIBS%\x86_64" 2>nul
pushd "%REPO_ROOT%"
go build -tags et_ffi -buildmode=c-shared -trimpath -ldflags="-w -s" -o ffi\android\jniLibs\x86_64\libgravitycone.so ./ffi/common/
if errorlevel 1 (popd & goto :error)
popd

echo.
echo ============================================================
echo  DONE. libgravitycone.so installed to:
echo    %JNILIBS%\arm64-v8a\libgravitycone.so
echo    %JNILIBS%\x86_64\libgravitycone.so
echo  Next: rebuild the test APK
echo    cd test ^&^& gradlew.bat :app:assembleDebug
echo ============================================================
exit /b 0

:error
echo.
echo *** FAILED. See messages above.
exit /b 1
