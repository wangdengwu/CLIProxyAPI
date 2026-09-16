package auth

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// shanghai is the default window anchor; tests build explicit wall-clock instants in it
// so they never depend on the machine's local zone.
func shanghai(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("LoadLocation(Asia/Shanghai) error = %v", err)
	}
	return loc
}

// atShanghai builds an instant at the given wall-clock time in Asia/Shanghai.
func atShanghai(t *testing.T, hour, minute int) time.Time {
	t.Helper()
	return time.Date(2026, 9, 16, hour, minute, 0, 0, shanghai(t))
}

// withAvailabilityConfig installs an availability config for the duration of the test
// and restores the previous snapshot afterwards. Tests using it must not run in
// parallel: the snapshot is package-level process state.
func withAvailabilityConfig(t *testing.T, cfg *internalconfig.Config) {
	t.Helper()
	previous := currentAvailabilitySnapshot()
	setAvailabilityConfig(cfg)
	t.Cleanup(func() { availabilityConfig.Store(previous) })
}

// windowAuth builds a minimally-populated auth carrying only an availability window,
// so nothing else in the block chain can be the reason it is or isn't picked.
func windowAuth(window string) *Auth {
	return &Auth{
		ID:         "a",
		Attributes: map[string]string{"available_window": window},
	}
}

// availabilityConfigFor builds config as the loader would produce it: the loader seeds
// Enabled=true before unmarshalling, so a plain &Config{} (Enabled false = kill switch
// off) is NOT the default state. Empty timezone exercises the Asia/Shanghai fallback.
func availabilityConfigFor(timezone string) *internalconfig.Config {
	cfg := &internalconfig.Config{}
	cfg.AuthAvailability.Enabled = true
	cfg.AuthAvailability.Timezone = timezone
	return cfg
}

func TestIsAuthBlockedForModel_SameDayWindow(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	auth := windowAuth("09:00-18:00")

	if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, 12, 0)); blocked {
		t.Errorf("inside same-day window: blocked = true, want false")
	}
	if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, 20, 0)); !blocked {
		t.Errorf("after same-day window: blocked = false, want true")
	}
	if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, 3, 0)); !blocked {
		t.Errorf("before same-day window: blocked = false, want true")
	}
}

// TestIsAuthBlockedForModel_OvernightWindow covers the motivating case: a colleague
// lends their account only after hours, 18:00 through 09:00 the next morning.
func TestIsAuthBlockedForModel_OvernightWindow(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	auth := windowAuth("18:00-09:00")

	tests := []struct {
		name        string
		hour        int
		minute      int
		wantBlocked bool
	}{
		// Half-open: open AT start, closed AT end.
		{name: "exactly at start is open", hour: 18, minute: 0, wantBlocked: false},
		{name: "exactly at end is closed", hour: 9, minute: 0, wantBlocked: true},
		{name: "evening, after start", hour: 20, minute: 0, wantBlocked: false},
		{name: "just before midnight", hour: 23, minute: 59, wantBlocked: false},
		{name: "small hours, before end", hour: 3, minute: 0, wantBlocked: false},
		{name: "morning, just before end", hour: 8, minute: 59, wantBlocked: false},
		{name: "midday, well outside", hour: 12, minute: 0, wantBlocked: true},
		{name: "late afternoon, just before start", hour: 17, minute: 59, wantBlocked: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, tt.hour, tt.minute))
			if blocked != tt.wantBlocked {
				t.Errorf("at %02d:%02d blocked = %v, want %v", tt.hour, tt.minute, blocked, tt.wantBlocked)
			}
		})
	}
}

