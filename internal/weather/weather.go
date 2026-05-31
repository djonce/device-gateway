// Package weather provides time + weather content for clock-category devices.
//
// Weather is fetched from Open-Meteo (https://open-meteo.com), which is free
// and requires no API key, so the gateway holds no secret and devices never
// talk to a third-party API directly. Results are cached per location with a
// TTL to avoid hammering the upstream. The fetch function is injectable so the
// service can be unit-tested without network access.
package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Weather is the normalized current conditions plus a short daily forecast.
type Weather struct {
	TempC     float64         `json:"tempC"`
	Humidity  int             `json:"humidity"`
	Code      int             `json:"code"`
	Text      string          `json:"text"`
	Daily     []DailyForecast `json:"daily,omitempty"`
	FetchedAt time.Time       `json:"fetchedAt"`
}

// DailyForecast is one day of the forecast.
type DailyForecast struct {
	Date string  `json:"date"`
	Code int     `json:"code"`
	Text string  `json:"text"`
	MaxC float64 `json:"maxC"`
	MinC float64 `json:"minC"`
}

// ClockTime is the current time the screen should display/sync to.
type ClockTime struct {
	Epoch int64  `json:"epoch"`
	ISO   string `json:"iso"`
	TZ    string `json:"tz"`
}

// ClockContent is what a clock device pulls each cycle.
type ClockContent struct {
	Time         ClockTime `json:"time"`
	Weather      *Weather  `json:"weather,omitempty"`
	WeatherError string    `json:"weatherError,omitempty"`
}

type cacheEntry struct {
	weather Weather
	at      time.Time
}

// Service fetches and caches weather, and assembles clock content.
type Service struct {
	client *http.Client
	ttl    time.Duration
	now    func() time.Time
	logger *slog.Logger

	// Fetch is the upstream weather fetcher. Defaults to Open-Meteo; overridden
	// in tests.
	Fetch func(ctx context.Context, lat, lon float64) (Weather, error)

	mu    sync.Mutex
	cache map[string]cacheEntry
}

// NewService builds a weather service with a sensible default cache TTL.
func NewService(logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		client: &http.Client{Timeout: 8 * time.Second},
		ttl:    10 * time.Minute,
		now:    time.Now,
		logger: logger,
		cache:  map[string]cacheEntry{},
	}
	s.Fetch = s.fetchOpenMeteo
	return s
}

func cacheKey(lat, lon float64) string {
	// ~100m precision is plenty for weather and keeps the cache small.
	return fmt.Sprintf("%.3f,%.3f", lat, lon)
}

// Get returns cached weather for a location, fetching on miss or when stale.
func (s *Service) Get(ctx context.Context, lat, lon float64) (Weather, error) {
	key := cacheKey(lat, lon)
	now := s.now()

	s.mu.Lock()
	entry, ok := s.cache[key]
	if ok && now.Sub(entry.at) < s.ttl {
		s.mu.Unlock()
		return entry.weather, nil
	}
	s.mu.Unlock()

	w, err := s.Fetch(ctx, lat, lon)
	if err != nil {
		// Serve stale data if we have any; otherwise surface the error.
		if ok {
			s.logger.Warn("weather fetch failed, serving stale", "error", err, "loc", key)
			return entry.weather, nil
		}
		return Weather{}, err
	}
	w.FetchedAt = now

	s.mu.Lock()
	s.cache[key] = cacheEntry{weather: w, at: now}
	s.mu.Unlock()
	return w, nil
}

// ClockContent assembles current time (optionally in tz) plus weather. Weather
// failures are non-fatal: time is always returned so the screen can still tick.
func (s *Service) ClockContent(lat, lon float64, tz string) ClockContent {
	now := s.now()
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		} else {
			tz = "UTC"
		}
	} else {
		tz = "UTC"
	}
	content := ClockContent{
		Time: ClockTime{Epoch: now.Unix(), ISO: now.In(loc).Format(time.RFC3339), TZ: tz},
	}
	w, err := s.Get(context.Background(), lat, lon)
	if err != nil {
		content.WeatherError = err.Error()
		return content
	}
	content.Weather = &w
	return content
}

// openMeteoResponse mirrors the subset of the Open-Meteo forecast API we use.
type openMeteoResponse struct {
	Current struct {
		Temperature float64 `json:"temperature_2m"`
		Humidity    int     `json:"relative_humidity_2m"`
		WeatherCode int     `json:"weather_code"`
	} `json:"current"`
	Daily struct {
		Time    []string  `json:"time"`
		Code    []int     `json:"weather_code"`
		MaxTemp []float64 `json:"temperature_2m_max"`
		MinTemp []float64 `json:"temperature_2m_min"`
	} `json:"daily"`
}

func (s *Service) fetchOpenMeteo(ctx context.Context, lat, lon float64) (Weather, error) {
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f"+
			"&current=temperature_2m,relative_humidity_2m,weather_code"+
			"&daily=weather_code,temperature_2m_max,temperature_2m_min"+
			"&timezone=UTC&forecast_days=3",
		lat, lon)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Weather{}, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return Weather{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Weather{}, fmt.Errorf("open-meteo status %d", resp.StatusCode)
	}
	var raw openMeteoResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Weather{}, err
	}
	return normalize(raw), nil
}

// normalize converts an Open-Meteo response into our Weather type. Exposed
// indirectly via fetchOpenMeteo; kept separate so tests can exercise mapping.
func normalize(raw openMeteoResponse) Weather {
	w := Weather{
		TempC:    raw.Current.Temperature,
		Humidity: raw.Current.Humidity,
		Code:     raw.Current.WeatherCode,
		Text:     WMOText(raw.Current.WeatherCode),
	}
	for i := range raw.Daily.Time {
		d := DailyForecast{Date: raw.Daily.Time[i]}
		if i < len(raw.Daily.Code) {
			d.Code = raw.Daily.Code[i]
			d.Text = WMOText(raw.Daily.Code[i])
		}
		if i < len(raw.Daily.MaxTemp) {
			d.MaxC = raw.Daily.MaxTemp[i]
		}
		if i < len(raw.Daily.MinTemp) {
			d.MinC = raw.Daily.MinTemp[i]
		}
		w.Daily = append(w.Daily, d)
	}
	return w
}

// WMOText maps a WMO weather interpretation code to a short Chinese label.
func WMOText(code int) string {
	switch code {
	case 0:
		return "晴"
	case 1:
		return "多云转晴"
	case 2:
		return "局部多云"
	case 3:
		return "阴"
	case 45, 48:
		return "雾"
	case 51, 53, 55:
		return "毛毛雨"
	case 56, 57:
		return "冻毛毛雨"
	case 61, 63, 65:
		return "雨"
	case 66, 67:
		return "冻雨"
	case 71, 73, 75:
		return "雪"
	case 77:
		return "雪粒"
	case 80, 81, 82:
		return "阵雨"
	case 85, 86:
		return "阵雪"
	case 95:
		return "雷阵雨"
	case 96, 99:
		return "雷阵雨伴冰雹"
	default:
		return "未知"
	}
}
