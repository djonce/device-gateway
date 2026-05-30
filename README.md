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

## 设计文档

设备接入模型、协议决策和后续扩展点见 [docs/access-model.md](docs/access-model.md)。
