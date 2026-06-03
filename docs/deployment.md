# Light Gateway 安装与部署指南

本文覆盖从源码构建、配置、本地开发到生产部署（systemd + 反向代理 + TLS）、设备接入与日常运维。

---

## 1. 架构与组件

一次完整部署包含三类东西：

| 组件 | 是什么 | 默认端口 | 进程 |
| --- | --- | --- | --- |
| **网关 gateway** | Go 单二进制，HTTP API + 实时 WebSocket + SQLite | `:7001` | 一个常驻进程 |
| **管理台 web** | UmiJS/React 静态站点（构建产物 `web/dist`） | 开发 `:8000` | 静态文件，由反向代理或静态服务器托管 |
| **设备 / Agent / SDK** | ESP 固件、Linux/串口 Agent、各端 SDK | — | 连到网关 |

数据全部落在**一个 SQLite 文件**（默认 `data/light-gateway.db`，含设备/Token/遥测/rollup/命令/事件/固件 blob/规则/API 密钥）。无外部数据库、无外部依赖。

网关只对外提供 `/api/*`、`/health`、`/metrics`、WebSocket（`/api/v1/devices/{id}/ws`）；**它不托管管理台静态文件**——生产环境由反向代理同源托管 `web/dist` 并把 `/api`、`/health`、`/metrics` 反代到网关。

---

## 2. 前置依赖

| 用途 | 依赖 | 版本 |
| --- | --- | --- |
| 构建/运行网关与 Agent | Go | ≥ **1.25**（见 `go.mod`） |
| 构建管理台 | Node + pnpm | Node **20** + pnpm **9** |
| 烧录 ESP 固件（可选） | arduino-cli + esp32 core | 任意近版 |
| 反向代理 + TLS（生产推荐） | Caddy 或 nginx | 任意近版 |

---

## 3. 从源码构建

```bash
# 网关与 Agent（产物在仓库根目录）
go build -o light-gateway ./cmd/gateway
go build -o light-agent ./cmd/agent
go build -o light-serial-agent ./cmd/serial-agent

# 交叉编译 Orange Pi / ARM64 Linux 节点的 Agent
GOOS=linux GOARCH=arm64 go build -o light-agent-arm64 ./cmd/agent

# 管理台静态产物（输出到 web/dist）
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
```

根目录也提供聚合脚本：`pnpm build`（构建三个二进制 + web）。构建前可先 `scripts/test-all.sh` 跑一遍全测（go + web + sdk）。

---

## 4. 配置（环境变量）

### 网关

| 变量 | 默认 | 说明 |
| --- | --- | --- |
| `LIGHT_GATEWAY_ADDR` | `:7001` | 监听地址 |
| `LIGHT_GATEWAY_DATA` | `data/light-gateway.db` | SQLite 数据文件路径（`""` 为纯内存，仅测试） |
| `LIGHT_GATEWAY_WEB` | （空=不托管） | 设为管理台 `dist` 目录则网关同进程托管前端（单容器/单进程部署） |
| `LIGHT_ADMIN_USER` | `admin` | 管理员用户名 |
| `LIGHT_ADMIN_PASSWORD` | （空=开放模式） | **设置后才强制管理后台登录**；不设则任何人可调运营接口（启动告警） |
| `LIGHT_PROVISION_KEY` | （空=开放注册） | **设置后才要求设备注册带预配密钥**；不设则任何人可注册设备 |
| `LIGHT_VOICE_LLM_URL` | （空=回显占位） | OpenAI 兼容 `chat/completions` 端点（启用语音对话） |
| `LIGHT_VOICE_LLM_KEY` | — | LLM Bearer Key |
| `LIGHT_VOICE_LLM_MODEL` | `gpt-4o-mini` | 模型 |
| `LIGHT_VOICE_PROMPT` | 内置 | 系统提示词 |
| `LIGHT_VOICE_ASR_URL` | （空=占位） | 语音转写端点 |
| `LIGHT_VOICE_TTS_URL` | （空=不合成） | 语音合成端点 |
| `LIGHT_VOICE_CODEC` | `pcm16` | 通告音频编解码（`pcm16`/`opus`） |

