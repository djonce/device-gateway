package device

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAPIKeyCreateVerifyDelete(t *testing.T) {
	store, _ := NewStore("")

	if _, _, err := store.CreateAPIKey("k", "root"); err == nil {
		t.Fatal("expected invalid role to be rejected")
	}
	if _, _, err := store.CreateAPIKey("", "viewer"); err == nil {
		t.Fatal("expected empty name to be rejected")
	}

	key, plaintext, err := store.CreateAPIKey("dashboard", "viewer")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(plaintext, "lgwk_") {
		t.Fatalf("expected lgwk_ prefix, got %q", plaintext)
	}

	role, ok := store.VerifyAPIKey(plaintext)
	if !ok || role != "viewer" {
		t.Fatalf("verify: role=%q ok=%v", role, ok)
	}
	if _, ok := store.VerifyAPIKey("lgwk_wrong"); ok {
		t.Fatal("expected wrong key to fail")
	}

	// The hash must never be serialized to clients.
	blob, _ := json.Marshal(store.ListAPIKeys()[0])
	if strings.Contains(string(blob), key.KeyHash) || strings.Contains(strings.ToLower(string(blob)), "hash") {
		t.Fatalf("api key JSON leaks the hash: %s", blob)
	}

	if err := store.DeleteAPIKey(key.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok := store.VerifyAPIKey(plaintext); ok {
		t.Fatal("expected key invalid after delete")
	}
}
