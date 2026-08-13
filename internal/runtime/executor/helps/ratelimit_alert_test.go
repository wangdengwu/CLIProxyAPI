package helps

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func alertCfg(enabled bool, webhook string) *config.Config {
	return &config.Config{ClaudeRatelimitAlert: config.ClaudeRatelimitAlert{
		Enabled:    enabled,
		WebhookURL: webhook,
		Cooldown:   "5m",
	}}
}

func win(util float64, status string, resetUnix int64) *ClaudeRatelimitWindow {
	return &ClaudeRatelimitWindow{UsedRatio: util, Status: status, Reset: time.Unix(resetUnix, 0)}
}

const (
	testCooldown = 5 * time.Minute
	testResetA   = int64(1782884400)
	testResetB   = int64(1782902400) // a different 5h window
)

func sharedAlertDecision(alertThreshold float64) ClaudeRatelimitDecision {
	return ClaudeRatelimitDecision{
		Mode:              ClaudeUsageModeShared,
		IsNight:           false,
		SevenDayPresent:   true,
		FiveHourThreshold: alertThreshold + 0.05,
		AlertThreshold:    alertThreshold,
	}
}

func dedicatedNaturalDecision(window string) ClaudeRatelimitDecision {
	return ClaudeRatelimitDecision{
		Mode:              ClaudeUsageModeDedicated,
		FiveHourThreshold: 1.0,
		AlertThreshold:    1.0,
		NaturalFull:       true,
		NaturalFullWindow: window,
	}
}

// AC: enabled=false -> never dispatches, even when the state is over the water line.
func TestMaybeAlert_DisabledDoesNotDispatch(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: win(0.99, "allowed_warning", testResetA)}
	if MaybeAlertClaudeRatelimit(nil, alertCfg(false, "https://example.com/hook"), &cliproxyauth.Auth{ID: "auth-disabled"}, "m", st, sharedAlertDecision(0.75)) {
		t.Fatal("must not dispatch when feature disabled")
	}
}

// AC: empty webhook URL -> never dispatches.
func TestMaybeAlert_EmptyWebhookDoesNotDispatch(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: win(0.99, "allowed_warning", testResetA)}
	if MaybeAlertClaudeRatelimit(nil, alertCfg(true, "   "), &cliproxyauth.Auth{ID: "auth-nohook"}, "m", st, sharedAlertDecision(0.75)) {
		t.Fatal("must not dispatch when webhook URL is empty/blank")
	}
}

// nil config / nil auth -> no dispatch, no panic.
func TestMaybeAlert_NilConfigOrAuthSafe(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: win(0.99, "rejected", testResetA)}
	if MaybeAlertClaudeRatelimit(nil, nil, &cliproxyauth.Auth{ID: "x"}, "m", st, sharedAlertDecision(0.75)) {
		t.Fatal("nil config must not dispatch")
	}
	if MaybeAlertClaudeRatelimit(nil, alertCfg(true, "https://example.com/hook"), nil, "m", st, sharedAlertDecision(0.75)) {
		t.Fatal("nil auth must not dispatch")
	}
}

// Below-threshold state with the feature fully enabled -> no dispatch (debounce says no).
func TestMaybeAlert_BelowThresholdNoDispatch(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: win(0.5, "allowed", testResetA)}
	if MaybeAlertClaudeRatelimit(nil, alertCfg(true, "https://example.com/hook"), &cliproxyauth.Auth{ID: "auth-below"}, "m", st, sharedAlertDecision(0.75)) {
		t.Fatal("below threshold must not dispatch")
	}
}

// Enabled + webhook set + over the water line -> dispatches exactly once per window,
// and the async send actually reaches the webhook (pointed at a local test server so
// no real network call is made).
func TestMaybeAlert_EnabledCrossingDispatchesOnce(t *testing.T) {
	received := make(chan struct{}, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
		received <- struct{}{}
	}))
	defer srv.Close()

	st := ClaudeRatelimitState{FiveHour: win(0.85, "allowed_warning", testResetA)}
	auth := &cliproxyauth.Auth{ID: "auth-dispatch-once"}
	cfg := alertCfg(true, srv.URL)

	if !MaybeAlertClaudeRatelimit(nil, cfg, auth, "m", st, sharedAlertDecision(0.75)) {
		t.Fatal("first crossing with enabled+webhook must dispatch")
	}
	select {
	case <-received:
	case <-time.After(2 * time.Second):
		t.Fatal("expected async webhook send to reach the server")
	}

	// same window, same tier -> deduped, no second dispatch
	if MaybeAlertClaudeRatelimit(nil, cfg, auth, "m", st, sharedAlertDecision(0.75)) {
		t.Fatal("same window/tier must not dispatch again")
	}
	select {
	case <-received:
		t.Fatal("must not send a second time in same window/tier")
	case <-time.After(200 * time.Millisecond):
	}
}

