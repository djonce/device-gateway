// Light Gateway — ESP32-S3 voice assistant firmware (category=voice, 小智).
//
// Registers over HTTP (to obtain a device token), then opens a WebSocket to the
// gateway's real-time channel and speaks the lightgw.voice.v0 protocol:
//   push-to-talk (BOOT button): stream mic PCM as audio.append, audio.commit on release
//   receive: asr.final / tts.say (text) and tts.audio (base64 PCM -> speaker)
//   type text over Serial to send text.input (test without a mic)
//
// Audio is raw PCM16 mono 16 kHz here for clarity. For production use Opus and
// chunk/stream both directions; the protocol envelope stays the same.
//
// Hardware (adjust pins to your board):
//   Mic  INMP441 (I2S0 RX):  SCK=14  WS=15  SD=32
//   Amp  MAX98357A (I2S1 TX): BCLK=26 LRC=25 DIN=22
//   Push-to-talk: BOOT button on GPIO0 (active low)

#include <Arduino.h>
#include <WiFi.h>
#include <WiFiClient.h>
#include <HTTPClient.h>
#include <Preferences.h>
#include <WebServer.h>
#include <DNSServer.h>
#include <driver/i2s.h>
#include <mbedtls/base64.h>
#include <mbedtls/sha1.h>

#define BUTTON_PIN 0
#define MIC_SCK 14
#define MIC_WS 15
#define MIC_SD 32
#define AMP_BCLK 26
#define AMP_LRC 25
#define AMP_DIN 22
#define SAMPLE_RATE 16000

Preferences prefs;
DNSServer dnsServer;
WebServer portal(80);
IPAddress portalIP(192, 168, 4, 1);
IPAddress portalSubnet(255, 255, 255, 0);

struct Config {
  String ssid, password, gateway, deviceId, deviceName, token;
};
Config config;

WiFiClient ws;            // raw socket for the WebSocket
bool wsConnected = false;
bool portalMode = false;
bool talking = false;
unsigned long lastHeartbeatAt = 0;

String chipId() {
  uint64_t mac = ESP.getEfuseMac();
  char v[17];
  snprintf(v, sizeof(v), "%04X%08X", (uint16_t)(mac >> 32), (uint32_t)mac);
  return String(v);
}
String defaultDeviceId() { return "esp32-voice-" + chipId(); }

String jsonValue(const String& body, const String& key) {
  String m = "\"" + key + "\":\"";
  int s = body.indexOf(m);
  if (s < 0) return "";
  s += m.length();
  int e = body.indexOf("\"", s);
  return e < 0 ? "" : body.substring(s, e);
}

void loadConfig() {
  prefs.begin("lightgw", false);
  config.ssid = prefs.getString("ssid", "");
  config.password = prefs.getString("pass", "");
  config.gateway = prefs.getString("gateway", "http://192.168.3.109:7001");
  config.deviceId = prefs.getString("deviceId", defaultDeviceId());
  config.deviceName = prefs.getString("name", "XiaoZhi Voice");
  config.token = prefs.getString("token", "");
}
void saveConfig() {
  prefs.putString("ssid", config.ssid);
  prefs.putString("pass", config.password);
  prefs.putString("gateway", config.gateway);
  prefs.putString("deviceId", config.deviceId);
  prefs.putString("name", config.deviceName);
  prefs.putString("token", config.token);
}

// host/port parsed from config.gateway like "http://192.168.3.109:7001"
String gwHost() {
  String g = config.gateway;
  g.replace("http://", "");
  g.replace("https://", "");
  int slash = g.indexOf('/');
  if (slash >= 0) g = g.substring(0, slash);
  int colon = g.indexOf(':');
  return colon >= 0 ? g.substring(0, colon) : g;
}
int gwPort() {
  String g = config.gateway;
  g.replace("http://", "");
  g.replace("https://", "");
  int slash = g.indexOf('/');
  if (slash >= 0) g = g.substring(0, slash);
  int colon = g.indexOf(':');
  return colon >= 0 ? g.substring(colon + 1).toInt() : 80;
}

