# @light-gateway/sdk

跨端 TypeScript SDK，封装 Light Gateway 的**功能 API**与**设备配网**。零运行时依赖，基于 `fetch` 与 `WebSocket`，可在浏览器、Node 18+、React Native、Electron 中使用。

白板里的「功能组件 ①网络配置 ②功能 API」即由本包提供。原生 Android(Kotlin)/iOS(Swift) 端：协议为纯 HTTP + WebSocket，可照本包做薄封装（见末尾「原生移植」）。

## 安装与构建

```bash
cd sdk
npm run build      # 输出到 dist/
npm test           # tsc 类型检查 + node:test 单测（零依赖）
```

## 功能 API

`LightGatewayClient` 同时支持运营端（管理员会话）与设备端（设备 Token）。

```ts
import { LightGatewayClient } from '@light-gateway/sdk';

const gw = new LightGatewayClient({ baseUrl: 'http://192.168.3.109:7001' });

// 运营端：登录后调用管理接口
await gw.login('admin', '强密码');           // 自动保存会话 Token
const devices = await gw.listDevices();
await gw.light('esp-nightlight-001').color('#ffd9a0');
await gw.light('esp-nightlight-001').brightness(60);
await gw.clock('esp-deskclock-001').mode('weather');
await gw.gps('esp-tracker-001').geofence(31.2304, 121.4737, 300);
await gw.setOtaTarget('esp-nightlight-001', '1.2.0');

// 设备端：用设备 Token 上报与拉命令
const dev = new LightGatewayClient({ baseUrl, deviceToken: 'lgw_xxx' });
await dev.reportTelemetry('esp-tracker-001', 'gps.fix', { lat: 31.23, lng: 121.47 });
const { command } = await dev.nextCommand('esp-tracker-001', 30);
```

品类语义化助手：`light`(power/brightness/color/colorRGB/effect/schedule)、`clock`(mode/brightness/syncTime/pushWeather)、`gps`(interval/geofence)、`voice`(wake/say)，自动按能力档案契约拼命令。底层也可直接 `createCommand`。

非浏览器环境注入 fetch：`new LightGatewayClient({ baseUrl, fetch })`。

## 设备配网（SoftAP 门户）

设备首启进入热点 `LightGateway-xxxx`，门户在 `http://192.168.4.1`。把宿主连到该热点后：

```ts
import { provisionDevice } from '@light-gateway/sdk';

await provisionDevice({
  ssid: 'home-wifi',
  password: 'wifi-pass',
  gateway: 'http://192.168.3.109:7001',
  deviceName: '卧室小夜灯',
  extra: { tz: 'CST-8' },        // 时钟/GPS 还可传 lat/lon
});
```

浏览器跨域限制：Web 应用 fetch 到 `192.168.4.1` 可能被 CORS 拦截；可改用真实 `<form>` 提交，或用 `buildProvisionForm()` 拿到 `{url, body}` 自行提交。原生 App（Android/iOS/Electron）无此限制。

## 实时语音（小智）

```ts
import { VoiceSession } from '@light-gateway/sdk';

const session = new VoiceSession({ baseUrl, deviceId: 'esp-xiaozhi-001', deviceToken: 'lgw_xxx' });
session.connect({
  onAsrFinal: (t) => console.log('识别:', t),
  onTtsSay: (t) => console.log('回复:', t),
  onTtsAudio: (pcmB64) => playPcm(pcmB64),
});
session.sendText('今天天气如何');     // 无需音频即可测对话
// 或：session.appendAudio(pcmBase64); session.commitAudio();
```

Node 环境（无全局 WebSocket）可注入：`new VoiceSession({ ..., webSocketFactory: (url) => new WS(url) })`。

## 原生移植（Android / iOS）

协议即网关 REST + `lightgw.voice.v0` WebSocket，无私有编码：
- 功能 API：对 `/api/v1/...` 发 JSON，运营端带 `Authorization: Bearer <session>`，设备端带 `X-Device-Token`。
- 配网：向门户 `POST /save` 提交 `application/x-www-form-urlencoded`（ssid/password/gateway/deviceId/...）。
- 语音：`GET …/ws?token=<deviceToken>` 升级 WebSocket，收发 `{type,payload}` 信封。

本包的 `types.ts` 可作为各端数据模型的权威参考。
