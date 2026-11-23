# Tunnel Client(tunnel-agent)— 开发指导(client_dev.md)

> 配套文档:`caddy-ws-tunnel-plugin-design.md`(设计权威)、`dev.md`(Caddy 插件/服务端施工手册)。
>
> **本文面向 client(tunnel-agent)开发者。client 代码实现不在本仓库**,由独立项目落地。本文给出:client 的职责边界、启动参数、必须遵守的**协议契约**(握手/流头/控制指令/重连)、按里程碑的实现顺序与自测标准、易错点清单。
>
> 协议字段、流头格式、消息类型以服务端 `modules/tunnel/proto` 子包为**权威定义**;client 应对照该包实现,不要自行发明字段。

---

## 0. client 是什么(职责边界)

client 是 **NAT/内网后的瘦 agent**,主动外连 Caddy,把外部请求转发到本地端口。

**做这些:**
- 启动时只读两个参数:`--url` + `--key`(§3.3)。 可以通env 指定。 也可以编译指定默认值。
- 主动外连 Caddy:建 **1 条控制通道** + 服务端下发数量的 **固定 `channels` 条数据通道**。
- 控制通道上完成握手鉴权、接收 `welcome` 内嵌配置、保活(ping/pong)、上报遥测(stats)、响应热更(reload_config)/优雅停机(drain)。
- 数据通道上跑 smux:`AcceptStream` → 读流头 → 查允许清单 → dial 本地 TCP → 双向 `io.Copy`。
- 断连重连(指数退避),重连后补齐固定 `channels` 条。

**不做这些:**
- 不解析 HTTP(L4 透传,流头之后是裸字节)。
- 不自带端口/通道/超时配置——**全部由服务端握手下发,运行中可热更**。
- 不主动开流(流方向永远 Caddy `OpenStream` → client `AcceptStream`)。
- 不 dial 允许清单之外的任何端口。

---

## 1. 启动参数(§3.3)

| 参数 | 环境变量 | 含义 | 必填 |
|---|---|---|---|
| `--url` | `TUNNEL_URL` | 接入面 WS 地址,含 `/ws`(如 `wss://caddy.example.com:8443/ws`) | 是 |
| `--key` | `TUNNEL_KEY` | 明文鉴权密钥;服务端通过 key 反查节点身份。**明文模式**下使用 | 二选一 |
| `--secret` | `TUNNEL_SECRET` | HMAC 签名密钥(对应服务端节点 `hmac_secret`);**不发送明文 key**,启用后必须给 `--node` | 二选一 |
| `--node` | `TUNNEL_NODE` | 节点名;明文模式省略时取 key 前 8 位;HMAC 模式**必填** | HMAC 模式必填 |

```bash
# 明文模式(兼容旧服务端)
tunnel-agent --url wss://caddy.example.com:8443/ws --key xx1

# HMAC 模式(服务端 node 配置了 hmac_secret)
tunnel-agent --url wss://caddy.example.com:8443/ws --node node1 --secret <hmac_secret>
```

> 收益:边缘 agent 零配置、无需重新分发;所有路由/容量/端口策略集中在 Caddy 配置。**不要**给 client 加端口/通道数等本地配置项,否则违背设计意图。

---

## 2. 技术栈(建议与服务端一致)

```
Go            1.25+
WS 库         github.com/coder/websocket     # 二进制流模式,纯字节管道
多路复用       github.com/xtaci/smux          # 须与服务端一致(可切 yamux,但两端必须同款)
协议契约       对照服务端 modules/tunnel/proto(消息结构 / 流头编解码 / 版本常量) 服务端地址 github.com/wjzhangq/caddy-ws-tunnel-plugin
```

> ⚠️ **smux 库与版本、流头 version 常量必须与服务端严格一致**,否则握手或开流必失败。建议把服务端 `proto` 子包作为依赖或镜像同步,不要手抄。

---

## 3. 建议目录结构(client 仓库内)

