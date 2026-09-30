package config

import (
	"os"
	"path/filepath"
	"testing"
)

// LoadConfig backfills panel-github-repository with the default repository whenever it is
// absent or blank, so "the field is empty" is never a signal a loaded config can carry. Any
// decision keyed on emptiness is dead code in production while still passing tests built on
// a hand-constructed Config — which is exactly how a shipped release ended up inert.
func TestPanelRepositoryOverridden_ThroughRealConfigLoad(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want bool
	}{
		{
			name: "key absent",
			yaml: "port: 8317\n",
			want: false,
		},
		{
			name: "key present but blank",
			yaml: "port: 8317\nremote-management:\n  panel-github-repository: \"\"\n",
			want: false,
		},
		{
			name: "key set to the default repository",
			yaml: "port: 8317\nremote-management:\n  panel-github-repository: \"" + DefaultPanelGitHubRepository + "\"\n",
			want: false,
		},
		{
			name: "key set to a different repository",
			yaml: "port: 8317\nremote-management:\n  panel-github-repository: \"https://github.com/acme/panel\"\n",
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			cfg, err := LoadConfig(path)
			if err != nil {
				t.Fatalf("load config: %v", err)
			}
			if got := cfg.RemoteManagement.PanelRepositoryOverridden(); got != tc.want {
				t.Fatalf("PanelRepositoryOverridden = %v, want %v (loaded value %q)",
					got, tc.want, cfg.RemoteManagement.PanelGitHubRepository)
			}
		})
	}
}

// Trailing whitespace around the default is still the default, not a deliberate override.
func TestPanelRepositoryOverridden_DefaultWithWhitespace(t *testing.T) {
	rm := RemoteManagement{PanelGitHubRepository: "  " + DefaultPanelGitHubRepository + "  "}
	if rm.PanelRepositoryOverridden() {
		t.Fatal("padded default repository was treated as an override")
	}
}
