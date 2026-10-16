package ollama

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// Account is one configured Ollama account as collection sees it, without its
// secret.
type Account struct {
	ID              string
	Label           string
	Enabled         bool
	NextAttemptAtMS *int64
	AttemptCount    int
}

// StoredAccount adds the encrypted cookie the collector must decrypt. It is only
// ever held inside the collector; it is never serialized into a DTO or a log.
type StoredAccount struct {
	Account
	Ciphertext []byte
	Nonce      []byte
}

// CollectionResult is what the collector writes back for one account. It carries a
// safe snapshot (or nothing) and a scrubbed error message.
type CollectionResult struct {
	ID              string
	Status          string
	ObservedAtMS    int64
	IsSuccess       bool
	SnapshotJSON    string
	Error           string
	ErrorAtMS       int64
	NextAttemptAtMS *int64
	AttemptCount    int
}

// Collector scrapes Ollama quota for the enabled accounts on a fixed cadence.
//
// Its collaborators are functions rather than concrete services so the module
// stays free of the HTTP and repository layers and can be tested with an injected
// clock, fetch and store. The integration owner supplies the adapters in
// BuildOllamaService.
type Collector struct {
	// Accounts returns the collection-eligible accounts with their encrypted
	// cookies. The store is asked for enabled accounts only.
	Accounts func(ctx context.Context) ([]StoredAccount, error)
	// Save persists one account's collection result.
	Save func(ctx context.Context, result CollectionResult) error
	// Decrypt decrypts a stored cookie.
	Decrypt func(ciphertext, nonce []byte) ([]byte, error)
	// Fetch performs one scrape. It is the client's Fetch in production.
	Fetch func(ctx context.Context, account Account, cookie string) (Snapshot, error)
	// Observations, when set, records windows that carry a known duration into the
	// shared observation timeline. Ollama publishes no window duration today, so it
	// is not called by the first version; it is a wiring seam for when it does.
	Observations func(ctx context.Context, value *quota.NormalizedQuota) error
	// Audit records collection outcomes. Secrets must never be passed to it.
	Audit func(ctx context.Context, action, result, targetID string, details map[string]any)

	Logger *slog.Logger
	// Now is injectable so tests do not sleep.
	Now func() time.Time
	// Interval is the cadence between collection passes.
	Interval time.Duration
	// BackoffBase and BackoffMax shape the retry schedule after a timeout or an
	// upstream rate limit.
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// After waits between passes; tests inject a channel-driven wait.
	After func(time.Duration) <-chan time.Time
}

// RunOnce collects every due account once and returns how many were attempted.
// Disabled accounts are skipped entirely, and a persisted backoff window is
// honoured.
func (c *Collector) RunOnce(ctx context.Context) (int, error) {
	if c == nil || c.Accounts == nil || c.Fetch == nil {
		return 0, nil
	}
	accounts, err := c.Accounts(ctx)
	if err != nil {
		return 0, err
	}
	now := c.now()
	attempted := 0
	for _, account := range accounts {
		if !account.Enabled {
			continue
		}
		if account.NextAttemptAtMS != nil && *account.NextAttemptAtMS > now.UnixMilli() {
			continue
		}
		if err := ctx.Err(); err != nil {
			return attempted, err
		}
		c.collectAccount(ctx, account, now)
		attempted++
	}
	return attempted, nil
}

// Run collects once immediately, so a restart resumes at once, then on the cadence
// until the context is cancelled. It never sleeps in a test: the interval and the
// wait are both injected.
func (c *Collector) Run(ctx context.Context) error {
	if c == nil {
		return nil
	}
	interval := c.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	after := c.After
	if after == nil {
		after = time.After
	}
	c.logger().Info("ollama collection started", "interval", interval.String())
	if _, err := c.RunOnce(ctx); err != nil && ctx.Err() == nil {
		c.logger().Warn("ollama collection pass failed", "error", err)
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-after(interval):
			if _, err := c.RunOnce(ctx); err != nil && ctx.Err() == nil {
				c.logger().Warn("ollama collection pass failed", "error", err)
			}
		}
	}
}

