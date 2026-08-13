package helps

import (
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// ClaudeUsageMode identifies how a Claude credential is shared.
type ClaudeUsageMode string

const (
	ClaudeUsageModeShared    ClaudeUsageMode = "shared"
	ClaudeUsageModeDedicated ClaudeUsageMode = "dedicated"
)

const (
	ClaudeRatelimitBlockReasonSevenDayExhausted = "seven_day_exhausted"
	ClaudeRatelimitBlockReasonFiveHourThreshold = "five_hour_dynamic"
)

// ClaudeRatelimitPolicy is the resolved, validated policy used for one request.
type ClaudeRatelimitPolicy struct {
	Mode     ClaudeUsageMode
	Location *time.Location
	Shared   config.ClaudeSharedRatelimitPolicy
}

// ClaudeRatelimitDecision captures the deterministic result of applying the policy to
// one parsed Claude unified rate-limit state. It is used by both the selector block
// path and the WeCom alert path so they cannot drift.
type ClaudeRatelimitDecision struct {
	Mode              ClaudeUsageMode
	IsNight           bool
	SevenDayPresent   bool
	SevenDayUsed      float64
	FiveHourThreshold float64
	AlertThreshold    float64
	Block             bool
	BlockUntil        time.Time
	BlockReason       string
	NaturalFull       bool
	NaturalFullWindow string
}

// ResolveClaudeRatelimitPolicy builds a policy from config and the current auth.
// Unset auth mode falls back to the configured default, then to shared.
func ResolveClaudeRatelimitPolicy(cfg *config.Config, auth *cliproxyauth.Auth) ClaudeRatelimitPolicy {
	policy := ClaudeRatelimitPolicy{
		Mode:     ClaudeUsageModeShared,
		Location: mustLoadClaudeLocation("Asia/Shanghai"),
		Shared:   defaultClaudeSharedRatelimitPolicy(),
	}

	if cfg != nil {
		if mode := normalizeClaudeUsageMode(cfg.ClaudeRatelimitAlert.DefaultUsageMode); mode != "" {
			policy.Mode = mode
		}
		policy.Shared = normalizeClaudeSharedRatelimitPolicy(cfg.ClaudeRatelimitAlert.Shared)
		if loc := loadClaudeLocation(cfg.ClaudeRatelimitAlert.Timezone); loc != nil {
			policy.Location = loc
		}
	}

	if mode := normalizeClaudeUsageMode(auth.ClaudeUsageMode()); mode != "" {
		policy.Mode = mode
	}

	return policy
}

// EvaluateClaudeRatelimitPolicy applies the resolved policy to the parsed state at now.
func EvaluateClaudeRatelimitPolicy(state ClaudeRatelimitState, policy ClaudeRatelimitPolicy, now time.Time) ClaudeRatelimitDecision {
	decision := ClaudeRatelimitDecision{
		Mode:              policy.Mode,
		FiveHourThreshold: 1.0,
		AlertThreshold:    1.0,
	}
	natural := claudeNaturalFullWindow(state)
	decision.NaturalFull = natural != ""
	decision.NaturalFullWindow = natural

	if policy.Mode == ClaudeUsageModeDedicated {
		return decision
	}

	if w := state.SevenDay; w != nil && !w.Reset.IsZero() &&
		(w.Status == "rejected" || w.UsedRatio >= policy.Shared.SevenDayHardCap) {
		decision.Block = true
		decision.BlockUntil = w.Reset
		decision.BlockReason = ClaudeRatelimitBlockReasonSevenDayExhausted
	}

	decision.SevenDayPresent = state.SevenDay != nil
	if state.SevenDay != nil {
		decision.SevenDayUsed = state.SevenDay.UsedRatio
	}

	decision.IsNight = state.SevenDay != nil && claudeIsNight(now, policy.Location, policy.Shared)
	base := policy.Shared.DayBlockThreshold
	if decision.IsNight {
		base = policy.Shared.NightBlockThreshold
	}
	guard := claudeSevenDayGuard(decision.SevenDayUsed, policy.Shared)
	threshold := clampClaudeRatio(base * guard)
	if threshold < policy.Shared.MinBlockThreshold {
		threshold = clampClaudeRatio(policy.Shared.MinBlockThreshold)
	}
	decision.FiveHourThreshold = threshold
	decision.AlertThreshold = clampClaudeRatio(threshold - policy.Shared.AlertMargin)

	if w := state.FiveHour; w != nil && !w.Reset.IsZero() && w.UsedRatio >= threshold {
		if !decision.Block {
			decision.Block = true
			decision.BlockUntil = w.Reset
			decision.BlockReason = ClaudeRatelimitBlockReasonFiveHourThreshold
		} else if w.Reset.After(decision.BlockUntil) {
			decision.BlockUntil = w.Reset
		}
	}

	return decision
}

// ShouldBlockClaudeRatelimit is a small compatibility wrapper around
// EvaluateClaudeRatelimitPolicy for callers that only need the block decision.
func ShouldBlockClaudeRatelimit(state ClaudeRatelimitState, policy ClaudeRatelimitPolicy, now time.Time) (resetAt time.Time, ok bool) {
	decision := EvaluateClaudeRatelimitPolicy(state, policy, now)
	return decision.BlockUntil, decision.Block
}

func normalizeClaudeUsageMode(raw string) ClaudeUsageMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "shared":
		return ClaudeUsageModeShared
	case "dedicated", "exclusive":
		return ClaudeUsageModeDedicated
	default:
		return ""
	}
}

