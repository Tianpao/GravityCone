@echo off
rem =====================================================================
rem  find_ndk.bat - locate the Android NDK on Windows and make it visible
rem  to the build tools (cargo-ndk / Go cgo).
rem
rem  Search order: env vars (ANDROID_NDK_HOME / ANDROID_NDK_ROOT / NDK_HOME)
rem  -> common paths -> auto-download to %USERPROFILE%.
rem
rem  Usage:  call find_ndk.bat
rem  Sets:   NDK_DIR (full path), NDK_CLANG (clang.exe path), and exports
rem          ANDROID_NDK_HOME / ANDROID_NDK_ROOT / NDK_HOME.
rem  Returns exit code 1 if no NDK could be found (caller must check
rem  errorlevel after the call).
rem =====================================================================

set "NDK_VERSION=r26d"

rem ---- env vars first; invalid values (empty dirs) are ignored ----
set "NDK_DIR=%ANDROID_NDK_HOME%"
if not defined NDK_DIR set "NDK_DIR=%ANDROID_NDK_ROOT%"
if not defined NDK_DIR set "NDK_DIR=%NDK_HOME%"
set "NDK_CLANG=%NDK_DIR%\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe"
if not exist "%NDK_CLANG%" set "NDK_DIR="

rem ---- common paths ----
if not defined NDK_DIR if exist "%USERPROFILE%\android-ndk-%NDK_VERSION%\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe" set "NDK_DIR=%USERPROFILE%\android-ndk-%NDK_VERSION%"
if not defined NDK_DIR (
    for /d %%D in ("%LOCALAPPDATA%\Android\Sdk\ndk\*") do (
        if exist "%%D\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe" set "NDK_DIR=%%D"
    )
)
set "NDK_CLANG=%NDK_DIR%\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe"

rem ---- auto-download if still missing ----
if not exist "%NDK_CLANG%" (
    echo [!] Android NDK not found anywhere - downloading
    echo     android-ndk-%NDK_VERSION%-windows.zip ^(~500 MB^) to %USERPROFILE%...
    curl -fSL -o "%TEMP%\android-ndk-%NDK_VERSION%-windows.zip" "https://dl.google.com/android/repository/android-ndk-%NDK_VERSION%-windows.zip"
    if errorlevel 1 exit /b 1
    echo     Extracting ^(takes a few minutes^)...
    tar -xf "%TEMP%\android-ndk-%NDK_VERSION%-windows.zip" -C "%USERPROFILE%"
    if errorlevel 1 exit /b 1
    del "%TEMP%\android-ndk-%NDK_VERSION%-windows.zip"
    set "NDK_DIR=%USERPROFILE%\android-ndk-%NDK_VERSION%"
    set "NDK_CLANG=%NDK_DIR%\toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe"
    if not exist "%NDK_CLANG%" (echo *** ERROR: NDK extraction incomplete & exit /b 1)
)

echo [!] Using NDK: %NDK_DIR%

rem Make the detected NDK visible to cargo-ndk / Go cgo, which read these
rem env vars themselves. Overwrites invalid values (e.g. NDK_HOME pointing
rem to an empty dir).
set "ANDROID_NDK_HOME=%NDK_DIR%"
set "ANDROID_NDK_ROOT=%NDK_DIR%"
set "NDK_HOME=%NDK_DIR%"

exit /b 0
