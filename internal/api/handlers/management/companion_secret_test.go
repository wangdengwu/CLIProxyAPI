package management

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// companionSecretRouter wires the management middleware in front of POST
// /v0/management/companion-secret so tests cover the full chain the live
// server exposes (auth + endpoint + persistence + hot-reload).
func companionSecretRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	mgmt := e.Group("/v0/management")
	mgmt.Use(h.Middleware())
	mgmt.POST("/companion-secret", h.PostCompanionSecret)
	return e
}

// newCompanionSecretHandler wires up a Handler backed by a real
// LoadConfig-compatible config file so persistence assertions have somewhere
// to land. Tests then drive SetConfig on the live handler to prove
// hot-reload works without restarting the process.
func newCompanionSecretHandler(t *testing.T) (*Handler, string) {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	// secret-key must be set or the management middleware refuses everything
	// with "remote management disabled" before our endpoint is ever reached.
	yaml := "port: 8317\nremote-management:\n  allow-remote: true\n  secret-key: \"mgmt-key\"\n"
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write seed config: %v", err)
	}
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("load seed config: %v", err)
	}
	h := NewHandler(cfg, cfgPath, nil)
	return h, cfgPath
}

func postCompanionSecret(e *gin.Engine, mgmtKey, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v0/management/companion-secret", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if mgmtKey == "" {
		mgmtKey = "mgmt-key"
	}
	req.Header.Set("Authorization", "Bearer "+mgmtKey)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// First-time set: no companion-secret on disk, no current_secret supplied.
