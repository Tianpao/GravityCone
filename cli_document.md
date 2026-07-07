# GravityCone CLI 开发者文档

## 概述

GravityCone CLI 是一个独立的无 GUI 可执行文件，通过 **stdin/stdout** 与宿主进程通信，使用 JSON Lines 协议（每行一条 JSON 消息）。你可以从任何能启动子进程并读写其 stdio 的环境中调用它——Electron、Tauri、Python、Node.js、Shell 脚本等。

## 快速开始

### 构建

```bash
# 生产构建（体积小，~7MB）
task build:cli

# 开发构建（带调试信息）
task build:cli:dev
```

产物位于 `bin/gravitycone-cli.exe`（Windows）或 `bin/gravitycone-cli`（Linux/macOS）。

### 命令行参数

```
gravitycone-cli [-p <addr>] [--peers <addr>] [-v <prefix>] [--vendor <prefix>] [-m <motd>] [--motd <motd>]
```

| 参数 | 说明 |
|------|------|
| `-p <addr>` | 添加 EasyTier 公网节点地址，可多次使用 |
| `--peers <addr>` | 同 `-p`，可多次使用 |
| `-v <prefix>` | 厂商前缀，附加到 vendor 字符串前 |
| `--vendor <prefix>` | 同 `-v` |
| `-m <motd>` | 自定义局域网广播 MOTD，其他玩家在多人游戏列表中看到的房间名称 |
| `--motd <motd>` | 同 `-m` |

地址支持逗号分隔，以下写法均合法：

```bash
# 多次指定节点
gravitycone-cli -p tcp://1.2.3.4:5678 -p tcp://5.6.7.8:9012

# 逗号分隔节点
gravitycone-cli -p tcp://1.2.3.4:5678,tcp://5.6.7.8:9012

# 混用
gravitycone-cli -p tcp://1.2.3.4:5678 --peers tcp://5.6.7.8:9012

# 指定厂商前缀
gravitycone-cli -v GC
gravitycone-cli --vendor GC -p tcp://1.2.3.4:5678

# 自定义局域网广播 MOTD
gravitycone-cli -m "PCL CE 联机房间"
gravitycone-cli --motd "PCL CE 联机房间" -p tcp://1.2.3.4:5678
```

不指定节点则使用内置默认节点，不指定厂商前缀则默认为空，不指定 MOTD 则使用默认值 `§6§l双击进入联机房间（请保持GravityCone运行）`。

### 日志文件

CLI 启动后会在可执行文件同目录下创建 `logs/` 文件夹：

```
logs/
  gccore.log      — 程序自身运行日志
  easytier.log    — EasyTier 子进程日志
  stdio.log       — 协议输入输出记录（> 前缀表示输入，其余为输出）
```

**终端不输出任何日志**，仅输出协议 JSON 消息。

### 启动与关闭

```bash
# 启动 CLI 进程
./bin/gravitycone-cli

# CLI 启动后会立即向 stdout 输出 ready 事件：
# {"event":"system.ready","data":{"version":"1.0.0"}}
```

收到 `system.ready` 后即可开始发送请求。关闭 CLI 有三种方式：

1. 发送 `system.shutdown` 请求
2. 关闭 stdin（EOF）
3. 发送 SIGINT 信号（Ctrl+C）

## 通信协议

### 基本规则

- 通信通过 **stdin/stdout** 进行，每条消息占一行（以 `\n` 结尾）
- 所有消息均为 **JSON 格式**
- **终端不输出任何日志**，日志写入可执行文件同目录下的 `logs/` 文件夹
- CLI 启动后进入常驻模式，持续监听 stdin

### 请求格式

```json
{
  "id": 1,
  "method": "stun.probe",
  "params": {}
}
```

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `id` | number | 是 | 请求标识，响应中原样返回，用于匹配请求与响应 |
| `method` | string | 是 | 调用方法，格式为 `group.action` |
| `params` | object | 否 | 方法参数，无参数时为 `{}` 或省略 |

### 响应格式

**成功：**

```json
{"id": 1, "status": "success", "data": { ... }}
```

**失败：**

```json
{"id": 1, "status": "error", "error": {"code": "STUN_FAILED", "message": "easytier-cli stun failed: exit status 1"}}
```

**进度（长时任务中间通知）：**

