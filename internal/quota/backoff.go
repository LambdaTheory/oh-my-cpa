package quota

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RateLimitError is what the active refresh paths return when an upstream
// answered 429. It is a refusal an operator or agent can act on: the credential
// (and its provider) is parked until RetryAfter and must not be probed again.
//
// It implements capability.DetailedError through FailureDetail, so the error's
// code (rate_limited_backoff) and its detail survive the capability envelope.
type RateLimitError struct {
	Provider      string
	AuthIndex     string
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *RateLimitError) Error() string { return "rate_limited_backoff" }

// IsRateLimited lets bounded consuming adapters preserve this refusal without
// importing quota and turning it into an uncertain transport failure.
func (e *RateLimitError) IsRateLimited() bool { return true }

func (e *RateLimitError) FailureDetail() string {
	if e.RetryAfter > 0 {
		return "upstream rate limited; retry after " + e.RetryAfter.Round(time.Second).String()
	}
	return "upstream rate limited; retry later"
}

// rateLimitFrom extracts a RateLimitError from an error chain, or nil.
func rateLimitFrom(err error) *RateLimitError {
	if err == nil {
		return nil
	}
	var rateErr *RateLimitError
	if errors.As(err, &rateErr) {
		return rateErr
	}
	return nil
}

// NewRateLimitError builds the typed refusal all consuming callers propagate.
func NewRateLimitError(provider, authIndex string, headers map[string][]string) *RateLimitError {
	retryAfter, hasRetryAfter := parseRetryAfter(headers)
	return &RateLimitError{
		Provider:      strings.TrimSpace(provider),
		AuthIndex:     strings.TrimSpace(authIndex),
		RetryAfter:    retryAfter,
		HasRetryAfter: hasRetryAfter,
	}
}

// Backoff is the in-memory record of when each credential and provider may next
// be probed after a 429.
//
// It is deliberately not persisted: a restart clears it, and the worst a cleared
// state can do is probe an upstream once more. Retry-After is honoured when the
// upstream published it; otherwise the wait doubles from Base up to Max, so a
// provider that keeps refusing is probed at a decreasing rate rather than in a
// tight loop.
type Backoff struct {
	mu sync.Mutex
	// now is injectable so tests do not sleep.
	now func() time.Time
	// Base and Max shape the exponential fallback.
	Base time.Duration
	Max  time.Duration

	credentials map[string]time.Time
	providers   map[string]time.Time
	// providerOwners records which credential created the current provider park,
	// so one concurrent sibling's successful probe cannot clear another's refusal.
	providerOwners map[string]string
	attempts       map[string]int
}

// NewBackoff builds a backoff registry with the exponential fallback bounds.
func NewBackoff(base, maximum time.Duration) *Backoff {
	if base <= 0 {
		base = DefaultBackoffBase
	}
	if maximum <= 0 {
		maximum = DefaultBackoffMax
	}
	if maximum < base {
		maximum = base
	}
	return &Backoff{
		now:            time.Now,
		Base:           base,
		Max:            maximum,
		credentials:    map[string]time.Time{},
		providers:      map[string]time.Time{},
		providerOwners: map[string]string{},
		attempts:       map[string]int{},
	}
}

// DefaultBackoffBase is the first wait a 429 without a Retry-After produces. It
// is exported so the config layer and this package cannot drift.
const DefaultBackoffBase = 5 * time.Minute

// DefaultBackoffMax matches the process default when a caller supplies a zero-value
// configuration, including focused handler tests that do not load environment settings.
const DefaultBackoffMax = 6 * time.Hour

// CredentialBlocked reports whether the credential is parked and until when.
func (b *Backoff) CredentialBlocked(authIndex string, now time.Time) (bool, time.Time) {
	if b == nil || strings.TrimSpace(authIndex) == "" {
		return false, time.Time{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.credentials[authIndex]
	if !ok || !now.Before(until) {
		return false, time.Time{}
	}
	return true, until
}

// ProviderBlocked reports whether the whole provider is parked and until when.
// It is used by the scheduler, which must not start another credential of a
// provider that just refused one.
func (b *Backoff) ProviderBlocked(provider string, now time.Time) (bool, time.Time) {
	if b == nil || strings.TrimSpace(provider) == "" {
		return false, time.Time{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.providers[provider]
	if !ok || !now.Before(until) {
		return false, time.Time{}
	}
	return true, until
}

// Blocked is the scheduler's combined check across the credential and provider.
func (b *Backoff) Blocked(provider, authIndex string, now time.Time) (bool, time.Time) {
	blocked, until := b.ProviderBlocked(provider, now)
	if blocked {
		return true, until
	}
	return b.CredentialBlocked(authIndex, now)
}

// Record parks a credential and its provider after a 429. When the upstream gave
// a Retry-After it is used; otherwise the wait doubles per consecutive refusal.
// The provider is parked with the same horizon so the scheduler stops probing
// its siblings, which is what a provider-level limit looks like from here.
func (b *Backoff) Record(provider, authIndex string, retryAfter time.Duration, hasRetryAfter bool, now time.Time) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	wait := retryAfter
	if !hasRetryAfter || wait <= 0 {
		attempts := b.attempts[authIndex]
		wait = b.Base << attempts
		if wait <= 0 || wait > b.Max {
			wait = b.Max
		}
		b.attempts[authIndex] = attempts + 1
	}
	if wait > b.Max {
		wait = b.Max
	}
	until := now.Add(wait)
	if strings.TrimSpace(authIndex) != "" {
		b.credentials[authIndex] = until
	}
	if strings.TrimSpace(provider) != "" {
		b.providers[provider] = until
		b.providerOwners[provider] = strings.TrimSpace(authIndex)
	}
}

// Clear forgets a successful credential's park. The provider park leaves only if
// that same credential created it; a sibling whose attempt was already in flight
// may succeed after another credential was refused, but it has no right to end
// that refusal.
func (b *Backoff) Clear(provider, authIndex string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if strings.TrimSpace(authIndex) != "" {
		delete(b.credentials, authIndex)
		delete(b.attempts, authIndex)
	}
	if strings.TrimSpace(provider) != "" {
		if b.providerOwners[provider] == strings.TrimSpace(authIndex) {
			delete(b.providers, provider)
			delete(b.providerOwners, provider)
		}
	}
}

// parseRetryAfter reads the Retry-After header an upstream returned through
// CPA's api-call. It accepts delay-seconds and an HTTP date, bounds the result to
// one day so a hostile value cannot park a credential indefinitely, and reports
// false when the header is absent or unreadable.
func parseRetryAfter(headers map[string][]string) (time.Duration, bool) {
	for name, values := range headers {
		if !strings.EqualFold(name, "Retry-After") {
			continue
		}
		for _, raw := range values {
			value := strings.TrimSpace(raw)
			if value == "" {
				continue
			}
			if seconds, err := strconv.Atoi(value); err == nil {
				if seconds < 0 {
					continue
				}
				return clampBackoff(time.Duration(seconds) * time.Second), true
			}
			if when, err := http.ParseTime(value); err == nil {
				return clampBackoff(time.Until(when)), true
			}
		}
	}
	return 0, false
}

func clampBackoff(wait time.Duration) time.Duration {
	if wait < 0 {
		return 0
	}
	if wait > 24*time.Hour {
		return 24 * time.Hour
	}
	return wait
}
