# ESP32 Night Light / RGB Strip 固件（category=light）

面向小夜灯 / RGB 灯带的专用固件，注册为 `light` 产品（profile `light.v1`），实现 `light.*` 命令契约。

## 接线

默认使用三路 PWM 驱动共阴 RGB（或通过 MOSFET 驱动模拟 RGB 灯带）：

- R → GPIO 25
- G → GPIO 26
- B → GPIO 27

如使用 WS2812 等可寻址灯带，只需把 `applyOutput()` 换成 NeoPixel 写入，命令契约不变。

## ST7789 状态屏

固件默认启用 2 寸 ST7789 TFT，横屏显示设备 ID、当前阶段、Wi-Fi/网关状态、夜灯状态；底部每 3 秒刷新 RSSI、运行时长和剩余堆内存。

| 屏幕引脚 | 接到 ESP32-C3 SuperMini | 对应宏 |
| --- | --- | --- |
| GND | GND | - |
| VCC | 3V3 | - |
| SCL / SCLK | GPIO4 | `TFT_SCLK_PIN` |
| SDA / MOSI | GPIO6 | `TFT_MOSI_PIN` |
| CS | GPIO7 | `TFT_CS_PIN` |
| DC | GPIO3 | `TFT_DC_PIN` |
| RST | GPIO10 | `TFT_RST_PIN` |
| BLK / LED | GPIO1 | `TFT_BLK_PIN` |

如果背光直接接 3V3，把 `TFT_BLK_PIN` 改成 `-1`。如屏幕方向不同，调整 `TFT_ROTATION`。

## 编译 / 上传

```bash
arduino-cli compile --fqbn esp32:esp32:esp32c3:PartitionScheme=huge_app firmware/esp32-light
arduino-cli upload  --fqbn esp32:esp32:esp32c3:PartitionScheme=huge_app --port /dev/cu.usbmodem2101 firmware/esp32-light
```

首次启动进入配置热点 `LightGateway-<chip-id>`，连上后打开 `http://192.168.4.1` 填写 Wi-Fi、网关地址和时区（默认 `CST-8`）。

## 支持的命令

| 命令 | payload | 说明 |
| --- | --- | --- |
| `light.power` | `{"on": true}` | 开/关 |
| `light.brightness` | `{"value": 0-100}` | 亮度 |
| `light.color` | `{"hex":"#RRGGBB"}` 或 `{"r":..,"g":..,"b":..}` | 颜色 |
| `light.effect` | `{"name":"static\|breath\|rainbow","speed":1-10}` | 灯效 |
| `light.schedule` | `{"on":"22:00","off":"07:00"}` | 定时窗口自动开关（用 NTP 本地时间判断，支持跨夜） |

遥测：`light.state`（on/off）、`light.brightness`（0-100），心跳每 10s，遥测每 30s。

颜色、亮度、开关状态会持久化到 NVS，断电重启后恢复。

## OTA 自更新

固件每分钟轮询 `GET /api/v1/devices/{id}/ota`（带设备 Token）。当管理员在控制台上传了新固件并把该设备目标版本设为新版本时，设备会下载二进制（`…/firmware/{fwId}/download`）、校验 SHA-256、用 `Update` 刷写并重启，重启后以新 `fwVersion` 重新注册。本套 OTA 例程（`checkOTA()` / `performOTA()`）可原样移植到 clock/gps/voice 固件。
