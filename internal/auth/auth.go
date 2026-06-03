// Package auth provides admin authentication and role vocabulary for the
// management API.
//
// A single admin account is configured from the environment (no user database);
// logging in issues a short-lived session token bound to the admin role. Roles
// (viewer < operator < admin) are also used by API keys (stored by the device
// package) to give scoped, long-lived programmatic access. If no password is
// configured the gateway runs in "open mode" — that gate is enforced in the API
// layer, not here.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

// Role is an access level. Higher roles include lower-role permissions.
type Role string

const (
	RoleViewer   Role = "viewer"
	RoleOperator Role = "operator"
	RoleAdmin    Role = "admin"
)

func (r Role) rank() int {
	switch r {
	case RoleViewer:
		return 1
	case RoleOperator:
		return 2
	case RoleAdmin:
		return 3
	}
	return 0
}

// Allows reports whether this role satisfies a required minimum role.
func (r Role) Allows(min Role) bool {
	return r.rank() > 0 && r.rank() >= min.rank()
}

// ValidRole reports whether s names a known role.
func ValidRole(s string) bool {
	switch Role(s) {
	case RoleViewer, RoleOperator, RoleAdmin:
		return true
	}
	return false
}

type session struct {
	role      Role
	expiresAt time.Time
}

// Authenticator manages the admin account and active sessions.
type Authenticator struct {
	user     string
	password string
	enabled  bool
	ttl      time.Duration
	now      func() time.Time

	mu       sync.Mutex
	sessions map[string]session // sha256(token) -> session
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
		sessions: map[string]session{},
	}
}

// Enabled reports whether admin authentication is enforced.
func (a *Authenticator) Enabled() bool { return a.enabled }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Login validates the admin account (constant time) and returns an admin
// session token.
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
	a.sessions[hashToken(token)] = session{role: RoleAdmin, expiresAt: expiresAt}
	a.pruneLocked()
	a.mu.Unlock()
	return token, expiresAt, true
}

// Validate returns the role bound to a valid session token.
func (a *Authenticator) Validate(token string) (Role, bool) {
	if token == "" {
		return "", false
	}
	key := hashToken(token)
	a.mu.Lock()
	defer a.mu.Unlock()
	s, ok := a.sessions[key]
	if !ok {
		return "", false
	}
	if a.now().After(s.expiresAt) {
		delete(a.sessions, key)
		return "", false
	}
	return s.role, true
}

// Logout invalidates a session token.
func (a *Authenticator) Logout(token string) {
	a.mu.Lock()
	delete(a.sessions, hashToken(token))
	a.mu.Unlock()
}

func (a *Authenticator) pruneLocked() {
	now := a.now()
	for key, s := range a.sessions {
		if now.After(s.expiresAt) {
			delete(a.sessions, key)
		}
	}
}
