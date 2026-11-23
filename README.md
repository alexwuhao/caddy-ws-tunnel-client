# tunnel-agent

轻量级 NAT 穿透 agent，主动连接 [caddy-ws-tunnel-plugin](https://github.com/wjzhangq/caddy-ws-tunnel-plugin) 服务端，将外部请求转发到本地 TCP 服务。无需开放任何入站端口，所有连接由 agent 主动向外发起。

## 工作原理

```
外部客户端
    │  HTTPS/WSS
    ▼
Caddy（caddy-ws-tunnel-plugin）
    │  smux 多路复用 WebSocket
    ▼
tunnel-agent（本程序，运行于 NAT/内网）
    │  TCP
    ▼
本地服务（如 localhost:8080）
```

agent 维护两类长连接：
- **1 条控制通道** — 握手鉴权、保活、遥测上报、配置热更
- **N 条数据通道** — 每条是一个 smux 会话，单独断线单独重连

端口列表、通道数、超时等参数由服务端在握手时下发，运行中可通过 `reload_config` 热更，无需重连。

## 依赖

- Go 1.25+
- 已运行的 [caddy-ws-tunnel-plugin](https://github.com/wjzhangq/caddy-ws-tunnel-plugin) 服务端

## 安装

从 [Releases](https://github.com/wjzhangq/caddy-ws-tunnel-client/releases) 下载对应平台的二进制（Windows 是 `tunnel-agent-windows-amd64.exe` / `-arm64.exe`），或：

```bash
go install github.com/wjzhangq/caddy-ws-tunnel-client/cmd/tunnel-agent@latest
```

或从源码构建：

```bash
git clone https://github.com/wjzhangq/caddy-ws-tunnel-client
cd caddy-ws-tunnel-client
go build -o tunnel-agent ./cmd/tunnel-agent
```

### Windows 构建

```powershell
# 在 Windows 上直接构建（CGO 关掉，产物是单文件）
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -X main.version=v1.1.0" -o tunnel-agent.exe .\cmd\tunnel-agent

# 交叉编译（在任何平台）
$env:GOOS = "windows"; $env:GOARCH = "amd64"
go build -trimpath -ldflags "-s -w" -o tunnel-agent-windows-amd64.exe .\cmd\tunnel-agent
```

## 使用

### 明文 key 模式（兼容旧服务端）

```bash
tunnel-agent --url wss://caddy.example.com:8443/ws --key mykey
```

### HMAC 模式（服务端节点配置了 `hmac_secret`）

```bash
tunnel-agent --url wss://caddy.example.com:8443/ws --node node1 --secret <hmac_secret>
```

- `--secret` 是签名密钥（对应服务端节点的 `hmac_secret`），**不会**被发送到线路上；
- HMAC 模式下必须显式给 `--node`（不能像明文模式那样从 key 前 8 位推导）；
- 控制通道和每条数据通道的 hello 都会用新的 `ts/nonce` 重新签名。

也可全部走环境变量：

```bash
TUNNEL_URL=wss://caddy.example.com:8443/ws \
TUNNEL_KEY=mykey \
tunnel-agent

TUNNEL_URL=wss://caddy.example.com:8443/ws \
TUNNEL_NODE=node1 \
TUNNEL_SECRET=<hmac_secret> \
tunnel-agent
```

> `TUNNEL_NODE` / `TUNNEL_SECRET` / `TUNNEL_KEY` / `TUNNEL_URL` 都支持，命令行参数优先。

### 参数说明

| 参数 | 环境变量 | 必填 | 说明 |
|---|---|---|---|
| `--url` | `TUNNEL_URL` | 是 | Caddy 服务端 WebSocket 地址（如 `wss://host:port/ws`） |
| `--key` | `TUNNEL_KEY` | 二选一 | 明文鉴权密钥（服务端节点未配 `hmac_secret` 时用）；服务端通过此 key 反查节点身份 |
| `--secret` | `TUNNEL_SECRET` | 二选一 | HMAC 签名密钥（对应服务端 `hmac_secret`）；启用后不发送 `--key` 明文，且必须给 `--node` |
| `--node` | `TUNNEL_NODE` | HMAC 模式必填 | 节点名称；明文模式省略时取 key 前 8 位 |
| `--debug` | — | 否 | 开启详细结构化日志 |
| `--version` | — | 否 | 打印版本退出 |

### Windows 后台运行

`tunnel-agent.exe` 是普通控制台程序，**不能用 `sc create` 直接注册成 Windows 服务**（需要 SCM 交互）。两种常用做法：

```powershell
# 方式 1：NSSM（推荐，可自动重启）
nssm install tunnel-agent "C:\tools\tunnel-agent.exe" "--url" "wss://caddy.example.com:8443/ws" "--key" "mykey"
nssm set tunnel-agent AppStdout "C:\tools\tunnel-agent.log"
nssm start tunnel-agent

# 方式 2：任务计划程序（开机自启 + 崩溃重试）
$action = New-ScheduledTaskAction -Execute "C:\tools\tunnel-agent.exe" `
  -Argument '--url wss://caddy.example.com:8443/ws --key mykey'
Register-ScheduledTask -TaskName "tunnel-agent" -Action $action `
  -Trigger (New-ScheduledTaskTrigger -AtStartup) -RunLevel Highest -User "SYSTEM"
```

> 用 NSSM/计划任务时建议把 `--url` 等参数写进任务定义；也可以改用 `TUNNEL_URL` / `TUNNEL_KEY` 环境变量（NSSM 里在 `Environment` 页配置）。

## 协议实现

| 阶段 | 行为 |
|---|---|
| **握手** | 发送 `hello{role:"control", node, key|hidden, ts, nonce, sig}`（每次握手新 `ts/nonce`，`sig = HMAC-SHA256(signing_key, "node|ts|nonce")`）；读取 `welcome` 获得 session、通道数、心跳间隔和初始端口允许清单 |
| **数据通道** | 建立 N 条 WebSocket，每条发送**同样签名**的 `hello{role:"data", session, channel_id, ts, nonce, sig}`；`session` 来自控制握手，防止孤立数据连接注入 |
| **流转发** | `AcceptStream` → 读 1 字节版本号 + uvarint 端口 ID 流头 → 允许清单校验 → dial 本地 TCP → 双向 `io.Copy` |
| **保活** | 控制通道按服务端下发的 `heartbeat` 间隔收发 ping/pong；数据通道保活由 smux 负责。控制通道读循环用独立 goroutine + `select` 复用，不会因为读超时误断开连接（v1.1.0 修复） |
| **热更新** | `reload_config` 原子替换端口允许清单和 dial 超时，不断连生效 |
| **优雅停机** | 收到 `drain` 后停止接受新流，等待存量流跑完再关闭 |
| **重连** | 指数退避 1s → 30s 含抖动；每条数据通道独立重连；鉴权错误不重试 |
| **遥测** | 周期发送 `stats`，上报活跃通道数、流数、收发字节、本地 dial 错误，喂给服务端 `/status` |

## 安全说明

- **HMAC 模式（推荐）**：`--secret` 作为签名密钥，`hello` 里不发送任何密钥明文；每次 hello 新 `ts/nonce`，重放会被服务端拒绝（±60s 时间窗 + nonce 防重放）。控制通道和数据通道都签名。
- **明文模式（兼容旧服务端）**：`--key` 会随 hello 传输，**必须**配 `wss://`（TLS），或仅在可信内网使用。
- agent 仅 dial 服务端下发允许清单内的地址，无法访问清单外的任何本地端口。
- 服务端鉴权失败只返回通用 `auth failed`；具体原因看服务端日志（便于排障又不向未授权对端泄露细节）。

## 目录结构

```
cmd/tunnel-agent/        入口：参数解析、信号处理
internal/agent/
  agent.go               生命周期：控制+数据通道、重连退避
  control.go             控制通道：握手、ping/pong、热更、drain
  data.go                数据通道：smux 会话、AcceptStream 循环
  forward.go             单流处理：读流头 → 允许清单 → dial → io.Copy
  allowlist.go           并发安全端口允许清单，支持原子热替换
  auth.go                HMAC-SHA256 签名、nonce、hello 构造（明文/HMAC 两种模式）
  auth_test.go           签名契约测试（含 openssl 校验过的固定向量）
  proto/                 协议契约（与服务端 proto 包一致：消息结构、流头编解码、版本常量）
```

## 联调测试

用插件仓库的 `test/Caddyfile.test` 启动测试服务端，然后：

```bash
tunnel-agent --url wss://localhost:8443/ws --key xx1 --debug

# 另一个终端
curl -k https://node1.localhost/  # 应经隧道到达本地服务并正确回流
```

访问 `https://localhost:2019/status`，节点应显示 `up: true`，在线通道数与配置值一致。
