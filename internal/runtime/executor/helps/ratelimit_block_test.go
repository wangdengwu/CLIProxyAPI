package helps

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

func sharedPolicyForMode(mode string, cfg *config.Config) ClaudeRatelimitPolicy {
	auth := &cliproxyauth.Auth{Attributes: map[string]string{"claude_usage_mode": mode}}
	return ResolveClaudeRatelimitPolicy(cfg, auth)
}

func sharedNow(t *testing.T, hour int) time.Time {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load Asia/Shanghai: %v", err)
	}
	return time.Date(2026, 8, 13, hour, 30, 0, 0, loc)
}

// Shared, abundant 7d, daytime: 5h threshold is exactly 0.80.
func TestEvaluateSharedDayAbundant(t *testing.T) {
	policy := sharedPolicyForMode("shared", nil)
	state := ClaudeRatelimitState{
		FiveHour: win(0.80, "allowed_warning", testResetA),
		SevenDay: win(0.12, "allowed", testResetB),
	}
	decision := EvaluateClaudeRatelimitPolicy(state, policy, sharedNow(t, 12))
	if decision.IsNight {
		t.Fatal("expected daytime for a 12:30 Asia/Shanghai timestamp, got night")
	}
	if decision.FiveHourThreshold != 0.80 {
		t.Fatalf("FiveHourThreshold = %v, want 0.80", decision.FiveHourThreshold)
	}
	if !decision.Block || decision.BlockReason != ClaudeRatelimitBlockReasonFiveHourThreshold {
		t.Fatalf("expected 5h dynamic block, got block=%v reason=%s", decision.Block, decision.BlockReason)
	}
}

// Shared, abundant 7d, daytime: below 0.80 must not block.
func TestEvaluateSharedDayBelowThreshold(t *testing.T) {
	policy := sharedPolicyForMode("shared", nil)
	state := ClaudeRatelimitState{
		FiveHour: win(0.79, "allowed_warning", testResetA),
		SevenDay: win(0.12, "allowed", testResetB),
	}
	decision := EvaluateClaudeRatelimitPolicy(state, policy, sharedNow(t, 12))
	if decision.Block {
		t.Fatalf("must not block below 0.80, decision=%+v", decision)
	}
}

// Shared, abundant 7d, night: 5h threshold is 0.98.
func TestEvaluateSharedNightAbundant(t *testing.T) {
	policy := sharedPolicyForMode("shared", nil)
	state := ClaudeRatelimitState{
		FiveHour: win(0.98, "allowed_warning", testResetA),
		SevenDay: win(0.12, "allowed", testResetB),
	}
	decision := EvaluateClaudeRatelimitPolicy(state, policy, sharedNow(t, 23))
	if !decision.IsNight {
		t.Fatal("expected IsNight = true at 23:30")
	}
	if decision.FiveHourThreshold != 0.98 {
		t.Fatalf("FiveHourThreshold = %v, want 0.98", decision.FiveHourThreshold)
	}
	if !decision.Block {
		t.Fatal("expected block at 0.98 during the night window")
	}
}

// Shared with 7d partially consumed: the dynamic 5h threshold shrinks below 0.80.
func TestEvaluateSharedSevenDayGuard(t *testing.T) {
	policy := sharedPolicyForMode("shared", nil)
	state := ClaudeRatelimitState{
		FiveHour: win(0.52, "allowed_warning", testResetA),
		SevenDay: win(0.80, "allowed", testResetB),
	}
	decision := EvaluateClaudeRatelimitPolicy(state, policy, sharedNow(t, 12))
	want := 0.80 * ((0.98 - 0.80) / (0.98 - 0.70)) // 0.514285...
	if diff := decision.FiveHourThreshold - want; diff < -0.001 || diff > 0.001 {
		t.Fatalf("FiveHourThreshold = %v, want ~%v", decision.FiveHourThreshold, want)
	}
	if !decision.Block {
		t.Fatal("expected block because 5h usage is above the reduced threshold")
	}
}

