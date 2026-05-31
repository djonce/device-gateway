// Light Gateway — ESP32 night light / RGB strip firmware (category=light).
//
// Registers to the gateway as a `light` product (profile light.v1) and
// implements the light.* command contract:
//   light.power      {"on": true|false}
//   light.brightness {"value": 0..100}
//   light.color      {"hex": "#RRGGBB"}  or  {"r":..,"g":..,"b":..}
//   light.effect     {"name": "static|breath|rainbow", "speed": 1..10}
//   light.schedule   {"on": "HH:MM", "off": "HH:MM"}   (auto on/off window)
// Reports telemetry: light.state (on/off), light.brightness (0..100).
//
// RGB output uses analogWrite on three PWM pins (works on Arduino-ESP32 2.x
// and 3.x without external libraries). For a WS2812 addressable strip, replace
// applyOutput() with a NeoPixel write; the command contract stays identical.

#include <DNSServer.h>
#include <HTTPClient.h>
#include <Preferences.h>
#include <Update.h>
#include <WebServer.h>
#include <WiFi.h>
#include <time.h>
#include "mbedtls/sha256.h"

// PWM output pins for the common-cathode RGB channels.
const int PIN_R = 25;
const int PIN_G = 26;
const int PIN_B = 27;

Preferences prefs;
DNSServer dnsServer;
WebServer server(80);
IPAddress portalIP(192, 168, 4, 1);
IPAddress portalSubnet(255, 255, 255, 0);

struct Config {
  String ssid;
  String password;
  String gateway;
  String deviceId;
  String deviceName;
  String token;
  String tz;  // POSIX TZ, default China Standard Time
};

struct LightState {
  bool on = true;
  int brightness = 80;  // 0..100
  uint8_t r = 255, g = 217, b = 160;
  String effect = "static";
  int speed = 5;       // 1..10
  int onMinutes = -1;  // schedule window start, minutes since midnight; -1 = unset
  int offMinutes = -1;
};

Config config;
LightState light;
unsigned long lastHeartbeatAt = 0;
unsigned long lastTelemetryAt = 0;
unsigned long lastCommandPollAt = 0;
unsigned long lastEffectTickAt = 0;
unsigned long lastOtaCheckAt = 0;
int lastScheduleState = -1;  // -1 unknown, 0 off, 1 on
float effectPhase = 0.0f;
bool portalMode = false;

String chipId() {
  uint64_t mac = ESP.getEfuseMac();
  char value[17];
  snprintf(value, sizeof(value), "%04X%08X", (uint16_t)(mac >> 32), (uint32_t)mac);
  return String(value);
}

String defaultDeviceId() { return "esp32-light-" + chipId(); }

String jsonEscape(const String& value) {
  String out;
  out.reserve(value.length() + 8);
  for (size_t i = 0; i < value.length(); i++) {
    char c = value[i];
    if (c == '"' || c == '\\') {
      out += '\\';
      out += c;
    } else if (c == '\n') {
      out += "\\n";
    } else if (c == '\r') {
      out += "\\r";
    } else {
      out += c;
    }
  }
  return out;
}

String htmlEscape(const String& value) {
  String out;
  for (size_t i = 0; i < value.length(); i++) {
    char c = value[i];
    if (c == '&') out += "&amp;";
    else if (c == '<') out += "&lt;";
    else if (c == '>') out += "&gt;";
    else if (c == '"') out += "&quot;";
    else if (c == '\'') out += "&#39;";
    else out += c;
  }
  return out;
}

String jsonValue(const String& body, const String& key) {
  String marker = "\"" + key + "\":\"";
  int start = body.indexOf(marker);
  if (start < 0) return "";
  start += marker.length();
  int end = body.indexOf("\"", start);
  if (end < 0) return "";
  return body.substring(start, end);
}

