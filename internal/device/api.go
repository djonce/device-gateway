package device

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"light-gateway/internal/auth"
	"light-gateway/internal/realtime"
	"light-gateway/internal/rules"
	"light-gateway/internal/weather"
)

type API struct {
	store        *Store
	logger       *slog.Logger
	weather      *weather.Service
	realtime     *realtime.Hub
	auth         *auth.Authenticator
	provisionKey string
}

// SetProvisionKey requires a matching X-Provision-Key header (or a valid admin
// session) on device registration. Empty key = open registration.
func (a *API) SetProvisionKey(key string) { a.provisionKey = strings.TrimSpace(key) }

func NewAPI(store *Store, logger *slog.Logger, weatherSvc *weather.Service, hub *realtime.Hub, authn *auth.Authenticator) *API {
	if logger == nil {
		logger = slog.Default()
	}
	return &API{store: store, logger: logger, weather: weatherSvc, realtime: hub, auth: authn}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	// Public: health, metrics, auth, device bootstrap, and read-only content.
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("GET /metrics", a.metrics)
	mux.HandleFunc("GET /api/v1/auth/status", a.authStatus)
	mux.HandleFunc("POST /api/v1/auth/login", a.login)
	mux.HandleFunc("POST /api/v1/auth/logout", a.admin(a.logout))
	mux.HandleFunc("GET /api/v1/content/clock", a.clockContent)
	mux.HandleFunc("POST /api/v1/devices/register", a.registerDevice)

	// Device-authenticated (X-Device-Token): the device's own data plane.
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/heartbeat", a.heartbeat)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/telemetry", a.addTelemetry)
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/commands/next", a.nextCommand)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/commands/{commandID}/ack", a.ackDeviceCommand)
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/ws", a.realtimeWS)
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/ota", a.otaStatus)
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/firmware/{firmwareID}/download", a.downloadFirmware)

	// Admin-authenticated: the operator/console control plane.
	mux.HandleFunc("GET /api/v1/profiles", a.admin(a.listProfiles))
	mux.HandleFunc("GET /api/v1/devices", a.admin(a.listDevices))
	mux.HandleFunc("GET /api/v1/devices/{deviceID}", a.admin(a.getDevice))
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/token/reset", a.admin(a.resetDeviceToken))
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/status", a.admin(a.updateDeviceStatus))
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/telemetry", a.admin(a.listTelemetry))
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/telemetry/series", a.admin(a.telemetrySeries))
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/telemetry/history", a.admin(a.telemetryHistory))
	mux.HandleFunc("GET /api/v1/stats", a.admin(a.stats))
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/geofence", a.admin(a.setGeofence))
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/track", a.admin(a.listTrack))
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/commands", a.admin(a.createCommand))
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/commands", a.admin(a.listCommands))
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/realtime", a.admin(a.realtimeStatus))
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/realtime/say", a.admin(a.realtimeSay))
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/ota/target", a.admin(a.setOtaTarget))
	mux.HandleFunc("GET /api/v1/events", a.admin(a.listEvents))
	mux.HandleFunc("POST /api/v1/firmware", a.admin(a.uploadFirmware))
	mux.HandleFunc("GET /api/v1/firmware", a.admin(a.listFirmware))
	mux.HandleFunc("POST /api/v1/firmware/{firmwareID}/rollout", a.admin(a.rolloutFirmware))
	mux.HandleFunc("GET /api/v1/rules", a.admin(a.listRules))
	mux.HandleFunc("POST /api/v1/rules", a.admin(a.createRule))
	mux.HandleFunc("POST /api/v1/rules/{ruleID}/enable", a.admin(a.setRuleEnabled))
	mux.HandleFunc("DELETE /api/v1/rules/{ruleID}", a.admin(a.deleteRule))
}

// admin wraps an operator endpoint with admin-session auth. In open mode (no
// admin password configured) it passes through.
func (a *API) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.auth == nil || !a.auth.Enabled() {
			h(w, r)
			return
		}
		if !a.auth.Validate(a.bearerToken(r)) {
			writeError(w, http.StatusUnauthorized, "admin authentication required")
			return
		}
		h(w, r)
	}
}

func (a *API) bearerToken(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[len("bearer "):])
	}
	return ""
}

func (a *API) authStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"authRequired": a.auth != nil && a.auth.Enabled()})
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if a.auth == nil || !a.auth.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"authRequired": false})
		return
	}
	token, expiresAt, ok := a.auth.Login(req.Username, req.Password)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expiresAt": expiresAt})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if a.auth != nil {
		a.auth.Logout(a.bearerToken(r))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "light-gateway"})
}

func (a *API) listProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": Profiles()})
}

func (a *API) realtimeConnections() int {
	if a.realtime == nil {
		return 0
	}
	return a.realtime.Count()
}

