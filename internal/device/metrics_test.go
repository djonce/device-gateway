package device

import (
	"strings"
	"testing"
	"time"
)

func TestStatsCountsAndCounters(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	cur := base
	store.now = func() time.Time { return cur }

	store.Register(RegisterDeviceRequest{ID: "l1", Name: "Lamp", Type: DeviceTypeESP, Category: CategoryLight})
	cur = base.Add(5 * time.Minute)
	store.Register(RegisterDeviceRequest{ID: "g1", Name: "Tracker", Type: DeviceTypeESP, Category: CategoryGPS})

	// Move to base+11min: l1 (last seen base) -> offline; refresh l1 via heartbeat -> online.
	cur = base.Add(11 * time.Minute)
	if _, err := store.Heartbeat("l1", HeartbeatRequest{}); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, err := store.AddTelemetry("l1", TelemetryRequest{Key: "rssi", Value: float64(-50)}); err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	if _, err := store.CreateCommand("l1", CreateCommandRequest{Type: "light.power", Payload: map[string]any{"on": true}}); err != nil {
		t.Fatalf("command: %v", err)
	}

	st := store.Stats()
	if st.Total != 2 {
		t.Fatalf("expected 2 devices, got %d", st.Total)
	}
	if st.DevicesByState["online"] != 1 || st.DevicesByState["stale"] != 1 || st.DevicesByState["offline"] != 0 {
		t.Fatalf("unexpected state counts: %+v", st.DevicesByState)
	}
	if st.DevicesByCategory["light"] != 1 || st.DevicesByCategory["gps"] != 1 {
		t.Fatalf("unexpected category counts: %+v", st.DevicesByCategory)
	}
	if st.Registrations != 2 || st.TelemetryReceived != 1 || st.CommandsCreated != 1 {
		t.Fatalf("unexpected counters: reg=%d tel=%d cmd=%d", st.Registrations, st.TelemetryReceived, st.CommandsCreated)
	}
	if st.TelemetryStored != 1 {
		t.Fatalf("expected 1 telemetry stored, got %d", st.TelemetryStored)
	}
}

func TestTelemetrySeriesBucketing(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	cur := time.Unix(1700000000, 0).UTC() // bucket boundary at 1699999980 for 60s buckets
	store.now = func() time.Time { return cur }
	store.Register(RegisterDeviceRequest{ID: "d1", Name: "D1", Type: DeviceTypeESP, Category: CategoryGPS})

	add := func(at time.Time, v float64) {
		cur = at
		if _, err := store.AddTelemetry("d1", TelemetryRequest{Key: "rssi", Value: v}); err != nil {
			t.Fatalf("telemetry: %v", err)
		}
	}
	t0 := time.Unix(1700000000, 0).UTC()
	add(t0, 10)
	add(t0.Add(10*time.Second), 20) // same 60s bucket as t0
	add(t0.Add(70*time.Second), 5)  // next bucket
	// non-matching key must be ignored
	cur = t0.Add(80 * time.Second)
	store.AddTelemetry("d1", TelemetryRequest{Key: "other", Value: float64(99)})

	series, err := store.TelemetrySeries("d1", "rssi", 60, 0)
	if err != nil {
		t.Fatalf("series: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("expected 2 buckets, got %d (%+v)", len(series), series)
	}
	b0 := series[0]
	if b0.Count != 2 || b0.Min != 10 || b0.Max != 20 || b0.Avg != 15 || b0.Last != 20 {
		t.Fatalf("unexpected first bucket: %+v", b0)
	}
	b1 := series[1]
	if b1.Count != 1 || b1.Last != 5 {
		t.Fatalf("unexpected second bucket: %+v", b1)
	}
	if !b0.T.Before(b1.T) {
		t.Fatalf("buckets not chronological: %v vs %v", b0.T, b1.T)
	}
}

func TestPrometheusMetricsFormat(t *testing.T) {
	store, _ := NewStore("")
	store.Register(RegisterDeviceRequest{ID: "l1", Name: "Lamp", Type: DeviceTypeESP, Category: CategoryLight})
	text := store.Stats().PrometheusMetrics(3)

	for _, want := range []string{
		`# TYPE lightgw_devices gauge`,
		`lightgw_devices{state="online"} 1`,
		`lightgw_devices_by_category{category="light"} 1`,
		`lightgw_realtime_connections 3`,
		`# TYPE lightgw_registrations_total counter`,
		`lightgw_registrations_total 1`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("metrics output missing %q:\n%s", want, text)
		}
	}
}
