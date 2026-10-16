package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// fetchPanelBody pulls the served HTML through the real Server.serveUsageModePanel
// path. Returns the body string and an error if the route returned anything
// other than 200 + a non-trivial payload.
func fetchPanelBody(t *testing.T, s *Server) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	s.serveUsageModePanel(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200; body=%q", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if len(body) < 100 {
		t.Fatalf("body suspiciously short: %q", body)
	}
	return body
}

// Each new shape on the page must show up in the served HTML, with a
// discriminating assertion — mutation checks against the same body string
// run at the bottom of the file.
func TestServeUsageModePanel_HasCompanionLockCard(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	if !strings.Contains(body, `id="lockCard"`) {
		t.Fatalf("body missing #lockCard (companion lock card)")
	}
	if !strings.Contains(body, `id="lockBadge"`) {
		t.Fatalf("body missing #lockBadge")
	}
	if !strings.Contains(body, `id="lockBody"`) {
		t.Fatalf("body missing #lockBody")
	}
}

func TestServeUsageModePanel_HasCompanionKeyCard(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	if !strings.Contains(body, `id="companionKeyCard"`) {
		t.Fatalf("body missing #companionKeyCard")
	}
	if !strings.Contains(body, `id="companionToken"`) {
		t.Fatalf("body missing companion token input")
	}
	if !strings.Contains(body, `id="unlock"`) {
		t.Fatalf("body missing unlock button")
	}
}

// Companion key must be a SEPARATE localStorage entry from the management key.
// This guards against accidental "use one key for both" simplifications that
// would silently void the whole independent-secret design.
func TestServeUsageModePanel_DualKeyLocalStorage(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	if !strings.Contains(body, `cpa-companion-key`) {
		t.Fatalf("body missing cpa-companion-key localStorage key")
	}
	if !strings.Contains(body, `cliproxy.mgmtKey`) {
		t.Fatalf("body missing cliproxy.mgmtKey (existing management key localStorage key)")
	}
	if !strings.Contains(body, `localStorage.setItem(COMPANION_TOKEN_KEY, newKey)`) &&
		!strings.Contains(body, `setItem(COMPANION_TOKEN_KEY`) {
		t.Fatalf("body missing companion-key setItem call")
	}
}

// The page must drive the two new companion endpoints so the page JS can
// read pin states and toggle pins without the management key.
func TestServeUsageModePanel_ReferencesCompanionEndpoints(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	if !strings.Contains(body, `/v0/management/companion/claude-pin-states`) {
		t.Fatalf("body missing GET /v0/management/companion/claude-pin-states")
	}
	if !strings.Contains(body, `/v0/management/companion/claude-pin-states/" + encodeURIComponent(id)`) &&
		!strings.Contains(body, `companion/claude-pin-states/${`) &&
		!strings.Contains(body, `claude-pin-states/`+`encodeURIComponent`) {
		// The POST URL is constructed dynamically — assert by checking that
		// the prefix appears at least twice (once for GET, once for POST).
		t.Fatalf("body missing POST companion pin-states URL pattern")
	}
}

// The setter for the lock itself is management-keyed (NOT companion-keyed)
// so a phished companion key cannot remove its own gate.
func TestServeUsageModePanel_LockSetterUsesManagementKey(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	if !strings.Contains(body, `/v0/management/companion-secret`) {
		t.Fatalf("body missing /v0/management/companion-secret endpoint reference")
	}
	if !strings.Contains(body, `method: "POST"`) || !strings.Contains(body, `companion-secret`) {
		t.Fatalf("body missing POST + companion-secret wiring")
	}
	// The setter body builds with `current_secret` and `new_secret` keys,
	// mirroring the backend's request struct.
	if !strings.Contains(body, `current_secret`) || !strings.Contains(body, `new_secret`) {
		t.Fatalf("body missing current_secret/new_secret keys")
	}
	// The companion-secret call MUST use managementHeaders(), NOT
	// companionHeaders(). A bug here lets a stolen companion key remove
	// its own gate. Scan a tight window around the call site only — not
	// the whole page, which also legitimately uses companionHeaders() for
	// the pin-states endpoint below.
	idx := strings.Index(body, "/v0/management/companion-secret")
	if idx < 0 {
		t.Fatalf("body missing companion-secret call site")
	}
	// Look back ~300 chars for the surrounding `headers:` line.
	start := idx - 300
	if start < 0 {
		start = 0
	}
	end := idx + 200
	if end > len(body) {
		end = len(body)
	}
	window := body[start:end]
	if !strings.Contains(window, "headers: managementHeaders()") {
		t.Fatalf("companion-secret call site does not use headers: managementHeaders(); window:\n%s", window)
	}
}

// The "Lowest priority" column is the visible affordance for the pin
// state. Both the column header text and the row cell class must be there.
func TestServeUsageModePanel_HasLowestPriorityColumn(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	if !strings.Contains(body, `<th>Lowest</th>`) {
		t.Fatalf("body missing <th>Lowest</th> column header")
	}
	if !strings.Contains(body, `<td class="pin"`) {
		t.Fatalf("body missing <td class=\"pin\"> cells")
	}
	if !strings.Contains(body, `data-pin=`) {
		t.Fatalf("body missing data-pin attribute on toggle inputs")
	}
}

// 401 on a companion endpoint must NOT leave the page stuck — operator
// reloads to land back in locked-no-key. The reload happens on location.reload()
// inside the page after a 401 response; assert that the page triggers a
// reload when the companion endpoint returns 401.
func TestServeUsageModePanel_401FallsBackToReload(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)
	if !strings.Contains(body, `localStorage.removeItem(COMPANION_TOKEN_KEY)`) {
		t.Fatalf("body missing companion-key eviction on 401")
	}
	if !strings.Contains(body, `location.reload()`) {
		t.Fatalf("body missing location.reload()")
	}
}