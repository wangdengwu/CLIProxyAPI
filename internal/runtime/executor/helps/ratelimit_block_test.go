package helps

import (
	"testing"
	"time"
)

const testBlockThreshold = 0.85

// >= block threshold with a known reset -> block until reset.
func TestShouldBlockClaudeRatelimit_AtOrAboveThreshold(t *testing.T) {
	reset := time.Unix(1782884400, 0)
	st := ClaudeRatelimitState{FiveHour: &ClaudeRatelimitWindow{UsedRatio: 0.90, Status: "rejected", Reset: reset}}
	resetAt, ok := ShouldBlockClaudeRatelimit(st, testBlockThreshold)
	if !ok {
		t.Fatal("expected block at 0.90 >= 0.85")
	}
	if !resetAt.Equal(reset) {
		t.Fatalf("resetAt = %v, want %v (the 5h reset)", resetAt, reset)
	}
}

// Exactly at the threshold -> block (>=).
func TestShouldBlockClaudeRatelimit_ExactlyAtThreshold(t *testing.T) {
	reset := time.Unix(1782884400, 0)
	st := ClaudeRatelimitState{FiveHour: &ClaudeRatelimitWindow{UsedRatio: 0.85, Reset: reset}}
	if _, ok := ShouldBlockClaudeRatelimit(st, testBlockThreshold); !ok {
		t.Fatal("expected block at exactly the threshold")
	}
}

// Below threshold -> no block.
func TestShouldBlockClaudeRatelimit_BelowThreshold(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: &ClaudeRatelimitWindow{UsedRatio: 0.84, Reset: time.Unix(1782884400, 0)}}
	if _, ok := ShouldBlockClaudeRatelimit(st, testBlockThreshold); ok {
		t.Fatal("must not block below threshold")
	}
}

// No 5h window -> no block (missing data must not cause a block).
func TestShouldBlockClaudeRatelimit_NoFiveHour(t *testing.T) {
	if _, ok := ShouldBlockClaudeRatelimit(ClaudeRatelimitState{}, testBlockThreshold); ok {
		t.Fatal("must not block when 5h window absent")
	}
}

// 5h over threshold but reset unknown (zero) -> no block: without a bounded recovery
// time we must not block indefinitely.
func TestShouldBlockClaudeRatelimit_ZeroResetNoBlock(t *testing.T) {
	st := ClaudeRatelimitState{FiveHour: &ClaudeRatelimitWindow{UsedRatio: 0.99, Status: "rejected"}}
	if _, ok := ShouldBlockClaudeRatelimit(st, testBlockThreshold); ok {
		t.Fatal("must not block when reset time is unknown")
	}
}

// 7d over threshold must not trigger a block — only 5h drives blocking.
func TestShouldBlockClaudeRatelimit_SevenDayIgnored(t *testing.T) {
	st := ClaudeRatelimitState{
		FiveHour: &ClaudeRatelimitWindow{UsedRatio: 0.10, Reset: time.Unix(1782884400, 0)},
		SevenDay: &ClaudeRatelimitWindow{UsedRatio: 0.99, Reset: time.Unix(1783429200, 0)},
	}
	if _, ok := ShouldBlockClaudeRatelimit(st, testBlockThreshold); ok {
		t.Fatal("7d utilization must not trigger a block")
	}
}