String jsonNumberish(const String& body, const String& key) {
  String marker = "\"" + key + "\":";
  int start = body.indexOf(marker);
  if (start < 0) return "";
  start += marker.length();
  while (start < (int)body.length() && body[start] == ' ') start++;
  int end = start;
  while (end < (int)body.length()) {
    char c = body[end];
    if (c == ',' || c == '}' || c == '\n' || c == '\r') break;
    end++;
  }
  return body.substring(start, end);
}

void loadConfig() {
  prefs.begin("lightgw", false);
  config.ssid = prefs.getString("ssid", "");
  config.password = prefs.getString("pass", "");
  config.gateway = prefs.getString("gateway", "http://192.168.3.109:7001");
  config.deviceId = prefs.getString("deviceId", defaultDeviceId());
  config.deviceName = prefs.getString("name", "ESP32 Night Light");
  config.token = prefs.getString("token", "");
  config.tz = prefs.getString("tz", "CST-8");
  light.brightness = prefs.getInt("bri", 80);
  light.r = prefs.getUChar("r", 255);
  light.g = prefs.getUChar("g", 217);
  light.b = prefs.getUChar("b", 160);
  light.on = prefs.getBool("on", true);
}

void saveNetwork() {
  prefs.putString("ssid", config.ssid);
  prefs.putString("pass", config.password);
  prefs.putString("gateway", config.gateway);
  prefs.putString("deviceId", config.deviceId);
  prefs.putString("name", config.deviceName);
  prefs.putString("token", config.token);
}

void saveLight() {
  prefs.putInt("bri", light.brightness);
  prefs.putUChar("r", light.r);
  prefs.putUChar("g", light.g);
  prefs.putUChar("b", light.b);
  prefs.putBool("on", light.on);
}

// ---- LED output ----

uint8_t scaleChannel(uint8_t value, int brightness, float gain) {
  long out = (long)value * brightness / 100;
  out = (long)(out * gain);
  if (out < 0) out = 0;
  if (out > 255) out = 255;
  return (uint8_t)out;
}

void applyOutput() {
  float gain = 1.0f;
  if (light.on) {
    if (light.effect == "breath") {
      gain = 0.15f + 0.85f * (0.5f * (1.0f + sinf(effectPhase)));
    }
  }
  if (!light.on) {
    analogWrite(PIN_R, 0);
    analogWrite(PIN_G, 0);
    analogWrite(PIN_B, 0);
    return;
  }
  if (light.effect == "rainbow") {
    // Cycle hue; brightness still applies.
    float h = fmodf(effectPhase * 0.15f, 6.2831853f);
    uint8_t rr = (uint8_t)(127.5f * (1.0f + sinf(h)));
    uint8_t gg = (uint8_t)(127.5f * (1.0f + sinf(h + 2.094f)));
    uint8_t bb = (uint8_t)(127.5f * (1.0f + sinf(h + 4.188f)));
    analogWrite(PIN_R, scaleChannel(rr, light.brightness, 1.0f));
    analogWrite(PIN_G, scaleChannel(gg, light.brightness, 1.0f));
    analogWrite(PIN_B, scaleChannel(bb, light.brightness, 1.0f));
    return;
  }
  analogWrite(PIN_R, scaleChannel(light.r, light.brightness, gain));
  analogWrite(PIN_G, scaleChannel(light.g, light.brightness, gain));
  analogWrite(PIN_B, scaleChannel(light.b, light.brightness, gain));
}

// ---- Config portal (same provisioning UX as the generic Wi-Fi agent) ----

