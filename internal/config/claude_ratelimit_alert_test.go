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
	if rl.AlertThreshold != 0.80 {
		t.Errorf("AlertThreshold = %v, want 0.80 (default)", rl.AlertThreshold)
	}
	if rl.BlockThreshold != 0.85 {
		t.Errorf("BlockThreshold = %v, want 0.85 (default)", rl.BlockThreshold)
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
  alert-threshold: 0.5
  block-threshold: 0.9
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
	if rl.AlertThreshold != 0.5 {
		t.Errorf("AlertThreshold = %v, want 0.5", rl.AlertThreshold)
	}
	if rl.BlockThreshold != 0.9 {
		t.Errorf("BlockThreshold = %v, want 0.9", rl.BlockThreshold)
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
	if rl.AlertThreshold != 0.80 {
		t.Errorf("AlertThreshold = %v, want 0.80 (default retained)", rl.AlertThreshold)
	}
	if rl.BlockThreshold != 0.85 {
		t.Errorf("BlockThreshold = %v, want 0.85 (default retained)", rl.BlockThreshold)
	}
	if rl.Cooldown != "5m" {
		t.Errorf("Cooldown = %q, want 5m (default retained)", rl.Cooldown)
	}
	if rl.WebhookURL != "https://example.com/hook" {
		t.Errorf("WebhookURL = %q, want overridden value", rl.WebhookURL)
	}
}
