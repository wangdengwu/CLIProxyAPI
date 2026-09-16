package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

// The scheduler evaluates entry state with the real clock at rebuild time, so these
// tests cannot inject `now`. Instead they derive windows from the current wall clock
// with a generous margin, making the verdict deterministic whenever the suite runs.

// minuteOfDayNow returns the current minute-of-day in the configured availability zone.
func minuteOfDayNow(t *testing.T) int {
	t.Helper()
	local := time.Now().In(currentAvailabilitySnapshot().location)
	return local.Hour()*60 + local.Minute()
}

// windowStartingIn builds a window opening offsetMinutes from now and lasting
// durationMinutes, formatted as the operator would write it.
func windowStartingIn(t *testing.T, offsetMinutes, durationMinutes int) string {
	t.Helper()
	start := ((minuteOfDayNow(t)+offsetMinutes)%1440 + 1440) % 1440
	end := (start + durationMinutes) % 1440
	return fmt.Sprintf("%02d:%02d-%02d:%02d", start/60, start%60, end/60, end%60)
}

// closedWindow is guaranteed not to contain the current instant: it opens an hour from
// now and lasts an hour.
func closedWindow(t *testing.T) string {
	t.Helper()
	return windowStartingIn(t, 60, 60)
}

// openWindow is guaranteed to contain the current instant: it opened an hour ago and
// runs for two hours.
func openWindow(t *testing.T) string {
	t.Helper()
	return windowStartingIn(t, -60, 120)
}

// errorBodyOf decodes the JSON envelope an unavailability error renders.
func errorBodyOf(t *testing.T, err error) map[string]any {
	t.Helper()
	var payload map[string]any
	if errUnmarshal := json.Unmarshal([]byte(err.Error()), &payload); errUnmarshal != nil {
		t.Fatalf("error body is not JSON: %v; body = %q", errUnmarshal, err.Error())
	}
	body, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("error payload missing error object: %v", payload)
	}
	return body
}

// TestSchedulerPick_AllWindowsClosedReportsWhenItReopens exercises the production
// path. useSchedulerFastPath() is true for the built-in selectors the Manager defaults
// to, so this — not the legacy path — is what real traffic hits. Without it the
// operator sees a bare "no auth available" and cannot tell "nothing configured" from
// "everyone is off the clock until 18:00".
func TestSchedulerPick_AllWindowsClosedReportsWhenItReopens(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	window := closedWindow(t)
	scheduler := newSchedulerForTest(
		&RoundRobinSelector{},
		&Auth{ID: "a", Provider: "gemini", Attributes: map[string]string{"available_window": window}},
		&Auth{ID: "b", Provider: "gemini", Attributes: map[string]string{"available_window": window}},
	)

	_, err := scheduler.pickSingle(context.Background(), "gemini", "", cliproxyexecutor.Options{}, nil)
	if err == nil {
		t.Fatalf("pickSingle() error = nil, want an error when every window is closed")
	}

	var unavailable *modelCooldownError
	if !errors.As(err, &unavailable) {
		t.Fatalf("pickSingle() error = %T (%v), want the 429 carrying a reopen time", err, err)
	}
	if got := unavailable.StatusCode(); got != http.StatusTooManyRequests {
		t.Errorf("StatusCode() = %d, want %d", got, http.StatusTooManyRequests)
	}

	body := errorBodyOf(t, unavailable)
	if got, _ := body["code"].(string); got != availabilityWindowErrorCode {
		t.Errorf("error.code = %q, want %q", got, availabilityWindowErrorCode)
	}

	retryAfter := unavailable.Headers().Get("Retry-After")
	seconds, errParse := strconv.Atoi(retryAfter)
	if errParse != nil {
		t.Fatalf("Retry-After = %q, not an integer", retryAfter)
	}
	// The window opens an hour out; allow slack for test execution time.
	if seconds < 3000 || seconds > 3600 {
		t.Errorf("Retry-After = %ds, want roughly one hour (3000..3600)", seconds)
	}
}

// TestSchedulerPick_OneOpenWindowServesNormally is the counterweight: the error path
// must not fire while anyone is still on the clock.
func TestSchedulerPick_OneOpenWindowServesNormally(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	scheduler := newSchedulerForTest(
		&RoundRobinSelector{},
		&Auth{ID: "closed", Provider: "gemini", Attributes: map[string]string{"available_window": closedWindow(t)}},
		&Auth{ID: "open", Provider: "gemini", Attributes: map[string]string{"available_window": openWindow(t)}},
	)

	got, err := scheduler.pickSingle(context.Background(), "gemini", "", cliproxyexecutor.Options{}, nil)
	if err != nil {
		t.Fatalf("pickSingle() error = %v, want the open account", err)
	}
	if got == nil || got.ID != "open" {
		t.Fatalf("pickSingle() = %v, want the auth whose window is open", got)
	}
}

