# 实时通道 + 语音（小智）占位协议

语音设备（如小智）是双向音频流场景，HTTP 长轮询 + 命令队列模型不适用。阶段 4 先落地**实时通道抽象**与**占位协议**，真实 ASR/TTS/LLM 与设备端音频固件作为后续单独迭代。

## 设计

- 传输：WebSocket（RFC 6455），由 `internal/realtime` 用 **Go 标准库**自实现（握手 + 帧读写 + ping/pong/close），不引入外部依赖，与网关整体低依赖一致。
- 连接管理：`realtime.Hub` 按 `deviceId` 维护连接，提供注册/路由/状态/推送（非阻塞，慢客户端丢弃）。同一设备重连会顶掉旧连接。
- 鉴权：复用设备 Token（`X-Device-Token` / `Authorization: Bearer` 头，或浏览器测试用 `?token=`），握手前由网关校验。
- 事件：连接/断开/会话事件经 `Hub.OnEvent` 回调写入网关事件流（`voice.connected` / `voice.disconnected` / `voice.session`）。

## 接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET (Upgrade) | `/api/v1/devices/{id}/ws` | 设备建立 WebSocket（需设备 Token）|
| GET | `/api/v1/devices/{id}/realtime` | 查询连接状态 `{connected, connections}` |
| POST | `/api/v1/devices/{id}/realtime/say` | 让设备播报 `{text}`（网关→设备 push tts.say）|

`voice.v1` 档案的 `voice.wake` / `voice.say` 命令仍走命令队列（控制类）；音频与会话走实时通道。

## 协议（`lightgw.voice.v0`）

JSON 信封：`{ "type": string, "seq"?: int, "ts"?: int, "payload"?: object }`

设备 → 网关：`hello` · `ping` · `text.input {text}` · `audio.append {pcm:base64, codec}` · `audio.commit {}` · `audio.reset {}`
网关 → 设备：`welcome {codec, streaming}` · `pong` · `asr.final {text}` · `tts.say {text}` · `tts.audio {pcm:base64, codec, seq, final}` · `audio.ack {bytes}` · `error {message}`

**编解码与流式**：音频消息带 `codec`（默认 `pcm16`，可 `opus`）。网关把音频当不透明字节，`codec` 透传给 ASR/TTS（Opus 端到端，网关不转码）。`welcome` 通告网关产出的 `codec` 与 `streaming:true`。TTS **分片流式下发**：`tts.audio` 是一串带 `seq` 的分片，最后一片 `final:true`，设备可边收边播以降低首响延迟。

会话流程（`internal/realtime/hub.go` 的 `dispatch`，控制类同步、管线类异步以免阻塞读循环）：

- `ping` → `pong`；`hello` → `welcome`
- `audio.append` 记录会话 `codec` 并累积音频缓冲，回 `audio.ack`
- `audio.commit` → `ASR(audio, codec)` → `asr.final {text}` → `LLM(text)` → `tts.say {reply}` →（如配置 TTS）流式 `tts.audio` 分片
- `text.input {text}` → `asr.final {text}` → `LLM(text)` → `tts.say {reply}`（无需麦克风即可测对话链路）

## 可插拔管线（ASR / LLM / TTS）

`internal/realtime/pipeline.go` 定义 `Pipeline` 接口：`Transcribe(audio, codec)`（ASR）、`Respond`（对话 LLM）、`SynthesizeStream`（流式 TTS，按分片 `emit`）、`AudioCodec()`。默认 `echoPipeline`（回显占位）；配置任一环境变量即启用 HTTP 管线（标准库实现，零新依赖）：

| 环境变量 | 作用 |
| --- | --- |
| `LIGHT_VOICE_LLM_URL` | OpenAI 兼容 `chat/completions` 端点（启用对话）|
| `LIGHT_VOICE_LLM_KEY` | LLM Bearer Key |
| `LIGHT_VOICE_LLM_MODEL` | 模型名（默认 `gpt-4o-mini`）|
| `LIGHT_VOICE_PROMPT` | 系统提示词 |
| `LIGHT_VOICE_ASR_URL` | POST 原始音频字节（带 `X-Audio-Codec` 头）→ `{"text":"…"}` |
| `LIGHT_VOICE_TTS_URL` | POST `{"text":"…","codec":"…"}` → 音频字节（**网关边读边分片下发**）|
| `LIGHT_VOICE_CODEC` | 通告/产出的音频编解码：`pcm16`（默认）或 `opus` |

`SynthesizeStream` 直接读 TTS 响应体并按块（默认 16KiB，一块前瞻以标 `final`）即时转发为 `tts.audio` 分片——上游不需支持分块即可获得渐进播放。未配置的环节回退占位（如只配 LLM：音频转写仍占位，但 `text.input` 对话已可真跑）。各环节失败回 `error`，不影响通道。

## 测试

无需硬件即可验证：用任意 WebSocket 客户端连 `ws://<gateway>/api/v1/devices/<id>/ws?token=<deviceToken>`（设备先注册拿 Token），发 `{"type":"text.input","payload":{"text":"hi"}}` 应收到 `asr.final` + `tts.say`（未配 LLM 时为回显，配了即真实对话）。管理台「小智 / 语音」面板可看连接状态并推送播报。

握手 accept-key、掩码帧编解码、Hub 路由、管线选择与会话流程（含 echo 与注入式 fake 管线、音频 commit 流程）、**TTS 分片流式（seq/final）与 `streamChunks` 前瞻分片、`welcome` 通告 codec** 均有单测（`internal/realtime`）。

## 后续（单独立项）

1. 设备端音频保真：ESP32 接入 libopus 做 Opus 编解码（协议与网关已支持 `codec:"opus"` 透传，设备端置 `codec` 即可）；回声消除/VAD（当前为 PCM16 16kHz + 按键说话）。
2. 上行流式 ASR 与流式 LLM：当前下行 TTS 已分片流式（边读上游边下发），上行音频与 LLM token 流式可进一步降首响延迟；可评估对齐开源 xiaozhi-esp32 协议。
3. 健壮性：背压/重连/心跳超时、会话级上下文记忆与多轮对话。
