# Light Gateway 架构重新规划方案（v2）

> 本轮重点：**ESP 设备类型的产品化重构**。同时给出整体目标架构，作为后续手机/PC SDK、管理后台演进的统一框架。
> 现有代码（Go 网关 + SQLite + React 管理台 + ESP32/串口/Linux Agent）保留，本方案以「增量演进」而非推倒重来为原则。

## 1. 背景与目标

当前 Light Gateway 已经跑通了一条最小可用链路：设备注册 → 心跳 → 遥测 → 命令下发/回执 → 管理台可视化。但接入侧是「按 Agent 形态」拼出来的（ESP32 Wi-Fi 固件、USB 串口桥接、Linux/Orange Pi Agent），设备类型只有泛化的 `esp / android / orangepi / linux-node / unknown`，并没有把白板上规划的具体产品（小夜灯、时钟天气屏、GPS、小智）作为一等公民建模。

白板架构把系统重新分成四层、四类终端，并新增一套跨端 SDK（网络配置 + 功能 API）。本方案要解决三件事：

1. **统一目标架构**：把网关、管理后台、四类终端、SDK 的边界画清楚，避免后续每接一个设备都重写一套逻辑。
2. **ESP 设备产品化（本轮核心）**：从「一个泛化 esp 类型」升级到「设备类型 + 产品型号 + 能力档案（capability profile）」的模型，让四款 ESP 设备各自有清晰的能力、命令、遥测契约。
3. **识别架构升级点**：尤其是小智（语音助手）对实时通道的需求，现有「HTTP 长轮询」模型无法直接满足，需要提前在架构层面预留。

本方案的非目标（留待后续轮次）：手机/PC SDK 的完整实现、管理员登录与权限体系、OTA。这些在第 6 节路线图里标注为横切项。

## 2. 现状梳理（基于现有代码）

### 2.1 后端网关（Go）

- 纯 Go 标准库 `net/http`，路由见 `internal/device/api.go`，监听 `:7001`。
- 持久化在 `internal/device/store.go`：默认 SQLite（`data/light-gateway.db`），`.json` 结尾时退化为 JSON 快照模式；测试用内存。
- 已实现接口：注册 `register`、列表/详情、Token 重置、启用/禁用、心跳 `heartbeat`、遥测 `telemetry`、命令 `commands`（创建/列表/拉取 next/回执 ack）、事件流 `events`、健康检查。
- 设备鉴权：`X-Device-Token` 或 `Authorization: Bearer`，平台只存 SHA-256 摘要，Token 明文仅在注册/重置时返回一次。
- 命令模型：平台入队 → 设备长轮询 `commands/next?timeout=N`（服务端 500ms 轮询，最多 60s）→ 设备 `ack`。天然适配 NAT 后设备。
- 在线状态：2 分钟内 `online`，2–10 分钟 `stale`，超 10 分钟 `offline`。

### 2.2 设备类型与能力模型

- `DeviceType` 目前是枚举字符串：`esp / android / orangepi / linux-node / unknown`（见 `model.go`）。**没有产品型号维度**。
- 能力用 `Capability{name, description, schema}` 列表声明，但 `name` 是自由字符串（如 `sensor.read`、`gpio.write`、`shell.exec`、`serial.line`），**没有统一的命令/遥测 schema 约束**，校验全靠各端自觉。
- 命令 `payload` 和遥测 `value` 都是 `map[string]any` / `any`，灵活但无契约。

### 2.3 接入端现状

- **ESP32 Wi-Fi 固件**（`firmware/esp32-wifi-agent`）：自带 SoftAP 配网门户（`192.168.4.1`，扫描 SSID、填网关地址），配网后以 `esp` 类型注册，上报 `wifi.rssi / system.heap` 遥测，支持 `sensor.read / gpio.write` 命令。JSON 是手写字符串拼接 + 手写解析。
- **USB 串口桥接 Agent**（`cmd/serial-agent`）：电脑侧把开发板串口桥接进网关，能力为 `serial.line / serial.write / serial.recent`。
- **Linux / Orange Pi Agent**（`cmd/agent`）：systemd 服务，能力 `system.info / shell.exec（白名单）/ log.collect`。
- **管理台**（`web`，React + UmiJS）：设备列表、状态概览、详情、Token 管理、命令下发、遥测/事件查看。单页 `pages/index.tsx`。

### 2.4 已知局限（影响新设备落地）

- **SQLite 持久化是「全表删后重插」**：`persistSQLiteLocked()` 每次写入（每一次心跳、每一条遥测）都 `DELETE FROM` 五张表再全量 `INSERT`。设备数/遥测量一上来（尤其 GPS 高频定位、小智），这是首要性能瓶颈。
- 遥测内存里按设备只保留最近 200 条（`appendBounded`），无时序聚合/降采样。
- 命令通道是**单机内存 + 长轮询**，没有实时推送，无法支撑音频流式等低延迟场景。
- **没有管理员登录/权限**：管理台和 `/api` 任何人可调；设备鉴权有，平台侧鉴权没有。
- 设备分组、标签筛选、固件版本管理、OTA 均未实现。
- ESP 固件 JSON 手写拼接/解析，脆弱，难以承载结构化命令（如灯效、日程、天气数据）。

