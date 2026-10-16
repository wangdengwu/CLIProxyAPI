package management

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// companionPinState is one row of the GET /v0/management/companion/claude-pin-states
// response — the operator's read view of every Claude account's pin state.
type companionPinState struct {
	AuthID         string `json:"auth_id"`
	PinnedToLowest bool   `json:"pinned_to_lowest"`
	Provider       string `json:"provider"`
}

// GetCompanionClaudePinStates lists every Claude account's pin-to-lowest state.
// Mounted behind CompanionAuthMiddleware (companion key only — NOT management
// key), so the response is reachable from the companion page even when no
// management key is in play. Non-Claude accounts are deliberately absent:
// the field is Claude-specific and we don't want to offer it as a UI
// affordance on providers that ignore it.
func (h *Handler) GetCompanionClaudePinStates(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auth manager unavailable"})
		return
	}
	states := []companionPinState{}
	for _, auth := range h.authManager.List() {
		if auth == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(auth.Provider), "claude") {
			continue
		}
		states = append(states, companionPinState{
			AuthID:         auth.ID,
			PinnedToLowest: coreauth.IsPinnedToLowest(auth),
			Provider:       auth.Provider,
		})
	}
	c.JSON(http.StatusOK, gin.H{"states": states})
}

// PostCompanionClaudePinState toggles the pin-to-lowest flag on one Claude
// account. body {pinned_to_lowest: bool}. Mounted behind CompanionAuthMiddleware
// only — a holder of the management key (and only the management key) cannot
// change pin state; only the companion page can.
func (h *Handler) PostCompanionClaudePinState(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	var req struct {
		PinnedToLowest bool `json:"pinned_to_lowest"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	auth, ok := h.authManager.GetByID(id)
	if !ok || auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}

	// Pin-to-lowest is Claude-only. Provider-scoped; non-Claude accounts return
	// 400 so the companion page can't accidentally try to pin a Gemini or Codex
	// account.
	if !strings.EqualFold(strings.TrimSpace(auth.Provider), "claude") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "claude_pin_to_lowest only applies to Claude accounts"})
		return
	}

	if req.PinnedToLowest {
		if auth.Metadata == nil {
			auth.Metadata = map[string]any{}
		}
		if auth.Attributes == nil {
			auth.Attributes = map[string]string{}
		}
		auth.Metadata["claude_pin_to_lowest"] = true
		auth.Attributes["claude_pin_to_lowest"] = "true"
	} else {
		if auth.Metadata != nil {
			delete(auth.Metadata, "claude_pin_to_lowest")
		}
		if auth.Attributes != nil {
			delete(auth.Attributes, "claude_pin_to_lowest")
		}
	}

	if err := h.persistAuthUpdate(c.Request.Context(), auth); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Pin-to-lowest always means "ready to take traffic". Clearing the
	// in-memory rate-limit block is the same symmetric move
	// claude_usage_mode → dedicated performs. Order matters: persist the
	// pin first, then clear — clearing before persist would let the
	// subsequent Update re-install the previous block from the auth
	// snapshot the endpoint already mutated.
	if req.PinnedToLowest {
		coreauth.ClearRatelimitBlock(auth.ID)
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "auth_id": auth.ID, "pinned_to_lowest": req.PinnedToLowest})
}

// persistAuthUpdate writes the modified auth through the same Manager.Update
// path that PatchAuthFileFields uses for claude_usage_mode, so the
// Metadata+Attributes pair stays in sync with whatever is on disk and the
// selector sees the new state on the very next pick.
func (h *Handler) persistAuthUpdate(ctx context.Context, auth *coreauth.Auth) error {
	if h == nil || h.authManager == nil || auth == nil {
		return nil
	}
	if _, err := h.authManager.Update(ctx, auth); err != nil {
		return err
	}
	return nil
}
