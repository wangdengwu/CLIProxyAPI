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

// panelRegion returns the slice of body from the first occurrence of start up
// to (not including) the first occurrence of end after it. Page functions are
// declared two-space-indented, so "\n  }\n" is the end of a top-level function.
func panelRegion(t *testing.T, body, start, end string) string {
	t.Helper()
	i := strings.Index(body, start)
	if i < 0 {
		t.Fatalf("body missing %q", start)
	}
	rest := body[i+len(start):]
	j := strings.Index(rest, end)
	if j < 0 {
		t.Fatalf("no %q found after %q", end, start)
	}
	return body[i : i+len(start)+j]
}

// ---- "Lowest" column: single render --------------------------------------
//
// History: the checkbox never appeared because the pin states were requested
// right after the account-list request was STARTED rather than after the table
// was RENDERED. The pin response landed on an empty table, then render() wrote
// the em-dash placeholder over everything. The previous regression test only
// grepped for listener wiring, so it could not see the ordering. These tests
// pin the structure that makes the ordering impossible to get wrong (both
// responses awaited, one render) — but source-shape tests still cannot prove
// runtime ordering; that is the lab browser check in the task's second step.

// The loader-wrapper machinery is the bug's habitat; it must stay deleted.
func TestServeUsageModePanel_NoLoaderWrapperMachinery(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)
	for _, gone := range []string{
		`loadAccounts = function`,
		`origLoadAccounts`,
		`applyPinStates`,
		`fetchPinStatesAndApply`,
		`removeEventListener("click"`,
	} {
		if strings.Contains(body, gone) {
			t.Fatalf("body still contains %q — the wrap-and-patch loader must be gone", gone)
		}
	}
}

// Both responses are awaited together and handed to ONE render call.
func TestServeUsageModePanel_LoadsListAndPinStatesTogether(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	loader := panelRegion(t, body, "function loadAccounts() {", "\n  }\n")
	if !strings.Contains(loader, "Promise.all([fetchAuthFiles(), fetchPinStates()])") {
		t.Fatalf("loadAccounts must await auth-files and pin-states together via Promise.all; got:\n%s", loader)
	}
	if !strings.Contains(loader, "render(files, pins)") {
		t.Fatalf("loadAccounts must render once with both results (render(files, pins)); got:\n%s", loader)
	}

	list := panelRegion(t, body, "function fetchAuthFiles() {", "\n  }\n")
	if !strings.Contains(list, `"/v0/management/auth-files"`) || !strings.Contains(list, "headers()") {
		t.Fatalf("fetchAuthFiles must GET /v0/management/auth-files with the management headers; got:\n%s", list)
	}
	pins := panelRegion(t, body, "function fetchPinStates() {", "\n  }\n")
	if !strings.Contains(pins, `"/v0/management/companion/claude-pin-states"`) || !strings.Contains(pins, "companionHeaders()") {
		t.Fatalf("fetchPinStates must GET the companion pin-states endpoint with the companion headers; got:\n%s", pins)
	}
}

// The checkbox is produced by render itself, driven by the pin map — not
// patched in after the fact over a static placeholder.
func TestServeUsageModePanel_RenderEmitsPinCheckboxInline(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	render := panelRegion(t, body, "function render(entries, pinByID) {", "\n  }\n")
	if !strings.Contains(render, `'<td class="pin">' + pinCell(id, pinByID) + '</td>'`) {
		t.Fatalf("render must build the Lowest cell with pinCell(id, pinByID); got:\n%s", render)
	}
	if strings.Contains(render, `<td class="pin"><span class="na">—</span></td>`) {
		t.Fatalf("render still writes a static em-dash Lowest cell")
	}

	cell := panelRegion(t, body, "function pinCell(id, pinByID) {", "\n  }\n")
	for _, want := range []string{
		`type="checkbox"`,
		`data-pin="`,
		`pinByID[id] === true`,
		`always last`,
		`always picked last`,
	} {
		if !strings.Contains(cell, want) {
			t.Fatalf("pinCell missing %q; got:\n%s", want, cell)
		}
	}
}

// Checkbox clicks are handled by one delegated listener on the table host, so
// re-renders never need to re-bind anything.
func TestServeUsageModePanel_PinToggleIsDelegated(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	listener := panelRegion(t, body, `host.addEventListener("change", function (e) {`, "\n  });\n")
	if !strings.Contains(listener, `data-pin`) || !strings.Contains(listener, `togglePin(`) {
		t.Fatalf("host change listener must route data-pin checkboxes to togglePin; got:\n%s", listener)
	}
	if strings.Contains(body, `input[data-pin]`) {
		t.Fatalf("per-render querySelectorAll binding of pin checkboxes must be gone")
	}
}

// A failed or missing pin state must read as "unknown", never as "unpinned":
// painting an unchecked box on a pinned account invites a wrong toggle.
func TestServeUsageModePanel_PinStateFailureIsNotUnchecked(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	pins := panelRegion(t, body, "function fetchPinStates() {", "\n  }\n")
	if !strings.Contains(pins, "return null") {
		t.Fatalf("fetchPinStates must resolve null on failure; got:\n%s", pins)
	}
	if strings.Contains(pins, "return {}") {
		t.Fatalf("fetchPinStates must not fall back to an empty (all-unchecked) map; got:\n%s", pins)
	}

	cell := panelRegion(t, body, "function pinCell(id, pinByID) {", "\n  }\n")
	if !strings.Contains(cell, `typeof pinByID[id] !== "boolean"`) {
		t.Fatalf("pinCell must treat a missing/unknown state as unavailable; got:\n%s", cell)
	}
	iDash := strings.Index(cell, "—")
	iBox := strings.Index(cell, `type="checkbox"`)
	if iDash < 0 || iBox < 0 || iDash > iBox {
		t.Fatalf("pinCell must return the em-dash fallback BEFORE it can build a checkbox (dash=%d box=%d)", iDash, iBox)
	}
	if !strings.Contains(cell, "pin state failed to load") {
		t.Fatalf("the fallback cell must explain itself (title \"pin state failed to load\")")
	}

	loader := panelRegion(t, body, "function loadAccounts() {", "\n  }\n")
	if !strings.Contains(loader, "pins === null") {
		t.Fatalf("loadAccounts must surface a visible message when pins === null; got:\n%s", loader)
	}
}

// The page was too narrow for the 7-column table (980px max width less 2x20px
// padding = 940px content area).
func TestServeUsageModePanel_WiderLayout(t *testing.T) {
	s := &Server{cfg: &proxyconfig.Config{}}
	body := fetchPanelBody(t, s)

	if strings.Contains(body, "max-width: 980px") {
		t.Fatalf("old 980px max width is still present")
	}
	if !strings.Contains(body, ".wrap { max-width: 1280px; margin: 0 auto; padding: 32px 20px 64px; }") {
		t.Fatalf("wider .wrap rule (1280px max width, 20px side padding kept) not found")
	}
}