> **生产至少要设** `LIGHT_ADMIN_PASSWORD` 和 `LIGHT_PROVISION_KEY`，否则管理与注册均为开放模式。

### Agent（Linux / 串口）

`cmd/agent`：`-gateway`/`LIGHT_AGENT_GATEWAY`、`-id`/`LIGHT_AGENT_ID`、`-name`/`LIGHT_AGENT_NAME`、`-token-file`/`LIGHT_AGENT_TOKEN_FILE`、`-provision-key`/`LIGHT_AGENT_PROVISION_KEY`、`-heartbeat`、`-allowed-commands`。
`cmd/serial-agent`：`-gateway`、`-port`、`-baud`、`-id`、`-name`、`-token-file`、`-provision-key`/`LIGHT_SERIAL_PROVISION_KEY`。

---

## 5. 本地开发

```bash
# 终端 1：网关
go run ./cmd/gateway

# 终端 2：管理台（开发服务器，自动把 /api、/health 代理到 127.0.0.1:7001）
pnpm --dir web dev      # http://127.0.0.1:8000
```

冒烟测试：`BASE_URL=http://127.0.0.1:7001 scripts/smoke.sh`。

---

## 6. 生产部署

### 6.1 目录与数据

```bash
sudo useradd --system --home /opt/light-gateway --shell /usr/sbin/nologin lightgw
sudo mkdir -p /opt/light-gateway/data /etc/light-gateway
sudo cp light-gateway /opt/light-gateway/
sudo cp -r web/dist /opt/light-gateway/web-dist
sudo chown -R lightgw:lightgw /opt/light-gateway
```

`/etc/light-gateway/gateway.env`（权限设 `600`，含敏感值）：

```ini
LIGHT_GATEWAY_ADDR=:7001
LIGHT_GATEWAY_DATA=/opt/light-gateway/data/light-gateway.db
LIGHT_ADMIN_USER=admin
LIGHT_ADMIN_PASSWORD=请改成强密码
LIGHT_PROVISION_KEY=请改成强预配密钥
# 可选语音：
# LIGHT_VOICE_LLM_URL=https://api.openai.com/v1/chat/completions
# LIGHT_VOICE_LLM_KEY=sk-...
```

### 6.2 systemd 运行网关

`/etc/systemd/system/light-gateway.service`：

```ini
[Unit]
Description=Light Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=lightgw
WorkingDirectory=/opt/light-gateway
EnvironmentFile=/etc/light-gateway/gateway.env
ExecStart=/opt/light-gateway/light-gateway
Restart=on-failure
RestartSec=3
# 加固
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=/opt/light-gateway/data

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now light-gateway
sudo systemctl status light-gateway --no-pager
journalctl -u light-gateway -f
```

### 6.3 反向代理 + TLS（推荐 Caddy）

网关是明文 HTTP，**生产务必在前面终结 TLS**。Caddy 自动签证书、自动处理 WebSocket 升级，同源托管管理台。`/etc/caddy/Caddyfile`：

```caddy
gateway.example.com {
    encode gzip

    # API / 健康检查 / 指标 / WebSocket -> 网关
    @backend path /api/* /health /metrics
    reverse_proxy @backend 127.0.0.1:7001

    # 管理台（SPA 静态）
    handle {
        root * /opt/light-gateway/web-dist
        try_files {path} /index.html
        file_server
    }
}
```

> 想让 `/metrics` 仅内网可见：把 `@backend` 里去掉 `/metrics`，Prometheus 直接抓 `127.0.0.1:7001/metrics`。

nginx 等价要点：`location /api/`、`/health`、`/metrics` `proxy_pass http://127.0.0.1:7001;`，且对 `/api/` 加 `proxy_set_header Upgrade $http_upgrade; proxy_set_header Connection "upgrade";`（WebSocket）；其余 `try_files $uri /index.html;` 托管 `web-dist`。

