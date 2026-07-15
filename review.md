# GravityCone 项目代码审查报告

审查日期：2026-07-15

## 总体评价

GravityCone 是一个架构清晰、功能完整的 Minecraft LAN 联机工具。Go 后端与 Vue 3 前端的分离设计合理，build tags 实现 GUI/CLI 双模式是亮点。但存在若干安全性、错误处理和代码质量问题需要关注。

---

## 1. 安全漏洞

### 1.1 硬编码 OAuth Client Secret（严重）

**文件：** `core/app/account/natayarkservice.go:26`

```go
natayarkClientSecret = "ec9080b65cd9c5076f964c27da992def"
```

客户端密钥硬编码在源码中，任何拥有二进制文件的人都可以通过 `strings` 命令提取。应通过 ldflags 或环境变量注入，与 MS OAuth 凭据的处理方式保持一致。

### 1.2 盲水印种子硬编码

**文件：** `core/app/watermarkservice.go:23-24`

```go
const seedImg = 12345
const seedWm = 67890
```

种子是固定常量，意味着任何拥有 GravityCone 的人都可以解码图片中的房间代码。如果希望只有特定应用能解码，需要将种子作为密钥管理。

### 1.3 机器 ID 持久化路径可预测

**文件：** `core/utils/machineid.go:32-33`

机器 ID 存储在 `UserConfigDir/GravityCone/machine_id`，任何本地进程都可以读取。对于本地应用而言风险较低，但值得注意。

### 1.4 HTTP 客户端无证书验证配置

多处使用 `&http.Client{Timeout: ...}` 的默认配置，未自定义 TLS 验证。对于 OAuth 流程影响不大，但下载 EasyTier 二进制时（`easytierdownload.go`）应考虑校验 SHA256 哈希以防止供应链攻击。

**建议：** 下载 EasyTier 后验证其 SHA256 checksum，或将已知的哈希值硬编码到源码中。

---

## 2. 错误处理

### 2.1 忽略关键错误

多处使用 `_` 忽略错误，可能在边缘情况下导致难以调试的问题：

- `scaffoldingservice.go:215` — `machineID, _ := utils.GetMachineID()` 在 HOST 添加玩家时忽略错误
- `scaffoldingservice.go:581` — 同样在 GUEST 加入时忽略
- `scaffoldingservice.go:613` — `json.Marshal` 错误被忽略
- `scaffoldingservice.go:992` — 心跳中 `json.Marshal` 错误被忽略
- `machineid.go:51` — `os.WriteFile` 错误被忽略
- `natayarkservice.go:65-66` — 保存 session 时的序列化和写入错误被忽略
- `minecraftservice.go:128-129` — 保存 Minecraft session 时同样忽略错误

### 2.2 WriteProtocolResponse 错误未检查

**文件：** `core/protocol/scaffolding/scaffoldingservice.go`

多个 handler 方法中 `WriteProtocolResponse` 的返回值未被检查（如 `handlePing`, `handleServerPort`, `handlePlayerPing`）。如果写入失败，调用方不会收到任何通知。

### 2.3 StopRoom 中的竞态窗口

**文件：** `scaffoldingservice.go:233-271`

`StopRoom` 在释放 `hostMu` 后关闭连接，但在关闭 `hostStopCh` 和设置 `hostRunning = false` 之间有一个小窗口，新的连接可能被接受但 `hostRunning` 尚未更新。

---

## 3. 并发与竞态

### 3.1 guestReadCh 缓冲区不足

**文件：** `scaffoldingservice.go:709`

```go
s.guestReadCh = make(chan readResult, 1)
```

缓冲区只有 1。`guestReadLoop` 在写入 channel 后立即继续读取下一个响应。如果消费者（`writeAndWait`）尚未消费前一个结果，`guestReadLoop` 将阻塞，可能导致读取超时。

### 3.2 多处锁内日志调用

`GetConnectionStatus`（line 883-905）在持有和不持有锁之间多次切换来检查状态，可能导致不一致的读取。不过在实际使用中这种模式问题不大。

---

## 4. 代码质量

### 4.1 函数过长

- `JoinRoom`（~160 行）— 可拆分为多个步骤方法
- `discoverHostAndConnect`（~114 行）— 重试逻辑可抽取
- `handleHostConnection`（~50 行）— switch 分支可独立

