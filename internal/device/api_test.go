package device

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIRegistersDeviceAndReturnsList(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	mux := http.NewServeMux()
	NewAPI(store, nil, nil, nil, nil).RegisterRoutes(mux)

	body := bytes.NewBufferString(`{"id":"android-a","name":"Pixel Lab","type":"android"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/register", body)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status=%d body=%s", rec.Code, rec.Body.String())
	}
	var registration DeviceRegistration
	if err := json.NewDecoder(rec.Body).Decode(&registration); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if registration.Device.ID != "android-a" || registration.Token == "" {
		t.Fatalf("unexpected registration: %+v", registration)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Items []Device `json:"items"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&payload); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(payload.Items) != 1 || payload.Items[0].ID != "android-a" {
		t.Fatalf("unexpected devices: %+v", payload.Items)
	}
}

func TestAPIRequiresDeviceTokenForDeviceEndpoints(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	registration, err := store.Register(RegisterDeviceRequest{ID: "esp-a", Name: "ESP A", Type: DeviceTypeESP})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	mux := http.NewServeMux()
	NewAPI(store, nil, nil, nil, nil).RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/devices/esp-a/telemetry", bytes.NewBufferString(`{"key":"temperature","value":24}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized without token, got status=%d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/devices/esp-a/telemetry", bytes.NewBufferString(`{"key":"temperature","value":24}`))
	req.Header.Set("X-Device-Token", registration.Token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected telemetry accepted, got status=%d body=%s", rec.Code, rec.Body.String())
	}
}
