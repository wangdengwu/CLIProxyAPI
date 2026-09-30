package managementasset

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// The background updater exists to keep an on-disk copy of the control panel fresh. With no
// panel repository configured the panel served to operators is the one embedded in the
// binary, so anything this updater downloads is never read — it would only burn bandwidth
// and keep hitting GitHub's per-IP rate limit, which is noise in the startup log.
func TestPanelUpdaterSkipReason(t *testing.T) {
	newCfg := func(mutate func(*config.Config)) *config.Config {
		cfg := &config.Config{}
		// A repository override is what makes the updater relevant at all; each case below
		// starts from "relevant" and takes away one thing.
		cfg.RemoteManagement.PanelGitHubRepository = "https://github.com/acme/panel"
		if mutate != nil {
			mutate(cfg)
		}
		return cfg
	}

	cases := []struct {
		name       string
		cfg        *config.Config
		wantSkip   bool
		reasonHint string
	}{
		{
			name:       "config not loaded yet",
			cfg:        nil,
			wantSkip:   true,
			reasonHint: "config",
		},
		{
			name:       "control panel disabled",
			cfg:        newCfg(func(c *config.Config) { c.RemoteManagement.DisableControlPanel = true }),
			wantSkip:   true,
			reasonHint: "control panel",
		},
		{
			name:       "auto update disabled",
			cfg:        newCfg(func(c *config.Config) { c.RemoteManagement.DisableAutoUpdatePanel = true }),
			wantSkip:   true,
			reasonHint: "disable-auto-update-panel",
		},
		{
			name:       "no repository override: the embedded panel is what gets served",
			cfg:        newCfg(func(c *config.Config) { c.RemoteManagement.PanelGitHubRepository = "" }),
			wantSkip:   true,
			reasonHint: "built-in",
		},
		{
			name:     "repository override and nothing disabled",
			cfg:      newCfg(nil),
			wantSkip: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := panelUpdaterSkipReason(tc.cfg)
			if gotSkip := reason != ""; gotSkip != tc.wantSkip {
				t.Fatalf("panelUpdaterSkipReason = %q (skip=%v), want skip=%v", reason, gotSkip, tc.wantSkip)
			}
			if tc.reasonHint != "" && !strings.Contains(reason, tc.reasonHint) {
				t.Fatalf("skip reason = %q, want it to mention %q", reason, tc.reasonHint)
			}
		})
	}
}

// Whitespace is not a repository. Treating "  " as an override would send the updater at
// GitHub's default repository and quietly overwrite nothing anybody reads.
func TestPanelUpdaterSkipReason_BlankRepositoryIsNoOverride(t *testing.T) {
	cfg := &config.Config{}
	cfg.RemoteManagement.PanelGitHubRepository = "   "
	if reason := panelUpdaterSkipReason(cfg); reason == "" {
		t.Fatal("blank panel-github-repository was treated as an override")
	}
}

// The config this updater sees at runtime comes from LoadConfig, which backfills
// panel-github-repository with the default repository — so a config built by hand can show
// this updater standing down while the real one runs on every tick. That gap shipped once.
func TestPanelUpdaterSkipReason_ThroughRealConfigLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.RemoteManagement.PanelGitHubRepository == "" {
		t.Fatal("precondition lost: LoadConfig no longer backfills the panel repository, so this test no longer guards anything")
	}
	if reason := panelUpdaterSkipReason(cfg); reason == "" {
		t.Fatal("updater would run for a config that never asked for a panel repository")
	}
}
