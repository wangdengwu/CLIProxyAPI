package auth

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// decodeErrorBody unmarshals the JSON envelope an unavailability error renders from
// Error() and returns the inner error object.
func decodeErrorBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("json.Unmarshal(Error()) error = %v; body = %q", err, raw)
	}
	body, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("Error() payload missing error object: %v", payload)
	}
	return body
}

// TestModelCooldownError_RenderingContract pins the full wire contract of the
// model-cooldown error: code, message wording, field set, duration formatting and
// headers. Task 1 parameterizes this rendering so a second error code can reuse it;
// this test is the safety net proving that refactor changes nothing observable.
func TestModelCooldownError_RenderingContract(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		model        string
		provider     string
		resetIn      time.Duration
		wantMessage  string
		wantModel    string
		wantResetStr string
		wantResetSec float64
		wantProvider string // "" means the field must be absent
	}{
		{
			name:         "model and provider present",
			model:        "gemini-2.5-pro",
			provider:     "gemini",
			resetIn:      90 * time.Second,
			wantMessage:  "All credentials for model gemini-2.5-pro are cooling down via provider gemini",
			wantModel:    "gemini-2.5-pro",
			wantResetStr: "1m30s",
			wantResetSec: 90,
			wantProvider: "gemini",
		},
		{
			name:         "provider empty omits provider field and suffix",
			model:        "claude-opus-4-8",
			provider:     "",
			resetIn:      30 * time.Second,
			wantMessage:  "All credentials for model claude-opus-4-8 are cooling down",
			wantModel:    "claude-opus-4-8",
			wantResetStr: "30s",
			wantResetSec: 30,
			wantProvider: "",
		},
		{
			name:         "empty model falls back to placeholder wording",
			model:        "",
			provider:     "",
			resetIn:      5 * time.Second,
			wantMessage:  "All credentials for model requested model are cooling down",
			wantModel:    "",
			wantResetStr: "5s",
			wantResetSec: 5,
			wantProvider: "",
		},
		{
			name:         "sub-second reset displays as one second but keeps ceil seconds",
			model:        "m",
			provider:     "",
			resetIn:      250 * time.Millisecond,
			wantMessage:  "All credentials for model m are cooling down",
			wantModel:    "m",
			wantResetStr: "1s",
			wantResetSec: 1,
			wantProvider: "",
		},
		{
			name:         "fractional reset rounds the display and ceils the seconds",
			model:        "m",
			provider:     "",
			resetIn:      1500 * time.Millisecond,
			wantMessage:  "All credentials for model m are cooling down",
			wantModel:    "m",
			wantResetStr: "2s",
			wantResetSec: 2,
			wantProvider: "",
		},
		{
			name:         "zero reset renders zero",
			model:        "m",
			provider:     "",
			resetIn:      0,
			wantMessage:  "All credentials for model m are cooling down",
			wantModel:    "m",
			wantResetStr: "0s",
			wantResetSec: 0,
			wantProvider: "",
		},
		{
			name:         "negative reset is clamped to zero at construction",
			model:        "m",
			provider:     "",
			resetIn:      -5 * time.Second,
			wantMessage:  "All credentials for model m are cooling down",
			wantModel:    "m",
			wantResetStr: "0s",
			wantResetSec: 0,
			wantProvider: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := newModelCooldownError(tt.model, tt.provider, tt.resetIn)
			body := decodeErrorBody(t, err.Error())

			if got, _ := body["code"].(string); got != "model_cooldown" {
				t.Errorf("error.code = %q, want %q", got, "model_cooldown")
			}
			if got, _ := body["message"].(string); got != tt.wantMessage {
				t.Errorf("error.message = %q, want %q", got, tt.wantMessage)
			}
			if got, _ := body["model"].(string); got != tt.wantModel {
				t.Errorf("error.model = %q, want %q", got, tt.wantModel)
			}
			if got, _ := body["reset_time"].(string); got != tt.wantResetStr {
				t.Errorf("error.reset_time = %q, want %q", got, tt.wantResetStr)
			}
			if got, _ := body["reset_seconds"].(float64); got != tt.wantResetSec {
				t.Errorf("error.reset_seconds = %v, want %v", got, tt.wantResetSec)
			}
			if tt.wantProvider == "" {
				if _, exists := body["provider"]; exists {
					t.Errorf("error.provider present = %v, want absent", body["provider"])
				}
			} else if got, _ := body["provider"].(string); got != tt.wantProvider {
				t.Errorf("error.provider = %q, want %q", got, tt.wantProvider)
			}

			if got := err.StatusCode(); got != http.StatusTooManyRequests {
				t.Errorf("StatusCode() = %d, want %d", got, http.StatusTooManyRequests)
			}

			headers := err.Headers()
			if got := headers.Get("Content-Type"); got != "application/json" {
				t.Errorf("Headers().Content-Type = %q, want %q", got, "application/json")
			}
			wantRetryAfter := strconv.Itoa(int(tt.wantResetSec))
			if got := headers.Get("Retry-After"); got != wantRetryAfter {
				t.Errorf("Headers().Retry-After = %q, want %q", got, wantRetryAfter)
			}
		})
	}
}