// AC: 5h first crosses the alert water line -> alert exactly once.
func TestShouldAlert_FirstCrossFires(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	level, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(0.81, "allowed_warning", testResetA)}, sharedAlertDecision(0.80), testCooldown, now)
	if !ok {
		t.Fatal("expected alert to fire on first crossing")
	}
	if level != ClaudeRatelimitLevelAlert {
		t.Fatalf("level = %q, want %q", level, ClaudeRatelimitLevelAlert)
	}
}

// AC: subsequent requests in the SAME window still >= threshold -> no further alert.
func TestShouldAlert_SameWindowNoRepeat(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	st := ClaudeRatelimitState{FiveHour: win(0.85, "allowed_warning", testResetA)}
	if _, ok := a.ShouldAlert("auth-1", st, sharedAlertDecision(0.80), testCooldown, now); !ok {
		t.Fatal("first crossing should fire")
	}
	later := now.Add(30 * time.Minute)
	if _, ok := a.ShouldAlert("auth-1", st, sharedAlertDecision(0.80), testCooldown, later); ok {
		t.Fatal("same window/tier must not re-alert even after cooldown")
	}
}

// AC: after reset timestamp changes (new window), crossing again -> re-alert.
func TestShouldAlert_NewWindowRearms(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	if _, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(0.85, "allowed_warning", testResetA)}, sharedAlertDecision(0.80), testCooldown, now); !ok {
		t.Fatal("first window should fire")
	}
	later := now.Add(6 * time.Hour)
	level, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(0.85, "allowed_warning", testResetB)}, sharedAlertDecision(0.80), testCooldown, later)
	if !ok {
		t.Fatal("new window should re-arm and fire")
	}
	if level != ClaudeRatelimitLevelAlert {
		t.Fatalf("level = %q, want %q", level, ClaudeRatelimitLevelAlert)
	}
}

// AC: "rejected/full" tier and "alert" tier each fire once within the same window.
func TestShouldAlert_RejectedAndAlertTiersEachFireOnce(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	if l, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(0.82, "allowed_warning", testResetA)}, sharedAlertDecision(0.80), testCooldown, now); !ok || l != ClaudeRatelimitLevelAlert {
		t.Fatalf("expected alert tier, got (%q,%v)", l, ok)
	}
	later := now.Add(10 * time.Minute)
	rejected := sharedAlertDecision(0.80)
	rejected.NaturalFull = true
	rejected.NaturalFullWindow = "5h"
	if l, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(1.05, "rejected", testResetA)}, rejected, testCooldown, later); !ok || l != ClaudeRatelimitLevelRejected {
		t.Fatalf("expected rejected tier, got (%q,%v)", l, ok)
	}
	evenLater := later.Add(10 * time.Minute)
	if _, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(1.09, "rejected", testResetA)}, rejected, testCooldown, evenLater); ok {
		t.Fatal("rejected tier must not repeat within same window")
	}
}

// AC: hard cooldown limits push interval for the same account.
func TestShouldAlert_HardCooldownSuppressesWithinInterval(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	if _, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(0.85, "allowed_warning", testResetA)}, sharedAlertDecision(0.80), testCooldown, now); !ok {
		t.Fatal("first should fire")
	}
	soon := now.Add(1 * time.Minute)
	if _, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetB)}, sharedAlertDecision(0.80), testCooldown, soon); ok {
		t.Fatal("hard cooldown must suppress a send within the cooldown interval")
	}
}

// Below the alert threshold -> never fires.
func TestShouldAlert_BelowThreshold(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	if _, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{FiveHour: win(0.5, "allowed", testResetA)}, sharedAlertDecision(0.80), testCooldown, now); ok {
		t.Fatal("below threshold must not alert")
	}
}

// Dedicated account with a 7d natural full should alert even without a 5h window.
func TestShouldAlert_DedicatedNaturalFullAlerts(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	level, ok := a.ShouldAlert("auth-1", ClaudeRatelimitState{SevenDay: win(1.0, "rejected", testResetB)}, dedicatedNaturalDecision("7d"), testCooldown, now)
	if !ok {
		t.Fatal("dedicated account must alert when 7d is naturally full")
	}
	if level != ClaudeRatelimitLevelRejected {
		t.Fatalf("level = %q, want %q", level, ClaudeRatelimitLevelRejected)
	}
}

