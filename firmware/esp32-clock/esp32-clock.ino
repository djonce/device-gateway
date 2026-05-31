// Light Gateway — ESP32 clock / calendar / weather display (category=clock).
//
// Registers as a `clock` product (profile clock.v1) and:
//   * routinely PULLS /api/v1/content/clock?lat&lon&tz from the gateway, which
//     proxies Open-Meteo (no API key on the device) and returns time + weather;
//   * handles PUSH commands for immediate updates / control:
//       display.mode       {"mode": "clock|calendar|weather"}
//       display.brightness {"value": 0..100}
//       time.sync          {"epoch": 1730000000, "tz": "CST-8"}
//       weather.push       {"temp": 21.4, "cond": "晴", "city": "..."}
//
// Rendering here writes to Serial as a stand-in "screen". Wire renderScreen()
// to your actual panel (SSD1306/TFT/LED matrix); the data flow stays the same.

#include <DNSServer.h>
#include <HTTPClient.h>
#include <Preferences.h>
#include <WebServer.h>
#include <WiFi.h>
#include <time.h>
#include <sys/time.h>
#include <math.h>

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
  String tz;   // POSIX TZ for local time, default China Standard Time
  String lat;  // latitude for weather content
  String lon;  // longitude
};

struct Display {
  String mode = "clock";  // clock | calendar | weather
  int brightness = 80;    // 0..100
  float tempC = NAN;
  String cond = "--";
};

Config config;
Display screen;
unsigned long lastHeartbeatAt = 0;
unsigned long lastContentAt = 0;
unsigned long lastCommandPollAt = 0;
unsigned long lastRenderAt = 0;
bool portalMode = false;
bool timeSynced = false;

String chipId() {
  uint64_t mac = ESP.getEfuseMac();
  char value[17];
  snprintf(value, sizeof(value), "%04X%08X", (uint16_t)(mac >> 32), (uint32_t)mac);
  return String(value);
}

String defaultDeviceId() { return "esp32-clock-" + chipId(); }

