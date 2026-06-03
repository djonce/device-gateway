package auth

import (
	"testing"
	"time"
)

func TestRoleAllows(t *testing.T) {
	if !RoleAdmin.Allows(RoleOperator) || !RoleAdmin.Allows(RoleViewer) || !RoleAdmin.Allows(RoleAdmin) {
		t.Fatal("admin should allow all roles")
	}
	if !RoleOperator.Allows(RoleViewer) || RoleOperator.Allows(RoleAdmin) {
		t.Fatal("operator should allow viewer but not admin")
	}
	if RoleViewer.Allows(RoleOperator) {
		t.Fatal("viewer should not allow operator")
	}
	if Role("bogus").Allows(RoleViewer) {
		t.Fatal("unknown role should allow nothing")
	}
	if !ValidRole("operator") || ValidRole("root") {
		t.Fatal("ValidRole mismatch")
	}
}

func TestDisabledWhenNoPassword(t *testing.T) {
	a := New("admin", "")
	if a.Enabled() {
		t.Fatal("expected auth disabled without password")
	}
	if _, _, ok := a.Login("admin", "x"); ok {
		t.Fatal("login should fail when disabled")
	}
	// No sessions can exist when disabled; open-mode access is an API-layer concern.
	if _, ok := a.Validate("anything"); ok {
		t.Fatal("validate should fail when disabled")
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
	role, ok := a.Validate(token)
	if !ok || role != RoleAdmin {
		t.Fatalf("expected admin session, got role=%q ok=%v", role, ok)
	}
	if _, ok := a.Validate("bogus"); ok {
		t.Fatal("expected bogus token to fail")
	}

	a.Logout(token)
	if _, ok := a.Validate(token); ok {
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
	if _, ok := a.Validate(token); !ok {
		t.Fatal("token should be valid initially")
	}
	cur = base.Add(25 * time.Hour)
	if _, ok := a.Validate(token); ok {
		t.Fatal("token should be expired")
	}
}