### 6.4 Docker 一键启动（推荐）

仓库自带 `Dockerfile`（多阶段：构建管理台 + 编译网关 → distroless 运行时）与 `docker-compose.yml`。**镜像里的网关一个进程就同时服务 API 和管理台**（通过 `LIGHT_GATEWAY_WEB` 托管前端静态），无需再单独跑 Caddy。

```bash
cp .env.example .env        # 填上 LIGHT_ADMIN_PASSWORD / LIGHT_PROVISION_KEY
docker compose up -d --build
# 打开 http://<主机>:7001  （管理台 + API 同源同端口）
docker compose logs -f gateway
```

- 数据持久化在命名卷 `gwdata`（`/data/light-gateway.db`）；备份即备份该卷。
- 健康检查用 `light-gateway healthcheck` 子命令（distroless 无 curl，二进制自检 `/health`）。
- compose 自动读取同目录 `.env`；`.env` 已在 `.dockerignore` 里，不会进镜像。

**生产对外（公网 + TLS）**：上面是单容器、明文 7001，适合内网/家用。要上公网，仍建议前置 TLS——再加一个 Caddy 容器反代 `gateway:7001`（WebSocket 自动升级）：

```yaml
  caddy:
    image: caddy:2
    ports: ["443:443", "80:80"]
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - caddydata:/data
# Caddyfile: gateway.example.com { reverse_proxy gateway:7001 }
```

此时 Caddy 把所有路径（含 `/api`、`/metrics`、WebSocket、管理台）整体反代到网关即可，因为网关自身已托管前端。

### 6.5 不用 Docker 的单容器替代

非容器环境想要"一个进程服务全部"，给 systemd 的 `gateway.env` 加 `LIGHT_GATEWAY_WEB=/opt/light-gateway/web-dist` 即可——网关会托管管理台，省掉反向代理的静态托管部分（仍建议前置 TLS）。

---

## 7. 安全清单（上线前过一遍）

- [ ] 设 `LIGHT_ADMIN_PASSWORD`（强密码）——否则管理接口开放。
- [ ] 设 `LIGHT_PROVISION_KEY`——否则任何人可注册设备拿 Token。
- [ ] 前置 TLS（Caddy/nginx），不要把 `:7001` 明文直接暴露公网。
- [ ] 决定 `/metrics` 是否对外（默认公开，可改为仅内网抓取）。
- [ ] `gateway.env` 权限 `600`；数据目录归属 `lightgw`。
- [ ] 给集成/看板发**最小权限的 API 密钥**（viewer/operator），不要共享管理员会话。
- [ ] 定期备份 SQLite（见 §9）。

---

## 8. 设备接入

### 8.1 ESP 固件（灯/钟/GPS/语音）

```bash
arduino-cli compile --fqbn esp32:esp32:esp32 firmware/esp32-light
arduino-cli upload  --fqbn esp32:esp32:esp32 --port /dev/cu.xxx firmware/esp32-light
```

首启进入配置热点 `LightGateway-<chip-id>`，连上后访问 `http://192.168.4.1` 的门户，填 Wi-Fi、网关地址、（启用了预配密钥时）Provision Key，时钟/GPS 还可填时区/经纬度。保存后设备重启并以对应 `category` 注册。语音设备见 `firmware/esp32-voice/`，时序保留/规则等无需设备改动。

### 8.2 Linux / Orange Pi Agent

```bash
GOOS=linux GOARCH=arm64 go build -o light-agent ./cmd/agent
# 复制到设备后，安装 systemd 服务（脚本在 scripts/install-linux-agent.sh）
sudo LIGHT_AGENT_GATEWAY=https://gateway.example.com \
     LIGHT_AGENT_ID=opi-zero2w-001 \
     ./scripts/install-linux-agent.sh
```