// TestIsAuthBlockedForModel_MalformedWindowFailsOpen is the safety-critical case: a
// hard gate that misreads a hand-typed string would silently drop an account from the
// pool with no error anywhere. Every unreadable value must let traffic through.
func TestIsAuthBlockedForModel_MalformedWindowFailsOpen(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	malformed := []struct {
		name   string
		window string
	}{
		{name: "tilde separator", window: "18:00~09:00"},
		{name: "em dash separator", window: "18:00—09:00"},
		{name: "am/pm notation", window: "6pm-9am"},
		{name: "hours without minutes", window: "18-09"},
		{name: "only one half", window: "18:00"},
		{name: "three halves", window: "18:00-09:00-10:00"},
		{name: "hour out of range", window: "25:00-09:00"},
		{name: "minute out of range", window: "18:60-09:00"},
		{name: "negative hour", window: "-1:00-09:00"},
		{name: "start equals end is ambiguous", window: "18:00-18:00"},
		{name: "non-numeric", window: "ab:cd-ef:gh"},
		{name: "empty halves", window: ":-:"},
		// 24:00 is end-of-day, but 24 with any minute past it is not a real time.
		{name: "24 with nonzero minutes", window: "18:00-24:30"},
		{name: "25 is still out of range", window: "18:00-25:00"},
		// Full-width digits from a Chinese IME look right but are not ASCII numerals.
		{name: "full-width digits", window: "１８:００-０９:００"},
		// Seconds are not part of the format.
		{name: "with seconds", window: "18:00:00-09:00:00"},
	}

	for _, tt := range malformed {
		t.Run(tt.name, func(t *testing.T) {
			auth := windowAuth(tt.window)
			// Probe both an hour inside and outside any plausible reading of the
			// string — a malformed window must never block at any time of day.
			for _, hour := range []int{3, 12, 20} {
				if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, hour, 0)); blocked {
					t.Errorf("malformed window %q blocked at %02d:00, want fail-open", tt.window, hour)
				}
			}
		})
	}
}

// TestIsAuthBlockedForModel_MidnightEndAndUnpaddedHours covers two spellings an
// operator is very likely to reach for, both of which would otherwise fail open —
// the worst outcome here, because the operator believes a restriction is in force
// while the account is in fact serving traffic around the clock.
//
//   - "18:00-24:00" is the natural way to write "from six in the evening until
//     midnight". Rejecting 24:00 as out-of-range makes that window silently vanish.
//   - "9:00-18:00" omits the leading zero, which no one thinks twice about.
func TestIsAuthBlockedForModel_MidnightEndAndUnpaddedHours(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	tests := []struct {
		name        string
		window      string
		hour        int
		wantBlocked bool
	}{
		{name: "24:00 end, inside", window: "18:00-24:00", hour: 20, wantBlocked: false},
		{name: "24:00 end, at start", window: "18:00-24:00", hour: 18, wantBlocked: false},
		{name: "24:00 end, before start", window: "18:00-24:00", hour: 12, wantBlocked: true},
		{name: "24:00 end, after midnight", window: "18:00-24:00", hour: 1, wantBlocked: true},
		{name: "00:00-24:00 is all day", window: "00:00-24:00", hour: 3, wantBlocked: false},
		{name: "unpadded start, inside", window: "9:00-18:00", hour: 12, wantBlocked: false},
		{name: "unpadded start, outside", window: "9:00-18:00", hour: 20, wantBlocked: true},
		{name: "unpadded both, overnight inside", window: "8:00-9:30", hour: 9, wantBlocked: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, _, _ := isAuthBlockedForModel(windowAuth(tt.window), "m", atShanghai(t, tt.hour, 0))
			if blocked != tt.wantBlocked {
				t.Errorf("window %q at %02d:00 blocked = %v, want %v", tt.window, tt.hour, blocked, tt.wantBlocked)
			}
		})
	}
}

