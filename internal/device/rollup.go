package device

import (
	"sort"
	"time"
)

// Long-term telemetry retention via downsampling. Raw points stay capped at
// telemetryPerDevice for live detail; numeric points are also rolled up into
// fixed-resolution buckets (min/max/avg/last) kept far longer, so charts can
// show days/weeks/months without unbounded raw storage.

type rollupResolution struct {
	seconds   int64
	retention time.Duration
}

var rollupResolutions = []rollupResolution{
	{seconds: 60, retention: 48 * time.Hour},          // 1 minute, kept 2 days
	{seconds: 3600, retention: 30 * 24 * time.Hour},   // 1 hour, kept 30 days
	{seconds: 86400, retention: 365 * 24 * time.Hour}, // 1 day, kept 1 year
}

const rollupMaxBuckets = 1000 // target points per chart query

type rollupKey struct {
	DeviceID   string
	Key        string
	Resolution int64
	Bucket     int64 // unix seconds, bucket start
}

type rollupAgg struct {
	Count  int
	Min    float64
	Max    float64
	Sum    float64
	Last   float64
	LastTS time.Time
}

// updateRollupsLocked folds a numeric telemetry value into every resolution.
func (s *Store) updateRollupsLocked(deviceID, key string, ts time.Time, v float64) {
	for _, res := range rollupResolutions {
		bucket := ts.Unix() / res.seconds * res.seconds
		rk := rollupKey{DeviceID: deviceID, Key: key, Resolution: res.seconds, Bucket: bucket}
		a := s.rollups[rk]
		if a == nil {
			a = &rollupAgg{Min: v, Max: v}
			s.rollups[rk] = a
		}
		a.Count++
		a.Sum += v
		if v < a.Min {
			a.Min = v
		}
		if v > a.Max {
			a.Max = v
		}
		a.Last = v
		a.LastTS = ts
		if err := s.persistRollupLocked(rk, a); err != nil {
			s.logger.Warn("persist rollup failed", "error", err, "deviceId", deviceID, "key", key)
		}
	}
}

// PruneRollups drops rollup buckets older than each resolution's retention.
// Called periodically by the gateway (and directly in tests).
func (s *Store) PruneRollups() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC().Unix()
	retention := make(map[int64]int64, len(rollupResolutions))
	for _, res := range rollupResolutions {
		retention[res.seconds] = int64(res.retention.Seconds())
	}
	for rk := range s.rollups {
		ret, ok := retention[rk.Resolution]
		if !ok || rk.Bucket >= now-ret {
			continue
		}
		delete(s.rollups, rk)
		if s.storage == "sqlite" {
			if _, err := s.db.Exec(
				"DELETE FROM telemetry_rollup WHERE device_id=? AND key=? AND resolution=? AND bucket_start=?",
				rk.DeviceID, rk.Key, rk.Resolution, rk.Bucket); err != nil {
				s.logger.Warn("prune rollup failed", "error", err)
			}
		}
	}
}

// chooseResolution picks the finest resolution whose bucket count over span
// stays under rollupMaxBuckets, falling back to the coarsest.
func chooseResolution(span time.Duration) int64 {
	secs := int64(span.Seconds())
	if secs < 1 {
		secs = 1
	}
	for _, res := range rollupResolutions {
		if secs/res.seconds <= rollupMaxBuckets {
			return res.seconds
		}
	}
	return rollupResolutions[len(rollupResolutions)-1].seconds
}

// TelemetryHistory returns rolled-up buckets for a key over [from,to], choosing
// a resolution from the range width. Returns the chosen resolution (seconds).
func (s *Store) TelemetryHistory(deviceID, key string, from, to time.Time) (int64, []TelemetryBucket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.devices[deviceID]; !ok {
		return 0, nil, ErrNotFound
	}
	res := chooseResolution(to.Sub(from))
	fromBucket := from.Unix() / res * res
	toU := to.Unix()

	out := []TelemetryBucket{}
	for rk, a := range s.rollups {
		if rk.DeviceID != deviceID || rk.Key != key || rk.Resolution != res {
			continue
		}
		if rk.Bucket < fromBucket || rk.Bucket > toU {
			continue
		}
		out = append(out, TelemetryBucket{
			T:     time.Unix(rk.Bucket, 0).UTC(),
			Count: a.Count,
			Min:   a.Min,
			Max:   a.Max,
			Avg:   a.Sum / float64(a.Count),
			Last:  a.Last,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T.Before(out[j].T) })
	return res, out, nil
}

func (s *Store) persistRollupLocked(rk rollupKey, a *rollupAgg) error {
	if s.storage != "sqlite" {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO telemetry_rollup(device_id, key, resolution, bucket_start, count, min, max, sum, last, last_ts)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(device_id, key, resolution, bucket_start) DO UPDATE SET
		   count=excluded.count, min=excluded.min, max=excluded.max, sum=excluded.sum, last=excluded.last, last_ts=excluded.last_ts`,
		rk.DeviceID, rk.Key, rk.Resolution, rk.Bucket,
		a.Count, a.Min, a.Max, a.Sum, a.Last, a.LastTS.Format(time.RFC3339Nano))
	return err
}

func (s *Store) loadSQLiteRollups() error {
	rows, err := s.db.Query("SELECT device_id, key, resolution, bucket_start, count, min, max, sum, last, last_ts FROM telemetry_rollup")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var rk rollupKey
		var a rollupAgg
		var lastTS string
		if err := rows.Scan(&rk.DeviceID, &rk.Key, &rk.Resolution, &rk.Bucket, &a.Count, &a.Min, &a.Max, &a.Sum, &a.Last, &lastTS); err != nil {
			return err
		}
		if t, err := time.Parse(time.RFC3339Nano, lastTS); err == nil {
			a.LastTS = t
		}
		agg := a
		s.rollups[rk] = &agg
	}
	return rows.Err()
}
