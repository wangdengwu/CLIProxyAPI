package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gin "github.com/gin-gonic/gin"
	proxyconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// Requests go through a real gin engine rather than a bare test context: gin's
// ResponseWriter buffers the status code until the engine flushes it, so a handler that
// writes 304 and no body looks like a 200 to a bare recorder. Serving through the engine
// makes every assertion below an assertion about the actual HTTP response.
func serveManagementPanel(t *testing.T, s *Server, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	engine := gin.New()
	engine.GET("/management.html", s.serveManagementControlPanel)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec
}

// The control panel asset ships inside the binary. Without an explicit panel repository
// override the handler must serve those bytes — no disk read, no download.
func TestServeManagementControlPanel_ServesEmbeddedAssetByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{cfg: &proxyconfig.Config{}}

	rec := serveManagementPanel(t, s, httptest.NewRequest(http.MethodGet, "/management.html", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type = %q, want text/html", ct)
	}
	if got, want := rec.Body.Len(), len(managementPanelHTML); got != want {
		t.Fatalf("body length = %d, want %d (the embedded asset)", got, want)
	}
}

// The reason this whole change exists: panel v1.25.0 moved to /v8/management, which this
// backend does not serve, and the auto-updater silently installed it. Whoever swaps the
// embedded asset for a v8-era panel must go red here rather than in an operator's browser.
func TestEmbeddedManagementPanel_SpeaksV0NotV8(t *testing.T) {
	if !strings.Contains(managementPanelHTML, "/v0/management") {
		t.Fatal("embedded panel does not reference /v0/management")
	}
	if strings.Contains(managementPanelHTML, "/v8/management") {
		t.Fatal("embedded panel references /v8/management — this backend only serves v0")
	}
}

// The recorded digest is also the ETag, so a silent asset swap would hand out a stale
// validator and browsers would keep a cached panel forever.
func TestEmbeddedManagementPanel_MatchesPinnedSHA256(t *testing.T) {
	sum := sha256.Sum256([]byte(managementPanelHTML))
	if got := hex.EncodeToString(sum[:]); got != pinnedManagementPanelSHA256 {
		t.Fatalf("embedded asset sha256 = %s, want %s", got, pinnedManagementPanelSHA256)
	}
}

// Serving 2.7MB on every visit is what the previous c.File path avoided for free. The
// digest is a strong validator, so a repeat visit must collapse to an empty 304.
func TestServeManagementControlPanel_ConditionalRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{cfg: &proxyconfig.Config{}}

	rec := serveManagementPanel(t, s, httptest.NewRequest(http.MethodGet, "/management.html", nil))

	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on the embedded panel response")
	}
	if !strings.Contains(etag, pinnedManagementPanelSHA256) {
		t.Fatalf("ETag = %q, want it to carry the pinned digest", etag)
	}

	conditional := httptest.NewRequest(http.MethodGet, "/management.html", nil)
	conditional.Header.Set("If-None-Match", etag)
	rec2 := serveManagementPanel(t, s, conditional)

	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304 when If-None-Match matches", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Fatalf("304 carried %d bytes of body, want none", rec2.Body.Len())
	}
}

// Disabling the control panel must still gate the panel off entirely — embedding the asset
// does not create a way around that switch.
func TestServeManagementControlPanel_GatedOffWhenControlPanelDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &proxyconfig.Config{}
	cfg.RemoteManagement.DisableControlPanel = true
	s := &Server{cfg: cfg}

	rec := serveManagementPanel(t, s, httptest.NewRequest(http.MethodGet, "/management.html", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when control panel disabled", rec.Code)
	}
}