```
tunnel-agent/
├── go.mod
├── client_dev.md            # 本文(或链接)
├── cmd/tunnel-agent/
│   └── main.go              # 解析 --url/--key、装配并启动 agent、信号处理
└── internal/agent/
    ├── agent.go             # 生命周期:控制+数据通道建立、重连退避、状态机
    ├── control.go           # 控制通道:hello/welcome、ping/pong、stats、reload_config、drain、bye
    ├── data.go              # 数据通道:WS→net.Conn→smux、AcceptStream 循环
    ├── forward.go           # 单流处理:读流头 → 查清单 → dial 本地 → 双向 io.Copy
    ├── allowlist.go         # 端口允许清单(welcome/reload_config 下发),并发安全热更
    ├── auth.go              # HMAC 签名生成(node|ts|nonce)
    └── proto/               # 协议契约:对照服务端 proto 子包(消息结构 + 流头编解码 + version)
```

---

## 4. 协议契约(client 必须严格遵守)

### 4.1 握手时序(§4.3)

```
1. (client→) 开第一条 WS → hello{role:"control", node, key, ts, nonce, sig}
2. (←server) welcome{session, heartbeat, mux, channels, config{ports,...}}
   ★ 初始配置只在 welcome 内;不会再单独发 config 帧。client 必须在此读取 config。
3. (client→) 按 welcome.channels 并发开 N 条数据 WS:
   每条 hello{role:"data", node, key|(HMAC 模式留空), session, channel_id, ts, nonce, sig}
   —— 数据通道的 hello **同样必须签名**(服务端 HMAC 模式会校验)
4. (←server) welcome{channel_id} → 在该 WS 上启动 smux(client 侧 AcceptStream)
```

**强制要求:**
- 数据通道 hello **必须带 control 阶段拿到的 `session`**;不带或带错会被服务端拒绝(防孤立数据连接注入)。
- 数据通道 hello 在 HMAC 模式下**必须带新的 `ts/nonce/sig`**(不要复用控制通道的 nonce,否则被当重放拒绝)。
- **不要等待独立 `config` 帧**——初始配置内嵌在 `welcome`。等待会导致握手永远卡住。
- `channel_id` 在 `0 .. channels-1` 范围内,逐条建立。

### 4.2 hello / 鉴权(§10)

两种姿态,按服务端节点配置选择:

| 姿态 | 触发 | hello 字段 | 说明 |
|---|---|---|---|
| 明文 key(默认) | 节点未配 `hmac_secret` | `key` 明文 + `ts/nonce/sig`(可选,服务端忽略) | 必须配 `wss`;仍建议照常签名 |
| HMAC 挑战应答 | 节点配了 `hmac_secret` | `ts`、`nonce`、`sig`;**`key` 留空** | secret 不上线;`sig = hex(HMAC-SHA256(secret, "node|ts|nonce"))` |

- `ts` 为 Unix 秒,服务端要求 `|now - ts| ≤ 60s`(客户端需 NTP 正确)。
- `nonce` 为一次性随机串(建议 16 字节 hex,长度 1..128),**每条 hello 都要新生成**;服务端有界缓存(4096 / 60s)拒绝重放。
- 签名密钥:显式 `--secret` 优先,否则用明文 `--key`;HMAC 模式下 `hello.Key` 必须为空,避免 secret 上线。
- 服务端对 HMAC 节点**不接受**明文 key 降级;鉴权失败 WS close reason 只回通用 `auth failed`。

### 4.3 流头(§6.2)— 仅读

```
┌──────────┬──────────────────────┬──────────────────────┐
│ ver (1B) │ 端口 id (varint 数字) │  HTTP 请求原始字节…    │
└──────────┴──────────────────────┴──────────────────────┘
```

client 永远是**读流头**方(服务端写):
1. `AcceptStream` 后,先读 `ver`(校验 = 1,proto/version)。
2. 读 varint 得到**数字**端口 id(没有命名端口)。
3. 在允许清单解析为本地地址;**不在清单 → 立即关流**(服务端据此回 502)。
4. dial 本地地址,流头之后是裸字节,双向 `io.Copy(smuxStream <-> localConn)`。

### 4.4 控制指令(§5)client 侧职责

| type | 方向 | client 行为 |
|---|---|---|
| `hello` | client→ | 控制/数据通道握手时发送(见 4.1/4.2) |
| `welcome` | →client | 解析 session/heartbeat/mux/channels/config;**初始配置来源** |
| `reload_config` | →client | 全量替换允许清单/容量参数,**不断连热更**(allowlist 并发安全替换) |
| `ping`/`pong` | 双向 | 按 `heartbeat` 间隔在控制通道收发,维持保活与 RTT(数据通道保活交给 smux) |
| `stats` | client→ | 周期上报 `channels/streams/bytes_in/out/local_dial_errors` 等遥测 |
| `drain` | →client | 收到后**停止接受新流**,等存量流跑完再关(优雅停机) |
| `bye` | 双向 | 优雅关闭通告 |
| `error` | 双向 | 收到即按 `code/msg` 处理(鉴权失败/未知端口/配置非法等) |