// TestIsAuthBlockedForModel_MalformedWindowWarns pins the operator's only signal.
// Failing open is deliberately silent to traffic, so without this log a typo'd window
// would be indistinguishable from no window at all — the operator would believe a
// restriction is in force when nothing is. The log must name the auth and the value.
func TestIsAuthBlockedForModel_MalformedWindowWarns(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	hook := test.NewGlobal()
	t.Cleanup(hook.Reset)

	auth := &Auth{ID: "claude-a@b.c", Attributes: map[string]string{"available_window": "6pm-9am"}}
	if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, 12, 0)); blocked {
		t.Fatalf("precondition: malformed window must fail open")
	}

	var found *logrus.Entry
	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.WarnLevel && strings.Contains(entry.Message, "available_window") {
			found = entry
			break
		}
	}
	if found == nil {
		t.Fatalf("no warning logged for a malformed window; entries = %v", hook.AllEntries())
	}
	if !strings.Contains(found.Message, "6pm-9am") {
		t.Errorf("warning does not quote the offending value: %q", found.Message)
	}
	if !strings.Contains(found.Message, "claude-a@b.c") {
		t.Errorf("warning does not name the auth: %q", found.Message)
	}
}

// A well-formed window must not produce warning noise on every single pick.
func TestIsAuthBlockedForModel_ValidWindowDoesNotWarn(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	hook := test.NewGlobal()
	t.Cleanup(hook.Reset)

	for i := 0; i < 5; i++ {
		_, _, _ = isAuthBlockedForModel(windowAuth("18:00-09:00"), "m", atShanghai(t, 12, 0))
	}

	for _, entry := range hook.AllEntries() {
		if entry.Level == logrus.WarnLevel && strings.Contains(entry.Message, "available_window") {
			t.Errorf("valid window logged a warning: %q", entry.Message)
		}
	}
}

// TestIsAuthBlockedForModel_NoWindowIsAlwaysAvailable pins that the overwhelming
// majority of accounts — those that never declare a window — are untouched.
func TestIsAuthBlockedForModel_NoWindowIsAlwaysAvailable(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	cases := map[string]*Auth{
		"attribute absent":    {ID: "a"},
		"attribute empty":     windowAuth(""),
		"attribute blank":     windowAuth("   "),
		"nil attribute map":   {ID: "a", Attributes: nil},
		"unrelated attribute": {ID: "a", Attributes: map[string]string{"priority": "5"}},
	}

	for name, auth := range cases {
		t.Run(name, func(t *testing.T) {
			for _, hour := range []int{0, 9, 18, 23} {
				if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, hour, 0)); blocked {
					t.Errorf("auth without window blocked at %02d:00", hour)
				}
			}
		})
	}
}

// TestIsAuthBlockedForModel_KillSwitchDisablesAllWindows covers the operator's escape
// hatch: one config flag must return every account to all-day availability.
func TestIsAuthBlockedForModel_KillSwitchDisablesAllWindows(t *testing.T) {
	cfg := availabilityConfigFor("Asia/Shanghai")
	cfg.AuthAvailability.Enabled = false
	withAvailabilityConfig(t, cfg)

	auth := windowAuth("18:00-09:00")
	// Midday is squarely outside the window; with the kill switch off it must not block.
	if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, 12, 0)); blocked {
		t.Errorf("kill switch off but auth still blocked outside its window")
	}
}

// TestIsAuthBlockedForModel_TolerantWhitespace pins that surrounding and inner
// whitespace is accepted rather than rejected. "18:00 - 09:00" is unambiguous; failing
// it open would silently ignore a window the operator clearly meant to set — which for
// a hard gate is the more surprising outcome, not the safer one.
func TestIsAuthBlockedForModel_TolerantWhitespace(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	for _, window := range []string{" 18:00-09:00 ", "18:00 - 09:00", " 18:00 -09:00"} {
		t.Run(window, func(t *testing.T) {
			auth := windowAuth(window)
			if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, 12, 0)); !blocked {
				t.Errorf("window %q did not take effect at midday", window)
			}
			if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, 20, 0)); blocked {
				t.Errorf("window %q blocked at 20:00, inside the window", window)
			}
		})
	}
}

