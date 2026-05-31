// Package auth provides simple admin authentication for the management API.
//
// A single admin account is configured from the environment (no user database).
// Login compares credentials in constant time and issues a random session token;
// only the token's SHA-256 hash is kept in memory, with an expiry. If no
// password is configured the authenticator runs in "open mode" (auth disabled)
// for local development — the gateway logs a warning in that case.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

// Authenticator manages the admin account and active sessions.
type Authenticator struct {
	user     string
	password string
	enabled  bool
	ttl      time.Duration
	now      func() time.Time

	mu       sync.Mutex
	sessions map[string]time.Time // sha256(token) -> expiry
}

// New builds an authenticator. Auth is enabled only when a password is set.
// An empty user defaults to "admin".
func New(user, password string) *Authenticator {
	if user == "" {
		user = "admin"
	}
	return &Authenticator{
		user:     user,
		password: password,
		enabled:  password != "",
		ttl:      24 * time.Hour,
		now:      time.Now,
		sessions: map[string]time.Time{},
	}
}

// Enabled reports whether admin authentication is enforced.
func (a *Authenticator) Enabled() bool { return a.enabled }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Login validates credentials (constant time) and returns a new session token.
func (a *Authenticator) Login(user, password string) (string, time.Time, bool) {
	if !a.enabled {
		return "", time.Time{}, false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.user)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(password), []byte(a.password)) == 1
	if !userOK || !passOK {
		return "", time.Time{}, false
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, false
	}
	token := hex.EncodeToString(raw)
	expiresAt := a.now().Add(a.ttl)

	a.mu.Lock()
	a.sessions[hashToken(token)] = expiresAt
	a.pruneLocked()
	a.mu.Unlock()
	return token, expiresAt, true
}

// Validate reports whether a session token is currently valid. In open mode
// (auth disabled) it always returns true.
func (a *Authenticator) Validate(token string) bool {
	if !a.enabled {
		return true
	}
	if token == "" {
		return false
	}
	key := hashToken(token)
	a.mu.Lock()
	defer a.mu.Unlock()
	expiresAt, ok := a.sessions[key]
	if !ok {
		return false
	}
	if a.now().After(expiresAt) {
		delete(a.sessions, key)
		return false
	}
	return true
}

// Logout invalidates a session token.
func (a *Authenticator) Logout(token string) {
	a.mu.Lock()
	delete(a.sessions, hashToken(token))
	a.mu.Unlock()
}

func (a *Authenticator) pruneLocked() {
	now := a.now()
	for key, expiresAt := range a.sessions {
		if now.After(expiresAt) {
			delete(a.sessions, key)
		}
	}
}