void sendHTML() {
  int networkCount = WiFi.scanNetworks(false, true);
  String html = "<!doctype html><html><head><meta name='viewport' content='width=device-width,initial-scale=1'>";
  html += "<title>Light Gateway Setup</title>";
  html += "<style>body{font-family:-apple-system,Segoe UI,sans-serif;margin:24px;max-width:620px;background:#f6f8fb;color:#1c2331}main{background:#fff;border:1px solid #dce3ee;border-radius:10px;padding:18px}label{display:block;margin:12px 0 6px;color:#5f6b7d}input{width:100%;padding:10px;border:1px solid #ccd;border-radius:8px;box-sizing:border-box}button{margin-top:16px;padding:10px 14px;border:0;border-radius:8px;background:#1464f4;color:#fff}</style>";
  html += "</head><body><h1>Night Light Setup</h1><main>";
  html += "<form method='post' action='/save'>";
  html += "<label>Wi-Fi SSID</label><input name='ssid' list='ssid-list' value='" + htmlEscape(config.ssid) + "' autocomplete='off'>";
  html += "<datalist id='ssid-list'>";
  for (int i = 0; i < networkCount; i++) html += "<option value='" + htmlEscape(WiFi.SSID(i)) + "'>";
  html += "</datalist>";
  html += "<label>Wi-Fi Password</label><input name='password' type='password' value=''>";
  html += "<label>Gateway URL</label><input name='gateway' value='" + htmlEscape(config.gateway) + "'>";
  html += "<label>Device ID</label><input name='deviceId' value='" + htmlEscape(config.deviceId) + "'>";
  html += "<label>Device Name</label><input name='deviceName' value='" + htmlEscape(config.deviceName) + "'>";
  html += "<label>Timezone (POSIX TZ)</label><input name='tz' value='" + htmlEscape(config.tz) + "'>";
  html += "<button type='submit'>Save and reboot</button></form>";
  html += "<p>AP: LightGateway-" + chipId() + "</p></main></body></html>";
  server.send(200, "text/html", html);
}

void handleSave() {
  config.ssid = server.arg("ssid");
  config.password = server.arg("password");
  config.gateway = server.arg("gateway");
  config.deviceId = server.arg("deviceId");
  config.deviceName = server.arg("deviceName");
  config.tz = server.arg("tz");
  config.token = "";
  saveNetwork();
  prefs.putString("tz", config.tz);
  server.send(200, "text/html", "<html><body><h1>Saved</h1><p>Rebooting...</p></body></html>");
  delay(600);
  ESP.restart();
}

void startPortal() {
  portalMode = true;
  String apName = "LightGateway-" + chipId();
  WiFi.mode(WIFI_AP_STA);
  WiFi.softAPConfig(portalIP, portalIP, portalSubnet);
  WiFi.softAP(apName.c_str());
  dnsServer.start(53, "*", portalIP);
  server.on("/", HTTP_GET, sendHTML);
  server.on("/generate_204", HTTP_GET, sendHTML);
  server.on("/hotspot-detect.html", HTTP_GET, sendHTML);
  server.on("/save", HTTP_POST, handleSave);
  server.onNotFound(sendHTML);
  server.begin();
  Serial.println("CONFIG_PORTAL " + apName + " http://192.168.4.1");
}

bool connectWiFi() {
  if (config.ssid == "") return false;
  WiFi.mode(WIFI_STA);
  WiFi.begin(config.ssid.c_str(), config.password.c_str());
  Serial.print("WIFI_CONNECTING ");
  Serial.println(config.ssid);
  unsigned long startedAt = millis();
  while (WiFi.status() != WL_CONNECTED && millis() - startedAt < 20000) {
    delay(500);
    Serial.print(".");
  }
  Serial.println();
  if (WiFi.status() == WL_CONNECTED) {
    Serial.print("WIFI_CONNECTED ");
    Serial.println(WiFi.localIP());
    return true;
  }
  Serial.println("WIFI_FAILED");
  return false;
}

bool httpJSON(const String& method, const String& path, const String& payload, String& response) {
  if (WiFi.status() != WL_CONNECTED) return false;
  HTTPClient http;
  http.begin(config.gateway + path);
  http.addHeader("Content-Type", "application/json");
  if (config.token != "") http.addHeader("X-Device-Token", config.token);
  int code = method == "POST" ? http.POST(payload) : http.GET();
  response = http.getString();
  http.end();
  Serial.printf("HTTP %s %s %d\n", method.c_str(), path.c_str(), code);
  return code >= 200 && code < 300;
}

