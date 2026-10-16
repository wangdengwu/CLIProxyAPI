package auth

import (
	"math"
	"testing"
)

// authPriority has to balance two signals: the long-standing `priority` integer
// that callers have been setting by hand, and the new `claude_pin_to_lowest`
// flag that the companion page toggles. The flag must always win for Claude
// accounts (it's an absolute floor) and must be ignored entirely on other
// providers — pinning Gemini or Codex would silently change the order of
// things those providers do not have a concept for.
func TestAuthPriority_PinToLowest_AlwaysBeatsNumericForClaude(t *testing.T) {
	cases := []struct {
		name string
		a    *Auth
		want int
	}{
		{
			name: "claude + pin + no priority",
			a:    claudeAuth("", map[string]string{"claude_pin_to_lowest": "true"}),
			want: math.MinInt,
		},
		{
			name: "claude + pin + priority=5",
			a:    claudeAuth("5", map[string]string{"claude_pin_to_lowest": "true"}),
			want: math.MinInt,
		},
		{
			name: "claude + pin + priority=999",
			a:    claudeAuth("999", map[string]string{"claude_pin_to_lowest": "true"}),
			want: math.MinInt,
		},
		{
			name: "claude + no pin + priority=5",
			a:    claudeAuth("5", nil),
			want: 5,
		},
		{
			name: "claude + no pin + no priority",
			a:    claudeAuth("", nil),
			want: 0,
		},
		{
			name: "claude + pin=false + priority=3",
			a:    claudeAuth("3", map[string]string{"claude_pin_to_lowest": "false"}),
			want: 3,
		},
		{
			name: "gemini + pin=true (other providers ignore the flag)",
			a:    otherProviderAuth("gemini", "", map[string]string{"claude_pin_to_lowest": "true"}),
			want: 0,
		},
		{
			name: "gemini + pin=true + priority=10",
			a:    otherProviderAuth("gemini", "10", map[string]string{"claude_pin_to_lowest": "true"}),
			want: 10,
		},
		{
			name: "claude + empty-string pin + priority=5 (empty != true)",
			a:    claudeAuth("5", map[string]string{"claude_pin_to_lowest": ""}),
			want: 5,
		},
		{
			name: "claude + invalid pin string + priority=5",
			a:    claudeAuth("5", map[string]string{"claude_pin_to_lowest": "yes"}),
			want: 5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := authPriority(tc.a); got != tc.want {
				t.Fatalf("authPriority = %d, want %d", got, tc.want)
			}
		})
	}
}

// Metadata-only fallback: some auths (UploadAuthFile path) never go through
// the synthesizer, so Attributes is nil. The flag must be readable from
// Metadata so the selector still gets the pin intent.
func TestAuthPriority_PinFromMetadata_ForNonSynthesizedAuth(t *testing.T) {
	a := &Auth{
		Provider: "claude",
		Metadata: map[string]any{"claude_pin_to_lowest": true},
	}
	if got := authPriority(a); got != math.MinInt {
		t.Fatalf("authPriority = %d, want MinInt", got)
	}
}

// Multiple pinned Claude accounts share the lowest bucket — same effective
// priority — so the scheduler's existing round-robin within a bucket picks
// the right one next. Defends against the selector silently de-pinning one
// of them.
func TestAuthPriority_MultiplePinnedAllReturnMinInt(t *testing.T) {
	a := claudeAuth("7", map[string]string{"claude_pin_to_lowest": "true"})
	b := claudeAuth("", map[string]string{"claude_pin_to_lowest": "true"})
	c := claudeAuth("2", map[string]string{"claude_pin_to_lowest": "1"})

	if got := authPriority(a); got != math.MinInt {
		t.Fatalf("a = %d, want MinInt", got)
	}
	if got := authPriority(b); got != math.MinInt {
		t.Fatalf("b = %d, want MinInt", got)
	}
	if got := authPriority(c); got != math.MinInt {
		t.Fatalf("c = %d, want MinInt", got)
	}
}

// isPinnedLowest is the gate the selector uses; pinning a non-Claude account
// must return false so the new mechanism cannot leak into provider-agnostic
// priority paths.
func TestIsPinnedLowest_NonClaudeAlwaysFalse(t *testing.T) {
	others := []string{"gemini", "codex", "kimi", "antigravity", "openai"}
	for _, prov := range others {
		t.Run(prov, func(t *testing.T) {
			a := otherProviderAuth(prov, "", map[string]string{"claude_pin_to_lowest": "true"})
			if isPinnedLowest(a) {
				t.Fatalf("%s account should not be pinned by claude_pin_to_lowest", prov)
			}
		})
	}
}

// helpers

func claudeAuth(priority string, attrs map[string]string) *Auth {
	if attrs == nil {
		attrs = map[string]string{}
	}
	attrs["type"] = "claude"
	if priority != "" {
		attrs["priority"] = priority
	}
	return &Auth{
		Provider:   "claude",
		Attributes: attrs,
		Metadata:   map[string]any{"type": "claude"},
	}
}

func otherProviderAuth(provider, priority string, attrs map[string]string) *Auth {
	if attrs == nil {
		attrs = map[string]string{}
	}
	attrs["type"] = provider
	if priority != "" {
		attrs["priority"] = priority
	}
	return &Auth{
		Provider:   provider,
		Attributes: attrs,
		Metadata:   map[string]any{"type": provider},
	}
}