## 3. 目标架构

### 3.1 分层

白板可以收敛成四层加一条横切 SDK：

```
                          ┌─────────────────┐
                          │   管理后台 Web    │  运营/调试/下发/监控
                          └────────▲────────┘
                                   │ 管理 API（带登录鉴权）
                          ┌────────┴────────┐
                          │     网关 Gateway   │  注册/心跳/遥测/命令/事件
                          │  设备注册表 + 能力档案 │  + 实时通道(新增) + 设备服务(天气/时间)
                          └───┬───────┬───────┘
        设备接入(Token)        │       │        客户端接入(SDK)
        ┌────────────────────┘       └──────────────────────┐
        ▼                                                     ▼
┌───────────────┐   ┌───────────────┐   ┌───────────────┐   ┌───────────────┐
│  ESP 设备       │   │   手机           │   │   PC           │   │   PC Web        │
│ 灯带/小夜灯      │   │ Android / iOS   │   │ Win/Linux/Mac  │   │   浏览器         │
│ 时钟日历天气     │   │                │   │                │   │                │
│ GPS            │   │   ╔═══════════════════════════════╗      （复用 SDK 的     │
│ 小智(语音)      │   │   ║   跨端 SDK / 功能组件            ║       功能 API 层）    │
└───────────────┘   │   ║  ① 网络配置  ② 功能 API          ║                       │
                    │   ╚═══════════════════════════════╝                       │
                    └───────────────┘   └───────────────┘   └───────────────┘
```

四类终端的角色要分清：

- **ESP 设备**：被纳管的「物」。通过 Token 直连网关，上报遥测、执行命令。本轮重点。
- **手机 / PC / PC Web**：纳管者/控制端，也是 SDK 的宿主。它们不是被控设备，而是通过 **网络配置**（帮 ESP 设备配网/绑定）和 **功能 API**（查询设备、下发控制、订阅状态）与网关交互。
- **跨端 SDK**：把「配网协议」和「平台 API」封装成各平台一致的组件，避免每个 App 各写一遍。本轮只定义边界，不实现。

### 3.2 本轮要落的核心改动

1. 给设备模型加 **产品维度**：`category`（产品大类）+ `model`（具体型号）+ **capability profile**（能力档案/设备模板）。
2. 为四款 ESP 设备定义标准 **能力契约**（命令 type + payload schema、遥测 key + 单位）。
3. 在网关侧新增两类 **设备服务**：时间/天气代理（给时钟屏用）、位置/地理围栏（给 GPS 用）。
4. 预留 **实时通道**（WebSocket/MQTT）抽象，先不全量实现，但小智依赖它。

## 4. 重点：ESP 设备类型重新规划

### 4.1 从「泛化 esp」到「产品化设备」

建议把设备标识拆成三层，向后兼容现有 `type` 字段：

| 维度 | 字段 | 说明 | 示例 |
| --- | --- | --- | --- |
| 接入大类 | `type`（保留） | 决定鉴权/通道形态 | `esp` |
| 产品类别 | `category`（新增） | 决定能力档案 | `light` / `clock` / `gps` / `voice` |
| 具体型号 | `model`（新增） | 决定固件/UI 细节 | `esp-nightlight-v1` |

落到 Go 模型（`model.go`）的增量（示意，非最终代码）：

```go
type Device struct {
    // ... 现有字段保留 ...
    Category   string `json:"category,omitempty"`   // light / clock / gps / voice ...
    Model      string `json:"model,omitempty"`      // 具体型号
    Profile    string `json:"profile,omitempty"`    // 引用的能力档案 ID
    FwVersion  string `json:"fwVersion,omitempty"`  // 固件版本，为 OTA 预留
}
```

**能力档案（Capability Profile / 设备模板）** 是这次的关键抽象：把每类产品「支持哪些命令、命令 payload 长什么样、上报哪些遥测」固化成模板，注册时设备声明 `profile`，网关据此校验命令、在管理台渲染对应控制面板。模板可以先用 Go 内置常量 + 一份 JSON 清单，后续再做成可配置。

### 4.2 四款设备的能力契约

下面给出每款设备的命令（平台→设备）与遥测（设备→平台）建议契约。命令 `type` 采用 `domain.action` 命名，payload 字段尽量结构化。

**① ESP-灯带 / 小夜灯（`category=light`）** — 最简单，作为端到端模板首选。

- 命令：
  - `light.power` `{ "on": true }`
  - `light.brightness` `{ "value": 0-100 }`
  - `light.color` `{ "r":255, "g":180, "b":80 }` 或 `{ "hex":"#FFB450" }`
  - `light.effect` `{ "name":"breath|rainbow|static", "speed":1-10 }`
  - `light.schedule` `{ "on":"22:00", "off":"07:00" }`（定时小夜灯）
- 遥测：`light.state`（on/off）、`light.brightness`、`power.mw`（可选功耗）。
- 备注：纯命令驱动，长轮询完全够用；最适合先打通「设备模板 + 管理台控制面板」全链路。