---

## 5. 按里程碑实现(对齐服务端 `dev.md` §15,client 视角)

> client 实现应与服务端里程碑配套推进,以便联调。每个里程碑给**做什么 + 自测标准**。

### C1 单流打通(配服务端 M1)
- 实现:控制通道 hello/welcome、读 config;建 1 条数据通道(WS→net.Conn→smux);`AcceptStream` → 读流头 → dial 本地 → `io.Copy`。
- **自测**:连本地跑的测试 Caddy,外部 `curl` 经隧道打到本地 echo,响应正确回流。

### C2 多流(配服务端 M2)
- 实现:单数据通道上并发处理多个 `AcceptStream`;允许清单校验(不在清单关流)。
- **自测**:并发 50 请求全部成功;访问清单外端口 id → client 关流(外部得 502)。

### C3 固定池(配服务端 M3)
- 实现:按 `welcome.channels` 建满固定条数;每条独立跑 smux AcceptStream 循环。
- **自测**:`channels=N` 时恰好建 N 条数据通道,服务端在线集显示 N 条 online。

### C4 遥测(配服务端 M4)
- 实现:周期发 `stats`(channels/streams/bytes_in_out/local_dial_errors),喂服务端 `/status`。
- **自测**:服务端 `/status` 的 traffic/streams 数值与 client 实际转发量一致。

### C5 健壮性(配服务端 M5)
- 实现:
  - **重连退避**:指数 `1s → 30s`;控制通道断 → 重连 → 重走控制握手 → 补足固定 `channels` 条数据通道。
  - **HMAC 鉴权**:控制通道与**每条数据通道**的 hello 都生成新的 `ts/nonce/sig`;`--secret` 模式不发送明文 key。
  - **reload_config 热更**:不断连替换允许清单/容量。
  - **drain 优雅停机**:停新流、等存量、再关。
  - 本地 dial 失败/端口不可达 → 关流并计入 `local_dial_errors`。
- **自测**:
  - 断网/杀连接后能在退避节奏内自动重连并补齐通道。
  - 服务端推 `reload_config` 改端口清单 → client 不断连即生效。
  - 收到 `drain` 后不再 Accept 新流,存量流跑完才退出。
  - 重放同一 nonce 的 hello 被服务端拒(验证 client 每次换 nonce)。

### C6 压测配合(配服务端 M6)
- 配合服务端做 HOL/配比压测;client 侧关注本地 dial 并发、io.Copy 缓冲、goroutine 数。

---

## 6. 故障与重连(§11 client 侧)

| 场景 | client 处理 |
|---|---|
| 控制通道断 | 指数退避重连(`1s→30s`);重连后重走控制握手并补足固定 `channels` 条。服务端在宽限窗口(`heartbeat×3`)内保留在途流,client 尽快重连可避免在途请求被中断 |
| 某数据通道断 | 单独重连补回该通道;其上在途流会中断(外部得 502),client 无需特殊补偿 |
| 端口 id 不在清单 | 立即关流(不要 dial);服务端回 502 |
| 本地端口不可达 | dial 失败 → 关流 → `local_dial_errors++` → 经 stats 上报 |
| 收到 `drain` | 停接新流、等存量流结束、再关闭通道 |
| 收到 `error` | 按 code 处理:鉴权失败类应停止重试或告警,而非死循环重连 |

**重连要点:**
- 退避**带抖动**,避免大量 client 同时重连打雷群效应。
- 鉴权类 `error`(如 key 失效)**不要无脑重连**,应退避更久或退出并告警。
- 重连成功后,数据通道数量必须精确回到服务端下发的 `channels`,不多不少。

---

## 7. 易错点清单(client Review 必查)