```json
{"id": 2, "status": "progress", "data": {"step": "connecting", "message": "正在连接 EasyTier 网络..."}}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | number | 对应请求的 id |
| `status` | string | `"success"` / `"error"` / `"progress"` |
| `data` | any | 成功或进度时的返回数据 |
| `error` | object | 失败时的错误信息，含 `code` 和 `message` |

> **注意**：`progress` 响应后仍会跟随最终的 `success` 或 `error` 响应。同一 `id` 可能收到多条 `progress`，但最终一定有一条终结响应。

### 事件推送

CLI 会主动向 stdout 推送事件通知，**无 `id` 字段**，以 `event` 字段标识：

```json
{"event": "room.player_joined", "data": { "name": "Steve", "machine_id": "abc123", ... }}
```

### 错误码

| code | 说明 |
|------|------|
| `STUN_FAILED` | STUN 探查失败 |
| `STUN_PARSE_ERROR` | STUN 结果解析失败 |
| `EASYTIER_NOT_FOUND` | easytier-cli 未找到 |
| `INVALID_METHOD` | 未知的方法名 |
| `INVALID_PARAMS` | 参数格式错误或缺少必要参数 |
| `NOT_CONNECTED` | 未连接到任何房间 |
| `ROOM_NOT_FOUND` | 房间不存在或已关闭 |
| `ROOM_ALREADY_RUNNING` | 已有房间在运行中 / 已在一个房间中 |
| `INTERNAL_ERROR` | 内部错误 |

---

## 方法列表

### system 组 — 系统控制

#### `system.ping`

健康检查，确认 CLI 存活。

```json
// 请求
{"id": 1, "method": "system.ping", "params": {}}

// 响应
{"id": 1, "status": "success", "data": {"pong": true}}
```

#### `system.shutdown`

关闭 CLI 进程。CLI 发送响应后退出。

```json
// 请求
{"id": 2, "method": "system.shutdown", "params": {}}

// 响应
{"id": 2, "status": "success", "data": {}}
```

#### `system.add_peers`

添加节点地址，追加到启动参数中。下次创建或加入房间时生效。GravityCone Core不会储存你的节点信息，请各位开发者自行储存节点信息。

```json
// 请求
{"id": 3, "method": "system.add_peers", "params": {"peers": ["tcp://1.2.3.4:5678", "tcp://5.6.7.8:9012"]}}

// 响应
{"id": 3, "status": "success", "data": {}}
```

| params 字段 | 类型 | 必填 | 说明 |
|-------------|------|------|------|
| `peers` | string[] | 是 | 要添加的节点地址列表 |

---

### stun 组 — NAT 探查

#### `stun.probe`

执行 STUN 探查，获取 NAT 类型和公网信息。耗时约 3-10 秒。

```json
// 请求
{"id": 3, "method": "stun.probe", "params": {}}

// 响应
{
  "id": 3,
  "status": "success",
  "data": {
    "udp_nat_type": 1,
    "tcp_nat_type": 2,
    "last_update_time": 1720246800,
    "public_ip": ["203.0.113.1"],
    "min_port": 30000,
    "max_port": 40000
  }
}
```

| data 字段 | 类型 | 说明 |
|-----------|------|------|
| `udp_nat_type` | number | UDP NAT 类型：0=Public, 1=FullCone, 2=RestrictedCone, 3=PortRestrictedCone, 4=Symmetric |
| `tcp_nat_type` | number | TCP NAT 类型（编码同上） |
| `last_update_time` | number | 探查时间戳（Unix 秒） |
| `public_ip` | string[] | 公网 IP 列表 |
| `min_port` | number | 端口范围下限 |
| `max_port` | number | 端口范围上限 |

---

### room 组 — 房间管理

#### `room.create`

创建联机房间（HOST 模式）。耗时约 2-5 秒。

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
    "mc_address": "10.114.51.41",
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

#### `room.stop`

停止当前房间（HOST 模式）。

```json
// 请求
{"id": 5, "method": "room.stop", "params": {}}