**② ESP-时钟 / 日历 / 天气屏（`category=clock`）** — 引入网关侧「内容服务」。

- 命令：
  - `display.mode` `{ "mode":"clock|calendar|weather" }`
  - `display.brightness` `{ "value":0-100 }`
  - `time.sync` `{ "epoch":..., "tz":"Asia/Shanghai" }`
  - `weather.push` `{ "city":"...", "temp":..., "cond":"...", "forecast":[...] }`
- 遥测：`env.temp`、`env.humidity`（若带传感器）、`display.mode`。
- **网关新增能力**：设备本身不应直接去调第三方天气 API（密钥下发到固件不安全、固件改 API 很痛）。改为 **网关定时拉取天气/校时，主动 push 命令给屏**，或设备拉「内容接口」。这要求网关侧新增一个 `weather/time` 服务模块 + 定时任务。天气数据源需选型（见第 7 节）。

**③ ESP-GPS（`category=gps`）** — 高频遥测 + 地理能力。

- 遥测（主）：`gps.fix` `{ "lat":..., "lng":..., "alt":..., "speed":..., "sats":... }`，频率可能 1–10s 一次。
- 命令：`gps.interval` `{ "seconds":10 }`（调上报频率）、`geofence.set` `{ "center":[lat,lng], "radius_m":200 }`。
- **网关新增能力**：地理围栏判定（进/出区域产生事件告警）、轨迹存储与查询、管理台地图展示。
- **直接冲击现状**：高频定位会放大 2.4 节的「全表重插」持久化问题。**必须先重构遥测写入**（见 4.4），否则 GPS 单设备就能拖垮库。

**④ ESP-小智（语音助手，`category=voice`）** — 架构最难，需实时通道。

- 典型链路：唤醒 → 上行音频流 → ASR → LLM/对话 → TTS → 下行音频流，要求**双向、低延迟、长连接**。
- 现有「命令长轮询 + 一次性 ack」模型**不适用**：音频是流，不是一条命令。
- 需要新增 **实时通道**：WebSocket（或 MQTT + 音频走另一通道）。网关要么自己接 ASR/TTS/LLM，要么作为代理转发到对话后端。
- 建议：本轮**只做架构预留和接口定义**，把小智排到路线图最后阶段；前三款设备先在现有长轮询模型上跑通，验证设备模板体系。
- 参考：`小智` 与开源 xiaozhi-esp32 生态接近，可考虑对齐其「设备↔后端 WebSocket + Opus 音频」协议，降低固件侧工作量（需进一步调研确认，见第 7 节决策点）。

### 4.3 能力契约对照表

| 设备 | category | 主要命令 | 主要遥测 | 通道 | 网关侧新增 |
| --- | --- | --- | --- | --- | --- |
| 灯带/小夜灯 | `light` | power/brightness/color/effect/schedule | light.state, brightness | 长轮询(够用) | 设备模板 + 控制面板 |
| 时钟/日历/天气 | `clock` | display.mode/time.sync/weather.push | env.temp, display.mode | 长轮询 | 天气/校时服务 + 定时任务 |
| GPS | `gps` | gps.interval/geofence.set | gps.fix(高频) | 长轮询(需优化写入) | 地理围栏 + 轨迹 + 地图 |
| 小智 | `voice` | 对话/音频控制 | 音频流/会话事件 | **实时(WS/MQTT)** | 实时通道 + ASR/TTS/LLM 对接 |

### 4.4 支撑性重构（落任何新设备前先做）

1. **遥测写入改为增量 INSERT**：放弃「全表删后重插」，遥测/事件改为追加写 + 定期清理（或保留 N 条的滚动删除），命令/设备改为按行 upsert。这是 GPS 和小智的前置条件。
2. **能力档案机制**：内置四套 profile，注册时按 `category` 绑定，命令创建时按 profile 校验 `type` 与 payload。
3. **设备模板驱动的管理台**：管理台按 `category` 渲染不同控制面板（灯光调色盘、屏幕模式切换、地图、语音会话），而不是只有一个通用「发命令」表单。
4. **实时通道抽象**：定义 `Channel` 接口（长轮询实现 + WebSocket 实现），命令/事件经由统一抽象下发，为小智铺路。

## 5. 差距分析（现状 → 目标）

| 能力项 | 现状 | 目标 | 缺口/工作量 |
| --- | --- | --- | --- |
| 设备建模 | 仅 `type` 枚举 | type + category + model + profile | 中：模型字段 + 注册流程 + 迁移 |
| 能力契约 | 自由字符串，无校验 | 每类产品标准命令/遥测 schema | 中：定义 profile + 校验逻辑 |
| 灯光控制 | 仅通用 gpio.write | 结构化灯效/亮度/色彩/日程 | 低–中：固件 + 模板 + 面板 |
| 时钟天气 | 无 | 网关天气/校时服务 + 屏命令 | 中：新服务模块 + 数据源选型 |
| GPS | 无 | 高频定位 + 围栏 + 轨迹 + 地图 | 中–高：含写入重构 + 地图 UI |
| 小智语音 | 无 | 实时音频 + ASR/TTS/LLM | 高：实时通道 + 对话后端 |
| 持久化性能 | 全表删后重插 | 增量写入/滚动清理 | 中：Store 重写，**优先级最高** |
| 实时通道 | 仅长轮询 | WS/MQTT 抽象 | 中–高：通道接口 + 实现 |
| 管理后台鉴权 | 无登录 | 管理员登录 + 角色 | 中：横切，后续 |
| 跨端 SDK | 无 | 配网 + 功能 API 封装 | 高：横切，后续轮次 |
| OTA / 固件管理 | 无 | 版本管理 + 推送升级 | 中–高：后续 |

