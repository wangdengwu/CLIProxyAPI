package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
)

// Pinned-lowest accounts must share the absolute-bottom priority bucket with
// each other. They sit below every non-pinned Claude account, including
// accounts whose numeric priority has been raised above. The existing
// round-robin within a bucket picks among them.
//
// We test by marking the non-pinned account as unavailable — then every
// remaining pick MUST come from the pinned bucket, distributed between the
// two pinned accounts.
func TestSchedulerPick_PinnedLowestClaudeAccountsRoundRobinBelowNonPinned(t *testing.T) {
	t.Parallel()

	const model = "claude-sonnet"
	registerSchedulerModels(t, "claude", model, "regular", "pinned-a", "pinned-b")

	regular := &Auth{
		ID:       "regular",
		Provider: "claude",
		Attributes: map[string]string{
			"priority": "1",
		},
		ModelStates: map[string]*ModelState{
			model: unavailableState(),
		},
	}
	// Two pinned accounts — one with a huge numeric priority (would otherwise
	// dominate) and one with no numeric priority at all. Both must land in
	// the same bottom bucket and round-robin among themselves.
	pinnedA := &Auth{
		ID:       "pinned-a",
		Provider: "claude",
		Attributes: map[string]string{
			"priority":             "999",
			"claude_pin_to_lowest": "true",
		},
	}
	pinnedB := &Auth{
		ID:       "pinned-b",
		Provider: "claude",
		Attributes: map[string]string{
			"claude_pin_to_lowest": "true",
		},
	}

	scheduler := newSchedulerForTest(&RoundRobinSelector{}, regular, pinnedA, pinnedB)

	picks := map[string]int{}
	for i := 0; i < 6; i++ {
		got, errPick := scheduler.pickSingle(context.Background(), "claude", model, cliproxyexecutor.Options{}, nil)
		if errPick != nil {
			t.Fatalf("pick #%d error = %v", i, errPick)
		}
		if got == nil {
			t.Fatalf("pick #%d nil", i)
		}
		picks[got.ID]++
	}
	if picks["regular"] > 0 {
		t.Fatalf("regular was marked unavailable but got %d picks from it: %v", picks["regular"], picks)
	}
	if picks["pinned-a"]+picks["pinned-b"] != 6 {
		t.Fatalf("expected 6 picks split between pinned-a/pinned-b, got %v", picks)
	}
	if picks["pinned-a"] == 0 || picks["pinned-b"] == 0 {
		t.Fatalf("expected both pinned accounts to be picked (round-robin), got %v", picks)
	}
}

// Pinned-lowest must NOT be the highest bucket even when the non-pinned
// account has a lower number than the pinned one. This is the absolute-floor
// guarantee: pinning always beats the numeric.
func TestSchedulerPick_PinnedLowestBeatsHigherNumericPriority(t *testing.T) {
	t.Parallel()

	const model = "claude-sonnet"
	registerSchedulerModels(t, "claude", model, "low-numeric", "pinned-high")

	lowNumeric := &Auth{
		ID:       "low-numeric",
		Provider: "claude",
		Attributes: map[string]string{
			"priority": "1",
		},
	}
	pinnedHigh := &Auth{
		ID:       "pinned-high",
		Provider: "claude",
		Attributes: map[string]string{
			"priority":             "9999",
			"claude_pin_to_lowest": "true",
		},
	}

	scheduler := newSchedulerForTest(&RoundRobinSelector{}, lowNumeric, pinnedHigh)

	// All picks should be lowNumeric — it's the only one in the top bucket.
	// The pinned one stays in math.MinInt's bucket and only gets picked if
	// we exhaust that one. Since lowNumeric is alone in its bucket and
	// RoundRobinSelector keeps cycling, pinned-high never appears.
	for i := 0; i < 3; i++ {
		got, errPick := scheduler.pickSingle(context.Background(), "claude", model, cliproxyexecutor.Options{}, nil)
		if errPick != nil {
			t.Fatalf("pick #%d error = %v", i, errPick)
		}
		if got == nil {
			t.Fatalf("pick #%d nil", i)
		}
		if got.ID != "low-numeric" {
			t.Fatalf("pick #%d auth.ID = %q, want low-numeric (pin must lose to non-pinned even with priority=9999)", i, got.ID)
		}
	}
}

// Helper: build an unavailable ModelState pointing an hour into the future
// so the scheduler treats the auth as on cooldown for the duration of the
// test.
func unavailableState() *ModelState {
	return &ModelState{
		Status:         StatusError,
		Unavailable:    true,
		NextRetryAfter: time.Now().Add(time.Hour),
	}
}