// metrics serves Prometheus text exposition (public, for scrapers).
func (a *API) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(a.store.Stats().PrometheusMetrics(a.realtimeConnections())))
}

// stats serves the fleet snapshot as JSON for the console dashboard.
func (a *API) stats(w http.ResponseWriter, r *http.Request) {
	st := a.store.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"devicesByState":      st.DevicesByState,
		"devicesByCategory":   st.DevicesByCategory,
		"total":               st.Total,
		"disabled":            st.Disabled,
		"telemetryStored":     st.TelemetryStored,
		"firmware":            st.Firmware,
		"registrations":       st.Registrations,
		"telemetryReceived":   st.TelemetryReceived,
		"commandsCreated":     st.CommandsCreated,
		"commandAcks":         st.CommandAcks,
		"events":              st.Events,
		"realtimeConnections": a.realtimeConnections(),
	})
}

func (a *API) listRules(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": a.store.ListRules()})
}

func (a *API) createRule(w http.ResponseWriter, r *http.Request) {
	var rule rules.Rule
	if !decodeJSON(w, r, &rule) {
		return
	}
	created, err := a.store.AddRule(rule)
	respond(w, created, err, http.StatusCreated)
}

func (a *API) setRuleEnabled(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	rule, err := a.store.SetRuleEnabled(r.PathValue("ruleID"), body.Enabled)
	respond(w, rule, err, http.StatusOK)
}

func (a *API) deleteRule(w http.ResponseWriter, r *http.Request) {
	if err := a.store.DeleteRule(r.PathValue("ruleID")); err != nil {
		respond(w, nil, err, http.StatusOK)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *API) telemetrySeries(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query param is required")
		return
	}
	bucket := parseQueryInt(r, "bucket", 60)
	limit := parseQueryInt(r, "limit", 120)
	series, err := a.store.TelemetrySeries(r.PathValue("deviceID"), key, bucket, limit)
	respond(w, map[string]any{"items": series}, err, http.StatusOK)
}

// telemetryHistory serves long-term rolled-up history for a key over a time
// window (?from=&to= unix seconds; default last 24h). Resolution is chosen
// from the window width.
func (a *API) telemetryHistory(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.URL.Query().Get("key"))
	if key == "" {
		writeError(w, http.StatusBadRequest, "key query param is required")
		return
	}
	now := time.Now().Unix()
	to := int64(parseQueryInt(r, "to", int(now)))
	from := int64(parseQueryInt(r, "from", int(to-24*3600)))
	resolution, series, err := a.store.TelemetryHistory(r.PathValue("deviceID"), key, time.Unix(from, 0), time.Unix(to, 0))
	respond(w, map[string]any{"resolution": resolution, "items": series}, err, http.StatusOK)
}

// clockContent returns current time + weather for a location. Routine content
// for clock-category devices, which poll it instead of holding a weather API
// key. Read-only public data, so no device token is required.
func (a *API) clockContent(w http.ResponseWriter, r *http.Request) {
	if a.weather == nil {
		writeError(w, http.StatusServiceUnavailable, "weather service not configured")
		return
	}
	lat, latErr := strconv.ParseFloat(strings.TrimSpace(r.URL.Query().Get("lat")), 64)
	lon, lonErr := strconv.ParseFloat(strings.TrimSpace(r.URL.Query().Get("lon")), 64)
	if latErr != nil || lonErr != nil {
		writeError(w, http.StatusBadRequest, "lat and lon query params are required")
		return
	}
	tz := strings.TrimSpace(r.URL.Query().Get("tz"))
	writeJSON(w, http.StatusOK, a.weather.ClockContent(lat, lon, tz))
}

// allowRegister gates device self-registration. When a provisioning key is
// configured, the caller must present it (X-Provision-Key) or hold a valid
// admin session (so the console/admin can still register devices).
func (a *API) allowRegister(r *http.Request) bool {
	if a.provisionKey == "" {
		return true
	}
	key := strings.TrimSpace(r.Header.Get("X-Provision-Key"))
	if key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(a.provisionKey)) == 1 {
		return true
	}
	return a.auth != nil && a.auth.Enabled() && a.auth.Validate(a.bearerToken(r))
}

func (a *API) registerDevice(w http.ResponseWriter, r *http.Request) {
	if !a.allowRegister(r) {
		writeError(w, http.StatusUnauthorized, "a valid provisioning key or admin session is required to register")
		return
	}
	var req RegisterDeviceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	device, err := a.store.Register(req)
	respond(w, device, err, http.StatusCreated)
}

func (a *API) listDevices(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": a.store.ListDevices()})
}

