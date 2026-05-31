// Light Gateway — ESP32 GPS tracker firmware (category=gps).
//
// Reads NMEA from a UART GPS module (e.g. NEO-6M) on Serial2, parses position
// from $..RMC sentences, and reports gps.fix telemetry at a configurable
// interval. Handles commands:
//   gps.interval  {"seconds": 1..3600}      -> change reporting frequency
//   geofence.set  {"center":[lat,lng], "radius_m": 200}  -> stored locally
//
// gps.fix telemetry value: {"lat":..,"lng":..,"speed":..,"sats":..}
// The gateway performs authoritative geofence enter/exit detection from these
// fixes; the device just reports position.

#include <DNSServer.h>
#include <HTTPClient.h>
#include <Preferences.h>
#include <WebServer.h>
#include <WiFi.h>

// GPS module UART (module TX -> ESP RX pin).
const int GPS_RX_PIN = 16;
const int GPS_TX_PIN = 17;
const long GPS_BAUD = 9600;

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
};

struct Fix {
  bool valid = false;
  double lat = 0;
  double lng = 0;
  double speedMps = 0;
  int sats = 0;
};

Config config;
Fix fix;
int reportIntervalMs = 10000;  // gps.interval default 10s
double fenceLat = 0, fenceLng = 0, fenceRadiusM = 0;
bool fenceSet = false;

unsigned long lastHeartbeatAt = 0;
unsigned long lastReportAt = 0;
unsigned long lastCommandPollAt = 0;
bool portalMode = false;
String nmeaLine;

String chipId() {
  uint64_t mac = ESP.getEfuseMac();
  char value[17];
  snprintf(value, sizeof(value), "%04X%08X", (uint16_t)(mac >> 32), (uint32_t)mac);
  return String(value);
}

String defaultDeviceId() { return "esp32-gps-" + chipId(); }

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
    if (c == ',' || c == '}' || c == ']' || c == '\n' || c == '\r') break;
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
  config.deviceName = prefs.getString("name", "ESP32 GPS Tracker");
  config.token = prefs.getString("token", "");
  reportIntervalMs = prefs.getInt("interval", 10) * 1000;
}

void saveNetwork() {
  prefs.putString("ssid", config.ssid);
  prefs.putString("pass", config.password);
  prefs.putString("gateway", config.gateway);
  prefs.putString("deviceId", config.deviceId);
  prefs.putString("name", config.deviceName);
  prefs.putString("token", config.token);
}

// ---- NMEA parsing ----

// nthField returns the comma-separated field at index i of an NMEA sentence.
String nthField(const String& sentence, int index) {
  int field = 0, start = 0;
  for (int i = 0; i <= (int)sentence.length(); i++) {
    if (i == (int)sentence.length() || sentence[i] == ',') {
      if (field == index) return sentence.substring(start, i);
      field++;
      start = i + 1;
    }
  }
  return "";
}

// ddmmToDecimal converts NMEA ddmm.mmmm / dddmm.mmmm to decimal degrees.
double ddmmToDecimal(const String& raw, const String& hemisphere) {
  if (raw.length() < 3) return 0;
  double value = raw.toDouble();
  int deg = (int)(value / 100);
  double minutes = value - deg * 100;
  double dec = deg + minutes / 60.0;
  if (hemisphere == "S" || hemisphere == "W") dec = -dec;
  return dec;
}

// parseRMC parses $..RMC; updates fix on a valid sentence.
void parseRMC(const String& line) {
  // $GPRMC,time,status,lat,N/S,lng,E/W,speedKnots,course,date,...
  String status = nthField(line, 2);
  if (status != "A") {  // 'A' = valid, 'V' = warning/void
    fix.valid = false;
    return;
  }
  String latRaw = nthField(line, 3), latH = nthField(line, 4);
  String lngRaw = nthField(line, 5), lngH = nthField(line, 6);
  String knots = nthField(line, 7);
  if (latRaw.length() == 0 || lngRaw.length() == 0) {
    fix.valid = false;
    return;
  }
  fix.lat = ddmmToDecimal(latRaw, latH);
  fix.lng = ddmmToDecimal(lngRaw, lngH);
  fix.speedMps = knots.toDouble() * 0.514444;  // knots -> m/s
  fix.valid = true;
}

void parseGGA(const String& line) {
  // $GPGGA,time,lat,N/S,lng,E/W,fixQuality,sats,...
  String sats = nthField(line, 7);
  if (sats.length() > 0) fix.sats = sats.toInt();
}

void readGps() {
  while (Serial2.available() > 0) {
    char c = (char)Serial2.read();
    if (c == '\n' || c == '\r') {
      if (nmeaLine.startsWith("$") && nmeaLine.length() > 6) {
        String type = nmeaLine.substring(3, 6);
        if (type == "RMC") parseRMC(nmeaLine);
        else if (type == "GGA") parseGGA(nmeaLine);
      }
      nmeaLine = "";
    } else {
      nmeaLine += c;
      if (nmeaLine.length() > 120) nmeaLine = "";  // guard against junk
    }
  }
}

// ---- Config portal ----

