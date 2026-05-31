# ESP32 GPS 追踪器固件（category=gps）

注册为 `gps` 产品（profile `gps.v1`）。从 UART GPS 模块（如 NEO-6M）读 NMEA，解析 `$..RMC` 得到经纬度/速度，按间隔上报 `gps.fix` 遥测。地理围栏的进出判定在**网关侧**完成（设备只负责上报位置），`geofence.set` 命令会下发给设备做本地留存/指示。

## 接线

- GPS 模块 TX → ESP32 GPIO 16（RX）
- GPS 模块 RX → ESP32 GPIO 17（TX，可选）
- 默认波特率 9600

## 编译 / 上传

```bash
arduino-cli compile --fqbn esp32:esp32:esp32 firmware/esp32-gps
arduino-cli upload  --fqbn esp32:esp32:esp32 --port /dev/cu.wchusbserial110 firmware/esp32-gps
```

配置热点 `LightGateway-<chip-id>` → `http://192.168.4.1` 填写 Wi-Fi 与网关地址。

## 命令与遥测

| 命令 | payload | 说明 |
| --- | --- | --- |
| `gps.interval` | `{"seconds":1-3600}` | 调整上报频率 |
| `geofence.set` | `{"center":[lat,lng],"radius_m":200}` | 围栏（网关下发，设备留存）|

遥测 `gps.fix`：`{"lat":..,"lng":..,"speed":..(m/s),"sats":..}`。网关收到后做围栏判定，进出区域产生 `geofence.enter` / `geofence.exit` 事件；轨迹可通过 `GET /api/v1/devices/{id}/track` 查询。
