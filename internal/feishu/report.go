package feishu

import (
	"context"
	"strconv"
	"time"
)

// Credential display states. They mirror the states OMC's quota layer exposes so
// the report adapter can map one onto the other without the feishu package
// importing the quota or domain packages. An unknown value is treated as
// unknown, never as healthy and never as zero.
const (
	StateHealthy   = "healthy"
	StateLimited   = "limited" // a model- or group-scoped window is limited
	StateNotice    = "notice"
	StateWarning   = "warning"
	StateExhausted = "exhausted"
	StateInvalid   = "invalid"
	StateSuspect   = "suspect"
	StateStale     = "stale"
	StateUnknown   = "unknown"
)

// Report is the minimal, read-only projection of OMC's newest quota data that
// the bot answers from. It is a small internal DTO rather than a copy of OMC's
// domain model, so this package depends on neither the API layer nor the quota
// package and therefore cannot trigger an upstream refresh.
//
// GeneratedAt is the observation time of the newest evaluation. A query shows
// it and grades its age; it is never replaced by "now".
type Report struct {
	GeneratedAt time.Time
	Providers   []ProviderReport
}

// ProviderReport is one display channel's reading.
type ProviderReport struct {
	// Provider is the display channel name, e.g. "codex", "claude",
	// "gemini-cli", "antigravity" or "ollama".
	Provider string
	// Error is a collector-level failure message for the whole channel. It is
	// set when the channel is configured but produced no account reading at
	// all. It is scrubbed text, never a raw upstream payload.
	Error    string
	Accounts []AccountReport
}

// AccountReport is one credential's reading within a channel.
type AccountReport struct {
	AttemptedAt      time.Time
	CollectionFailed bool
	// Key is the opaque credential identity (the auth index). It is a
	// persistence key, not text for a person.
	Key string
	// Label is the human-facing account label. It may be empty.
	Label string
	// Disabled reports an operator's deliberate CPA disable. A disabled account
	// is never the evidence for a channel outage or a monitor-down alert.
	Disabled bool
	// IsUnmonitored excludes credentials without a quota reader from channel
	// evidence, but never suppresses an independently evidenced auth failure.
	IsUnmonitored bool
	// State is one of the State* constants.
	State string
	// OK reports whether the stored snapshot contains a valid numeric reading. An
	// exhausted account reports OK true and a maxed window, so availability is
	// decided from the windows, not from OK alone.
	OK bool
	// Windows are the account's quota windows. Only window Scope "account"
	// decides whole-account availability; a model- or group-scoped window
	// running out does not.
	Windows []Window
	// CooldownUntil is the latest non-model cooldown recovery for this account,
	// nil when none is active. A model-scoped cooldown is not whole-account
	// unavailability.
	CooldownUntil *time.Time
	// LastSuccessAt is when this account last produced a valid reading. It is
	// what monitor_down is timed from, never the timestamp of a failed attempt.
	LastSuccessAt time.Time
	// ObservedAt is when this account's newest reading was taken.
	ObservedAt time.Time
	// MetadataObservedAt is when this account's disable/identity metadata was
	// last synced from the trusted discovery path. It is a cache timestamp, not
	// a reading: the Disabled flag is only as current as this instant, so a
	// reader must not present it as live. The zero value means unknown.
	MetadataObservedAt time.Time
	// AuthFailed reports that the newest unrefuted collection evidence is an
	// authentication failure. It is set from the stored, enum-only collection
	// signal, never from a cached availability flag: an unknown or stale state
	// must not close an open credential item, and only a successful reading
	// observed after the auth failure clears it.
	AuthFailed bool
}

// Window is one quota window as a person reads it. A window exists only when the
// upstream published a numeric percentage; a missing percentage is "not
// reported", never zero and never one hundred.
type Window struct {
	ID            string
	Kind          string
	ResetAccuracy string
	// Name is the internal window key (e.g. "5h", "7d").
	Name string
	// Label is the readable window label.
	Label string
	// Scope is "account", "model" or "group". Only "account" decides
	// whole-account availability.
	Scope string
	// UsedPercent is the reported used share in [0,100], or nil when the
	// provider published none.
	UsedPercent *float64
	// ResetAt is the window's reset instant, or nil when unknown.
	ResetAt *time.Time
}

