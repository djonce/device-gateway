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

	acked, err := store.AckCommandForDevice("esp-livingroom-001", command.ID, AckCommandRequest{Status: CommandStatusSucceeded, Result: map[string]any{"message": "ok"}})
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

func TestProfileEnforcesCommandAllowlist(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	// Registering with a known category binds the default profile.
	reg, err := store.Register(RegisterDeviceRequest{
		ID: "esp-light-1", Name: "Night Light", Type: DeviceTypeESP, Category: CategoryLight,
	})
	if err != nil {
		t.Fatalf("register light: %v", err)
	}
	if reg.Device.Profile != "light.v1" {
		t.Fatalf("expected default profile light.v1, got %q", reg.Device.Profile)
	}

	// A command in the profile is accepted.
	if _, err := store.CreateCommand("esp-light-1", CreateCommandRequest{
		Type: "light.power", Payload: map[string]any{"on": true},
	}); err != nil {
		t.Fatalf("expected light.power accepted: %v", err)
	}

	// A command outside the profile is rejected.
	if _, err := store.CreateCommand("esp-light-1", CreateCommandRequest{Type: "gpio.write"}); !errors.Is(err, ErrBadRequest) {
		t.Fatalf("expected gpio.write rejected by profile, got %v", err)
	}

	// Legacy device with no category/profile accepts any command type.
	if _, err := store.Register(RegisterDeviceRequest{ID: "legacy-1", Name: "Legacy", Type: DeviceTypeESP}); err != nil {
		t.Fatalf("register legacy: %v", err)
	}
	if _, err := store.CreateCommand("legacy-1", CreateCommandRequest{Type: "gpio.write"}); err != nil {
		t.Fatalf("expected legacy device to accept any command: %v", err)
	}
}

func TestTelemetryRollingPruneOnSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	base := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	calls := 0
	// Advance time on every call so ids/timestamps are unique and monotonic.
	store.now = func() time.Time {
		calls++
		return base.Add(time.Duration(calls) * time.Millisecond)
	}
	if _, err := store.Register(RegisterDeviceRequest{ID: "gps-1", Name: "GPS 1", Type: DeviceTypeESP, Category: CategoryGPS}); err != nil {
		t.Fatalf("register: %v", err)
	}

	total := telemetryPerDevice + 25
	for i := 0; i < total; i++ {
		if _, err := store.AddTelemetry("gps-1", TelemetryRequest{Key: "gps.fix", Value: float64(i)}); err != nil {
			t.Fatalf("telemetry %d: %v", i, err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopen: the DB must have been pruned to the cap, not grown unbounded.
	reopened, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	points, err := reopened.ListTelemetry("gps-1", 0)
	if err != nil {
		t.Fatalf("list telemetry: %v", err)
	}
	if len(points) != telemetryPerDevice {
		t.Fatalf("expected %d telemetry rows after prune, got %d", telemetryPerDevice, len(points))
	}
	// Newest-first ordering; the last value written must survive the prune.
	if points[0].Value != float64(total-1) {
		t.Fatalf("expected newest value %v to survive, got %v", float64(total-1), points[0].Value)
	}
}

func countEvents(s *Store, eventType string) int {
	count := 0
	for _, e := range s.ListEvents(0) {
		if e.Type == eventType {
			count++
		}
	}
	return count
}

func TestHaversineMeters(t *testing.T) {
	// One degree of latitude is ~111 km.
	d := haversineMeters(31.0, 121.0, 32.0, 121.0)
	if d < 110000 || d > 112000 {
		t.Fatalf("expected ~111km for 1° latitude, got %.0fm", d)
	}
	if same := haversineMeters(31.2, 121.4, 31.2, 121.4); same > 1 {
		t.Fatalf("expected ~0 for identical points, got %.3fm", same)
	}
}

func TestGeofenceEnterExitEvents(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	n := 0
	store.now = func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) }
	if _, err := store.Register(RegisterDeviceRequest{ID: "gps-1", Name: "Tracker", Type: DeviceTypeESP, Category: CategoryGPS}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := store.SetGeofence("gps-1", SetGeofenceRequest{CenterLat: 31.2304, CenterLng: 121.4737, RadiusM: 500}); err != nil {
		t.Fatalf("set geofence: %v", err)
	}

	// Inside the 500m circle.
	if _, err := store.AddTelemetry("gps-1", TelemetryRequest{Key: "gps.fix", Value: map[string]any{"lat": 31.2305, "lng": 121.4738}}); err != nil {
		t.Fatalf("telemetry inside: %v", err)
	}
	if dev, _ := store.GetDevice("gps-1"); dev.GeofenceState != "inside" {
		t.Fatalf("expected inside, got %q", dev.GeofenceState)
	}

	// ~22km north -> outside.
	if _, err := store.AddTelemetry("gps-1", TelemetryRequest{Key: "gps.fix", Value: map[string]any{"lat": 31.4304, "lng": 121.4737}}); err != nil {
		t.Fatalf("telemetry outside: %v", err)
	}
	if dev, _ := store.GetDevice("gps-1"); dev.GeofenceState != "outside" {
		t.Fatalf("expected outside, got %q", dev.GeofenceState)
	}

	// Still outside -> no duplicate exit event.
	if _, err := store.AddTelemetry("gps-1", TelemetryRequest{Key: "gps.fix", Value: map[string]any{"lat": 31.4305, "lng": 121.4738}}); err != nil {
		t.Fatalf("telemetry outside again: %v", err)
	}
	if enter, exit := countEvents(store, "geofence.enter"), countEvents(store, "geofence.exit"); enter != 1 || exit != 1 {
		t.Fatalf("expected 1 enter / 1 exit, got %d / %d", enter, exit)
	}
}

func TestListTrackReturnsGpsFixesChronologically(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	n := 0
	store.now = func() time.Time { n++; return base.Add(time.Duration(n) * time.Second) }
	if _, err := store.Register(RegisterDeviceRequest{ID: "gps-1", Name: "Tracker", Type: DeviceTypeESP, Category: CategoryGPS}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := store.AddTelemetry("gps-1", TelemetryRequest{Key: "gps.fix", Value: map[string]any{"lat": 31.1, "lng": 121.1, "speed": 2.5}}); err != nil {
		t.Fatalf("telemetry 1: %v", err)
	}
	if _, err := store.AddTelemetry("gps-1", TelemetryRequest{Key: "battery", Value: 80}); err != nil {
		t.Fatalf("telemetry battery: %v", err)
	}
	if _, err := store.AddTelemetry("gps-1", TelemetryRequest{Key: "gps.fix", Value: map[string]any{"lat": 31.2, "lng": 121.2}}); err != nil {
		t.Fatalf("telemetry 2: %v", err)
	}

	track, err := store.ListTrack("gps-1", 0)
	if err != nil {
		t.Fatalf("list track: %v", err)
	}
	if len(track) != 2 {
		t.Fatalf("expected 2 gps.fix points (battery ignored), got %d", len(track))
	}
	if track[0].Lat != 31.1 || track[1].Lat != 31.2 {
		t.Fatalf("expected chronological order, got %+v", track)
	}
	if track[0].Speed != 2.5 {
		t.Fatalf("expected speed 2.5 parsed, got %v", track[0].Speed)
	}
}

func TestFirmwareAndOTAResolution(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	n := 0
	store.now = func() time.Time { n++; return now.Add(time.Duration(n) * time.Second) }

	if _, err := store.Register(RegisterDeviceRequest{
		ID: "esp-light-1", Name: "Lamp", Type: DeviceTypeESP, Category: CategoryLight, FwVersion: "1.0.0",
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	// No target yet -> no update.
	if status, _ := store.ResolveOTA("esp-light-1"); status.UpdateAvailable {
		t.Fatalf("expected no update without target")
	}

	bin := []byte("FIRMWARE-BINARY-v1.1.0")
	fw, err := store.AddFirmware(AddFirmwareRequest{Category: CategoryLight, Version: "1.1.0"}, bin)
	if err != nil {
		t.Fatalf("add firmware: %v", err)
	}
	if fw.Size != int64(len(bin)) || fw.SHA256 == "" {
		t.Fatalf("unexpected firmware meta: %+v", fw)
	}
	if list := store.ListFirmware(CategoryLight); len(list) != 1 {
		t.Fatalf("expected 1 firmware, got %d", len(list))
	}
	if list := store.ListFirmware(CategoryGPS); len(list) != 0 {
		t.Fatalf("expected 0 firmware for gps, got %d", len(list))
	}

	// Target the new version -> update available, pointing at the firmware.
	if _, err := store.SetDeviceTarget("esp-light-1", "1.1.0"); err != nil {
		t.Fatalf("set target: %v", err)
	}
	status, err := store.ResolveOTA("esp-light-1")
	if err != nil {
		t.Fatalf("resolve ota: %v", err)
	}
	if !status.UpdateAvailable || status.FirmwareID != fw.ID || status.Version != "1.1.0" {
		t.Fatalf("unexpected ota status: %+v", status)
	}
	if status.DownloadURL != "/api/v1/devices/esp-light-1/firmware/"+fw.ID+"/download" {
		t.Fatalf("unexpected download url: %q", status.DownloadURL)
	}

	// Blob round-trips.
	blob, err := store.FirmwareBlob(fw.ID)
	if err != nil || string(blob) != string(bin) {
		t.Fatalf("firmware blob mismatch: %v / %q", err, blob)
	}

	// After the device updates and re-registers reporting the new version, the
	// target is preserved (server-managed) and current==target -> no update.
	if _, err := store.Register(RegisterDeviceRequest{
		ID: "esp-light-1", Name: "Lamp", Type: DeviceTypeESP, Category: CategoryLight, FwVersion: "1.1.0",
	}); err != nil {
		t.Fatalf("re-register: %v", err)
	}
	if d, _ := store.GetDevice("esp-light-1"); d.TargetFwVersion != "1.1.0" {
		t.Fatalf("target should persist across re-registration, got %q", d.TargetFwVersion)
	}
	if status, _ := store.ResolveOTA("esp-light-1"); status.UpdateAvailable {
		t.Fatalf("expected no update when current==target, got %+v", status)
	}
}

func TestRolloutFirmwareTargetsCategory(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	n := 0
	store.now = func() time.Time { n++; return now.Add(time.Duration(n) * time.Second) }

	store.Register(RegisterDeviceRequest{ID: "l1", Name: "L1", Type: DeviceTypeESP, Category: CategoryLight, FwVersion: "1.0.0"})
	store.Register(RegisterDeviceRequest{ID: "l2", Name: "L2", Type: DeviceTypeESP, Category: CategoryLight, FwVersion: "1.0.0"})
	store.Register(RegisterDeviceRequest{ID: "g1", Name: "G1", Type: DeviceTypeESP, Category: CategoryGPS, FwVersion: "1.0.0"})

	fw, err := store.AddFirmware(AddFirmwareRequest{Category: CategoryLight, Version: "2.0.0"}, []byte("bin"))
	if err != nil {
		t.Fatalf("add firmware: %v", err)
	}
	affected, err := store.RolloutFirmware(fw.ID)
	if err != nil {
		t.Fatalf("rollout: %v", err)
	}
	if affected != 2 {
		t.Fatalf("expected 2 light devices targeted, got %d", affected)
	}
	if d, _ := store.GetDevice("l1"); d.TargetFwVersion != "2.0.0" {
		t.Fatalf("l1 target not set: %q", d.TargetFwVersion)
	}
	if d, _ := store.GetDevice("g1"); d.TargetFwVersion != "" {
		t.Fatalf("gps device should be untouched, got %q", d.TargetFwVersion)
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