String b64encode(const uint8_t* data, size_t len) {
  size_t outLen = 0;
  size_t cap = ((len + 2) / 3) * 4 + 1;
  String out;
  out.reserve(cap);
  unsigned char* buf = (unsigned char*)malloc(cap);
  if (!buf) return "";
  if (mbedtls_base64_encode(buf, cap, &outLen, data, len) == 0) {
    out = String((char*)buf).substring(0, outLen);
  }
  free(buf);
  return out;
}

size_t b64decode(const String& in, uint8_t* out, size_t cap) {
  size_t outLen = 0;
  if (mbedtls_base64_decode(out, cap, &outLen, (const unsigned char*)in.c_str(), in.length()) != 0) return 0;
  return outLen;
}

// ---- I2S ----

void setupI2S() {
  i2s_config_t rx = {};
  rx.mode = (i2s_mode_t)(I2S_MODE_MASTER | I2S_MODE_RX);
  rx.sample_rate = SAMPLE_RATE;
  rx.bits_per_sample = I2S_BITS_PER_SAMPLE_16BIT;
  rx.channel_format = I2S_CHANNEL_FMT_ONLY_LEFT;
  rx.communication_format = I2S_COMM_FORMAT_STAND_I2S;
  rx.dma_buf_count = 4;
  rx.dma_buf_len = 256;
  i2s_pin_config_t rxPins = {};
  rxPins.bck_io_num = MIC_SCK;
  rxPins.ws_io_num = MIC_WS;
  rxPins.data_in_num = MIC_SD;
  rxPins.data_out_num = I2S_PIN_NO_CHANGE;
  i2s_driver_install(I2S_NUM_0, &rx, 0, NULL);
  i2s_set_pin(I2S_NUM_0, &rxPins);

  i2s_config_t tx = {};
  tx.mode = (i2s_mode_t)(I2S_MODE_MASTER | I2S_MODE_TX);
  tx.sample_rate = SAMPLE_RATE;
  tx.bits_per_sample = I2S_BITS_PER_SAMPLE_16BIT;
  tx.channel_format = I2S_CHANNEL_FMT_ONLY_LEFT;
  tx.communication_format = I2S_COMM_FORMAT_STAND_I2S;
  tx.dma_buf_count = 6;
  tx.dma_buf_len = 256;
  i2s_pin_config_t txPins = {};
  txPins.bck_io_num = AMP_BCLK;
  txPins.ws_io_num = AMP_LRC;
  txPins.data_out_num = AMP_DIN;
  txPins.data_in_num = I2S_PIN_NO_CHANGE;
  i2s_driver_install(I2S_NUM_1, &tx, 0, NULL);
  i2s_set_pin(I2S_NUM_1, &txPins);
}

// ---- WebSocket client (RFC 6455, client frames MUST be masked) ----

bool wsHandshake() {
  if (!ws.connect(gwHost().c_str(), gwPort())) {
    Serial.println("WS_TCP_FAILED");
    return false;
  }
  uint8_t rnd[16];
  for (int i = 0; i < 16; i++) rnd[i] = (uint8_t)esp_random();
  String key = b64encode(rnd, 16);
  String path = "/api/v1/devices/" + config.deviceId + "/ws";
  ws.printf("GET %s HTTP/1.1\r\n", path.c_str());
  ws.printf("Host: %s:%d\r\n", gwHost().c_str(), gwPort());
  ws.print("Upgrade: websocket\r\n");
  ws.print("Connection: Upgrade\r\n");
  ws.printf("Sec-WebSocket-Key: %s\r\n", key.c_str());
  ws.print("Sec-WebSocket-Version: 13\r\n");
  ws.printf("X-Device-Token: %s\r\n", config.token.c_str());
  ws.print("\r\n");

  unsigned long t0 = millis();
  String status;
  while (ws.connected() && millis() - t0 < 5000) {
    String line = ws.readStringUntil('\n');
    if (status == "") status = line;
    if (line == "\r" || line.length() == 0) break;
  }
  if (status.indexOf("101") < 0) {
    Serial.println("WS_HANDSHAKE_FAILED " + status);
    ws.stop();
    return false;
  }
  Serial.println("WS_CONNECTED");
  return true;
}

