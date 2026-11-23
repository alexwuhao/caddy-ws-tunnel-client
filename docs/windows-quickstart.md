# Windows 客户端快速上手（tunnel-agent）

> 目标：10 分钟内在 Windows 上跑通 `tunnel-agent`，把本地一个端口通过隧道暴露到公网 Caddy。
> 本文命令均在 Windows 10/11 上实测通过（tunnel-agent v1.1.0 + caddy-ws-tunnel-plugin）。

## 0. 先理解两个角色

```
浏览器/外部 → Caddy（公网服务器，装了 tunnel 插件） → WebSocket 隧道 → Windows 上的 tunnel-agent.exe → 127.0.0.1:你的服务
```

- **服务端**：Caddy + [`caddy-ws-tunnel-plugin`](https://github.com/wjzhangq/caddy-ws-tunnel-plugin)，负责公网入口。
  必须是 `xcaddy build` 编译进去的 Caddy，官方 Caddy 不带这个插件。
- **客户端**：`tunnel-agent.exe`，主动外连 Caddy（NAT/防火墙后不用开入站端口）。
- 客户端**不需要配置端口映射**：端口清单、通道数、心跳等全部由服务端握手时下发。

## 1. 下载客户端

从 Releases 下载对应架构的二进制：

- `tunnel-agent-windows-amd64.exe`（绝大多数 Windows 机器）
- `tunnel-agent-windows-arm64.exe`（ARM 版 Windows）

https://github.com/alexwuhao/caddy-ws-tunnel-client/releases

> HMAC 模式要求 `tunnel-agent >= v1.1.0`（更早版本只签控制通道，会被服务端拒绝数据通道）。

## 2. 5 分钟自测（同一台电脑，不需要公网/域名/证书）

### 2.1 准备一个本地服务

```powershell
mkdir C:\demo
# 在 C:\demo 放一个 index.html（内容随意），然后：
cd C:\demo
python -m http.server 18080 --bind 127.0.0.1
```

也可以直接用你现有的服务（比如某程序监听 `127.0.0.1:8080`），把下面配置里的地址换掉即可。

### 2.2 新建服务端配置 `Caddyfile`

```caddyfile
{
	admin off
	auto_https off
}

# ① 接入面：Windows 客户端连这里
:18443 {
	route /ws* {
		tunnel_server {
			node node1 {
				key xx1
				channels 2
				# 左边的 8080 是“端口 id”（外部寻址用），右边是 Windows 本地真实地址
				port 8080 127.0.0.1:18080
			}
		}
	}
	route /status* {
		tunnel_status
	}
}

# ② 业务入口：访问这个端口 = 经隧道打到 Windows 本地
:18081 {
	reverse_proxy tunnel {          # tunnel 只是占位 upstream，必须写，否则 503
		transport tunnel node1:8080 # 对应上面的端口 id
	}
}
```

> **易错点**：`reverse_proxy` 后面必须跟一个非空 upstream（名字随意，如 `tunnel`）。
> Caddy 只有在上游列表非空时才会调用自定义 transport；写成 `reverse_proxy { transport tunnel ... }`
> 会被判定“没有上游”，直接返回 **503**，transport 根本不会执行。

### 2.3 启动服务端

```powershell
caddy-tunnel.exe run --config Caddyfile
```

### 2.4 启动客户端（另开一个窗口）

```powershell
.\tunnel-agent-windows-amd64.exe --url ws://127.0.0.1:18443/ws --key xx1 --node node1 --debug
```

### 2.5 验证

- 浏览器打开 **http://127.0.0.1:18081/** → 应看到 Windows 本地的页面。
- 查看状态：**http://127.0.0.1:18443/status**

```powershell
Invoke-RestMethod http://127.0.0.1:18443/status | ConvertTo-Json -Depth 5
```

正常情况下：

```json
"up": true,
"control": { "connected": true },
"channels": { "configured": 2, "online": 2 },
"traffic": { "bytes_in": 1947, "rate_in_bps": 486 }
```

## 3. 连接真实公网服务器

服务端改成域名 + TLS（Caddy 自动申请证书），客户端 URL 换成 `wss://`：

```caddyfile
# 业务入口（443）
app.example.com {
	reverse_proxy tunnel {
		transport tunnel node1:8080
	}
}

# 客户端接入 + 状态（8443）
caddy.example.com:8443 {
	route /ws* {
		tunnel_server {
			node node1 {
				key 换成你的随机密钥
				channels 4
				port 8080 127.0.0.1:8080
			}
		}
	}
	route /status* {
		tunnel_status {
			auth_token 换成随机 token   # 建议加，避免状态页裸奔
		}
	}
}
```

Windows 客户端：

```powershell
.\tunnel-agent-windows-amd64.exe --url wss://caddy.example.com:8443/ws `
  --key 换成你的密钥 --node node1
```

- 服务器防火墙放行 **443**（业务）与 **8443**（客户端接入）。
- 需要暴露更多端口就继续加 `port <id> 127.0.0.1:<本地端口>`，外部用 `node1:<id>` 寻址。

### HMAC 模式（公网推荐）

服务端节点：

```caddyfile
node node1 {
	hmac_secret "用 openssl rand -hex 32 生成的 32 字节随机串"
	channels 4
	port 8080 127.0.0.1:8080
}
```

客户端（**>= v1.1.0**，且必须写 `--node`）：

```powershell
.\tunnel-agent-windows-amd64.exe --url wss://caddy.example.com:8443/ws `
  --node node1 --secret "同一个随机串"
```

## 4. 开机自启 / 后台常驻

普通 exe 不能用 `sc create`（不响应 SCM）。两种做法：

```powershell
# 方式 1：NSSM（推荐，崩溃自动重启）
nssm install tunnel-agent "C:\tools\tunnel-agent.exe" `
  "--url" "wss://caddy.example.com:8443/ws" "--key" "你的密钥" "--node" "node1"
nssm set tunnel-agent AppStdout "C:\tools\tunnel-agent.log"
nssm set tunnel-agent AppStderr "C:\tools\tunnel-agent.err.log"
nssm start tunnel-agent

# 方式 2：任务计划程序（开机自启）
$action = New-ScheduledTaskAction -Execute "C:\tools\tunnel-agent.exe" `
  -Argument '--url wss://caddy.example.com:8443/ws --key 你的密钥 --node node1'
Register-ScheduledTask -TaskName "tunnel-agent" -Action $action `
  -Trigger (New-ScheduledTaskTrigger -AtStartup) -RunLevel Highest -User "SYSTEM"
```

## 5. 参数速查

| 参数 | 环境变量 | 说明 |
|---|---|---|
| `--url` | `TUNNEL_URL` | 服务端 WS 地址，**必须带 `/ws`**（`ws://…/ws` 或 `wss://…/ws`） |
| `--key` | `TUNNEL_KEY` | 明文密钥（服务端节点用 `key` 时） |
| `--secret` | `TUNNEL_SECRET` | HMAC 密钥（服务端节点用 `hmac_secret` 时），**必须配 `--node`** |
| `--node` | `TUNNEL_NODE` | 节点名；明文模式可省略（取 key 前 8 位），HMAC 模式必填 |
| `--debug` | — | 详细日志，排查用 |
| `--version` | — | 打印版本 |

## 6. 常见问题

| 现象 | 原因 / 解决 |
|---|---|
| 业务端口 **503**，`/status` 里 `channels.online=0` | 客户端没连上：检查 URL 是否含 `/ws`、端口、密钥；客户端开 `--debug` 看日志 |
| `channels.online>0` 但业务端口仍 **503** | 服务端 `reverse_proxy` 没写占位 upstream，必须写 `reverse_proxy tunnel { transport tunnel … }` |
| **502** | 隧道通了但 Windows 本地端口不可达：`port 8080` 默认指 `127.0.0.1:8080`；服务在别的端口就写 `port 8080 127.0.0.1:18080` |
| 客户端 `connection refused` / `404` | `ws`/`wss` 与 TLS 配置不匹配；URL 路径必须是 `/ws` |
| HMAC 模式立刻断开 | 客户端 < v1.1.0；或 secret 与服务端 `hmac_secret` 不一致；或漏了 `--node` |
| `address already in use` | 端口被占用，换端口（接入端口与业务端口都要换） |
| 没有域名但想 HTTPS | 测试可用 `tls internal`，但客户端不信任自签证书；生产建议用域名让 Caddy 自动签发 |

## 7. 排查工具

```powershell
# 1) 看版本
.\tunnel-agent-windows-amd64.exe --version

# 2) 前端调试日志（重点看 handshake / reconnect / local dial failed）
.\tunnel-agent-windows-amd64.exe --url ws://... --key ... --node ... --debug

# 3) 看服务端状态（把 URL 换成你的接入地址）
Invoke-RestMethod http://127.0.0.1:18443/status | ConvertTo-Json -Depth 6

# 4) 直连本地服务，确认它本身是通的
Invoke-WebRequest http://127.0.0.1:18080/ -UseBasicParsing
```

排查顺序：**本地服务 → 本地端口 → 客户端握手 → 服务端 /status → 业务入口**，逐段排除。

## 8. 附录：服务端部署参考（Ubuntu + 已有 nginx）

如果服务器上 80/443 已被 nginx 占用，可以让 **Caddy 只监听 127.0.0.1**，由 nginx 把公网流量转进来（不用改防火墙、不用新端口，TLS 沿用 nginx 的证书）。

### 8.1 Caddy 配置（只监听回环）

```caddyfile
{
	admin off
	auto_https off
}

http://127.0.0.1:18443 {
	route /ws* {
		tunnel_server {
			node node1 {
				hmac_secret "openssl rand -hex 32 生成"
				channels 2
				port 8080 127.0.0.1:8080
			}
		}
	}
	route /status* {
		tunnel_status
	}
}

http://127.0.0.1:18081 {
	reverse_proxy tunnel {
		transport tunnel node1:8080
	}
}
```

### 8.2 systemd

```ini
# /etc/systemd/system/caddy-tunnel.service
[Unit]
Description=Caddy WS Tunnel server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=/opt/caddy-tunnel
ExecStart=/opt/caddy-tunnel/caddy-tunnel run --config /opt/caddy-tunnel/Caddyfile
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
```

### 8.3 nginx 反代（示例：复用已有 8443 TLS 站点）

```nginx
# 客户端接入：wss://your.domain:8443/tunnel/ws
location = /tunnel/ws {
    proxy_pass http://127.0.0.1:18443/ws;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection $connection_upgrade;
    proxy_read_timeout 86400s;
    proxy_send_timeout 86400s;
    proxy_buffering off;
}

# 状态（可选）
location ^~ /tunnel/status {
    proxy_pass http://127.0.0.1:18443/status;
}

# 业务入口：https://your.domain:8443/tunnel/app/ → 隧道 → 客户端本地端口
location ^~ /tunnel/app/ {
    proxy_pass http://127.0.0.1:18081/;
    proxy_set_header Host $host;
    proxy_read_timeout 3600s;
    proxy_buffering off;
}
```

改完：`nginx -t && systemctl reload nginx`。客户端 URL 就是
`wss://your.domain:8443/tunnel/ws`（注意用 `wss`，因为 nginx 这层是 TLS）。

