# GravityCone CLI PaperConnect 文档

PaperConnect 是 GravityCone 面向 **Minecraft Bedrock Edition** 的房间协议，使用 `P/` 前缀房间代码。通用 CLI 生命周期、JSON Lines 消息格式、错误码、共享房间操作和 SDK 集成示例请参见 [cli_sdk.md](cli_sdk.md)。

`room.join` 收到 `P/` 前缀代码时自动使用 PaperConnect；创建 PaperConnect 房间时，`room.create` 必须传入 `"protocol": "paperconnect"`。

## 底层传输

创建房间时会同时扫描本机的 NetherNet 和 RakNet 局域网世界；两者都发现时优先选择 NetherNet。所有 PaperConnect 响应均包含高层协议字段 `"protocol": "paperconnect"`，并以 `sub_protocol` 指明实际底层传输：`"nethernet"` 或 `"raknet"`。

`game_port` 由 GravityCone 自动确定：RakNet 时为检测到的房主基岩版游戏端口；NetherNet 时为房主分配的 RakNet 代理端口。调用方不需要提供游戏端口。

## 房间方法

### `room.create`

创建基岩版联机房间（HOST 模式）。耗时约 2-5 秒。

```json
// 请求
{
  "id": 4,
  "method": "room.create",
  "params": {
    "player_name": "Steve",
    "protocol": "paperconnect"
  }
}

// 响应
{
  "id": 4,
  "status": "success",
  "data": {
    "code": "P/G1E4-Y2H8-N3XR-72LN",
    "game_port": 45678,
    "sub_protocol": "nethernet",
    "online_count": 1,
    "players": [
      {"player": "Steve", "clientId": "GravityCone-1.0.0", "isRoomHost": true}
    ],
    "running": true,
    "protocol": "paperconnect"
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|-------------|------|------|------|
| `player_name` | string | 是 | 玩家名称 |
| `protocol` | string | 是 | 必须为 `"paperconnect"` |
| `mc_port` | number | 否 | PaperConnect 不接受此参数；游戏端口由本机检测或代理自动分配 |

### `room.join`

加入基岩版联机房间（GUEST 模式）。成功响应表示控制通道已建立；本地游戏桥会继续异步建立。只有收到 `paperconnect.connection.ready` 事件后，才可确认游戏可发现并连接。

```json
// 请求
{
  "id": 6,
  "method": "room.join",
  "params": {
    "code": "P/G1E4-Y2H8-N3XR-72LN",
    "player_name": "Newton"
  }
}

// 最终响应
{
  "id": 6,
  "status": "success",
  "data": {
    "room_code": "P/G1E4-Y2H8-N3XR-72LN",
    "host_address": "10.114.51.42",
    "game_port": 45678,
    "connected": true,
    "online_count": 2,
    "players": [
      {"player": "Steve", "clientId": "GravityCone-1.0.0", "isRoomHost": true},
      {"player": "Newton", "clientId": "GravityCone-1.0.0", "isRoomHost": false}
    ],
    "heartbeating": true,
    "disconnect_reason": "",
    "protocol": "paperconnect",
    "sub_protocol": "nethernet"
  }
}
```

| params 字段 | 类型 | 必填 | 说明 |
|-------------|------|------|------|
| `code` | string | 是 | `P/` 前缀房间代码 |
| `player_name` | string | 是 | 玩家名称 |

#### 本地发现

`sub_protocol` 为 `"nethernet"` 时，客人会提供名为 `GravityCone Proxy` 的本地 NetherNet 发现监听器。为 `"raknet"` 时，客人会将转发后的服务器作为本地局域网假服务器广播。两种模式均应等待 `paperconnect.connection.ready` 后，再在基岩版多人游戏列表中查找房间。

### `room.status`

查询当前基岩版房间状态。当前没有 PaperConnect 活动房间时，响应为 `{"role": "none"}`。

```json
// 请求
{"id": 9, "method": "room.status", "params": {}}

// HOST 响应
{"id": 9, "status": "success", "data": {
  "role": "host",
  "code": "P/G1E4-Y2H8-N3XR-72LN",
  "game_port": 45678,
  "online_count": 2,
  "players": [...],
  "running": true,
  "protocol": "paperconnect",
  "sub_protocol": "nethernet"
}}