func (a *API) getDevice(w http.ResponseWriter, r *http.Request) {
	device, err := a.store.GetDevice(r.PathValue("deviceID"))
	respond(w, device, err, http.StatusOK)
}

func (a *API) heartbeat(w http.ResponseWriter, r *http.Request) {
	if !a.requireDeviceAuth(w, r, r.PathValue("deviceID")) {
		return
	}
	var req HeartbeatRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	device, err := a.store.Heartbeat(r.PathValue("deviceID"), req)
	respond(w, device, err, http.StatusOK)
}

func (a *API) addTelemetry(w http.ResponseWriter, r *http.Request) {
	if !a.requireDeviceAuth(w, r, r.PathValue("deviceID")) {
		return
	}
	var req TelemetryRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	point, err := a.store.AddTelemetry(r.PathValue("deviceID"), req)
	respond(w, point, err, http.StatusCreated)
}

func (a *API) listTelemetry(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 50)
	points, err := a.store.ListTelemetry(r.PathValue("deviceID"), limit)
	respond(w, map[string]any{"items": points}, err, http.StatusOK)
}

// setGeofence stores a circular geofence server-side (for enter/exit detection)
// and also pushes a geofence.set command so the device knows its bounds.
func (a *API) setGeofence(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceID")
	var req SetGeofenceRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	device, err := a.store.SetGeofence(deviceID, req)
	if err != nil {
		respond(w, nil, err, http.StatusOK)
		return
	}
	if req.RadiusM > 0 {
		if _, cmdErr := a.store.CreateCommand(deviceID, CreateCommandRequest{
			Type:        "geofence.set",
			Payload:     map[string]any{"center": []float64{req.CenterLat, req.CenterLng}, "radius_m": req.RadiusM},
			RequestedBy: "console",
		}); cmdErr != nil {
			a.logger.Warn("push geofence.set command failed", "error", cmdErr, "deviceId", deviceID)
		}
	}
	writeJSON(w, http.StatusOK, device)
}

func (a *API) listTrack(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 200)
	points, err := a.store.ListTrack(r.PathValue("deviceID"), limit)
	respond(w, map[string]any{"items": points}, err, http.StatusOK)
}

// realtimeWS authenticates the device, upgrades to WebSocket, and serves the
// real-time channel until it closes. Token may come from the X-Device-Token /
// Authorization header or a ?token= query param (for browser test clients).
func (a *API) realtimeWS(w http.ResponseWriter, r *http.Request) {
	if a.realtime == nil {
		writeError(w, http.StatusServiceUnavailable, "realtime channel not configured")
		return
	}
	deviceID := r.PathValue("deviceID")
	token := strings.TrimSpace(r.Header.Get("X-Device-Token"))
	if token == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			token = strings.TrimSpace(auth[len("bearer "):])
		}
	}
	if token == "" {
		token = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	if err := a.store.VerifyDeviceToken(deviceID, token); err != nil {
		respond(w, nil, err, http.StatusOK)
		return
	}
	conn, err := realtime.Upgrade(w, r)
	if err != nil {
		a.logger.Warn("websocket upgrade failed", "error", err, "deviceId", deviceID)
		return
	}
	a.realtime.Serve(conn, deviceID) // blocks until the connection closes
}

