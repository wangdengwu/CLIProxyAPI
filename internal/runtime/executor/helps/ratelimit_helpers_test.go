package helps

import (
	"context"
	"net/http"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// LogClaudeRatelimitState must not panic on an empty state or a nil auth, and must
// be a no-op (no crash) when there is no window data to report.
func TestLogClaudeRatelimitState_EmptyAndNilAreSafe(t *testing.T) {
	// Empty state, nil auth — no-op, no panic.
	LogClaudeRatelimitState(context.Background(), nil, ClaudeRatelimitState{})

	// Populated state, nil auth — must still not panic.
	LogClaudeRatelimitState(context.Background(), nil, ClaudeRatelimitState{
		FiveHour: &ClaudeRatelimitWindow{UsedRatio: 0.9, Status: "allowed_warning", Reset: time.Unix(1782884400, 0)},
	})

	// Populated state with a real auth — must not panic.
	LogClaudeRatelimitState(context.Background(), &cliproxyauth.Auth{ID: "auth-1", Label: "acct"}, ClaudeRatelimitState{
		FiveHour: &ClaudeRatelimitWindow{UsedRatio: 1.09, Status: "rejected"},
		SevenDay: &ClaudeRatelimitWindow{UsedRatio: 0.12, Status: "allowed", Reset: time.Unix(1783429200, 0)},
	})
}

// Full 5h + 7d headers → both windows parsed with correct utilization/status/reset.
// reset is a Unix epoch second string and must decode to the matching time.
func TestParseClaudeRatelimit_Full5hAnd7d(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.98")
	h.Set("Anthropic-Ratelimit-Unified-5h-Status", "allowed_warning")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1782884400")
	h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", "0.12")
	h.Set("Anthropic-Ratelimit-Unified-7d-Status", "allowed")
	h.Set("Anthropic-Ratelimit-Unified-7d-Reset", "1783429200")

	st := ParseClaudeRatelimit(h)

	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil")
	}
	if st.FiveHour.UsedRatio != 0.98 {
		t.Fatalf("5h UsedRatio = %v, want 0.98", st.FiveHour.UsedRatio)
	}
	if st.FiveHour.Status != "allowed_warning" {
		t.Fatalf("5h Status = %q, want allowed_warning", st.FiveHour.Status)
	}
	if !st.FiveHour.Reset.Equal(time.Unix(1782884400, 0)) {
		t.Fatalf("5h Reset = %v, want %v", st.FiveHour.Reset, time.Unix(1782884400, 0))
	}

	if st.SevenDay == nil {
		t.Fatal("SevenDay = nil, want non-nil")
	}
	if st.SevenDay.UsedRatio != 0.12 {
		t.Fatalf("7d UsedRatio = %v, want 0.12", st.SevenDay.UsedRatio)
	}
	if st.SevenDay.Status != "allowed" {
		t.Fatalf("7d Status = %q, want allowed", st.SevenDay.Status)
	}
	if !st.SevenDay.Reset.Equal(time.Unix(1783429200, 0)) {
		t.Fatalf("7d Reset = %v, want %v", st.SevenDay.Reset, time.Unix(1783429200, 0))
	}
}

// Only 5h present (no 7d headers at all) → FiveHour set, SevenDay nil.
func TestParseClaudeRatelimit_FiveHourOnly(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.5")
	h.Set("Anthropic-Ratelimit-Unified-5h-Status", "allowed")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1782884400")

	st := ParseClaudeRatelimit(h)

	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil")
	}
	if st.SevenDay != nil {
		t.Fatalf("SevenDay = %+v, want nil", st.SevenDay)
	}
}

// No unified headers at all → both windows nil, no panic. (e.g. API-key request)
func TestParseClaudeRatelimit_NoHeaders(t *testing.T) {
	st := ParseClaudeRatelimit(http.Header{})
	if st.FiveHour != nil || st.SevenDay != nil {
		t.Fatalf("expected empty state, got 5h=%+v 7d=%+v", st.FiveHour, st.SevenDay)
	}
}

// nil header → empty state, no panic.
func TestParseClaudeRatelimit_NilHeader(t *testing.T) {
	st := ParseClaudeRatelimit(nil)
	if st.FiveHour != nil || st.SevenDay != nil {
		t.Fatalf("expected empty state, got 5h=%+v 7d=%+v", st.FiveHour, st.SevenDay)
	}
}

// Malformed utilization (non-numeric) → that window is NOT reported (nil), so a
// missing/garbage utilization is never silently treated as 0%.
func TestParseClaudeRatelimit_MalformedUtilizationYieldsNilWindow(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "not-a-number")
	h.Set("Anthropic-Ratelimit-Unified-5h-Status", "allowed")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1782884400")

	st := ParseClaudeRatelimit(h)
	if st.FiveHour != nil {
		t.Fatalf("FiveHour = %+v, want nil for malformed utilization", st.FiveHour)
	}
}

