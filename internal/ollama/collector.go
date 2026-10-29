package ollama

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
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

// StoredAccount adds the encrypted credentials the collector must decrypt. It is
// only ever held inside the collector; it is never serialized into a DTO or a log.
//
// The API key is optional and preferred when present; the session cookie stays
// mandatory because it is the fallback for every account, including ones that
// predate the key.
type StoredAccount struct {
	Account
	Ciphertext []byte
	Nonce      []byte
	// APIKeyCiphertext and APIKeyNonce are nil when the account has no key. They are
	// never written anywhere except through Decrypt.
	APIKeyCiphertext []byte
	APIKeyNonce      []byte
	// CookieUpdatedAtMS and APIKeyUpdatedAtMS are the versions of the stored
	// credentials. A collection result is written back only while the stored
	// version still matches, so a scrape that read replaced material can never
	// overwrite the reading of the newer one.
	CookieUpdatedAtMS int64
	APIKeyUpdatedAtMS int64
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
	// CookieUpdatedAtMS and APIKeyUpdatedAtMS, when set, guard the write: a result
	// whose credential version no longer matches the stored one is dropped rather
	// than applied. Either is nil for callers that do not track that credential.
	CookieUpdatedAtMS *int64
	APIKeyUpdatedAtMS *int64
	// Auto marks a result the periodic pass or a create/update trigger produced. An
	// automatic result never writes to a disabled account; a manual refresh of a
	// paused account passes Auto false and still lands.
	Auto bool
}

// RefreshResult is the outcome of one per-account refresh entry point.
type RefreshResult struct {
	// Refreshed reports whether a scrape actually ran for this call. It is false
	// when the request was suppressed, so a caller never mistakes "nothing
	// happened" for "a fresh reading".
	Refreshed bool
	// Suppressed reports that the scrape was deliberately skipped - a persisted
	// backoff or the manual minimum gap still applied.
	Suppressed bool
	// RetryAtMS is the earliest instant the account may be refreshed again, set
	// when Suppressed.
	RetryAtMS *int64
}

// refreshCall is one in-flight collection for one account. Every entry point - the
// periodic pass, a create/update trigger and a manual refresh - joins the call that is
// already running instead of starting a second one, so the same credential is never
// fetched twice at once.
type refreshCall struct {
	done chan struct{}
	// result is written before done is closed; readers only observe it after
	// receiving from done.
	result RefreshResult
	// cookieVersion and apiKeyVersion are the stored credential versions the
	// running scrape read.
	cookieVersion int64
	apiKeyVersion int64
	// auto records whether the running scrape is automatic, so a manual waiter can
	// tell that a dropped automatic result (a paused account) is not its own reading.
	auto bool
}

// Collector scrapes Ollama quota for the enabled accounts on a fixed cadence.
//
// Its collaborators are functions rather than concrete services so the module
// stays free of the HTTP and repository layers and can be tested with an injected
// clock, fetch and store. The integration owner supplies the adapters in
// BuildOllamaService.
//
// Besides the periodic pass it is also the one per-account refresh entry point
// (`RefreshAccount`) and the trigger queue (`Trigger`): the console's manual
// button, a newly created account and a changed credential all go through the same
// lock, concurrency cap and backoff, so no path can bypass another's rate limit.
type Collector struct {
	// Accounts returns the collection-eligible accounts with their encrypted
	// credentials. The store is asked for enabled accounts only.
	Accounts func(ctx context.Context) ([]StoredAccount, error)
	// AccountByID loads one account's newest stored secret, enabled or not. Manual
	// and triggered refreshes read through it so a queued scrape always uses the
	// latest credential rather than one captured before an update. Nil disables the
	// per-account entry points.
	AccountByID func(ctx context.Context, id string) (StoredAccount, bool, error)
	// Save persists one account's collection result.
	Save func(ctx context.Context, result CollectionResult) error
	// Decrypt decrypts one stored credential.
	Decrypt func(ciphertext, nonce []byte) ([]byte, error)
	// Fetch performs one settings-page fetch with the session cookie. It is Fetch in
	// production and the fallback path whenever a stored API key fails.
	Fetch func(ctx context.Context, account Account, cookie string) (Snapshot, error)
	// FetchBalance performs one API-key balance scrape. It is the client's
	// FetchBalance in production. Nil disables the preferred path, so an account
	// with a stored key falls back to the cookie instead of failing outright.
	FetchBalance func(ctx context.Context, account Account, apiKey string) (Snapshot, error)
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
	// ManualMinGap bounds how often the console may force a refresh of one account.
	// Zero means DefaultManualMinGap; a negative value disables the gap.
	ManualMinGap time.Duration
	// Concurrency caps how many scrapes run at once across every entry point. Zero
	// means DefaultConcurrency.
	Concurrency int
	// RefreshTimeout bounds one manual refresh end to end: waiting for a free
	// concurrency permit, the scrape and the reload. Zero means
	// DefaultRefreshTimeout. It is derived from the service context, not the request,
	// so a caller disconnect never truncates the work and the caller never waits
	// longer than this.
	RefreshTimeout time.Duration

	mu          sync.Mutex
	calls       map[string]*refreshCall
	lastManual  map[string]time.Time
	pending     map[string]struct{}
	wake        chan struct{}
	baseContext context.Context

	workerOnce sync.Once
	semOnce    sync.Once
	sem        chan struct{}
}

