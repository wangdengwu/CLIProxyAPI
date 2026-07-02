package helps

import (
	"context"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// defaultClaudeRatelimitAlerter holds the process-wide debounce state used by the
// executor integration. State is in-memory only and not persisted.
var defaultClaudeRatelimitAlerter = NewClaudeRatelimitAlerter()

const defaultClaudeRatelimitCooldown = 5 * time.Minute

// MaybeAlertClaudeRatelimit evaluates the parsed rate-limit state for the given
// credential and, when the debounce alerter decides to fire, asynchronously pushes a
// WeCom markdown alert. It self-gates on config (feature disabled or empty webhook →
// no-op) and on the debounce logic, so callers may invoke it unconditionally after a
// successful parse. It never blocks the caller and never returns/propagates a send
// error — failures are logged only, so the request-forwarding path is unaffected.
//
// Returns true iff an alert was dispatched (an async send was launched). The return
// value exists for testing the gating decision deterministically.
func MaybeAlertClaudeRatelimit(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, model string, state ClaudeRatelimitState) bool {
	if cfg == nil || !cfg.ClaudeRatelimitAlert.Enabled || auth == nil {
		return false
	}
	webhook := strings.TrimSpace(cfg.ClaudeRatelimitAlert.WebhookURL)
	if webhook == "" {
		return false
	}

	account := claudeRatelimitAccountLabel(auth)
	reqID := logging.GetRequestID(ctx)
	authID := auth.ID

	// Block notice is evaluated INDEPENDENTLY of the alert decision below: it fires under
	// the exact selector-block condition, deduped once per window, and bypasses the alert
	// cooldown. This is deliberate — an alert sent moments earlier (same window, within
	// cooldown) must not swallow the "account blocked" notice.
	if blockUntil, ok := defaultClaudeRatelimitAlerter.ShouldNotifyBlock(authID, state, cfg.ClaudeRatelimitAlert.BlockThreshold); ok {
		sendClaudeRatelimitWeComAsync(webhook, BuildClaudeRatelimitBlockMarkdown(account, model, state, blockUntil), reqID, authID, ClaudeRatelimitLevelBlocked)
	}

	cooldown := parseClaudeRatelimitCooldown(cfg.ClaudeRatelimitAlert.Cooldown)
	level, ok := defaultClaudeRatelimitAlerter.ShouldAlert(authID, state, cfg.ClaudeRatelimitAlert.AlertThreshold, cooldown, time.Now())
	if !ok {
		return false
	}
	sendClaudeRatelimitWeComAsync(webhook, BuildClaudeRatelimitMarkdown(account, model, state), reqID, authID, level)
	return true
}

// sendClaudeRatelimitWeComAsync fires a WeCom notification off the request path:
// fire-and-forget, detached from the (soon-cancelled) request context, and never
// propagating a failure to the caller — failures are logged only.
func sendClaudeRatelimitWeComAsync(webhook string, msg WeComMessage, reqID, authID, level string) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Errorf("claude ratelimit alert: panic while sending webhook: %v", r)
			}
		}()
		sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		entry := log.WithField("request_id", reqID).WithField("auth_id", authID)
		if err := SendWeCom(sendCtx, webhook, msg); err != nil {
			entry.Warnf("claude ratelimit alert (%s) webhook send failed: %v", level, err)
			return
		}
		entry.Infof("claude ratelimit alert (%s) sent", level)
	}()
}

// parseClaudeRatelimitCooldown parses a duration string (e.g. "5m"), falling back to
// the 5m default on empty/invalid input or a non-positive value.
func parseClaudeRatelimitCooldown(s string) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(s)); err == nil && d > 0 {
		return d
	}
	return defaultClaudeRatelimitCooldown
}

// claudeRatelimitAccountLabel derives a human-readable credential identifier for the
// alert body, mirroring the logging helper (email preferred, API keys hidden).
func claudeRatelimitAccountLabel(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if acctType, acctVal := auth.AccountInfo(); acctVal != "" {
		if acctType == "api_key" {
			acctVal = util.HideAPIKey(acctVal)
		}
		return acctVal
	}
	if auth.Label != "" {
		return auth.Label
	}
	return auth.ID
}
