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

// companionPinStatesRouter wires CompanionAuthMiddleware in front of the
// two companion-only endpoints. Crucially NO management middleware — the
// companion key is sufficient on its own.
func companionPinStatesRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	companion := e.Group("/v0/management/companion")
	companion.Use(h.CompanionAuthMiddleware())
	companion.GET("/claude-pin-states", h.GetCompanionClaudePinStates)
	companion.POST("/claude-pin-states/:id", h.PostCompanionClaudePinState)
	return e
}

// newCompanionPinHandler wires a Handler that knows about a handful of
// Claude + non-Claude accounts so the listing endpoint has something to
// enumerate. companion-secret is empty so CompanionAuthMiddleware lets the
// request through (the open-mode default for first-run install).
func newCompanionPinHandler(t *testing.T) (*Handler, *coreauth.Manager) {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "")
	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)

	mustRegister(t, manager, &coreauth.Auth{
		ID:         "claude-a.json",
		FileName:   "claude-a.json",
		Provider:   "claude",
		Attributes: map[string]string{"path": "/tmp/claude-a.json", "type": "claude"},
		Metadata:   map[string]any{"type": "claude"},
	})
	mustRegister(t, manager, &coreauth.Auth{
		ID:         "claude-b.json",
		FileName:   "claude-b.json",
		Provider:   "claude",
		Attributes: map[string]string{"path": "/tmp/claude-b.json", "type": "claude"},
		Metadata:   map[string]any{"type": "claude"},
	})
	mustRegister(t, manager, &coreauth.Auth{
		ID:         "gemini.json",
		FileName:   "gemini.json",
		Provider:   "gemini",
		Attributes: map[string]string{"path": "/tmp/gemini.json", "type": "gemini"},
		Metadata:   map[string]any{"type": "gemini"},
	})

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)
	return h, manager
}

func mustRegister(t *testing.T, m *coreauth.Manager, auth *coreauth.Auth) {
	t.Helper()
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatalf("register %s: %v", auth.ID, err)
	}
}

