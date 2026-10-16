package management

import (
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// CompanionAuthMiddleware gates routes on the companion secret under
// cfg.RemoteManagement.CompanionSecret. When the secret is empty the request
// passes through — the first-run install needs the operator to reach the page
// and set a lock from inside it. When set, the middleware accepts either
// `Authorization: Bearer <secret>` or `X-Companion-Key: <secret>` and 401s on
// mismatch with a `WWW-Authenticate: Basic` header so browsers can offer a
// native credential dialog.
func (h *Handler) CompanionAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		provided := extractCompanionKey(c)
		allowed, statusCode, errMsg := h.AuthenticateCompanionKey(provided)
		if !allowed {
			c.Header("WWW-Authenticate", `Basic realm="Companion panel"`)
			c.AbortWithStatusJSON(statusCode, gin.H{"error": errMsg})
			return
		}
		c.Next()
	}
}

// extractCompanionKey pulls the companion secret from the request. The
// companion key is set by an operator in their browser and held in
// localStorage, so we accept three header shapes:
//
//  1. Authorization: Bearer <secret>        — the page JS sends this from localStorage
//  2. X-Companion-Key: <secret>            — same idea, different header name
//  3. Authorization: Basic <base64(u:p)>    — the browser's native auth dialog sends
//     this when the page is opened directly and a lock is in place; the user
//     types "any username" / "the companion key" and the password portion is
//     what we compare. Failing to support Basic here turns a forgotten key
//     into "the page just won't load" with no recovery UI.
//
// For Basic, we split on the LAST ':' to honor passwords that contain colons;
// the username portion is ignored by design — there is no per-user identity
// at the companion secret layer.
func extractCompanionKey(c *gin.Context) string {
	if ah := c.GetHeader("Authorization"); ah != "" {
		parts := strings.SplitN(ah, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
			return strings.TrimSpace(parts[1])
		}
		if len(parts) == 2 && strings.EqualFold(parts[0], "basic") {
			if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1])); err == nil {
				creds := string(raw)
				// Per RFC 7617: split on the FIRST colon. The user-id cannot
				// contain a colon (so the first colon is the separator), and
				// the password may freely contain colons — everything after
				// the first colon is the password portion. If there is no
				// colon at all, treat the whole decoded value as the secret
				// (the username field was left empty).
				if idx := strings.Index(creds, ":"); idx >= 0 {
					return strings.TrimSpace(creds[idx+1:])
				}
				return strings.TrimSpace(creds)
			}
			return ""
		}
		// Unknown scheme — fall back to the whole value so a custom
		// Authorization header still has a chance to match (and fail
		// closed via bcrypt compare) instead of silently bypassing.
		return strings.TrimSpace(ah)
	}
	if x := c.GetHeader("X-Companion-Key"); x != "" {
		return strings.TrimSpace(x)
	}
	return ""
}

// AuthenticateCompanionKey is the helper that powers CompanionAuthMiddleware
// and is split out so callers outside an HTTP request (none today, future
// ones possible) can reuse the same logic. Mirrors AuthenticateManagementKey
// in shape but does not currently track failed-attempt bans — the companion
// secret lives in a single operator's browser localStorage, so brute-force
// is not the threat model.
func (h *Handler) AuthenticateCompanionKey(provided string) (bool, int, string) {
	if h == nil {
		return false, http.StatusForbidden, "remote management disabled"
	}
	cfg := h.cfg
	stored := ""
	if cfg != nil {
		stored = cfg.RemoteManagement.CompanionSecret
	}
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return true, http.StatusOK, ""
	}
	if provided == "" {
		return false, http.StatusUnauthorized, "missing companion key"
	}
	if matchCompanionSecret(stored, provided) {
		return true, http.StatusOK, ""
	}
	return false, http.StatusUnauthorized, "invalid companion key"
}

// matchCompanionSecret returns true when `provided` equals the stored
// companion secret. The stored value may be a bcrypt hash (loaded from a
// previously-saved config) or plaintext (the very first save, or a value
// the operator has not yet triggered a hash for via LoadConfig). Mirrors how
// the management secret is verified in AuthenticateManagementKey.
func matchCompanionSecret(stored, provided string) bool {
	if isCompanionBcrypt(stored) {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(provided)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(provided)) == 1
}

func isCompanionBcrypt(s string) bool {
	return len(s) > 4 && (s[:4] == "$2a$" || s[:4] == "$2b$" || s[:4] == "$2y$")
}
