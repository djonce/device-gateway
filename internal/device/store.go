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
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

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
)

type Store struct {
	mu        sync.RWMutex
	path      string
	storage   string
	db        *sql.DB
	devices   map[string]Device
	tokens    map[string]DeviceToken
	telemetry map[string][]TelemetryPoint
	commands  map[string]Command
	events    []Event
	now       func() time.Time
}

type snapshot struct {
	Devices   map[string]Device           `json:"devices"`
	Tokens    map[string]DeviceToken      `json:"tokens"`
	Telemetry map[string][]TelemetryPoint `json:"telemetry"`
	Commands  map[string]Command          `json:"commands"`
	Events    []Event                     `json:"events"`
}

func NewStore(path string) (*Store, error) {
	s := &Store{
		path:      path,
		storage:   "json",
		devices:   map[string]Device{},
		tokens:    map[string]DeviceToken{},
		telemetry: map[string][]TelemetryPoint{},
		commands:  map[string]Command{},
		events:    []Event{},
		now:       time.Now,
	}
	if path == "" {
		return s, nil
	}
	if strings.HasSuffix(strings.ToLower(path), ".db") || strings.HasSuffix(strings.ToLower(path), ".sqlite") {
		s.storage = "sqlite"
	}
	if err := s.load(); err != nil {
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
	}
	s.applyTokenMetadataLocked(&device)
	s.devices[device.ID] = device
	s.appendEventLocked("device.registered", device.ID, "device registered", map[string]any{"name": device.Name, "type": device.Type})
	return DeviceRegistration{Device: device, Token: rawToken, Reused: ok}, s.persistLocked()
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
	return device, s.persistLocked()
}

func (s *Store) ListDevices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Device, 0, len(s.devices))
	for id, device := range s.devices {
		device.State = stateFor(s.now().UTC(), device.LastSeenAt)
		s.applyTokenMetadataLocked(&device)
		s.devices[id] = device
		out = append(out, device)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].LastSeenAt.After(out[j].LastSeenAt)
	})
	return out
}

func (s *Store) GetDevice(id string) (Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	device, ok := s.devices[id]
	if !ok {
		return Device{}, ErrNotFound
	}
	device.State = stateFor(s.now().UTC(), device.LastSeenAt)
	s.applyTokenMetadataLocked(&device)
	s.devices[id] = device
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
	device.TokenLastUsedAt = &now
	s.devices[deviceID] = device
	return s.persistLocked()
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
	return DeviceTokenReset{DeviceID: deviceID, Token: token, IssuedAt: now}, s.persistLocked()
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
	return device, s.persistLocked()
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
	s.telemetry[deviceID] = appendBounded(s.telemetry[deviceID], point, 200)
	s.appendEventLocked("telemetry.received", deviceID, "telemetry received", map[string]any{"key": point.Key, "value": point.Value})
	return point, s.persistLocked()
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
	if _, ok := s.devices[deviceID]; !ok {
		return Command{}, ErrNotFound
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
	return cmd, s.persistLocked()
}

func (s *Store) ListCommands(deviceID string, limit int) ([]Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[deviceID]; !ok {
		return nil, ErrNotFound
	}
	now := s.now().UTC()
	out := []Command{}
	changed := false
	for id, cmd := range s.commands {
		if cmd.DeviceID != deviceID {
			continue
		}
		if isExpired(now, cmd) {
			cmd.Status = CommandStatusExpired
			finished := now
			cmd.FinishedAt = &finished
			s.commands[id] = cmd
			changed = true
		}
		out = append(out, cmd)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	if changed {
		_ = s.persistLocked()
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
		return Command{}, false, s.persistLocked()
	}
	delivered := now
	selected.Status = CommandStatusDelivered
	selected.DeliveredAt = &delivered
	s.commands[selected.ID] = *selected
	s.appendEventLocked("command.delivered", deviceID, "command delivered", map[string]any{"commandId": selected.ID, "type": selected.Type})
	return *selected, true, s.persistLocked()
}

func (s *Store) AckCommand(commandID string, req AckCommandRequest) (Command, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cmd, ok := s.commands[commandID]
	if !ok {
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
	return cmd, s.persistLocked()
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
	return cmd, s.persistLocked()
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

func (s *Store) appendEventLocked(eventType, deviceID, message string, metadata map[string]any) {
	now := s.now().UTC()
	event := Event{
		ID:       newID("evt", now),
		Type:     eventType,
		DeviceID: deviceID,
		Message:  message,
		At:       now,
		Metadata: cloneAnyMap(metadata),
	}
	s.events = appendBounded(s.events, event, 500)
}

func (s *Store) load() error {
	if s.storage == "sqlite" {
		return s.loadSQLite()
	}
	bytes, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snap snapshot
	if err := json.Unmarshal(bytes, &snap); err != nil {
		return err
	}
	if snap.Devices != nil {
		s.devices = snap.Devices
	}
	if snap.Tokens != nil {
		s.tokens = snap.Tokens
	}
	if snap.Telemetry != nil {
		s.telemetry = snap.Telemetry
	}
	if snap.Commands != nil {
		s.commands = snap.Commands
	}
	if snap.Events != nil {
		s.events = snap.Events
	}
	return nil
}

func (s *Store) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if s.storage == "sqlite" {
		return s.persistSQLiteLocked()
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	snap := snapshot{
		Devices:   s.devices,
		Tokens:    s.tokens,
		Telemetry: s.telemetry,
		Commands:  s.commands,
		Events:    s.events,
	}
	bytes, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, bytes, 0o644)
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
	return s.loadSQLiteEvents()
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

func (s *Store) persistSQLiteLocked() error {
	if s.db == nil {
		return ErrNotFound
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		"DELETE FROM devices",
		"DELETE FROM device_tokens",
		"DELETE FROM telemetry",
		"DELETE FROM commands",
		"DELETE FROM events",
	} {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	for _, device := range s.devices {
		raw, err := json.Marshal(device)
		if err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT INTO devices(id, data) VALUES(?, ?)", device.ID, string(raw)); err != nil {
			return err
		}
	}
	for _, token := range s.tokens {
		var lastUsed any
		if token.LastUsedAt != nil {
			lastUsed = token.LastUsedAt.Format(time.RFC3339Nano)
		}
		if _, err := tx.Exec(
			"INSERT INTO device_tokens(device_id, token_hash, created_at, last_used_at) VALUES(?, ?, ?, ?)",
			token.DeviceID,
			token.TokenHash,
			token.CreatedAt.Format(time.RFC3339Nano),
			lastUsed,
		); err != nil {
			return err
		}
	}
	for _, points := range s.telemetry {
		for _, point := range points {
			raw, err := json.Marshal(point)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(
				"INSERT INTO telemetry(id, device_id, timestamp, data) VALUES(?, ?, ?, ?)",
				point.ID,
				point.DeviceID,
				point.Timestamp.Format(time.RFC3339Nano),
				string(raw),
			); err != nil {
				return err
			}
		}
	}
	for _, command := range s.commands {
		raw, err := json.Marshal(command)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			"INSERT INTO commands(id, device_id, status, created_at, data) VALUES(?, ?, ?, ?, ?)",
			command.ID,
			command.DeviceID,
			string(command.Status),
			command.CreatedAt.Format(time.RFC3339Nano),
			string(raw),
		); err != nil {
			return err
		}
	}
	for _, event := range s.events {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(
			"INSERT INTO events(id, device_id, type, at, data) VALUES(?, ?, ?, ?, ?)",
			event.ID,
			event.DeviceID,
			event.Type,
			event.At.Format(time.RFC3339Nano),
			string(raw),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
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
