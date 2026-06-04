# 固件烧录指南（ESP32 / ESP32-S3）

本目录下的固件都是 Arduino 工程（`.ino`），用 **ESP32 Arduino core** 编译烧录。多数固件只用 core 自带库（WiFi/WebServer/HTTPClient/Preferences/Update/I2S/mbedtls），**唯独 `esp32-light` 带 ST7789 状态屏，需额外装 Adafruit GFX + ST7789 库**（用 `scripts/flash.sh` 会自动装）。

## 固件总览

| 目录 | 板子 / FQBN | category | 额外库 | 说明 |
| --- | --- | --- | --- | --- |
| `esp32-light/` | **ESP32-C3** · `esp32:esp32:esp32c3:PartitionScheme=huge_app` | light | Adafruit GFX + ST7789 | 灯带/小夜灯（PWM RGB）+ ST7789 屏，含配网门户 Provision Key 字段 |
| `esp32-clock/` | ESP32 · `esp32:esp32:esp32` | clock | 无 | 时钟/日历/天气屏 |
| `esp32-gps/` | ESP32 · `esp32:esp32:esp32` | gps | 无 | GPS 追踪（NEO-6M 等 UART 模块） |
| `esp32-voice/` | **ESP32-S3** · `esp32:esp32:esp32s3` | voice | 无 | 小智语音（I2S 麦克风/扬声器 + WebSocket） |
| `esp32-wifi-agent/` | ESP32 · `esp32:esp32:esp32` | esp（通用） | 无 | 早期通用 Wi-Fi Agent |

> `light` 是 ESP32-C3（原生 USB，端口形如 macOS `/dev/cu.usbmodem*`）；`voice` 是 ESP32-S3（也是原生 USB）；`clock/gps` 是普通 ESP32（经 CH340/CP2102 桥接，端口形如 `/dev/cu.wchusbserial*`）。接线/引脚见各目录自己的 README。

## 0. 准备：USB 线 + 驱动

- 用**能传数据**的 USB 线（很多线只供电）。
- 装 USB-串口驱动（看板子上的芯片）：**CH340/CH9102（WCH）** 或 **CP2102（Silicon Labs）**。装完插板子应能看到一个串口设备。

## 方式 A：arduino-cli（命令行，推荐，和仓库命令一致）

### 1) 安装 arduino-cli

- macOS：`brew install arduino-cli`
- Linux/macOS 通用：`curl -fsSL https://raw.githubusercontent.com/arduino/arduino-cli/master/install.sh | sh`

### 2) 安装 ESP32 core（一次性）

```bash
arduino-cli config init
arduino-cli config add board_manager.additional_urls https://espressif.github.io/arduino-esp32/package_esp32_index.json
arduino-cli core update-index
arduino-cli core install esp32:esp32
```

### 3) 找串口

```bash
arduino-cli board list
# macOS:  /dev/cu.usbserial-xxxx 或 /dev/cu.wchusbserial... 或 /dev/cu.SLAB_USBtoUART
# Linux:  /dev/ttyUSB0 或 /dev/ttyACM0   （Linux 需把自己加入 dialout 组：sudo usermod -aG dialout $USER 后重登录）
# Windows: COMx
```

### 4) 编译 + 上传（在仓库根目录执行，`PORT` 换成上一步的端口）

最省事是用一键脚本（自动装库、自动选对 FQBN）：

```bash
scripts/flash.sh light /dev/cu.usbmodem2101   # ESP32-C3，会自动装 Adafruit 库
scripts/flash.sh clock PORT
scripts/flash.sh gps   PORT
scripts/flash.sh voice PORT --monitor
```

或手动：