// TestGetAvailableAuths_AllWindowsClosedReportsWhenItReopens covers the legacy
// selector path, which a custom (non-built-in) selector still reaches.
func TestGetAvailableAuths_AllWindowsClosedReportsWhenItReopens(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	now := time.Now()
	auths := []*Auth{
		{ID: "a", Attributes: map[string]string{"available_window": closedWindow(t)}},
		{ID: "b", Attributes: map[string]string{"available_window": closedWindow(t)}},
	}

	_, err := getAvailableAuths(auths, "claude", "claude-opus-4-8", now)
	if err == nil {
		t.Fatalf("getAvailableAuths() error = nil, want an error when every window is closed")
	}

	var unavailable *modelCooldownError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %T (%v), want the 429 carrying a reopen time", err, err)
	}
	body := errorBodyOf(t, unavailable)
	if got, _ := body["code"].(string); got != availabilityWindowErrorCode {
		t.Errorf("error.code = %q, want %q", got, availabilityWindowErrorCode)
	}
}

// TestNextOpenAt covers the reopen-time calculation directly, with an injected clock —
// the scheduler tests above cannot control `now`, so the exact arithmetic is pinned here.
func TestNextOpenAt(t *testing.T) {
	loc := shanghai(t)

	tests := []struct {
		name       string
		window     string
		nowHour    int
		nowMinute  int
		wantDay    int
		wantHour   int
		wantMinute int
	}{
		{
			name:   "same-day window, now before it opens today",
			window: "09:00-18:00", nowHour: 3, nowMinute: 0,
			wantDay: 16, wantHour: 9, wantMinute: 0,
		},
		{
			name:   "same-day window, now after it closed, opens tomorrow",
			window: "09:00-18:00", nowHour: 20, nowMinute: 0,
			wantDay: 17, wantHour: 9, wantMinute: 0,
		},
		{
			name:   "overnight window, now in the midday gap, opens this evening",
			window: "18:00-09:00", nowHour: 12, nowMinute: 0,
			wantDay: 16, wantHour: 18, wantMinute: 0,
		},
		{
			name:   "overnight window, now just before it opens",
			window: "18:00-09:00", nowHour: 17, nowMinute: 59,
			wantDay: 16, wantHour: 18, wantMinute: 0,
		},
		{
			name:   "window ending at midnight, now after it closed",
			window: "18:00-24:00", nowHour: 1, nowMinute: 0,
			wantDay: 16, wantHour: 18, wantMinute: 0,
		},
		{
			name:   "non-zero minutes are preserved",
			window: "09:30-18:15", nowHour: 20, nowMinute: 0,
			wantDay: 17, wantHour: 9, wantMinute: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			window, ok := parseAvailabilityWindow(tt.window)
			if !ok {
				t.Fatalf("parseAvailabilityWindow(%q) failed", tt.window)
			}
			now := time.Date(2026, 9, 16, tt.nowHour, tt.nowMinute, 0, 0, loc)
			got := window.nextOpenAt(loc, now)

			if got.Day() != tt.wantDay || got.Hour() != tt.wantHour || got.Minute() != tt.wantMinute {
				t.Errorf("nextOpenAt = %s, want day %d at %02d:%02d", got.Format(time.RFC3339), tt.wantDay, tt.wantHour, tt.wantMinute)
			}
			if !got.After(now) {
				t.Errorf("nextOpenAt = %s is not after now = %s", got, now)
			}
		})
	}
}

// TestNextOpenAt_AcrossDSTTransition is why the instant is built by calendar date
// rather than by adding a fixed duration. On the US spring-forward day the clock jumps
// 02:00 -> 03:00, so "tomorrow at 10:00" is 23 hours away, not 24. Adding 24h would
// report 11:00 — an hour late, every spring. Asia/Shanghai has no DST today, but the
// timezone is operator-configurable.
func TestNextOpenAt_AcrossDSTTransition(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York unavailable: %v", err)
	}

	window, ok := parseAvailabilityWindow("10:00-12:00")
	if !ok {
		t.Fatalf("parse failed")
	}

	// 2026-03-08 is the US spring-forward date. Start the evening before, after the
	// window has closed, so the next opening is on the far side of the transition.
	now := time.Date(2026, 3, 7, 13, 0, 0, 0, loc)
	got := window.nextOpenAt(loc, now)

	if got.Day() != 8 || got.Hour() != 10 || got.Minute() != 0 {
		t.Fatalf("nextOpenAt = %s, want 2026-03-08 10:00 local", got.Format(time.RFC3339))
	}
	// The correct gap is 20h (21 calendar hours minus the hour DST eats). A naive
	// now.Add(24h) would land on 13:00 the next day instead.
	if gap := got.Sub(now); gap != 20*time.Hour {
		t.Errorf("gap = %v, want 20h — the DST hour was not accounted for", gap)
	}
}