// Utilization can exceed 1.0 in production (observed 1.09) and must be preserved
// verbatim — no clamping.
func TestParseClaudeRatelimit_UtilizationAboveOnePreserved(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "1.09")
	h.Set("Anthropic-Ratelimit-Unified-5h-Status", "rejected")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1782884400")

	st := ParseClaudeRatelimit(h)
	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil")
	}
	if st.FiveHour.UsedRatio != 1.09 {
		t.Fatalf("5h UsedRatio = %v, want 1.09 (no clamping)", st.FiveHour.UsedRatio)
	}
	if st.FiveHour.Status != "rejected" {
		t.Fatalf("5h Status = %q, want rejected", st.FiveHour.Status)
	}
}

// A valid utilization with an unparseable reset still yields a usable window
// (ratio preserved); Reset is left zero rather than dropping the whole window.
func TestParseClaudeRatelimit_UnparseableResetKeepsWindowZeroTime(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.42")
	h.Set("Anthropic-Ratelimit-Unified-5h-Status", "allowed")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "garbage")

	st := ParseClaudeRatelimit(h)
	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil (utilization is valid)")
	}
	if st.FiveHour.UsedRatio != 0.42 {
		t.Fatalf("5h UsedRatio = %v, want 0.42", st.FiveHour.UsedRatio)
	}
	if !st.FiveHour.Reset.IsZero() {
		t.Fatalf("5h Reset = %v, want zero time for unparseable reset", st.FiveHour.Reset)
	}
}

// Header lookup is case-insensitive (http.Header canonicalizes), and a window
// with utilization but no status still parses with an empty Status.
func TestParseClaudeRatelimit_MissingStatusEmptyString(t *testing.T) {
	h := http.Header{}
	h.Set("anthropic-ratelimit-unified-5h-utilization", "0.7")
	h.Set("anthropic-ratelimit-unified-5h-reset", "1782884400")

	st := ParseClaudeRatelimit(h)
	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil")
	}
	if st.FiveHour.UsedRatio != 0.7 {
		t.Fatalf("5h UsedRatio = %v, want 0.7", st.FiveHour.UsedRatio)
	}
	if st.FiveHour.Status != "" {
		t.Fatalf("5h Status = %q, want empty", st.FiveHour.Status)
	}
}

// [C-1] Utilization emitted as an integer string ("1" for exactly 100%) must parse
// to 1.0 and yield a present window — this is the block-threshold boundary.
func TestParseClaudeRatelimit_IntegerUtilization(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "1")
	h.Set("Anthropic-Ratelimit-Unified-5h-Status", "rejected")

	st := ParseClaudeRatelimit(h)
	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil for integer utilization")
	}
	if st.FiveHour.UsedRatio != 1.0 {
		t.Fatalf("5h UsedRatio = %v, want 1.0", st.FiveHour.UsedRatio)
	}
}

// [C-2] Utilization of exactly "0" is real data (0% used, window present), NOT
// absence. It must yield a non-nil window with UsedRatio 0 so downstream logic can
// tell "0% used" apart from "no window data" (nil).
func TestParseClaudeRatelimit_ZeroUtilizationIsPresentNotAbsent(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0")
	h.Set("Anthropic-Ratelimit-Unified-5h-Status", "allowed")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1782884400")

	st := ParseClaudeRatelimit(h)
	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil (0% used is present data, not absence)")
	}
	if st.FiveHour.UsedRatio != 0 {
		t.Fatalf("5h UsedRatio = %v, want 0", st.FiveHour.UsedRatio)
	}
}

// [I-2] Reset rendered as float seconds ("1782884400.0") is still a valid Unix
// timestamp and must decode, not fall through to zero time.
func TestParseClaudeRatelimit_FloatSecondsReset(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.99")
	h.Set("Anthropic-Ratelimit-Unified-5h-Status", "allowed_warning")
	h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1782884400.0")

	st := ParseClaudeRatelimit(h)
	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil")
	}
	if !st.FiveHour.Reset.Equal(time.Unix(1782884400, 0)) {
		t.Fatalf("5h Reset = %v, want %v (float-seconds string must decode)", st.FiveHour.Reset, time.Unix(1782884400, 0))
	}
}

// [I-3] The 7d window shares the parse path but its malformed handling was untested:
// a malformed 7d utilization must drop only the 7d window (nil), leaving 5h intact.
func TestParseClaudeRatelimit_SevenDayMalformedIsSymmetric(t *testing.T) {
	h := http.Header{}
	h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", "0.5")
	h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", "not-a-number")
	h.Set("Anthropic-Ratelimit-Unified-7d-Status", "allowed")

	st := ParseClaudeRatelimit(h)
	if st.FiveHour == nil {
		t.Fatal("FiveHour = nil, want non-nil")
	}
	if st.SevenDay != nil {
		t.Fatalf("SevenDay = %+v, want nil for malformed 7d utilization", st.SevenDay)
	}
}
