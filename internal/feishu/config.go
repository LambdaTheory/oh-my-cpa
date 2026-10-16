package feishu

import (
	"fmt"
	"strings"
	"time"
)

// Default notification policy constants. They are the documented starting
// points; a web setting or an environment override replaces each one.
const (
	// DefaultQuietStart and DefaultQuietEnd are the quiet window: immediate
	// pushes are held back and merged at its end.
	DefaultQuietStart = "23:00"
	DefaultQuietEnd   = "08:00"
	// DefaultSummaryTimes are the two daily digests.
	DefaultSummaryTime1 = "10:00"
	DefaultSummaryTime2 = "17:00"
	// DefaultTightRemainingPercent is the account-window remaining share below
	// which a scheduled digest is sent even with no open item.
	DefaultTightRemainingPercent = 20.0
	// DefaultChannelUnavailableAfter and DefaultMonitorDownAfter are the
	// immediate-notify thresholds: an unavailable channel whose earliest
	// recovery is later than the first, or a channel with no valid data for
	// longer than the second, is announced once.
	DefaultChannelUnavailableAfter = 30 * time.Minute
	DefaultMonitorDownAfter        = 30 * time.Minute
)

// Config is the web-configured Feishu settings. The app secret is deliberately
// absent: only HasSecret travels with the settings, so a settings read can never
// leak the secret, and the plaintext reaches the sender only through a resolved
// configuration.
type Config struct {
	Enabled bool
	AppID   string
	// HasSecret reports whether an encrypted secret is stored; it is never the
	// secret itself.
	HasSecret bool
	ChatID    string
	// SummaryTimes are the daily digest times in "HH:MM".
	SummaryTimes []string
	// Timezone is an IANA name; empty means the server location.
	Timezone string
	// QuietStart and QuietEnd bound the quiet window in "HH:MM".
	QuietStart string
	QuietEnd   string
	// TightRemainingPercent is the account-window remaining share below which a
	// scheduled digest is sent with no open item.
	TightRemainingPercent float64
	// ChannelUnavailableAfter and MonitorDownAfter are the immediate-notify
	// thresholds.
	ChannelUnavailableAfter time.Duration
	MonitorDownAfter        time.Duration
}

// Overlay is the environment override applied on top of the web settings. It is
// populated by the config/integration layer; this package only resolves it.
//
// ForceDisabled is the emergency off the web can never override. A non-nil
// field replaces the web value for that field only. The plaintext secret travels
// here and nowhere else.
type Overlay struct {
	ForceDisabled bool
	Enabled       *bool
	AppID         *string
	AppSecret     *string
	ChatID        *string
}

// Source names where the effective enable switch came from, so the console can
// show it. The values are stable wire strings.
const (
	SourceEnvDisabled = "env_disabled"
	SourceEnvEnabled  = "env_enabled"
	SourceWeb         = "web"
	SourceDefaultOff  = "default_off"
)

// Resolved is the effective configuration after the web settings and the
// environment overlay are combined and validated. The app secret is present only
// here, never in Config or in any DTO.
type Resolved struct {
	Enabled                 bool
	AppID                   string
	AppSecret               string
	ChatID                  string
	SummaryTimes            []string
	Location                *time.Location
	QuietStart              clock
	QuietEnd                clock
	TightRemainingPercent   float64
	ChannelUnavailableAfter time.Duration
	MonitorDownAfter        time.Duration
	// Source is one of the Source* constants.
	Source string
	// Degraded reports that the configuration is unusable. The module then stays
	// loaded but disabled: it must never fail process startup.
	Degraded bool
	// Reason names the first validation problem, empty when the configuration is
	// usable.
	Reason string
	// ForcedOff reports that the emergency off is in effect.
	ForcedOff bool
}

// Ready reports whether the resolved configuration can run the bot.
func (r Resolved) Ready() bool {
	return r.Enabled && !r.Degraded && r.AppID != "" && r.AppSecret != "" && r.ChatID != ""
}

// clock is a wall-clock time of day in minutes since midnight.
type clock int

