package helps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Alert tier levels returned by ShouldAlert / ShouldNotifyBlock.
const (
	ClaudeRatelimitLevelAlert    = "alert"    // crossed the alert water line
	ClaudeRatelimitLevelRejected = "rejected" // rejected / window full (used >= 1.0 or status "rejected")
	ClaudeRatelimitLevelBlocked  = "blocked"  // crossed the block water line -> credential held unavailable
)

// authAlertState tracks debounce state for a single authID.
type authAlertState struct {
	windowKey    int64           // FiveHour.Reset.Unix() for the current window
	alertedTiers map[string]bool // tiers already alerted in the current window
	lastSent     time.Time       // zero if never sent
}

// ClaudeRatelimitAlerter holds in-memory, per-auth debounce state. Safe for
// concurrent use.
type ClaudeRatelimitAlerter struct {
	mu    sync.Mutex
	auths map[string]*authAlertState
}

// NewClaudeRatelimitAlerter creates a new ClaudeRatelimitAlerter.
func NewClaudeRatelimitAlerter() *ClaudeRatelimitAlerter {
	return &ClaudeRatelimitAlerter{
		auths: make(map[string]*authAlertState),
	}
}

// ShouldAlert decides whether an alert should be sent for authID given the latest
// state. Pure decision over in-memory state (NO IO, NO time.Now() inside — use the
// passed `now`). Returns the tier and true iff a notification should fire now.
func (a *ClaudeRatelimitAlerter) ShouldAlert(authID string, state ClaudeRatelimitState, alertThreshold float64, cooldown time.Duration, now time.Time) (level string, ok bool) {
	if state.FiveHour == nil {
		return "", false
	}

	w := state.FiveHour

	// Determine tier.
	var tier string
	if w.Status == "rejected" || w.UsedRatio >= 1.0 {
		tier = ClaudeRatelimitLevelRejected
	} else if w.UsedRatio >= alertThreshold {
		tier = ClaudeRatelimitLevelAlert
	} else {
		return "", false
	}

	windowKey := w.Reset.Unix()

	a.mu.Lock()
	defer a.mu.Unlock()

	st, exists := a.auths[authID]
	if !exists {
		st = &authAlertState{
			windowKey:    windowKey,
			alertedTiers: make(map[string]bool),
		}
		a.auths[authID] = st
	}

	// Rule 1: New window re-arm.
	if windowKey != st.windowKey {
		st.windowKey = windowKey
		st.alertedTiers = make(map[string]bool)
	}

	// Rule 2: Per-window, per-tier dedup.
	if st.alertedTiers[tier] {
		return "", false
	}

	// Rule 3: Hard cooldown backstop (skip if never sent).
	if !st.lastSent.IsZero() && now.Sub(st.lastSent) < cooldown {
		return "", false
	}

	// All gates passed — fire.
	st.alertedTiers[tier] = true
	st.lastSent = now
	return tier, true
}

// ShouldNotifyBlock reports whether a distinct "account blocked" notification should
// fire for authID this window, returning the block-until time (the 5h window reset).
//
// It fires under exactly the condition the selector applies a block
// (ShouldBlockClaudeRatelimit: 5h present, reset known, used ratio >= blockThreshold),
// deduped to once per 5h window via the shared per-tier map. Unlike ShouldAlert it
// intentionally does NOT consult or update the hard-cooldown timestamp, so a block notice
// is never swallowed by a preceding alert within the cooldown interval (nor does it
// suppress a later alert). A non-positive blockThreshold disables block notices.
func (a *ClaudeRatelimitAlerter) ShouldNotifyBlock(authID string, state ClaudeRatelimitState, blockThreshold float64) (resetAt time.Time, ok bool) {
	if blockThreshold <= 0 {
		return time.Time{}, false
	}
	resetAt, blocked := ShouldBlockClaudeRatelimit(state, blockThreshold)
	if !blocked {
		return time.Time{}, false
	}

	// ShouldBlockClaudeRatelimit guarantees FiveHour is non-nil with a known reset.
	windowKey := state.FiveHour.Reset.Unix()

	a.mu.Lock()
	defer a.mu.Unlock()

	st, exists := a.auths[authID]
	if !exists {
		st = &authAlertState{
			windowKey:    windowKey,
			alertedTiers: make(map[string]bool),
		}
		a.auths[authID] = st
	}

	// New window re-arm (shared with ShouldAlert; only clears on an actual window change).
	if windowKey != st.windowKey {
		st.windowKey = windowKey
		st.alertedTiers = make(map[string]bool)
	}

	// Once-per-window dedup on the blocked tier. Deliberately no cooldown gate and no
	// lastSent update, so alerts and block notices don't debounce each other.
	if st.alertedTiers[ClaudeRatelimitLevelBlocked] {
		return time.Time{}, false
	}
	st.alertedTiers[ClaudeRatelimitLevelBlocked] = true
	return resetAt, true
}

