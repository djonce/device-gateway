# ESP32 时钟 / 日历 / 天气屏固件（category=clock）

注册为 `clock` 产品（profile `clock.v1`）。时间与天气走**网关内容接口**：设备定时拉取 `GET /api/v1/content/clock?lat&lon&tz`，网关代理 Open-Meteo（设备不持有任何 API key）。即时刷新走命令。

## 编译 / 上传

```bash
arduino-cli compile --fqbn esp32:esp32:esp32 firmware/esp32-clock
arduino-cli upload  --fqbn esp32:esp32:esp32 --port /dev/cu.wchusbserial110 firmware/esp32-clock
```

配置热点 `LightGateway-<chip-id>` → `http://192.168.4.1`，填写 Wi-Fi、网关地址、时区（POSIX TZ，默认 `CST-8`）和经纬度（默认上海）。

## 命令

| 命令 | payload | 说明 |
| --- | --- | --- |
| `display.mode` | `{"mode":"clock\|calendar\|weather"}` | 切换显示模式 |
| `display.brightness` | `{"value":0-100}` | 屏幕亮度 |
| `time.sync` | `{"epoch":..,"tz":"CST-8"}` | 校时（settimeofday）|
| `weather.push` | `{"temp":21.4,"cond":"晴"}` | 即时推送天气，无需等下次拉取 |

## 显示

示例固件把画面输出到串口（`[SCREEN ...]`），作为占位。接 SSD1306/TFT/点阵屏时，把 `renderScreen()` 换成真实绘制即可，数据流不变。时间用 NTP 兜底，并可被 `time.sync` 覆盖。
