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

```bash
go install github.com/wjzhangq/caddy-ws-tunnel-client/cmd/tunnel-agent@latest
```

或从源码构建：

```bash
git clone https://github.com/wjzhangq/caddy-ws-tunnel-client
cd caddy-ws-tunnel-client
go build -o tunnel-agent ./cmd/tunnel-agent
```

## 使用

```bash
tunnel-agent --url wss://caddy.example.com:8443/ws --key mykey
```

或通过环境变量：

```bash
TUNNEL_URL=wss://caddy.example.com:8443/ws \
TUNNEL_KEY=mykey \
tunnel-agent
```

### 参数说明

| 参数 | 环境变量 | 必填 | 说明 |
|---|---|---|---|
| `--url` | `TUNNEL_URL` | 是 | Caddy 服务端 WebSocket 地址（如 `wss://host:port/ws`） |
| `--key` | `TUNNEL_KEY` | 是 | 鉴权密钥；服务端通过此 key 反查节点身份 |
| `--node` | — | 否 | 覆盖节点名称（默认取 key 前 8 位） |
| `--debug` | — | 否 | 开启详细结构化日志 |

## 协议实现

| 阶段 | 行为 |
|---|---|
| **握手** | 发送 `hello{role:"control"}` 含 HMAC-SHA256 签名；读取 `welcome` 获得 session、通道数、心跳间隔和初始端口允许清单 |
| **数据通道** | 建立 N 条 WebSocket，每条发送 `hello{role:"data", session}`；`session` 来自控制握手，防止孤立数据连接注入 |
| **流转发** | `AcceptStream` → 读 1 字节版本号 + uvarint 端口 ID 流头 → 允许清单校验 → dial 本地 TCP → 双向 `io.Copy` |
| **保活** | 控制通道按服务端下发的 `heartbeat` 间隔收发 ping/pong；数据通道保活由 smux 负责 |
| **热更新** | `reload_config` 原子替换端口允许清单和 dial 超时，不断连生效 |
| **优雅停机** | 收到 `drain` 后停止接受新流，等待存量流跑完再关闭 |
| **重连** | 指数退避 1s → 30s 含抖动；每条数据通道独立重连；鉴权错误不重试 |
| **遥测** | 周期发送 `stats`，上报活跃通道数、流数、收发字节、本地 dial 错误，喂给服务端 `/status` |

## 安全说明

- key 通过 WebSocket 连接传输，生产环境请务必使用 `wss://`（TLS）。
- 每次握手生成新 nonce，HMAC-SHA256 签名防止重放攻击（服务端开启 HMAC 模式时生效）。
- agent 仅 dial 服务端下发允许清单内的地址，无法访问清单外的任何本地端口。

## 目录结构

```
cmd/tunnel-agent/        入口：参数解析、信号处理
internal/agent/
  agent.go               生命周期：控制+数据通道、重连退避
  control.go             控制通道：握手、ping/pong、热更、drain
  data.go                数据通道：smux 会话、AcceptStream 循环
  forward.go             单流处理：读流头 → 允许清单 → dial → io.Copy
  allowlist.go           并发安全端口允许清单，支持原子热替换
  auth.go                HMAC-SHA256 签名与 nonce 生成
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
