package device

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"light-gateway/internal/rules"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrBadRequest   = errors.New("bad request")
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

const (
	staleAfter   = 2 * time.Minute
	offlineAfter = 10 * time.Minute

	// In-memory and on-disk retention caps. The store keeps the most recent
	// rows per device (telemetry, commands) and globally (events); older rows
	// are pruned. In-flight commands (queued/delivered) are never pruned.
	telemetryPerDevice = 500
	commandsPerDevice  = 200
	eventBacklog       = 1000
)

type Store struct {
	mu            sync.RWMutex
	path          string
	storage       string
	db            *sql.DB
	devices       map[string]Device
	tokens        map[string]DeviceToken
	telemetry     map[string][]TelemetryPoint
	commands      map[string]Command
	events        []Event
	firmwares     map[string]Firmware
	firmwareBlobs map[string][]byte
	rules         map[string]rules.Rule
	enabledRules  int
	rollups       map[rollupKey]*rollupAgg
	apiKeys       map[string]APIKey
	now           func() time.Time
	logger        *slog.Logger

	// Cumulative counters (process lifetime), guarded by mu.
	statRegistrations int64
	statTelemetry     int64
	statCommands      int64
	statAcks          int64
	statEvents        int64
}

func NewStore(path string) (*Store, error) {
	s := &Store{
		path:          path,
		storage:       "memory",
		devices:       map[string]Device{},
		tokens:        map[string]DeviceToken{},
		telemetry:     map[string][]TelemetryPoint{},
		commands:      map[string]Command{},
		events:        []Event{},
		firmwares:     map[string]Firmware{},
		firmwareBlobs: map[string][]byte{},
		rules:         map[string]rules.Rule{},
		rollups:       map[rollupKey]*rollupAgg{},
		apiKeys:       map[string]APIKey{},
		now:           time.Now,
		logger:        slog.Default(),
	}
	if path == "" {
		// In-memory only: used for tests and ephemeral/dev runs.
		return s, nil
	}
	// Any configured data path uses the SQLite backend.
	s.storage = "sqlite"
	if err := s.loadSQLite(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Register(req RegisterDeviceRequest) (DeviceRegistration, error) {
	req.ID = strings.TrimSpace(req.ID)
	req.Name = strings.TrimSpace(req.Name)
	if req.ID == "" || req.Name == "" {
		return DeviceRegistration{}, fmt.Errorf("%w: id and name are required", ErrBadRequest)
	}
	if req.Type == "" {
		req.Type = DeviceTypeUnknown
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().UTC()
	existing, ok := s.devices[req.ID]
	registeredAt := now
	disabled := false
	if ok {
		registeredAt = existing.RegisteredAt
		disabled = existing.Disabled
	}
	device := Device{
		ID:           req.ID,
		Name:         req.Name,
		Type:         req.Type,
		Category:     req.Category,
		Model:        req.Model,
		Profile:      resolveProfileID(req.Category, req.Profile),
		FwVersion:    req.FwVersion,
		AgentVersion: req.AgentVersion,
		Labels:       cloneStringMap(req.Labels),
		Capabilities: cloneCapabilities(req.Capabilities),
		Metadata:     cloneAnyMap(req.Metadata),
		Disabled:     disabled,
		LastSeenAt:   now,
		RegisteredAt: registeredAt,
		UpdatedAt:    now,
		State:        OnlineStateOnline,
	}
	// Preserve server-managed state across re-registration (devices re-register
	// on every boot and don't report these): geofence and OTA target.
	if ok {
		device.Geofence = existing.Geofence
		device.GeofenceState = existing.GeofenceState
		device.TargetFwVersion = existing.TargetFwVersion
	}

	rawToken := ""
	if _, ok := s.tokens[device.ID]; !ok {
		token, err := generateToken()
		if err != nil {
			return DeviceRegistration{}, err
		}
		rawToken = token
		s.tokens[device.ID] = DeviceToken{
			DeviceID:  device.ID,
			TokenHash: hashToken(token),
			CreatedAt: now,
		}
		if err := s.persistTokenLocked(s.tokens[device.ID]); err != nil {
			return DeviceRegistration{}, err
		}
	}
	s.applyTokenMetadataLocked(&device)
	s.devices[device.ID] = device
	s.appendEventLocked("device.registered", device.ID, "device registered", map[string]any{"name": device.Name, "type": device.Type})
	s.statRegistrations++
	if err := s.persistDeviceLocked(device); err != nil {
		return DeviceRegistration{}, err
	}
	return DeviceRegistration{Device: device, Token: rawToken, Reused: ok}, nil
}

func (s *Store) Heartbeat(deviceID string, req HeartbeatRequest) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	device, ok := s.devices[deviceID]
	if !ok {
		return Device{}, ErrNotFound
	}
	now := s.now().UTC()
	device.LastSeenAt = now
	device.UpdatedAt = now
	device.State = OnlineStateOnline
	if req.AgentVersion != "" {
		device.AgentVersion = req.AgentVersion
	}
	if req.Labels != nil {
		device.Labels = cloneStringMap(req.Labels)
	}
	if req.Metadata != nil {
		device.Metadata = cloneAnyMap(req.Metadata)
	}
	s.devices[device.ID] = device
	s.appendEventLocked("device.heartbeat", device.ID, "heartbeat received", nil)
	if err := s.persistDeviceLocked(device); err != nil {
		return Device{}, err
	}
	return device, nil
}

func (s *Store) ListDevices() []Device {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := s.now().UTC()
	out := make([]Device, 0, len(s.devices))
	for _, device := range s.devices {
		// device is a copy; compute state and token metadata onto the copy only.
		device.State = stateFor(now, device.LastSeenAt)
		s.applyTokenMetadataLocked(&device)
		out = append(out, device)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	return out
}

func (s *Store) GetDevice(id string) (Device, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	device, ok := s.devices[id]
	if !ok {
		return Device{}, ErrNotFound
	}
	device.State = stateFor(s.now().UTC(), device.LastSeenAt)
	s.applyTokenMetadataLocked(&device)
	return device, nil
}

func (s *Store) VerifyDeviceToken(deviceID, token string) error {
	deviceID = strings.TrimSpace(deviceID)
	token = strings.TrimSpace(token)
	if deviceID == "" || token == "" {
		return ErrUnauthorized
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	device, ok := s.devices[deviceID]
	if !ok {
		return ErrNotFound
	}
	if device.Disabled {
		return ErrForbidden
	}
	stored, ok := s.tokens[deviceID]
	if !ok {
		return ErrUnauthorized
	}
	if subtle.ConstantTimeCompare([]byte(stored.TokenHash), []byte(hashToken(token))) != 1 {
		return ErrUnauthorized
	}
	now := s.now().UTC()
	stored.LastUsedAt = &now
	s.tokens[deviceID] = stored
	// Only the token row changes here. Device.TokenLastUsedAt is derived from the
	// token map on read (applyTokenMetadataLocked), so we don't rewrite the device
	// on every auth — this keeps high-frequency polling cheap.
	return s.persistTokenLocked(stored)
}

func (s *Store) ResetDeviceToken(deviceID string) (DeviceTokenReset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[deviceID]; !ok {
		return DeviceTokenReset{}, ErrNotFound
	}
	token, err := generateToken()
	if err != nil {
		return DeviceTokenReset{}, err
	}
	now := s.now().UTC()
	s.tokens[deviceID] = DeviceToken{
		DeviceID:  deviceID,
		TokenHash: hashToken(token),
		CreatedAt: now,
	}
	device := s.devices[deviceID]
	s.applyTokenMetadataLocked(&device)
	device.UpdatedAt = now
	s.devices[deviceID] = device
	s.appendEventLocked("device.token_reset", deviceID, "device token reset", nil)
	if err := s.persistTokenLocked(s.tokens[deviceID]); err != nil {
		return DeviceTokenReset{}, err
	}
	if err := s.persistDeviceLocked(device); err != nil {
		return DeviceTokenReset{}, err
	}
	return DeviceTokenReset{DeviceID: deviceID, Token: token, IssuedAt: now}, nil
}

func (s *Store) UpdateDeviceStatus(deviceID string, req UpdateDeviceStatusRequest) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return Device{}, ErrNotFound
	}
	now := s.now().UTC()
	device.Disabled = req.Disabled
	device.UpdatedAt = now
	s.applyTokenMetadataLocked(&device)
	s.devices[deviceID] = device
	eventType := "device.enabled"
	message := "device enabled"
	if req.Disabled {
		eventType = "device.disabled"
		message = "device disabled"
	}
	s.appendEventLocked(eventType, deviceID, message, nil)
	if err := s.persistDeviceLocked(device); err != nil {
		return Device{}, err
	}
	return device, nil
}

func (s *Store) AddTelemetry(deviceID string, req TelemetryRequest) (TelemetryPoint, error) {
	req.Key = strings.TrimSpace(req.Key)
	if req.Key == "" {
		return TelemetryPoint{}, fmt.Errorf("%w: key is required", ErrBadRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[deviceID]; !ok {
		return TelemetryPoint{}, ErrNotFound
	}
	now := s.now().UTC()
	point := TelemetryPoint{
		ID:        newID("tel", now),
		DeviceID:  deviceID,
		Key:       req.Key,
		Value:     req.Value,
		Unit:      req.Unit,
		Timestamp: now,
		Metadata:  cloneAnyMap(req.Metadata),
	}
	points := s.telemetry[deviceID]
	prune := len(points) >= telemetryPerDevice
	s.telemetry[deviceID] = appendBounded(points, point, telemetryPerDevice)
	s.appendEventLocked("telemetry.received", deviceID, "telemetry received", map[string]any{"key": point.Key, "value": point.Value})
	s.statTelemetry++
	if err := s.persistTelemetryLocked(point, prune); err != nil {
		return TelemetryPoint{}, err
	}
	if point.Key == "gps.fix" {
		s.evaluateGeofenceLocked(deviceID, point.Value)
	}
	if v, ok := toFloat(point.Value); ok {
		s.updateRollupsLocked(deviceID, point.Key, point.Timestamp, v)
		if s.enabledRules > 0 {
			cat := string(s.devices[deviceID].Category)
			go s.fireRules(rules.Context{Kind: rules.TriggerTelemetry, DeviceID: deviceID, Category: cat, Key: point.Key, Value: v})
		}
	}
	return point, nil
}

func (s *Store) ListTelemetry(deviceID string, limit int) ([]TelemetryPoint, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.devices[deviceID]; !ok {
		return nil, ErrNotFound
	}
	points := s.telemetry[deviceID]
	if limit <= 0 || limit > len(points) {
		limit = len(points)
	}
	out := make([]TelemetryPoint, 0, limit)
	out = append(out, points[len(points)-limit:]...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.After(out[j].Timestamp)
	})
	return out, nil
}

func (s *Store) CreateCommand(deviceID string, req CreateCommandRequest) (Command, error) {
	req.Type = strings.TrimSpace(req.Type)
	if req.Type == "" {
		return Command{}, fmt.Errorf("%w: type is required", ErrBadRequest)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	device, ok := s.devices[deviceID]
	if !ok {
		return Command{}, ErrNotFound
	}
	// Profile enforcement: a device bound to a product profile only accepts the
	// commands that profile declares. Devices without a profile (infra/bridge
	// agents such as the serial or Linux agents) accept any command type.
	if prof, ok := profileForDevice(device); ok && !prof.AllowsCommand(req.Type) {
		return Command{}, fmt.Errorf("%w: command %q is not allowed by profile %q", ErrBadRequest, req.Type, prof.ID)
	}
	now := s.now().UTC()
	cmd := Command{
		ID:          newID("cmd", now),
		DeviceID:    deviceID,
		Type:        req.Type,
		Payload:     cloneAnyMap(req.Payload),
		Status:      CommandStatusQueued,
		RequestedBy: req.RequestedBy,
		CreatedAt:   now,
	}
	if req.TTLSeconds > 0 {
		expires := now.Add(time.Duration(req.TTLSeconds) * time.Second)
		cmd.ExpiresAt = &expires
	}
	s.commands[cmd.ID] = cmd
	s.appendEventLocked("command.created", deviceID, "command queued", map[string]any{"commandId": cmd.ID, "type": cmd.Type})
	s.statCommands++
	if err := s.persistCommandLocked(cmd); err != nil {
		return Command{}, err
	}
	s.pruneCommandsLocked(deviceID)
	return cmd, nil
}

// pruneCommandsLocked caps a device's command history at commandsPerDevice,
// dropping the oldest terminal commands first. In-flight commands
// (queued/delivered) are always kept regardless of the cap.
func (s *Store) pruneCommandsLocked(deviceID string) {
	type ref struct {
		id       string
		created  time.Time
		terminal bool
	}
	refs := make([]ref, 0)
	for id, c := range s.commands {
		if c.DeviceID != deviceID {
			continue
		}
		terminal := c.Status == CommandStatusSucceeded || c.Status == CommandStatusFailed || c.Status == CommandStatusExpired
		refs = append(refs, ref{id: id, created: c.CreatedAt, terminal: terminal})
	}
	if len(refs) <= commandsPerDevice {
		return
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].created.After(refs[j].created) })
	for _, r := range refs[commandsPerDevice:] {
		if !r.terminal {
			continue // never drop in-flight commands
		}
		delete(s.commands, r.id)
		if s.storage == "sqlite" {
			if _, err := s.db.Exec("DELETE FROM commands WHERE id=?", r.id); err != nil {
				s.logger.Warn("prune command failed", "error", err, "commandId", r.id)
			}
		}
	}
}

func (s *Store) ListCommands(deviceID string, limit int) ([]Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[deviceID]; !ok {
		return nil, ErrNotFound
	}
	now := s.now().UTC()
	out := []Command{}
	for id, cmd := range s.commands {
		if cmd.DeviceID != deviceID {
			continue
		}
		if isExpired(now, cmd) {
			cmd.Status = CommandStatusExpired
			finished := now
			cmd.FinishedAt = &finished
			s.commands[id] = cmd
			if err := s.persistCommandLocked(cmd); err != nil {
				s.logger.Warn("persist expired command failed", "error", err, "commandId", id)
			}
		}
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) NextCommand(deviceID string) (Command, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[deviceID]; !ok {
		return Command{}, false, ErrNotFound
	}
	now := s.now().UTC()
	var selected *Command
	for _, cmd := range s.commands {
		if cmd.DeviceID != deviceID {
			continue
		}
		if isExpired(now, cmd) {
			cmd.Status = CommandStatusExpired
			finished := now
			cmd.FinishedAt = &finished
			s.commands[cmd.ID] = cmd
			if err := s.persistCommandLocked(cmd); err != nil {
				s.logger.Warn("persist expired command failed", "error", err, "commandId", cmd.ID)
			}
			continue
		}
		if cmd.Status != CommandStatusQueued {
			continue
		}
		candidate := cmd
		if selected == nil || candidate.CreatedAt.Before(selected.CreatedAt) {
			selected = &candidate
		}
	}
	if selected == nil {
		return Command{}, false, nil
	}
	delivered := now
	selected.Status = CommandStatusDelivered
	selected.DeliveredAt = &delivered
	s.commands[selected.ID] = *selected
	s.appendEventLocked("command.delivered", deviceID, "command delivered", map[string]any{"commandId": selected.ID, "type": selected.Type})
	if err := s.persistCommandLocked(*selected); err != nil {
		return Command{}, false, err
	}
	return *selected, true, nil
}

func (s *Store) AckCommandForDevice(deviceID, commandID string, req AckCommandRequest) (Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.commands[commandID]
	if !ok || cmd.DeviceID != deviceID {
		return Command{}, ErrNotFound
	}
	if req.Status != CommandStatusSucceeded && req.Status != CommandStatusFailed {
		return Command{}, fmt.Errorf("%w: status must be succeeded or failed", ErrBadRequest)
	}
	now := s.now().UTC()
	cmd.Status = req.Status
	cmd.Result = cloneAnyMap(req.Result)
	cmd.Error = req.Error
	cmd.FinishedAt = &now
	s.commands[cmd.ID] = cmd
	s.appendEventLocked("command.ack", cmd.DeviceID, "command acknowledged", map[string]any{"commandId": cmd.ID, "status": cmd.Status})
	s.statAcks++
	if err := s.persistCommandLocked(cmd); err != nil {
		return Command{}, err
	}
	return cmd, nil
}

func (s *Store) ListEvents(limit int) []Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 || limit > len(s.events) {
		limit = len(s.events)
	}
	out := append([]Event(nil), s.events[len(s.events)-limit:]...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].At.After(out[j].At)
	})
	return out
}