// TestNewUnavailabilityError_CarriesCallerCodeAndPhrase proves the rendering path is
// parameterized: a caller can supply its own code and phrase and get the identical
// envelope shape, headers and duration handling. Task 3 uses this to add an
// "outside availability window" error without duplicating the renderer.
func TestNewUnavailabilityError_CarriesCallerCodeAndPhrase(t *testing.T) {
	t.Parallel()

	err := newUnavailabilityError("auth_window_closed", "are outside their availability window", "claude-opus-4-8", "claude", 90*time.Second)
	body := decodeErrorBody(t, err.Error())

	if got, _ := body["code"].(string); got != "auth_window_closed" {
		t.Errorf("error.code = %q, want %q", got, "auth_window_closed")
	}
	wantMessage := "All credentials for model claude-opus-4-8 are outside their availability window via provider claude"
	if got, _ := body["message"].(string); got != wantMessage {
		t.Errorf("error.message = %q, want %q", got, wantMessage)
	}
	// Shape must match the cooldown error exactly — same fields, same duration handling.
	if got, _ := body["model"].(string); got != "claude-opus-4-8" {
		t.Errorf("error.model = %q, want %q", got, "claude-opus-4-8")
	}
	if got, _ := body["provider"].(string); got != "claude" {
		t.Errorf("error.provider = %q, want %q", got, "claude")
	}
	if got, _ := body["reset_time"].(string); got != "1m30s" {
		t.Errorf("error.reset_time = %q, want %q", got, "1m30s")
	}
	if got, _ := body["reset_seconds"].(float64); got != 90 {
		t.Errorf("error.reset_seconds = %v, want 90", got)
	}
	if got := err.StatusCode(); got != http.StatusTooManyRequests {
		t.Errorf("StatusCode() = %d, want %d", got, http.StatusTooManyRequests)
	}
	if got := err.Headers().Get("Retry-After"); got != "90" {
		t.Errorf("Headers().Retry-After = %q, want %q", got, "90")
	}
}

// TestNewUnavailabilityError_OmitsProviderWhenEmpty pins that the shared renderer's
// provider-omission rule applies to caller-supplied codes too, not just cooldown.
func TestNewUnavailabilityError_OmitsProviderWhenEmpty(t *testing.T) {
	t.Parallel()

	err := newUnavailabilityError("auth_window_closed", "are outside their availability window", "m", "", 0)
	body := decodeErrorBody(t, err.Error())

	if _, exists := body["provider"]; exists {
		t.Errorf("error.provider present = %v, want absent", body["provider"])
	}
	wantMessage := "All credentials for model m are outside their availability window"
	if got, _ := body["message"].(string); got != wantMessage {
		t.Errorf("error.message = %q, want %q", got, wantMessage)
	}
}
