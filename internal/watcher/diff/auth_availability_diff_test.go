package diff

import (
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// containsChange reports whether any change line has the given prefix.
func containsChange(changes []string, prefix string) bool {
	for _, change := range changes {
		if strings.HasPrefix(change, prefix) {
			return true
		}
	}
	return false
}

// availabilityCfg builds a config carrying only auth-availability settings.
func availabilityCfg(enabled bool, timezone string) *config.Config {
	cfg := &config.Config{}
	cfg.AuthAvailability.Enabled = enabled
	cfg.AuthAvailability.Timezone = timezone
	return cfg
}

// Hot reload must surface availability changes. Without this the operator flips the
// kill switch, sees no acknowledgement in the logs, and cannot tell whether the reload
// took — for the one control whose whole purpose is recovering from a bad night.
func TestBuildConfigChangeDetails_AuthAvailability(t *testing.T) {
	tests := []struct {
		name       string
		oldCfg     *config.Config
		newCfg     *config.Config
		wantPrefix string
	}{
		{
			name:       "kill switch flipped off",
			oldCfg:     availabilityCfg(true, "Asia/Shanghai"),
			newCfg:     availabilityCfg(false, "Asia/Shanghai"),
			wantPrefix: "auth-availability.enabled:",
		},
		{
			name:       "kill switch flipped on",
			oldCfg:     availabilityCfg(false, "Asia/Shanghai"),
			newCfg:     availabilityCfg(true, "Asia/Shanghai"),
			wantPrefix: "auth-availability.enabled:",
		},
		{
			name:       "timezone changed",
			oldCfg:     availabilityCfg(true, "Asia/Shanghai"),
			newCfg:     availabilityCfg(true, "UTC"),
			wantPrefix: "auth-availability.timezone:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := BuildConfigChangeDetails(tt.oldCfg, tt.newCfg)
			if !containsChange(changes, tt.wantPrefix) {
				t.Errorf("changes = %v, want one starting with %q", changes, tt.wantPrefix)
			}
		})
	}
}

// An unchanged availability section must not produce noise on every reload.
func TestBuildConfigChangeDetails_AuthAvailabilityUnchanged(t *testing.T) {
	cfg := availabilityCfg(true, "Asia/Shanghai")
	changes := BuildConfigChangeDetails(cfg, availabilityCfg(true, "Asia/Shanghai"))
	if containsChange(changes, "auth-availability.") {
		t.Errorf("changes = %v, want no auth-availability entries", changes)
	}
}
