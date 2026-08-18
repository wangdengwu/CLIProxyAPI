package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// patchUsageMode registers a claude auth, PATCHes claude_usage_mode, and returns the
// recorder plus the manager so callers can inspect the post-patch state.
func patchUsageMode(t *testing.T, record *coreauth.Auth, mode string) (*httptest.ResponseRecorder, *coreauth.Manager) {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), record); err != nil {
		t.Fatalf("register: %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	body := `{"name":"` + record.ID + `","claude_usage_mode":"` + mode + `"}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/fields", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	h.PatchAuthFileFields(ctx)
	return rec, manager
}

func claudeAuth(id string) *coreauth.Auth {
	return &coreauth.Auth{
		ID:         id,
		FileName:   id,
		Provider:   "claude",
		Attributes: map[string]string{"path": "/tmp/" + id},
		Metadata:   map[string]any{"type": "claude"},
	}
}

// dedicated: both Metadata and Attributes carry the canonical value and the accessor
// reports dedicated.
func TestPatchUsageMode_DedicatedWritesBothMaps(t *testing.T) {
	rec, manager := patchUsageMode(t, claudeAuth("ded.json"), "dedicated")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	got, _ := manager.GetByID("ded.json")
	if v, _ := got.Metadata["claude_usage_mode"].(string); v != "dedicated" {
		t.Fatalf("metadata.claude_usage_mode = %q, want dedicated", v)
	}
	if got.Attributes["claude_usage_mode"] != "dedicated" {
		t.Fatalf("attrs.claude_usage_mode = %q, want dedicated", got.Attributes["claude_usage_mode"])
	}
	if got.ClaudeUsageMode() != "dedicated" {
		t.Fatalf("ClaudeUsageMode() = %q, want dedicated", got.ClaudeUsageMode())
	}
}

// shared: the key is deleted from both maps and the accessor falls back to empty.
func TestPatchUsageMode_SharedDeletesBothMaps(t *testing.T) {
	record := claudeAuth("shr.json")
	record.Metadata["claude_usage_mode"] = "dedicated"
	record.Attributes["claude_usage_mode"] = "dedicated"

	rec, manager := patchUsageMode(t, record, "shared")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	got, _ := manager.GetByID("shr.json")
	if _, ok := got.Metadata["claude_usage_mode"]; ok {
		t.Fatalf("metadata.claude_usage_mode should be deleted, got %v", got.Metadata["claude_usage_mode"])
	}
	if _, ok := got.Attributes["claude_usage_mode"]; ok {
		t.Fatalf("attrs.claude_usage_mode should be deleted, got %v", got.Attributes["claude_usage_mode"])
	}
	if got.ClaudeUsageMode() != "" {
		t.Fatalf("ClaudeUsageMode() = %q, want empty", got.ClaudeUsageMode())
	}
}

// exclusive is an alias: it normalizes to and stores dedicated.
func TestPatchUsageMode_ExclusiveNormalizesToDedicated(t *testing.T) {
	rec, manager := patchUsageMode(t, claudeAuth("exc.json"), "exclusive")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	got, _ := manager.GetByID("exc.json")
	if v, _ := got.Metadata["claude_usage_mode"].(string); v != "dedicated" {
		t.Fatalf("metadata.claude_usage_mode = %q, want dedicated (exclusive normalized)", v)
	}
}

// An unrecognized value is rejected with 400 and mutates nothing.
func TestPatchUsageMode_InvalidValueRejected(t *testing.T) {
	rec, manager := patchUsageMode(t, claudeAuth("bad.json"), "bogus")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	got, _ := manager.GetByID("bad.json")
	if _, ok := got.Metadata["claude_usage_mode"]; ok {
		t.Fatalf("invalid patch must not write metadata.claude_usage_mode")
	}
	if _, ok := got.Attributes["claude_usage_mode"]; ok {
		t.Fatalf("invalid patch must not write attrs.claude_usage_mode")
	}
}

// Switching a currently rate-limit-blocked account to dedicated clears the block so it
// resumes taking traffic immediately.
func TestPatchUsageMode_DedicatedClearsRatelimitBlock(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), claudeAuth("blk.json")); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Register makes NewManager the active ratelimit target; block via the exported path.
	reset := time.Now().Add(3 * time.Hour)
	coreauth.ApplyRatelimitBlock("blk.json", reset)
	if blocked, _ := manager.GetByID("blk.json"); blocked == nil || blocked.RatelimitBlockUntil.IsZero() {
		t.Fatalf("precondition: expected blk.json to be blocked")
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	body := `{"name":"blk.json","claude_usage_mode":"dedicated"}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/fields", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	h.PatchAuthFileFields(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	got, _ := manager.GetByID("blk.json")
	if !got.RatelimitBlockUntil.IsZero() {
		t.Fatalf("RatelimitBlockUntil = %v, want cleared", got.RatelimitBlockUntil)
	}
	if got.Unavailable {
		t.Fatal("auth must be available after clearing the block")
	}
}

// A patch that omits claude_usage_mode (here: a note-only patch) must leave an existing
// mode untouched — guards against a future change accidentally resetting it.
func TestPatchUsageMode_OmittedLeavesExistingUntouched(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	record := claudeAuth("keep.json")
	record.Metadata["claude_usage_mode"] = "dedicated"
	record.Attributes["claude_usage_mode"] = "dedicated"

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), record); err != nil {
		t.Fatalf("register: %v", err)
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	body := `{"name":"keep.json","note":"just a note"}`
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/fields", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	h.PatchAuthFileFields(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	got, _ := manager.GetByID("keep.json")
	if got.ClaudeUsageMode() != "dedicated" {
		t.Fatalf("ClaudeUsageMode() = %q after note-only patch, want dedicated (untouched)", got.ClaudeUsageMode())
	}
}

// The listing exposes claude_usage_mode for an account that has one and omits it for an
// account that does not.
func TestListAuthFiles_ExposesUsageMode(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(nil, nil, nil)
	withMode := claudeAuth("with.json")
	withMode.Attributes["claude_usage_mode"] = "dedicated"
	withMode.Metadata["claude_usage_mode"] = "dedicated"
	if _, err := manager.Register(context.Background(), withMode); err != nil {
		t.Fatalf("register with: %v", err)
	}
	if _, err := manager.Register(context.Background(), claudeAuth("without.json")); err != nil {
		t.Fatalf("register without: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	h.tokenStore = &memoryAuthStore{}
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	files, _ := payload["files"].([]any)
	modes := map[string]any{}
	for _, f := range files {
		e, _ := f.(map[string]any)
		id, _ := e["name"].(string)
		modes[id] = e["claude_usage_mode"]
	}
	if modes["with.json"] != "dedicated" {
		t.Fatalf("with.json claude_usage_mode = %#v, want dedicated", modes["with.json"])
	}
	if v, present := modes["without.json"]; present && v != nil {
		t.Fatalf("without.json should omit claude_usage_mode, got %#v", v)
	}
}