void registerDevice() {
  String payload = "{";
  payload += "\"id\":\"" + jsonEscape(config.deviceId) + "\",";
  payload += "\"name\":\"" + jsonEscape(config.deviceName) + "\",";
  payload += "\"type\":\"esp\",";
  payload += "\"category\":\"light\",";
  payload += "\"profile\":\"light.v1\",";
  payload += "\"model\":\"esp32-rgb-strip\",";
  payload += "\"fwVersion\":\"esp32-light/0.1.0\",";
  payload += "\"agentVersion\":\"esp32-light/0.1.0\",";
  payload += "\"labels\":{\"transport\":\"wifi\",\"chip\":\"esp32\"},";
  payload += "\"metadata\":{\"ip\":\"" + WiFi.localIP().toString() + "\",\"mac\":\"" + WiFi.macAddress() + "\"}";
  payload += "}";
  String response;
  if (!httpJSON("POST", "/api/v1/devices/register", payload, response)) return;
  String token = jsonValue(response, "token");
  if (token != "") {
    config.token = token;
    saveNetwork();
    Serial.println("TOKEN_STORED");
  }
}

void heartbeat() {
  String payload = "{\"agentVersion\":\"esp32-light/0.1.0\",\"metadata\":{\"rssi\":" + String(WiFi.RSSI()) + ",\"heap\":" + String(ESP.getFreeHeap()) + "}}";
  String response;
  httpJSON("POST", "/api/v1/devices/" + config.deviceId + "/heartbeat", payload, response);
}

void telemetry(const String& key, const String& value, bool quote) {
  String payload = "{\"key\":\"" + jsonEscape(key) + "\",\"value\":";
  payload += quote ? "\"" + jsonEscape(value) + "\"" : value;
  payload += "}";
  String response;
  httpJSON("POST", "/api/v1/devices/" + config.deviceId + "/telemetry", payload, response);
}

void reportLightState() {
  telemetry("light.state", light.on ? "on" : "off", true);
  telemetry("light.brightness", String(light.brightness), false);
}

void ackCommand(const String& commandId, const String& status, const String& result, const String& error) {
  String payload = "{\"status\":\"" + status + "\"";
  if (result != "") payload += ",\"result\":" + result;
  if (error != "") payload += ",\"error\":\"" + jsonEscape(error) + "\"";
  payload += "}";
  String response;
  httpJSON("POST", "/api/v1/devices/" + config.deviceId + "/commands/" + commandId + "/ack", payload, response);
}

int parseHHMM(const String& value) {
  int colon = value.indexOf(':');
  if (colon < 0) return -1;
  int h = value.substring(0, colon).toInt();
  int m = value.substring(colon + 1).toInt();
  if (h < 0 || h > 23 || m < 0 || m > 59) return -1;
  return h * 60 + m;
}

bool parseHexColor(const String& hex, uint8_t& r, uint8_t& g, uint8_t& b) {
  String h = hex;
  if (h.startsWith("#")) h = h.substring(1);
  if (h.length() != 6) return false;
  long value = strtol(h.c_str(), nullptr, 16);
  r = (value >> 16) & 0xFF;
  g = (value >> 8) & 0xFF;
  b = value & 0xFF;
  return true;
}