// 响应
{"id": 5, "status": "success", "data": {}}
```

#### `room.join`

加入房间（GUEST 模式）。耗时约 5-30 秒，期间会推送 `progress` 响应。

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
| `code` | string | 是 | 房间代码 |
| `player_name` | string | 是 | 玩家名称 |

**进度步骤：**

| step | message | 说明 |
|------|---------|------|
| `connecting` | 正在连接 EasyTier 网络... | 启动虚拟网络 |
| `waiting_peer` | 等待对端节点上线... | 寻找 HOST 节点 |
| `handshaking` | 正在握手协商... | 协议协商 |
| `ready` | 连接就绪 | 连接建立完成 |

#### `room.cancel_join`

取消正在进行的加入操作。

```json
// 请求
{"id": 7, "method": "room.cancel_join", "params": {}}

// 响应
{"id": 7, "status": "success", "data": {}}
```

#### `room.leave`

离开当前房间（GUEST 模式）。

```json
// 请求
{"id": 8, "method": "room.leave", "params": {}}

// 响应
{"id": 8, "status": "success", "data": {}}
```

#### `room.status`

查询当前房间状态。根据角色返回不同结构。

```json
// 请求
{"id": 9, "method": "room.status", "params": {}}

// HOST 模式
{"id": 9, "status": "success", "data": {
  "role": "host",
  "code": "U/1234-5678-9012-3456",
  "mc_address": "10.114.51.41",
  "mc_port": 25565,
  "online_count": 2,
  "players": [...],
  "running": true
}}

// GUEST 模式
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

// 未在房间中
{"id": 9, "status": "success", "data": {"role": "none"}}
```

---

### lan 组 — 局域网发现

#### `lan.start_discovery`

开始监听局域网 Minecraft 服务器广播。

```json
// 请求
{"id": 10, "method": "lan.start_discovery", "params": {}}

// 响应
{"id": 10, "status": "success", "data": {}}
```

启动后，发现新服务器时会自动推送 `lan.server_found` 事件。

#### `lan.stop_discovery`

停止局域网监听。

```json
// 请求
{"id": 11, "method": "lan.stop_discovery", "params": {}}

// 响应
{"id": 11, "status": "success", "data": {}}
```

#### `lan.list_servers`

获取已发现的局域网服务器列表。

```json
// 请求
{"id": 12, "method": "lan.list_servers", "params": {}}

// 响应
{"id": 12, "status": "success", "data": {
  "servers": [
    {"motd": "A Minecraft Server", "ip": "192.168.1.100", "port": 25565}
  ]
}}
```

#### `lan.verify_server`

验证服务器是否可达并获取版本信息。耗时约 1-3 秒。

```json
// 请求
{"id": 13, "method": "lan.verify_server", "params": {"ip": "192.168.1.100", "port": 25565}}

// 响应
{"id": 13, "status": "success", "data": {"online": true, "version": "1.21.4"}}
```

| params 字段 | 类型 | 必填 | 说明 |
|-------------|------|------|------|
| `ip` | string | 是 | 服务器 IP |
| `port` | number | 是 | 服务器端口 |

---

## 事件列表

CLI 会主动推送以下事件，宿主进程无需发送请求即可接收。

| event | data 结构 | 说明 |
|-------|----------|------|
| `system.ready` | `{"version": "1.0.0"}` | CLI 启动就绪 |
| `room.player_joined` | `PlayerInfo` | 新玩家加入房间 |
| `room.player_left` | `PlayerInfo` | 玩家离开房间（超时） |
| `room.closed` | `{"reason": "..."}` | 房间被关闭 |
| `room.disconnected` | `{"reason": "..."}` | 与房间断开连接 |
| `lan.server_found` | `{"motd": "...", "ip": "...", "port": 25565}` | 发现新的局域网服务器 |
| `lan.server_lost` | `{"ip": "...", "port": 25565}` | 局域网服务器消失（30秒无广播） |

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

---

## 集成示例

### Node.js

```javascript
const { spawn } = require('child_process');

const cli = spawn('./bin/gravitycone-cli', ['-p', 'tcp://1.2.3.4:5678', '-v', 'GC', '-m', 'PCL CE 联机房间'], { stdio: ['pipe', 'pipe', 'pipe'] });

let nextId = 1;

// 监听 stdout
const readline = require('readline');
const rl = readline.createInterface({ input: cli.stdout });

rl.on('line', (line) => {
  const msg = JSON.parse(line);

  if (msg.event) {
    // 异步事件
    console.log('Event:', msg.event, msg.data);
  } else {
    // 请求响应
    console.log('Response:', msg.id, msg.status, msg.data || msg.error);
  }
});