// Shared 7d hard cap: block until 7d reset even when 5h is low.
func TestEvaluateSharedSevenDayHardBlock(t *testing.T) {
	policy := sharedPolicyForMode("shared", nil)
	sevenReset := time.Unix(1783429200, 0)
	state := ClaudeRatelimitState{
		FiveHour: win(0.01, "allowed", testResetA),
		SevenDay: win(0.98, "allowed_warning", sevenReset.Unix()),
	}
	decision := EvaluateClaudeRatelimitPolicy(state, policy, sharedNow(t, 12))
	if !decision.Block || decision.BlockReason != ClaudeRatelimitBlockReasonSevenDayExhausted {
		t.Fatalf("expected 7d hard block, got block=%v reason=%s", decision.Block, decision.BlockReason)
	}
	if !decision.BlockUntil.Equal(sevenReset) {
		t.Fatalf("BlockUntil = %v, want %v", decision.BlockUntil, sevenReset)
	}
}

// Missing 7d data disables night boosting for safety.
func TestEvaluateSharedMissingSevenDayNoNightBoost(t *testing.T) {
	policy := sharedPolicyForMode("shared", nil)
	state := ClaudeRatelimitState{FiveHour: win(0.81, "allowed_warning", testResetA)}
	decision := EvaluateClaudeRatelimitPolicy(state, policy, sharedNow(t, 23))
	if decision.IsNight {
		t.Fatal("missing 7d data must disable the night boost")
	}
	if decision.FiveHourThreshold != 0.80 {
		t.Fatalf("FiveHourThreshold = %v, want 0.80", decision.FiveHourThreshold)
	}
	if !decision.Block {
		t.Fatal("expected block at 0.81 during the conservative daytime policy")
	}
}

// Dedicated and exclusive accounts never proactively block, even when naturally full.
func TestEvaluateDedicatedNeverBlocks(t *testing.T) {
	for _, mode := range []string{"dedicated", "exclusive"} {
		policy := sharedPolicyForMode(mode, nil)
		state := ClaudeRatelimitState{
			FiveHour: win(1.05, "rejected", testResetA),
			SevenDay: win(1.0, "rejected", testResetB),
		}
		decision := EvaluateClaudeRatelimitPolicy(state, policy, sharedNow(t, 12))
		if decision.Mode != ClaudeUsageModeDedicated {
			t.Fatalf("Mode = %s, want dedicated", decision.Mode)
		}
		if decision.Block {
			t.Fatalf("dedicated account must not proactively block, decision=%+v", decision)
		}
		if !decision.NaturalFull || decision.NaturalFullWindow == "" {
			t.Fatalf("dedicated account must report natural full, decision=%+v", decision)
		}
	}
}

// When both 5h and 7d protections trigger, block until the later reset.
func TestEvaluateSharedBothWindowsUsesLaterReset(t *testing.T) {
	policy := sharedPolicyForMode("shared", nil)
	fiveReset := time.Unix(1782884400, 0)
	sevenReset := time.Unix(1782902400, 0)
	state := ClaudeRatelimitState{
		FiveHour: win(0.99, "allowed_warning", fiveReset.Unix()),
		SevenDay: win(0.99, "rejected", sevenReset.Unix()),
	}
	decision := EvaluateClaudeRatelimitPolicy(state, policy, sharedNow(t, 12))
	if !decision.Block {
		t.Fatal("expected block")
	}
	if !decision.BlockUntil.Equal(sevenReset) {
		t.Fatalf("BlockUntil = %v, want later reset %v", decision.BlockUntil, sevenReset)
	}
}

// Auth mode overrides config default; invalid auth mode falls back to config default.
func TestResolveClaudeRatelimitPolicyModeFallback(t *testing.T) {
	cfg := &config.Config{ClaudeRatelimitAlert: config.ClaudeRatelimitAlert{DefaultUsageMode: "dedicated"}}
	if p := sharedPolicyForMode("shared", cfg); p.Mode != ClaudeUsageModeShared {
		t.Fatalf("auth shared should override config default, got %s", p.Mode)
	}
	if p := sharedPolicyForMode("unknown", cfg); p.Mode != ClaudeUsageModeDedicated {
		t.Fatalf("invalid auth mode should fall back to config default, got %s", p.Mode)
	}
}
