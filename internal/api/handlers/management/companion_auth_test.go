package management

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// companionRouter wires CompanionAuthMiddleware onto a tiny test route so the middleware
// can be exercised end-to-end against the real gin engine. Empty body matches what
// /usage-mode.html serves; the assertion is "did the middleware let it reach the handler?"
func companionRouter(h *Handler, body string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.Use(h.CompanionAuthMiddleware())
	e.GET("/usage-mode.html", func(c *gin.Context) {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.String(http.StatusOK, body)
	})
	return e
}

// Empty companion-secret must pass requests through with no Authorization header —
// this is the "first-run / set" gate a fresh server needs so the operator can set a
// lock from the page itself.
func TestCompanionAuthMiddleware_EmptySecret_PassesThrough(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{},
	}

	e := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (empty secret should passthrough); body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<page>") {
		t.Fatalf("body = %q, want it to reach the handler", rec.Body.String())
	}
}

// Non-empty companion-secret + wrong bearer must 401 with a WWW-Authenticate header
// so a browser can pop a native credential dialog.
func TestCompanionAuthMiddleware_NonEmpty_WrongBearer_Returns401WithWWWAuthenticate(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: "real-secret"},
		},
	}

	e := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Bearer wrong-secret")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401; body=%q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, `Basic realm="Companion panel"`) {
		t.Fatalf("WWW-Authenticate = %q, want it to mention Basic realm", got)
	}
	if strings.Contains(rec.Body.String(), "<page>") {
		t.Fatalf("body leaked page content despite bad key: %q", rec.Body.String())
	}
}

// Missing key entirely (secret is set, no header) must also 401 — same gate.
func TestCompanionAuthMiddleware_NonEmpty_NoHeader_Returns401(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: "real-secret"},
		},
	}

	e := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatalf("WWW-Authenticate missing on missing-key rejection")
	}
}

// Correct bearer via Authorization header lets the request through.
func TestCompanionAuthMiddleware_NonEmpty_CorrectBearer_Returns200(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: "real-secret"},
		},
	}

	e := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Bearer real-secret")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<page>") {
		t.Fatalf("body = %q, want page to reach the handler", rec.Body.String())
	}
}

// Correct key via X-Companion-Key must work the same way — both headers are
// accepted so the operator's page JS doesn't have to commit to one shape.
func TestCompanionAuthMiddleware_NonEmpty_CorrectXCompanionKey_Returns200(t *testing.T) {
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: "real-secret"},
		},
	}

	e := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("X-Companion-Key", "real-secret")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<page>") {
		t.Fatalf("body = %q, want page to reach the handler", rec.Body.String())
	}
}

// A bcrypt-hashed stored value must still verify when the caller supplies the
// original plaintext. LoadConfig hashes companion-secret on first save just
// like secret-key; this is the path that gets exercised on every request
// after that first save.
func TestCompanionAuthMiddleware_NonEmpty_BcryptStored_PlaintextCallerPasses(t *testing.T) {
	hashed, err := bcrypt.GenerateFromPassword([]byte("real-secret"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: string(hashed)},
		},
	}

	e := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Bearer real-secret")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (bcrypt stored should still verify plaintext caller); body=%q",
			rec.Code, rec.Body.String())
	}
}