// RecordEvent appends an event from an external source (e.g. the realtime hub)
// into the gateway event stream. Argument order matches realtime.Hub.OnEvent
// (deviceID first) so it can be passed directly as the callback.
func (s *Store) RecordEvent(deviceID, eventType, message string, metadata map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendEventLocked(eventType, deviceID, message, metadata)
}

func (s *Store) appendEventLocked(eventType, deviceID, message string, metadata map[string]any) {
	s.statEvents++
	now := s.now().UTC()
	event := Event{
		ID:       newID("evt", now),
		Type:     eventType,
		DeviceID: deviceID,
		Message:  message,
		At:       now,
		Metadata: cloneAnyMap(metadata),
	}
	prune := len(s.events) >= eventBacklog
	s.events = appendBounded(s.events, event, eventBacklog)
	if err := s.persistEventLocked(event, prune); err != nil {
		s.logger.Warn("persist event failed", "error", err, "type", eventType)
	}
	if s.enabledRules > 0 && ruleEventAllowed(eventType) {
		cat := ""
		if d, ok := s.devices[deviceID]; ok {
			cat = string(d.Category)
		}
		go s.fireRules(rules.Context{Kind: rules.TriggerEvent, DeviceID: deviceID, Category: cat, EventType: eventType})
	}
}