## 6. 分阶段实施路线图

按「先打地基、再由易到难逐款设备」排列。每阶段都应可独立验收。

**阶段 0 — 地基（必须先做）**
- 遥测/事件持久化改增量写入 + 滚动清理（解决全表重插）。
- 设备模型加 `category / model / profile / fwVersion`，注册接口与管理台兼容。
- 落地「能力档案」机制（内置 profile + 命令校验）。
- 验收：现有 ESP32 固件以 `category` 注册，老接口不回归。

**阶段 1 — 灯带 / 小夜灯（端到端模板）**
- 定义 `light` profile，固件实现 light.* 命令（建议引入轻量 JSON 库替代手写拼接）。
- 管理台做第一个「设备控制面板」（开关/亮度/调色/灯效/定时）。
- 验收：从管理台一键调色、设小夜灯定时，设备实时响应并回执。沉淀「新增一款设备」的标准流程文档。

**阶段 2 — 时钟 / 日历 / 天气屏**
- 网关新增天气/校时服务模块 + 定时任务，选定天气数据源。
- `clock` profile：display.mode / time.sync / weather.push。
- 管理台屏内容预览/模式切换面板。
- 验收：屏定时显示正确时间与当地天气，可远程切换模式。

**阶段 3 — GPS**
- 高频遥测落地（依赖阶段 0 写入重构），地理围栏判定 + 进出事件。
- 轨迹查询 API + 管理台地图（轨迹回放、围栏绘制）。
- 验收：地图实时显示位置，出围栏触发告警事件。

**阶段 4 — 小智（语音）**
- 实现 WebSocket 实时通道抽象，定义音频/会话协议（评估对齐 xiaozhi-esp32）。
- 对接 ASR/TTS/LLM（自建或第三方），固件音频采集/播放。
- 验收：唤醒后完成一轮「语音提问 → 语音回答」。

**横切项（与上述并行，按需插入）**
- 管理后台登录与角色权限（建议在阶段 1–2 之间补，因为要对外了）。
- 跨端 SDK（配网 + 功能 API），手机/PC/Web 复用。
- OTA 固件管理。

## 7. 风险与待决策点

- **实时通道选型**：WebSocket（简单、网关自持）vs MQTT（生态成熟、解耦，但多一个 broker 依赖）。小智决定上限，建议先做 WebSocket 抽象，MQTT 留作可选适配器（与现有「后续扩展」一致）。
- **天气数据源**：和风/OpenWeather/第三方聚合。密钥只放网关，不下发固件。需确认调用额度与商用授权。
- **小智协议**：是否对齐开源 xiaozhi-esp32 的设备↔后端协议与音频编码（Opus），以及对话后端是自建还是接第三方。需专门调研（涉及版权/许可，单独评估）。
- **持久化演进**：增量写入是止血；若设备/遥测规模继续增长，按既有规划引入 PostgreSQL（保留 Store 接口）和时序处理。
- **配网体验**：现有 ESP32 SoftAP 门户可用，但跨端 SDK 的「网络配置」若要统一（含 BLE 配网等），需要和固件侧约定统一配网协议。
- **安全**：管理后台目前无鉴权，对外前必须补登录；设备 Token 一次性返回的找回流程要在 UI 上讲清楚。

## 8. 建议的下一步

1. 确认本方案的设备分类（`light/clock/gps/voice`）与命令命名约定。
2. 先做 **阶段 0**（持久化重构 + 设备模型 + 能力档案），这是所有新设备的公共地基。
3. 用 **灯带/小夜灯** 跑通第一条「设备模板 → 固件 → 管理台控制面板」全链路，作为后续设备的样板。
4. 小智单独立项调研（实时通道 + 对话后端 + 协议对齐），不阻塞前三款设备推进。

## 9. 阶段 0 实现进展（已落地）

阶段 0 地基已实现，全部向后兼容（旧设备、旧 `data/light-gateway.db` 无需迁移）：

**设备模型产品化**（`internal/device/model.go`）：`Device` 与注册请求新增 `category`（light/clock/gps/voice）、`model`、`profile`、`fwVersion`。`type` 保留为接入大类，`category` 作为产品大类。

**能力档案机制**（新增 `internal/device/profile.go`）：内置四套 profile（`light.v1 / clock.v1 / gps.v1 / voice.v1`），各自声明允许的命令 type 与遥测 key。注册时按 `category` 自动绑定默认 profile；`CreateCommand` 对绑定了 profile 的设备做命令白名单校验，未知命令返回 400；无 profile 的旧设备保持「任意命令」行为。新增 `GET /api/v1/profiles` 供管理台发现契约。

