package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// The embedded companion page is served with 200 and its own HTML when the control panel
// is enabled (the default).
func TestServeUsageModePanel_ServesWhenEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{cfg: &proxyconfig.Config{}}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	s.serveUsageModePanel(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if !strings.Contains(rec.Body.String(), "Claude Usage Mode") {
		t.Fatal("body does not contain the expected page marker")
	}
	// It must drive the real management endpoints, not a placeholder.
	if !strings.Contains(rec.Body.String(), "/v0/management/auth-files") {
		t.Fatal("page does not reference the management auth-files endpoint")
	}
}

// Disabling the control panel gates the companion page off too (404) — a security-
// relevant guard: disabling the panel must disable all operator UI.
func TestServeUsageModePanel_GatedOffWhenControlPanelDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &proxyconfig.Config{}
	cfg.RemoteManagement.DisableControlPanel = true
	s := &Server{cfg: cfg}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	s.serveUsageModePanel(ctx)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when control panel disabled", rec.Code)
	}
}