// TestSchedulerPick_EarliestReopenWins pins that the reported time belongs to whichever
// account comes back first, not an arbitrary one.
func TestSchedulerPick_EarliestReopenWins(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	scheduler := newSchedulerForTest(
		&RoundRobinSelector{},
		&Auth{ID: "late", Provider: "gemini", Attributes: map[string]string{"available_window": windowStartingIn(t, 180, 60)}},
		&Auth{ID: "soon", Provider: "gemini", Attributes: map[string]string{"available_window": windowStartingIn(t, 60, 60)}},
	)

	_, err := scheduler.pickSingle(context.Background(), "gemini", "", cliproxyexecutor.Options{}, nil)
	if err == nil {
		t.Fatalf("pickSingle() error = nil, want closed-window error")
	}
	var unavailable *modelCooldownError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %T, want the timed 429", err)
	}

	seconds, errParse := strconv.Atoi(unavailable.Headers().Get("Retry-After"))
	if errParse != nil {
		t.Fatalf("Retry-After not an integer: %v", errParse)
	}
	// Must track the account reopening in ~1h, not the one in ~3h.
	if seconds < 3000 || seconds > 3600 {
		t.Errorf("Retry-After = %ds, want ~1h (the earliest reopen), not the later one", seconds)
	}
}

// TestSchedulerPick_KillSwitchSuppressesTheError pins that flipping the switch off
// restores normal service rather than merely changing the error text.
func TestSchedulerPick_KillSwitchSuppressesTheError(t *testing.T) {
	cfg := availabilityConfigFor("Asia/Shanghai")
	cfg.AuthAvailability.Enabled = false
	withAvailabilityConfig(t, cfg)

	scheduler := newSchedulerForTest(
		&RoundRobinSelector{},
		&Auth{ID: "a", Provider: "gemini", Attributes: map[string]string{"available_window": closedWindow(t)}},
	)

	got, err := scheduler.pickSingle(context.Background(), "gemini", "", cliproxyexecutor.Options{}, nil)
	if err != nil {
		t.Fatalf("pickSingle() error = %v, want the account served with the kill switch off", err)
	}
	if got == nil || got.ID != "a" {
		t.Fatalf("pickSingle() = %v, want auth a", got)
	}
}

// TestGetAvailableAuths_MixedCausesReportCooldown pins the tie-break. When some
// accounts are rate-limited and others are merely off the clock, the error still
// carries a time — but names cooldown, the more conservative cause. Claiming
// "everyone is outside their window" would send an operator hunting a scheduling
// misconfiguration when the real problem is upstream rate limits.
func TestGetAvailableAuths_MixedCausesReportCooldown(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	now := time.Now()
	model := "claude-opus-4-8"
	cooldownUntil := now.Add(30 * time.Minute)

	auths := []*Auth{
		{ID: "windowed", Attributes: map[string]string{"available_window": closedWindow(t)}},
		{
			ID: "ratelimited",
			ModelStates: map[string]*ModelState{
				model: {
					Status:         StatusActive,
					Unavailable:    true,
					NextRetryAfter: cooldownUntil,
					Quota:          QuotaState{Exceeded: true, NextRecoverAt: cooldownUntil},
				},
			},
		},
	}

	_, err := getAvailableAuths(auths, "claude", model, now)
	if err == nil {
		t.Fatalf("getAvailableAuths() error = nil, want an error")
	}
	var unavailable *modelCooldownError
	if !errors.As(err, &unavailable) {
		t.Fatalf("error = %T (%v), want the timed 429", err, err)
	}
	body := errorBodyOf(t, unavailable)
	if got, _ := body["code"].(string); got != cooldownErrorCode {
		t.Errorf("error.code = %q, want %q for a mixed cause", got, cooldownErrorCode)
	}
	// It must still report the earliest recovery — here the 30-minute cooldown.
	seconds, errParse := strconv.Atoi(unavailable.Headers().Get("Retry-After"))
	if errParse != nil {
		t.Fatalf("Retry-After not an integer: %v", errParse)
	}
	if seconds < 1500 || seconds > 1800 {
		t.Errorf("Retry-After = %ds, want ~30m (the earliest of the two causes)", seconds)
	}
}

// TestGetAvailableAuths_WindowPlusNonRecoverableFallsBack pins the honest limitation
// recorded in the task brief: a rate-limit block reports as blockReasonOther and
// carries no recovery time, so mixing it with a closed window still degrades to the
// untimed error. Pinned so the boundary is visible rather than discovered in an incident.
func TestGetAvailableAuths_WindowPlusNonRecoverableFallsBack(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	now := time.Now()
	auths := []*Auth{
		{ID: "windowed", Attributes: map[string]string{"available_window": closedWindow(t)}},
		{ID: "ratelimit-blocked", RatelimitBlockUntil: now.Add(2 * time.Hour)},
	}

	_, err := getAvailableAuths(auths, "claude", "claude-opus-4-8", now)
	if err == nil {
		t.Fatalf("getAvailableAuths() error = nil, want an error")
	}
	var unavailable *modelCooldownError
	if errors.As(err, &unavailable) {
		t.Fatalf("error = timed 429; expected the untimed fallback for this mix")
	}
	var authErr *Error
	if !errors.As(err, &authErr) || authErr.Code != "auth_unavailable" {
		t.Errorf("error = %v, want auth_unavailable", err)
	}
}