func (s *Store) loadSQLite() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", s.path)
	if err != nil {
		return err
	}
	s.db = db
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		return err
	}
	if err := s.migrateSQLite(); err != nil {
		return err
	}
	if err := s.loadSQLiteDevices(); err != nil {
		return err
	}
	if err := s.loadSQLiteTokens(); err != nil {
		return err
	}
	if err := s.loadSQLiteTelemetry(); err != nil {
		return err
	}
	if err := s.loadSQLiteCommands(); err != nil {
		return err
	}
	if err := s.loadSQLiteEvents(); err != nil {
		return err
	}
	if err := s.loadSQLiteFirmware(); err != nil {
		return err
	}
	if err := s.loadSQLiteRules(); err != nil {
		return err
	}
	if err := s.loadSQLiteRollups(); err != nil {
		return err
	}
	return s.loadSQLiteAPIKeys()
}

func (s *Store) migrateSQLite() error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS devices (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS device_tokens (
			device_id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL,
			created_at TEXT NOT NULL,
			last_used_at TEXT
		)`,
		`CREATE TABLE IF NOT EXISTS telemetry (
			id TEXT PRIMARY KEY,
			device_id TEXT NOT NULL,
			timestamp TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_telemetry_device_time ON telemetry(device_id, timestamp)`,
		`CREATE TABLE IF NOT EXISTS commands (
			id TEXT PRIMARY KEY,
			device_id TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_commands_device_created ON commands(device_id, created_at)`,
		`CREATE TABLE IF NOT EXISTS events (
			id TEXT PRIMARY KEY,
			device_id TEXT,
			type TEXT NOT NULL,
			at TEXT NOT NULL,
			data TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_events_at ON events(at)`,
		`CREATE TABLE IF NOT EXISTS firmware (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL,
			blob BLOB NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS rules (
			id TEXT PRIMARY KEY,
			data TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS telemetry_rollup (
			device_id TEXT NOT NULL,
			key TEXT NOT NULL,
			resolution INTEGER NOT NULL,
			bucket_start INTEGER NOT NULL,
			count INTEGER NOT NULL,
			min REAL NOT NULL,
			max REAL NOT NULL,
			sum REAL NOT NULL,
			last REAL NOT NULL,
			last_ts TEXT NOT NULL,
			PRIMARY KEY(device_id, key, resolution, bucket_start)
		)`,
		`CREATE TABLE IF NOT EXISTS api_keys (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			role TEXT NOT NULL,
			key_hash TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) loadSQLiteDevices() error {
	rows, err := s.db.Query("SELECT data FROM devices")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var device Device
		if err := json.Unmarshal([]byte(raw), &device); err != nil {
			return err
		}
		s.devices[device.ID] = device
	}
	return rows.Err()
}

func (s *Store) loadSQLiteTokens() error {
	rows, err := s.db.Query("SELECT device_id, token_hash, created_at, last_used_at FROM device_tokens")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var token DeviceToken
		var createdAt string
		var lastUsedAt sql.NullString
		if err := rows.Scan(&token.DeviceID, &token.TokenHash, &createdAt, &lastUsedAt); err != nil {
			return err
		}
		parsedCreatedAt, err := time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return err
		}
		token.CreatedAt = parsedCreatedAt
		if lastUsedAt.Valid {
			parsedLastUsedAt, err := time.Parse(time.RFC3339Nano, lastUsedAt.String)
			if err != nil {
				return err
			}
			token.LastUsedAt = &parsedLastUsedAt
		}
		s.tokens[token.DeviceID] = token
	}
	return rows.Err()
}

func (s *Store) loadSQLiteTelemetry() error {
	rows, err := s.db.Query("SELECT data FROM telemetry ORDER BY timestamp ASC")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var point TelemetryPoint
		if err := json.Unmarshal([]byte(raw), &point); err != nil {
			return err
		}
		s.telemetry[point.DeviceID] = append(s.telemetry[point.DeviceID], point)
	}
	return rows.Err()
}

func (s *Store) loadSQLiteCommands() error {
	rows, err := s.db.Query("SELECT data FROM commands")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var command Command
		if err := json.Unmarshal([]byte(raw), &command); err != nil {
			return err
		}
		s.commands[command.ID] = command
	}
	return rows.Err()
}

func (s *Store) loadSQLiteEvents() error {
	rows, err := s.db.Query("SELECT data FROM events ORDER BY at ASC")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var event Event
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return err
		}
		s.events = append(s.events, event)
	}
	return rows.Err()
}

