package device

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Stats is a point-in-time snapshot of fleet state plus lifetime counters.
type Stats struct {
	DevicesByState    map[string]int `json:"devicesByState"`    // online / stale / offline
	DevicesByCategory map[string]int `json:"devicesByCategory"` // light / clock / gps / voice / generic
	Total             int            `json:"total"`
	Disabled          int            `json:"disabled"`
	TelemetryStored   int            `json:"telemetryStored"`
	Firmware          int            `json:"firmware"`

	Registrations     int64 `json:"registrations"`
	TelemetryReceived int64 `json:"telemetryReceived"`
	CommandsCreated   int64 `json:"commandsCreated"`
	CommandAcks       int64 `json:"commandAcks"`
	Events            int64 `json:"events"`
}

// Stats returns a fleet snapshot. Device online state is recomputed from
// last-seen times so it reflects the current moment.
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now().UTC()
	out := Stats{
		DevicesByState:    map[string]int{"online": 0, "stale": 0, "offline": 0},
		DevicesByCategory: map[string]int{},
		Total:             len(s.devices),
		Firmware:          len(s.firmwares),
		Registrations:     s.statRegistrations,
		TelemetryReceived: s.statTelemetry,
		CommandsCreated:   s.statCommands,
		CommandAcks:       s.statAcks,
		Events:            s.statEvents,
	}
	for _, device := range s.devices {
		if device.Disabled {
			out.Disabled++
		}
		out.DevicesByState[string(stateFor(now, device.LastSeenAt))]++
		cat := string(device.Category)
		if cat == "" {
			cat = "generic"
		}
		out.DevicesByCategory[cat]++
	}
	for _, points := range s.telemetry {
		out.TelemetryStored += len(points)
	}
	return out
}

// TelemetryBucket is one aggregated time bucket for a telemetry key.
type TelemetryBucket struct {
	T     time.Time `json:"t"`
	Count int       `json:"count"`
	Min   float64   `json:"min"`
	Max   float64   `json:"max"`
	Avg   float64   `json:"avg"`
	Last  float64   `json:"last"`
}

// TelemetrySeries aggregates a device's numeric telemetry for one key into
// fixed time buckets (bucketSeconds wide), returning the most recent `limit`
// buckets in chronological order. Non-numeric values are ignored.
func (s *Store) TelemetrySeries(deviceID, key string, bucketSeconds, limit int) ([]TelemetryBucket, error) {
	if bucketSeconds <= 0 {
		bucketSeconds = 60
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.devices[deviceID]; !ok {
		return nil, ErrNotFound
	}

	type agg struct {
		count    int
		min, max float64
		sum      float64
		last     float64
		lastTime time.Time
	}
	buckets := map[int64]*agg{}
	for _, p := range s.telemetry[deviceID] {
		if p.Key != key {
			continue
		}
		v, ok := toFloat(p.Value)
		if !ok {
			continue
		}
		bucket := p.Timestamp.Unix() / int64(bucketSeconds) * int64(bucketSeconds)
		a := buckets[bucket]
		if a == nil {
			a = &agg{min: v, max: v}
			buckets[bucket] = a
		}
		a.count++
		a.sum += v
		if v < a.min {
			a.min = v
		}
		if v > a.max {
			a.max = v
		}
		if !p.Timestamp.Before(a.lastTime) {
			a.last = v
			a.lastTime = p.Timestamp
		}
	}

	out := make([]TelemetryBucket, 0, len(buckets))
	for ts, a := range buckets {
		out = append(out, TelemetryBucket{
			T:     time.Unix(ts, 0).UTC(),
			Count: a.count,
			Min:   a.min,
			Max:   a.max,
			Avg:   a.sum / float64(a.count),
			Last:  a.last,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

// PrometheusMetrics renders the stats (plus the live realtime connection count)
// in Prometheus text exposition format. Hand-written to avoid a dependency.
func (st Stats) PrometheusMetrics(realtimeConnections int) string {
	var b strings.Builder
	gauge := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
	}
	counter := func(name, help string) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	}

	gauge("lightgw_devices", "Devices by online state.")
	for _, state := range []string{"online", "stale", "offline"} {
		fmt.Fprintf(&b, "lightgw_devices{state=%q} %d\n", state, st.DevicesByState[state])
	}
	gauge("lightgw_devices_by_category", "Devices by product category.")
	for _, cat := range sortedKeys(st.DevicesByCategory) {
		fmt.Fprintf(&b, "lightgw_devices_by_category{category=%q} %d\n", cat, st.DevicesByCategory[cat])
	}
	gauge("lightgw_devices_disabled", "Disabled devices.")
	fmt.Fprintf(&b, "lightgw_devices_disabled %d\n", st.Disabled)
	gauge("lightgw_devices_total", "Total registered devices.")
	fmt.Fprintf(&b, "lightgw_devices_total %d\n", st.Total)
	gauge("lightgw_realtime_connections", "Active realtime voice connections.")
	fmt.Fprintf(&b, "lightgw_realtime_connections %d\n", realtimeConnections)
	gauge("lightgw_telemetry_stored", "Telemetry points currently stored.")
	fmt.Fprintf(&b, "lightgw_telemetry_stored %d\n", st.TelemetryStored)
	gauge("lightgw_firmware_total", "Firmware artifacts stored.")
	fmt.Fprintf(&b, "lightgw_firmware_total %d\n", st.Firmware)

	counter("lightgw_registrations_total", "Device registration calls.")
	fmt.Fprintf(&b, "lightgw_registrations_total %d\n", st.Registrations)
	counter("lightgw_telemetry_received_total", "Telemetry points received.")
	fmt.Fprintf(&b, "lightgw_telemetry_received_total %d\n", st.TelemetryReceived)
	counter("lightgw_commands_created_total", "Commands created.")
	fmt.Fprintf(&b, "lightgw_commands_created_total %d\n", st.CommandsCreated)
	counter("lightgw_command_acks_total", "Command acknowledgements.")
	fmt.Fprintf(&b, "lightgw_command_acks_total %d\n", st.CommandAcks)
	counter("lightgw_events_total", "Events recorded.")
	fmt.Fprintf(&b, "lightgw_events_total %d\n", st.Events)
	return b.String()
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