func (a *API) realtimeStatus(w http.ResponseWriter, r *http.Request) {
	connected := false
	count := 0
	if a.realtime != nil {
		connected = a.realtime.Connected(r.PathValue("deviceID"))
		count = a.realtime.Count()
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": connected, "connections": count})
}

// realtimeSay pushes a tts.say to a connected device (console "make it speak").
func (a *API) realtimeSay(w http.ResponseWriter, r *http.Request) {
	if a.realtime == nil {
		writeError(w, http.StatusServiceUnavailable, "realtime channel not configured")
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	if !a.realtime.SayText(r.PathValue("deviceID"), req.Text) {
		writeError(w, http.StatusConflict, "device is not connected to the realtime channel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// uploadFirmware (admin) stores a firmware binary. Metadata comes from query
// params; the request body is the raw binary.
func (a *API) uploadFirmware(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 16<<20)) // 16 MiB cap
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read firmware body")
		return
	}
	q := r.URL.Query()
	req := AddFirmwareRequest{
		Category: DeviceCategory(strings.TrimSpace(q.Get("category"))),
		Model:    strings.TrimSpace(q.Get("model")),
		Version:  strings.TrimSpace(q.Get("version")),
		Notes:    strings.TrimSpace(q.Get("notes")),
	}
	fw, err := a.store.AddFirmware(req, data)
	respond(w, fw, err, http.StatusCreated)
}

func (a *API) listFirmware(w http.ResponseWriter, r *http.Request) {
	cat := DeviceCategory(strings.TrimSpace(r.URL.Query().Get("category")))
	writeJSON(w, http.StatusOK, map[string]any{"items": a.store.ListFirmware(cat)})
}

func (a *API) rolloutFirmware(w http.ResponseWriter, r *http.Request) {
	affected, err := a.store.RolloutFirmware(r.PathValue("firmwareID"))
	respond(w, map[string]any{"devices": affected}, err, http.StatusOK)
}

func (a *API) setOtaTarget(w http.ResponseWriter, r *http.Request) {
	var req SetTargetRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	device, err := a.store.SetDeviceTarget(r.PathValue("deviceID"), req.Version)
	respond(w, device, err, http.StatusOK)
}

// otaStatus (device token) tells a device whether an update is available.
func (a *API) otaStatus(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceID")
	if !a.requireDeviceAuth(w, r, deviceID) {
		return
	}
	status, err := a.store.ResolveOTA(deviceID)
	respond(w, status, err, http.StatusOK)
}

// downloadFirmware (device token) streams a firmware binary to the device.
func (a *API) downloadFirmware(w http.ResponseWriter, r *http.Request) {
	if !a.requireDeviceAuth(w, r, r.PathValue("deviceID")) {
		return
	}
	blob, err := a.store.FirmwareBlob(r.PathValue("firmwareID"))
	if err != nil {
		respond(w, nil, err, http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(blob)
}

func (a *API) createCommand(w http.ResponseWriter, r *http.Request) {
	var req CreateCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cmd, err := a.store.CreateCommand(r.PathValue("deviceID"), req)
	respond(w, cmd, err, http.StatusCreated)
}

func (a *API) resetDeviceToken(w http.ResponseWriter, r *http.Request) {
	reset, err := a.store.ResetDeviceToken(r.PathValue("deviceID"))
	respond(w, reset, err, http.StatusOK)
}

func (a *API) updateDeviceStatus(w http.ResponseWriter, r *http.Request) {
	var req UpdateDeviceStatusRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	device, err := a.store.UpdateDeviceStatus(r.PathValue("deviceID"), req)
	respond(w, device, err, http.StatusOK)
}

func (a *API) listCommands(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 50)
	commands, err := a.store.ListCommands(r.PathValue("deviceID"), limit)
	respond(w, map[string]any{"items": commands}, err, http.StatusOK)
}

func (a *API) nextCommand(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceID")
	if !a.requireDeviceAuth(w, r, deviceID) {
		return
	}
	timeout := parseTimeout(r, 0)
	deadline := time.Now().Add(timeout)
	for {
		cmd, ok, err := a.store.NextCommand(deviceID)
		if err != nil {
			respond(w, nil, err, http.StatusOK)
			return
		}
		if ok {
			writeJSON(w, http.StatusOK, map[string]any{"command": cmd})
			return
		}
		if timeout == 0 || time.Now().After(deadline) {
			writeJSON(w, http.StatusOK, map[string]any{"command": nil})
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (a *API) ackDeviceCommand(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("deviceID")
	if !a.requireDeviceAuth(w, r, deviceID) {
		return
	}
	var req AckCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cmd, err := a.store.AckCommandForDevice(deviceID, r.PathValue("commandID"), req)
	respond(w, cmd, err, http.StatusOK)
}

func (a *API) listEvents(w http.ResponseWriter, r *http.Request) {
	limit := parseLimit(r, 100)
	writeJSON(w, http.StatusOK, map[string]any{"items": a.store.ListEvents(limit)})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return false
	}
	return true
}

func respond(w http.ResponseWriter, data any, err error, successStatus int) {
	if err == nil {
		writeJSON(w, successStatus, data)
		return
	}
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, "resource not found")
	case errors.Is(err, ErrBadRequest):
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), ErrBadRequest.Error()+": "))
	case errors.Is(err, ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "device token is required or invalid")
	case errors.Is(err, ErrForbidden):
		writeError(w, http.StatusForbidden, "device is disabled")
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func (a *API) requireDeviceAuth(w http.ResponseWriter, r *http.Request, deviceID string) bool {
	token := strings.TrimSpace(r.Header.Get("X-Device-Token"))
	if token == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			token = strings.TrimSpace(auth[len("bearer "):])
		}
	}
	if err := a.store.VerifyDeviceToken(deviceID, token); err != nil {
		respond(w, nil, err, http.StatusOK)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func parseQueryInt(r *http.Request, name string, fallback int) int {
	value := r.URL.Query().Get(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func parseLimit(r *http.Request, fallback int) int {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return fallback
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 0 {
		return fallback
	}
	return limit
}

func parseTimeout(r *http.Request, fallback time.Duration) time.Duration {
	value := r.URL.Query().Get("timeout")
	if value == "" {
		return fallback
	}
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 || seconds > 60 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