// ScopeAccount is the only window scope that decides whole-account availability.
const ScopeAccount = "account"

// MetadataFresh reports whether this account's disable/identity metadata is
// recent enough to count as current evidence. The sync instant is a cache time,
// not a reading time: a zero instant is unknown, never fresh, so stale or untimed
// metadata can never manufacture a current auth failure.
func (a AccountReport) MetadataFresh(now time.Time, window time.Duration) bool {
	if window <= 0 {
		window = defaultFreshWindow
	}
	if a.MetadataObservedAt.IsZero() {
		return false
	}
	age := now.Sub(a.MetadataObservedAt)
	if age < 0 {
		age = 0
	}
	return age <= window
}

// ReportSource supplies the newest evaluated report. It is implemented by the
// integration owner over OMC's repository. has is true when stored channels are
// known, even before their first success. ReadReport must not trigger a refresh.
type ReportSource interface {
	ReadReport(ctx context.Context) (Report, bool)
}

// ReportSourceFunc adapts a function to ReportSource so tests and the
// integration layer can inject a report without a concrete type.
type ReportSourceFunc func(ctx context.Context) (Report, bool)

// ReadReport implements ReportSource.
func (f ReportSourceFunc) ReadReport(ctx context.Context) (Report, bool) {
	return f(ctx)
}

// freshness describes how current an answer is. The distinction that matters to
// a reader is three-way, not two-way:
//
//	live    — collected within one poll interval, i.e. the newest data that can
//	          exist; saying "采集失败" here would be a false alarm
//	expired — older than one poll interval, so the numbers may have moved
//	absent  — no report has ever been produced
type freshness struct {
	live      bool
	expired   bool
	available bool
	age       time.Duration
}

// defaultFreshWindow is used when no poll interval is configured. Cached data
// younger than the collection cadence is simply the newest data that exists.
const defaultFreshWindow = 15 * time.Minute

// DefaultFreshWindow is the freshness window a cached reading or collection signal
// must fall within to count as current evidence. It is exported so the stored-only
// integration source grades a collection signal with the same window the bot uses
// to grade a reading.
const DefaultFreshWindow = defaultFreshWindow

// summaryFreshWindow bounds how old the reading behind a scheduled-digest or
// startup tight warning may be. A digest that fires at 10:00 must not warn that
// a channel is nearly out from a sample taken hours earlier, and a stale maxed
// window must not be read as "0 remaining".
const summaryFreshWindow = defaultFreshWindow

// freshnessOf classifies a report by its observation age.
func freshnessOf(rep Report, window time.Duration, now time.Time) freshness {
	if window <= 0 {
		window = defaultFreshWindow
	}
	f := freshness{live: true, available: !rep.GeneratedAt.IsZero()}
	if f.available {
		f.age = now.Sub(rep.GeneratedAt)
		if f.age < 0 {
			f.age = 0
		}
		f.live = f.age <= window
		f.expired = !f.live
	}
	return f
}

// label is the short suffix shown next to the data time.
func (f freshness) label() string {
	switch {
	case !f.available:
		return "时间未知"
	case f.live:
		return "实时"
	case f.expired:
		return "缓存 · " + humanAge(f.age)
	default:
		return "缓存 · " + humanAge(f.age)
	}
}

// notice is the prominent warning line. It is empty for an ordinary still-current
// reading, because that is not a failure.
func (f freshness) notice() string {
	if !f.expired || !f.available {
		return ""
	}
	return "以下为 " + humanAge(f.age) + "的观察数据，可能已过期。"
}

// humanAge renders an age the way a person would say it.
func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "刚刚"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + " 分钟前"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + " 小时前"
	default:
		return strconv.Itoa(int(d.Hours()/24)) + " 天前"
	}
}
