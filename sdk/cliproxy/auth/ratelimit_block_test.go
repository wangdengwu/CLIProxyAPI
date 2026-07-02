package auth

import (
	"context"
	"testing"
	"time"
)

func newTestManager() *Manager {
	return NewManager(nil, nil, nil)
}

// applyRatelimitBlock sets Unavailable and NextRetryAfter == the given reset time on
// the stored auth, through the locked map path.
func TestApplyRatelimitBlock_SetsUnavailableAndReset(t *testing.T) {
	m := newTestManager()
	reset := time.Now().Add(3 * time.Hour)
	m.auths["a1"] = &Auth{ID: "a1", Provider: "claude"}

	m.applyRatelimitBlock("a1", reset)

	got := m.auths["a1"]
	if !got.Unavailable {
		t.Fatal("auth should be marked Unavailable")
	}
	if !got.NextRetryAfter.Equal(reset) {
		t.Fatalf("NextRetryAfter = %v, want %v (the 5h reset)", got.NextRetryAfter, reset)
	}
}

// Blocking an unknown auth is a no-op and must not panic.
func TestApplyRatelimitBlock_UnknownAuthNoop(t *testing.T) {
	m := newTestManager()
	m.applyRatelimitBlock("missing", time.Now().Add(time.Hour)) // must not panic
}

// Account-level block must apply to model-specific requests (not just model==""),
// and auto-recover once NextRetryAfter passes.
func TestIsAuthBlockedForModel_AccountLevelBlocksAllModels(t *testing.T) {
	now := time.Unix(1782880000, 0)
	reset := now.Add(2 * time.Hour)
	auth := &Auth{ID: "a1", Provider: "claude", Unavailable: true, NextRetryAfter: reset}

	// model-specific request: must be blocked despite no per-model state
	if blocked, _, _ := isAuthBlockedForModel(auth, "claude-opus-4-8", now); !blocked {
		t.Fatal("account-level block must block a model-specific request")
	}
	// model=="" request: also blocked
	if blocked, _, _ := isAuthBlockedForModel(auth, "", now); !blocked {
		t.Fatal("account-level block must block a model-agnostic request")
	}
	// after reset: auto-recovers for the model request
	if blocked, _, _ := isAuthBlockedForModel(auth, "claude-opus-4-8", reset.Add(time.Second)); blocked {
		t.Fatal("auth must auto-recover once NextRetryAfter passes")
	}
}

// End-to-end via availableAuthsForRouteModel: a blocked account is skipped and a
// healthy one is selected; when all are blocked, selection returns an error; once the
// reset passes, the account returns to the available set.
func TestApplyRatelimitBlock_SelectorSkipsSwitchesAndRecovers(t *testing.T) {
	m := newTestManager()
	reset := time.Now().Add(2 * time.Hour)
	a1 := &Auth{ID: "a1", Provider: "claude"}
	a2 := &Auth{ID: "a2", Provider: "claude"}
	m.auths["a1"] = a1
	m.auths["a2"] = a2

	model := "claude-opus-4-8"

	// block a1
	m.applyRatelimitBlock("a1", reset)

	now := time.Now()
	avail, err := m.availableAuthsForRouteModel([]*Auth{a1, a2}, "claude", model, now)
	if err != nil {
		t.Fatalf("unexpected error with one available auth: %v", err)
	}
	if len(avail) != 1 || avail[0].ID != "a2" {
		t.Fatalf("expected only a2 available, got %v", authIDs(avail))
	}

	// block a2 too -> all blocked -> error
	m.applyRatelimitBlock("a2", reset)
	if _, err := m.availableAuthsForRouteModel([]*Auth{a1, a2}, "claude", model, now); err == nil {
		t.Fatal("expected error when all auths are blocked")
	}

	// after reset -> both available again
	after := reset.Add(time.Minute)
	avail, err = m.availableAuthsForRouteModel([]*Auth{a1, a2}, "claude", model, after)
	if err != nil {
		t.Fatalf("unexpected error after reset: %v", err)
	}
	if len(avail) != 2 {
		t.Fatalf("expected both auths available after reset, got %v", authIDs(avail))
	}
}