### 4.2 重复代码

`LeaveRoom`（line 833-881）和 `autoDisconnect`（line 1074-1118）有大量重复的状态重置逻辑。应抽取一个 `resetGuestState()` 方法。

### 4.3 未使用的代码

- `EasyTierManager.DiscoverPeer()` — 未被任何调用方使用（`FindPeerByHostnamePrefix` 替代了它）
- `LanService.CreateRoom()` — 返回 `fmt.Errorf("not implemented")`，但 GUI 中创建房间走的是 ScaffoldingService，此方法实际上为死代码

### 4.4 `generateCodeVerifier` 安全性

**文件：** `core/app/account/minecraftservice.go:94-100`

```go
b := make([]byte, 32)
for i := range b {
    b[i] = charsetForPKCE[i%len(charsetForPKCE)]
}
```

这段代码实际上是一个确定性的字符串（只是 charsetForPKCE 的重复），而不是随机生成的。PKCE 应该是随机生成的才有意义。应使用 `crypto/rand`。

### 4.5 线性搜索 roomCodeCharset

**文件：** `core/protocol/scaffolding/roomcode.go:19-25`

`charToValue` 使用 O(n) 线性搜索。用 `map[byte]int` 或数组查找（因为 charset 是有限的 34 个字符，可用 256 大小的数组做直接索引）会更高效。

### 4.6 `--service` 标志无实际功能

**文件：** `main.go:56-60`

当 `--service` 标志存在时，程序直接 `return`，不做任何事情。要么移除这个标志，要么实现服务模式功能。

---

## 5. 前端

### 5.1 轮询效率

多个页面使用 `setInterval` 每 2-3 秒轮询后端状态。这在本地应用中是可接受的，但更好的方案是使用 Wails 事件系统推送状态变更。

### 5.2 Watermark Store 中的动态导入

**文件：** `frontend/src/stores/watermark.ts`

每次调用 `encode`/`decode` 都使用动态 `import()` 来加载绑定。静态导入在顶层更简单、更高效。

### 5.3 错误处理不一致

部分 store actions 捕获异常后设置 error 状态但不 rethrow（如 `scaffolding.store.ts` 中的 `refreshRoomStatus`），而其他 action（如 `createRoom`）会 rethrow。调用方需要知道每个 action 的行为才能正确处理。

### 5.4 TypeScript 严格模式

未启用 TypeScript strict mode。启用后可以发现潜在的空值引用问题。

---

## 6. 测试

项目中**完全没有测试**。对于核心逻辑（房间代码解析/生成、协议序列化、EasyTier 管理）来说这是显著的风险。

建议至少为以下模块添加单元测试：
- `roomcode.go` — 房间代码生成和解析
- `scaffoldingprotocol.go` — 协议读写
- `easytiermanager.go` — 端口分配和参数构建逻辑

---

## 7. 构建配置

### 7.1 build/config.yml 使用占位值

`build/config.yml` 中的 metadata（companyName, productName 等）仍为默认占位值，应在发布前更新。

### 7.2 无 CI/CD 配置

项目没有 GitHub Actions 或其他 CI 配置文件。建议至少添加构建验证流程。

---

## 8. 改进建议（按优先级排序）

| 优先级 | 问题 | 建议 |
|--------|------|------|
| **P0** | 硬编码 OAuth client secret | 通过 ldflags 注入 |
| **P0** | PKCE code verifier 非随机 | 使用 `crypto/rand` 生成 |
| **P1** | 下载的 EasyTier 未校验哈希 | 校验 SHA256 防供应链攻击 |
| **P1** | guestReadCh 缓冲区不足 | 增大缓冲区或使用无界队列 |
| **P1** | 多处错误被忽略 | 至少记录日志 |
| **P2** | LeaveRoom/autoDisconnect 代码重复 | 抽取公共方法 |
| **P2** | 无单元测试 | 为核心模块添加测试 |
| **P2** | 函数过长 | 拆分 JoinRoom 等方法 |
| **P3** | `--service` 标志无功能 | 删除或实现 |
| **P3** | build/config.yml 占位值 | 填写实际项目信息 |
| **P3** | Dead code (DiscoverPeer, LanService.CreateRoom) | 清理 |
