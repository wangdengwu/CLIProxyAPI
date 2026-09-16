package auth

import (
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// defaultAvailabilityTimezone anchors availability windows when config is absent or
// names an unloadable zone. Deliberately not time.Local: in a container that is
// usually UTC, which would silently shift an 18:00 window by eight hours.
const defaultAvailabilityTimezone = "Asia/Shanghai"

// availabilityAttributeKey is the auth attribute (mirrored from the auth file's
// top-level key) holding the window string.
const availabilityAttributeKey = "available_window"

// availabilitySnapshot is the immutable view of availability config used on the
// selection hot path. The location is resolved once, when config is set — loading a
// zone touches the filesystem and must never happen per pick.
type availabilitySnapshot struct {
	enabled  bool
	location *time.Location
}

// availabilityConfig holds the current *availabilitySnapshot. It is package-level for
// the same reason activeRatelimitTarget is: the block decision happens inside
// isAuthBlockedForModel, which is reached through the public Selector interface and
// therefore cannot take config as a parameter without breaking that interface.
var availabilityConfig atomic.Value

func init() {
	// atomic.Value requires a non-nil initial value; a nil read before the first
	// SetConfig would otherwise panic on the hot path.
	availabilityConfig.Store(defaultAvailabilitySnapshot())
}

func defaultAvailabilitySnapshot() *availabilitySnapshot {
	return &availabilitySnapshot{enabled: true, location: mustLoadAvailabilityLocation("")}
}

// setAvailabilityConfig refreshes the snapshot from application config. Called on
// startup and on every hot reload.
func setAvailabilityConfig(cfg *internalconfig.Config) {
	if cfg == nil {
		availabilityConfig.Store(defaultAvailabilitySnapshot())
		return
	}
	availabilityConfig.Store(&availabilitySnapshot{
		enabled:  cfg.AuthAvailability.Enabled,
		location: mustLoadAvailabilityLocation(cfg.AuthAvailability.Timezone),
	})
}

func currentAvailabilitySnapshot() *availabilitySnapshot {
	snapshot, _ := availabilityConfig.Load().(*availabilitySnapshot)
	if snapshot == nil {
		return defaultAvailabilitySnapshot()
	}
	return snapshot
}

// mustLoadAvailabilityLocation resolves name, falling back to Asia/Shanghai and only
// then to the system local zone (a stripped container may ship no tzdata at all).
func mustLoadAvailabilityLocation(name string) *time.Location {
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		if loc, err := time.LoadLocation(trimmed); err == nil {
			return loc
		}
		log.Warnf("auth-availability: cannot load timezone %q, falling back to %s", trimmed, defaultAvailabilityTimezone)
	}
	if loc, err := time.LoadLocation(defaultAvailabilityTimezone); err == nil {
		return loc
	}
	return time.Local
}

// availabilityWindow is a parsed window expressed as minutes-of-day. It is half-open:
// an auth is available at start and unavailable at end.
type availabilityWindow struct {
	startMinute int
	endMinute   int
}

// parseAvailabilityWindow parses "HH:MM-HH:MM". ok is false for anything it cannot
// read with certainty, including start == end, whose meaning ("always open" or "never
// open"?) is ambiguous and must not be guessed.
func parseAvailabilityWindow(raw string) (availabilityWindow, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return availabilityWindow{}, false
	}
	parts := strings.Split(trimmed, "-")
	if len(parts) != 2 {
		return availabilityWindow{}, false
	}
	start, okStart := parseAvailabilityMinute(parts[0])
	end, okEnd := parseAvailabilityMinute(parts[1])
	if !okStart || !okEnd || start == end {
		return availabilityWindow{}, false
	}
	return availabilityWindow{startMinute: start, endMinute: end}, true
}

// minutesPerDay is the exclusive upper bound of a wall-clock minute-of-day, and the
// value "24:00" resolves to.
const minutesPerDay = 24 * 60

// ValidAvailabilityWindow reports whether raw is a window this package will honour.
//
// Exported so the management write path validates with the exact same parser the
// scheduler gates on. Two parsers would eventually disagree about a value like
// "18:00-18:00", and the UI would report success for a window the gate ignores.
// A blank string is valid and means "no window" (all-day availability).
func ValidAvailabilityWindow(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	_, ok := parseAvailabilityWindow(raw)
	return ok
}

