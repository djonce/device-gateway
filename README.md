# Light Gateway

Light Gateway 是一套面向 ESP、Android、Orange Pi Zero 2W 和 Linux 节点的轻量设备接入平台。后端使用 Go，前端管理台使用 React + UmiJS。

## 功能

- 设备注册、心跳和在线状态计算。
- 设备 Token 鉴权、Token 重置、设备禁用/启用。
- 遥测数据上报和最近数据查询。
- 命令入队、设备长轮询拉取、执行回执。
- SQLite 持久化，默认数据文件 `data/light-gateway.db`。
- Linux/Orange Pi Agent：注册、心跳、命令拉取、白名单 Shell、日志采集。
- 串口开发板 Agent：通过 USB 串口把本地开发板桥接到网关。
- 最近事件流。
- React/Umi 管理台：设备列表、状态概览、设备详情、Token 管理、命令下发、遥测和事件查看。

## 本地启动

启动后端：

```bash
go run ./cmd/gateway
```

启动前端：

```bash
pnpm --dir web dev
```

默认地址：

- 后端 API: `http://127.0.0.1:7001`
- 前端管理台: `http://127.0.0.1:8000`

## Linux / Orange Pi Agent

本机调试：

```bash
go run ./cmd/agent \
  -gateway http://127.0.0.1:7001 \
  -id opi-zero2w-001 \
  -name "Orange Pi Zero 2W"
```

交叉编译 Orange Pi Zero 2W 常见的 Linux ARM64 版本：

```bash
GOOS=linux GOARCH=arm64 go build -o light-agent ./cmd/agent
```

复制到设备后，可用 `scripts/install-linux-agent.sh` 安装 systemd 服务。Agent 首次注册会把平台返回的设备 Token 写入 `LIGHT_AGENT_TOKEN_FILE`，默认是 `data/agent-token`；systemd 安装脚本使用 `/var/lib/light-gateway/agent-token`。

Agent 当前支持的命令：

- `system.info`: 返回主机名、系统、架构、CPU 数、进程号。
- `shell.exec`: 只执行白名单命令，默认 `uptime,df,free,uname,whoami,pwd,date`。
- `log.collect`: 读取 `/var/log/` 或 `/tmp/` 下文件尾部。

## USB 串口开发板接入

如果开发板通过 CH340/CP210x/USB CDC 串口插在电脑上，可以用串口桥接 Agent 先接入网关，不要求板子已经烧录 Light Gateway 固件。

自动检测串口：

```bash
go run ./cmd/serial-agent
```

指定当前这块 WCH/CH340 开发板：

```bash
go run ./cmd/serial-agent \
  -gateway http://127.0.0.1:7001 \
  -port /dev/cu.wchusbserial110 \
  -baud 115200 \
  -id devboard-wchusbserial110 \
  -name "USB Serial Dev Board"
```

串口 Agent 会注册一个 `esp` 类型设备，并声明：

- `serial.line`: 从串口读到的文本行会上报为遥测。
- `serial.write`: 从网关向串口写入文本，payload 示例 `{"data":"help","newline":true}`。
- `serial.recent`: 返回桥接 Agent 最近缓存的串口文本行。

## ESP32 Wi-Fi 直连固件

ESP32 开发板推荐烧录 Wi-Fi Agent，让开发板自己通过局域网访问网关，不依赖电脑串口桥接。

固件目录：

```bash
firmware/esp32-wifi-agent
```

编译：

```bash
"/Applications/Arduino IDE.app/Contents/Resources/app/lib/backend/resources/arduino-cli" \
  compile --fqbn esp32:esp32:esp32 firmware/esp32-wifi-agent
```

上传：

```bash
"/Applications/Arduino IDE.app/Contents/Resources/app/lib/backend/resources/arduino-cli" \
  upload --fqbn esp32:esp32:esp32 --port /dev/cu.wchusbserial110 firmware/esp32-wifi-agent
```

首次启动后，开发板会启动配置热点：

```text
LightGateway-<chip-id>
```

连接该热点后，手机或电脑通常会自动弹出配置页。配置页会扫描周围 Wi-Fi，SSID 可以从列表里选，也可以手动输入隐藏网络。

如果没有自动弹出，手动打开：

```text
http://192.168.4.1
```

填写 Wi-Fi SSID、密码和网关地址。当前 Mac 在局域网内的网关地址是：

```text
http://192.168.3.109:7001
```

配置保存后，开发板会重启并以 `esp32-<chip-id>` 注册到网关。

