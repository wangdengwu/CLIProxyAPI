package auth

import (
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
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
	m := activeRatelimitTarget.Load()
	if m == nil {
		// Diagnostic: block requested but no Manager registered itself as the active
		// target. In a normal single-Manager deployment this should never happen; if it
		// does, the block silently does nothing.
		log.WithField("auth_id", authID).Warn("claude ratelimit block skipped: no active manager")
		return
	}
	m.applyRatelimitBlock(authID, resetAt)
}

// applyRatelimitBlock marks the auth account-level unavailable until resetAt, mirroring
// the locked-write pattern MarkResult uses: mutate m.auths[authID] under m.mu, then
// refresh the scheduler so selection skips the auth until resetAt (and auto-recovers
// afterwards). Not persisted — the block is process-local.
func (m *Manager) applyRatelimitBlock(authID string, resetAt time.Time) {
	if m == nil || authID == "" {
		return
	}
	if resetAt.IsZero() || !resetAt.After(time.Now()) {
		log.WithFields(log.Fields{"auth_id": authID, "reset": resetAt.Format(time.RFC3339)}).
			Warn("claude ratelimit block skipped: reset is zero or already past")
		return
	}
	now := time.Now()
	var snapshot *Auth
	found := false
	m.mu.Lock()
	if auth, ok := m.auths[authID]; ok && auth != nil {
		found = true
		// RatelimitBlockUntil is the durable source of truth for this block: it is
		// checked directly by the selector and is never recomputed from ModelStates,
		// so a subsequent successful-request MarkResult (which resets ModelStates and
		// recomputes Unavailable) cannot clear it. The aggregate fields below are set
		// for immediate effect and status visibility only.
		auth.RatelimitBlockUntil = resetAt
		auth.Unavailable = true
		auth.Status = StatusError
		auth.StatusMessage = "claude 5h rate limit reached; blocked until window reset"
		auth.NextRetryAfter = resetAt
		auth.UpdatedAt = now
		snapshot = auth.Clone()
	}
	m.mu.Unlock()
	if !found {
		// Diagnostic: the executor asked to block an auth ID that the Manager does not
		// have in its map. Indicates an ID mismatch or that the auth set was reloaded.
		log.WithField("auth_id", authID).Warn("claude ratelimit block skipped: auth id not found in manager")
		return
	}
	if snapshot != nil && m.scheduler != nil {
		m.scheduler.upsertAuth(snapshot)
	}
	log.WithFields(log.Fields{
		"auth_id":               authID,
		"ratelimit_block_until": resetAt.Format(time.RFC3339),
	}).Info("claude ratelimit block applied")
}