// 监听 stderr（CLI 不向终端输出日志，stderr 通常为空）
cli.stderr.on('data', (data) => {
  console.error('[cli-err]', data.toString());
});

// 发送请求
function request(method, params = {}) {
  const id = nextId++;
  const msg = JSON.stringify({ id, method, params }) + '\n';
  cli.stdin.write(msg);
  return id;
}

// 等待 ready 事件后开始操作
// （rl.on('line') 会先收到 system.ready 事件）

// 探查 NAT
request('stun.probe');

// 创建房间
request('room.create', { mc_port: 25565, player_name: 'Steve' });

// 关闭 CLI
request('system.shutdown');
```

### Python

```python
import subprocess
import json
import sys

cli = subprocess.Popen(
    ['./bin/gravitycone-cli', '-p', 'tcp://1.2.3.4:5678', '-v', 'GC', '-m', 'PCL CE 联机房间'],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
    bufsize=1,
)

next_id = 1

def request(method, params=None):
    global next_id
    msg = {'id': next_id, 'method': method, 'params': params or {}}
    next_id += 1
    cli.stdin.write(json.dumps(msg) + '\n')
    cli.stdin.flush()
    return msg['id']

# 读取 stdout（注意：生产环境应使用线程分别读写）
for line in cli.stdout:
    msg = json.loads(line)
    if 'event' in msg:
        print(f'Event: {msg["event"]}', msg['data'])
    else:
        print(f'Response: {msg["id"]} {msg["status"]}', msg.get('data') or msg.get('error'))
```

### Shell（快速测试）

```bash
# 管道方式：发送多条请求
printf '{"id":1,"method":"system.ping","params":{}}\n{"id":2,"method":"room.status","params":{}}\n{"id":3,"method":"system.shutdown","params":{}}\n' \
  | ./bin/gravitycone-cli

# 指定自定义节点
printf '{"id":1,"method":"system.ping","params":{}}\n{"id":2,"method":"system.shutdown","params":{}}\n' \
  | ./bin/gravitycone-cli -p tcp://1.2.3.4:5678 -p tcp://5.6.7.8:9012
```

---

## 完整交互流程示例

```
宿主进程                        CLI 进程
   │                              │
   │──── 启动 CLI (-p ... -v ... -m ...) ──>│
   │                              │  初始化服务，日志写入 logs/
   │<─── system.ready 事件 ───────│  {"event":"system.ready","data":{"version":"1.0.0"}}
   │                              │
   │──── stun.probe ─────────────>│
   │<─── success ─────────────────│  返回 NAT 信息
   │                              │
   │──── room.create ────────────>│
   │<─── success ─────────────────│  返回房间信息（含房间代码）
   │                              │
   │<─── room.player_joined 事件 ─│  其他玩家加入时异步推送
   │                              │
   │──── room.status ────────────>│
   │<─── success ─────────────────│  返回当前房间状态
   │                              │
   │──── system.add_peers ───────>│  运行中动态添加节点
   │<─── success ─────────────────│
   │                              │
   │──── system.shutdown ────────>│
   │<─── success ─────────────────│
   │                              │  进程退出
```

## 方法速查表

| 方法 | 必填参数 | 预计耗时 | 说明 |
|------|---------|---------|------|
| `system.ping` | 无 | <1ms | 健康检查 |
| `system.shutdown` | 无 | <1s | 关闭 CLI |
| `system.add_peers` | `peers` | <1s | 动态添加节点 |
| `stun.probe` | 无 | 3-10s | NAT 探查 |
| `room.create` | `mc_port`, `player_name` | 2-5s | 创建房间 |
| `room.stop` | 无 | <1s | 停止房间 |
| `room.join` | `code`, `player_name` | 5-30s | 加入房间（有 progress） |
| `room.cancel_join` | 无 | <1s | 取消加入 |
| `room.leave` | 无 | <1s | 离开房间 |
| `room.status` | 无 | <1s | 查询房间状态 |
| `lan.start_discovery` | 无 | <1s | 开始 LAN 发现 |
| `lan.stop_discovery` | 无 | <1s | 停止 LAN 发现 |
| `lan.list_servers` | 无 | <1s | 列出 LAN 服务器 |
| `lan.verify_server` | `ip`, `port` | 1-3s | 验证服务器 |