// Distinct auth IDs keep independent debounce state.
func TestShouldAlert_PerAuthIsolation(t *testing.T) {
	a := NewClaudeRatelimitAlerter()
	now := time.Unix(1782880000, 0)
	st := ClaudeRatelimitState{FiveHour: win(0.85, "allowed_warning", testResetA)}
	if _, ok := a.ShouldAlert("auth-1", st, sharedAlertDecision(0.80), testCooldown, now); !ok {
		t.Fatal("auth-1 first should fire")
	}
	if _, ok := a.ShouldAlert("auth-2", st, sharedAlertDecision(0.80), testCooldown, now); !ok {
		t.Fatal("auth-2 first should fire independently of auth-1")
	}
}

// BuildMarkdown: full 5h + 7d -> valid WeCom markdown JSON carrying account, model,
// a percentage, a 5h marker and a 7d marker; content within 4096 bytes.
func TestBuildClaudeRatelimitMarkdown_Full(t *testing.T) {
	st := ClaudeRatelimitState{
		FiveHour: win(0.98, "allowed_warning", testResetA),
		SevenDay: win(0.12, "allowed", testResetB),
	}
	msg := BuildClaudeRatelimitMarkdown("user@example.com", "claude-opus-4-8", st, sharedAlertDecision(0.75))
	if msg.MsgType != "markdown" {
		t.Fatalf("MsgType = %q, want markdown", msg.MsgType)
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatal("payload is not valid JSON")
	}
	c := msg.Markdown.Content
	for _, want := range []string{"user@example.com", "claude-opus-4-8", "%", "5h", "7d", "shared", "动态 5h 阻断阈值"} {
		if !strings.Contains(c, want) {
			t.Fatalf("content missing %q\ncontent=%s", want, c)
		}
	}
	if len(c) > 4096 {
		t.Fatalf("content length = %d bytes, want <= 4096", len(c))
	}
}

// BuildMarkdown: only 5h -> no 7d usage section, still valid and populated.
func TestBuildClaudeRatelimitMarkdown_FiveHourOnly(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: win(0.90, "allowed_warning", testResetA)}
	msg := BuildClaudeRatelimitMarkdown("user@example.com", "claude-opus-4-8", st, sharedAlertDecision(0.75))
	c := msg.Markdown.Content
	if !strings.Contains(c, "5h") {
		t.Fatalf("content missing 5h section: %s", c)
	}
	if strings.Contains(c, "7d 窗口使用率") {
		t.Fatalf("content must not contain a 7d usage section when 7d absent: %s", c)
	}
	if strings.TrimSpace(c) == "" {
		t.Fatal("content must not be empty")
	}
}

// BuildMarkdown: pathologically long inputs must be clamped to <= 4096 bytes and
// remain valid UTF-8 (no mid-rune truncation).
func TestBuildClaudeRatelimitMarkdown_ClampsTo4096(t *testing.T) {
	longAccount := strings.Repeat("超长账号名", 2000) // multi-byte, way over 4096
	st := ClaudeRatelimitState{FiveHour: win(0.99, "allowed_warning", testResetA)}
	msg := BuildClaudeRatelimitMarkdown(longAccount, strings.Repeat("m", 5000), st, sharedAlertDecision(0.75))
	c := msg.Markdown.Content
	if len(c) > 4096 {
		t.Fatalf("content length = %d bytes, want <= 4096", len(c))
	}
	if !utf8.ValidString(c) {
		t.Fatal("content must remain valid UTF-8 after clamping")
	}
}

// SendWeCom: happy path posts the exact JSON body with the right content-type and
// returns nil on 2xx.
func TestSendWeCom_PostsBodyAndSucceeds(t *testing.T) {
	var gotBody []byte
	var gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()

	msg := BuildClaudeRatelimitMarkdown("user@example.com", "claude-opus-4-8", ClaudeRatelimitState{FiveHour: win(0.9, "allowed_warning", testResetA)}, sharedAlertDecision(0.75))
	if err := SendWeCom(nil, srv.URL, msg); err != nil {
		t.Fatalf("SendWeCom error = %v, want nil", err)
	}
	if !strings.Contains(gotCT, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", gotCT)
	}
	var decoded WeComMessage
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("server received invalid JSON: %v (%s)", err, gotBody)
	}
	if decoded.MsgType != "markdown" || decoded.Markdown.Content == "" {
		t.Fatalf("server received unexpected payload: %+v", decoded)
	}
}

// SendWeCom: non-2xx from the webhook -> returns an error (caller logs it).
func TestSendWeCom_Non2xxReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	msg := BuildClaudeRatelimitMarkdown("a", "m", ClaudeRatelimitState{FiveHour: win(0.9, "rejected", testResetA)}, sharedAlertDecision(0.75))
	if err := SendWeCom(nil, srv.URL, msg); err == nil {
		t.Fatal("expected error on non-2xx response")
	}
}