void wsSendText(const String& payload) {
  size_t n = payload.length();
  uint8_t header[14];
  int hi = 0;
  header[hi++] = 0x81;  // FIN + text
  uint8_t maskBit = 0x80;
  if (n < 126) {
    header[hi++] = maskBit | (uint8_t)n;
  } else if (n < 65536) {
    header[hi++] = maskBit | 126;
    header[hi++] = (n >> 8) & 0xFF;
    header[hi++] = n & 0xFF;
  } else {
    header[hi++] = maskBit | 127;
    for (int i = 7; i >= 0; i--) header[hi++] = (n >> (8 * i)) & 0xFF;
  }
  uint8_t mask[4];
  for (int i = 0; i < 4; i++) mask[i] = (uint8_t)esp_random();
  memcpy(header + hi, mask, 4);
  hi += 4;
  ws.write(header, hi);
  // Mask and send payload in a small buffer.
  const char* p = payload.c_str();
  uint8_t buf[256];
  for (size_t i = 0; i < n;) {
    size_t chunk = min((size_t)sizeof(buf), n - i);
    for (size_t j = 0; j < chunk; j++) buf[j] = p[i + j] ^ mask[(i + j) % 4];
    ws.write(buf, chunk);
    i += chunk;
  }
}

// Read one server frame (unmasked) into out; returns opcode or -1.
int wsReadFrame(String& out) {
  if (ws.available() < 2) return -1;
  uint8_t b0 = ws.read();
  uint8_t b1 = ws.read();
  int opcode = b0 & 0x0F;
  uint64_t len = b1 & 0x7F;
  if (len == 126) {
    len = ((uint64_t)ws.read() << 8) | ws.read();
  } else if (len == 127) {
    len = 0;
    for (int i = 0; i < 8; i++) len = (len << 8) | ws.read();
  }
  out = "";
  out.reserve(len);
  unsigned long t0 = millis();
  for (uint64_t i = 0; i < len;) {
    if (ws.available()) {
      out += (char)ws.read();
      i++;
    } else if (millis() - t0 > 3000) {
      break;
    }
  }
  return opcode;
}

void wsSendEnvelope(const String& type, const String& payloadJson) {
  String msg = "{\"type\":\"" + type + "\"";
  if (payloadJson.length()) msg += ",\"payload\":" + payloadJson;
  msg += "}";
  wsSendText(msg);
}

void playAudio(const String& b64) {
  size_t cap = (b64.length() / 4) * 3 + 4;
  uint8_t* pcm = (uint8_t*)malloc(cap);
  if (!pcm) return;
  size_t n = b64decode(b64, pcm, cap);
  size_t written = 0;
  i2s_write(I2S_NUM_1, pcm, n, &written, portMAX_DELAY);
  free(pcm);
}

void handleServerMessage(const String& body) {
  String type = jsonValue(body, "type");
  if (type == "welcome") {
    Serial.println("WELCOME " + jsonValue(body, "protocol"));
  } else if (type == "asr.final") {
    Serial.println("ASR: " + jsonValue(body, "text"));
  } else if (type == "tts.say") {
    Serial.println("TTS: " + jsonValue(body, "text"));
  } else if (type == "tts.audio") {
    String pcm = jsonValue(body, "pcm");
    if (pcm.length()) playAudio(pcm);
  } else if (type == "error") {
    Serial.println("ERR: " + jsonValue(body, "message"));
  }
}

void streamMicWhilePressed() {
  if (!talking) {
    talking = true;
    Serial.println("PTT_START");
  }
  int16_t samples[256];
  size_t bytesRead = 0;
  i2s_read(I2S_NUM_0, samples, sizeof(samples), &bytesRead, 10 / portTICK_PERIOD_MS);
  if (bytesRead > 0) {
    String b64 = b64encode((uint8_t*)samples, bytesRead);
    // codec "pcm16" here; for Opus, encode with libopus and set codec "opus".
    wsSendEnvelope("audio.append", "{\"codec\":\"pcm16\",\"pcm\":\"" + b64 + "\"}");
  }
}

// ---- Config portal (minimal) ----

