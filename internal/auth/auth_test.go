package auth

import (
	"testing"
	"time"
)

func TestDisabledWhenNoPassword(t *testing.T) {
	a := New("admin", "")
	if a.Enabled() {
		t.Fatal("expected auth disabled without password")
	}
	// Open mode: any token validates, but Login is refused.
	if !a.Validate("anything") {
		t.Fatal("open mode should validate any token")
	}
	if _, _, ok := a.Login("admin", "x"); ok {
		t.Fatal("login should fail when disabled")
	}
}

func TestLoginValidateLogout(t *testing.T) {
	a := New("ops", "s3cret")
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	cur := base
	a.now = func() time.Time { return cur }

	if _, _, ok := a.Login("ops", "wrong"); ok {
		t.Fatal("expected wrong password to fail")
	}
	if _, _, ok := a.Login("nope", "s3cret"); ok {
		t.Fatal("expected wrong user to fail")
	}

	token, exp, ok := a.Login("ops", "s3cret")
	if !ok || token == "" {
		t.Fatal("expected successful login")
	}
	if !exp.After(base) {
		t.Fatal("expected future expiry")
	}
	if !a.Validate(token) {
		t.Fatal("expected token to validate")
	}
	if a.Validate("bogus") {
		t.Fatal("expected bogus token to fail")
	}

	a.Logout(token)
	if a.Validate(token) {
		t.Fatal("expected token invalid after logout")
	}
}

func TestSessionExpiry(t *testing.T) {
	a := New("admin", "pw")
	base := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)
	cur := base
	a.now = func() time.Time { return cur }

	token, _, ok := a.Login("admin", "pw")
	if !ok {
		t.Fatal("login failed")
	}
	if !a.Validate(token) {
		t.Fatal("token should be valid initially")
	}
	cur = base.Add(25 * time.Hour) // past the 24h TTL
	if a.Validate(token) {
		t.Fatal("token should be expired")
	}
}
