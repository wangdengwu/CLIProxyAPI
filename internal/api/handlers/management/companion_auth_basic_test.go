package management

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// The browser's native auth dialog sends Authorization: Basic <base64(user:pass)>
// when the page is opened directly and a lock is in place. The page JS sends
// Authorization: Bearer from localStorage; both must work — the Basic dialog
// is the only recovery path an operator with the lock in place has without
// resetting via PG.
func TestCompanionAuthMiddleware_NonEmpty_BasicAuth_CorrectPassword(t *testing.T) {
	hashed, err := bcrypt.GenerateFromPassword([]byte("hello1234"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: string(hashed)},
		},
	}

	e := companionRouter(h, "<page>")
	creds := base64.StdEncoding.EncodeToString([]byte("any-user:hello1234"))
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (Basic auth should accept the password portion); body=%q",
			rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "<page>") {
		t.Fatalf("body = %q, want page to render", rec.Body.String())
	}
}

// Wrong password in Basic auth → 401.
func TestCompanionAuthMiddleware_NonEmpty_BasicAuth_WrongPassword_Returns401(t *testing.T) {
	hashed, _ := bcrypt.GenerateFromPassword([]byte("hello1234"), bcrypt.MinCost)
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: string(hashed)},
		},
	}

	e := companionRouter(h, "<page>")
	creds := base64.StdEncoding.EncodeToString([]byte("any-user:wrong"))
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// Password containing a colon must still match — split on the LAST colon so
// the trailing colon-delimited portion is treated as the password.
func TestCompanionAuthMiddleware_NonEmpty_BasicAuth_PasswordWithColon(t *testing.T) {
	// Note bcrypt's 72-byte limit; pick a value under it.
	password := "a:b:c"
	hashed, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: string(hashed)},
		},
	}

	e := companionRouter(h, "<page>")
	creds := base64.StdEncoding.EncodeToString([]byte("any-user:" + password))
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Basic "+creds)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 (password with colons should match); body=%q",
			rec.Code, rec.Body.String())
	}
}

// Malformed Basic auth (non-base64) → 401, not bypass.
func TestCompanionAuthMiddleware_NonEmpty_BasicAuth_Malformed_Returns401(t *testing.T) {
	hashed, _ := bcrypt.GenerateFromPassword([]byte("hello1234"), bcrypt.MinCost)
	h := &Handler{
		cfg: &config.Config{
			RemoteManagement: config.RemoteManagement{CompanionSecret: string(hashed)},
		},
	}

	e := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Basic !!not-base64!!")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 (malformed Basic must not bypass)", rec.Code)
	}
}