```bash
# 灯（ESP32-C3，需先装 Adafruit 库，且用 huge_app 分区）
arduino-cli lib install "Adafruit GFX Library" "Adafruit ST7735 and ST7789 Library"
arduino-cli compile --fqbn esp32:esp32:esp32c3:PartitionScheme=huge_app firmware/esp32-light
arduino-cli upload  --fqbn esp32:esp32:esp32c3:PartitionScheme=huge_app -p PORT firmware/esp32-light

# 时钟 / GPS（普通 ESP32）
arduino-cli compile --fqbn esp32:esp32:esp32 firmware/esp32-clock
arduino-cli upload  --fqbn esp32:esp32:esp32 -p PORT firmware/esp32-clock

# 语音（ESP32-S3，注意 fqbn 不同；开启 USB CDC 以便串口看日志）
arduino-cli compile --fqbn esp32:esp32:esp32s3:CDCOnBoot=cdc firmware/esp32-voice
arduino-cli upload  --fqbn esp32:esp32:esp32s3:CDCOnBoot=cdc -p PORT firmware/esp32-voice
```

> 上传报 “Failed to connect / wrong boot mode”：按住板子 **BOOT** 键，点一下 **RST/EN**，松开 BOOT，再上传一次（部分板子需要手动进下载模式）。ESP32-S3 用原生 USB 口时，首次也可能要这样进下载模式。

## 方式 B：Arduino IDE（图形界面）

1. 偏好设置 → 附加开发板管理器网址，加 `https://espressif.github.io/arduino-esp32/package_esp32_index.json`。
2. 开发板管理器搜索 **esp32**，安装 “esp32 by Espressif Systems”。
3. 工具 → 开发板：ESP32 选 **ESP32 Dev Module**；语音板选 **ESP32S3 Dev Module**（并把 “USB CDC On Boot” 设为 Enabled）。
4. 工具 → 端口：选你的串口。
5. 打开对应 `firmware/<目录>/<同名>.ino`，点上传（→）。

## 烧录后：配网，让设备连上网关

1. 用串口监视器（**115200** 波特率）看日志，会打印 `DEVICE_ID ...`、`CONFIG_PORTAL ...`。
2. 首次启动设备开热点 **`LightGateway-<chip-id>`**；手机/电脑连上它，浏览器自动弹出（或手动打开 `http://192.168.4.1`）配置页。
3. 填：**Wi-Fi SSID/密码**、**网关地址**（如 `http://<网关主机IP>:7001`，注意是网关电脑/容器的局域网 IP，不是 `localhost`），时钟/GPS 还可填时区、经纬度。
4. 保存后设备重启，自动注册到网关，几秒后会出现在管理台设备列表里。

### 关于预配密钥（Provision Key）

如果网关设了 `LIGHT_PROVISION_KEY`（Docker 默认是 `lightgateway-enroll`），注册需要带它：
- **`esp32-light`** 的配网门户有 “Provision Key” 字段，直接填即可。
- `esp32-clock/gps/voice` 的门户暂未加该字段——**首次接入时把网关设为开放注册**（不设 `LIGHT_PROVISION_KEY`）最省事，设备连上后再按需开启；或参照 `esp32-light` 在它们的 `httpJSON` 注册请求里加一行 `X-Provision-Key` 头。

## 常见问题

| 现象 | 处理 |
| --- | --- |
| `board list` 看不到端口 | USB 线只供电 / 没装 CH340/CP210x 驱动 |
| Linux 上传 `Permission denied` | `sudo usermod -aG dialout $USER` 后重新登录，或临时 `sudo chmod a+rw /dev/ttyUSB0` |
| Failed to connect / wrong boot mode | 按住 BOOT，点 RST，松开 BOOT，再上传 |
| 串口无日志（尤其 S3） | S3 用原生 USB 时把 “USB CDC On Boot” 开启（fqbn 加 `:CDCOnBoot=cdc`），或改插板上的 UART 口 |
| 设备一直不出现在管理台 | 网关地址填错（要填局域网 IP 不是 localhost）/ Wi-Fi 没连上 / 网关设了预配密钥但设备没带（见上） |
| OTA 升级失败 “no partition” | 用默认分区方案（已含双 OTA 分区）；Arduino IDE 里 Partition Scheme 选 Default |
