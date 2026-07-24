# GravityCone CLI ScaffoldingMC 文档

ScaffoldingMC 是 GravityCone 面向 **Minecraft Java Edition** 的房间协议，使用 `U/` 前缀房间代码。通用 CLI 生命周期、JSON Lines 消息格式、错误码、共享房间操作和 SDK 集成示例请参见 [cli_sdk.md](cli_sdk.md)。

`room.join` 收到非 `P/` 前缀的代码时自动使用 ScaffoldingMC；`room.create` 省略 `protocol` 时创建 ScaffoldingMC 房间。

## 房间方法

### `room.create`

创建 Java 版联机房间（HOST 模式）。耗时约 2-5 秒。

```json
// 请求
{
  "id": 4,
  "method": "room.create",
  "params": {
    "mc_port": 25565,
    "player_name": "Steve"
  }
}

// 响应
{
  "id": 4,
  "status": "success",
  "data": {
    "code": "U/1234-5678-9012-3456",
    "mc_address": "10.144.144.1",
    "mc_port": 25565,
    "online_count": 1,
    "players": [
      {"name": "Steve", "machine_id": "abc123", "vendor": "GC", "kind": "HOST"}
    ],
    "running": true
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|-------------|------|------|------|
| `mc_port` | number | 是 | Minecraft 服务器端口（1025-65535） |
| `player_name` | string | 是 | 玩家名称 |

### `room.join`

加入 Java 版联机房间（GUEST 模式）。耗时约 5-30 秒，期间会推送 `progress` 响应。

```json
// 请求
{
  "id": 6,
  "method": "room.join",
  "params": {
    "code": "U/1234-5678-9012-3456",
    "player_name": "Alex"
  }
}

// 进度通知（可能收到多条）
{"id": 6, "status": "progress", "data": {"step": "connecting", "message": "正在连接 EasyTier 网络..."}}
{"id": 6, "status": "progress", "data": {"step": "waiting_peer", "message": "等待对端节点上线..."}}
{"id": 6, "status": "progress", "data": {"step": "handshaking", "message": "正在握手协商..."}}
{"id": 6, "status": "progress", "data": {"step": "ready", "message": "连接就绪"}}

// 最终响应
{
  "id": 6,
  "status": "success",
  "data": {
    "room_code": "U/1234-5678-9012-3456",
    "host_address": "10.114.51.42",
    "mc_address": "127.0.0.1",
    "mc_port": 25565,
    "connected": true,
    "online_count": 2,
    "players": [
      {"name": "Steve", "machine_id": "abc123", "vendor": "GC", "kind": "HOST"},
      {"name": "Alex", "machine_id": "def456", "vendor": "GC", "kind": "GUEST"}
    ],
    "heartbeating": true,
    "disconnect_reason": ""
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|-------------|------|------|------|
| `code` | string | 是 | `U/` 前缀房间代码 |
| `player_name` | string | 是 | 玩家名称 |

#### ScaffoldingMC 进度步骤

| step | message | 说明 |
|------|---------|------|
| `connecting` | 正在连接 EasyTier 网络... | 启动虚拟网络 |
| `waiting_peer` | 等待对端节点上线... | 寻找 HOST 节点 |
| `handshaking` | 正在握手协商... | 协议协商 |
| `ready` | 连接就绪 | 连接建立完成 |

### `room.status`

查询当前 Java 版房间状态。当前没有 ScaffoldingMC 活动房间时，响应为 `{"role": "none"}`。

```json
// 请求
{"id": 9, "method": "room.status", "params": {}}

// HOST 响应
{"id": 9, "status": "success", "data": {
  "role": "host",
  "code": "U/1234-5678-9012-3456",
  "mc_address": "10.144.144.1",
  "mc_port": 25565,
  "online_count": 2,
  "players": [...],
  "running": true
}}

// GUEST 响应
{"id": 9, "status": "success", "data": {
  "role": "guest",
  "room_code": "U/1234-5678-9012-3456",
  "host_address": "10.114.51.42",
  "mc_address": "127.0.0.1",
  "mc_port": 25565,
  "connected": true,
  "online_count": 2,
  "players": [...],
  "heartbeating": true,
  "disconnect_reason": ""
}}
```

`room.stop`、`room.cancel_join` 和 `room.leave` 为共享方法，详见 [cli_sdk.md](cli_sdk.md)。

## 事件

| event | data 结构 | 说明 |
|-------|----------|------|
| `room.player_joined` | `PlayerInfo` | 仅房主模式：有新客人加入时推送 |
| `room.player_left` | `PlayerInfo` | 仅房主模式：客人心跳超时时推送 |
| `room.closed` | `{"reason": "..."}` | 仅房主模式：房间被关闭 |
| `room.disconnected` | `{"reason": "..."}` | 仅客人模式：与房间断开连接 |
| `room.guest_player_list_updated` | `PlayerInfo[]` | 仅客人模式：从房主同步到最新玩家列表时推送 |

### PlayerInfo 结构

```json
{
  "name": "Steve",
  "machine_id": "abc123",
  "easytier_id": "...",
  "vendor": "GC, GCCore v0.1.0, EasyTier v2.6.4",
  "kind": "HOST"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `name` | string | 玩家名称 |
| `machine_id` | string | 机器唯一标识 |
| `easytier_id` | string | EasyTier 节点 ID（可能为空） |
| `vendor` | string | 客户端版本信息 |
| `kind` | string | `"HOST"` 或 `"GUEST"` |
