package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// LoadConfig hashes a plaintext companion-secret exactly the way it hashes
// secret-key: the operator's cleartext never lives on disk longer than the
// first save, and a config that already carries a bcrypt hash is left alone.
// Without this, every comparison the middleware does would be against a
// raw secret the operator typed.
func TestLoadConfig_CompanionSecret_HashesPlaintextOnFirstLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "port: 8317\nremote-management:\n  companion-secret: \"my-cleartext\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if !looksLikeBcrypt(cfg.RemoteManagement.CompanionSecret) {
		t.Fatalf("stored value after load = %q, want bcrypt shape", cfg.RemoteManagement.CompanionSecret)
	}
	if cfg.RemoteManagement.CompanionSecret == "my-cleartext" {
		t.Fatalf("plaintext survived into the loaded config")
	}

	// Persisted back to disk so the next start does not re-hash.
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if strings.Contains(string(disk), "my-cleartext") {
		t.Fatalf("plaintext leaked back to disk:\n%s", string(disk))
	}
}

// A bcrypt-hashed companion-secret on disk must NOT be re-hashed on load —
// bcrypt-of-bcrypt is unrecoverable, so this is the silent footgun we are
// guarding against.
func TestLoadConfig_CompanionSecret_LeavesBcryptAlone(t *testing.T) {
	hashed, err := hashSecret("my-cleartext")
	if err != nil {
		t.Fatalf("seed hash: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yaml")
	yaml := "port: 8317\nremote-management:\n  companion-secret: \"" + hashed + "\"\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.RemoteManagement.CompanionSecret != hashed {
		t.Fatalf("bcrypt value was rewritten: got %q, want %q", cfg.RemoteManagement.CompanionSecret, hashed)
	}
}

// Empty companion-secret must stay empty — the "page unlocked" state is a
// real production state (first-run install), not just a test convenience.
func TestLoadConfig_CompanionSecret_EmptyStaysEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if cfg.RemoteManagement.CompanionSecret != "" {
		t.Fatalf("empty field was rewritten to %q", cfg.RemoteManagement.CompanionSecret)
	}
}