void handleCommand(const String& body) {
  String commandId = jsonValue(body, "id");
  if (commandId == "") return;
  String type = jsonValue(body, "type");

  if (type == "light.power") {
    String v = jsonNumberish(body, "on");
    light.on = (v == "true" || v == "1");
    applyOutput();
    saveLight();
    reportLightState();
    ackCommand(commandId, "succeeded", String("{\"on\":") + (light.on ? "true" : "false") + "}", "");
  } else if (type == "light.brightness") {
    int value = jsonNumberish(body, "value").toInt();
    if (value < 0 || value > 100) {
      ackCommand(commandId, "failed", "", "value must be 0..100");
      return;
    }
    light.brightness = value;
    light.on = value > 0 ? true : light.on;
    applyOutput();
    saveLight();
    reportLightState();
    ackCommand(commandId, "succeeded", String("{\"brightness\":") + value + "}", "");
  } else if (type == "light.color") {
    String hex = jsonValue(body, "hex");
    uint8_t r, g, b;
    if (hex != "" && parseHexColor(hex, r, g, b)) {
      light.r = r; light.g = g; light.b = b;
    } else {
      light.r = (uint8_t)constrain(jsonNumberish(body, "r").toInt(), 0, 255);
      light.g = (uint8_t)constrain(jsonNumberish(body, "g").toInt(), 0, 255);
      light.b = (uint8_t)constrain(jsonNumberish(body, "b").toInt(), 0, 255);
    }
    applyOutput();
    saveLight();
    char buf[48];
    snprintf(buf, sizeof(buf), "{\"r\":%d,\"g\":%d,\"b\":%d}", light.r, light.g, light.b);
    ackCommand(commandId, "succeeded", String(buf), "");
  } else if (type == "light.effect") {
    String name = jsonValue(body, "name");
    if (name != "static" && name != "breath" && name != "rainbow") {
      ackCommand(commandId, "failed", "", "unknown effect");
      return;
    }
    light.effect = name;
    int sp = jsonNumberish(body, "speed").toInt();
    if (sp >= 1 && sp <= 10) light.speed = sp;
    applyOutput();
    ackCommand(commandId, "succeeded", "{\"name\":\"" + name + "\",\"speed\":" + String(light.speed) + "}", "");
  } else if (type == "light.schedule") {
    light.onMinutes = parseHHMM(jsonValue(body, "on"));
    light.offMinutes = parseHHMM(jsonValue(body, "off"));
    lastScheduleState = -1;  // force re-evaluation
    ackCommand(commandId, "succeeded", "{\"on\":\"" + jsonValue(body, "on") + "\",\"off\":\"" + jsonValue(body, "off") + "\"}", "");
  } else {
    ackCommand(commandId, "failed", "", "unsupported command type");
  }
}

void pollCommand() {
  String response;
  if (!httpJSON("GET", "/api/v1/devices/" + config.deviceId + "/commands/next?timeout=1", "", response)) return;
  if (response.indexOf("\"command\":null") >= 0) return;
  handleCommand(response);
}

// Auto on/off from the schedule window using NTP-synced local time.
void evaluateSchedule() {
  if (light.onMinutes < 0 || light.offMinutes < 0) return;
  struct tm now;
  if (!getLocalTime(&now, 50)) return;  // time not synced yet
  int cur = now.tm_hour * 60 + now.tm_min;
  bool inWindow;
  if (light.onMinutes <= light.offMinutes) {
    inWindow = cur >= light.onMinutes && cur < light.offMinutes;
  } else {
    // Overnight window, e.g. 22:00 -> 07:00
    inWindow = cur >= light.onMinutes || cur < light.offMinutes;
  }
  int desired = inWindow ? 1 : 0;
  if (desired != lastScheduleState) {
    lastScheduleState = desired;
    light.on = inWindow;
    applyOutput();
    saveLight();
    reportLightState();
  }
}

// ---- OTA self-update ----

String toHex(const uint8_t* data, size_t n) {
  static const char* hexd = "0123456789abcdef";
  String s;
  s.reserve(n * 2);
  for (size_t i = 0; i < n; i++) {
    s += hexd[data[i] >> 4];
    s += hexd[data[i] & 0xF];
  }
  return s;
}