// The escape hatch: naming a panel repository means "track that repository's latest
// release", so the handler goes back to serving whatever the updater put on disk. The file
// is pre-created, so this exercises the disk branch without any network access.
func TestServeManagementControlPanel_PanelRepositoryOverrideServesDiskAsset(t *testing.T) {
	gin.SetMode(gin.TestMode)
	staticDir := t.TempDir()
	const marker = "<html><body>asset from disk</body></html>"
	if err := os.WriteFile(filepath.Join(staticDir, "management.html"), []byte(marker), 0o644); err != nil {
		t.Fatalf("seed on-disk asset: %v", err)
	}
	t.Setenv("MANAGEMENT_STATIC_PATH", staticDir)

	cfg := &proxyconfig.Config{}
	cfg.RemoteManagement.PanelGitHubRepository = "https://github.com/acme/panel"
	s := &Server{cfg: cfg}

	rec := serveManagementPanel(t, s, httptest.NewRequest(http.MethodGet, "/management.html", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	// Truncated on purpose: when this assertion fails the body is the 2.7MB embedded asset,
	// and dumping it buries the failure.
	if body := rec.Body.String(); body != marker {
		if len(body) > 120 {
			body = body[:120] + "…"
		}
		t.Fatalf("body = %q, want the on-disk asset", body)
	}
}

// The default path must not touch the static directory at all: no read, and above all no
// write. A download would land here, so an untouched directory is the observable proof
// that serving the panel no longer depends on the network.
func TestServeManagementControlPanel_DefaultPathLeavesStaticDirUntouched(t *testing.T) {
	gin.SetMode(gin.TestMode)
	staticDir := t.TempDir()
	t.Setenv("MANAGEMENT_STATIC_PATH", staticDir)

	s := &Server{cfg: &proxyconfig.Config{}}

	rec := serveManagementPanel(t, s, httptest.NewRequest(http.MethodGet, "/management.html", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != len(managementPanelHTML) {
		t.Fatalf("body length = %d, want the embedded asset (%d)", rec.Body.Len(), len(managementPanelHTML))
	}
	entries, err := os.ReadDir(staticDir)
	if err != nil {
		t.Fatalf("read static dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("static dir gained %d entries, want none", len(entries))
	}
}

// The config that reaches the server at runtime comes from LoadConfig, which backfills
// panel-github-repository with the default repository. A hand-built Config therefore proves
// nothing about production: the first release of this change shipped with the default path
// unreachable and every unit test green. This one goes through the real loader.
func TestServeManagementControlPanel_LoadedConfigWithoutOverrideServesEmbedded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte("port: 8317\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := proxyconfig.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.RemoteManagement.PanelGitHubRepository == "" {
		t.Fatal("precondition lost: LoadConfig no longer backfills the panel repository, so this test no longer guards anything")
	}

	staticDir := t.TempDir()
	t.Setenv("MANAGEMENT_STATIC_PATH", staticDir)
	s := &Server{cfg: cfg, configFilePath: configPath}

	rec := serveManagementPanel(t, s, httptest.NewRequest(http.MethodGet, "/management.html", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != len(managementPanelHTML) {
		t.Fatalf("body length = %d, want the embedded asset (%d)", rec.Body.Len(), len(managementPanelHTML))
	}
	entries, err := os.ReadDir(staticDir)
	if err != nil {
		t.Fatalf("read static dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("static dir gained %d entries, want none", len(entries))
	}
}

// Which panel an operator is looking at is otherwise invisible at runtime; this string is
// what the startup log prints.
func TestManagementPanelSource(t *testing.T) {
	disabled := &proxyconfig.Config{}
	disabled.RemoteManagement.DisableControlPanel = true
	overridden := &proxyconfig.Config{}
	overridden.RemoteManagement.PanelGitHubRepository = "https://github.com/acme/panel"
	defaulted := &proxyconfig.Config{}
	defaulted.RemoteManagement.PanelGitHubRepository = proxyconfig.DefaultPanelGitHubRepository

	cases := []struct {
		name string
		cfg  *proxyconfig.Config
		want []string
	}{
		{name: "nil config", cfg: nil, want: []string{"disabled"}},
		{name: "control panel disabled", cfg: disabled, want: []string{"disabled"}},
		{name: "default", cfg: &proxyconfig.Config{}, want: []string{"built-in", pinnedManagementPanelVersion}},
		{name: "default repository spelled out is not an override", cfg: defaulted, want: []string{"built-in", pinnedManagementPanelVersion}},
		{name: "repository override", cfg: overridden, want: []string{"https://github.com/acme/panel", "auto-update"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := managementPanelSource(tc.cfg)
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("managementPanelSource = %q, want it to mention %q", got, want)
				}
			}
		})
	}
}