// --- incremental SQLite persistence (replaces the old full-table rewrite) ---
//
// Each mutation persists only the rows it touched. This removes the previous
// "DELETE FROM <all tables> + re-INSERT everything" cost that ran on every
// single heartbeat/telemetry/command write, which was the main scalability
// bottleneck (especially for high-frequency GPS telemetry).

func (s *Store) persistDeviceLocked(d Device) error {
	if s.storage != "sqlite" {
		return nil
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO devices(id, data) VALUES(?, ?)
		 ON CONFLICT(id) DO UPDATE SET data=excluded.data`,
		d.ID, string(raw))
	return err
}

func (s *Store) persistTokenLocked(t DeviceToken) error {
	if s.storage != "sqlite" {
		return nil
	}
	var lastUsed any
	if t.LastUsedAt != nil {
		lastUsed = t.LastUsedAt.Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(
		`INSERT INTO device_tokens(device_id, token_hash, created_at, last_used_at) VALUES(?, ?, ?, ?)
		 ON CONFLICT(device_id) DO UPDATE SET token_hash=excluded.token_hash, created_at=excluded.created_at, last_used_at=excluded.last_used_at`,
		t.DeviceID, t.TokenHash, t.CreatedAt.Format(time.RFC3339Nano), lastUsed)
	return err
}

func (s *Store) persistCommandLocked(c Command) error {
	if s.storage != "sqlite" {
		return nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO commands(id, device_id, status, created_at, data) VALUES(?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET status=excluded.status, data=excluded.data`,
		c.ID, c.DeviceID, string(c.Status), c.CreatedAt.Format(time.RFC3339Nano), string(raw))
	return err
}