- [ ] **不等独立 config 帧**:配置只在 `welcome` 里,等待会卡死握手。
- [ ] **数据通道必带 session**:用 control 阶段拿到的 session,否则被拒。
- [ ] **每次握手换 nonce**:重连也要换,否则被 LRU 当重放拒绝。
- [ ] **数据通道 hello 也签名**(HMAC 模式):控制通道签了不等于数据通道签了。
- [ ] **控制通道读循环不要用 per-read 超时**:`coder/websocket` 的 `Read(ctx)` 在 ctx 取消时会关闭连接;用独立读 goroutine + `select` 复用(见 §10)。
- [ ] **端口 id 是数字**:varint 解码为数字,无命名端口;清单外一律关流不 dial。
- [ ] **只 AcceptStream,不 OpenStream**:流方向单一,client 永不主动开流。
- [ ] **流头读全再 io.Copy**:先读完 `ver`+varint 端口 id,剩余字节才是裸数据。
- [ ] **smux/version 与服务端一致**:库、版本、流头 version 常量必须对齐 proto 子包。
- [ ] **数据通道保活靠 smux**:别用 `heartbeat` 驱动数据通道;`heartbeat` 只管控制通道 ping/pong。
- [ ] **重连补齐固定条数**:精确补回 `channels` 条。
- [ ] **重连带抖动 + 鉴权错不死循环**。
- [ ] **drain 要等存量流**:不能收到 drain 就硬关。
- [ ] **WS 用二进制消息**:纯字节管道,不要用文本帧。
- [ ] **goroutine/连接不泄漏**:每条流、每个 io.Copy 方向都要可靠收尾(用 `goleak` 自测)。

---

## 8. client 侧参数(本地实现,非服务端下发)

下列是 client 自身行为参数,**不**由服务端下发,client 项目自定义合理默认:

| 参数 | 建议默认 | 说明 |
|---|---|---|
| 重连退避 | `1s → 30s` 指数 + 抖动 | 控制/数据通道重连 |
| 本地 dial 超时 | 与服务端 `dial_timeout` 协调(≤ 它) | dial 本地端口超时 |
| io.Copy 缓冲 | 32KB 量级 | 视吞吐调优 |
| stats 上报间隔 | `heartbeat` 同量级或其倍数 | 遥测频率 |

> 服务端下发的 `channels` / `max_streams_per_conn` / `heartbeat` / `dial_timeout` / `queue_timeout` / `ports` 等,client **只接收不自定义**(可热更)。

---

## 9. 与服务端联调清单

- [ ] 服务端按 `dev.md` §6 起 `test/Caddyfile.test`。
- [ ] 明文模式:client 连 `wss://localhost:8443/ws --key xx1`。
- [ ] HMAC 模式:服务端节点加 `hmac_secret`,client 用 `--secret <同值> --node node1`。
- [ ] 服务端 `/status` 应显示该 node `up:true`、control connected、channels online = 配置值。
- [ ] 外部 `curl node1.localhost` 经隧道到达 client 本地端口并正确回流。
- [ ] 改服务端 Caddyfile 端口清单触发 `reload_config`,client 不断连生效。
- [ ] 观察 ≥ 3×`heartbeat`:服务端 `/status` 的 `control.connected` 不应周期掉线,`data_reconnect_total` 不应增长(稳定性回归)。
- [ ] 观察 `/status` 的 `client` 块出现(说明 client `stats` 帧被服务端接收)。

---

## 10. 实现记录(v1.1.0)

1. **HMAC 完整支持**:新增 `--secret` / `TUNNEL_SECRET`;控制通道与数据通道的 hello 都用新的 `ts/nonce` 签名;`--secret` 模式不发送明文 key;新增 `--version`。实现见 `internal/agent/auth.go` 的 `newSignedHello` / `helloKey` / `hmacSigner`,契约测试见 `auth_test.go`(固定向量由 `openssl` 独立生成)。
2. **修复控制通道周期性重建**:旧实现用 `context.WithTimeout` 包 `conn.Read` 来复用 ping/stats ticker;`coder/websocket` 在 ctx 取消时会关闭连接,导致控制通道每 `heartbeat/2` 断开,并连带重建全部数据通道(线上表现为持续 502/抖动)。现改为独立读 goroutine + `select` 多路复用(`internal/agent/control.go`);本机实测 12s 观察窗口内 `control channel dropped` 次数为 0。
3. **Windows 友好**:`os.Interrupt`(Ctrl+C)纳入信号处理;`version` 支持 `-ldflags -X main.version=...` 注入并在 `--version` 打印;release workflow 已覆盖 windows/amd64 与 arm64。

