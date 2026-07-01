package helps

import "time"

// ShouldBlockClaudeRatelimit reports whether the credential should be temporarily
// blocked based solely on its 5h window, and the time to block until.
//
// It blocks only when the 5h window is present, its used ratio is at or above
// blockThreshold, AND its reset time is known (non-zero) — so missing/unbounded data
// never triggers a block. Only the 5h window participates (the 7d window never blocks).
func ShouldBlockClaudeRatelimit(state ClaudeRatelimitState, blockThreshold float64) (resetAt time.Time, ok bool) {
	w := state.FiveHour
	if w == nil || w.Reset.IsZero() {
		return time.Time{}, false
	}
	if w.UsedRatio >= blockThreshold {
		return w.Reset, true
	}
	return time.Time{}, false
}