## ESP32 小夜灯 / RGB 灯带固件（category=light）

面向具体产品的固件，注册为 `light` 类别并绑定 `light.v1` 能力档案，实现 `light.*` 命令（开关、亮度、颜色、灯效、定时）。

```bash
arduino-cli compile --fqbn esp32:esp32:esp32 firmware/esp32-light
arduino-cli upload  --fqbn esp32:esp32:esp32 --port /dev/cu.wchusbserial110 firmware/esp32-light
```

详见 [firmware/esp32-light/README.md](firmware/esp32-light/README.md)。在管理台选中该设备会显示「灯光控制」面板，可直接开关、调色、调亮度、切灯效、设定时。

## ESP32 时钟 / 天气屏固件（category=clock）

注册为 `clock` 类别（profile `clock.v1`）。时间与天气走网关内容接口 `GET /api/v1/content/clock?lat&lon&tz`，由网关代理 [Open-Meteo](https://open-meteo.com)（免费、无需 API key，密钥不下发到设备），设备定时拉取；控制台也可即时下发 `time.sync` / `weather.push` 命令刷新。

```bash
arduino-cli compile --fqbn esp32:esp32:esp32 firmware/esp32-clock
arduino-cli upload  --fqbn esp32:esp32:esp32 --port /dev/cu.wchusbserial110 firmware/esp32-clock
```

详见 [firmware/esp32-clock/README.md](firmware/esp32-clock/README.md)。管理台选中该设备显示「时钟 / 天气屏」面板：切换显示模式、调屏幕亮度、按经纬度获取天气并推送。

## ESP32 GPS 追踪器固件（category=gps）

注册为 `gps` 类别（profile `gps.v1`）。从 UART GPS 模块读 NMEA 上报 `gps.fix` 遥测。**地理围栏进出判定在网关侧**：设备上报位置后，网关用 haversine 算距离判断是否在围栏内，跨越边界产生 `geofence.enter` / `geofence.exit` 事件。

```bash
arduino-cli compile --fqbn esp32:esp32:esp32 firmware/esp32-gps
arduino-cli upload  --fqbn esp32:esp32:esp32 --port /dev/cu.wchusbserial110 firmware/esp32-gps
```

相关接口：`GET /api/v1/devices/{id}/track?limit=N` 查询轨迹，`POST /api/v1/devices/{id}/geofence`（body `{centerLat,centerLng,radiusM}`）设置围栏（同时下发 `geofence.set` 命令）。详见 [firmware/esp32-gps/README.md](firmware/esp32-gps/README.md)。管理台选中该设备显示「GPS 定位 / 轨迹」面板：轻量 SVG 轨迹图 + 围栏圈、实时位置与围栏状态、调上报频率、设围栏。

## 实时通道 / 语音（小智）

语音设备走 WebSocket 实时通道（`internal/realtime`，标准库自实现，无外部依赖），而非命令长轮询。设备建连 `GET /api/v1/devices/{id}/ws`（设备 Token 鉴权），收发 JSON 信封协议。

对话/转写/合成由**可插拔管线**决定：默认回显占位；配置环境变量后接真实 LLM/ASR/TTS（OpenAI 兼容 `chat/completions` + 可选 ASR/TTS 端点，密钥只在网关）：

```bash
LIGHT_VOICE_LLM_URL=https://api.openai.com/v1/chat/completions \
LIGHT_VOICE_LLM_KEY=sk-... LIGHT_VOICE_LLM_MODEL=gpt-4o-mini \
go run ./cmd/gateway
```

设备端固件见 [firmware/esp32-voice/](firmware/esp32-voice/)（ESP32-S3 + I2S 麦克风/扬声器 + 按键说话）。控制台「小智 / 语音」面板可看连接状态、推送播报、切唤醒。协议、环境变量与测试方法详见 [docs/realtime-voice.md](docs/realtime-voice.md)。

## 自动化规则 + 通知

让网关从"记录器"变成"会反应的中枢"：遥测/事件触发 → 下发命令或 Webhook 通知。

- **触发器**：遥测阈值（`key op value`，op=gt/gte/lt/lte/eq/ne）或事件类型（如 `geofence.exit`），可加 deviceId/category 过滤。
- **动作**：下发命令（给触发设备 / 指定设备 / 整类广播）或 Webhook（POST JSON 含触发上下文）。
- **求值**：纯函数 `internal/rules.Evaluate` 决定动作；Store 在遥测/事件处挂钩，仅当有启用规则时异步执行；触发自身的 `command.*`/`rule.fired`/`telemetry.received` 等被 denylist 排除以防环。规则触发会记 `rule.fired` 事件。
- 管理员接口：`GET/POST /api/v1/rules`、`POST …/{id}/enable`、`DELETE …/{id}`；管理台「自动化规则」面板可建/列/启停/删。

例：「`env.temp > 30` → 给 `light` 类广播 `light.power {on:true}`」，或「`geofence.exit` → Webhook 通知」。

## 可观测性 / 监控

- `GET /metrics`（公开，Prometheus 文本，零依赖手写）：设备按在线状态/类别计数、禁用数、实时连接数、存储遥测点数、固件数，以及注册/遥测/命令/回执/事件累计计数器。可直接被 Prometheus 抓取。
- `GET /api/v1/stats`（管理员）：同一份快照的 JSON，给管理台舰队仪表盘用。
- `GET /api/v1/devices/{id}/telemetry/series?key=&bucket=&limit=`（管理员）：实时 raw 分桶聚合（最近 ≤500 条）。
- `GET /api/v1/devices/{id}/telemetry/history?key=&from=&to=`（管理员）：**长期历史**，从 rollup 卷叠按时间范围自动选分辨率（1m/1h/1d）。
- 管理台顶部「舰队概览」面板，设备视图内「遥测趋势」SVG 折线图（零图表库），范围选「实时/1h/24h/7d/30d」。

### 长期时序保留（rollup 降采样）

raw 遥测每设备只留最近 500 条（实时细节）。数值遥测同时被**降采样**进 `telemetry_rollup` 表（每设备每指标 min/max/avg/last），保留更久：1m 卷叠留 48h、1h 留 30 天、1d 留 1 年——存储是常数级而非随上报频率线性增长。写入是增量 upsert（无批处理任务），保留过期由网关每分钟 `PruneRollups` 滚动清理。查询按时间范围自动选分辨率（窗口越宽分辨率越粗，每次只返回 ≤1000 桶）。仅适用于标量数值指标（rssi/temp/brightness/speed 等；`gps.fix` 等对象走轨迹接口）。

```bash
curl -s http://127.0.0.1:7001/metrics
```

## 跨端 SDK

`sdk/` 是零依赖 TypeScript SDK，封装功能 API（设备/命令/遥测/档案/围栏轨迹/OTA/语音 + 各品类语义化控制助手）、设备配网（SoftAP 门户）与实时语音会话，可在浏览器/Node/React Native/Electron 复用。原生 Android/iOS 照协议做薄封装即可。详见 [sdk/README.md](sdk/README.md)。

```bash
cd sdk && npm run build && npm test
```

## OTA 固件管理

管理员上传固件二进制（按 category/model + 版本，存元数据 + SHA-256 + blob），给设备设目标版本或一键灰度整类；设备轮询 `GET /api/v1/devices/{id}/ota` 发现更新后，带设备 Token 下载、校验 SHA-256、刷写重启。

- 管理员接口：`POST /api/v1/firmware?category=&version=`（body 为二进制）上传、`GET /api/v1/firmware` 列表、`POST /api/v1/firmware/{id}/rollout` 灰度、`POST /api/v1/devices/{id}/ota/target` 设目标。
- 设备接口（设备 Token）：`GET /api/v1/devices/{id}/ota` 查询、`GET /api/v1/devices/{id}/firmware/{fwId}/download` 下载。
- 管理台「固件 / OTA」面板：看当前/目标版本、上传固件、设目标、灰度。
- 设备端 OTA 例程见 [firmware/esp32-light/](firmware/esp32-light/)（可移植到其他固件）。

注意：OTA 目标与地理围栏等服务端状态在设备重启重新注册时会被保留（不被设备上报覆盖）。

## 设备注册预配密钥

默认任何人都能调 `POST /api/v1/devices/register` 注册设备并拿 Token。设置 `LIGHT_PROVISION_KEY` 后，注册必须满足其一：请求头带正确的 `X-Provision-Key`，**或**持有效管理员会话（这样控制台/管理端仍可注册）。未设置则保持开放（启动告警）。

```bash
LIGHT_PROVISION_KEY=你的预配密钥 go run ./cmd/gateway
```

设备端带密钥：
- SDK：`new LightGatewayClient({ baseUrl, provisionKey })`，`registerDevice` 自动带头。
- Go Agent：`-provision-key`（或 `LIGHT_AGENT_PROVISION_KEY` / `LIGHT_SERIAL_PROVISION_KEY`）。
- ESP 固件：配网门户的「Provision Key」字段（esp32-light 已接入；clock/gps/voice 固件在 `httpJSON` 注册请求里加一行 `X-Provision-Key` 头即可，与 esp32-light 一致）。

## 管理后台登录鉴权

运营接口（设备列表/详情、下发命令、Token 重置、启停、围栏、轨迹、事件、实时状态/播报、profiles）需要管理员会话；设备数据面（register、heartbeat、telemetry、commands/next、ack、ws）仍用设备 Token，与管理员登录无关。

管理员账号从环境变量读取，设置密码即启用鉴权：

```bash
LIGHT_ADMIN_USER=admin LIGHT_ADMIN_PASSWORD=请改成强密码 go run ./cmd/gateway
```

- 启用后：管理台显示登录页；登录成功发 24 小时会话 Token（前端存 localStorage，请求带 `Authorization: Bearer`）。`POST /api/v1/auth/login` 取 Token，`/api/v1/auth/status` 查询是否需要登录，`POST /api/v1/auth/logout` 注销。
- 未设密码：**开放模式**（本地开发方便），网关启动告警，管理台跳过登录。对外部署务必设置密码。

> 注意：`scripts/smoke.sh` 默认针对开放模式；若启用了管理员鉴权，命令创建等运营接口需要带 Token。

## 角色 + API 密钥（RBAC，轻量）

运营接口按**角色**分级（在管理员鉴权启用时生效）：

- **viewer**：只读（设备/遥测/趋势/事件/仪表盘/轨迹/列表）。
- **operator**：viewer + 写操作（下发命令、设围栏、OTA 设目标/灰度、规则增删、固件上传、启停、重置设备 Token）。
- **admin**：operator + 管 API 密钥。

交互式管理员（`LIGHT_ADMIN_*`）登录即 admin 角色。**API 密钥**给机器用（脚本/CI/看板/集成）：管理员创建、绑角色、可吊销，明文只在创建时返回一次，存哈希。把密钥当 `Authorization: Bearer <key>` 即可，其角色决定能调哪些接口。

- 接口：`GET/POST /api/v1/apikeys`（admin）、`DELETE /api/v1/apikeys/{id}`（admin）。
- 管理台「API 密钥」面板：建（选角色）/ 列 / 吊销，新密钥明文一次性显示。
- SDK：`client.createApiKey/listApiKeys/deleteApiKey`；机器侧 `new LightGatewayClient({ baseUrl, adminToken: <apiKey> })` 即可用 key 鉴权。

## 能力档案

`GET /api/v1/profiles` 返回内置能力档案（`light.v1 / clock.v1 / gps.v1 / voice.v1`）。设备注册时带 `category`（如 `light`）会自动绑定默认档案，平台据此校验命令类型，管理台据此渲染对应控制面板。无档案的设备（如串口/Linux 桥接 Agent）不受命令限制。

## 测试

```bash
go test ./...
pnpm --dir web typecheck
pnpm --dir web build
```

如果本地环境限制 Go 写入用户缓存，可以把缓存放到项目内：

```bash
GOCACHE="$PWD/.cache/go-build" go test ./...
```

## API 冒烟

先启动后端，再运行：

```bash
BASE_URL=http://127.0.0.1:7001 scripts/smoke.sh
```

该脚本会完成健康检查、设备注册、遥测上报、命令创建、命令拉取和命令回执。

## 持续集成 (CI)

`.github/workflows/ci.yml` 在每次 push / PR 上并行跑三个 job：

- **go**：`gofmt`（报告）+ `go vet` + `go build` + `go test ./...`（Go 版本按 `go.mod` 取）。
- **web**：`pnpm install --frozen-lockfile` + `pnpm typecheck` + `pnpm build`。
- **sdk**：`npm install` + `npm test`（tsc 类型检查 + `node --test`，零额外运行时依赖）。

本地一键复跑全部：

```bash
scripts/test-all.sh
```

## 安装部署

完整的构建、配置、生产部署（systemd + 反向代理 + TLS + Docker）、设备接入与运维指南见 [docs/deployment.md](docs/deployment.md)。

## 设计文档

设备接入模型、协议决策和后续扩展点见 [docs/access-model.md](docs/access-model.md)；整体架构与各阶段演进见 [docs/architecture-replan-v2.md](docs/architecture-replan-v2.md)；实时语音协议见 [docs/realtime-voice.md](docs/realtime-voice.md)。