// resolveConfig combines the web settings and the environment overlay and
// validates the result. It never returns an error: an unusable configuration
// yields a degraded, disabled Resolved so the caller can log and continue rather
// than fail startup. The environment force-off is applied first and wins.
func resolveConfig(web Config, overlay Overlay) Resolved {
	out := Resolved{
		Enabled:                 web.Enabled,
		AppID:                   strings.TrimSpace(web.AppID),
		ChatID:                  strings.TrimSpace(web.ChatID),
		SummaryTimes:            normalizeSummaryTimes(web.SummaryTimes),
		Location:                resolveLocation(web.Timezone),
		QuietStart:              clockOr(web.QuietStart, DefaultQuietStart),
		QuietEnd:                clockOr(web.QuietEnd, DefaultQuietEnd),
		TightRemainingPercent:   web.TightRemainingPercent,
		ChannelUnavailableAfter: web.ChannelUnavailableAfter,
		MonitorDownAfter:        web.MonitorDownAfter,
		Source:                  SourceDefaultOff,
	}

	if overlay.Enabled != nil {
		out.Enabled = *overlay.Enabled
	}
	if overlay.AppID != nil {
		out.AppID = strings.TrimSpace(*overlay.AppID)
	}
	if overlay.AppSecret != nil {
		out.AppSecret = *overlay.AppSecret
	}
	if overlay.ChatID != nil {
		out.ChatID = strings.TrimSpace(*overlay.ChatID)
	}

	// Source is resolved before validation so a force-off reads as its own
	// source even when the rest of the configuration is incomplete.
	switch {
	case overlay.ForceDisabled:
		out.Enabled = false
		out.ForcedOff = true
		out.Source = SourceEnvDisabled
	case overlay.Enabled != nil:
		if out.Enabled {
			out.Source = SourceEnvEnabled
		} else {
			out.Source = SourceEnvDisabled
		}
	case web.Enabled:
		out.Source = SourceWeb
	default:
		out.Source = SourceDefaultOff
	}

	// Validate the effective values. A forced-off module is never degraded: it is
	// intentionally silent, not broken.
	if overlay.ForceDisabled {
		return out
	}
	if reason := validateResolved(out); reason != "" {
		out.Degraded = true
		out.Enabled = false
		out.Reason = reason
	}
	return out
}

// validateResolved returns a human-readable reason the configuration cannot run,
// or "" when it is usable. Only a module that is meant to be enabled is checked;
// a disabled module is allowed to be incomplete because the operator has not
// finished configuring it yet.
func validateResolved(r Resolved) string {
	if !r.Enabled {
		return ""
	}
	if r.AppID == "" {
		return "缺少应用 App ID"
	}
	if r.AppSecret == "" {
		return "缺少应用 App Secret"
	}
	if r.ChatID == "" {
		return "缺少目标群 chat_id"
	}
	return ""
}

// normalizeSummaryTimes parses the configured digest times, dropping anything
// unparsable. An empty result falls back to the documented pair, so a blank web
// setting still produces the standard two digests.
func normalizeSummaryTimes(raw []string) []string {
	out := make([]string, 0, len(raw))
	for _, value := range raw {
		if normalized, ok := normalizeClockString(value); ok {
			out = append(out, normalized)
		}
	}
	if len(out) == 0 {
		return []string{DefaultSummaryTime1, DefaultSummaryTime2}
	}
	return out
}

// clockOr parses an "HH:MM" value, falling back to the default when it is empty
// or unparsable.
func clockOr(value, fallback string) clock {
	if parsed, ok := parseClock(value); ok {
		return parsed
	}
	return mustClock(fallback)
}

// parseClock parses an "HH:MM" time of day.
func parseClock(value string) (clock, bool) {
	value = strings.TrimSpace(value)
	parsed, err := time.Parse("15:04", value)
	if err != nil {
		return 0, false
	}
	return clock(parsed.Hour()*60 + parsed.Minute()), true
}

// normalizeClockString returns the canonical "HH:MM" text for a valid value.
func normalizeClockString(value string) (string, bool) {
	parsed, ok := parseClock(value)
	if !ok {
		return "", false
	}
	return parsed.String(), true
}

func mustClock(value string) clock {
	parsed, _ := parseClock(value)
	return parsed
}

// String renders a clock as "HH:MM".
func (c clock) String() string {
	return fmt.Sprintf("%02d:%02d", int(c)/60, int(c)%60)
}

// resolveLocation resolves an IANA time zone, falling back to the server
// location for an empty or unknown name.
func resolveLocation(name string) *time.Location {
	name = strings.TrimSpace(name)
	if name == "" {
		return time.Local
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		return time.Local
	}
	return location
}
