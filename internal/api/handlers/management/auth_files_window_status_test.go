package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// listEntries returns the auth-files listing keyed by id.
func listEntries(t *testing.T, records ...*coreauth.Auth) map[string]map[string]any {
	t.Helper()
	t.Setenv("MANAGEMENT_PASSWORD", "")
	gin.SetMode(gin.TestMode)

	manager := coreauth.NewManager(&memoryAuthStore{}, nil, nil)
	for _, record := range records {
		if _, err := manager.Register(context.Background(), record); err != nil {
			t.Fatalf("register %s: %v", record.ID, err)
		}
	}
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager)

	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/v0/management/auth-files", nil)
	h.ListAuthFiles(ctx)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal: %v; body = %s", err, rec.Body.String())
	}
	byID := make(map[string]map[string]any, len(payload.Files))
	for _, entry := range payload.Files {
		id, _ := entry["id"].(string)
		byID[id] = entry
	}
	return byID
}

// windowRelativeToNow builds a window offset from the current instant in the default
// availability zone, so the open/closed verdict is deterministic whenever the suite
// runs. No config is installed here, so the package default (enabled, Asia/Shanghai)
// applies — the same state a freshly started server has before its first reload.
func windowRelativeToNow(t *testing.T, offsetMinutes, durationMinutes int) string {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("Asia/Shanghai unavailable: %v", err)
	}
	local := time.Now().In(loc)
	start := ((local.Hour()*60+local.Minute()+offsetMinutes)%1440 + 1440) % 1440
	end := (start + durationMinutes) % 1440
	return fmt.Sprintf("%02d:%02d-%02d:%02d", start/60, start%60, end/60, end%60)
}

// The page must not compute open/closed itself: the window is anchored to the
// server's configured zone while the browser's zone is arbitrary, so a client-side
// verdict would be confidently wrong for anyone working in another timezone.
func TestListAuthFiles_ReportsWindowStatus(t *testing.T) {
	entries := listEntries(t,
		authWithWindow("closed.json", windowRelativeToNow(t, 60, 60)),
		authWithWindow("open.json", windowRelativeToNow(t, -60, 120)),
		claudeAuth("none.json"),
		authWithWindow("malformed.json", "6pm-9am"),
	)

	t.Run("closed account reports unavailable with a reopen time", func(t *testing.T) {
		entry := entries["closed.json"]
		if v, _ := entry["available_now"].(bool); v {
			t.Errorf("available_now = true, want false for a closed window")
		}
		raw, _ := entry["next_open_at"].(string)
		if raw == "" {
			t.Fatalf("next_open_at missing for a closed account: %v", entry)
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			t.Fatalf("next_open_at = %q, not RFC3339: %v", raw, err)
		}
		if !parsed.After(time.Now()) {
			t.Errorf("next_open_at = %s is not in the future", raw)
		}
		// The timestamp must carry a zone offset — a bare local time would let the
		// browser reinterpret it and contradict the badge beside it.
		if parsed.Location() == time.UTC && !hasOffsetSuffix(raw) {
			t.Errorf("next_open_at = %q carries no usable zone offset", raw)
		}
	})

	t.Run("open account reports available and omits the reopen time", func(t *testing.T) {
		entry := entries["open.json"]
		if v, ok := entry["available_now"].(bool); !ok || !v {
			t.Errorf("available_now = %v, want true for an open window", entry["available_now"])
		}
		if v, exists := entry["next_open_at"]; exists {
			t.Errorf("next_open_at = %v present while open, want omitted", v)
		}
	})

	t.Run("account without a window is always available", func(t *testing.T) {
		entry := entries["none.json"]
		if v, ok := entry["available_now"].(bool); !ok || !v {
			t.Errorf("available_now = %v, want true when no window is set", entry["available_now"])
		}
		if _, exists := entry["next_open_at"]; exists {
			t.Errorf("next_open_at present for an account with no window")
		}
	})

	t.Run("malformed window reports available, matching the gate's fail-open", func(t *testing.T) {
		entry := entries["malformed.json"]
		if v, ok := entry["available_now"].(bool); !ok || !v {
			t.Errorf("available_now = %v, want true — the gate fails open on unparseable values", entry["available_now"])
		}
		// The raw value must still be echoed so the operator can see and fix the typo.
		if v, _ := entry["available_window"].(string); v != "6pm-9am" {
			t.Errorf("available_window = %q, want the raw value echoed back", v)
		}
	})
}

func hasOffsetSuffix(raw string) bool {
	if len(raw) < 6 {
		return false
	}
	tail := raw[len(raw)-6:]
	return raw[len(raw)-1] == 'Z' || tail[0] == '+' || tail[0] == '-'
}

// available_now must be present on every entry, so the page can distinguish "the
// server does not support this yet" (field absent) from "available" (field true).
func TestListAuthFiles_AvailableNowAlwaysPresent(t *testing.T) {
	entries := listEntries(t, claudeAuth("plain.json"))

	if _, ok := entries["plain.json"]["available_now"]; !ok {
		t.Errorf("available_now absent; the page cannot tell an old server from an available account")
	}
}