**持久化重构**（`internal/device/store.go`）：删除「每次写入全表 DELETE + 全量 INSERT」的热路径，改为按行 `INSERT ... ON CONFLICT DO UPDATE`（设备/Token/命令）和 `INSERT`+滚动清理（遥测/事件）。遥测按设备保留最新 `telemetryPerDevice=500` 条，事件全局保留最新 `eventBacklog=1000` 条；清理用 `id`（编码创建 UnixNano、定宽）排序，避免 RFC3339Nano 字符串在整秒边界的误排序。

**前端类型**（`web/src/services/api.ts`）：补齐 `DeviceCategory`、`Device`/注册输入的新字段、`Profile` 类型与 `listProfiles()`。

**测试**：新增 `TestProfileEnforcesCommandAllowlist`（档案命令白名单 + 旧设备放行）与 `TestTelemetryRollingPruneOnSQLite`（写入超过上限后 DB 被裁剪到上限且保留最新）。注：本沙箱无 Go 1.25 工具链，未能跑 `go test`；但已用 SQLite 3.37 复刻表结构与全部 upsert/裁剪 SQL 验证逻辑正确（幂等 upsert、按设备裁剪保留最新 500、事件保留最新 1000）。落地到仓库后请运行 `GOCACHE="$PWD/.cache/go-build" go test ./...` 与 `pnpm --dir web typecheck` 复核。

**清理（按"全新设计、不背历史包袱"原则）**：
- 移除 JSON 快照存储模式，存储后端收敛为「SQLite（有数据路径）或纯内存（测试）」两种，删除散落在各处的快照刷盘调用。
- 读路径（`ListDevices`/`GetDevice`）改为只读锁、状态在返回副本上计算，不再回写内存。
- 鉴权（`VerifyDeviceToken`）每次只更新 token 行，不再冗余重写设备行——高频轮询更省。
- 新增命令历史滚动清理（每设备保留最新 `commandsPerDevice=200`，永不清理在飞命令 queued/delivered），避免命令无限增长。
- 删除未鉴权的 `POST /api/v1/commands/{id}/ack` 开放端点（任何人可 ack 任意命令的安全漏洞），统一走设备鉴权的 `…/devices/{id}/commands/{cmdId}/ack`。

## 10. 阶段 1 实现进展（灯带/小夜灯端到端，已落地）

第一款产品设备已端到端打通，作为后续设备的样板：

**固件**（新增 `firmware/esp32-light/`）：专用小夜灯/RGB 灯带固件，注册时声明 `type=esp, category=light, profile=light.v1, model=esp32-rgb-strip`，实现完整 `light.*` 命令：`light.power / light.brightness / light.color / light.effect / light.schedule`；上报 `light.state`、`light.brightness` 遥测。RGB 用 `analogWrite` 三路 PWM（不依赖外部库，WS2812 可一处替换）；`breath/rainbow` 灯效在主循环渲染；定时用 NTP 本地时间判断、支持跨夜窗口；颜色/亮度/开关持久化到 NVS。沿用 SoftAP 配网门户。

**管理台**（`web/src/pages/index.tsx`）：选中 `category=light` 设备时显示「灯光控制」面板——开关、亮度滑杆、取色器、灯效+速度、定时开关，直接下发对应 `light.*` 命令。新增一台演示灯具 `esp-nightlight-001`，点「Seed Demo」即可体验。`web typecheck` 通过。

**契约**：命令全部走 `light.v1` 档案校验（已有单测覆盖：`light.power` 放行、`gpio.write` 被拒）。

**沉淀的"新增一款设备"标准流程**：① `profile.go` 增一套 `<category>.v1` 档案（命令/遥测契约）→ ② 固件按 `category` 注册并实现命令 → ③ 管理台加一个 `category` 专属控制面板。阶段 2（时钟/天气屏）按此套路推进，额外只需在网关侧加天气/校时服务。

**下一步**：阶段 2（时钟/日历/天气屏）——`clock.v1` 档案 + 网关天气/校时服务模块 + 屏内容面板。

## 11. 阶段 2 实现进展（时钟/日历/天气屏，已落地）

第二款产品打通，并验证了"档案→固件→面板"三步对**需要网关侧服务**的设备同样适用。

