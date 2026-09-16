package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// servedPanel returns the companion page body as served over HTTP, so these assertions
// cover what an operator's browser actually receives rather than a file on disk.
func servedPanel(t *testing.T) string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := &Server{cfg: &proxyconfig.Config{}}

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/usage-mode.html", nil)
	s.serveUsageModePanel(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// The availability-window editor must actually reach the browser and be wired to the
// right endpoint and field. The repo has no JS test infrastructure, so these markup
// assertions are the automated floor; behaviour in a real browser is confirmed
// manually on lab.
func TestUsageModePanel_ServesAvailabilityWindowEditor(t *testing.T) {
	body := servedPanel(t)

	required := []struct {
		name    string
		snippet string
	}{
		{name: "window column header", snippet: "<th>Window</th>"},
		{name: "window input is rendered per row", snippet: `data-window="`},
		{name: "input carries its saved value for dirty-checking", snippet: `data-current="`},
		{name: "placeholder states the default", snippet: `placeholder="all day"`},
		{name: "value goes through the null-safe normalizer", snippet: "escapeHtml(windowValue(raw))"},
		{name: "normalizer lives in the verifiable pure block", snippet: "function windowValue(raw)"},
		{name: "saves via the management fields endpoint", snippet: "/v0/management/auth-files/fields"},
		{name: "sends the available_window field", snippet: "available_window: next"},
		{name: "change is delegated so it survives re-render", snippet: `host.addEventListener("change"`},
		{name: "reverts the control when the server rejects it", snippet: "input.value = current"},
	}

	for _, tt := range required {
		if !strings.Contains(body, tt.snippet) {
			t.Errorf("%s: served page does not contain %q", tt.name, tt.snippet)
		}
	}
}

// The window editor must not disturb what was already on the page.
func TestUsageModePanel_RetainsExistingControls(t *testing.T) {
	body := servedPanel(t)

	for _, snippet := range []string{
		"claude_usage_mode: next", // the shared/dedicated toggle
		`data-load="`,             // on-demand usage loading
		"<th>5h</th>",
		"<th>7d</th>",
	} {
		if !strings.Contains(body, snippet) {
			t.Errorf("existing control lost: served page no longer contains %q", snippet)
		}
	}

	// Usage must still NOT be fetched for every row on load — fanning out can itself
	// trip upstream rate limits, which is why loading is per-row and on demand.
	if strings.Contains(body, "claude.forEach(function (e) { fetchUsage") {
		t.Error("page auto-fetches usage for every account on render")
	}
}

// windowValue is the one piece of new display logic, and its job is to keep absent or
// malformed API values from reaching the DOM as "undefined". Go cannot execute it, so
// pin its source shape: the type guard must come before any string operation.
func TestUsageModePanel_WindowValueGuardsNonStrings(t *testing.T) {
	body := servedPanel(t)

	start := strings.Index(body, "function windowValue(raw)")
	if start < 0 {
		t.Fatal("windowValue not found in served page")
	}
	end := strings.Index(body[start:], "\n  }")
	if end < 0 {
		t.Fatal("could not delimit windowValue body")
	}
	fn := body[start : start+end]

	guard := strings.Index(fn, `typeof raw !== "string"`)
	if guard < 0 {
		t.Fatal("windowValue does not type-guard its input; a non-string would render as \"undefined\"")
	}
	if trim := strings.Index(fn, ".trim()"); trim >= 0 && trim < guard {
		t.Error("windowValue calls .trim() before the type guard; a null value would throw")
	}
}