func getPinStates(e *gin.Engine, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v0/management/companion/claude-pin-states", nil)
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func postPinState(e *gin.Engine, key, id, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v0/management/companion/claude-pin-states/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

// GET lists every Claude account and only Claude accounts. Non-Claude
// providers (gemini, codex, kimi, ...) must not appear.
func TestCompanionPinStates_List_OnlyClaudeAccounts(t *testing.T) {
	h, _ := newCompanionPinHandler(t)
	e := companionPinStatesRouter(h)

	rec := getPinStates(e, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body=%q", rec.Code, rec.Body.String())
	}
	var resp struct {
		States []struct {
			AuthID         string `json:"auth_id"`
			Provider       string `json:"provider"`
			PinnedToLowest bool   `json:"pinned_to_lowest"`
		} `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.States) != 2 {
		t.Fatalf("got %d states, want 2 (just claude): %+v", len(resp.States), resp.States)
	}
	for _, st := range resp.States {
		if st.Provider != "claude" {
			t.Fatalf("non-Claude account leaked into pin-states: %+v", st)
		}
		if st.PinnedToLowest {
			t.Fatalf("account marked pinned when it shouldn't be: %+v", st)
		}
	}
}

// When companion-secret is set, the GET requires the companion key — NOT
// the management key.
func TestCompanionPinStates_List_RequiresCompanionKeyWhenSecretSet(t *testing.T) {
	h, _ := newCompanionPinHandler(t)
	h.SetConfig(&config.Config{
		AuthDir: t.TempDir(),
		RemoteManagement: config.RemoteManagement{
			AllowRemote:     true,
			SecretKey:       "mgmt",
			CompanionSecret: "comp",
		},
	})
	e := companionPinStatesRouter(h)

	// No key at all → 401
	rec := getPinStates(e, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no-key status %d, want 401", rec.Code)
	}

	// Management key alone → 401 (companion middleware does not accept it)
	rec = getPinStates(e, "mgmt")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("mgmt-only status %d, want 401; body=%q", rec.Code, rec.Body.String())
	}

	// Companion key → 200
	rec = getPinStates(e, "comp")
	if rec.Code != http.StatusOK {
		t.Fatalf("comp key status %d, want 200", rec.Code)
	}
}

// POST true → both Metadata and Attributes carry the flag; authManager
// reflects it; ClearRatelimitBlock has fired (we test via the in-memory
// helper directly).
func TestCompanionPinStates_PostTrue_WritesBothMapsAndClearsBlock(t *testing.T) {
	h, manager := newCompanionPinHandler(t)
	e := companionPinStatesRouter(h)

	// Seed a ratelimit block on claude-a so we can verify it gets cleared.
	resetAt := futureTime()
	coreauth.ApplyRatelimitBlock("claude-a.json", resetAt)

	rec := postPinState(e, "", "claude-a.json", `{"pinned_to_lowest":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body=%q", rec.Code, rec.Body.String())
	}

	got, _ := manager.GetByID("claude-a.json")
	if v, _ := got.Metadata["claude_pin_to_lowest"].(bool); !v {
		t.Fatalf("metadata.claude_pin_to_lowest = %v, want true", got.Metadata["claude_pin_to_lowest"])
	}
	if got.Attributes["claude_pin_to_lowest"] != "true" {
		t.Fatalf("attrs.claude_pin_to_lowest = %q, want \"true\"", got.Attributes["claude_pin_to_lowest"])
	}
	// ClearRatelimitBlock is called; the in-memory block we seeded must be
	// gone, otherwise an account that was just pinned stays 503 until its
	// 5-hour window resets — exactly the failure mode the pin is meant to
	// short-circuit.
	if !got.RatelimitBlockUntil.IsZero() {
		t.Fatalf("RatelimitBlockUntil = %v, want zero (block must be cleared when pinning)",
			got.RatelimitBlockUntil)
	}
	if got.Unavailable {
		t.Fatalf("Unavailable = true after pin, want false")
	}
}

// POST false → both maps lose the key.
func TestCompanionPinStates_PostFalse_DeletesBothMaps(t *testing.T) {
	h, manager := newCompanionPinHandler(t)
	e := companionPinStatesRouter(h)

	// First pin it.
	if rec := postPinState(e, "", "claude-a.json", `{"pinned_to_lowest":true}`); rec.Code != http.StatusOK {
		t.Fatalf("seed: status %d, body=%q", rec.Code, rec.Body.String())
	}
	// Then unpin.
	rec := postPinState(e, "", "claude-a.json", `{"pinned_to_lowest":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("unpin status %d, body=%q", rec.Code, rec.Body.String())
	}

	got, _ := manager.GetByID("claude-a.json")
	if _, present := got.Metadata["claude_pin_to_lowest"]; present {
		t.Fatalf("metadata still carries the key after unpin: %v", got.Metadata["claude_pin_to_lowest"])
	}
	if _, present := got.Attributes["claude_pin_to_lowest"]; present {
		t.Fatalf("attributes still carries the key after unpin: %q", got.Attributes["claude_pin_to_lowest"])
	}
}

// POST on a non-Claude account → 400 (the field is Claude-only).
func TestCompanionPinStates_PostNonClaude_Returns400(t *testing.T) {
	h, _ := newCompanionPinHandler(t)
	e := companionPinStatesRouter(h)

	rec := postPinState(e, "", "gemini.json", `{"pinned_to_lowest":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%q", rec.Code, rec.Body.String())
	}
}

// POST on a missing id → 404.
func TestCompanionPinStates_PostMissing_Returns404(t *testing.T) {
	h, _ := newCompanionPinHandler(t)
	e := companionPinStatesRouter(h)

	rec := postPinState(e, "", "ghost.json", `{"pinned_to_lowest":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", rec.Code)
	}
}

// futureTime is a small helper to schedule a future ratelimit reset.
func futureTime() (t time.Time) {
	return time.Now().Add(time.Hour)
}