// collectAccount decrypts one account's cookie, scrapes it and persists the
// outcome. The plaintext cookie is cleared before returning.
func (c *Collector) collectAccount(ctx context.Context, account StoredAccount, now time.Time) {
	cookie, err := c.decrypt(account.Ciphertext, account.Nonce)
	if err != nil {
		c.persist(ctx, CollectionResult{
			ID:           account.ID,
			Status:       StatusAuthInvalid,
			ObservedAtMS: now.UnixMilli(),
			Error:        "stored Ollama cookie could not be decrypted; re-enter it",
			ErrorAtMS:    now.UnixMilli(),
		})
		return
	}
	defer clearBytes(cookie)

	snapshot, err := c.fetch(ctx, Account{ID: account.ID, Label: account.Label, Enabled: account.Enabled}, string(cookie))
	if err != nil {
		// A transport failure carries no Retry-After; the backoff grows by attempt.
		c.persist(ctx, CollectionResult{
			ID:              account.ID,
			Status:          StatusTransportError,
			ObservedAtMS:    now.UnixMilli(),
			Error:           scrubError(err),
			ErrorAtMS:       now.UnixMilli(),
			NextAttemptAtMS: epochMSPtr(c.nextAttempt(now, account.AttemptCount, snapshot.RetryAfter, snapshot.HasRetryAfter)),
			AttemptCount:    account.AttemptCount + 1,
		})
		c.audit(ctx, "ollama.collect.transport_error", "failure", account.ID)
		return
	}

	if snapshot.IsSuccess() {
		value := snapshot.ToQuota(account.Label)
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			encoded = nil
		}
		c.persist(ctx, CollectionResult{
			ID:           account.ID,
			Status:       StatusOK,
			ObservedAtMS: snapshot.ObservedAt.UnixMilli(),
			IsSuccess:    true,
			SnapshotJSON: string(encoded),
		})
		if c.Observations != nil && hasKnownDurations(value) {
			if err := c.Observations(ctx, value); err != nil {
				c.logger().Warn("ollama quota observation write failed", "account", account.ID, "error", err)
			}
		}
		c.audit(ctx, "ollama.collect.success", "success", account.ID)
		return
	}

	status := StatusForFailure(snapshot.Failure)
	result := CollectionResult{
		ID:           account.ID,
		Status:       status,
		ObservedAtMS: snapshot.ObservedAt.UnixMilli(),
		Error:        snapshot.Err,
		ErrorAtMS:    snapshot.ObservedAt.UnixMilli(),
	}
	// Auth is not retried with a backoff: only the operator can supply a new cookie.
	// Every other failure backs off so a broken page or an upstream limit is not
	// hammered.
	if snapshot.Failure != FailureAuth {
		result.NextAttemptAtMS = epochMSPtr(c.nextAttempt(now, account.AttemptCount, snapshot.RetryAfter, snapshot.HasRetryAfter))
		result.AttemptCount = account.AttemptCount + 1
	}
	c.persist(ctx, result)
	c.audit(ctx, "ollama.collect."+status, "failure", account.ID)
}

// nextAttempt computes the next allowed attempt. A Retry-After the upstream gave is
// honoured; otherwise the wait doubles from BackoffBase, bounded by BackoffMax.
func (c *Collector) nextAttempt(now time.Time, attempts int, retryAfter time.Duration, hasRetryAfter bool) time.Time {
	if hasRetryAfter && retryAfter > 0 {
		return now.Add(retryAfter)
	}
	base := c.BackoffBase
	if base <= 0 {
		base = DefaultBackoffBase
	}
	maximum := c.BackoffMax
	if maximum < base {
		maximum = base
	}
	wait := base
	for count := 0; count < attempts && count < 32; count++ {
		wait *= 2
		if wait >= maximum {
			wait = maximum
			break
		}
	}
	return now.Add(wait)
}

func (c *Collector) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Collector) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c *Collector) decrypt(ciphertext, nonce []byte) ([]byte, error) {
	if c.Decrypt == nil {
		return nil, errNoDecryptor
	}
	return c.Decrypt(ciphertext, nonce)
}

func (c *Collector) fetch(ctx context.Context, account Account, cookie string) (Snapshot, error) {
	return c.Fetch(ctx, account, cookie)
}

func (c *Collector) persist(ctx context.Context, result CollectionResult) {
	if c.Save == nil {
		return
	}
	if err := c.Save(ctx, result); err != nil {
		c.logger().Warn("ollama collection result could not be saved", "account", result.ID, "error", err)
	}
}

func (c *Collector) audit(ctx context.Context, action, result, targetID string) {
	if c.Audit == nil {
		return
	}
	c.Audit(ctx, action, result, targetID, nil)
}

// hasKnownDurations reports whether any window states a real duration, which is
// what qualifies it for the shared observation timeline. Ollama states none today,
// so a reading is persisted only as this module's last snapshot.
func hasKnownDurations(value *quota.NormalizedQuota) bool {
	if value == nil {
		return false
	}
	for _, window := range value.Windows {
		if window.PeriodHours != nil && *window.PeriodHours > 0 {
			return true
		}
	}
	return false
}

// scrubError bounds a transport error message. The client never embeds the cookie
// in an error, so this only guards against an unbounded transport string.
func scrubError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	const limit = 200
	if len(message) > limit {
		message = message[:limit]
	}
	return message
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func epochMSPtr(value time.Time) *int64 {
	millis := value.UnixMilli()
	return &millis
}

type missingDecryptorError struct{}

func (missingDecryptorError) Error() string { return "ollama: no cookie decryptor configured" }

var errNoDecryptor = missingDecryptorError{}