**网关天气/校时服务**（新增 `internal/weather` 包）：`Service` 从 [Open-Meteo](https://open-meteo.com) 抓取天气（免费、无 key，密钥不下发设备），按经纬度缓存（默认 TTL 10min，抓取失败时回退上次缓存），WMO 天气码映射为中文。`fetch` 函数可注入，便于离线单测。新增内容接口 `GET /api/v1/content/clock?lat&lon&tz`，一次返回 `{time:{epoch,iso,tz}, weather:{tempC,humidity,text,daily[]}}`。`device.API` 通过构造参数持有该服务（`NewAPI(store, logger, weatherSvc)`），`cmd/gateway` 负责装配。

**路由模型**：例行内容走**设备拉取**（时钟每 10min 拉 `/content/clock`，无命令churn）；即时刷新走 `time.sync` / `weather.push` **命令**（控制台按钮触发）。这是"周期数据用拉、即时控制用推"的清晰分工。

**固件**（新增 `firmware/esp32-clock/`）：注册 `category=clock, profile=clock.v1`，定时拉内容接口显示时间/天气，处理 `display.mode / display.brightness / time.sync / weather.push`；`time.sync` 用 `settimeofday` 校时、NTP 兜底。画面先输出到串口，`renderScreen()` 留作真实 OLED/TFT 的接入点。

**管理台**（`web/src/pages/index.tsx`）：选中 `category=clock` 显示「时钟/天气屏」面板——模式切换（时钟/日历/天气）、屏幕亮度、按经纬度「获取天气并推送」（拉内容接口预览并下发 `time.sync`+`weather.push`）。新增演示设备 `esp-deskclock-001`。`web typecheck` 通过。

**验证**：天气缓存 TTL/回退、内容组装、WMO 映射、Open-Meteo 解析有单测覆盖；并用代表性 Open-Meteo 响应在外部校验了字段映射；`web typecheck` 通过。

**下一步**：阶段 3（GPS）——`gps.fix` 高频遥测（已受益于阶段 0 写入重构）、地理围栏进出事件、轨迹查询与管理台地图。

## 12. 阶段 3 实现进展（GPS，已落地）

第三款产品打通，验证了高频遥测路径与服务端事件判定。

**后端**（新增 `internal/device/geofence.go`）：`Geofence`（圆形，存于设备记录）+ `GeofenceState`。`AddTelemetry` 收到 `gps.fix` 时用 haversine 算到围栏中心距离，跨越边界产生 `geofence.enter` / `geofence.exit` 事件（同状态不重复发）。`SetGeofence` 设/清围栏；`ListTrack` 从 `gps.fix` 遥测解析出轨迹（按时间正序）。高频定位直接受益于阶段 0 的增量写入——不再每点重写全表。新增接口：`POST /api/v1/devices/{id}/geofence`（存服务端 + 下发 `geofence.set` 命令）、`GET /api/v1/devices/{id}/track`。

**固件**（新增 `firmware/esp32-gps/`）：注册 `category=gps, profile=gps.v1`，从 UART GPS 模块解析 `$..RMC` NMEA 得经纬度/速度，按 `gps.interval` 间隔上报 `gps.fix`；处理 `gps.interval`、`geofence.set` 命令。围栏判定在网关侧，设备只上报位置。

**管理台**（`web/src/pages/index.tsx`）：选中 `category=gps` 显示「GPS 定位 / 轨迹」面板——**无第三方地图库**的轻量 SVG 轨迹图（等距投影、围栏渲染为真圆）、实时位置与围栏状态、上报频率滑杆、设围栏（可"用当前位置"一键填中心）。新增演示设备 `esp-tracker-001`。

**验证**：haversine、围栏进出事件（含不重复）、轨迹解析有单测；用 Python 独立校验了 haversine 距离与测试用例的内外判定；`web typecheck` 通过。

**下一步**：阶段 4（小智/语音）——这是架构最大跳变，长轮询模型不适用，需要新增 WebSocket 实时通道抽象 + 音频/会话协议，并接 ASR/TTS/LLM。建议单独立项，前三款设备已可独立交付。

## 13. 阶段 4 实现进展（小智 / 语音，实时通道 + 占位协议，已落地）

按"先落实时通道抽象 + 占位协议、单独立项"推进，**不接真实 ASR/TTS/LLM**，但把通道、连接管理、协议、占位会话全链路打通。详见 [docs/realtime-voice.md](realtime-voice.md)。

**实时通道**（新增 `internal/realtime` 包）：用 **Go 标准库自实现最小 WebSocket**（RFC 6455 握手 + 帧读写 + 分片 + ping/pong/close），不引入外部 WS 库——延续整个网关的低依赖（也因此不动 go.mod/go.sum，你无需 `go mod tidy`）。`Hub` 按 `deviceId` 管连接：注册/路由/状态/非阻塞推送，重连顶旧连接，连接/会话事件经回调写入事件流。

**占位协议**（`lightgw.voice.v0`）：JSON 信封 `{type,seq,ts,payload}`。占位会话：`ping→pong`、`hello→welcome`、`text.input→asr.final+tts.say`（回显）、`audio.commit→占位回复`。把这些分支换成真实 `ASR→LLM→TTS` 即可，通道与信封不变。

**接入**：`GET …/ws`（设备 Token 鉴权后升级）、`GET …/realtime`（连接状态）、`POST …/realtime/say`（控制台让设备播报）。`cmd/gateway` 装配 Hub 并把事件回调接到 `store.RecordEvent`。

**管理台**：选中 `category=voice` 显示「小智 / 语音」面板——实时连接状态、向已连接设备推送播报（tts.say）、唤醒开关（voice.wake 命令）、占位说明。新增演示设备 `esp-xiaozhi-001`。`web typecheck` 通过。

**验证**：WS 握手 accept-key、掩码帧解码 / 服务端帧编码、Hub 路由与状态、占位会话回显均有单测；并用 Python 按 RFC 6455 独立校验了握手 key 与帧编解码（含 16/64 位长度、客户端掩码、服务端不掩码）。

**明确的后续（单独立项）**：① 设备端语音固件（ESP32-S3 WebSocket 客户端 + 麦克风/扬声器 + Opus）；② 真实流式 ASR / 对话 LLM / 流式 TTS，可评估对齐开源 xiaozhi-esp32；③ 背压/重连/心跳与音频分片时序。

至此四款 ESP 设备（灯带/小夜灯、时钟/天气屏、GPS、小智）在架构层面全部接入：前三款已端到端可用，小智的实时通道与协议骨架就绪、待接语音管线。

## 14. 阶段 4 续：可插拔语音管线 + 设备固件（已落地）

在实时通道之上接入了**真实管线能力**（仍零外部依赖、标准库实现）：

**可插拔管线**（新增 `internal/realtime/pipeline.go`）：`Pipeline` 接口 = `Transcribe`(ASR) + `Respond`(对话 LLM) + `Synthesize`(TTS)。默认 `echoPipeline` 回显占位；`httpPipeline` 按环境变量接 OpenAI 兼容 `chat/completions`（对话）与可选 ASR/TTS 端点，**密钥只在网关、不下发设备**。未配置的环节回退占位（只配 LLM 时 `text.input` 对话已可真跑）。`cmd/gateway` 按 `LIGHT_VOICE_*` 环境变量装配。

**Hub 接管线**（`hub.go`）：`dispatch` 把控制类消息（ping/hello/audio.append/reset）同步处理，把管线类（`text.input` / `audio.commit`）放到 goroutine 异步跑，慢 ASR/LLM/TTS 不阻塞读循环。新增会话音频缓冲（`audio.append` 累积、`audio.commit` 触发 ASR→LLM→TTS）、`tts.audio`（base64 PCM）回传、各环节失败回 `error`。

**设备固件**（新增 `firmware/esp32-voice/`，ESP32-S3）：HTTP 注册拿 Token → 开 WebSocket（**客户端帧按 RFC 6455 掩码**、`X-Device-Token` 头鉴权）；I2S 麦克风采集 + 扬声器播放；BOOT 键按住说话（PCM16/16kHz 经 `audio.append` 流式上传，松开 `audio.commit`）；收 `tts.audio` 播放、`tts.say`/`asr.final` 打印；串口直接输入文本可发 `text.input` 测对话链路。

**验证**：管线选择（echo 默认 / 注入式 fake）、`text.input` 与 `audio.commit` 全流程（asr.final→tts.say→tts.audio）、Hub 路由与状态均有单测；用 Python 按 RFC 6455 与 OpenAI/ASR 响应结构独立校验了握手 key、掩码帧编解码、LLM/ASR 字段映射与音频 base64 往返。

至此「档案→固件→面板」的设备接入范式 + 实时语音管线全部就位；剩余主要是设备端音频保真（Opus/流式）与管线流式化等工程深化，可按 §13/§14 的"后续"逐步推进。

## 15. 横切项：管理后台登录鉴权（已落地）

补上了架构评审反复标注的安全缺口（此前 `/api` 与管理台任何人可调）。新增 `internal/auth`：单管理员账号从环境变量 `LIGHT_ADMIN_USER/PASSWORD` 读取，登录用 constant-time 比对，发随机会话 Token（只存 SHA-256 哈希、24h 过期），零外部依赖。

**端点分层**：①公开（health、auth/login、auth/status、设备注册、天气内容）②设备 Token（heartbeat/telemetry/commands-next/ack/ws，设备数据面）③管理员会话（设备列表/详情、下发命令、Token 重置、启停、围栏、轨迹、事件、实时状态/播报、profiles，经 `a.admin()` 包装）。设备数据面与管理员登录解耦——设备不登录。

**开放模式**：未设密码时鉴权关闭（本地开发方便），启动告警；`/api/v1/auth/status` 让前端自适应是否显示登录页。

**管理台**：登录门（用户名/密码）+ Bearer Token 注入（localStorage 持久化）+ 401 自动回登录页 + 退出按钮。

**验证**：登录成功/失败、会话校验、过期、登出、开放模式均有单测（`internal/auth`）；`web typecheck` 通过。

后续可加：操作员角色/RBAC、API Key、设备注册预配密钥、审计日志。

## 16. 横切项：OTA 固件管理（已落地）

补齐了固件远程升级闭环（仍零外部依赖）。

**固件仓库**（新增 `internal/device/firmware.go`）：`Firmware`（category/model/version/size/sha256/notes）+ 二进制 blob，存 SQLite（元数据 JSON + BLOB 列，启动一并加载）。`AddFirmware`（算 SHA-256/大小）、`ListFirmware`、`FirmwareBlob`、`SetDeviceTarget`、`RolloutFirmware`（按类别批量设目标）、`ResolveOTA`（目标版本 + 类别/型号匹配 + 当前≠目标 ⇒ 有更新，返回下载地址）。

**接口分层**：管理员——上传/列表/灰度/设目标；设备 Token——`GET …/ota` 查询、`GET …/firmware/{id}/download` 下载（设备域内鉴权）。

**修复的架构缺陷**：原 `Register` 每次重新注册都从零重建设备记录，会**抹掉服务端管理的状态**（OTA 目标、地理围栏）。已改为重新注册时保留 `Geofence/GeofenceState/TargetFwVersion`——否则设备每次重启都会丢掉 OTA 目标导致升级无法完成、围栏失效。

**设备端 OTA**（`firmware/esp32-light` 参考实现）：每分钟轮询 `…/ota`，有更新则下载、流式校验 SHA-256、`Update` 刷写、重启、以新版本重新注册。例程可原样移植到其余固件。

**管理台**：「固件 / OTA」面板——当前/目标版本、按类别列出固件、设为目标、灰度本类、上传固件（二进制 multipart-free 直传）。

**验证**：固件增删/列表/blob 往返、OTA 解析（无目标/有更新/已最新）、按类别灰度、重新注册保留目标均有单测；`web typecheck` 通过；用 Python 校验 SHA-256。

横切项（管理后台登录、OTA）至此完成；白板里仍未做的主要是**跨端 SDK**（统一配网 + 功能 API 组件）。

## 17. 白板主打件：跨端 SDK（已落地）

白板的「功能组件 ①网络配置 ②功能 API」落地为 `sdk/`——零运行时依赖的 TypeScript 包，基于 `fetch`/`WebSocket`，覆盖浏览器/Node 18+/React Native/Electron（即白板的手机/PC/PC Web 各端）。原生 Android(Kotlin)/iOS(Swift) 因协议为纯 HTTP+WS，可照 `types.ts` 与文档做薄封装。

**功能 API**（`LightGatewayClient`）：一个客户端同时支持运营端（管理员会话 Bearer）与设备端（`X-Device-Token`）。覆盖鉴权/设备/遥测/命令（含设备端长轮询+回执）/事件/档案/天气内容/围栏轨迹/OTA/实时状态与播报；并提供品类语义化助手 `light/clock/gps/voice`，按能力档案契约拼命令（如 `gw.light(id).color('#ffd9a0')`、`gw.gps(id).geofence(lat,lng,r)`）。

**配网**（`provisionDevice`/`buildProvisionForm`）：向设备 SoftAP 门户 `POST /save` 提交 Wi-Fi+网关设置；文档标注了 Web 跨域限制与原生端无碍。

**实时语音**（`VoiceSession`）：封装 `…/ws?token=` 的 `lightgw.voice.v0` 会话，文本/音频收发 + asr/tts 回调，WebSocket 实现可注入（Node 等）。

**验证**：`tsc` 严格类型检查通过；`node:test` 9 个用例（零依赖，假 fetch）覆盖请求构造、Bearer/设备 Token 注入、品类助手 payload 契约、登录存 Token、错误状态、配网表单体、VoiceSession 的 ws URL 与发送——全部通过。

至此白板架构的四类终端、网关、管理后台、跨端 SDK 全部就位。后续多为工程深化：SDK 原生端封装、语音 Opus/流式、权限 RBAC、设备注册预配密钥等。

## 18. 语音深化：编解码透传 + TTS 流式分片（已落地）

把语音管线从「整段请求-响应」推进到「编解码可协商 + 下行流式」（仍零外部依赖）。

**编解码透传**：音频消息带 `codec`（`pcm16` 默认 / `opus`）。网关把音频当不透明字节、`codec` 透传给 ASR/TTS（`Transcribe(audio, codec)`、`X-Audio-Codec` 头；TTS 请求体带 `codec`），Opus 端到端、网关不转码。`welcome` 通告网关产出的 `codec` 与 `streaming:true`。`LIGHT_VOICE_CODEC` 配置。

**TTS 流式分片**：`Pipeline.Synthesize` 改为 `SynthesizeStream(text, emit)`，网关**边读上游 TTS 响应体边分片下发** `tts.audio {seq, final, codec}`（`streamChunks` 一块前瞻以正确标 `final`，默认 16KiB），设备边收边播、降低首响延迟；上游 TTS 不需支持分块。`Pipeline` 同时加 `AudioCodec()`。

**SDK / 固件适配**：SDK `VoiceSession.appendAudio(pcm, codec?)`、`onTtsAudio(pcm, {final, codec, seq})`；esp32-voice 上行带 `codec:"pcm16"`（Opus 留接入点），下行按分片顺序播放。

**验证**：`streamChunks` 前瞻分片（含空输入/单块/多块）、音频 `commit` 流式（asr.final→tts.say→多个 `tts.audio` seq/final/codec）、`welcome` 通告 codec、注入式 fake 管线均有 Go 单测；并用 Python 独立复核了分片前瞻逻辑；SDK `npm test` 9/9 通过。

---
*本文档基于当前仓库代码（`internal/device`、`cmd/*`、`firmware/esp32-wifi-agent`、`web`）梳理，与 `docs/access-model.md` 的接入模型一脉相承，作为其 v2 演进规划。第 9 节为阶段 0 已实现内容。*