func defaultClaudeSharedRatelimitPolicy() config.ClaudeSharedRatelimitPolicy {
	return config.ClaudeSharedRatelimitPolicy{
		DayBlockThreshold:   0.80,
		NightBlockThreshold: 0.98,
		SevenDaySoftStart:   0.70,
		SevenDayHardCap:     0.98,
		MinBlockThreshold:   0.03,
		AlertMargin:         0.05,
		NightStart:          "22:00",
		NightEnd:            "08:00",
	}
}

func normalizeClaudeSharedRatelimitPolicy(in config.ClaudeSharedRatelimitPolicy) config.ClaudeSharedRatelimitPolicy {
	defaults := defaultClaudeSharedRatelimitPolicy()
	if in.DayBlockThreshold <= 0 {
		in.DayBlockThreshold = defaults.DayBlockThreshold
	}
	if in.NightBlockThreshold <= 0 {
		in.NightBlockThreshold = defaults.NightBlockThreshold
	}
	if in.SevenDaySoftStart <= 0 {
		in.SevenDaySoftStart = defaults.SevenDaySoftStart
	}
	if in.SevenDayHardCap <= 0 {
		in.SevenDayHardCap = defaults.SevenDayHardCap
	}
	if in.MinBlockThreshold <= 0 {
		in.MinBlockThreshold = defaults.MinBlockThreshold
	}
	if in.AlertMargin <= 0 {
		in.AlertMargin = defaults.AlertMargin
	}
	if strings.TrimSpace(in.NightStart) == "" {
		in.NightStart = defaults.NightStart
	}
	if strings.TrimSpace(in.NightEnd) == "" {
		in.NightEnd = defaults.NightEnd
	}
	return in
}

func claudeSevenDayGuard(used float64, shared config.ClaudeSharedRatelimitPolicy) float64 {
	hardCap := shared.SevenDayHardCap
	softStart := shared.SevenDaySoftStart
	if hardCap <= softStart {
		if used < hardCap {
			return 1
		}
		return 0
	}
	guard := (hardCap - used) / (hardCap - softStart)
	if guard < 0 {
		return 0
	}
	if guard > 1 {
		return 1
	}
	return guard
}

func claudeIsNight(now time.Time, loc *time.Location, shared config.ClaudeSharedRatelimitPolicy) bool {
	start := parseClaudeHHMM(shared.NightStart)
	end := parseClaudeHHMM(shared.NightEnd)
	if start < 0 || end < 0 || start == end {
		return false
	}
	if loc == nil {
		loc = time.Local
	}
	t := now.In(loc)
	cur := t.Hour()*60 + t.Minute()
	if start < end {
		return cur >= start && cur < end
	}
	return cur >= start || cur < end
}

func parseClaudeHHMM(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return -1
	}
	parts := strings.Split(raw, ":")
	if len(parts) != 2 {
		return -1
	}
	hour, errHour := strconv.Atoi(strings.TrimSpace(parts[0]))
	minute, errMinute := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errHour != nil || errMinute != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return -1
	}
	return hour*60 + minute
}

func loadClaudeLocation(name string) *time.Location {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	loc, err := time.LoadLocation(strings.TrimSpace(name))
	if err != nil {
		return nil
	}
	return loc
}

func mustLoadClaudeLocation(name string) *time.Location {
	if loc := loadClaudeLocation(name); loc != nil {
		return loc
	}
	return time.Local
}

func clampClaudeRatio(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

func claudeNaturalFullWindow(state ClaudeRatelimitState) string {
	fiveFull := false
	sevenFull := false
	if w := state.FiveHour; w != nil && (w.Status == "rejected" || w.UsedRatio >= 1.0) {
		fiveFull = true
	}
	if w := state.SevenDay; w != nil && (w.Status == "rejected" || w.UsedRatio >= 1.0) {
		sevenFull = true
	}
	switch {
	case fiveFull && sevenFull:
		return "5h+7d"
	case fiveFull:
		return "5h"
	case sevenFull:
		return "7d"
	default:
		return ""
	}
}
