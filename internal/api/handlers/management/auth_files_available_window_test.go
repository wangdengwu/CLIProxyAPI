package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// patchAvailableWindow registers an auth, PATCHes available_window with the raw JSON
// value supplied, and returns the recorder plus the manager for post-patch inspection.
// rawJSONValue is inserted verbatim so callers can also send a missing field.
func patchAvailableWindow(t *testing.T, record *coreauth.Auth, rawJSONValue string) (*httptest.ResponseRecorder, *coreauth.Manager) {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), record); err != nil {
		t.Fatalf("register: %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	body := `{"name":"` + record.ID + `"`
	if rawJSONValue != "" {
		body += `,"available_window":` + rawJSONValue
	}
	body += `}`

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/fields", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	h.PatchAuthFileFields(ctx)
	return rec, manager
}

func authWithWindow(id, window string) *coreauth.Auth {
	record := claudeAuth(id)
	if window != "" {
		record.Attributes["available_window"] = window
		record.Metadata["available_window"] = window
	}
	return record
}

// A well-formed window lands in both maps: Metadata is the on-disk source of truth,
// Attributes is the in-memory mirror the scheduler's gate reads first. Writing only
// one would make the UI and the scheduler disagree until the next reload.
func TestPatchAvailableWindow_ValidWritesBothMaps(t *testing.T) {
	rec, manager := patchAvailableWindow(t, claudeAuth("claude-a.json"), `"18:00-09:00"`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got, ok := manager.GetByID("claude-a.json")
	if !ok {
		t.Fatalf("GetByID: auth not found")
	}
	if v := got.Attributes["available_window"]; v != "18:00-09:00" {
		t.Errorf("Attributes[available_window] = %q, want %q", v, "18:00-09:00")
	}
	if v, _ := got.Metadata["available_window"].(string); v != "18:00-09:00" {
		t.Errorf("Metadata[available_window] = %q, want %q", v, "18:00-09:00")
	}
	if v := got.AvailableWindow(); v != "18:00-09:00" {
		t.Errorf("AvailableWindow() = %q, want %q", v, "18:00-09:00")
	}
}

// Empty string clears the window, matching how priority and note are cleared. The
// account returns to all-day availability.
func TestPatchAvailableWindow_EmptyClearsBothMaps(t *testing.T) {
	rec, manager := patchAvailableWindow(t, authWithWindow("claude-b.json", "18:00-09:00"), `""`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	got, ok := manager.GetByID("claude-b.json")
	if !ok {
		t.Fatalf("GetByID: auth not found")
	}
	if v, exists := got.Attributes["available_window"]; exists {
		t.Errorf("Attributes[available_window] = %q, want removed", v)
	}
	if v, exists := got.Metadata["available_window"]; exists {
		t.Errorf("Metadata[available_window] = %v, want removed", v)
	}
	if v := got.AvailableWindow(); v != "" {
		t.Errorf("AvailableWindow() = %q, want empty", v)
	}
}

// Whitespace-only is the same intent as empty: clear it.
func TestPatchAvailableWindow_BlankClearsBothMaps(t *testing.T) {
	rec, manager := patchAvailableWindow(t, authWithWindow("claude-c.json", "18:00-09:00"), `"   "`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got, _ := manager.GetByID("claude-c.json")
	if v := got.AvailableWindow(); v != "" {
		t.Errorf("AvailableWindow() = %q, want empty after blank input", v)
	}
}

// Rejecting typos is the write path's whole job: at runtime an unreadable window fails
// open, so without a 400 here the operator would believe a restriction is in force
// while the account keeps serving around the clock.
func TestPatchAvailableWindow_MalformedRejected(t *testing.T) {
	malformed := []string{
		"18:00~09:00",
		"18:00—09:00",
		"6pm-9am",
		"18-09",
		"18:00",
		"18:00-09:00-10:00",
		"25:00-09:00",
		"18:60-09:00",
		"18:00-24:30",
		"18:00-18:00",
		"ab:cd-ef:gh",
		"１８:００-０９:００",
	}

	for _, window := range malformed {
		t.Run(window, func(t *testing.T) {
			encoded, errEncode := json.Marshal(window)
			if errEncode != nil {
				t.Fatalf("marshal: %v", errEncode)
			}
			rec, manager := patchAvailableWindow(t, claudeAuth("claude-d.json"), string(encoded))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d for %q, want 400; body = %s", rec.Code, window, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "available_window") {
				t.Errorf("error body does not mention the field: %s", rec.Body.String())
			}
			// A rejected request must not have partially written anything.
			got, _ := manager.GetByID("claude-d.json")
			if v := got.AvailableWindow(); v != "" {
				t.Errorf("AvailableWindow() = %q after a rejected patch, want untouched", v)
			}
		})
	}
}

// The spellings the gate accepts must also be accepted here — a stricter write path
// would reject windows the scheduler would happily honour.
func TestPatchAvailableWindow_AcceptsTheSameSpellingsAsTheGate(t *testing.T) {
	accepted := []string{"18:00 - 09:00", " 18:00-09:00 ", "18:00-24:00", "9:00-18:00", "00:00-24:00"}

	for _, window := range accepted {
		t.Run(window, func(t *testing.T) {
			encoded, _ := json.Marshal(window)
			rec, manager := patchAvailableWindow(t, claudeAuth("claude-e.json"), string(encoded))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d for %q, want 200; body = %s", rec.Code, window, rec.Body.String())
			}
			got, _ := manager.GetByID("claude-e.json")
			if got.AvailableWindow() == "" {
				t.Errorf("AvailableWindow() empty after accepting %q", window)
			}
		})
	}
}

// Omitting the field must leave an existing window alone — the page PATCHes one field
// at a time, so a mode toggle must not wipe the window.
func TestPatchAvailableWindow_OmittedLeavesExistingUntouched(t *testing.T) {
	record := authWithWindow("claude-f.json", "18:00-09:00")
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), record); err != nil {
		t.Fatalf("register: %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	// Patch an unrelated field.
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/fields",
		strings.NewReader(`{"name":"claude-f.json","claude_usage_mode":"dedicated"}`))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	h.PatchAuthFileFields(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got, _ := manager.GetByID("claude-f.json")
	if v := got.AvailableWindow(); v != "18:00-09:00" {
		t.Errorf("AvailableWindow() = %q after patching another field, want it untouched", v)
	}
}

// The list endpoint must echo the raw window so the page can prefill its input.
func TestListAuthFiles_ExposesAvailableWindow(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), authWithWindow("claude-g.json", "18:00-09:00")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := manager.Register(context.Background(), claudeAuth("claude-h.json")); err != nil {
		t.Fatalf("register: %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v; body = %s", err, rec.Body.String())
	}

	byID := make(map[string]map[string]any, len(payload.Files))
	for _, entry := range payload.Files {
		id, _ := entry["id"].(string)
		byID[id] = entry
	}

	windowed, ok := byID["claude-g.json"]
	if !ok {
		t.Fatalf("windowed auth missing from listing: %s", rec.Body.String())
	}
	if v, _ := windowed["available_window"].(string); v != "18:00-09:00" {
		t.Errorf("available_window = %q, want %q", v, "18:00-09:00")
	}

	plain, ok := byID["claude-h.json"]
	if !ok {
		t.Fatalf("plain auth missing from listing")
	}
	if v, exists := plain["available_window"]; exists && v != "" {
		t.Errorf("available_window = %v for an auth without one, want absent or empty", v)
	}
}
