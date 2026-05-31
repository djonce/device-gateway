package weather

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWeatherCacheHonorsTTL(t *testing.T) {
	svc := NewService(nil)
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	cur := base
	svc.now = func() time.Time { return cur }
	calls := 0
	svc.Fetch = func(ctx context.Context, lat, lon float64) (Weather, error) {
		calls++
		return Weather{TempC: 20 + float64(calls), Text: "晴"}, nil
	}

	if _, err := svc.Get(context.Background(), 31.23, 121.47); err != nil {
		t.Fatalf("first get: %v", err)
	}
	if _, err := svc.Get(context.Background(), 31.23, 121.47); err != nil {
		t.Fatalf("cached get: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected 1 fetch within TTL, got %d", calls)
	}

	cur = base.Add(svc.ttl + time.Minute)
	if _, err := svc.Get(context.Background(), 31.23, 121.47); err != nil {
		t.Fatalf("get after ttl: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected refetch after TTL, got %d calls", calls)
	}
}

func TestGetServesStaleOnFetchError(t *testing.T) {
	svc := NewService(nil)
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	cur := base
	svc.now = func() time.Time { return cur }
	svc.Fetch = func(ctx context.Context, lat, lon float64) (Weather, error) {
		return Weather{TempC: 25, Text: "晴"}, nil
	}
	if _, err := svc.Get(context.Background(), 1, 2); err != nil {
		t.Fatalf("seed: %v", err)
	}

	svc.Fetch = func(ctx context.Context, lat, lon float64) (Weather, error) {
		return Weather{}, errors.New("upstream down")
	}
	cur = base.Add(svc.ttl + time.Minute)
	w, err := svc.Get(context.Background(), 1, 2)
	if err != nil {
		t.Fatalf("expected stale served without error, got %v", err)
	}
	if w.TempC != 25 {
		t.Fatalf("expected stale temp 25, got %v", w.TempC)
	}
}

func TestClockContentTimeAndWeather(t *testing.T) {
	svc := NewService(nil)
	fixed := time.Date(2026, 5, 31, 8, 30, 0, 0, time.UTC)
	svc.now = func() time.Time { return fixed }
	svc.Fetch = func(ctx context.Context, lat, lon float64) (Weather, error) {
		return Weather{TempC: 19.5, Code: 3, Text: "阴"}, nil
	}

	c := svc.ClockContent(31.23, 121.47, "Asia/Shanghai")
	if c.Time.Epoch != fixed.Unix() {
		t.Fatalf("epoch mismatch: got %d want %d", c.Time.Epoch, fixed.Unix())
	}
	// tz is echoed when the zone DB is available, otherwise falls back to UTC.
	if c.Time.TZ != "Asia/Shanghai" && c.Time.TZ != "UTC" {
		t.Fatalf("unexpected tz %q", c.Time.TZ)
	}
	if c.Weather == nil || c.Weather.Text != "阴" {
		t.Fatalf("weather missing or wrong: %+v", c.Weather)
	}
}

func TestNormalizeAndWMOMapping(t *testing.T) {
	var raw openMeteoResponse
	raw.Current.Temperature = 22.1
	raw.Current.Humidity = 55
	raw.Current.WeatherCode = 61
	raw.Daily.Time = []string{"2026-05-31", "2026-06-01"}
	raw.Daily.Code = []int{0, 95}
	raw.Daily.MaxTemp = []float64{28, 30}
	raw.Daily.MinTemp = []float64{18, 19}

	w := normalize(raw)
	if w.Text != "雨" {
		t.Fatalf("expected 雨 for code 61, got %q", w.Text)
	}
	if w.Humidity != 55 || w.TempC != 22.1 {
		t.Fatalf("current mapping wrong: %+v", w)
	}
	if len(w.Daily) != 2 || w.Daily[0].Text != "晴" || w.Daily[1].Text != "雷阵雨" {
		t.Fatalf("daily mapping wrong: %+v", w.Daily)
	}
	if w.Daily[1].MaxC != 30 || w.Daily[1].MinC != 19 {
		t.Fatalf("daily temps wrong: %+v", w.Daily[1])
	}
	if WMOText(48) != "雾" || WMOText(12345) != "未知" {
		t.Fatalf("WMOText mapping wrong")
	}
}