bool performOTA(const String& url, const String& expectedSha, int expectedSize) {
  HTTPClient http;
  http.begin(config.gateway + url);
  if (config.token != "") http.addHeader("X-Device-Token", config.token);
  int code = http.GET();
  if (code != 200) {
    Serial.printf("OTA_HTTP %d\n", code);
    http.end();
    return false;
  }
  int len = http.getSize();
  if (len <= 0) len = expectedSize;
  if (!Update.begin(len > 0 ? (size_t)len : UPDATE_SIZE_UNKNOWN)) {
    Serial.println("OTA_BEGIN_FAIL");
    http.end();
    return false;
  }

  mbedtls_sha256_context sha;
  mbedtls_sha256_init(&sha);
  mbedtls_sha256_starts(&sha, 0);  // 0 = SHA-256
  WiFiClient* stream = http.getStreamPtr();
  uint8_t buf[1024];
  int remaining = len;
  unsigned long t0 = millis();
  while (http.connected() && (remaining > 0 || len <= 0)) {
    size_t avail = stream->available();
    if (avail) {
      int n = stream->readBytes(buf, avail > sizeof(buf) ? sizeof(buf) : avail);
      if (n <= 0) break;
      mbedtls_sha256_update(&sha, buf, n);
      if (Update.write(buf, n) != (size_t)n) {
        Serial.println("OTA_WRITE_FAIL");
        Update.abort();
        http.end();
        return false;
      }
      if (len > 0) remaining -= n;
      t0 = millis();
    } else {
      if (millis() - t0 > 8000) break;  // stall guard
      delay(1);
    }
  }
  uint8_t digest[32];
  mbedtls_sha256_finish(&sha, digest);
  mbedtls_sha256_free(&sha);
  http.end();

  if (expectedSha != "" && !expectedSha.equalsIgnoreCase(toHex(digest, 32))) {
    Serial.println("OTA_SHA_MISMATCH");
    Update.abort();
    return false;
  }
  if (!Update.end(true)) {
    Serial.printf("OTA_END_FAIL %d\n", Update.getError());
    return false;
  }
  Serial.println("OTA_OK rebooting");
  delay(500);
  ESP.restart();
  return true;
}

void checkOTA() {
  String resp;
  if (!httpJSON("GET", "/api/v1/devices/" + config.deviceId + "/ota", "", resp)) return;
  if (resp.indexOf("\"updateAvailable\":true") < 0) return;
  String url = jsonValue(resp, "downloadUrl");
  String sha = jsonValue(resp, "sha256");
  String version = jsonValue(resp, "version");
  int size = jsonNumberish(resp, "size").toInt();
  if (url == "") return;
  Serial.println("OTA_UPDATE -> " + version);
  performOTA(url, sha, size);
}

void setup() {
  Serial.begin(115200);
  delay(300);
  loadConfig();
  analogWrite(PIN_R, 0);
  analogWrite(PIN_G, 0);
  analogWrite(PIN_B, 0);
  applyOutput();
  Serial.println("LIGHT_GATEWAY_ESP32_LIGHT");
  Serial.println("DEVICE_ID " + config.deviceId);
  if (!connectWiFi()) {
    startPortal();
    return;
  }
  setenv("TZ", config.tz.c_str(), 1);
  tzset();
  configTime(0, 0, "pool.ntp.org", "time.nist.gov");
  registerDevice();
  heartbeat();
  reportLightState();
}

void loop() {
  if (portalMode) {
    dnsServer.processNextRequest();
    server.handleClient();
    delay(5);
    return;
  }
  if (WiFi.status() != WL_CONNECTED) {
    connectWiFi();
    delay(1000);
    return;
  }
  unsigned long now = millis();
  if (now - lastEffectTickAt > 30) {
    lastEffectTickAt = now;
    if (light.on && (light.effect == "breath" || light.effect == "rainbow")) {
      effectPhase += 0.01f * light.speed;
      applyOutput();
    }
  }
  if (now - lastHeartbeatAt > 10000) {
    lastHeartbeatAt = now;
    heartbeat();
    evaluateSchedule();
  }
  if (now - lastTelemetryAt > 30000) {
    lastTelemetryAt = now;
    reportLightState();
  }
  if (now - lastCommandPollAt > 2000) {
    lastCommandPollAt = now;
    pollCommand();
  }
  if (now - lastOtaCheckAt > 60000) {  // check for firmware updates every minute
    lastOtaCheckAt = now;
    checkOTA();
  }
  delay(20);
}