String jsonEscape(const String& value) {
  String out;
  for (size_t i = 0; i < value.length(); i++) {
    char c = value[i];
    if (c == '"' || c == '\\') { out += '\\'; out += c; }
    else if (c == '\n') out += "\\n";
    else if (c == '\r') out += "\\r";
    else out += c;
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
  config.deviceName = prefs.getString("name", "ESP32 Clock");
  config.token = prefs.getString("token", "");
  config.tz = prefs.getString("tz", "CST-8");
  config.lat = prefs.getString("lat", "31.2304");
  config.lon = prefs.getString("lon", "121.4737");
  screen.brightness = prefs.getInt("bri", 80);
  screen.mode = prefs.getString("mode", "clock");
}

void saveNetwork() {
  prefs.putString("ssid", config.ssid);
  prefs.putString("pass", config.password);
  prefs.putString("gateway", config.gateway);
  prefs.putString("deviceId", config.deviceId);
  prefs.putString("name", config.deviceName);
  prefs.putString("token", config.token);
  prefs.putString("tz", config.tz);
  prefs.putString("lat", config.lat);
  prefs.putString("lon", config.lon);
}

// ---- "Screen" rendering (replace with your real display driver) ----

void renderScreen() {
  struct tm now;
  char timeStr[32] = "--:--:--";
  char dateStr[32] = "----------";
  if (getLocalTime(&now, 10)) {
    strftime(timeStr, sizeof(timeStr), "%H:%M:%S", &now);
    strftime(dateStr, sizeof(dateStr), "%Y-%m-%d %a", &now);
  }
  String tempStr = isnan(screen.tempC) ? "--" : String(screen.tempC, 1) + "C";
  Serial.printf("[SCREEN mode=%s bri=%d] ", screen.mode.c_str(), screen.brightness);
  if (screen.mode == "weather") {
    Serial.printf("%s  %s  %s\n", dateStr, tempStr.c_str(), screen.cond.c_str());
  } else if (screen.mode == "calendar") {
    Serial.printf("%s  (%s)\n", dateStr, timeStr);
  } else {
    Serial.printf("%s   %s %s\n", timeStr, tempStr.c_str(), screen.cond.c_str());
  }
}

// ---- Config portal ----

void sendHTML() {
  int networkCount = WiFi.scanNetworks(false, true);
  String html = "<!doctype html><html><head><meta name='viewport' content='width=device-width,initial-scale=1'>";
  html += "<title>Clock Setup</title>";
  html += "<style>body{font-family:-apple-system,Segoe UI,sans-serif;margin:24px;max-width:620px;background:#f6f8fb;color:#1c2331}main{background:#fff;border:1px solid #dce3ee;border-radius:10px;padding:18px}label{display:block;margin:12px 0 6px;color:#5f6b7d}input{width:100%;padding:10px;border:1px solid #ccd;border-radius:8px;box-sizing:border-box}button{margin-top:16px;padding:10px 14px;border:0;border-radius:8px;background:#1464f4;color:#fff}</style>";
  html += "</head><body><h1>Clock / Weather Setup</h1><main>";
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
  html += "<label>Latitude</label><input name='lat' value='" + htmlEscape(config.lat) + "'>";
  html += "<label>Longitude</label><input name='lon' value='" + htmlEscape(config.lon) + "'>";
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
  config.lat = server.arg("lat");
  config.lon = server.arg("lon");
  config.token = "";
  saveNetwork();
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
  payload += "\"category\":\"clock\",";
  payload += "\"profile\":\"clock.v1\",";
  payload += "\"model\":\"esp32-clock-weather\",";
  payload += "\"fwVersion\":\"esp32-clock/0.1.0\",";
  payload += "\"agentVersion\":\"esp32-clock/0.1.0\",";
  payload += "\"labels\":{\"transport\":\"wifi\",\"chip\":\"esp32\",\"lat\":\"" + config.lat + "\",\"lon\":\"" + config.lon + "\"},";
  payload += "\"metadata\":{\"ip\":\"" + WiFi.localIP().toString() + "\",\"tz\":\"" + config.tz + "\"}";
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
  String payload = "{\"agentVersion\":\"esp32-clock/0.1.0\",\"metadata\":{\"rssi\":" + String(WiFi.RSSI()) + ",\"mode\":\"" + screen.mode + "\"}}";
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

void applyEpoch(long epoch) {
  if (epoch <= 0) return;
  struct timeval tv;
  tv.tv_sec = (time_t)epoch;
  tv.tv_usec = 0;
  settimeofday(&tv, nullptr);
  timeSynced = true;
}

// Routine pull: gateway proxies Open-Meteo and returns time + weather.
void pullContent() {
  String response;
  String path = "/api/v1/content/clock?lat=" + config.lat + "&lon=" + config.lon + "&tz=UTC";
  if (!httpJSON("GET", path, "", response)) return;
  String epoch = jsonNumberish(response, "epoch");
  if (epoch.length() > 0) applyEpoch(epoch.toInt());
  String temp = jsonNumberish(response, "tempC");
  if (temp.length() > 0) screen.tempC = temp.toFloat();
  String text = jsonValue(response, "text");
  if (text.length() > 0) screen.cond = text;
  renderScreen();
}

void ackCommand(const String& commandId, const String& status, const String& result, const String& error) {
  String payload = "{\"status\":\"" + status + "\"";
  if (result != "") payload += ",\"result\":" + result;
  if (error != "") payload += ",\"error\":\"" + jsonEscape(error) + "\"";
  payload += "}";
  String response;
  httpJSON("POST", "/api/v1/devices/" + config.deviceId + "/commands/" + commandId + "/ack", payload, response);
}

void handleCommand(const String& body) {
  String commandId = jsonValue(body, "id");
  if (commandId == "") return;
  String type = jsonValue(body, "type");

  if (type == "display.mode") {
    String mode = jsonValue(body, "mode");
    if (mode != "clock" && mode != "calendar" && mode != "weather") {
      ackCommand(commandId, "failed", "", "unknown mode");
      return;
    }
    screen.mode = mode;
    prefs.putString("mode", mode);
    renderScreen();
    telemetry("display.mode", mode, true);
    ackCommand(commandId, "succeeded", "{\"mode\":\"" + mode + "\"}", "");
  } else if (type == "display.brightness") {
    int value = jsonNumberish(body, "value").toInt();
    if (value < 0 || value > 100) {
      ackCommand(commandId, "failed", "", "value must be 0..100");
      return;
    }
    screen.brightness = value;
    prefs.putInt("bri", value);
    renderScreen();
    ackCommand(commandId, "succeeded", String("{\"brightness\":") + value + "}", "");
  } else if (type == "time.sync") {
    String tz = jsonValue(body, "tz");
    if (tz.length() > 0) {
      config.tz = tz;
      setenv("TZ", config.tz.c_str(), 1);
      tzset();
      prefs.putString("tz", config.tz);
    }
    applyEpoch(jsonNumberish(body, "epoch").toInt());
    renderScreen();
    ackCommand(commandId, "succeeded", "{\"synced\":true}", "");
  } else if (type == "weather.push") {
    String temp = jsonNumberish(body, "temp");
    if (temp.length() > 0) screen.tempC = temp.toFloat();
    String cond = jsonValue(body, "cond");
    if (cond.length() > 0) screen.cond = cond;
    renderScreen();
    ackCommand(commandId, "succeeded", "{\"applied\":true}", "");
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

void setup() {
  Serial.begin(115200);
  delay(300);
  loadConfig();
  Serial.println("LIGHT_GATEWAY_ESP32_CLOCK");
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
  pullContent();
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
  if (now - lastRenderAt > 1000) {
    lastRenderAt = now;
    renderScreen();  // tick the clock once per second
  }
  if (now - lastCommandPollAt > 2000) {
    lastCommandPollAt = now;
    pollCommand();
  }
  if (now - lastHeartbeatAt > 10000) {
    lastHeartbeatAt = now;
    heartbeat();
  }
  if (now - lastContentAt > 600000) {  // refresh weather every 10 min
    lastContentAt = now;
    pullContent();
  }
  delay(20);
}