// GUEST 响应
{"id": 9, "status": "success", "data": {
  "role": "guest",
  "room_code": "P/G1E4-Y2H8-N3XR-72LN",
  "host_address": "10.114.51.42",
  "game_port": 45678,
  "connected": true,
  "online_count": 2,
  "players": [...],
  "heartbeating": true,
  "disconnect_reason": "",
  "protocol": "paperconnect",
  "sub_protocol": "nethernet"
}}
```

`room.stop`、`room.cancel_join` 和 `room.leave` 为共享方法，详见 [cli_sdk.md](cli_sdk.md)。

### `room.confirm_minecraft_ended`

当客人收到 `paperconnect.connection.port_busy` 后，调用方必须先结束 Minecraft，再发送此请求以允许 GravityCone 对 UDP 7551 做一次新的绑定尝试。成功响应仅表示确认已交给连接流程；必须继续等待 `paperconnect.connection.ready`，才能确认本地游戏广播可用。

```json
{"id": 10, "method": "room.confirm_minecraft_ended", "params": {}}
```

## 事件

| event | data 结构 | 说明 |
|-------|----------|------|
| `paperconnect.room.info` | HOST: `PaperConnectRoomStatus`；GUEST: `PaperConnectConnectionStatus` | 房间完整状态。创建/加入成功后立即推送一次，之后每次玩家列表变化时重新推送 |
| `paperconnect.room.player_joined` | `PCPlayerInfo` | 新玩家加入房间 |
| `paperconnect.room.player_left` | `PCPlayerInfo` | 玩家离开房间（超时） |
| `paperconnect.room.closed` | `{"reason": "..."}` | 房间被关闭 |
| `paperconnect.room.disconnected` | `{"reason": "..."}` | 与房间断开连接 |
| `paperconnect.connection.ready` | `{"protocol": "nethernet"}` 或 `{"protocol": "raknet"}` | 客人的本地游戏桥已建立，可以在基岩版多人游戏列表中发现并连接 |
| `paperconnect.connection.port_busy` | `{"port": "7551", "message": "..."}` | Minecraft 占用本地 UDP 7551，客人控制通道和代理仍保持运行；结束 Minecraft 后发送 `room.confirm_minecraft_ended`，然后等待 `paperconnect.connection.ready` |
| `paperconnect.connection.error` | `{"message": "游戏连接建立失败，仅控制通道可用"}` | 客人的本地游戏桥建立失败；控制通道仍可能保持连接 |

### PaperConnectRoomInfo 结构

房主和房客收到的 `paperconnect.room.info` 数据结构不同，分别与 `room.create` 和 `room.join` 的响应 `data` 字段一致：

**房主**（`PaperConnectRoomStatus`）：

```json
{
  "code": "P/G1E4-Y2H8-N3XR-72LN",
  "game_port": 45678,
  "sub_protocol": "nethernet",
  "online_count": 2,
  "players": [
    {"player": "Steve", "clientId": "GravityCone-1.0.0", "isRoomHost": true},
    {"player": "Newton", "clientId": "GravityCone-1.0.0", "isRoomHost": false}
  ],
  "running": true
}
```

**房客**（`PaperConnectConnectionStatus`）：

```json
{
  "room_code": "P/G1E4-Y2H8-N3XR-72LN",
  "host_address": "10.114.51.42",
  "game_port": 45678,
  "sub_protocol": "nethernet",
  "connected": true,
  "online_count": 2,
  "players": [
    {"player": "Steve", "clientId": "GravityCone-1.0.0", "isRoomHost": true},
    {"player": "Newton", "clientId": "GravityCone-1.0.0", "isRoomHost": false}
  ],
  "heartbeating": true,
  "disconnect_reason": ""
}
```

### PCPlayerInfo 结构

```json
{
  "player": "Steve",
  "clientId": "GravityCone-1.0.0",
  "isRoomHost": true
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `player` | string | 玩家名称 |
| `clientId` | string | 客户端标识 |
| `isRoomHost` | bool | 是否为房主 |
