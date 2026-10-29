// Package ollama reads Ollama Cloud quota from the documented balance endpoint,
// falling back to the account settings page.
//
// An account may store an API key, a session cookie, or both. When a key exists the
// client reads the fixed https://ollama.com/api/balance JSON endpoint with a Bearer
// header; if that path fails it reads the fixed settings URL with the cookie and
// parses the returned HTML. The JSON path is authoritative when it succeeds, and a
// key failure is never folded into a cookie reading. Either upstream can change, so
// a failure degrades to an unavailable snapshot rather than to wrong numbers.
//
// This is a fresh implementation for Oh My CPA. The settings-page parsing approach
// is informed by the retired CPA-buddy (cpab) Ollama collector, which in turn credits
// jacklee-code/ollama-cloud-quota-monitor and Wei-Shaw/sub2api. That lineage is
// LGPL-3.0-only; before this package is merged for distribution, its licensing
// and the repository's THIRD_PARTY_NOTICES obligation must be reviewed. Only the
// approach was reused, not the source text.
//
// The balance-endpoint mapping is derived from the MIT-licensed CodexBar
// (Plugins/ollama-api.ts): remaining is included.balance_usd, used is
// allowance_usd minus included.balance_usd, and purchased.balance_usd is a
// separate credit balance rather than part of the included allowance. The Go
// client, validation and wire mapping here are this project's own.
//
// SECURITY: the session cookie and API key are high-value secrets. Neither may
// appear in logs, error strings, snapshots or any returned value, and each is sent
// only to its fixed Ollama origin.
package ollama

