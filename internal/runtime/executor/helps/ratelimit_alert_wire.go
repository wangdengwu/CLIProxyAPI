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

	cooldown := parseClaudeRatelimitCooldown(cfg.ClaudeRatelimitAlert.Cooldown)
	level, ok := defaultClaudeRatelimitAlerter.ShouldAlert(auth.ID, state, cfg.ClaudeRatelimitAlert.AlertThreshold, cooldown, time.Now())
	if !ok {
		return false
	}

	account := claudeRatelimitAccountLabel(auth)
	msg := BuildClaudeRatelimitMarkdown(account, model, state)
	reqID := logging.GetRequestID(ctx)
	authID := auth.ID

	// Fire-and-forget: detach from the request context (which is cancelled once the
	// request completes) and never let a failure reach the caller.
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

	return true
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
