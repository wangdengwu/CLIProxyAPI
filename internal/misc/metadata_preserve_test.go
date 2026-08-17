package misc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// recorder captures whatever the helper injects so a test can assert on it
// without going near a real file write.
type recorder struct {
	injected    map[string]any
	injectCalls int
}

func (r *recorder) SetMetadata(meta map[string]any) {
	r.injected = meta
	r.injectCalls++
}

func (r *recorder) SaveTokenToFile(string) error { return nil }

func (r *recorder) seen() (map[string]any, int) { return r.injected, r.injectCalls }

// injectProbe is what the table below stores: a storage the helper can inject
// into, plus a way to read back what it got.
type injectProbe interface {
	SetMetadata(map[string]any)
	seen() (map[string]any, int)
}

// fakeStorage stands in for a claude-shaped token storage: a flat set of exported
// fields that together form the payload a login is about to write.
type fakeStorage struct {
	recorder
	AccessToken string `json:"access_token"`
	Email       string `json:"email"`
	Type        string `json:"type"`
}

// nestedStorage stands in for a gemini-shaped storage, whose credential material
// sits inside one top-level object rather than beside it.
type nestedStorage struct {
	recorder
	Token     map[string]any `json:"token"`
	ProjectID string         `json:"project_id"`
	Type      string         `json:"type"`
}

// writeAuthFile drops a raw JSON auth file into a temp dir and returns its path.
func writeAuthFile(t *testing.T, raw string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-a@b.c.json")
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// missingAuthFile returns a path inside a fresh temp dir with nothing at it.
func missingAuthFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "claude-nobody@b.c.json")
}

