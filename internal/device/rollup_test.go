package device

import (
	"testing"
	"time"
)

func TestChooseResolution(t *testing.T) {
	cases := []struct {
		span time.Duration
		want int64
	}{
		{3 * time.Hour, 60},          // 180 1m-buckets <= 1000 -> 1m
		{2 * 24 * time.Hour, 3600},   // 2880 1m-buckets > 1000 -> 1h
		{60 * 24 * time.Hour, 86400}, // too many for 1m/1h -> 1d
	}
	for _, c := range cases {
		if got := chooseResolution(c.span); got != c.want {
			t.Fatalf("chooseResolution(%v) = %d, want %d", c.span, got, c.want)
		}
	}
}

func TestRollupAggregationAndHistory(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	cur := time.Unix(1700000000, 0).UTC()
	store.now = func() time.Time { return cur }
	store.Register(RegisterDeviceRequest{ID: "d1", Name: "D1", Type: DeviceTypeESP, Category: CategoryClock})

	add := func(at time.Time, v float64) {
		cur = at
		if _, err := store.AddTelemetry("d1", TelemetryRequest{Key: "rssi", Value: v}); err != nil {
			t.Fatalf("telemetry: %v", err)
		}
	}
	t0 := time.Unix(1700000000, 0).UTC() // 1m bucket = 1699999980
	add(t0, 10)
	add(t0.Add(30*time.Second), 20) // same 1m bucket
	add(t0.Add(70*time.Second), 5)  // next 1m bucket
	// non-numeric value must be ignored by rollups
	cur = t0.Add(80 * time.Second)
	store.AddTelemetry("d1", TelemetryRequest{Key: "rssi", Value: "n/a"})

	res, buckets, err := store.TelemetryHistory("d1", "rssi", t0.Add(-time.Minute), t0.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if res != 60 {
		t.Fatalf("expected 1m resolution, got %d", res)
	}
	if len(buckets) != 2 {
		t.Fatalf("expected 2 buckets, got %d (%+v)", len(buckets), buckets)
	}
	if buckets[0].Count != 2 || buckets[0].Min != 10 || buckets[0].Max != 20 || buckets[0].Avg != 15 || buckets[0].Last != 20 {
		t.Fatalf("unexpected first bucket: %+v", buckets[0])
	}
	if buckets[1].Count != 1 || buckets[1].Last != 5 {
		t.Fatalf("unexpected second bucket: %+v", buckets[1])
	}
}

func TestPruneRollupsByResolutionRetention(t *testing.T) {
	store, err := NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cur := base
	store.now = func() time.Time { return cur }
	store.Register(RegisterDeviceRequest{ID: "d1", Name: "D1", Type: DeviceTypeESP, Category: CategoryClock})

	cur = base
	store.AddTelemetry("d1", TelemetryRequest{Key: "rssi", Value: float64(10)})

	// Advance 73h: the 1m bucket (retention 48h) expires; 1h/1d survive.
	cur = base.Add(73 * time.Hour)
	store.PruneRollups()

	// 1m window over base -> resolution 1m -> pruned -> empty.
	if res, b, _ := store.TelemetryHistory("d1", "rssi", base.Add(-time.Hour), base.Add(time.Hour)); res != 60 || len(b) != 0 {
		t.Fatalf("expected 1m buckets pruned, got res=%d len=%d", res, len(b))
	}
	// 48h window -> resolution 1h -> bucket still retained.
	if res, b, _ := store.TelemetryHistory("d1", "rssi", base.Add(-24*time.Hour), base.Add(24*time.Hour)); res != 3600 || len(b) != 1 {
		t.Fatalf("expected 1h bucket retained, got res=%d len=%d", res, len(b))
	}
}