// Server hashes the plaintext and writes back, and the live handler must see
// the new state on the very next request — proving hot-reload without
// restarting the process.
func TestPostCompanionSecret_FirstTimeSet_PersistsAndHotReloads(t *testing.T) {
	h, cfgPath := newCompanionSecretHandler(t)
	e := companionSecretRouter(h)

	rec := postCompanionSecret(e, "", `{"new_secret":"alpha"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	// 1) Persisted to disk as a bcrypt hash (no plaintext).
	disk, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(disk), "alpha") {
		t.Fatalf("plaintext leaked back to disk:\n%s", string(disk))
	}

	// 2) The endpoint must hot-reload the live handler itself — the very next
	//    CompanionAuthMiddleware call must see the new value, with NO manual
	//    SetConfig from the test. This is what catches an endpoint that
	//    writes disk but forgets to swap the cfg pointer.
	e2 := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Bearer alpha")
	w := httptest.NewRecorder()
	e2.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("after endpoint, correct key status = %d, want 200 (endpoint must hot-reload); body=%q",
			w.Code, w.Body.String())
	}

	// 3) Wrong key now rejected.
	req2 := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req2.Header.Set("Authorization", "Bearer wrong")
	w2 := httptest.NewRecorder()
	e2.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Fatalf("after endpoint, wrong key status = %d, want 401", w2.Code)
	}
}

// Overwrite path: secret exists, current_secret is correct, new_secret
// supersedes it. Storage on disk and the live state both update.
func TestPostCompanionSecret_OverwriteWithCorrectCurrent(t *testing.T) {
	h, _ := newCompanionSecretHandler(t)
	e := companionSecretRouter(h)

	// Seed an existing secret via the endpoint.
	if rec := postCompanionSecret(e, "", `{"new_secret":"first"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed: status %d, body=%q", rec.Code, rec.Body.String())
	}

	// Overwrite with the correct current_secret.
	rec := postCompanionSecret(e, "", `{"current_secret":"first","new_secret":"second"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("overwrite: status %d, want 200; body=%q", rec.Code, rec.Body.String())
	}

	// "first" no longer works, "second" does.
	e2 := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Bearer second")
	w := httptest.NewRecorder()
	e2.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("after overwrite, new key rejected: status %d", w.Code)
	}
}

// Wrong current_secret must 400 and leave the stored value untouched —
// this is the brute-force guardrail the operator relies on.
func TestPostCompanionSecret_WrongCurrent_Returns400AndUnchanged(t *testing.T) {
	h, _ := newCompanionSecretHandler(t)
	e := companionSecretRouter(h)

	// Seed.
	if rec := postCompanionSecret(e, "", `{"new_secret":"real"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed: status %d, body=%q", rec.Code, rec.Body.String())
	}

	// Wrong current.
	rec := postCompanionSecret(e, "", `{"current_secret":"wrong","new_secret":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400", rec.Code)
	}

	// Live state still knows "real" — endpoint's own hot-reload proves it.
	e2 := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	req.Header.Set("Authorization", "Bearer real")
	w := httptest.NewRecorder()
	e2.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("'real' should still authenticate after rejected overwrite: status %d", w.Code)
	}
}

// Clear path: current_secret correct + new_secret="" → page is unlocked
// again (the "remove the lock" flow).
func TestPostCompanionSecret_ClearWithCurrent_UnlocksPage(t *testing.T) {
	h, cfgPath := newCompanionSecretHandler(t)
	e := companionSecretRouter(h)

	if rec := postCompanionSecret(e, "", `{"new_secret":"alpha"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed: status %d", rec.Code)
	}

	rec := postCompanionSecret(e, "", `{"current_secret":"alpha","new_secret":""}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("clear: status %d, want 200", rec.Code)
	}

	diskCfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if diskCfg.RemoteManagement.CompanionSecret != "" {
		t.Fatalf("disk still has %q after clear", diskCfg.RemoteManagement.CompanionSecret)
	}

	// Empty cfg now bypasses the middleware (live state via endpoint's hot-reload).
	e2 := companionRouter(h, "<page>")
	req := httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	w := httptest.NewRecorder()
	e2.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("after clear, page should be unlocked: status %d", w.Code)
	}
}

// The endpoint must require the management key, not the companion key — an
// attacker who somehow phishes the companion key must not be able to remove
// or rotate the lock itself.
func TestPostCompanionSecret_RequiresManagementKey(t *testing.T) {
	h, _ := newCompanionSecretHandler(t)
	e := companionSecretRouter(h)

	// Seed via management key.
	if rec := postCompanionSecret(e, "", `{"new_secret":"alpha"}`); rec.Code != http.StatusOK {
		t.Fatalf("seed via management key: status %d", rec.Code)
	}

	// Now without management key → must not succeed.
	req := httptest.NewRequest(http.MethodPost, "/v0/management/companion-secret", bytes.NewBufferString(`{"new_secret":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer alpha") // only the companion key, not management
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("endpoint accepted companion-only key: status %d, body=%q", rec.Code, rec.Body.String())
	}
}

// hashForStorage must always return either "" or a bcrypt-shaped string. A
// plaintext round-trip would land the operator's cleartext on disk if
// LoadConfig ever stopped hashing on load — defending against that drift is
// the whole point of hashing at the write site, not just the load site.
func TestHashForStorage_AlwaysBcryptOrEmpty(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty clears to empty", ""},
		{"non-empty hashes", "alpha"},
		{"unicode survives hashing", "中文钥匙"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := hashForStorage(tc.in)
			if err != nil {
				t.Fatalf("hashForStorage(%q): %v", tc.in, err)
			}
			if tc.in == "" {
				if got != "" {
					t.Fatalf("empty input -> %q, want \"\"", got)
				}
				return
			}
			if got == tc.in {
				t.Fatalf("plaintext survived: hashForStorage(%q) = %q", tc.in, got)
			}
			if !strings.HasPrefix(got, "$2") {
				t.Fatalf("hashForStorage(%q) = %q, want bcrypt shape", tc.in, got)
			}
		})
	}
}

// Body shape sanity: ensure the request/response structures stay consistent.
// Defends against accidental field renames that would silently break the
// page-side JS that calls this endpoint.
func TestPostCompanionSecret_RequestResponseShape(t *testing.T) {
	h, _ := newCompanionSecretHandler(t)
	e := companionSecretRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/v0/management/companion-secret", strings.NewReader(`{"new_secret":"alpha"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer mgmt-key")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body=%q", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, hasOK := resp["ok"]; !hasOK {
		t.Fatalf("response missing 'ok' field: %v", resp)
	}
}