// Regression: the account-level block MUST survive a subsequent successful request.
// A maxed Claude account keeps returning HTTP 200 (5h-Status: rejected is a soft
// header, not a 429), so MarkResult(success) fires right after the block and used to
// wipe it by recomputing the aggregate Unavailable flag from the (clean) ModelStates.
// The durable RatelimitBlockUntil field must keep the account blocked regardless.
func TestApplyRatelimitBlock_SurvivesSuccessfulMarkResult(t *testing.T) {
	m := newTestManager()
	reset := time.Now().Add(2 * time.Hour)
	model := "claude-opus-4-8"
	a1 := &Auth{ID: "a1", Provider: "claude"}
	a2 := &Auth{ID: "a2", Provider: "claude"}
	m.auths["a1"] = a1
	m.auths["a2"] = a2

	m.applyRatelimitBlock("a1", reset)

	// A successful request completes on a1 (upstream served despite the rejected 5h
	// header). Before the fix this cleared the block.
	m.MarkResult(context.Background(), Result{AuthID: "a1", Provider: "claude", Model: model, Success: true})

	now := time.Now()
	if blocked, _, _ := isAuthBlockedForModel(m.auths["a1"], model, now); !blocked {
		t.Fatal("a1 must stay blocked after a successful MarkResult")
	}
	avail, err := m.availableAuthsForRouteModel([]*Auth{m.auths["a1"], m.auths["a2"]}, "claude", model, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(avail) != 1 || avail[0].ID != "a2" {
		t.Fatalf("expected only a2 available after block+success, got %v", authIDs(avail))
	}

	// Auto-recovers once the reset passes.
	if blocked, _, _ := isAuthBlockedForModel(m.auths["a1"], model, reset.Add(time.Minute)); blocked {
		t.Fatal("a1 must auto-recover once RatelimitBlockUntil passes")
	}
}

// Accounts that never reported a 5h/7d limit (RatelimitBlockUntil zero) must never be
// blocked by this path — guards the "not all accounts have limits" case.
func TestIsAuthBlockedForModel_ZeroRatelimitBlockNeverBlocks(t *testing.T) {
	now := time.Now()
	auth := &Auth{ID: "nolimit", Provider: "claude"} // RatelimitBlockUntil is zero
	if blocked, _, _ := isAuthBlockedForModel(auth, "claude-opus-4-8", now); blocked {
		t.Fatal("account without a rate-limit block must not be blocked")
	}
	if blocked, _, _ := isAuthBlockedForModel(auth, "", now); blocked {
		t.Fatal("account without a rate-limit block must not be blocked (model-agnostic)")
	}
}

// A zero/past reset must be a no-op — never set a block that is already expired.
func TestApplyRatelimitBlock_ZeroOrPastResetNoop(t *testing.T) {
	m := newTestManager()
	m.auths["a1"] = &Auth{ID: "a1", Provider: "claude"}

	m.applyRatelimitBlock("a1", time.Time{})               // zero
	m.applyRatelimitBlock("a1", time.Now().Add(-time.Hour)) // past

	if got := m.auths["a1"]; got.Unavailable || !got.RatelimitBlockUntil.IsZero() {
		t.Fatalf("zero/past reset must not block: Unavailable=%v RatelimitBlockUntil=%v", got.Unavailable, got.RatelimitBlockUntil)
	}
}

// Concurrent block writes and reads must be race-free (run with -race).
func TestApplyRatelimitBlock_NoRace(t *testing.T) {
	m := newTestManager()
	m.auths["a1"] = &Auth{ID: "a1", Provider: "claude"}
	reset := time.Now().Add(time.Hour)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			m.applyRatelimitBlock("a1", reset)
		}
		close(done)
	}()
	for i := 0; i < 200; i++ {
		_, _ = m.availableAuthsForRouteModel([]*Auth{m.snapshotAuthByID("a1")}, "claude", "claude-opus-4-8", time.Now())
	}
	<-done
}

func authIDs(auths []*Auth) []string {
	ids := make([]string, 0, len(auths))
	for _, a := range auths {
		ids = append(ids, a.ID)
	}
	return ids
}

// snapshotAuthByID returns a locked clone of the auth for concurrent-read safety.
func (m *Manager) snapshotAuthByID(id string) *Auth {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if a, ok := m.auths[id]; ok && a != nil {
		return a.Clone()
	}
	return nil
}
