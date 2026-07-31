@echo off
setlocal
title GravityCone - build Android FFI SDK (Windows)

rem =====================================================================
rem  build_android_sdk.bat - one-shot Android FFI SDK build on Windows:
rem    step 1: libeasytier_ffi.so   (EasyTier source, cargo-ndk)
rem    step 2: libgravitycone.so    (GravityCone Go cgo, NDK clang)
rem    step 3: package gravitycone-android-sdk.zip
rem
rem  Prerequisites: Rust(rustup) + cargo-ndk + protoc (auto-installed by
rem  step 1), Go 1.22+, Android NDK (auto-detected / auto-downloaded).
rem  Output: <repo>\ffi\android\dist\gravitycone-android-sdk.zip
rem =====================================================================

set "SCRIPT_DIR=%~dp0"
for %%I in ("%SCRIPT_DIR%..\..\..") do set "REPO_ROOT=%%~fI"

echo ============================================================
echo  Step 1/3: libeasytier_ffi.so (EasyTier source, cargo-ndk)
echo ============================================================
call "%SCRIPT_DIR%build_easytier_ffi_android.bat"
if errorlevel 1 (echo *** SDK build FAILED at step 1 & exit /b 1)

echo.
echo ============================================================
echo  Step 2/3: libgravitycone.so (GravityCone, Go cross-compile)
echo ============================================================
call "%SCRIPT_DIR%build_gravitycone_android.bat"
if errorlevel 1 (echo *** SDK build FAILED at step 2 & exit /b 1)

echo.
echo ============================================================
echo  Step 3/3: Packaging gravitycone-android-sdk.zip
echo ============================================================
pushd "%REPO_ROOT%\ffi\android"
mkdir dist 2>nul
del /q dist\gravitycone-android-sdk.zip 2>nul
rem NOTE: Windows tar (libarchive) treats "\" as an escape char in paths,
rem so use forward slashes here.
tar -a -c -f dist\gravitycone-android-sdk.zip ^
  jniLibs/arm64-v8a/libeasytier_ffi.so ^
  jniLibs/arm64-v8a/libgravitycone.so ^
  jniLibs/arm64-v8a/libgravitycone.h ^
  jniLibs/x86_64/libeasytier_ffi.so ^
  jniLibs/x86_64/libgravitycone.so ^
  jniLibs/x86_64/libgravitycone.h ^
  java/net/gravitycone/ffi/GravityConeAndroidAPI.java
if errorlevel 1 (echo *** ERROR: zip packaging failed & popd & exit /b 1)
popd

echo.
echo ============================================================
echo  DONE. SDK package:
echo    %REPO_ROOT%\ffi\android\dist\gravitycone-android-sdk.zip
echo  Next: rebuild the test APK
echo    cd test ^&^& gradlew.bat :app:assembleDebug
echo ============================================================
exit /b 0
