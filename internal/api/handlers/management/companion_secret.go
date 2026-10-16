package management

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// companionSecretRequest is the body the companion page sends when the operator
// sets, rotates, or removes the companion lock. current_secret is required
// whenever a secret already exists; it is omitted on the very first save.
type companionSecretRequest struct {
	CurrentSecret string `json:"current_secret"`
	NewSecret     string `json:"new_secret"`
}

// PostCompanionSecret is the management-keyed endpoint that lets the operator
// set, rotate, or clear the companion lock from the page itself. The
// management middleware already enforced the management key on the way in;
// here we only worry about the lifecycle of the value.
//
//	rules:
//	- empty stored + any new_secret  → set (first-run path)
//	- non-empty stored + matching current_secret + non-empty new_secret → overwrite
//	- non-empty stored + matching current_secret + empty new_secret → clear
//	- non-empty stored + missing/wrong current_secret → 400, untouched
//
// The persisted value is always a bcrypt hash (the middleware compares plain
// to whatever the loader left on disk, so the hash is the canonical shape);
// the operator's cleartext never lives on disk longer than one save.
//
// Hot-reload follows the same shape PutConfigYAML uses: write to disk,
// reload from disk, swap the handler's cfg pointer. The very next request
// through CompanionAuthMiddleware sees the new state.
func (h *Handler) PostCompanionSecret(c *gin.Context) {
	var req companionSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	newSecret := strings.TrimSpace(req.NewSecret)
	currentSecret := req.CurrentSecret // keep raw; compared against stored bcrypt

	cfg := h.cfg
	stored := ""
	if cfg != nil {
		stored = strings.TrimSpace(cfg.RemoteManagement.CompanionSecret)
	}

	if stored != "" {
		if currentSecret == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "current_secret required when companion-secret exists"})
			return
		}
		if !matchCompanionSecret(stored, currentSecret) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "current_secret does not match"})
			return
		}
	}

	if err := h.persistCompanionSecret(newSecret); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// persistCompanionSecret writes the new value, hashes it if non-empty,
// updates disk, and reloads the cfg so the live middleware sees the new
// state on the next request. Mirrors PutConfigYAML's hot-reload shape.
func (h *Handler) persistCompanionSecret(newSecret string) error {
	path := strings.TrimSpace(h.configFilePath)
	if path == "" {
		// No on-disk config (in-memory tests, etc.) — skip persistence and
		// just update the in-memory cfg. Mirrors LoadConfigOptional's no-file
		// branch.
		h.mu.Lock()
		if h.cfg != nil {
			hashed, err := hashForStorage(newSecret)
			if err != nil {
				h.mu.Unlock()
				return err
			}
			h.cfg.RemoteManagement.CompanionSecret = hashed
		}
		h.mu.Unlock()
		return nil
	}

	stored, err := hashForStorage(newSecret)
	if err != nil {
		return err
	}

	if err := config.SaveConfigPreserveCommentsUpdateNestedScalar(path, []string{"remote-management", "companion-secret"}, stored); err != nil {
		return err
	}

	// Reload from disk and swap so middleware sees the new state. Matches
	// PutConfigYAML's pattern.
	reloaded, err := config.LoadConfig(path)
	if err != nil {
		return err
	}
	h.mu.Lock()
	h.cfg = reloaded
	h.mu.Unlock()
	return nil
}

// hashForStorage returns the bcrypt form for non-empty secrets and "" for
// empty ones — so a "remove lock" round-trip lands the empty string on
// disk and the next request bypasses the middleware.
func hashForStorage(secret string) (string, error) {
	if secret == "" {
		return "", nil
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hashed), nil
}
