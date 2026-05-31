package device

import "math"

const earthRadiusM = 6371000.0

// haversineMeters returns the great-circle distance between two lat/lng points.
func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLng := (lng2 - lng1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusM * c
}

// toFloat coerces a JSON-decoded numeric value to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// parseLatLng extracts lat/lng (and optional speed) from a gps.fix value, which
// is a JSON object like {"lat":..,"lng":..,"speed":..}.
func parseLatLng(v any) (lat, lng, speed float64, ok bool) {
	m, isMap := v.(map[string]any)
	if !isMap {
		return 0, 0, 0, false
	}
	lat, ok1 := toFloat(m["lat"])
	lng, ok2 := toFloat(m["lng"])
	if !ok1 || !ok2 {
		return 0, 0, 0, false
	}
	speed, _ = toFloat(m["speed"])
	return lat, lng, speed, true
}

// SetGeofence stores (or clears) a device's geofence. radiusM<=0 clears it.
// Geofence transition detection then happens as gps.fix telemetry arrives.
func (s *Store) SetGeofence(deviceID string, req SetGeofenceRequest) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return Device{}, ErrNotFound
	}
	now := s.now().UTC()
	if req.RadiusM <= 0 {
		device.Geofence = nil
		device.GeofenceState = ""
		s.appendEventLocked("geofence.cleared", deviceID, "geofence cleared", nil)
	} else {
		device.Geofence = &Geofence{CenterLat: req.CenterLat, CenterLng: req.CenterLng, RadiusM: req.RadiusM}
		device.GeofenceState = "" // unknown until the next fix is evaluated
		s.appendEventLocked("geofence.configured", deviceID, "geofence configured", map[string]any{
			"centerLat": req.CenterLat, "centerLng": req.CenterLng, "radiusM": req.RadiusM,
		})
	}
	device.UpdatedAt = now
	s.devices[deviceID] = device
	if err := s.persistDeviceLocked(device); err != nil {
		return Device{}, err
	}
	return device, nil
}

// evaluateGeofenceLocked checks a new gps.fix against the device geofence and
// emits a geofence.enter / geofence.exit event on state transitions. Caller
// must hold s.mu.
func (s *Store) evaluateGeofenceLocked(deviceID string, value any) {
	device, ok := s.devices[deviceID]
	if !ok || device.Geofence == nil {
		return
	}
	lat, lng, _, ok := parseLatLng(value)
	if !ok {
		return
	}
	dist := haversineMeters(device.Geofence.CenterLat, device.Geofence.CenterLng, lat, lng)
	inside := dist <= device.Geofence.RadiusM
	newState := "outside"
	if inside {
		newState = "inside"
	}
	if device.GeofenceState == newState {
		return
	}
	device.GeofenceState = newState
	s.devices[deviceID] = device
	eventType := "geofence.exit"
	message := "device left geofence"
	if inside {
		eventType = "geofence.enter"
		message = "device entered geofence"
	}
	s.appendEventLocked(eventType, deviceID, message, map[string]any{
		"lat": lat, "lng": lng, "distanceM": math.Round(dist),
	})
	if err := s.persistDeviceLocked(device); err != nil {
		s.logger.Warn("persist geofence state failed", "error", err, "deviceId", deviceID)
	}
}

// ListTrack returns recent gps.fix positions for a device in chronological order
// (oldest first), capped at limit (most recent points kept when over the cap).
func (s *Store) ListTrack(deviceID string, limit int) ([]TrackPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.devices[deviceID]; !ok {
		return nil, ErrNotFound
	}
	out := []TrackPoint{}
	for _, p := range s.telemetry[deviceID] {
		if p.Key != "gps.fix" {
			continue
		}
		lat, lng, speed, ok := parseLatLng(p.Value)
		if !ok {
			continue
		}
		out = append(out, TrackPoint{Lat: lat, Lng: lng, Speed: speed, Timestamp: p.Timestamp})
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
