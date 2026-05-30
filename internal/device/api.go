package device

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type API struct {
	store  *Store
	logger *slog.Logger
}

func NewAPI(store *Store, logger *slog.Logger) *API {
	if logger == nil {
		logger = slog.Default()
	}
	return &API{store: store, logger: logger}
}

func (a *API) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", a.health)
	mux.HandleFunc("POST /api/v1/devices/register", a.registerDevice)
	mux.HandleFunc("GET /api/v1/devices", a.listDevices)
	mux.HandleFunc("GET /api/v1/devices/{deviceID}", a.getDevice)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/token/reset", a.resetDeviceToken)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/status", a.updateDeviceStatus)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/heartbeat", a.heartbeat)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/telemetry", a.addTelemetry)
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/telemetry", a.listTelemetry)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/commands", a.createCommand)
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/commands", a.listCommands)
	mux.HandleFunc("GET /api/v1/devices/{deviceID}/commands/next", a.nextCommand)
	mux.HandleFunc("POST /api/v1/devices/{deviceID}/commands/{commandID}/ack", a.ackDeviceCommand)
	mux.HandleFunc("POST /api/v1/commands/{commandID}/ack", a.ackCommand)
	mux.HandleFunc("GET /api/v1/events", a.listEvents)
}

func (a *API) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "light-gateway"})
}

func (a *API) registerDevice(w http.ResponseWriter, r *http.Request) {
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

func (a *API) ackCommand(w http.ResponseWriter, r *http.Request) {
	var req AckCommandRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	cmd, err := a.store.AckCommand(r.PathValue("commandID"), req)
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
