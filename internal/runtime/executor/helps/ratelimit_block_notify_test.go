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

func blockedDecision(resetUnix int64, reason string) ClaudeRatelimitDecision {
	return ClaudeRatelimitDecision{
		Mode:        ClaudeUsageModeShared,
		Block:       true,
		BlockUntil:  time.Unix(resetUnix, 0),
		BlockReason: reason,
	}
}

// The core regression: an alert already sent this window must NOT swallow the distinct
// block notice. ShouldNotifyBlock fires once, independently, bypassing the cooldown.
func TestShouldNotifyBlock_FiresAfterAlertWithinCooldown(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	st := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetA)}

	decision := sharedAlertDecision(0.80)
	decision.Block = true
	decision.BlockUntil = time.Unix(testResetA, 0)
	decision.BlockReason = ClaudeRatelimitBlockReasonFiveHourThreshold

	if _, ok := a.ShouldAlert("auth-1", st, decision, testCooldown, now); !ok {
		t.Fatal("alert should fire on first crossing")
	}
	if _, ok := a.ShouldAlert("auth-1", st, decision, testCooldown, now.Add(time.Second)); ok {
		t.Fatal("second alert in same window must be deduped")
	}
	resetAt, ok := a.ShouldNotifyBlock("auth-1", decision)
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
	decisionA := blockedDecision(testResetA, ClaudeRatelimitBlockReasonFiveHourThreshold)
	if _, ok := a.ShouldNotifyBlock("auth-1", decisionA); !ok {
		t.Fatal("first block notice should fire")
	}
	if _, ok := a.ShouldNotifyBlock("auth-1", decisionA); ok {
		t.Fatal("second block notice in same window must be deduped")
	}
	decisionB := blockedDecision(testResetB, ClaudeRatelimitBlockReasonFiveHourThreshold)
	if _, ok := a.ShouldNotifyBlock("auth-1", decisionB); !ok {
		t.Fatal("new window must re-arm the block notice")
	}
}

// Missing block decision, zero block-until, and non-blocking decision must not notify.
func TestShouldNotifyBlock_GuardsNoNotify(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	if _, ok := a.ShouldNotifyBlock("a", ClaudeRatelimitDecision{}); ok {
		t.Fatal("missing block decision must not notify")
	}
	if _, ok := a.ShouldNotifyBlock("a", ClaudeRatelimitDecision{Block: true, BlockReason: ClaudeRatelimitBlockReasonFiveHourThreshold}); ok {
		t.Fatal("zero block-until must not notify")
	}
	if _, ok := a.ShouldNotifyBlock("a", sharedAlertDecision(0.80)); ok {
		t.Fatal("non-blocking alert decision must not notify")
	}
}

func TestBuildClaudeRatelimitBlockMarkdown_IncludesReasonAndUntil(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetA)}
	decision := blockedDecision(testResetA, ClaudeRatelimitBlockReasonFiveHourThreshold)
	msg := BuildClaudeRatelimitBlockMarkdown("user@example.com", "claude-opus-4-8", st, decision)
	c := msg.Markdown.Content
	for _, want := range []string{"已阻断", "shared", "动态保护阈值", decision.BlockUntil.Format("2006-01-02 15:04:05 MST")} {
		if !strings.Contains(c, want) {
			t.Fatalf("block markdown should contain %q; got: %s", want, c)
		}
	}
	if msg.MsgType != "markdown" {
		t.Fatalf("msgtype = %s, want markdown", msg.MsgType)
	}
}

// End-to-end via the wire: a block decision dispatches a block notice even when the
// alert for the same window was already sent.
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
	auth := &cliproxyauth.Auth{ID: "auth-block"}
	st := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetA)}
	decision := sharedAlertDecision(0.80)
	decision.Block = true
	decision.BlockUntil = time.Unix(testResetA, 0)
	decision.BlockReason = ClaudeRatelimitBlockReasonFiveHourThreshold

	MaybeAlertClaudeRatelimit(nil, cfg, auth, "m", st, decision)

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

	MaybeAlertClaudeRatelimit(nil, cfg, auth, "m", st, decision)
	select {
	case body := <-received:
		t.Fatalf("no send expected on repeat in same window; got: %s", body)
	case <-time.After(250 * time.Millisecond):
	}
}

// Dedicated natural full dispatches a rejected alert but never a proactive block notice.
func TestMaybeAlert_DedicatedNaturalFullAlertWithoutBlock(t *testing.T) {
	defaultClaudeRatelimitAlerter = NewClaudeRatelimitAlerter()
	received := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		received <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := alertCfg(true, srv.URL)
	auth := &cliproxyauth.Auth{ID: "auth-dedicated"}
	st := ClaudeRatelimitState{SevenDay: win(1.0, "rejected", testResetB)}
	decision := dedicatedNaturalDecision("7d")

	if !MaybeAlertClaudeRatelimit(nil, cfg, auth, "m", st, decision) {
		t.Fatal("dedicated natural full must dispatch an alert")
	}
	select {
	case body := <-received:
		if strings.Contains(body, "已阻断") {
			t.Fatalf("dedicated account must not dispatch a proactive block notice: %s", body)
		}
		if !strings.Contains(body, "dedicated") {
			t.Fatalf("dedicated alert should identify account mode: %s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected dedicated natural-full alert")
	}
}
