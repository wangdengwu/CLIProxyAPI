package auth

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/claude"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func TestExtractAccessToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata map[string]any
		expected string
	}{
		{
			"antigravity top-level access_token",
			map[string]any{"access_token": "tok-abc"},
			"tok-abc",
		},
		{
			"gemini nested token.access_token",
			map[string]any{
				"token": map[string]any{"access_token": "tok-nested"},
			},
			"tok-nested",
		},
		{
			"top-level takes precedence over nested",
			map[string]any{
				"access_token": "tok-top",
				"token":        map[string]any{"access_token": "tok-nested"},
			},
			"tok-top",
		},
		{
			"empty metadata",
			map[string]any{},
			"",
		},
		{
			"whitespace-only access_token",
			map[string]any{"access_token": "   "},
			"",
		},
		{
			"wrong type access_token",
			map[string]any{"access_token": 12345},
			"",
		},
		{
			"token is not a map",
			map[string]any{"token": "not-a-map"},
			"",
		},
		{
			"nested whitespace-only",
			map[string]any{
				"token": map[string]any{"access_token": "  "},
			},
			"",
		},
		{
			"fallback to nested when top-level empty",
			map[string]any{
				"access_token": "",
				"token":        map[string]any{"access_token": "tok-fallback"},
			},
			"tok-fallback",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractAccessToken(tt.metadata)
			if got != tt.expected {
				t.Errorf("extractAccessToken() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// rebindSave runs the storage branch of Save the way a login does: a freshly
// built ClaudeTokenStorage carrying new credentials, aimed at path. It returns
// the JSON that actually landed on disk.
func rebindSave(t *testing.T, path string, recordMeta map[string]any) map[string]any {
	t.Helper()

	storage := &claudeauth.ClaudeTokenStorage{
		AccessToken:  "FRESH-access",
		RefreshToken: "FRESH-refresh",
		Email:        "a@b.c",
		Type:         "claude",
		Expire:       "2026-08-18T00:00:00Z",
		LastRefresh:  "2026-08-17T00:00:00Z",
	}
	record := &cliproxyauth.Auth{
		ID:         "claude-a@b.c.json",
		FileName:   "claude-a@b.c.json",
		Storage:    storage,
		Metadata:   recordMeta,
		Attributes: map[string]string{"path": path},
	}

	saved, err := NewFileTokenStore().Save(context.Background(), record)
	if err != nil {
		t.Fatalf("Save() error = %v, want nil", err)
	}
	if saved != path {
		t.Fatalf("Save() path = %q, want %q", saved, path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	var got map[string]any
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("saved file is not a JSON object: %v\n%s", err, raw)
	}
	return got
}

// End-to-end wiring check for the storage branch: the helper's merge semantics
// are proven in internal/misc, but only this proves the helper is actually on the
// save path and that what it injects reaches the file. Re-binding a Claude
// account must land the new credentials and keep the operator's own keys.
func TestFileTokenStore_SaveStorageBranchPreservesOperatorKeys(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "claude-a@b.c.json")
	old := `{
		"access_token": "STALE-access",
		"refresh_token": "STALE-refresh",
		"id_token": "STALE-id",
		"expired": "2020-01-01T00:00:00Z",
		"last_refresh": "2020-01-01T00:00:00Z",
		"email": "a@b.c",
		"type": "claude",
		"claude_usage_mode": "dedicated",
		"priority": 5,
		"note": "lab dedicated account",
		"prefix": "lab",
		"proxy_url": "socks5://10.0.0.1:1080",
		"headers": {"anthropic-beta": "oauth-2025-04-20"},
		"excluded_models": ["claude-opus-4"],
		"disabled": true
	}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatalf("write old auth file: %v", err)
	}

	got := rebindSave(t, path, map[string]any{"email": "a@b.c"})

	// The credentials on disk are the new ones. A re-bind exists to replace dead
	// tokens; writing the old ones back would be worse than not preserving at all.
	for key, want := range map[string]string{
		"access_token":  "FRESH-access",
		"refresh_token": "FRESH-refresh",
		"expired":       "2026-08-18T00:00:00Z",
		"last_refresh":  "2026-08-17T00:00:00Z",
		"id_token":      "",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %q", key, got[key], want)
		}
	}

	// The operator's keys came along.
	if got["claude_usage_mode"] != "dedicated" {
		t.Errorf("claude_usage_mode = %v, want %q — the account would fall back to the shared policy", got["claude_usage_mode"], "dedicated")
	}
	if got["note"] != "lab dedicated account" {
		t.Errorf("note = %v, want the operator note", got["note"])
	}
	if got["prefix"] != "lab" {
		t.Errorf("prefix = %v, want %q", got["prefix"], "lab")
	}
	if got["proxy_url"] != "socks5://10.0.0.1:1080" {
		t.Errorf("proxy_url = %v, want the operator proxy", got["proxy_url"])
	}
	if priority, err := json.Marshal(got["priority"]); err != nil || string(priority) != "5" {
		t.Errorf("priority = %s (err %v), want 5", priority, err)
	}
	headers, ok := got["headers"].(map[string]any)
	if !ok || headers["anthropic-beta"] != "oauth-2025-04-20" {
		t.Errorf("headers = %v, want the operator header map", got["headers"])
	}
	excluded, ok := got["excluded_models"].([]any)
	if !ok || len(excluded) != 1 || excluded[0] != "claude-opus-4" {
		t.Errorf("excluded_models = %v, want [claude-opus-4]", got["excluded_models"])
	}

	// Re-binding a disabled account is how an operator re-enables it, so the flag
	// must not survive. The auth-file reader treats its absence as enabled.
	if _, present := got["disabled"]; present {
		t.Errorf("disabled key survived the re-bind: %v — the account would stay switched off", got["disabled"])
	}
}

// First bind: nothing on disk to inherit, so the written file must be exactly
// what it was before preservation existed — the fresh payload and nothing else.
func TestFileTokenStore_SaveStorageBranchFirstBindHasNoSideEffects(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "claude-a@b.c.json")
	got := rebindSave(t, path, map[string]any{"email": "a@b.c"})

	want := map[string]any{
		"access_token":  "FRESH-access",
		"refresh_token": "FRESH-refresh",
		"id_token":      "",
		"email":         "a@b.c",
		"type":          "claude",
		"expired":       "2026-08-18T00:00:00Z",
		"last_refresh":  "2026-08-17T00:00:00Z",
	}
	if len(got) != len(want) {
		t.Errorf("saved %d keys, want %d\n got: %v\nwant: %v", len(got), len(want), got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
}

// A corrupt auth file must not lock the operator out of re-binding: the save
// still succeeds and writes the fresh payload, it just has nothing to preserve.
func TestFileTokenStore_SaveStorageBranchSurvivesCorruptOldFile(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		old  string
	}{
		{"truncated JSON", `{"access_token": "STALE", "claude_usage_mode":`},
		{"empty file", ``},
		{"not an object", `["claude_usage_mode", "dedicated"]`},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "claude-a@b.c.json")
			if err := os.WriteFile(path, []byte(tc.old), 0o600); err != nil {
				t.Fatalf("write old auth file: %v", err)
			}

			got := rebindSave(t, path, map[string]any{"email": "a@b.c"})
			if got["access_token"] != "FRESH-access" {
				t.Errorf("access_token = %v, want %q", got["access_token"], "FRESH-access")
			}
		})
	}
}
