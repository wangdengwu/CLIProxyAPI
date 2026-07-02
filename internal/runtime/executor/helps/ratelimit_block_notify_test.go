package helps

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

const testBlockNotifyThreshold = 0.85

// The core regression: an alert already sent this window (Rule 2/3 would dedup/cooldown a
// second alert) must NOT swallow the distinct block notice. ShouldNotifyBlock fires once,
// independently, bypassing the cooldown.
func TestShouldNotifyBlock_FiresAfterAlertWithinCooldown(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Now()
	st := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetA)}

	// Alert fires first (0.90 >= 0.80).
	if _, ok := a.ShouldAlert("auth-1", st, testAlertThreshold, testCooldown, now); !ok {
		t.Fatal("alert should fire on first crossing")
	}
	// A second alert in the same window is deduped.
	if _, ok := a.ShouldAlert("auth-1", st, testAlertThreshold, testCooldown, now.Add(time.Second)); ok {
		t.Fatal("second alert in same window must be deduped")
	}
	// The block notice must STILL fire, despite the alert moments earlier within cooldown.
	resetAt, ok := a.ShouldNotifyBlock("auth-1", st, testBlockNotifyThreshold)
	if !ok {
		t.Fatal("block notice must fire even though an alert was just sent this window")
	}
	if resetAt.Unix() != testResetA {
		t.Fatalf("block resetAt = %d, want %d", resetAt.Unix(), testResetA)
	}
}

// Once per window; re-arms on a new window.
func TestShouldNotifyBlock_OncePerWindowRearms(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	stA := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetA)}
	if _, ok := a.ShouldNotifyBlock("auth-1", stA, testBlockNotifyThreshold); !ok {
		t.Fatal("first block notice should fire")
	}
	if _, ok := a.ShouldNotifyBlock("auth-1", stA, testBlockNotifyThreshold); ok {
		t.Fatal("second block notice in same window must be deduped")
	}
	stB := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetB)}
	if _, ok := a.ShouldNotifyBlock("auth-1", stB, testBlockNotifyThreshold); !ok {
		t.Fatal("new window must re-arm the block notice")
	}
}

// Below threshold, zero threshold, and missing reset/window must not notify (nil-safe,
// and never fires for accounts that never reported a 5h limit).
func TestShouldNotifyBlock_GuardsNoNotify(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	// below block threshold
	if _, ok := a.ShouldNotifyBlock("a", ClaudeRatelimitState{FiveHour: win(0.50, "allowed", testResetA)}, testBlockNotifyThreshold); ok {
		t.Fatal("below block threshold must not notify")
	}
	// zero threshold disables block notices (matches unset config)
	if _, ok := a.ShouldNotifyBlock("a", ClaudeRatelimitState{FiveHour: win(0.99, "allowed", testResetA)}, 0); ok {
		t.Fatal("zero block threshold must disable block notices")
	}
	// no 5h window (e.g. API-key request without unified headers)
	if _, ok := a.ShouldNotifyBlock("a", ClaudeRatelimitState{}, testBlockNotifyThreshold); ok {
		t.Fatal("missing 5h window must not notify")
	}
	// 5h over threshold but reset unknown -> mirrors ShouldBlock: no notify
	if _, ok := a.ShouldNotifyBlock("a", ClaudeRatelimitState{FiveHour: &ClaudeRatelimitWindow{UsedRatio: 0.99}}, testBlockNotifyThreshold); ok {
		t.Fatal("zero reset must not notify")
	}
}

func TestBuildClaudeRatelimitBlockMarkdown_IncludesBlockUntil(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetA)}
	blockUntil := time.Unix(testResetA, 0)
	msg := BuildClaudeRatelimitBlockMarkdown("user@example.com", "claude-opus-4-8", st, blockUntil)
	c := msg.Markdown.Content
	if !strings.Contains(c, "已阻断") {
		t.Fatalf("block markdown should mention 阻断; got: %s", c)
	}
	if !strings.Contains(c, blockUntil.Format("2006-01-02 15:04:05 MST")) {
		t.Fatalf("block markdown should include block_until time; got: %s", c)
	}
	if msg.MsgType != "markdown" {
		t.Fatalf("msgtype = %s, want markdown", msg.MsgType)
	}
}

// End-to-end via the wire: with a block threshold configured, a block notice is
// dispatched even when the alert for the same window was already sent (deduped).
func TestMaybeAlert_BlockNoticeDispatchesDespiteDedupedAlert(t *testing.T) {
	defaultClaudeRatelimitAlerter = NewClaudeRatelimitAlerter() // isolate process-wide debounce state
	received := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := alertCfg(true, srv.URL)
	cfg.ClaudeRatelimitAlert.BlockThreshold = 0.85
	auth := &cliproxyauth.Auth{ID: "auth-block"}
	st := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetA)}

	// First call: both an alert (>=0.80) and a block notice (>=0.85) dispatch.
	MaybeAlertClaudeRatelimit(nil, cfg, auth, "m", st)

	var gotAlert, gotBlock bool
	for i := 0; i < 2; i++ {
		select {
		case body := <-received:
			if strings.Contains(body, "已阻断") {
				gotBlock = true
			} else {
				gotAlert = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("expected 2 sends (alert+block), got alert=%v block=%v", gotAlert, gotBlock)
		}
	}
	if !gotAlert || !gotBlock {
		t.Fatalf("want both alert and block dispatched; alert=%v block=%v", gotAlert, gotBlock)
	}

	// Second call same window: alert deduped AND block deduped -> no further sends.
	MaybeAlertClaudeRatelimit(nil, cfg, auth, "m", st)
	select {
	case body := <-received:
		t.Fatalf("no send expected on repeat in same window; got: %s", body)
	case <-time.After(250 * time.Millisecond):
	}
}
