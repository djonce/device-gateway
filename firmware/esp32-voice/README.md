# ESP32-S3 语音助手固件（category=voice，小智）

注册为 `voice` 产品（profile `voice.v1`）。HTTP 注册拿 Token 后，开 WebSocket 接入网关实时通道，说 `lightgw.voice.v0` 协议。

## 工作流

- 按住 BOOT 键（GPIO0）说话：麦克风 PCM 以 `audio.append`（base64）流式上传，松开发 `audio.commit`。
- 接收 `asr.final` / `tts.say`（文本，打印到串口）与 `tts.audio`（base64 PCM → 扬声器）。
- 串口直接输入一行文本会发 `text.input`，便于无麦克风时测对话链路。

## 接线（按板子调整）

- 麦克风 INMP441（I2S0 RX）：SCK=14, WS=15, SD=32
- 功放 MAX98357A（I2S1 TX）：BCLK=26, LRC=25, DIN=22
- 按键：BOOT/GPIO0（低有效）

## 编译 / 上传

```bash
arduino-cli compile --fqbn esp32:esp32:esp32s3 firmware/esp32-voice
arduino-cli upload  --fqbn esp32:esp32:esp32s3 --port /dev/cu.usbmodem* firmware/esp32-voice
```

## 说明

- 音频为 PCM16 单声道 16kHz，便于演示；生产建议改 Opus 并双向分片流式（协议信封不变）。
- 网关侧的对话/转写/合成由可插拔管线决定：未配置时回显（占位），配置 `LIGHT_VOICE_LLM_URL` 等环境变量后接真实 LLM/ASR/TTS（见 [docs/realtime-voice.md](../../docs/realtime-voice.md)）。
- WebSocket 客户端帧按 RFC 6455 做了**掩码**（客户端必须掩码）；握手用 `Sec-WebSocket-Key` + `X-Device-Token` 头鉴权。
