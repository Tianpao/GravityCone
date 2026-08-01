# GravityCone Android FFI SDK 集成指南

## 概述

GravityCone FFI SDK 允许 Android 应用通过 JNI 调用 GravityCone 的 Minecraft LAN 联机隧道功能。EasyTier P2P 网络引擎以 `libeasytier_ffi.so` 形式在进程内运行，无需启动子进程。

SDK 包含两个 .so 库和一个 Java API 类：

| 文件 | 说明 |
|------|------|
| `libeasytier_ffi.so` | EasyTier P2P 引擎（C ABI，从 [EasyTier 官方源码](https://github.com/EasyTier/EasyTier/tree/main/easytier-contrib/easytier-ffi) 交叉编译，Android bionic 链接） |
| `libgravitycone.so` | GravityCone FFI 层（Go 编译，包含状态机、协议逻辑、JNI 导出） |
| `GravityConeAndroidAPI.java` | Java API 封装（JNI native 方法 + VpnService 回调） |

## 构建 SDK

### 前置条件

- Go 1.22+
- Android NDK（推荐 r26+），设置 `ANDROID_NDK_HOME` 环境变量
- Rust（rustup）+ [cargo-ndk](https://github.com/bbqsrc/cargo-ndk)（`cargo install cargo-ndk`）—— 用于交叉编译 `libeasytier_ffi.so`
- **宿主机要求**：
  - `libeasytier_ffi.so`：**Windows 也可以**（`build_easytier_ffi_android.bat`，cargo-ndk 支持 Windows 宿主交叉编译；NDK 缺失时自动下载 r26d）
  - `libgravitycone.so`（Go 交叉编译）：仅 macOS 或 Linux（Windows 需 WSL 或 CI）
- 能访问 GitHub（源码 clone + crates.io 依赖；国内可配置 `http_proxy`/`https_proxy`）

### 构建命令

```bash
# 一键构建完整 SDK（构建 easytier .so + 编译 + 打包）
task build:android:ffi:sdk
```

此命令会依次执行：

1. **源码构建** `libeasytier_ffi.so`（arm64 + amd64）→ `ffi/android/jniLibs/`
2. **交叉编译** `libgravitycone.so`（arm64 + amd64）→ `ffi/android/jniLibs/`
3. **打包** → `ffi/android/dist/gravitycone-android-sdk.zip`

也可以分步执行：

```bash
# 仅构建 EasyTier FFI .so（源码交叉编译，首次约 10-20 分钟）
task build:android:ffi:download

# 仅编译 libgravitycone.so（依赖已构建的 .so）
task build:android:ffi:compile
```

### libeasytier_ffi.so 来源说明

`libeasytier_ffi.so` 是 EasyTier 仓库中 `easytier-contrib/easytier-ffi`（cdylib crate）的构建产物。
**必须从源码用 cargo-ndk 交叉编译**（`ffi/android/scripts/build_easytier_ffi_android.sh`，
与官方 [easytier-android-jni/build.sh](https://github.com/EasyTier/EasyTier/tree/main/easytier-contrib/easytier-android-jni)
同款方式）——qteasytier/easytier-ffi-bin 发布的 `linux-*` 资产是桌面 glibc 库
（依赖 `libc.so.6`），Android bionic 无法加载，会报
`parse_config: dlopen libeasytier_ffi.so faied`。

源码缓存位于 `ffi/android/.cache/easytier`（可用 `EASYTIER_SRC_DIR` 覆盖），
增量构建复用 rustc 缓存。构建完成后脚本会自检产物不依赖 glibc。

### 产物结构

```
gravitycone-android-sdk.zip
├── jniLibs/
│   ├── arm64-v8a/
│   │   ├── libeasytier_ffi.so
│   │   └── libgravitycone.so
│   └── x86_64/
│       ├── libeasytier_ffi.so
│       └── libgravitycone.so
└── java/
    └── net/gravitycone/ffi/
        └── GravityConeAndroidAPI.java
```

## 集成到 Android 项目

### 1. 复制 .so 文件

将 SDK zip 中的 `jniLibs/` 目录复制到 Android 项目的 `app/src/main/jniLibs/`：

```
app/src/main/jniLibs/
├── arm64-v8a/
│   ├── libeasytier_ffi.so
│   └── libgravitycone.so
└── x86_64/
    ├── libeasytier_ffi.so
    └── libgravitycone.so
```

Android Gradle 会自动将 `jniLibs/` 下的 .so 打包进 APK，**无需额外配置 CMake 或 ndk-build**。

> **注意**：如果你的项目已自定义 `jniLibs` 路径（`android.sourceSets.main.jniLibs.srcDirs`），请将 .so 文件放到对应目录。

### 2. 复制 Java API

将 `GravityConeAndroidAPI.java` 复制到源码目录：

```
app/src/main/java/net/gravitycone/ffi/GravityConeAndroidAPI.java
```

包名 `net.gravitycone.ffi` 必须与 JNI 导出函数名匹配（`Java_net_gravitycone_ffi_GravityConeAndroidAPI_*`），**不可更改**。

### 3. 添加权限

在 `AndroidManifest.xml` 中添加网络权限：

```xml
<uses-permission android:name="android.permission.INTERNET" />
<uses-permission android:name="android.permission.ACCESS_NETWORK_STATE" />
```

**VpnService 权限是必需的**（EasyTier 在 Android 上运行需要注入 TUN fd，见下文「VpnService（TUN 模式）」）：

```xml
<service android:name=".YourVpnService"
         android:permission="android.permission.BIND_VPN_SERVICE">
    <intent-filter>
        <action android:name="android.net.VpnService" />
    </intent-filter>
</service>
```

## API 使用

### 初始化

```java
// 在 Application.onCreate() 或 Activity 中调用
GravityConeAndroidAPI.Metadata meta = GravityConeAndroidAPI.initialize(context, null);
// meta.getGravityconeVersion() → "0.1.3-alpha"
// meta.getEasyTierVersion()    → "v2.6.4"
```

### 状态机

```
Idle ──→ HostScanning ──→ HostStarting ──→ HostReady (创建房间)
 │
 └──→ GuestConnecting ──→ GuestReady     (加入房间)
                ↓
             Error
```

所有状态通过 `getState()` 轮询获取，返回 JSON 字符串。每个状态包含 `index` 字段，递增表示状态变化。

### 创建房间（主机）

```java
// Java Edition (ScaffoldingMC)
GravityConeAndroidAPI.setScanning(null, "Steve", "scaffolding");

// Bedrock Edition (PaperConnect)
GravityConeAndroidAPI.setScanning(null, "Steve", "paperconnect");

// 轮询状态（建议 500ms 间隔）
String state = GravityConeAndroidAPI.getState();
// host-scanning → host-starting → host-ok
// host-ok 示例: {"state":"host-ok","index":3,"protocol":"scaffolding","room":"U/1234-5678-9012-3456","mc_port":25565}
```

### 加入房间（客人）

```java
// 验证房间代码
GravityConeAndroidAPI.RoomType type = GravityConeAndroidAPI.parseRoomCode("U/1234-5678-9012-3456");
// type == RoomType.SCAFFOLDING (Java Edition)
// type == RoomType.PAPER_CONNECT (Bedrock Edition)
// type == null (无效代码)

// 加入房间
boolean ok = GravityConeAndroidAPI.setGuesting("U/1234-5678-9012-3456", "Alex");
// ok == true: 已开始加入（房间码非空且当前没有房间在运行）
// ok == false: 房间码为空，或已有房间/主机会话在进行中

// 轮询状态
String state = GravityConeAndroidAPI.getState();
// guest-connecting → guest-ok
// guest-ok 示例: {"state":"guest-ok","index":5,"protocol":"scaffolding","url":"127.0.0.1:25565"}
```

> **Bedrock 子协议**：`P/` 房间的 `sub_protocol` 为 `nethernet`（默认，优先）或 `raknet`（回退）。
> 两种子协议在 Android 上均受支持：NetherNet 经本机 discovery 监听 + 隧道直连 host 虚拟 IP；
> RakNet 经本地中继监听（`127.0.0.1`）把客户端流量转发到 host，无需端口转发。
> 创建/加入房时自动协商，无需调用方干预。

### 退出房间

```java
GravityConeAndroidAPI.setWaiting();
```

`setWaiting()` 会停止当前房间/加入流程并把状态重置为 `waiting`。若在加入
（`guest-connecting`）或建房（`host-scanning`/`host-starting`）过程中调用，
后台流程会尽快中断；此后迟到的状态写入（包括错误）都会被丢弃，不会把
`waiting` 覆盖回旧房间的 `guest-ok`/`host-ok`/`exception`。轮询到 `waiting`
即可安全地发起下一次会话。

### 关闭引擎

```java
GravityConeAndroidAPI.shutdown();
```

### STUN NAT 探测

```java
// 从运行中的 EasyTier 实例读取内部 STUN 探测结果（无独立 STUN 命令，实例
// 会自行探测 NAT 类型并随网络信息上报）。需先建房/入房让实例运行：
GravityConeAndroidAPI.setScanning(null, "Steve", "paperconnect");
Thread.sleep(5000); // 等待实例启动并完成 STUN 探测
String result = GravityConeAndroidAPI.stunProbe();
// {"udp_nat_type":3,"tcp_nat_type":3,"last_update_time":...,"public_ip":["..."],"min_port":...,"max_port":...}
// 实例未运行或探测未完成时：{"error":"stun probe failed: ..."}
```

NAT 类型数值为 EasyTier proto `NatType`：`0=Unknown`、`1=OpenInternet`、
`2=NoPAT`、`3=FullCone`、`4=Restricted`、`5=PortRestricted`、`6=Symmetric`、
`7=SymUdpFirewall`、`8=SymmetricEasyInc`、`9=SymmetricEasyDec`。

### 获取元数据

```java
String metaJson = GravityConeAndroidAPI.getMetadata();
// {"version":"0.1.3-alpha","compile_time":1720246800000,"easytier_version":"v2.6.4"}
```

## 状态 JSON 格式

| 状态 | JSON 示例 |
|------|-----------|
| 空闲 | `{"state":"waiting","index":0}` |
| 主机扫描中 | `{"state":"host-scanning","index":1}` |
| 主机启动中 | `{"state":"host-starting","index":2,"room":"U/..."}` |
| 主机就绪 (Java) | `{"state":"host-ok","index":3,"protocol":"scaffolding","room":"U/...","mc_port":25565}` |
| 主机就绪 (Bedrock) | `{"state":"host-ok","index":3,"protocol":"paperconnect","sub_protocol":"nethernet","room":"P/...","game_port":45678}` |
| 客机连接中 | `{"state":"guest-connecting","index":4,"room":"P/..."}` |
| 客机就绪 (Java) | `{"state":"guest-ok","index":5,"protocol":"scaffolding","url":"127.0.0.1:25565"}` |
| 客机就绪 (Bedrock) | `{"state":"guest-ok","index":5,"protocol":"paperconnect","sub_protocol":"nethernet","url":"127.0.0.1:45678","connection_state":"ready"}` |
| 错误 | `{"state":"exception","index":6,"type":0,"error":"加入房间失败: ..."}` |

> **客机就绪（Bedrock）的转发桥状态**：`connection_state` 为 `ready` / `error` /
> `disconnected`，`connection_error`（建立失败原因）和 `disconnect_reason`
> （房主断开等退出原因）按需出现。桥的建立发生在 `guest-ok` 状态上报之后，
> 需要持续轮询 `getState()` 才能看到这些字段变化。

## 房间代码格式

| 前缀 | 协议 | 版本 | parseRoomCode 返回值 |
|------|------|------|---------------------|
| `U/` | ScaffoldingMC | Java Edition | `RoomType.SCAFFOLDING` (3) |
| `P/` | PaperConnect | Bedrock Edition | `RoomType.PAPER_CONNECT` (4) |

## VpnService（TUN 模式）

GravityCone 使用 `no_tun` 模式（端口转发），但 EasyTier 的 Android 构建仍需要通过 VpnService 注入 TUN 文件描述符。

**集成方必须提供 `VpnServiceCallback` 实现**，不能传 `null`。流程如下：

1. 传入 `VpnServiceCallback` 实现
2. 当 EasyTier 需要网络接口时，回调的 `onStartVpnService()` 会被调用
3. 在回调中调用 `GravityConeAndroidAPI.getPendingVpnServiceRequest()` 获取请求
4. 在 30 秒内调用 `startVpnService()` 或 `reject()`

如果 VpnService 请求超时或被拒绝，房间创建/加入将失败并进入错误状态。

## 线程安全

- 所有 `GravityConeAndroidAPI` 的公共方法都是线程安全的
- `getState()` 设计为轮询调用（建议 500ms 间隔）
- `stunProbe()` 是阻塞调用（3-10 秒），请在后台线程调用
- 同一时刻只允许一个房间/加入会话：`setScanning`/`setGuesting` 的
  「检查空闲 + 转移状态」是原子的，并发调用中只有一个会生效，其余静默失败
  （`setGuesting` 返回 `false`）；任何会话都可用 `setWaiting()` 中断

## 日志

```java
// 收集引擎日志
Reader logs = GravityConeAndroidAPI.collectLogs();
// 读取后必须立即关闭 Reader
```

## 常见问题

### Q: 集成方需要手动下载 EasyTier .so 吗？

**不需要**。`libeasytier_ffi.so` 已包含在 SDK zip 中。构建 SDK 时由
`ffi/android/scripts/build_easytier_ffi_android.sh` 自动从 EasyTier 源码交叉编译。

### Q: 需要配置 CMake 或 ndk-build 吗？

**不需要**。.so 是预编译的，放入 `jniLibs/` 目录即可，Gradle 自动打包。

### Q: .so 加载顺序需要关心吗？

**不需要**。`GravityConeAndroidAPI.java` 的 static 初始化块已按正确顺序加载：先 `libeasytier_ffi`，后 `libgravitycone`（因为后者依赖前者的符号）。

### Q: 可以修改 Java 类的包名吗？

**不可以**。JNI 导出函数名包含完整包名（`Java_net_gravitycone_ffi_GravityConeAndroidAPI_*`），包名必须匹配。

### Q: Windows 上能构建 SDK 吗？

当前 `Taskfile.yml` 的交叉编译脚本不支持 Windows 宿主机。请使用 WSL、macOS 或 Linux。

## 鸣谢

GravityCone FFI 的 Android 集成设计参考了 [Terracotta](https://github.com/burningtnt/Terracotta) 项目，包括：

- **状态机架构**：`Idle → HostScanning → HostStarting → HostReady` / `GuestConnecting → GuestReady` 的状态流转与 JSON 轮询模式
- **JNI API 设计**：`initialize` / `getState` / `setScanning` / `setGuesting` / `setWaiting` 的函数签名与调用约定
- **VpnService 回调模式**：`VpnServiceCallback` + `VpnServiceRequest` 的异步 TUN fd 握手机制
- **Android 日志收集**：基于 `RandomAccessFile` 的日志 Reader 实现

Terracotta 是一个 Rust 编写的 Minecraft LAN 联机工具，GravityCone 在 Go 语言中重新实现了相同的 FFI 设计模式。