void sendHTML() {
  int networkCount = WiFi.scanNetworks(false, true);
  String html = "<!doctype html><html><head><meta name='viewport' content='width=device-width,initial-scale=1'>";
  html += "<title>GPS Tracker Setup</title>";
  html += "<style>body{font-family:-apple-system,Segoe UI,sans-serif;margin:24px;max-width:620px;background:#f6f8fb;color:#1c2331}main{background:#fff;border:1px solid #dce3ee;border-radius:10px;padding:18px}label{display:block;margin:12px 0 6px;color:#5f6b7d}input{width:100%;padding:10px;border:1px solid #ccd;border-radius:8px;box-sizing:border-box}button{margin-top:16px;padding:10px 14px;border:0;border-radius:8px;background:#1464f4;color:#fff}</style>";
  html += "</head><body><h1>GPS Tracker Setup</h1><main>";
  html += "<form method='post' action='/save'>";
  html += "<label>Wi-Fi SSID</label><input name='ssid' list='ssid-list' value='" + htmlEscape(config.ssid) + "' autocomplete='off'>";
  html += "<datalist id='ssid-list'>";
  for (int i = 0; i < networkCount; i++) html += "<option value='" + htmlEscape(WiFi.SSID(i)) + "'>";
  html += "</datalist>";
  html += "<label>Wi-Fi Password</label><input name='password' type='password' value=''>";
  html += "<label>Gateway URL</label><input name='gateway' value='" + htmlEscape(config.gateway) + "'>";
  html += "<label>Device ID</label><input name='deviceId' value='" + htmlEscape(config.deviceId) + "'>";
  html += "<label>Device Name</label><input name='deviceName' value='" + htmlEscape(config.deviceName) + "'>";
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
  payload += "\"category\":\"gps\",";
  payload += "\"profile\":\"gps.v1\",";
  payload += "\"model\":\"esp32-neo6m\",";
  payload += "\"fwVersion\":\"esp32-gps/0.1.0\",";
  payload += "\"agentVersion\":\"esp32-gps/0.1.0\",";
  payload += "\"labels\":{\"transport\":\"wifi\",\"chip\":\"esp32\"},";
  payload += "\"metadata\":{\"ip\":\"" + WiFi.localIP().toString() + "\"}";
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
  String payload = "{\"agentVersion\":\"esp32-gps/0.1.0\",\"metadata\":{\"rssi\":" + String(WiFi.RSSI()) + ",\"fix\":" + (fix.valid ? "true" : "false") + "}}";
  String response;
  httpJSON("POST", "/api/v1/devices/" + config.deviceId + "/heartbeat", payload, response);
}

void reportFix() {
  if (!fix.valid) {
    Serial.println("GPS_NO_FIX");
    return;
  }
  char value[128];
  snprintf(value, sizeof(value), "{\"lat\":%.6f,\"lng\":%.6f,\"speed\":%.2f,\"sats\":%d}",
           fix.lat, fix.lng, fix.speedMps, fix.sats);
  String payload = String("{\"key\":\"gps.fix\",\"value\":") + value + "}";
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

void handleCommand(const String& body) {
  String commandId = jsonValue(body, "id");
  if (commandId == "") return;
  String type = jsonValue(body, "type");

  if (type == "gps.interval") {
    int seconds = jsonNumberish(body, "seconds").toInt();
    if (seconds < 1 || seconds > 3600) {
      ackCommand(commandId, "failed", "", "seconds must be 1..3600");
      return;
    }
    reportIntervalMs = seconds * 1000;
    prefs.putInt("interval", seconds);
    ackCommand(commandId, "succeeded", String("{\"seconds\":") + seconds + "}", "");
  } else if (type == "geofence.set") {
    // payload: {"center":[lat,lng],"radius_m":200}
    int arrStart = body.indexOf("\"center\":[");
    if (arrStart >= 0) {
      int p = arrStart + 10;
      int comma = body.indexOf(",", p);
      int close = body.indexOf("]", p);
      if (comma > 0 && close > comma) {
        fenceLat = body.substring(p, comma).toDouble();
        fenceLng = body.substring(comma + 1, close).toDouble();
      }
    }
    fenceRadiusM = jsonNumberish(body, "radius_m").toDouble();
    fenceSet = fenceRadiusM > 0;
    char res[96];
    snprintf(res, sizeof(res), "{\"centerLat\":%.6f,\"centerLng\":%.6f,\"radiusM\":%.1f}", fenceLat, fenceLng, fenceRadiusM);
    ackCommand(commandId, "succeeded", String(res), "");
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
  Serial2.begin(GPS_BAUD, SERIAL_8N1, GPS_RX_PIN, GPS_TX_PIN);
  loadConfig();
  Serial.println("LIGHT_GATEWAY_ESP32_GPS");
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
  readGps();
  unsigned long now = millis();
  if (now - lastReportAt > (unsigned long)reportIntervalMs) {
    lastReportAt = now;
    reportFix();
  }
  if (now - lastHeartbeatAt > 10000) {
    lastHeartbeatAt = now;
    heartbeat();
  }
  if (now - lastCommandPollAt > 2000) {
    lastCommandPollAt = now;
    pollCommand();
  }
  delay(10);
}
