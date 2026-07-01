package helps

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// ClaudeRatelimitWindow holds one rolling-window's unified rate-limit state.
type ClaudeRatelimitWindow struct {
	UsedRatio float64   // fraction of the window used; MAY exceed 1.0
	Status    string    // "allowed" / "allowed_warning" / "rejected" (empty if absent)
	Reset     time.Time // window reset time; zero value if absent or unparseable
}

// ClaudeRatelimitState holds the parsed unified rate-limit windows.
// A nil window means Anthropic returned no usable data for that window.
type ClaudeRatelimitState struct {
	FiveHour *ClaudeRatelimitWindow
	SevenDay *ClaudeRatelimitWindow
}

// ParseClaudeRatelimit parses Anthropic's unified rate-limit headers from an upstream response.
func ParseClaudeRatelimit(header http.Header) ClaudeRatelimitState {
	if header == nil {
		return ClaudeRatelimitState{}
	}
	return ClaudeRatelimitState{
		FiveHour: parseClaudeRatelimitWindow(header, "5h"),
		SevenDay: parseClaudeRatelimitWindow(header, "7d"),
	}
}

// parseClaudeRatelimitWindow parses a single rate-limit window identified by windowPrefix ("5h" or "7d").
// Returns nil if the utilization header is absent or unparseable.
func parseClaudeRatelimitWindow(header http.Header, windowPrefix string) *ClaudeRatelimitWindow {
	base := "Anthropic-Ratelimit-Unified-" + windowPrefix

	utilizationRaw := strings.TrimSpace(header.Get(base + "-Utilization"))
	if utilizationRaw == "" {
		return nil
	}
	usedRatio, err := strconv.ParseFloat(utilizationRaw, 64)
	if err != nil {
		return nil
	}

	w := &ClaudeRatelimitWindow{
		UsedRatio: usedRatio,
		Status:    strings.TrimSpace(header.Get(base + "-Status")),
	}

	resetRaw := strings.TrimSpace(header.Get(base + "-Reset"))
	if resetRaw != "" {
		if sec, err := strconv.ParseInt(resetRaw, 10, 64); err == nil {
			w.Reset = time.Unix(sec, 0)
		} else if secF, err := strconv.ParseFloat(resetRaw, 64); err == nil {
			// Unix seconds occasionally rendered as a float (e.g. "1782884400.0").
			w.Reset = time.Unix(int64(secF), 0)
		} else if t, err := time.Parse(time.RFC3339, resetRaw); err == nil {
			w.Reset = t
		}
		// If all fail, leave Reset as zero time.Time
	}

	return w
}

// LogClaudeRatelimitState emits a structured log line describing the parsed Claude
// unified rate-limit windows for the given credential. It is a no-op when the state
// carries no window data (e.g. API-key requests that lack unified headers), so callers
// can invoke it unconditionally after a successful parse.
func LogClaudeRatelimitState(ctx context.Context, auth *cliproxyauth.Auth, state ClaudeRatelimitState) {
	if state.FiveHour == nil && state.SevenDay == nil {
		return
	}

	fields := log.Fields{"provider": "claude"}
	if auth != nil {
		fields["auth_id"] = auth.ID
		if auth.Label != "" {
			fields["auth_label"] = auth.Label
		}
		if acctType, acctVal := auth.AccountInfo(); acctVal != "" {
			if acctType == "api_key" {
				acctVal = util.HideAPIKey(acctVal)
			}
			fields["account"] = acctVal
		}
	}
	addClaudeRatelimitWindowFields(fields, "5h", state.FiveHour)
	addClaudeRatelimitWindowFields(fields, "7d", state.SevenDay)

	LogWithRequestID(ctx).WithFields(fields).Info("claude unified rate limit state")
}

func addClaudeRatelimitWindowFields(fields log.Fields, window string, w *ClaudeRatelimitWindow) {
	if w == nil {
		return
	}
	fields["ratelimit_"+window+"_utilization"] = w.UsedRatio
	if w.Status != "" {
		fields["ratelimit_"+window+"_status"] = w.Status
	}
	if !w.Reset.IsZero() {
		fields["ratelimit_"+window+"_reset"] = w.Reset.Format(time.RFC3339)
	}
}