// TestOutsideAvailableWindow_UsesConfiguredZoneNotLocal builds an instant whose
// verdict differs between the configured zone and UTC, proving the window is anchored
// to config. In a container time.Local is usually UTC, which would shift an 18:00
// window by eight hours — the single most likely way this feature misfires silently.
func TestOutsideAvailableWindow_UsesConfiguredZoneNotLocal(t *testing.T) {
	// 12:00 UTC on 2026-09-16 is 20:00 in Shanghai.
	instant := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	auth := windowAuth("18:00-09:00")

	t.Run("shanghai reads it as 20:00 and opens", func(t *testing.T) {
		withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))
		if closed, _ := outsideAvailableWindow(auth, instant); closed {
			t.Errorf("blocked at 20:00 Shanghai, inside the window — window evaluated in the wrong zone")
		}
	})

	t.Run("utc reads the same instant as 12:00 and closes", func(t *testing.T) {
		withAvailabilityConfig(t, availabilityConfigFor("UTC"))
		if closed, _ := outsideAvailableWindow(auth, instant); !closed {
			t.Errorf("open at 12:00 UTC, outside the window — window evaluated in the wrong zone")
		}
	})
}

// TestOutsideAvailableWindow_InvalidTimezoneFallsBackToShanghai pins that a typo'd
// zone lands on Asia/Shanghai, never on the process local zone.
func TestOutsideAvailableWindow_InvalidTimezoneFallsBackToShanghai(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shangai")) // typo

	snapshot := currentAvailabilitySnapshot()
	if got := snapshot.location.String(); got != "Asia/Shanghai" {
		t.Fatalf("location = %q, want Asia/Shanghai after invalid zone", got)
	}

	// And it behaves as Shanghai: 12:00 UTC == 20:00 Shanghai, inside 18:00-09:00.
	if closed, _ := outsideAvailableWindow(windowAuth("18:00-09:00"), time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)); closed {
		t.Errorf("invalid zone did not behave as Asia/Shanghai")
	}
}

// TestOutsideAvailableWindow_EmptyTimezoneDefaultsToShanghai covers config that sets
// enabled but leaves the zone blank.
func TestOutsideAvailableWindow_EmptyTimezoneDefaultsToShanghai(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor(""))

	if got := currentAvailabilitySnapshot().location.String(); got != "Asia/Shanghai" {
		t.Fatalf("location = %q, want Asia/Shanghai for empty zone", got)
	}
}

// TestIsAuthBlockedForModel_WindowClosedReportsItsOwnReason pins the distinct reason.
// Reusing blockReasonOther would drop the account into the no-information
// "no auth available" path instead of the one that reports when it reopens.
func TestIsAuthBlockedForModel_WindowClosedReportsItsOwnReason(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	blocked, reason, _ := isAuthBlockedForModel(windowAuth("18:00-09:00"), "m", atShanghai(t, 12, 0))
	if !blocked {
		t.Fatalf("blocked = false, want true")
	}
	if reason != blockReasonWindowClosed {
		t.Errorf("reason = %v, want blockReasonWindowClosed", reason)
	}
}

// TestIsAuthBlockedForModel_ClosedWindowLeavesDisabledUntouched is the red line: token
// refresh keys on auth.Disabled. If a closed window disabled the account, its
// credentials would go stale overnight and fail exactly when the window reopens.
func TestIsAuthBlockedForModel_ClosedWindowLeavesDisabledUntouched(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	auth := windowAuth("18:00-09:00")
	if blocked, _, _ := isAuthBlockedForModel(auth, "m", atShanghai(t, 12, 0)); !blocked {
		t.Fatalf("precondition: want the auth blocked at midday")
	}
	if auth.Disabled {
		t.Errorf("auth.Disabled = true after a closed window; background token refresh would stop")
	}
	if auth.Status == StatusDisabled {
		t.Errorf("auth.Status = StatusDisabled after a closed window")
	}
	if !auth.RatelimitBlockUntil.IsZero() {
		t.Errorf("auth.RatelimitBlockUntil set by a closed window; the window must stay stateless")
	}
}