// normalize round-trips a map through JSON so that a want literal written with
// Go ints compares equal to values the helper read back out of a file as
// float64. It compares shape and value, not the numeric type they arrived as.
func normalize(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	if m == nil {
		return nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("normalize: marshal: %v", err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("normalize: unmarshal: %v", err)
	}
	return out
}

// An existing auth file's operator-set keys survive a save that rewrites the file
// from a freshly built token storage. This is the whole point of the helper: those
// keys are configuration the operator applied, and a re-bind must not erase them.
func TestApplyPreservedMetadata_CarriesOperatorKeysForward(t *testing.T) {
	t.Parallel()

	path := writeAuthFile(t, `{
		"access_token": "old-token",
		"email": "a@b.c",
		"type": "claude",
		"claude_usage_mode": "dedicated",
		"priority": 5,
		"note": "dengwu personal max account"
	}`)

	storage := &fakeStorage{AccessToken: "new-token", Email: "a@b.c", Type: "claude"}
	recordMeta := map[string]any{"email": "a@b.c"}

	ApplyPreservedMetadata(path, storage, recordMeta)

	if storage.injected == nil {
		t.Fatal("expected metadata to be injected, got nil")
	}
	if got := storage.injected["claude_usage_mode"]; got != "dedicated" {
		t.Errorf("claude_usage_mode = %v, want %q", got, "dedicated")
	}
	if got := storage.injected["note"]; got != "dengwu personal max account" {
		t.Errorf("note = %v, want the operator note", got)
	}
	// priority arrives as a JSON number; compare through JSON so the test does not
	// over-specify which numeric Go type the helper carries it as.
	gotPriority, err := json.Marshal(storage.injected["priority"])
	if err != nil {
		t.Fatalf("marshal priority: %v", err)
	}
	if string(gotPriority) != "5" {
		t.Errorf("priority = %s, want 5", gotPriority)
	}
}

// Credential-rollback regression guard (PRD User Story 7). The merge the save
// performs lets metadata override the storage struct's own fields, so carrying an
// old file's credentials back would mean a re-bind that reports success and then
// writes the dead token it was supposed to replace. Nothing credential-shaped may
// ever appear in the injected map.
//
// This case stays on its own and must not be folded into the table: it is the one
// assertion that makes the whole feature safe to ship.
func TestApplyPreservedMetadata_NeverRollsBackCredentials(t *testing.T) {
	t.Parallel()

	path := writeAuthFile(t, `{
		"access_token": "STALE-access",
		"refresh_token": "STALE-refresh",
		"id_token": "STALE-id",
		"expired": "2020-01-01T00:00:00Z",
		"last_refresh": "2020-01-01T00:00:00Z",
		"email": "a@b.c",
		"type": "claude",
		"claude_usage_mode": "dedicated"
	}`)

	// The fresh payload defines every credential key, including id_token and the
	// timestamps, which arrive from the record metadata rather than the struct.
	// id_token is deliberately empty: real claude auth files carry the key with an
	// empty value, and "defined but empty" still means the new payload owns it.
	storage := &fakeStorage{AccessToken: "FRESH-access", Email: "a@b.c", Type: "claude"}
	recordMeta := map[string]any{
		"refresh_token": "FRESH-refresh",
		"id_token":      "",
		"expired":       "2026-08-18T00:00:00Z",
		"last_refresh":  "2026-08-17T00:00:00Z",
	}

	ApplyPreservedMetadata(path, storage, recordMeta)

	injected, calls := storage.seen()
	if calls != 1 {
		t.Fatalf("SetMetadata called %d times, want exactly 1", calls)
	}

	// Not one stale value may survive, under any key.
	for key, value := range injected {
		if str, ok := value.(string); ok && len(str) >= 5 && str[:5] == "STALE" {
			t.Errorf("stale credential leaked back: %s = %q", key, str)
		}
	}
	for key, want := range map[string]any{
		"refresh_token": "FRESH-refresh",
		"id_token":      "",
		"expired":       "2026-08-18T00:00:00Z",
		"last_refresh":  "2026-08-17T00:00:00Z",
	} {
		if got := injected[key]; got != want {
			t.Errorf("%s = %v, want %v", key, got, want)
		}
	}
	// access_token belongs to the storage struct, so it must not be present in the
	// injected metadata at all — if it were, it would override the struct field.
	if _, present := injected["access_token"]; present {
		t.Errorf("access_token must not be injected as metadata; it would shadow the fresh struct field")
	}
	// The operator key is still the reason we are here.
	if got := injected["claude_usage_mode"]; got != "dedicated" {
		t.Errorf("claude_usage_mode = %v, want %q", got, "dedicated")
	}
}

func TestApplyPreservedMetadata(t *testing.T) {
	t.Parallel()

	const noFile = "\x00missing"

	tests := []struct {
		name       string
		file       string // raw JSON to drop on disk, or noFile for "nothing there"
		storage    injectProbe
		recordMeta map[string]any
		want       map[string]any
	}{
		{
			// First bind: no history to inherit, so behavior is exactly what it was
			// before the helper existed.
			name:       "old file absent injects the record metadata unchanged",
			file:       noFile,
			storage:    &fakeStorage{AccessToken: "new-token", Email: "a@b.c", Type: "claude"},
			recordMeta: map[string]any{"email": "a@b.c", "expired": "2026-08-18T00:00:00Z"},
			want:       map[string]any{"email": "a@b.c", "expired": "2026-08-18T00:00:00Z"},
		},
		{
			// Re-binding a disabled account is how an operator re-enables it, so the
			// stale flag must die with the old file.
			name: "disabled flag is not carried forward",
			file: `{"access_token":"old","type":"claude","disabled":true,"note":"keep me"}`,
			storage: &fakeStorage{
				AccessToken: "new-token", Email: "a@b.c", Type: "claude",
			},
			recordMeta: map[string]any{"email": "a@b.c"},
			want:       map[string]any{"email": "a@b.c", "note": "keep me"},
		},
		{
			name:       "corrupt JSON degrades to the record metadata",
			file:       `{"access_token": "old", trailing garbage`,
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{"email": "a@b.c"},
			want:       map[string]any{"email": "a@b.c"},
		},
		{
			name:       "empty file degrades to the record metadata",
			file:       ``,
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{"email": "a@b.c"},
			want:       map[string]any{"email": "a@b.c"},
		},
		{
			name:       "whitespace-only file degrades to the record metadata",
			file:       "  \n\t ",
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{"email": "a@b.c"},
			want:       map[string]any{"email": "a@b.c"},
		},
		{
			// A JSON document that is valid but not an object has no top-level keys
			// to preserve, and must not be mistaken for one that does.
			name:       "non-object JSON degrades to the record metadata",
			file:       `["access_token", "old"]`,
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{"email": "a@b.c"},
			want:       map[string]any{"email": "a@b.c"},
		},
		{
			// The caller assembled its metadata from the live record; it is newer
			// than anything sitting on disk.
			name:       "record metadata wins over a colliding old key",
			file:       `{"type":"claude","note":"stale note","priority":1}`,
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{"note": "current note", "priority": 9},
			want:       map[string]any{"note": "current note", "priority": 9},
		},
		{
			// The auth-file reader accepts priority as either a number or a string.
			// Whichever the operator wrote is what must come back — no coercion.
			name:       "numeric priority is preserved as a number",
			file:       `{"type":"claude","priority":7}`,
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{},
			want:       map[string]any{"priority": 7},
		},
		{
			name:       "string priority is preserved as a string",
			file:       `{"type":"claude","priority":"7"}`,
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{},
			want:       map[string]any{"priority": "7"},
		},
		{
			// headers is a nested object and excluded_models an array; both are
			// operator intent and must pass through byte-for-byte.
			name: "structured operator values pass through unchanged",
			file: `{
				"type":"claude",
				"headers":{"anthropic-beta":"oauth-2025-04-20","x-tag":"lab"},
				"excluded_models":["claude-opus-4","claude-haiku-4-5"],
				"prefix":"lab",
				"proxy_url":"socks5://10.0.0.1:1080"
			}`,
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{},
			want: map[string]any{
				"headers":         map[string]any{"anthropic-beta": "oauth-2025-04-20", "x-tag": "lab"},
				"excluded_models": []any{"claude-opus-4", "claude-haiku-4-5"},
				"prefix":          "lab",
				"proxy_url":       "socks5://10.0.0.1:1080",
			},
		},
		{
			// gemini keeps its credentials inside a top-level "token" object. That
			// key belongs to the fresh payload as a whole; merging into it would
			// resurrect the old refresh token and expiry hiding inside.
			name: "nested credential object is owned entirely by the fresh payload",
			file: `{
				"token":{"access_token":"STALE","refresh_token":"STALE-r","expiry":"2020-01-01T00:00:00Z","scopes":["a","b"]},
				"project_id":"proj-1",
				"type":"gemini",
				"auto":true,
				"checked":true,
				"claude_usage_mode":"dedicated"
			}`,
			storage: &nestedStorage{
				Token:     map[string]any{"access_token": "FRESH"},
				ProjectID: "proj-1",
				Type:      "gemini",
			},
			recordMeta: map[string]any{},
			// token/project_id/type are in the fresh payload, so they are excluded
			// from the preserved set; auto/checked/claude_usage_mode are not.
			want: map[string]any{"auto": true, "checked": true, "claude_usage_mode": "dedicated"},
		},
		{
			// Keys left behind by an older schema are preserved rather than
			// curated. The auth-file reader ignores what it does not know, and
			// maintaining a drift-prone allowlist costs more than it saves.
			name:       "unknown legacy keys are preserved",
			file:       `{"type":"claude","some_retired_field":"junk"}`,
			storage:    &fakeStorage{AccessToken: "new-token", Type: "claude"},
			recordMeta: map[string]any{},
			want:       map[string]any{"some_retired_field": "junk"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := missingAuthFile(t)
			if tt.file != noFile {
				path = writeAuthFile(t, tt.file)
			}

			ApplyPreservedMetadata(path, tt.storage, tt.recordMeta)

			injected, calls := tt.storage.seen()
			if calls != 1 {
				t.Fatalf("SetMetadata called %d times, want exactly 1", calls)
			}
			if got, want := normalize(t, injected), normalize(t, tt.want); !reflect.DeepEqual(got, want) {
				t.Errorf("injected metadata =\n  %#v\nwant\n  %#v", got, want)
			}
		})
	}
}

// inertStorage is a token storage that cannot take injected metadata — vertex and
// the empty storage are shaped this way. The helper must leave it alone rather
// than fail the login.
type inertStorage struct {
	AccessToken string `json:"access_token"`
}

func TestApplyPreservedMetadata_StorageWithoutSetMetadata(t *testing.T) {
	t.Parallel()

	path := writeAuthFile(t, `{"access_token":"old","claude_usage_mode":"dedicated"}`)

	// The only contract is that this neither panics nor blows up the save.
	ApplyPreservedMetadata(path, &inertStorage{AccessToken: "new"}, map[string]any{"email": "a@b.c"})
	ApplyPreservedMetadata(path, nil, map[string]any{"email": "a@b.c"})
}

// omitEmptyStorage mirrors the kimi token storage, whose optional fields carry
// `omitempty`. When they are empty they vanish from the marshaled payload — but
// the schema still owns those keys, so they must not be resurrected.
type omitEmptyStorage struct {
	recorder
	AccessToken string `json:"access_token"`
	Type        string `json:"type"`
	Scope       string `json:"scope,omitempty"`
	DeviceID    string `json:"device_id,omitempty"`
	Expired     string `json:"expired,omitempty"`
}

// A key the fresh payload's schema declares belongs to the fresh payload even
// when this particular login left it empty and `omitempty` dropped it from the
// serialized output. Deciding ownership from the marshaled bytes instead of the
// schema would carry a stale expiry back onto a brand new token — the exact
// credential rollback the design forbids.
func TestApplyPreservedMetadata_OmitEmptyFieldsStillBelongToFreshPayload(t *testing.T) {
	t.Parallel()

	path := writeAuthFile(t, `{
		"access_token": "STALE-access",
		"type": "kimi",
		"scope": "STALE-scope",
		"device_id": "STALE-device",
		"expired": "2020-01-01T00:00:00Z",
		"claude_usage_mode": "dedicated"
	}`)

	// A refresh-token login yields no scope, no device id and no expiry.
	storage := &omitEmptyStorage{AccessToken: "FRESH-access", Type: "kimi"}

	ApplyPreservedMetadata(path, storage, map[string]any{})

	injected, _ := storage.seen()
	for _, key := range []string{"scope", "device_id", "expired"} {
		if value, present := injected[key]; present {
			t.Errorf("%s = %v was carried back; the fresh payload's schema owns that key", key, value)
		}
	}
	if got := injected["claude_usage_mode"]; got != "dedicated" {
		t.Errorf("claude_usage_mode = %v, want %q", got, "dedicated")
	}
}