import (
	"errors"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// ErrAccountNotFound is returned by a per-account refresh when the account does
// not exist (or has just been deleted). The console maps it to a 404.
var ErrAccountNotFound = errors.New("ollama: account not found")

// ErrServiceNotStarted is returned by a per-account refresh before the collector's
// process lifetime context is installed. It is the safe refusal for a request that
// arrives before Start; it is never a reason to fall back to the request context.
var ErrServiceNotStarted = errors.New("ollama: collector is not started")

// ErrRefreshSuperseded is returned by a manual refresh when the scrape read a
// credential the operator replaced while it was running, or coalesced onto an automatic scrape
// whose reading was refused because the account is now paused. The reading was not
// persisted, so the console reports the refresh as incomplete rather than as an old
// success; it is not retried automatically.
var ErrRefreshSuperseded = errors.New("ollama: refresh superseded by a newer change")

// Provider is the display provider this feature reports. It is deliberately not
// a CPA credential provider: an Ollama account read here is decoupled from CPA's
// credential status.
const Provider = "ollama"

// SettingsURL is the only page scraped. It is a constant, not a user input: the
// console never lets an operator point the collector at an arbitrary address.
const SettingsURL = "https://ollama.com/settings"

// Defaults chosen for the collector. They live here rather than in config so the
// wiring contract can pass overrides; the integration owner decides whether they
// become environment settings.
const (
	DefaultInterval    = 15 * time.Minute
	DefaultTimeout     = 20 * time.Second
	DefaultBackoffBase = 5 * time.Minute
	DefaultBackoffMax  = 6 * time.Hour

	// DefaultManualMinGap is the shortest interval between two console-forced
	// refreshes of one account. It exists so a held-down button cannot hammer the
	// settings page; a normal operator action is well outside it.
	DefaultManualMinGap = 30 * time.Second
	// DefaultConcurrency caps how many scrapes run at once across the periodic
	// pass, the create/update triggers and manual refreshes, so a burst of newly
	// added accounts does not open a connection per account.
	DefaultConcurrency = 2
	// DefaultRefreshTimeout bounds one manual refresh end to end - waiting for a
	// concurrency permit, the scrape and the reload - so a saturated service cannot
	// hold an HTTP request open indefinitely.
	DefaultRefreshTimeout = 20 * time.Second
)

// Failure names why a scrape did not produce a quota reading. The zero value is a
// successful read.
type Failure string

const (
	FailureNone      Failure = ""
	FailureAuth      Failure = "auth"
	FailureTransport Failure = "transport"
	FailureParse     Failure = "parse"
)

// Collection status values persisted on an account.
const (
	StatusOK             = "ok"
	StatusAuthInvalid    = "auth_invalid"
	StatusTransportError = "transport_error"
	StatusParseError     = "parse_error"
)

// errSessionInvalid is the scrubbed message for an expired or absent session. It
// carries no cookie material.
const errSessionInvalid = "Ollama session cookie is invalid or expired"

// Window is one quota window recovered from the settings page.
//
// A window exists only when the page published a numeric percentage. A lone reset
// hint never fabricates a window. Duration is left zero because the settings page
// does not state a window length: the console must not be told "5h" when that was
// only an inference from a label.
type Window struct {
	Name         string
	Label        string
	UsedPercent  *float64
	ResetAt      *time.Time
	ResetText    string
	ResetDerived bool
}

// Snapshot is the outcome of one scrape. A transport failure is returned as a
// Go error instead of a Snapshot; auth and parse failures are Snapshot values
// with OK false.
type Snapshot struct {
	Credits    *quota.OllamaCredits
	AccountID  string
	ObservedAt time.Time
	Plan       string
	Balance    string
	Windows    []Window
	Failure    Failure
	// Err is a scrubbed explanation for a non-success snapshot.
	Err string
	// RetryAfter is honoured for an upstream 429.
	RetryAfter    time.Duration
	HasRetryAfter bool
}

// IsSuccess requires two validated USD amounts or a legacy numeric window.
func (s Snapshot) IsSuccess() bool {
	return s.Failure == FailureNone && (len(s.Windows) > 0 ||
		(s.Credits != nil && s.Credits.RemainingCents != nil && s.Credits.UsedCents != nil))
}

// ToQuota turns a snapshot into the console's shared quota shape.
//
// The auth index is namespaced "ollama:<id>" so it can never collide with a real
// CPA auth index, and the provider is the display family "ollama". This keeps the
// reading decoupled from CPA credential status: it is a monitor-only observation.
func (s Snapshot) ToQuota(label string) *quota.NormalizedQuota {
	value := &quota.NormalizedQuota{
		OllamaCredits: s.Credits,
		AuthIndex:     "ollama:" + strings.TrimSpace(s.AccountID),
		Name:          label,
		Type:          Provider,
		Provider:      Provider,
		Status:        quotaStatusFor(s.Failure),
		ObservedAtMS:  s.ObservedAt.UnixMilli(),
		Windows:       make([]quota.QuotaWindow, 0, len(s.Windows)),
		Error:         s.Err,
	}
	if s.Plan != "" || s.Balance != "" {
		plan := &quota.QuotaPlan{}
		if s.Plan != "" {
			plan.PlanType = strings.ToLower(s.Plan)
			plan.PlanLabel = s.Plan
		}
		if s.Balance != "" {
			plan.Credits = &quota.QuotaCredits{Balance: s.Balance}
			value.RawSignals = map[string]string{"ollama_balance_source": "settings_balance"}
		}
		value.Plan = plan
	}
	for _, window := range s.Windows {
		value.Windows = append(value.Windows, window.toQuotaWindow())
	}
	return value
}

func (w Window) toQuotaWindow() quota.QuotaWindow {
	result := quota.QuotaWindow{
		ID:    w.Name,
		Label: w.Label,
		Kind:  windowKind(w.Name),
		Scope: quota.ObservationScopeStandard,
		// PeriodHours is deliberately left nil: neither the settings page nor the
		// label is trusted as a window duration.
	}
	if w.UsedPercent != nil {
		used := *w.UsedPercent
		result.UsedPercent = &used
		remaining := 100 - used
		if remaining < 0 {
			remaining = 0
		}
		result.RemainingPercent = &remaining
	}
	if w.ResetAt != nil {
		at := w.ResetAt.UnixMilli()
		result.ResetAtMS = &at
	}
	switch {
	case w.ResetText != "":
		result.ResetLabel = w.ResetText
	case w.ResetDerived:
		result.ResetAccuracy = "derived"
	case w.ResetAt != nil:
		result.ResetAccuracy = "exact"
	}
	return result
}

func windowKind(name string) string {
	switch name {
	case "5h":
		return "five_hour"
	case "7d":
		return "weekly"
	default:
		return name
	}
}

func quotaStatusFor(failure Failure) string {
	switch failure {
	case FailureNone:
		return "healthy"
	default:
		return "error"
	}
}

// StatusForFailure maps a failure onto the persisted collection status.
func StatusForFailure(failure Failure) string {
	switch failure {
	case FailureNone:
		return StatusOK
	case FailureAuth:
		return StatusAuthInvalid
	case FailureParse:
		return StatusParseError
	default:
		return StatusTransportError
	}
}
