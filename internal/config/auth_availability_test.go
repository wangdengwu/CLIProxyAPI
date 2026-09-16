package config

import "testing"

// When the auth-availability section is absent, the feature defaults to enabled and
// anchored to Asia/Shanghai. The zero value of Enabled is false, so this default is
// load-order dependent — if it ever regressed, every configured window would silently
// stop taking effect, which is why it is pinned here rather than assumed.
func TestAuthAvailabilityDefaultsWhenAbsent(t *testing.T) {
	path := writeTempConfig(t, "port: 8080\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.AuthAvailability.Enabled {
		t.Errorf("AuthAvailability.Enabled = false, want true (default)")
	}
	if cfg.AuthAvailability.Timezone != "Asia/Shanghai" {
		t.Errorf("AuthAvailability.Timezone = %q, want Asia/Shanghai (default)", cfg.AuthAvailability.Timezone)
	}
}

// An explicit section overrides both fields, including turning the kill switch off.
func TestAuthAvailabilityExplicitOverrides(t *testing.T) {
	path := writeTempConfig(t, `port: 8080
auth-availability:
  enabled: false
  timezone: "America/New_York"
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.AuthAvailability.Enabled {
		t.Errorf("AuthAvailability.Enabled = true, want false (explicit kill switch)")
	}
	if cfg.AuthAvailability.Timezone != "America/New_York" {
		t.Errorf("AuthAvailability.Timezone = %q, want America/New_York", cfg.AuthAvailability.Timezone)
	}
}

// A partial section keeps the untouched field on its default.
func TestAuthAvailabilityPartialSectionRetainsDefaults(t *testing.T) {
	path := writeTempConfig(t, `port: 8080
auth-availability:
  timezone: "UTC"
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !cfg.AuthAvailability.Enabled {
		t.Errorf("AuthAvailability.Enabled = false, want true (default retained)")
	}
	if cfg.AuthAvailability.Timezone != "UTC" {
		t.Errorf("AuthAvailability.Timezone = %q, want UTC", cfg.AuthAvailability.Timezone)
	}
}
