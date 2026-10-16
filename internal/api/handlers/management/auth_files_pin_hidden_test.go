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

// Even though an account has claude_pin_to_lowest=true in its Metadata AND
// Attributes, the listing endpoint must NOT expose that field — it is the
// companion-only state, hidden from manage.html.
func TestListAuthFiles_PinToLowestNeverExposed(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:         "pinned.json",
		FileName:   "pinned.json",
		Provider:   "claude",
		Attributes: map[string]string{"path": "/tmp/pinned.json", "claude_pin_to_lowest": "true"},
		Metadata:   map[string]any{"type": "claude", "claude_pin_to_lowest": true},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:         "regular.json",
		FileName:   "regular.json",
		Provider:   "claude",
		Attributes: map[string]string{"path": "/tmp/regular.json", "priority": "1"},
		Metadata:   map[string]any{"type": "claude"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body=%q", rec.Code, rec.Body.String())
	}

	var resp struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, entry := range resp.Files {
		if _, present := entry["claude_pin_to_lowest"]; present {
			t.Fatalf("entry exposes claude_pin_to_lowest to manage.html: %v", entry)
		}
	}
}

// PATCH /v0/management/auth-files/fields must NOT accept claude_pin_to_lowest
// — the field is intentionally outside the whitelist, so an attacker with
// the management key (but not the companion key) cannot toggle a pin state
// they cannot even see. The companion page is the only way to set this.
func TestPatchAuthFileFields_RejectsPinToLowestField(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	if _, err := manager.Register(context.Background(), &coreauth.Auth{
		ID:         "acme.json",
		FileName:   "acme.json",
		Provider:   "claude",
		Attributes: map[string]string{"type": "claude"},
		Metadata:   map[string]any{"type": "claude"},
	}); err != nil {
		t.Fatalf("register: %v", err)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodPatch, "/v0/management/auth-files/fields", strings.NewReader(
		`{"name":"acme.json","claude_pin_to_lowest":true}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	h.PatchAuthFileFields(ctx)

	if rec.Code == http.StatusOK {
		t.Fatalf("PATCH accepted claude_pin_to_lowest; body=%q", rec.Body.String())
	}
}
