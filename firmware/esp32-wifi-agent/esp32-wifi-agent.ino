#include <DNSServer.h>
#include <HTTPClient.h>
#include <Preferences.h>
#include <WebServer.h>
#include <WiFi.h>

Preferences prefs;
DNSServer dnsServer;
WebServer server(80);
IPAddress portalIP(192, 168, 4, 1);
IPAddress portalGateway(192, 168, 4, 1);
IPAddress portalSubnet(255, 255, 255, 0);

struct Config {
  String ssid;
  String password;
  String gateway;
  String deviceId;
  String deviceName;
  String token;
};

Config config;
unsigned long lastHeartbeatAt = 0;
unsigned long lastTelemetryAt = 0;
unsigned long lastCommandPollAt = 0;
bool portalMode = false;

String chipId() {
  uint64_t mac = ESP.getEfuseMac();
  char value[17];
  snprintf(value, sizeof(value), "%04X%08X", (uint16_t)(mac >> 32), (uint32_t)mac);
  return String(value);
}

String defaultDeviceId() {
  return "esp32-" + chipId();
}

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
  out.reserve(value.length() + 8);
  for (size_t i = 0; i < value.length(); i++) {
    char c = value[i];
    if (c == '&') {
      out += "&amp;";
    } else if (c == '<') {
      out += "&lt;";
    } else if (c == '>') {
      out += "&gt;";
    } else if (c == '"') {
      out += "&quot;";
    } else if (c == '\'') {
      out += "&#39;";
    } else {
      out += c;
    }
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
  config.deviceName = prefs.getString("name", "ESP32 WiFi Board");
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

void sendHTML() {
  int networkCount = WiFi.scanNetworks(false, true);
  String html = "<!doctype html><html><head><meta name='viewport' content='width=device-width,initial-scale=1'>";
  html += "<title>Light Gateway Setup</title>";
  html += "<style>body{font-family:-apple-system,BlinkMacSystemFont,Segoe UI,sans-serif;margin:24px;max-width:620px;background:#f6f8fb;color:#1c2331}main{background:#fff;border:1px solid #dce3ee;border-radius:10px;padding:18px}label{display:block;margin:12px 0 6px;color:#5f6b7d}input{width:100%;padding:10px;border:1px solid #ccd;border-radius:8px;box-sizing:border-box}button{margin-top:16px;padding:10px 14px;border:0;border-radius:8px;background:#1464f4;color:#fff}.hint{color:#6d778a;font-size:13px}.row{display:grid;grid-template-columns:1fr auto;gap:8px;align-items:end}.pill{display:inline-block;margin:4px 6px 4px 0;padding:4px 8px;border:1px solid #dce3ee;border-radius:999px;font-size:12px}</style>";
  html += "</head><body><h1>Light Gateway Setup</h1>";
  html += "<main>";
  html += "<p class='hint'>Select your Wi-Fi, enter the password, then save. Hidden networks can be typed manually.</p>";
  html += "<form method='post' action='/save'>";
  html += "<label>Wi-Fi SSID</label><input name='ssid' list='ssid-list' value='" + htmlEscape(config.ssid) + "' autocomplete='off'>";
  html += "<datalist id='ssid-list'>";
  for (int i = 0; i < networkCount; i++) {
    html += "<option value='" + htmlEscape(WiFi.SSID(i)) + "'>";
  }
  html += "</datalist>";
  html += "<label>Wi-Fi Password</label><input name='password' type='password' value='" + config.password + "'>";
  html += "<label>Gateway URL</label><input name='gateway' value='" + config.gateway + "'>";
  html += "<label>Device ID</label><input name='deviceId' value='" + config.deviceId + "'>";
  html += "<label>Device Name</label><input name='deviceName' value='" + config.deviceName + "'>";
  html += "<button type='submit'>Save and reboot</button></form>";
  html += "<p class='hint'>Current AP: LightGateway-" + chipId() + "</p>";
  html += "<p class='hint'>Scanned networks: ";
  if (networkCount <= 0) {
    html += "none";
  } else {
    for (int i = 0; i < networkCount && i < 12; i++) {
      html += "<span class='pill'>" + htmlEscape(WiFi.SSID(i)) + " (" + String(WiFi.RSSI(i)) + " dBm)</span>";
    }
  }
  html += "</p>";
  html += "</main>";
  html += "</body></html>";
  server.send(200, "text/html", html);
}

void handleSave() {
  config.ssid = server.arg("ssid");
  config.password = server.arg("password");
  config.gateway = server.arg("gateway");
  config.deviceId = server.arg("deviceId");
  config.deviceName = server.arg("deviceName");
  config.token = "";
  saveConfig();
  server.send(200, "text/html", "<html><body><h1>Saved</h1><p>Rebooting...</p></body></html>");
  delay(600);
  ESP.restart();
}

void startPortal() {
  portalMode = true;
  String apName = "LightGateway-" + chipId();
  WiFi.mode(WIFI_AP_STA);
  WiFi.softAPConfig(portalIP, portalGateway, portalSubnet);
  WiFi.softAP(apName.c_str());
  dnsServer.start(53, "*", portalIP);
  server.on("/", HTTP_GET, sendHTML);
  server.on("/generate_204", HTTP_GET, sendHTML);
  server.on("/gen_204", HTTP_GET, sendHTML);
  server.on("/hotspot-detect.html", HTTP_GET, sendHTML);
  server.on("/connecttest.txt", HTTP_GET, sendHTML);
  server.on("/ncsi.txt", HTTP_GET, sendHTML);
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
  String url = config.gateway + path;
  http.begin(url);
  http.addHeader("Content-Type", "application/json");
  if (config.token != "") {
    http.addHeader("X-Device-Token", config.token);
  }
  int code = 0;
  if (method == "POST") {
    code = http.POST(payload);
  } else {
    code = http.GET();
  }
  response = http.getString();
  http.end();
  Serial.print("HTTP ");
  Serial.print(method);
  Serial.print(" ");
  Serial.print(path);
  Serial.print(" ");
  Serial.println(code);
  if (code >= 200 && code < 300) return true;
  Serial.println(response);
  return false;
}

void registerDevice() {
  String payload = "{";
  payload += "\"id\":\"" + jsonEscape(config.deviceId) + "\",";
  payload += "\"name\":\"" + jsonEscape(config.deviceName) + "\",";
  payload += "\"type\":\"esp\",";
  payload += "\"agentVersion\":\"esp32-wifi-agent/0.1.0\",";
  payload += "\"labels\":{\"transport\":\"wifi\",\"chip\":\"esp32\"},";
  payload += "\"capabilities\":[";
  payload += "{\"name\":\"sensor.read\",\"description\":\"read rssi uptime and heap\"},";
  payload += "{\"name\":\"gpio.write\",\"description\":\"write digital gpio\"}";
  payload += "],";
  payload += "\"metadata\":{\"ip\":\"" + WiFi.localIP().toString() + "\",\"mac\":\"" + WiFi.macAddress() + "\"}";
  payload += "}";
  String response;
  if (!httpJSON("POST", "/api/v1/devices/register", payload, response)) return;
  String token = jsonValue(response, "token");
  if (token != "") {
    config.token = token;
    saveConfig();
    Serial.println("TOKEN_STORED");
  }
}

void heartbeat() {
  String payload = "{";
  payload += "\"agentVersion\":\"esp32-wifi-agent/0.1.0\",";
  payload += "\"metadata\":{\"ip\":\"" + WiFi.localIP().toString() + "\",\"rssi\":" + String(WiFi.RSSI()) + ",\"heap\":" + String(ESP.getFreeHeap()) + "}";
  payload += "}";
  String response;
  httpJSON("POST", "/api/v1/devices/" + config.deviceId + "/heartbeat", payload, response);
}

void telemetry(const String& key, const String& value, const String& unit, bool quoteValue) {
  String payload = "{\"key\":\"" + jsonEscape(key) + "\",\"value\":";
  payload += quoteValue ? "\"" + jsonEscape(value) + "\"" : value;
  if (unit != "") payload += ",\"unit\":\"" + jsonEscape(unit) + "\"";
  payload += "}";
  String response;
  httpJSON("POST", "/api/v1/devices/" + config.deviceId + "/telemetry", payload, response);
}

void ackCommand(const String& commandId, const String& status, const String& result, const String& error) {
  String payload = "{\"status\":\"" + status + "\"";
  if (result != "") payload += ",\"result\":" + result;
  if (error != "") payload += ",\"error\":\"" + jsonEscape(error) + "\"";
  payload += "}";
  String response;
  httpJSON("POST", "/api/v1/devices/" + config.deviceId + "/commands/" + commandId + "/ack", payload, response);
}

void handleCommand(const String& commandBody) {
  String commandId = jsonValue(commandBody, "id");
  if (commandId == "") return;
  String type = jsonValue(commandBody, "type");
  if (type == "sensor.read") {
    String result = "{\"rssi\":" + String(WiFi.RSSI()) + ",\"uptimeMs\":" + String(millis()) + ",\"heap\":" + String(ESP.getFreeHeap()) + "}";
    ackCommand(commandId, "succeeded", result, "");
  } else if (type == "gpio.write") {
    int pin = jsonNumberish(commandBody, "pin").toInt();
    String rawValue = jsonNumberish(commandBody, "value");
    bool value = rawValue == "true" || rawValue == "1";
    if (pin < 0 || pin > 39) {
      ackCommand(commandId, "failed", "", "invalid gpio pin");
      return;
    }
    pinMode(pin, OUTPUT);
    digitalWrite(pin, value ? HIGH : LOW);
    String result = "{\"pin\":" + String(pin) + ",\"value\":" + String(value ? "true" : "false") + "}";
    ackCommand(commandId, "succeeded", result, "");
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
  Serial.println("LIGHT_GATEWAY_ESP32_WIFI_AGENT");
  Serial.println("DEVICE_ID " + config.deviceId);
  if (!connectWiFi()) {
    startPortal();
    return;
  }
  registerDevice();
  heartbeat();
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
  if (now - lastHeartbeatAt > 10000) {
    lastHeartbeatAt = now;
    heartbeat();
  }
  if (now - lastTelemetryAt > 30000) {
    lastTelemetryAt = now;
    telemetry("wifi.rssi", String(WiFi.RSSI()), "dBm", false);
    telemetry("system.heap", String(ESP.getFreeHeap()), "bytes", false);
  }
  if (now - lastCommandPollAt > 3000) {
    lastCommandPollAt = now;
    pollCommand();
  }
  delay(50);
}
