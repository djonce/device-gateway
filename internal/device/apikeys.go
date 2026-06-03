package device

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"light-gateway/internal/auth"
)

// APIKey is a long-lived, revocable credential for programmatic access, bound
// to a role. Only its hash is stored; the plaintext is returned once at
// creation. The hash is never serialized to clients (json:"-").
type APIKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	KeyHash   string    `json:"-"`
	CreatedAt time.Time `json:"createdAt"`
}

// CreateAPIKey mints a new API key. Returns the stored metadata plus the
// one-time plaintext key (prefixed "lgwk_").
func (s *Store) CreateAPIKey(name, role string) (APIKey, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return APIKey{}, "", fmt.Errorf("%w: api key name is required", ErrBadRequest)
	}
	if !auth.ValidRole(role) {
		return APIKey{}, "", fmt.Errorf("%w: role must be viewer, operator or admin", ErrBadRequest)
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return APIKey{}, "", err
	}
	plaintext := "lgwk_" + hex.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	key := APIKey{ID: newID("key", now), Name: name, Role: role, KeyHash: hashToken(plaintext), CreatedAt: now}
	s.apiKeys[key.ID] = key
	s.appendEventLocked("apikey.created", "", "api key created", map[string]any{"keyId": key.ID, "name": name, "role": role})
	if err := s.persistAPIKeyLocked(key); err != nil {
		return APIKey{}, "", err
	}
	return key, plaintext, nil
}

// ListAPIKeys returns key metadata (no hashes), newest first.
func (s *Store) ListAPIKeys() []APIKey {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]APIKey, 0, len(s.apiKeys))
	for _, k := range s.apiKeys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// DeleteAPIKey revokes a key.
func (s *Store) DeleteAPIKey(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.apiKeys[id]; !ok {
		return ErrNotFound
	}
	delete(s.apiKeys, id)
	s.appendEventLocked("apikey.deleted", "", "api key deleted", map[string]any{"keyId": id})
	if s.storage == "sqlite" {
		if _, err := s.db.Exec("DELETE FROM api_keys WHERE id=?", id); err != nil {
			return err
		}
	}
	return nil
}

// VerifyAPIKey returns the role bound to a plaintext API key, if valid.
func (s *Store) VerifyAPIKey(key string) (string, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	h := hashToken(key)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, k := range s.apiKeys {
		if subtle.ConstantTimeCompare([]byte(k.KeyHash), []byte(h)) == 1 {
			return k.Role, true
		}
	}
	return "", false
}

func (s *Store) persistAPIKeyLocked(k APIKey) error {
	if s.storage != "sqlite" {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO api_keys(id, name, role, key_hash, created_at) VALUES(?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name=excluded.name, role=excluded.role, key_hash=excluded.key_hash`,
		k.ID, k.Name, k.Role, k.KeyHash, k.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) loadSQLiteAPIKeys() error {
	rows, err := s.db.Query("SELECT id, name, role, key_hash, created_at FROM api_keys")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k APIKey
		var createdAt string
		if err := rows.Scan(&k.ID, &k.Name, &k.Role, &k.KeyHash, &createdAt); err != nil {
			return err
		}
		if t, err := time.Parse(time.RFC3339Nano, createdAt); err == nil {
			k.CreatedAt = t
		}
		s.apiKeys[k.ID] = k
	}
	return rows.Err()
}
