package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

// When the claude-ratelimit-alert section is absent, all fields fall back to defaults.
func TestClaudeRatelimitAlertDefaultsWhenAbsent(t *testing.T) {
	path := writeTempConfig(t, "port: 8080\n")
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	rl := cfg.ClaudeRatelimitAlert
	if !rl.Enabled {
		t.Errorf("Enabled = false, want true (default)")
	}
	if rl.WebhookURL != "" {
		t.Errorf("WebhookURL = %q, want empty (default)", rl.WebhookURL)
	}
	if rl.DefaultUsageMode != "shared" {
		t.Errorf("DefaultUsageMode = %q, want shared (default)", rl.DefaultUsageMode)
	}
	if rl.Timezone != "Asia/Shanghai" {
		t.Errorf("Timezone = %q, want Asia/Shanghai (default)", rl.Timezone)
	}
	shared := rl.Shared
	if shared.DayBlockThreshold != 0.80 {
		t.Errorf("Shared.DayBlockThreshold = %v, want 0.80 (default)", shared.DayBlockThreshold)
	}
	if shared.NightBlockThreshold != 0.98 {
		t.Errorf("Shared.NightBlockThreshold = %v, want 0.98 (default)", shared.NightBlockThreshold)
	}
	if shared.SevenDaySoftStart != 0.70 {
		t.Errorf("Shared.SevenDaySoftStart = %v, want 0.70 (default)", shared.SevenDaySoftStart)
	}
	if shared.SevenDayHardCap != 0.98 {
		t.Errorf("Shared.SevenDayHardCap = %v, want 0.98 (default)", shared.SevenDayHardCap)
	}
	if shared.MinBlockThreshold != 0.03 {
		t.Errorf("Shared.MinBlockThreshold = %v, want 0.03 (default)", shared.MinBlockThreshold)
	}
	if shared.AlertMargin != 0.05 {
		t.Errorf("Shared.AlertMargin = %v, want 0.05 (default)", shared.AlertMargin)
	}
	if shared.NightStart != "22:00" || shared.NightEnd != "08:00" {
		t.Errorf("Shared night window = %q-%q, want 22:00-08:00", shared.NightStart, shared.NightEnd)
	}
	if rl.Cooldown != "5m" {
		t.Errorf("Cooldown = %q, want 5m (default)", rl.Cooldown)
	}
}

// Values present in YAML override the defaults, including enabled=false.
func TestClaudeRatelimitAlertOverridesFromYAML(t *testing.T) {
	path := writeTempConfig(t, `
claude-ratelimit-alert:
  enabled: false
  webhook-url: "https://example.com/hook"
  default-usage-mode: "dedicated"
  timezone: "UTC"
  shared:
    day-block-threshold: 0.60
    night-block-threshold: 0.95
    seven-day-soft-start: 0.55
    seven-day-hard-cap: 0.97
    min-block-threshold: 0.01
    alert-margin: 0.08
    night-start: "23:00"
    night-end: "07:00"
  cooldown: "10m"
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	rl := cfg.ClaudeRatelimitAlert
	if rl.Enabled {
		t.Errorf("Enabled = true, want false (explicit override)")
	}
	if rl.WebhookURL != "https://example.com/hook" {
		t.Errorf("WebhookURL = %q, want https://example.com/hook", rl.WebhookURL)
	}
	if rl.DefaultUsageMode != "dedicated" {
		t.Errorf("DefaultUsageMode = %q, want dedicated", rl.DefaultUsageMode)
	}
	if rl.Timezone != "UTC" {
		t.Errorf("Timezone = %q, want UTC", rl.Timezone)
	}
	shared := rl.Shared
	if shared.DayBlockThreshold != 0.60 {
		t.Errorf("Shared.DayBlockThreshold = %v, want 0.60", shared.DayBlockThreshold)
	}
	if shared.NightBlockThreshold != 0.95 {
		t.Errorf("Shared.NightBlockThreshold = %v, want 0.95", shared.NightBlockThreshold)
	}
	if shared.SevenDaySoftStart != 0.55 {
		t.Errorf("Shared.SevenDaySoftStart = %v, want 0.55", shared.SevenDaySoftStart)
	}
	if shared.SevenDayHardCap != 0.97 {
		t.Errorf("Shared.SevenDayHardCap = %v, want 0.97", shared.SevenDayHardCap)
	}
	if shared.MinBlockThreshold != 0.01 {
		t.Errorf("Shared.MinBlockThreshold = %v, want 0.01", shared.MinBlockThreshold)
	}
	if shared.AlertMargin != 0.08 {
		t.Errorf("Shared.AlertMargin = %v, want 0.08", shared.AlertMargin)
	}
	if shared.NightStart != "23:00" || shared.NightEnd != "07:00" {
		t.Errorf("Shared night window = %q-%q, want 23:00-07:00", shared.NightStart, shared.NightEnd)
	}
	if rl.Cooldown != "10m" {
		t.Errorf("Cooldown = %q, want 10m", rl.Cooldown)
	}
}

// A partially-specified section keeps defaults for the omitted fields.
func TestClaudeRatelimitAlertPartialKeepsDefaults(t *testing.T) {
	path := writeTempConfig(t, `
claude-ratelimit-alert:
  webhook-url: "https://example.com/hook"
`)
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	rl := cfg.ClaudeRatelimitAlert
	if !rl.Enabled {
		t.Errorf("Enabled = false, want true (default retained)")
	}
	if rl.DefaultUsageMode != "shared" {
		t.Errorf("DefaultUsageMode = %q, want shared (default retained)", rl.DefaultUsageMode)
	}
	if rl.Timezone != "Asia/Shanghai" {
		t.Errorf("Timezone = %q, want Asia/Shanghai (default retained)", rl.Timezone)
	}
	shared := rl.Shared
	if shared.DayBlockThreshold != 0.80 {
		t.Errorf("Shared.DayBlockThreshold = %v, want 0.80 (default retained)", shared.DayBlockThreshold)
	}
	if shared.NightBlockThreshold != 0.98 {
		t.Errorf("Shared.NightBlockThreshold = %v, want 0.98 (default retained)", shared.NightBlockThreshold)
	}
	if shared.NightStart != "22:00" || shared.NightEnd != "08:00" {
		t.Errorf("Shared night window = %q-%q, want 22:00-08:00 (default retained)", shared.NightStart, shared.NightEnd)
	}
	if rl.Cooldown != "5m" {
		t.Errorf("Cooldown = %q, want 5m (default retained)", rl.Cooldown)
	}
	if rl.WebhookURL != "https://example.com/hook" {
		t.Errorf("WebhookURL = %q, want overridden value", rl.WebhookURL)
	}
}
