# Light Gateway 设备接入模型

## 目标

Light Gateway 面向 ESP、Android 手机、Orange Pi Zero 2W 和其他 Linux 终端，提供统一设备注册、心跳、遥测、命令下发和管理界面。第一版优先保证低依赖、易部署、易调试，适合先在局域网、家庭实验室或小规模边缘现场落地。

## 关键决策

- 平台后端使用 Go 标准库 HTTP 服务，生产默认使用 SQLite，测试和轻量场景仍可使用内存/JSON。
- 前端管理台使用 React + UmiJS，默认通过 `/api` 代理到 Go 网关。
- 设备接入通道第一版使用 HTTP + 长轮询，不直接把 MQTT/WebSocket 作为核心依赖。
- 数据落盘为 SQLite 文件 `data/light-gateway.db`（增量写入）。`LIGHT_GATEWAY_DATA` 为空时使用纯内存模式，仅用于测试和临时调试。
- 设备命令采用 “平台入队，Agent 拉取，Agent 回执” 模型，天然适配 NAT 后设备和移动网络。
- 设备能力使用 capability 模型声明，例如 `gpio.write`、`shell.exec`、`sensor.read`、`camera.snapshot`。
- 设备侧接口使用 `X-Device-Token` 或 `Authorization: Bearer <token>` 鉴权。Token 只在首次注册或重置时返回明文，平台持久化 SHA-256 摘要。

## 设备类型建议

| 类型 | 建议接入方式 | 典型能力 |
| --- | --- | --- |
| ESP32/ESP8266 | HTTP 注册、心跳、遥测、短轮询命令 | GPIO、传感器、继电器、PWM |
| ESP32 Wi-Fi Agent | 开发板固件直连网关 | 心跳、RSSI/heap 遥测、GPIO 命令 |
| USB 串口开发板 | 电脑侧 Serial Agent 桥接 | 串口文本读取、串口写入、固件调试 |
| Android | App Agent 后台服务 | 定位、电量、网络状态、文件/日志采集、通知 |
| Orange Pi Zero 2W | Linux Agent/systemd 服务 | Shell、Docker、局域网探测、串口、摄像头、蓝牙 |
| 其他 Linux 节点 | Linux Agent | 进程管理、日志、脚本、边缘任务 |

## 生命周期

1. 设备启动后调用 `POST /api/v1/devices/register` 注册或刷新资料。
2. 平台返回一次性 `token`，设备保存到本地 token 文件。
3. 设备周期性调用 `POST /api/v1/devices/{deviceId}/heartbeat` 上报在线状态，请求头携带 `X-Device-Token`。
4. 设备按需调用 `POST /api/v1/devices/{deviceId}/telemetry` 上报遥测，请求头携带 `X-Device-Token`。
5. 管理员在控制台或 API 创建命令：`POST /api/v1/devices/{deviceId}/commands`。
6. 设备调用 `GET /api/v1/devices/{deviceId}/commands/next?timeout=30` 拉取命令，请求头携带 `X-Device-Token`。
7. 设备执行后调用 `POST /api/v1/devices/{deviceId}/commands/{commandId}/ack` 回传成功或失败。

## 在线状态规则

- `online`: 最近 2 分钟内有心跳。
- `stale`: 2 到 10 分钟内没有心跳，可能弱网或休眠。
- `offline`: 超过 10 分钟没有心跳。

## REST API

### 注册设备

`POST /api/v1/devices/register`

```json
{
  "id": "esp-livingroom-001",
  "name": "Living Room ESP",
  "type": "esp",
  "agentVersion": "0.1.0",
  "labels": {
    "room": "livingroom"
  },
  "capabilities": [
    {
      "name": "sensor.read",
      "description": "read temperature and humidity"
    }
  ]
}
```

响应：

```json
{
  "device": {
    "id": "esp-livingroom-001",
    "name": "Living Room ESP",
    "type": "esp",
    "state": "online",
    "disabled": false
  },
  "token": "lgw_xxx",
  "reused": false
}
```

如果设备已存在并且已有 Token，响应通常不会再次返回 Token。需要找回或轮换 Token 时，在管理台点击 Reset Token，或调用 `POST /api/v1/devices/{deviceId}/token/reset`。

### 心跳

`POST /api/v1/devices/{deviceId}/heartbeat`

请求头：

```text
X-Device-Token: lgw_xxx
```

```json
{
  "agentVersion": "0.1.1",
  "metadata": {
    "rssi": -61
  }
}
```

### 遥测

`POST /api/v1/devices/{deviceId}/telemetry`

请求头：

```text
X-Device-Token: lgw_xxx
```

```json
{
  "key": "temperature",
  "value": 24.7,
  "unit": "celsius"
}
```

### 创建命令

`POST /api/v1/devices/{deviceId}/commands`

```json
{
  "type": "gpio.write",
  "payload": {
    "pin": 2,
    "value": true
  },
  "requestedBy": "console",
  "ttlSeconds": 60
}
```

### 拉取命令

`GET /api/v1/devices/{deviceId}/commands/next?timeout=30`

请求头：

```text
X-Device-Token: lgw_xxx
```

如果没有命令，返回：

```json
{
  "command": null
}
```

### 命令回执

`POST /api/v1/devices/{deviceId}/commands/{commandId}/ack`

请求头：

```text
X-Device-Token: lgw_xxx
```

```json
{
  "status": "succeeded",
  "result": {
    "message": "ok"
  }
}
```

## 后续扩展

- 管理员登录已实现（环境变量账号 + 会话 Token，见 README「管理后台登录鉴权」）；后续可加操作员角色、API Key、设备注册预配密钥与更严格审计。
- 增加 MQTT 适配器：把 MQTT topic 转为同一套 Store 和 Command API。
- 增加 PostgreSQL 存储实现，保留当前 Store 接口。
- 为 Orange Pi Agent 增加 OTA 更新。
- 为 ESP 提供 Arduino/PlatformIO 示例固件。
- 为 USB 串口开发板增加自动识别芯片型号、波特率探测和固件烧录流程。