脚本把二进制装到 `/opt/light-gateway/light-agent`、Token 存 `/var/lib/light-gateway/agent-token`、注册为 `light-gateway-agent.service`。启用了预配密钥时，给该 service 追加 `Environment=LIGHT_AGENT_PROVISION_KEY=<key>`（或运行脚本前 export）。

### 8.3 SDK（程序化接入）

```ts
const gw = new LightGatewayClient({ baseUrl: 'https://gateway.example.com', provisionKey: '<key>' });
const { token } = await gw.registerDevice({ id: 'sensor-1', name: 'Sensor 1', type: 'esp' });
```

机器/集成用 **API 密钥**鉴权：管理员在控制台「API 密钥」面板按角色创建，机器侧 `new LightGatewayClient({ baseUrl, adminToken: '<apiKey>' })`。

---

## 9. 运维

**备份**：数据是单个 SQLite 文件（WAL 模式，运行时还有 `-wal`/`-shm`）。冷备最稳：

```bash
sudo systemctl stop light-gateway
sudo cp /opt/light-gateway/data/light-gateway.db* /backup/
sudo systemctl start light-gateway
```

若装了 `sqlite3` CLI，可在线热备：`sqlite3 light-gateway.db ".backup /backup/light-gateway.db"`。

**监控**：Prometheus 抓 `/metrics`（设备按状态/类别计数、实时连接、累计计数器等）：

```yaml
scrape_configs:
  - job_name: light-gateway
    static_configs: [{ targets: ['127.0.0.1:7001'] }]
```

管理台「舰队概览」和「遥测趋势」（实时/1h/24h/7d/30d）提供内置可视化，长期历史由 rollup 降采样保留（1m 留 48h、1h 留 30d、1d 留 1y）。

**健康检查**：`curl -fsS https://gateway.example.com/health` → `{"ok":true,...}`。

**固件 OTA**：管理台「固件 / OTA」上传新固件二进制（或 `POST /api/v1/firmware`），给设备设目标版本或一键灰度整类；设备每分钟轮询、自动下载校验 SHA-256 后刷写重启。

**日志**：`journalctl -u light-gateway -f`（结构化 slog）。

---

## 10. 升级网关本身

```bash
git pull
go build -o light-gateway ./cmd/gateway
pnpm --dir web install --frozen-lockfile && pnpm --dir web build
sudo cp light-gateway /opt/light-gateway/
sudo rm -rf /opt/light-gateway/web-dist && sudo cp -r web/dist /opt/light-gateway/web-dist
sudo chown -R lightgw:lightgw /opt/light-gateway
sudo systemctl restart light-gateway
```

数据库表结构用 `CREATE TABLE IF NOT EXISTS` 自动迁移，升级无需手动改库。建议升级前先备份数据文件。

---

## 11. 故障排查

| 现象 | 可能原因 |
| --- | --- |
| 注册返回 401 | 设了 `LIGHT_PROVISION_KEY` 但设备没带对 `X-Provision-Key`（或无管理员会话） |
| 运营接口 403 | 用的是 viewer/operator API 密钥，权限不足；密钥管理需 admin |
| 运营接口 401 | 设了管理员密码但未登录/Token 过期；机器侧未带有效 API 密钥 |
| 管理台能打开但调接口失败 | 反向代理没把 `/api` 正确反代到网关，或同源配置不对 |
| 语音 WebSocket 连不上 | 代理未透传 `Upgrade`/`Connection` 头（Caddy 自动，nginx 需手动加） |
| 设备一直 offline | 心跳没上来：检查网关地址、Token 文件、网络；超 10 分钟无心跳即 offline |
| 趋势图「暂无数据」 | 该指标还没积累 rollup（需设备持续上报数值遥测；`gps.fix` 等对象不入趋势，走轨迹） |
| go test/CI 失败 | 见 `.github/workflows/ci.yml`；本地 `scripts/test-all.sh` 复跑 |
