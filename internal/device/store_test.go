package device

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRegisterHeartbeatTelemetryAndCommandLifecycle(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	registered, err := store.Register(RegisterDeviceRequest{
		ID:   "esp-livingroom-001",
		Name: "Living Room ESP",
		Type: DeviceTypeESP,
		Capabilities: []Capability{
			{Name: "sensor.read"},
		},
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if registered.Token == "" {
		t.Fatalf("expected first registration to return a device token")
	}
	if registered.Device.State != OnlineStateOnline {
		t.Fatalf("expected online device, got %q", registered.Device.State)
	}
	if err := store.VerifyDeviceToken("esp-livingroom-001", registered.Token); err != nil {
		t.Fatalf("verify token: %v", err)
	}

	now = now.Add(time.Minute)
	if _, err := store.Heartbeat("esp-livingroom-001", HeartbeatRequest{AgentVersion: "0.1.1"}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	point, err := store.AddTelemetry("esp-livingroom-001", TelemetryRequest{Key: "temperature", Value: 24.5, Unit: "celsius"})
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	if point.Key != "temperature" {
		t.Fatalf("unexpected telemetry key %q", point.Key)
	}

	command, err := store.CreateCommand("esp-livingroom-001", CreateCommandRequest{
		Type:       "gpio.write",
		Payload:    map[string]any{"pin": float64(2), "value": true},
		TTLSeconds: 60,
	})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}
	if command.Status != CommandStatusQueued {
		t.Fatalf("expected queued command, got %q", command.Status)
	}

	next, ok, err := store.NextCommand("esp-livingroom-001")
	if err != nil {
		t.Fatalf("next command: %v", err)
	}
	if !ok || next.ID != command.ID {
		t.Fatalf("expected next command %q, got %q ok=%v", command.ID, next.ID, ok)
	}
	if next.Status != CommandStatusDelivered {
		t.Fatalf("expected delivered command, got %q", next.Status)
	}

	acked, err := store.AckCommand(command.ID, AckCommandRequest{Status: CommandStatusSucceeded, Result: map[string]any{"message": "ok"}})
	if err != nil {
		t.Fatalf("ack command: %v", err)
	}
	if acked.Status != CommandStatusSucceeded {
		t.Fatalf("expected succeeded command, got %q", acked.Status)
	}
}

func TestVerifyDeviceTokenRejectsInvalidAndDisabledDevice(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	registered, err := store.Register(RegisterDeviceRequest{ID: "android-1", Name: "Android 1", Type: DeviceTypeAndroid})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := store.VerifyDeviceToken("android-1", "bad-token"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if err := store.VerifyDeviceToken("android-1", registered.Token); err != nil {
		t.Fatalf("expected token to verify: %v", err)
	}
	if _, err := store.UpdateDeviceStatus("android-1", UpdateDeviceStatusRequest{Disabled: true}); err != nil {
		t.Fatalf("disable device: %v", err)
	}
	if err := store.VerifyDeviceToken("android-1", registered.Token); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected forbidden for disabled device, got %v", err)
	}
}

func TestDeviceStateTransitions(t *testing.T) {
	base := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)

	if got := stateFor(base.Add(90*time.Second), base); got != OnlineStateOnline {
		t.Fatalf("expected online, got %q", got)
	}
	if got := stateFor(base.Add(5*time.Minute), base); got != OnlineStateStale {
		t.Fatalf("expected stale, got %q", got)
	}
	if got := stateFor(base.Add(11*time.Minute), base); got != OnlineStateOffline {
		t.Fatalf("expected offline, got %q", got)
	}
}

func TestCommandExpires(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	if _, err := store.Register(RegisterDeviceRequest{ID: "node-1", Name: "Node 1", Type: DeviceTypeLinuxNode}); err != nil {
		t.Fatalf("register: %v", err)
	}
	cmd, err := store.CreateCommand("node-1", CreateCommandRequest{Type: "shell.exec", TTLSeconds: 1})
	if err != nil {
		t.Fatalf("create command: %v", err)
	}

	now = now.Add(2 * time.Second)
	next, ok, err := store.NextCommand("node-1")
	if err != nil {
		t.Fatalf("next command: %v", err)
	}
	if ok {
		t.Fatalf("expected no command, got %+v", next)
	}
	commands, err := store.ListCommands("node-1", 10)
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(commands) != 1 || commands[0].ID != cmd.ID || commands[0].Status != CommandStatusExpired {
		t.Fatalf("expected expired command, got %+v", commands)
	}
}

func TestSQLitePersistenceRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	registered, err := store.Register(RegisterDeviceRequest{ID: "opi-1", Name: "Orange Pi", Type: DeviceTypeOrangePi})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := store.AddTelemetry("opi-1", TelemetryRequest{Key: "load", Value: 0.42}); err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen sqlite store: %v", err)
	}
	defer reopened.Close()
	device, err := reopened.GetDevice("opi-1")
	if err != nil {
		t.Fatalf("get device: %v", err)
	}
	if device.Name != "Orange Pi" || device.TokenIssuedAt == nil {
		t.Fatalf("unexpected device after reopen: %+v", device)
	}
	if err := reopened.VerifyDeviceToken("opi-1", registered.Token); err != nil {
		t.Fatalf("verify persisted token: %v", err)
	}
	points, err := reopened.ListTelemetry("opi-1", 10)
	if err != nil {
		t.Fatalf("list telemetry: %v", err)
	}
	if len(points) != 1 || points[0].Key != "load" {
		t.Fatalf("unexpected telemetry after reopen: %+v", points)
	}
}