// TestSetConfig_WiresAvailabilitySnapshot proves the Manager actually installs the
// snapshot — without this the whole feature is inert no matter how correct the gate is.
func TestSetConfig_WiresAvailabilitySnapshot(t *testing.T) {
	previous := currentAvailabilitySnapshot()
	t.Cleanup(func() { availabilityConfig.Store(previous) })

	manager := NewManager(nil, nil, nil)
	manager.SetConfig(availabilityConfigFor("UTC"))

	snapshot := currentAvailabilitySnapshot()
	if !snapshot.enabled {
		t.Errorf("snapshot.enabled = false after SetConfig with enabled config")
	}
	if got := snapshot.location.String(); got != "UTC" {
		t.Errorf("snapshot.location = %q, want UTC", got)
	}

	// Hot reload must take effect on the next pick, with no restart.
	manager.SetConfig(availabilityConfigFor("Asia/Shanghai"))
	if got := currentAvailabilitySnapshot().location.String(); got != "Asia/Shanghai" {
		t.Errorf("snapshot.location = %q after reload, want Asia/Shanghai", got)
	}
}

// TestShouldRefresh_IgnoresAvailabilityWindow is direct evidence for the red line.
// Background token refresh keys on auth.Disabled and must be blind to the window: an
// account closed all night has to keep refreshing, or its credentials go stale and it
// fails the moment the window reopens — the feature breaking exactly when it matters.
// Asserting the open and closed accounts agree proves independence without having to
// construct a specific refresh verdict.
func TestShouldRefresh_IgnoresAvailabilityWindow(t *testing.T) {
	withAvailabilityConfig(t, availabilityConfigFor("Asia/Shanghai"))

	manager := NewManager(nil, nil, nil)
	midday := atShanghai(t, 12, 0) // outside 18:00-09:00, inside 09:00-18:00

	closed := windowAuth("18:00-09:00")
	open := windowAuth("09:00-18:00")
	none := &Auth{ID: "a"}

	// Precondition: the window gate really does disagree about these two right now.
	if isClosed, _ := outsideAvailableWindow(closed, midday); !isClosed {
		t.Fatalf("precondition: expected the 18:00-09:00 account to be closed at midday")
	}
	if isOpen, _ := outsideAvailableWindow(open, midday); isOpen {
		t.Fatalf("precondition: expected the 09:00-18:00 account to be open at midday")
	}

	wantSame := manager.shouldRefresh(none, midday)
	if got := manager.shouldRefresh(closed, midday); got != wantSame {
		t.Errorf("shouldRefresh(closed window) = %v, want %v — refresh must ignore the window", got, wantSame)
	}
	if got := manager.shouldRefresh(open, midday); got != wantSame {
		t.Errorf("shouldRefresh(open window) = %v, want %v", got, wantSame)
	}
}

// TestAvailabilitySnapshot_ConcurrentReadDuringReload exercises the hot path against
// concurrent hot reloads. The snapshot is package-level mutable state read on every
// pick; a data race here would surface as intermittent mis-scheduling under production
// load — exactly the kind of bug that never reproduces locally. Meaningful under -race.
func TestAvailabilitySnapshot_ConcurrentReadDuringReload(t *testing.T) {
	previous := currentAvailabilitySnapshot()
	t.Cleanup(func() { availabilityConfig.Store(previous) })

	auth := windowAuth("18:00-09:00")
	instant := atShanghai(t, 12, 0)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _, _ = isAuthBlockedForModel(auth, "m", instant)
				}
			}
		}()
	}

	zones := []string{"Asia/Shanghai", "UTC", "America/New_York", ""}
	for i := 0; i < 200; i++ {
		setAvailabilityConfig(availabilityConfigFor(zones[i%len(zones)]))
	}
	close(stop)
	wg.Wait()
}