// RunOnce collects every due account once and returns how many were attempted.
// Disabled accounts are skipped entirely, and a persisted backoff window is
// honoured. An account already being scraped by a trigger or a manual refresh is
// joined rather than scraped twice.
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
		c.refreshOwned(ctx, account, true)
		attempted++
	}
	return attempted, nil
}

// Start installs the process lifetime context. The application calls it before its
// listener accepts a request, so no scrape ever runs on a request context; Run also
// calls it so a test or a caller that only starts the loop is still correct. Calling
// it twice with the same context is harmless.
func (c *Collector) Start(ctx context.Context) {
	if c == nil || ctx == nil {
		return
	}
	c.setBaseContext(ctx)
}

// Run collects once immediately, so a restart resumes at once, then on the cadence
// until the context is cancelled. It also starts the worker that drains the
// create/update trigger queue. It never sleeps in a test: the interval and the
// wait are both injected.
func (c *Collector) Run(ctx context.Context) error {
	if c == nil {
		return nil
	}
	c.Start(ctx)
	c.startWorker(ctx)
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

// RefreshAccount scrapes one account on demand. It is the console's manual
// refresh: it works for a paused account (a one-off reading does not change
// `enabled`) and it coalesces onto a scrape that is already running rather than
// starting a second one. A persisted 429 backoff or the manual minimum gap
// suppresses the scrape without touching the upstream and reports when the account
// is free again.
//
// The whole call - waiting for a concurrency permit, the scrape and the reload - is
// bounded by RefreshTimeout and is rooted in the service context, never the caller's:
// a client that disconnects does not truncate a scrape that another path may be
// waiting on, and the caller is never held longer than the timeout.
// ErrServiceNotStarted is returned before Start, and ErrAccountNotFound when the
// account does not exist.
func (c *Collector) RefreshAccount(ctx context.Context, id string) (RefreshResult, error) {
	if c == nil || c.AccountByID == nil {
		return RefreshResult{}, ErrAccountNotFound
	}
	serviceCtx, started := c.serviceContext()
	if !started {
		return RefreshResult{}, ErrServiceNotStarted
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return RefreshResult{}, ErrAccountNotFound
	}
	account, found, err := c.AccountByID(serviceCtx, id)
	if err != nil {
		return RefreshResult{}, err
	}
	if !found {
		return RefreshResult{}, ErrAccountNotFound
	}
	now := c.now()
	if until, blocked := backoffUntil(account, now); blocked {
		return RefreshResult{Suppressed: true, RetryAtMS: &until}, nil
	}
	if retry, throttled := c.manualThrottled(id, now); throttled {
		return RefreshResult{Suppressed: true, RetryAtMS: &retry}, nil
	}
	c.recordManual(id, now)

	// One deadline for the whole call, derived from the service context so a request
	// cancellation cannot shorten it. Waiting for a permit and scraping share this
	// single budget, so a saturated service does not stack a second timeout on top of
	// the first.
	opCtx, cancel := context.WithTimeout(serviceCtx, c.refreshTimeout())
	defer cancel()

	var last RefreshResult
	call, owner := c.beginRefresh(id)
	if owner {
		c.performRefresh(opCtx, account, false, call)
	} else {
		select {
		case <-call.done:
		case <-opCtx.Done():
			return RefreshResult{Suppressed: true}, nil
		case <-ctx.Done():
			return RefreshResult{}, ctx.Err()
		}
	}
	last = call.result
	if last.Suppressed {
		// No scrape ran (backoff, deadline or a paused automatic call); there is no
		// stale result to report, so answer as suppressed rather than success.
		return last, nil
	}
	// A credential change under the collection makes the store refuse a result that
	// read the old version, and an automatic scrape on an account that is now paused is refused
	// too. In either case the reading was not persisted, so report the call as
	// incomplete rather than returning an old success. There is no retry loop: the
	// next click or the next cadence picks the new state up.
	if reloaded, stillThere, reloadErr := c.AccountByID(opCtx, id); reloadErr == nil && stillThere {
		if reloaded.CookieUpdatedAtMS != call.cookieVersion ||
			reloaded.APIKeyUpdatedAtMS != call.apiKeyVersion ||
			(call.auto && !reloaded.Enabled) {
			return RefreshResult{}, ErrRefreshSuperseded
		}
	}
	return last, nil
}

// Trigger queues one account for a scrape soon, without blocking the caller and
// without reading a request context. It is what a create, a credential update and a
// resume call: the actual scrape happens on the service's long-lived context and
// disappears when the process shuts down. The queue is a bounded set keyed by id, so
// repeated triggers collapse into one pending marker and do not become one scrape per
// change.
func (c *Collector) Trigger(id string) {
	if c == nil {
		return
	}
	c.enqueue(id)
}

// enqueue records one pending account and wakes the worker. It is the one place a
// marker is set, so a change that arrives while the worker is busy (including one
// noticed after a collection finished with a stale credential) survives to the next round.
func (c *Collector) enqueue(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	c.mu.Lock()
	if c.pending == nil {
		c.pending = make(map[string]struct{})
	}
	if c.wake == nil {
		c.wake = make(chan struct{}, 1)
	}
	c.pending[id] = struct{}{}
	wake := c.wake
	c.mu.Unlock()
	select {
	case wake <- struct{}{}:
	default:
	}
}

// Refreshing reports whether a scrape for the account is in flight. The console
// shows it as a spinner instead of offering a second refresh.
func (c *Collector) Refreshing(id string) bool {
	if c == nil {
		return false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.calls[id]
	return ok
}

// ManualRetryAt reports the instant the manual minimum gap next allows a refresh of
// the account. ok is false when the account has not been refreshed manually yet or
// the gap is disabled.
func (c *Collector) ManualRetryAt(id string) (time.Time, bool) {
	if c == nil {
		return time.Time{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return time.Time{}, false
	}
	gap := c.manualMinGap()
	if gap <= 0 {
		return time.Time{}, false
	}
	c.mu.Lock()
	last, ok := c.lastManual[id]
	c.mu.Unlock()
	if !ok {
		return time.Time{}, false
	}
	return last.Add(gap), true
}

// refreshOwned joins or starts one scrape for the periodic pass. A second caller
// for the same account finds the in-flight call and returns.
func (c *Collector) refreshOwned(ctx context.Context, seed StoredAccount, auto bool) {
	call, owner := c.beginRefresh(seed.ID)
	if !owner {
		return
	}
	c.performRefresh(ctx, seed, auto, call)
}

// performRefresh reloads the newest secret, respects the automatic/disabled gate,
// waits for a global concurrency permit bounded by ctx and then scrapes, publishing
// the result to every waiter. If ctx ends before a permit is free - a saturated
// service or a shutdown - no scrape runs and the call is finished suppressed rather
// than queued behind the others.
func (c *Collector) performRefresh(ctx context.Context, seed StoredAccount, auto bool, call *refreshCall) {
	defer c.finishRefresh(seed.ID, call)
	account := seed
	if c.AccountByID != nil {
		loaded, found, err := c.AccountByID(ctx, seed.ID)
		if err != nil || !found {
			call.result = RefreshResult{Suppressed: true}
			return
		}
		account = loaded
	}
	// Automatic collection is only for an enabled account; a manual refresh of a
	// paused account is deliberate and stays allowed.
	if auto && !account.Enabled {
		call.result = RefreshResult{Suppressed: true}
		return
	}
	call.auto = auto
	call.cookieVersion = account.CookieUpdatedAtMS
	call.apiKeyVersion = account.APIKeyUpdatedAtMS
	if !c.acquire(ctx) {
		call.result = RefreshResult{Suppressed: true}
		return
	}
	c.collectAccount(ctx, account, auto, c.now())
	c.release()
	call.result = RefreshResult{Refreshed: true}
}

// refreshTriggered runs one scrape round for an enabled, non-backed-off account. When
// the round coalesced onto a collection that read a credential the operator has since replaced,
// the trigger's change was not collected; the account is left as one pending marker so
// the worker takes the newest configuration in the next round, instead of the change
// waiting for the cadence. It never chases inside the call, so a repeated change still
// collapses into the single marker.
func (c *Collector) refreshTriggered(ctx context.Context, id string) {
	if c.AccountByID == nil {
		return
	}
	account, found, err := c.AccountByID(ctx, id)
	if err != nil || !found || !account.Enabled {
		return
	}
	if _, blocked := backoffUntil(account, c.now()); blocked {
		return
	}
	call, owner := c.beginRefresh(id)
	if owner {
		c.performRefresh(ctx, account, true, call)
	} else {
		// A scrape for this account is already running; wait for it so the result can be
		// compared with the credential the account now has.
		select {
		case <-call.done:
		case <-ctx.Done():
			return
		}
	}
	if ctx.Err() != nil || call.result.Suppressed {
		return
	}
	// A 429, a pause or a shutdown all stop the follow-up: only a live credential change
	// re-queues. The reload is the account's current state, never a generation.
	current, found, err := c.AccountByID(ctx, id)
	if err != nil || !found || !current.Enabled {
		return
	}
	if _, blocked := backoffUntil(current, c.now()); blocked {
		return
	}
	if call.cookieVersion != current.CookieUpdatedAtMS || call.apiKeyVersion != current.APIKeyUpdatedAtMS {
		c.enqueue(id)
	}
}

func (c *Collector) beginRefresh(id string) (*refreshCall, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.calls == nil {
		c.calls = make(map[string]*refreshCall)
	}
	if existing, ok := c.calls[id]; ok {
		return existing, false
	}
	call := &refreshCall{done: make(chan struct{})}
	c.calls[id] = call
	return call, true
}

func (c *Collector) finishRefresh(id string, call *refreshCall) {
	c.mu.Lock()
	if c.calls[id] == call {
		delete(c.calls, id)
	}
	c.mu.Unlock()
	close(call.done)
}

// acquire waits for one global concurrency permit. It returns false when ctx ends
// first, so a caller with a deadline is not queued indefinitely behind a saturated
// service and a shutdown releases a blocked worker.
func (c *Collector) acquire(ctx context.Context) bool {
	c.semOnce.Do(func() {
		limit := c.Concurrency
		if limit <= 0 {
			limit = DefaultConcurrency
		}
		c.sem = make(chan struct{}, limit)
	})
	select {
	case c.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Collector) release() { <-c.sem }

func (c *Collector) setBaseContext(ctx context.Context) {
	c.mu.Lock()
	c.baseContext = ctx
	c.mu.Unlock()
}

// serviceContext returns the process lifetime context. It is the only context a
// scrape may run on: it never falls back to a request context, so collection and
// persistence outlive the request that asked for them. The second result is false
// until Start has installed it.
func (c *Collector) serviceContext() (context.Context, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.baseContext == nil || c.baseContext.Err() != nil {
		return nil, false
	}
	return c.baseContext, true
}

func (c *Collector) refreshTimeout() time.Duration {
	if c.RefreshTimeout > 0 {
		return c.RefreshTimeout
	}
	return DefaultRefreshTimeout
}

func (c *Collector) startWorker(ctx context.Context) {
	c.mu.Lock()
	if c.wake == nil {
		c.wake = make(chan struct{}, 1)
	}
	c.mu.Unlock()
	c.workerOnce.Do(func() { go c.worker(ctx) })
}

func (c *Collector) worker(ctx context.Context) {
	for {
		c.mu.Lock()
		wake := c.wake
		c.mu.Unlock()
		if wake == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
			for _, id := range c.drainPending() {
				if ctx.Err() != nil {
					return
				}
				c.refreshTriggered(ctx, id)
			}
		}
	}
}

func (c *Collector) drainPending() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) == 0 {
		return nil
	}
	ids := make([]string, 0, len(c.pending))
	for id := range c.pending {
		ids = append(ids, id)
	}
	c.pending = make(map[string]struct{})
	return ids
}

func (c *Collector) manualThrottled(id string, now time.Time) (int64, bool) {
	gap := c.manualMinGap()
	if gap <= 0 {
		return 0, false
	}
	c.mu.Lock()
	last, ok := c.lastManual[id]
	c.mu.Unlock()
	if !ok {
		return 0, false
	}
	next := last.Add(gap)
	if now.Before(next) {
		return next.UnixMilli(), true
	}
	return 0, false
}

func (c *Collector) recordManual(id string, now time.Time) {
	if c.manualMinGap() <= 0 {
		return
	}
	c.mu.Lock()
	if c.lastManual == nil {
		c.lastManual = make(map[string]time.Time)
	}
	c.lastManual[id] = now
	c.mu.Unlock()
}

func (c *Collector) manualMinGap() time.Duration {
	if c.ManualMinGap != 0 {
		return c.ManualMinGap
	}
	return DefaultManualMinGap
}

// backoffUntil reports a live persisted backoff, which every entry point honours.
func backoffUntil(account StoredAccount, now time.Time) (int64, bool) {
	if account.NextAttemptAtMS != nil && *account.NextAttemptAtMS > now.UnixMilli() {
		return *account.NextAttemptAtMS, true
	}
	return 0, false
}

// collectAccount resolves one account's preferred credential, scrapes it and
// persists the outcome. A stored API key uses the balance endpoint; the session
// cookie is the fallback when the key is absent or fails. Plaintext credentials are
// cleared before returning and no error text ever carries them. auto is recorded on
// the result so the store can refuse an automatic write to an account that has been
// disabled since the scrape began; a manual refresh passes false.
func (c *Collector) collectAccount(ctx context.Context, account StoredAccount, auto bool, now time.Time) {
	base := Account{ID: account.ID, Label: account.Label, Enabled: account.Enabled}
	cookieVersion := versionPtr(account.CookieUpdatedAtMS)
	apiKeyVersion := versionPtr(account.APIKeyUpdatedAtMS)

	// The key path is tried first whenever the account has one. Its failure is kept
	// only as context for the fallback's own verdict, never merged into a success.
	keyFailure := ""
	if len(account.APIKeyCiphertext) > 0 {
		apiKey, err := c.decrypt(account.APIKeyCiphertext, account.APIKeyNonce)
		if err != nil {
			keyFailure = "stored Ollama API key could not be decrypted; re-enter it"
		} else {
			snapshot, fetchErr := c.fetchBalance(ctx, base, string(apiKey))
			clearBytes(apiKey)
			if fetchErr == nil && snapshot.IsSuccess() {
				c.persistSuccess(ctx, account, snapshot, nil, apiKeyVersion, auto)
				return
			}
			keyFailure = pathFailure(snapshot, fetchErr, "the API key balance path produced no reading")
		}
	}

	cookie, err := c.decrypt(account.Ciphertext, account.Nonce)
	if err != nil {
		c.persist(ctx, CollectionResult{
			ID:                account.ID,
			Status:            StatusAuthInvalid,
			ObservedAtMS:      now.UnixMilli(),
			Error:             combinePathErrors(keyFailure, "stored Ollama cookie could not be decrypted; re-enter it"),
			ErrorAtMS:         now.UnixMilli(),
			CookieUpdatedAtMS: cookieVersion,
			Auto:              auto,
		})
		return
	}
	defer clearBytes(cookie)

	snapshot, err := c.fetch(ctx, base, string(cookie))
	if err != nil {
		// A transport failure carries no Retry-After; the backoff grows by attempt.
		c.persist(ctx, CollectionResult{
			ID:                account.ID,
			Status:            StatusTransportError,
			ObservedAtMS:      now.UnixMilli(),
			Error:             combinePathErrors(keyFailure, scrubError(err)),
			ErrorAtMS:         now.UnixMilli(),
			NextAttemptAtMS:   epochMSPtr(c.nextAttempt(now, account.AttemptCount, snapshot.RetryAfter, snapshot.HasRetryAfter)),
			AttemptCount:      account.AttemptCount + 1,
			CookieUpdatedAtMS: cookieVersion,
			Auto:              auto,
		})
		c.audit(ctx, "ollama.collect.transport_error", "failure", account.ID)
		return
	}

	if snapshot.IsSuccess() {
		c.persistSuccess(ctx, account, snapshot, cookieVersion, nil, auto)
		return
	}

	status := StatusForFailure(snapshot.Failure)
	result := CollectionResult{
		ID:                account.ID,
		Status:            status,
		ObservedAtMS:      snapshot.ObservedAt.UnixMilli(),
		Error:             combinePathErrors(keyFailure, snapshot.Err),
		ErrorAtMS:         snapshot.ObservedAt.UnixMilli(),
		CookieUpdatedAtMS: cookieVersion,
		Auto:              auto,
	}
	// Auth is not retried with a backoff: only the operator can supply a new key or
	// cookie. Every other failure backs off so a broken page or an upstream limit is
	// not hammered.
	if snapshot.Failure != FailureAuth {
		result.NextAttemptAtMS = epochMSPtr(c.nextAttempt(now, account.AttemptCount, snapshot.RetryAfter, snapshot.HasRetryAfter))
		result.AttemptCount = account.AttemptCount + 1
	}
	c.persist(ctx, result)
	c.audit(ctx, "ollama.collect."+status, "failure", account.ID)
}

// persistSuccess stores one safe snapshot. Exactly one credential version is set:
// APIKeyUpdatedAtMS when the balance endpoint produced the reading, CookieUpdatedAtMS
// when the settings-page fallback did.
func (c *Collector) persistSuccess(ctx context.Context, account StoredAccount, snapshot Snapshot, cookieVersion, apiKeyVersion *int64, auto bool) {
	value := snapshot.ToQuota(account.Label)
	encoded, marshalErr := json.Marshal(value)
	if marshalErr != nil {
		encoded = nil
	}
	c.persist(ctx, CollectionResult{
		ID:                account.ID,
		Status:            StatusOK,
		ObservedAtMS:      snapshot.ObservedAt.UnixMilli(),
		IsSuccess:         true,
		SnapshotJSON:      string(encoded),
		CookieUpdatedAtMS: cookieVersion,
		APIKeyUpdatedAtMS: apiKeyVersion,
		Auto:              auto,
	})
	if c.Observations != nil && hasKnownDurations(value) {
		if err := c.Observations(ctx, value); err != nil {
			c.logger().Warn("ollama quota observation write failed", "account", account.ID, "error", err)
		}
	}
	c.audit(ctx, "ollama.collect.success", "success", account.ID)
}

// pathFailure turns one fetch outcome into the short reason recorded for it.
func pathFailure(snapshot Snapshot, err error, empty string) string {
	if err != nil {
		return scrubError(err)
	}
	if message := strings.TrimSpace(snapshot.Err); message != "" {
		return message
	}
	return empty
}

// combinePathErrors names the path that produced the final verdict and, when the
// preferred key path was tried first, why it did not decide the outcome itself. It
// never receives or returns credential material.
func combinePathErrors(keyFailure, cookieFailure string) string {
	cookieFailure = strings.TrimSpace(cookieFailure)
	if cookieFailure == "" {
		cookieFailure = "no reading was produced"
	}
	if strings.TrimSpace(keyFailure) == "" {
		return "Ollama session cookie settings path failed: " + cookieFailure
	}
	return "Ollama API key balance path failed (" + strings.TrimSpace(keyFailure) +
		"); session cookie settings path failed (" + cookieFailure + ")"
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

func (c *Collector) fetchBalance(ctx context.Context, account Account, apiKey string) (Snapshot, error) {
	if c.FetchBalance == nil {
		return Snapshot{}, errNoBalanceFetcher
	}
	return c.FetchBalance(ctx, account, apiKey)
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

// scrubError bounds a transport error message. The client never embeds a credential
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

// versionPtr turns a stored version into an optional guard: zero means the caller
// does not track one, so the write is unguarded.
func versionPtr(version int64) *int64 {
	if version <= 0 {
		return nil
	}
	return &version
}

type missingDecryptorError struct{}

func (missingDecryptorError) Error() string { return "ollama: no credential decryptor configured" }

var errNoDecryptor = missingDecryptorError{}

type missingBalanceFetcherError struct{}

func (missingBalanceFetcherError) Error() string {
	return "ollama: no API-key balance fetcher configured"
}

var errNoBalanceFetcher = missingBalanceFetcherError{}