void portalRoot() {
  int n = WiFi.scanNetworks(false, true);
  String h = "<!doctype html><meta name=viewport content='width=device-width,initial-scale=1'><h1>Voice Setup</h1><form method=post action=/save>";
  h += "SSID<input name=ssid value='" + config.ssid + "'>";
  h += "Pass<input name=password type=password>";
  h += "Gateway<input name=gateway value='" + config.gateway + "'>";
  h += "DeviceID<input name=deviceId value='" + config.deviceId + "'>";
  h += "<button>Save</button></form>";
  (void)n;
  portal.send(200, "text/html", h);
}
void portalSave() {
  config.ssid = portal.arg("ssid");
  config.password = portal.arg("password");
  config.gateway = portal.arg("gateway");
  config.deviceId = portal.arg("deviceId");
  config.token = "";
  saveConfig();
  portal.send(200, "text/html", "Saved. Rebooting...");
  delay(600);
  ESP.restart();
}
void startPortal() {
  portalMode = true;
  WiFi.mode(WIFI_AP_STA);
  WiFi.softAPConfig(portalIP, portalIP, portalSubnet);
  WiFi.softAP(("LightGateway-" + chipId()).c_str());
  dnsServer.start(53, "*", portalIP);
  portal.on("/", HTTP_GET, portalRoot);
  portal.on("/save", HTTP_POST, portalSave);
  portal.onNotFound(portalRoot);
  portal.begin();
  Serial.println("CONFIG_PORTAL http://192.168.4.1");
}

bool connectWiFi() {
  if (config.ssid == "") return false;
  WiFi.mode(WIFI_STA);
  WiFi.begin(config.ssid.c_str(), config.password.c_str());
  unsigned long t0 = millis();
  while (WiFi.status() != WL_CONNECTED && millis() - t0 < 20000) delay(500);
  return WiFi.status() == WL_CONNECTED;
}

void registerDevice() {
  HTTPClient http;
  http.begin(config.gateway + "/api/v1/devices/register");
  http.addHeader("Content-Type", "application/json");
  String payload = "{\"id\":\"" + config.deviceId + "\",\"name\":\"" + config.deviceName +
                   "\",\"type\":\"esp\",\"category\":\"voice\",\"profile\":\"voice.v1\",\"model\":\"esp32-s3-xiaozhi\",\"fwVersion\":\"esp32-voice/0.1.0\"}";
  int code = http.POST(payload);
  if (code >= 200 && code < 300) {
    String token = jsonValue(http.getString(), "token");
    if (token.length()) {
      config.token = token;
      saveConfig();
    }
  }
  http.end();
}

void heartbeat() {
  HTTPClient http;
  http.begin(config.gateway + "/api/v1/devices/" + config.deviceId + "/heartbeat");
  http.addHeader("Content-Type", "application/json");
  http.addHeader("X-Device-Token", config.token);
  http.POST("{\"agentVersion\":\"esp32-voice/0.1.0\"}");
  http.end();
}

void setup() {
  Serial.begin(115200);
  delay(300);
  pinMode(BUTTON_PIN, INPUT_PULLUP);
  loadConfig();
  Serial.println("LIGHT_GATEWAY_ESP32_VOICE");
  if (!connectWiFi()) {
    startPortal();
    return;
  }
  setupI2S();
  registerDevice();
  heartbeat();
  wsConnected = wsHandshake();
}

void loop() {
  if (portalMode) {
    dnsServer.processNextRequest();
    portal.handleClient();
    delay(5);
    return;
  }
  if (WiFi.status() != WL_CONNECTED) {
    connectWiFi();
    delay(1000);
    return;
  }
  if (!wsConnected || !ws.connected()) {
    delay(1000);
    wsConnected = wsHandshake();
    return;
  }

  // Drain inbound frames.
  while (ws.available() >= 2) {
    String body;
    int op = wsReadFrame(body);
    if (op == 0x8) {  // close
      ws.stop();
      wsConnected = false;
      return;
    }
    if (op == 0x1 || op == 0x0) handleServerMessage(body);
  }

  // Push-to-talk.
  if (digitalRead(BUTTON_PIN) == LOW) {
    streamMicWhilePressed();
  } else if (talking) {
    talking = false;
    wsSendEnvelope("audio.commit", "");
    Serial.println("PTT_COMMIT");
  }

  // Typed test input over Serial -> text.input.
  if (Serial.available()) {
    String line = Serial.readStringUntil('\n');
    line.trim();
    if (line.length()) wsSendEnvelope("text.input", "{\"text\":\"" + line + "\"}");
  }

  unsigned long now = millis();
  if (now - lastHeartbeatAt > 15000) {
    lastHeartbeatAt = now;
    heartbeat();
  }
  delay(2);
}