// WeComMessage is the WeCom (企业微信) group-bot markdown message envelope.
type WeComMessage struct {
	MsgType  string        `json:"msgtype"`
	Markdown WeComMarkdown `json:"markdown"`
}

// WeComMarkdown holds the markdown content for a WeCom message.
type WeComMarkdown struct {
	Content string `json:"content"`
}

const wecomMaxContentBytes = 4096

// BuildClaudeRatelimitMarkdown builds the WeCom markdown payload for a rate-limit
// notification. `account` is a human-readable credential identifier (email/label/id),
// `model` the requested model.
func BuildClaudeRatelimitMarkdown(account, model string, state ClaudeRatelimitState) WeComMessage {
	var sb strings.Builder

	sb.WriteString("## Claude 速率限制告警\n\n")
	sb.WriteString(fmt.Sprintf("**账号 (Account):** %s\n\n", account))
	sb.WriteString(fmt.Sprintf("**模型 (Model):** %s\n\n", model))

	// 5h section — always present.
	if state.FiveHour != nil {
		w := state.FiveHour
		resetStr := "未知"
		if !w.Reset.IsZero() {
			resetStr = w.Reset.Format("2006-01-02 15:04:05 MST")
		}
		sb.WriteString(fmt.Sprintf("**5h 窗口使用率:** %.1f%%\n\n", w.UsedRatio*100))
		if w.Status != "" {
			sb.WriteString(fmt.Sprintf("**5h 窗口状态:** %s\n\n", w.Status))
		}
		sb.WriteString(fmt.Sprintf("**5h 窗口重置时间:** %s\n\n", resetStr))
	} else {
		sb.WriteString("**5h 窗口:** 无数据\n\n")
	}

	// 7d section — only if present.
	if state.SevenDay != nil {
		w := state.SevenDay
		resetStr := "未知"
		if !w.Reset.IsZero() {
			resetStr = w.Reset.Format("2006-01-02 15:04:05 MST")
		}
		sb.WriteString(fmt.Sprintf("**7d 窗口使用率:** %.1f%%\n\n", w.UsedRatio*100))
		if w.Status != "" {
			sb.WriteString(fmt.Sprintf("**7d 窗口状态:** %s\n\n", w.Status))
		}
		sb.WriteString(fmt.Sprintf("**7d 窗口重置时间:** %s\n\n", resetStr))
	}

	content := sb.String()
	content = clampUTF8(content, wecomMaxContentBytes)

	return WeComMessage{
		MsgType:  "markdown",
		Markdown: WeComMarkdown{Content: content},
	}
}

// BuildClaudeRatelimitBlockMarkdown builds the WeCom payload for an account-block notice:
// the credential crossed the block water line and is now held unavailable (no longer
// consumed via the proxy) until blockUntil — the 5h window reset.
func BuildClaudeRatelimitBlockMarkdown(account, model string, state ClaudeRatelimitState, blockUntil time.Time) WeComMessage {
	var sb strings.Builder

	sb.WriteString("## Claude 账号已阻断\n\n")
	sb.WriteString(fmt.Sprintf("**账号 (Account):** %s\n\n", account))
	sb.WriteString(fmt.Sprintf("**模型 (Model):** %s\n\n", model))

	if state.FiveHour != nil {
		sb.WriteString(fmt.Sprintf("**5h 窗口使用率:** %.1f%%\n\n", state.FiveHour.UsedRatio*100))
		if state.FiveHour.Status != "" {
			sb.WriteString(fmt.Sprintf("**5h 窗口状态:** %s\n\n", state.FiveHour.Status))
		}
	}

	blockStr := "未知"
	if !blockUntil.IsZero() {
		blockStr = blockUntil.Format("2006-01-02 15:04:05 MST")
	}
	sb.WriteString(fmt.Sprintf("**已阻断至 (窗口重置前不再经代理消耗):** %s\n\n", blockStr))

	content := clampUTF8(sb.String(), wecomMaxContentBytes)
	return WeComMessage{
		MsgType:  "markdown",
		Markdown: WeComMarkdown{Content: content},
	}
}

// clampUTF8 truncates s to at most maxBytes bytes on a UTF-8 rune boundary.
func clampUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	b := []byte(s[:maxBytes])
	// Walk back to the last valid UTF-8 rune boundary.
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}

// SendWeCom POSTs the message as JSON to webhookURL. Returns an error on transport
// failure or non-2xx status. If ctx is nil, use context.Background().
func SendWeCom(ctx context.Context, webhookURL string, msg WeComMessage) error {
	if ctx == nil {
		ctx = context.Background()
	}

	body, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("wecom: marshal message: %w", err)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("wecom: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("wecom: send request: %w", err)
	}
	defer resp.Body.Close()
	// Drain body to allow connection reuse.
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("wecom: unexpected status %d", resp.StatusCode)
	}
	return nil
}
