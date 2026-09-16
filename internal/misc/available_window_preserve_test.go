package misc

import "testing"

// available_window is an operator-set key: it expresses when the account's owner is
// willing to share it, and a re-login must not silently erase that intent. The
// preserve mechanism is a pure set difference (old file keys minus the keys the fresh
// payload owns), not an allowlist, so the field needs no registration — this test
// pins that property rather than assuming it, because the failure mode is silent:
// the window vanishes and the account quietly goes back to 24/7.
func TestApplyPreservedMetadata_CarriesAvailableWindowAcrossRebind(t *testing.T) {
	path := writeAuthFile(t, `{
		"access_token": "old-token",
		"email": "a@b.c",
		"type": "claude",
		"available_window": "18:00-09:00",
		"claude_usage_mode": "dedicated"
	}`)

	storage := &fakeStorage{
		AccessToken: "fresh-token",
		Email:       "a@b.c",
		Type:        "claude",
	}

	ApplyPreservedMetadata(path, storage, map[string]any{"email": "a@b.c"})

	injected, calls := storage.seen()
	if calls != 1 {
		t.Fatalf("SetMetadata called %d times, want 1", calls)
	}
	if got := injected["available_window"]; got != "18:00-09:00" {
		t.Errorf("available_window = %v, want %q", got, "18:00-09:00")
	}
	// The rotated credential must still win over the stale one it replaces.
	if got, exists := injected["access_token"]; exists {
		t.Errorf("access_token = %v resurrected from the old file, want it owned by the fresh payload", got)
	}
}

// An account that never declared a window must not gain one from anywhere.
func TestApplyPreservedMetadata_NoAvailableWindowStaysAbsent(t *testing.T) {
	path := writeAuthFile(t, `{
		"access_token": "old-token",
		"email": "a@b.c",
		"type": "claude"
	}`)

	storage := &fakeStorage{AccessToken: "fresh-token", Email: "a@b.c", Type: "claude"}
	ApplyPreservedMetadata(path, storage, map[string]any{"email": "a@b.c"})

	injected, _ := storage.seen()
	if got, exists := injected["available_window"]; exists {
		t.Errorf("available_window = %v, want absent", got)
	}
}