func (s *Store) persistTelemetryLocked(p TelemetryPoint, prune bool) error {
	if s.storage != "sqlite" {
		return nil
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO telemetry(id, device_id, timestamp, data) VALUES(?, ?, ?, ?)`,
		p.ID, p.DeviceID, p.Timestamp.Format(time.RFC3339Nano), string(raw)); err != nil {
		return err
	}
	if !prune {
		return nil
	}
	// Rolling cleanup: keep only the newest telemetryPerDevice rows per device.
	// Ordering by id (which encodes the creation UnixNano, fixed width) is a
	// deterministic monotonic key, unlike RFC3339Nano timestamp strings.
	_, err = s.db.Exec(
		`DELETE FROM telemetry WHERE device_id=? AND id NOT IN (
			SELECT id FROM telemetry WHERE device_id=? ORDER BY id DESC LIMIT ?)`,
		p.DeviceID, p.DeviceID, telemetryPerDevice)
	return err
}

func (s *Store) persistEventLocked(e Event, prune bool) error {
	if s.storage != "sqlite" {
		return nil
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(
		`INSERT INTO events(id, device_id, type, at, data) VALUES(?, ?, ?, ?, ?)`,
		e.ID, e.DeviceID, e.Type, e.At.Format(time.RFC3339Nano), string(raw)); err != nil {
		return err
	}
	if !prune {
		return nil
	}
	// Rolling cleanup: keep only the newest eventBacklog events globally.
	// id encodes the creation UnixNano (fixed width) -> deterministic ordering.
	_, err = s.db.Exec(
		`DELETE FROM events WHERE id NOT IN (
			SELECT id FROM events ORDER BY id DESC LIMIT ?)`,
		eventBacklog)
	return err
}

func stateFor(now, lastSeen time.Time) OnlineState {
	age := now.Sub(lastSeen)
	if age <= staleAfter {
		return OnlineStateOnline
	}
	if age <= offlineAfter {
		return OnlineStateStale
	}
	return OnlineStateOffline
}

func isExpired(now time.Time, cmd Command) bool {
	return cmd.ExpiresAt != nil && now.After(*cmd.ExpiresAt) && (cmd.Status == CommandStatusQueued || cmd.Status == CommandStatusDelivered)
}

func newID(prefix string, t time.Time) string {
	return fmt.Sprintf("%s-%d", prefix, t.UnixNano())
}

func generateToken() (string, error) {
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return "lgw_" + hex.EncodeToString(bytes), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (s *Store) applyTokenMetadataLocked(device *Device) {
	token, ok := s.tokens[device.ID]
	if !ok {
		device.TokenIssuedAt = nil
		device.TokenLastUsedAt = nil
		return
	}
	issuedAt := token.CreatedAt
	device.TokenIssuedAt = &issuedAt
	device.TokenLastUsedAt = token.LastUsedAt
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneCapabilities(in []Capability) []Capability {
	if in == nil {
		return nil
	}
	out := make([]Capability, len(in))
	copy(out, in)
	return out
}

func appendBounded[T any](items []T, item T, limit int) []T {
	items = append(items, item)
	if limit > 0 && len(items) > limit {
		return items[len(items)-limit:]
	}
	return items
}