// parseAvailabilityMinute parses "HH:MM" into minutes since midnight.
//
// Accepts "24:00" as end-of-day (1440). "18:00-24:00" is the natural way to write
// "from six in the evening until midnight"; rejecting it would fail open and silently
// discard a window the operator believes is in force. Unpadded hours ("9:00") are
// likewise accepted — nobody thinks twice about writing them.
func parseAvailabilityMinute(raw string) (int, bool) {
	trimmed := strings.TrimSpace(raw)
	hourText, minuteText, found := strings.Cut(trimmed, ":")
	if !found {
		return 0, false
	}
	hour, errHour := strconv.Atoi(strings.TrimSpace(hourText))
	minute, errMinute := strconv.Atoi(strings.TrimSpace(minuteText))
	if errHour != nil || errMinute != nil {
		return 0, false
	}
	if hour == 24 && minute == 0 {
		return minutesPerDay, true
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// crossesMidnight reports whether the window wraps past midnight (e.g. 18:00-09:00).
func (w availabilityWindow) crossesMidnight() bool {
	return w.startMinute > w.endMinute
}

// contains reports whether the wall-clock minute-of-day falls inside the window.
// Half-open in both shapes: open at start, closed at end.
func (w availabilityWindow) contains(minuteOfDay int) bool {
	if w.crossesMidnight() {
		return minuteOfDay >= w.startMinute || minuteOfDay < w.endMinute
	}
	return minuteOfDay >= w.startMinute && minuteOfDay < w.endMinute
}

// Code and phrase for the "every account is off the clock" flavour of the shared
// unavailability 429. Distinct from model cooldown so an operator reading the error
// can tell "everyone is rate-limited" from "everyone is outside their hours".
const (
	availabilityWindowErrorCode   = "auth_window_closed"
	availabilityWindowErrorPhrase = "are outside their availability window"
)

// newAvailabilityWindowError builds the 429 returned when every candidate is closed.
func newAvailabilityWindowError(model, provider string, resetIn time.Duration) *modelCooldownError {
	return newUnavailabilityError(availabilityWindowErrorCode, availabilityWindowErrorPhrase, model, provider, resetIn)
}

// nextOpenAt returns the next instant this window opens, at or after now.
//
// The instant is constructed by calendar date in the target zone rather than by adding
// a fixed number of hours to now: a DST transition day is not 24 hours long, and while
// Asia/Shanghai has no DST today the timezone is operator-configurable. Passing the
// start as a minute offset also lets time.Date normalize 24:00 into the next midnight.
func (w availabilityWindow) nextOpenAt(loc *time.Location, now time.Time) time.Time {
	local := now.In(loc)
	candidate := time.Date(local.Year(), local.Month(), local.Day(), 0, w.startMinute, 0, 0, loc)
	if !candidate.After(local) {
		candidate = time.Date(local.Year(), local.Month(), local.Day()+1, 0, w.startMinute, 0, 0, loc)
	}
	return candidate
}

// outsideAvailableWindow reports whether auth is currently outside its declared
// availability window, and when that window next opens.
//
// Every uncertain path deliberately fails OPEN (returns false). A hard gate's every
// misjudgement silently removes an account from the pool, so at runtime the bias is
// always toward letting traffic through; rejecting typos is the write path's job.
func outsideAvailableWindow(auth *Auth, now time.Time) (bool, time.Time) {
	if auth == nil {
		return false, time.Time{}
	}
	snapshot := currentAvailabilitySnapshot()
	if !snapshot.enabled {
		return false, time.Time{}
	}
	raw := auth.AvailableWindow()
	if raw == "" {
		return false, time.Time{}
	}
	window, ok := parseAvailabilityWindow(raw)
	if !ok {
		log.Warnf("auth-availability: auth %s has unparseable available_window %q; treating as available all day", auth.ID, raw)
		return false, time.Time{}
	}
	local := now.In(snapshot.location)
	if window.contains(local.Hour()*60 + local.Minute()) {
		return false, time.Time{}
	}
	return true, window.nextOpenAt(snapshot.location, now)
}
