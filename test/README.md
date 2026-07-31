# GravityCone FFI 测试应用（Android / 基岩版）

用于测试 GravityCone Android FFI SDK 的测试应用，聚焦**基岩版（PaperConnect）**功能：
创建房间（主机）、加入房间（客人）、房间码校验、状态轮询、引擎日志、VpnService TUN 注入。

## 结构

```
test/
├── settings.gradle.kts / build.gradle.kts / gradle.properties
├── gradlew / gradlew.bat / gradle/wrapper/   （Gradle 8.14.2 wrapper）
└── app/
    ├── build.gradle.kts
    └── src/main/
        ├── AndroidManifest.xml
        ├── java/com/gravitycone/test/
        │   ├── MainActivity.java          # 测试界面 + 状态轮询
        │   └── GravityConeVpnService.java # VpnService 占位（Manifest 声明 VPN 能力）
        └── res/                           # 布局、字符串、主题
```

## FFI SDK 引用方式

**不复制 .so / Java 类**，`app/build.gradle.kts` 直接引用 SDK 产物（相对 `app/` 模块目录）：

| 引用 | 路径 |
|------|------|
| jniLibs（.so） | `../../ffi/android/jniLibs`（arm64-v8a + x86_64） |
| Java API | `../../ffi/android/java/net/gravitycone/ffi/GravityConeAndroidAPI.java` |

重新执行 `task build:android:ffi:sdk` 后，本测试应用自动使用最新产物，无需手动同步。

## 构建

前置条件：JDK 17+、Android SDK（compileSdk 35，需接受 license）。

```bash
# 方式一：Android Studio 直接打开 test/ 目录
# 方式二：命令行
./gradlew :app:assembleDebug
# 产物：app/build/outputs/apk/debug/app-debug.apk
```

安装到设备/模拟器：

```bash
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

> 模拟器请使用 x86_64 镜像（已打包 `x86_64/libeasytier_ffi.so` + `libgravitycone.so`）；
> 真机使用 arm64-v8a。

## 使用步骤（基岩版联机测试）

1. **初始化引擎**：点击「初始化引擎」，顶部显示 GravityCone / EasyTier 版本。
2. **创建房间（主机）**：
   - 输入玩家名（可选，默认 Player）
   - 点击「开始托管（基岩版）」→ 状态流转 host-scanning → host-starting → **host-ok**
   - 记录显示的 `P/XXXX-XXXX-XXXX-XXXX` 房间码（这是基岩版 PaperConnect 房间码）
3. **加入房间（客人，另一台设备）**：
   - 输入房间码 + 玩家名
   - 可先点「校验房间码」确认格式（应提示 PaperConnect（基岩版）✓）
   - 点击「加入房间」→ 状态流转 guest-connecting → **guest-ok**，显示 `url: 127.0.0.1:端口`
   - 在 Minecraft 基岩版中添加服务器：地址 `127.0.0.1`，端口为显示的端口
4. **断开**：点击「断开连接 / 回到空闲」（setWaiting）。
5. **关闭引擎**：点击「关闭引擎」（shutdown）。

## VpnService 说明

- GravityCone 在 Android 上以 `no_tun` 模式（端口转发）运行，但 EasyTier 的
  Android 构建仍需要注入 TUN fd，因此会通过 JNI 回调请求建立 VPN。
- 本应用**自动接受**该请求（不弹窗），建立后引擎日志区可见
  「VPN 已建立，TUN fd=…」。
- Android 14+ 系统可能弹出 VPN 确认对话框，点击「允许」即可。
- 若 30 秒内未能建立 VPN，房间创建/加入会失败并进入 `exception` 状态。

## 界面说明

| 区域 | 说明 |
|------|------|
| 引擎状态 | GravityCone / EasyTier 版本、初始化状态 |
| 当前状态 | 500ms 轮询 `getState()`，友好格式 + 原始 JSON |
| 操作日志 | 应用侧操作记录（初始化、状态变化、VPN 事件等） |
| 引擎日志 | 1s 轮询 `collectLogs()`，即 `application.log` 内容 |

## 故障排查

**`parse_config: dlopen libeasytier_ffi.so faied`**（引擎初始化即失败）：

这是旧版 SDK 打包了**桌面 glibc 库**导致的（`libeasytier_ffi.so` 依赖 `libc.so.6`，
Android bionic 无法加载）。新版 SDK 已改为从 EasyTier 官方源码用 cargo-ndk
交叉编译 Android bionic 版（见 `ffi/android/README.md`）。升级 SDK：
删除本地 `ffi/android/jniLibs/` 下的旧 .so 后，在 WSL/Linux/CI 重新执行
`task build:android:ffi:sdk`（需要 Rust + cargo-ndk + Android NDK，首次约 10-20 分钟），
再重新安装 APK。

**创建/加入房间失败时**：

1. **看界面「当前状态」**：新版 SDK 的 exception 状态带 `error` 字段，
   直接显示失败原因（如 `创建房间失败: TUN fd注入失败: ...`）。
   旧版 SDK 只有 `{"state":"exception","type":0}`，需要升级 FFI 重建 SDK。
2. **看引擎日志区**：引擎日志输出到 `application.log`
   （`/data/data/com.gravitycone.test/files/net.gravitycone.ffi/application.log`），
   界面 1s 刷新一次；日志接线需要新版 SDK。
3. **adb logcat 看 Go 日志**（所有版本可用）：Go 运行时在 Android 上把
   stdout/stderr 输出到 logcat（tag `Go`），带 `et_ffi` 的构建会打印
   `[easytier]`、`[状态错误]` 等关键步骤日志：

   ```bash
   adb logcat -s Go:V
   # 或抓取全量后过滤
   adb logcat | grep -E "easytier|状态错误|ffi"
   ```

4. **VpnService 提示**：no_tun 模式下 EasyTier 仍会请求 TUN fd，因此
   每次创建/加入房间都会触发一次 VPN 建立（Android 14+ 弹出系统确认框，
   点"允许"）。若从未出现该请求，说明 Go 侧在 TUN 注入之前就失败了
   （见日志 2/3），或 `jvmInitialized` 失败（JNI 层，logcat 可见）。

**升级 FFI 生效**：`ffi/` 下的 Go 改动需要重新构建 SDK
（Windows 不支持交叉编译，需在 WSL/Linux/macOS 或 CI 上执行
`task build:android:ffi:sdk`），然后重新安装 APK。
