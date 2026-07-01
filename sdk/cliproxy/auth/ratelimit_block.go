package auth

import (
	"sync/atomic"
	"time"
)

// activeRatelimitTarget holds the most recently constructed Manager so callers that
// lack a Manager reference (e.g. provider executors, which are invoked by the Manager
// but do not hold a back-reference) can request a utilization-based block through the
// Manager's lock. Best-effort: in the normal single-Manager deployment this is the
// running Manager.
var activeRatelimitTarget atomic.Pointer[Manager]

// ApplyRatelimitBlock marks authID temporarily unavailable until resetAt via the
// active Manager's locked write path. It is a no-op when authID is empty or there is
// no active Manager. The block is in-memory only (not persisted) and auto-recovers
// once resetAt passes, via the selector's existing NextRetryAfter semantics.
func ApplyRatelimitBlock(authID string, resetAt time.Time) {
	if authID == "" {
		return
	}
	if m := activeRatelimitTarget.Load(); m != nil {
		m.applyRatelimitBlock(authID, resetAt)
	}
}

// applyRatelimitBlock marks the auth account-level unavailable until resetAt, mirroring
// the locked-write pattern MarkResult uses: mutate m.auths[authID] under m.mu, then
// refresh the scheduler so selection skips the auth until resetAt (and auto-recovers
// afterwards). Not persisted — the block is process-local.
func (m *Manager) applyRatelimitBlock(authID string, resetAt time.Time) {
	if m == nil || authID == "" {
		return
	}
	now := time.Now()
	var snapshot *Auth
	m.mu.Lock()
	if auth, ok := m.auths[authID]; ok && auth != nil {
		auth.Unavailable = true
		auth.Status = StatusError
		auth.StatusMessage = "claude 5h rate limit reached; blocked until window reset"
		auth.NextRetryAfter = resetAt
		auth.UpdatedAt = now
		snapshot = auth.Clone()
	}
	m.mu.Unlock()
	if snapshot != nil && m.scheduler != nil {
		m.scheduler.upsertAuth(snapshot)
	}
}
