package ollama

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// newRefreshCollector builds a collector whose per-account entry points read a
// fixed map of stored accounts, so a test can drive the manual and trigger paths
// without a repository or an injected wait. The service context is installed (as
// the application does before it serves) so a manual refresh is not refused.
func newRefreshCollector(clock *time.Time, seeds map[string]StoredAccount, fetch func(ctx context.Context, account Account, cookie string) (Snapshot, error)) *Collector {
	collector := &Collector{
		AccountByID: func(_ context.Context, id string) (StoredAccount, bool, error) {
			seed, ok := seeds[id]
			return seed, ok, nil
		},
		Decrypt:     func([]byte, []byte) ([]byte, error) { return []byte("decrypted-cookie"), nil },
		Fetch:       fetch,
		Now:         func() time.Time { return *clock },
		BackoffBase: time.Minute,
		BackoffMax:  time.Hour,
	}
	collector.Start(context.Background())
	return collector
}

func successSnapshot(id string, at time.Time) Snapshot {
	return Snapshot{
		AccountID:  id,
		ObservedAt: at,
		Windows:    []Window{{Name: "5h", Label: "Ollama 5-hour", UsedPercent: floatPtr(30)}},
	}
}

func enabledSeedWithVersion(id string, version int64) StoredAccount {
	return StoredAccount{
		Account:           Account{ID: id, Enabled: true},
		Ciphertext:        []byte("cipher"),
		Nonce:             []byte("nonce"),
		CookieUpdatedAtMS: version,
	}
}

// TestTriggerScrapesWithoutTheIntervalElapsing proves a create/update trigger runs
// on the service's own context, not on a request context and not after the fifteen
// minute cadence: the clock never moves.
func TestTriggerScrapesWithoutTheIntervalElapsing(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var fetches atomic.Int32
	scraped := make(chan string, 1)
	collector := newRefreshCollector(&clock, map[string]StoredAccount{"acct-1": enabledSeedWithVersion("acct-1", 7)}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		fetches.Add(1)
		select {
		case scraped <- account.ID:
		default:
		}
		return successSnapshot(account.ID, clock), nil
	})
	collector.Accounts = func(context.Context) ([]StoredAccount, error) { return nil, nil }
	collector.After = func(time.Duration) <-chan time.Time { return make(chan time.Time) }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = collector.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("Run did not stop on cancellation")
		}
	})

	collector.Trigger("acct-1")
	select {
	case id := <-scraped:
		if id != "acct-1" {
			t.Fatalf("scraped %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a create trigger did not scrape before the interval elapsed")
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetches = %d, want 1", fetches.Load())
	}
}

// TestTriggerHonoursBackoffAndDisabled proves the automatic trigger never bypasses a
// persisted 429/backoff and never scrapes a disabled account.
func TestTriggerHonoursBackoffAndDisabled(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	future := clock.Add(10 * time.Minute).UnixMilli()
	blocked := enabledSeedWithVersion("blocked", 3)
	blocked.NextAttemptAtMS = &future
	disabled := enabledSeedWithVersion("disabled", 4)
	disabled.Enabled = false

	var fetches atomic.Int32
	collector := newRefreshCollector(&clock, map[string]StoredAccount{
		"blocked":  blocked,
		"disabled": disabled,
	}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		fetches.Add(1)
		return successSnapshot(account.ID, clock), nil
	})

	// refreshTriggered is the worker's per-id body; calling it directly keeps the two
	// negative cases deterministic instead of racing a goroutine.
	collector.refreshTriggered(context.Background(), "blocked")
	collector.refreshTriggered(context.Background(), "disabled")
	if fetches.Load() != 0 {
		t.Fatalf("a blocked or disabled account was scraped: fetches = %d", fetches.Load())
	}
}

// TestRefreshAccountManualGapSuppresses proves a held-down button cannot hammer the
// upstream: a second manual refresh inside the gap is suppressed with the instant it
// is allowed again, and nothing is scraped.
func TestRefreshAccountManualGapSuppresses(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var fetches atomic.Int32
	collector := newRefreshCollector(&clock, map[string]StoredAccount{"acct-1": enabledSeedWithVersion("acct-1", 1)}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		fetches.Add(1)
		return successSnapshot(account.ID, clock), nil
	})
	collector.ManualMinGap = 30 * time.Second

	first, err := collector.RefreshAccount(context.Background(), "acct-1")
	if err != nil || !first.Refreshed || first.Suppressed {
		t.Fatalf("first refresh = %+v %v", first, err)
	}
	second, err := collector.RefreshAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if !second.Suppressed || second.Refreshed {
		t.Fatalf("second refresh inside the gap = %+v, want suppressed", second)
	}
	want := clock.Add(30 * time.Second).UnixMilli()
	if second.RetryAtMS == nil || *second.RetryAtMS != want {
		t.Fatalf("retry_at_ms = %v, want %d", second.RetryAtMS, want)
	}
	if fetches.Load() != 1 {
		t.Fatalf("fetches = %d, want 1", fetches.Load())
	}

	// Once the clock passes the gap the account is scraped again.
	clock = clock.Add(31 * time.Second)
	third, err := collector.RefreshAccount(context.Background(), "acct-1")
	if err != nil || !third.Refreshed {
		t.Fatalf("refresh after the gap = %+v %v", third, err)
	}
	if fetches.Load() != 2 {
		t.Fatalf("fetches = %d, want 2", fetches.Load())
	}
}

// TestRefreshAccountRespectsPersistedBackoff pins that a manual refresh obeys a
// stored 429/backoff instead of silently bypassing it.
func TestRefreshAccountRespectsPersistedBackoff(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	future := clock.Add(2 * time.Hour).UnixMilli()
	seed := enabledSeedWithVersion("acct-1", 1)
	seed.NextAttemptAtMS = &future

	var fetches atomic.Int32
	collector := newRefreshCollector(&clock, map[string]StoredAccount{"acct-1": seed}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		fetches.Add(1)
		return successSnapshot(account.ID, clock), nil
	})
	result, err := collector.RefreshAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Suppressed || result.RetryAtMS == nil || *result.RetryAtMS != future {
		t.Fatalf("manual refresh ignored the backoff: %+v", result)
	}
	if fetches.Load() != 0 {
		t.Fatalf("fetches = %d, want 0", fetches.Load())
	}
}

// TestRefreshAccountPausedOneOff proves a paused account can still be refreshed
// explicitly - a one-off reading that does not change `enabled` - while the
// automatic trigger leaves it alone.
func TestRefreshAccountPausedOneOff(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	seed := enabledSeedWithVersion("acct-1", 1)
	seed.Enabled = false

	var fetches atomic.Int32
	collector := newRefreshCollector(&clock, map[string]StoredAccount{"acct-1": seed}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		fetches.Add(1)
		return successSnapshot(account.ID, clock), nil
	})
	collector.ManualMinGap = -1

	result, err := collector.RefreshAccount(context.Background(), "acct-1")
	if err != nil || !result.Refreshed {
		t.Fatalf("manual refresh of a paused account = %+v %v", result, err)
	}
	// The automatic trigger must not scrape a disabled account.
	collector.refreshTriggered(context.Background(), "acct-1")
	if fetches.Load() != 1 {
		t.Fatalf("fetches = %d, want only the manual one", fetches.Load())
	}
}

// TestRefreshAccountJoinsTheInFlightScrape pins that a manual refresh coalesces onto
// a scrape already running instead of starting a second one, and reports the
// in-flight outcome rather than a fabricated success. The in-flight call is created
// directly and left registered with its done channel already closed, so the manual
// call finds it no matter how the goroutine is scheduled.
func TestRefreshAccountJoinsTheInFlightScrape(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var fetches atomic.Int32
	collector := newRefreshCollector(&clock, map[string]StoredAccount{"acct-1": enabledSeedWithVersion("acct-1", 9)}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		fetches.Add(1)
		return successSnapshot(account.ID, clock), nil
	})
	collector.ManualMinGap = -1

	call, owner := collector.beginRefresh("acct-1")
	if !owner {
		t.Fatal("expected to own the first call")
	}
	// A real periodic pass reports Refreshed once the scrape has run; publish that
	// result and complete the call while leaving it registered.
	call.cookieVersion = 9
	call.result = RefreshResult{Refreshed: true}
	close(call.done)
	t.Cleanup(func() {
		collector.mu.Lock()
		delete(collector.calls, "acct-1")
		collector.mu.Unlock()
	})

	result, err := collector.RefreshAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Refreshed || result.Suppressed {
		t.Fatalf("coalesced refresh = %+v, want the in-flight result", result)
	}
	if fetches.Load() != 0 {
		t.Fatalf("a coalesced manual refresh started a second scrape: fetches = %d", fetches.Load())
	}
	if !collector.Refreshing("acct-1") {
		t.Fatal("the in-flight call should still be visible while registered")
	}
}

// TestRefreshAccountSuppressedCoalesceIsNotAFalseSuccess proves a coalesced call
// surfaces a suppressed in-flight outcome as suppressed, never as a success.
func TestRefreshAccountSuppressedCoalesceIsNotAFalseSuccess(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	collector := newRefreshCollector(&clock, map[string]StoredAccount{"acct-1": enabledSeedWithVersion("acct-1", 9)}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		return successSnapshot(account.ID, clock), nil
	})
	collector.ManualMinGap = -1

	call, _ := collector.beginRefresh("acct-1")
	call.cookieVersion = 9
	call.result = RefreshResult{Suppressed: true}
	close(call.done)
	t.Cleanup(func() {
		collector.mu.Lock()
		delete(collector.calls, "acct-1")
		collector.mu.Unlock()
	})

	result, err := collector.RefreshAccount(context.Background(), "acct-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Refreshed || !result.Suppressed {
		t.Fatalf("coalesced suppressed call reported success: %+v", result)
	}
}

// TestRefreshAccountUnknownAccount proves the manual entry point reports a missing
// account rather than scraping a zero value.
func TestRefreshAccountUnknownAccount(t *testing.T) {
	clock := time.Now()
	collector := newRefreshCollector(&clock, map[string]StoredAccount{}, func(context.Context, Account, string) (Snapshot, error) {
		t.Fatal("a missing account must not be scraped")
		return Snapshot{}, nil
	})
	if _, err := collector.RefreshAccount(context.Background(), "nope"); err != ErrAccountNotFound {
		t.Fatalf("err = %v, want ErrAccountNotFound", err)
	}
}

// TestRefreshResultCarriesTheCookieVersion pins that a refresh records the cookie
// version it read and marks itself manual, which is what lets the repository drop a
// stale reading without dropping a paused manual refresh.
func TestRefreshResultCarriesTheCookieVersion(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	seed := enabledSeedWithVersion("acct-1", 5)
	var recorded atomic.Int32
	collector := newRefreshCollector(&clock, map[string]StoredAccount{"acct-1": seed}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		return successSnapshot(account.ID, clock), nil
	})
	collector.Save = func(_ context.Context, result CollectionResult) error {
		recorded.Add(1)
		if result.CookieUpdatedAtMS == nil || *result.CookieUpdatedAtMS != 5 {
			t.Errorf("result carried cookie version %v, want 5", result.CookieUpdatedAtMS)
		}
		if result.Auto {
			t.Error("a manual refresh was marked automatic")
		}
		return nil
	}
	if _, err := collector.RefreshAccount(context.Background(), "acct-1"); err != nil {
		t.Fatal(err)
	}
	if recorded.Load() != 1 {
		t.Fatalf("recorded = %d, want 1", recorded.Load())
	}
}

// TestManualRefreshReportsIncompleteWhenCookieChangedUnderScrape pins the new manual
// policy: a single round, no catch-up. When the cookie is replaced while the manual
// scrape runs, the store refuses the old-cookie result and the call reports itself
// incomplete (ErrRefreshSuperseded, mapped to 503) rather than returning an old OK.
func TestManualRefreshReportsIncompleteWhenCookieChangedUnderScrape(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var cookieVersion atomic.Int64
	cookieVersion.Store(20)
	collector := newRefreshCollector(&clock, map[string]StoredAccount{}, nil)
	collector.AccountByID = func(_ context.Context, id string) (StoredAccount, bool, error) {
		if id != "acct-1" {
			return StoredAccount{}, false, nil
		}
		return enabledSeedWithVersion("acct-1", cookieVersion.Load()), true, nil
	}
	collector.ManualMinGap = -1
	var fetches atomic.Int32
	collector.Fetch = func(_ context.Context, account Account, _ string) (Snapshot, error) {
		fetches.Add(1)
		// The operator replaces the cookie while this manual scrape is running.
		cookieVersion.Store(21)
		return successSnapshot(account.ID, clock), nil
	}
	if _, err := collector.RefreshAccount(context.Background(), "acct-1"); !errors.Is(err, ErrRefreshSuperseded) {
		t.Fatalf("err = %v, want ErrRefreshSuperseded", err)
	}
	if fetches.Load() != 1 {
		t.Fatalf("the manual refresh retried after a cookie change: fetches = %d", fetches.Load())
	}
}

// TestTriggerMarksOnePendingWhenCookieChangedUnderScrape pins the fix: a trigger whose
// scrape read a cookie the operator has since replaced does not chase inside the call,
// but leaves exactly one pending marker so the worker takes the newest cookie in the
// next round.
func TestTriggerMarksOnePendingWhenCookieChangedUnderScrape(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var cookieVersion atomic.Int64
	cookieVersion.Store(20)
	collector := newRefreshCollector(&clock, map[string]StoredAccount{}, nil)
	collector.AccountByID = func(_ context.Context, id string) (StoredAccount, bool, error) {
		if id != "acct-1" {
			return StoredAccount{}, false, nil
		}
		return enabledSeedWithVersion("acct-1", cookieVersion.Load()), true, nil
	}
	var fetches atomic.Int32
	collector.Fetch = func(_ context.Context, account Account, _ string) (Snapshot, error) {
		fetches.Add(1)
		cookieVersion.Store(21)
		return successSnapshot(account.ID, clock), nil
	}
	collector.refreshTriggered(context.Background(), "acct-1")
	if fetches.Load() != 1 {
		t.Fatalf("the trigger chased inside one call: fetches = %d", fetches.Load())
	}
	pending := collector.drainPending()
	if len(pending) != 1 || pending[0] != "acct-1" {
		t.Fatalf("pending = %v, want exactly one marker for acct-1", pending)
	}
}

// TestTriggerFollowsUpWithNewCookieWithoutCadence is the deterministic worker case: a
// cookie is replaced while an automatic scrape is in flight; once the old round is
// released, the worker runs one more round with the new cookie, with no clock advance
// and no cadence wait. The old round is a 429, so the follow-up would be suppressed if
// that stale result had been written - it happens only because the store refused it.
// Repeated triggers for the same change collapse into that one follow-up.
func TestTriggerFollowsUpWithNewCookieWithoutCadence(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var cookieVersion atomic.Int64
	cookieVersion.Store(10)
	collector := newRefreshCollector(&clock, map[string]StoredAccount{}, nil)
	collector.AccountByID = func(_ context.Context, id string) (StoredAccount, bool, error) {
		if id != "acct-1" {
			return StoredAccount{}, false, nil
		}
		return enabledSeedWithVersion("acct-1", cookieVersion.Load()), true, nil
	}
	firstStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	var fetches atomic.Int32
	collector.Fetch = func(_ context.Context, account Account, _ string) (Snapshot, error) {
		if fetches.Add(1) == 1 {
			firstStarted <- struct{}{}
			<-release
			return Snapshot{
				AccountID: account.ID, ObservedAt: clock, Failure: FailureTransport,
				Err: "rate limited", RetryAfter: time.Hour, HasRetryAfter: true,
			}, nil
		}
		return successSnapshot(account.ID, clock), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	collector.Start(ctx)
	collector.startWorker(ctx)

	collector.Trigger("acct-1")
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the first scrape never started")
	}
	// The operator replaces the cookie; the update triggers, and repeated triggers
	// collapse into one pending marker.
	cookieVersion.Store(11)
	collector.Trigger("acct-1")
	collector.Trigger("acct-1")
	collector.Trigger("acct-1")
	close(release)

	waitUntil(t, func() bool { return fetches.Load() >= 2 })
	waitUntil(t, func() bool { return !collector.Refreshing("acct-1") })
	if fetches.Load() != 2 {
		t.Fatalf("fetches = %d, want 2 (one collapsed follow-up)", fetches.Load())
	}
}

// TestTriggerDoesNotFollowUpWhenPaused pins that a change under a scrape does not keep
// scraping a disabled account: the follow-up reloads the account and stops at the
// enabled check.
func TestTriggerDoesNotFollowUpWhenPaused(t *testing.T) {
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	var cookieVersion atomic.Int64
	cookieVersion.Store(10)
	var enabled atomic.Bool
	enabled.Store(true)
	collector := newRefreshCollector(&clock, map[string]StoredAccount{}, nil)
	collector.AccountByID = func(_ context.Context, id string) (StoredAccount, bool, error) {
		if id != "acct-1" {
			return StoredAccount{}, false, nil
		}
		seed := enabledSeedWithVersion("acct-1", cookieVersion.Load())
		seed.Enabled = enabled.Load()
		return seed, true, nil
	}
	firstStarted := make(chan struct{}, 1)
	release := make(chan struct{})
	var fetches atomic.Int32
	collector.Fetch = func(_ context.Context, account Account, _ string) (Snapshot, error) {
		if fetches.Add(1) == 1 {
			firstStarted <- struct{}{}
			<-release
		}
		return successSnapshot(account.ID, clock), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	collector.Start(ctx)
	collector.startWorker(ctx)

	collector.Trigger("acct-1")
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("the first scrape never started")
	}
	// The operator replaces the cookie and pauses the account while the scrape runs.
	cookieVersion.Store(11)
	enabled.Store(false)
	collector.Trigger("acct-1")
	close(release)

	waitUntil(t, func() bool { return !collector.Refreshing("acct-1") })
	if fetches.Load() != 1 {
		t.Fatalf("a paused account was scraped again: fetches = %d", fetches.Load())
	}
}

// TestRunStopsWithInjectedWait proves Run returns on cancellation even when a trigger
// was queued, so the worker leaves no goroutine behind.
func TestRunStopsWithInjectedWait(t *testing.T) {
	clock := time.Now()
	collector := newRefreshCollector(&clock, map[string]StoredAccount{}, func(context.Context, Account, string) (Snapshot, error) {
		return Snapshot{}, nil
	})
	collector.Accounts = func(context.Context) ([]StoredAccount, error) { return nil, nil }
	waited := make(chan struct{}, 1)
	collector.After = func(time.Duration) <-chan time.Time {
		select {
		case waited <- struct{}{}:
		default:
		}
		return make(chan time.Time)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	collector.Trigger("acct-1")
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("Run never requested its injected wait")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on cancellation")
	}
}

// TestRefreshBeforeStartIsRefused pins the fix for the startup window: a refresh
// that arrives before Start installs the lifecycle context is refused instead of
// running on the request context, and the already-cancelled request proves no scrape
// is started from it.
func TestRefreshBeforeStartIsRefused(t *testing.T) {
	clock := time.Now()
	var fetched atomic.Int32
	collector := &Collector{
		AccountByID: func(context.Context, string) (StoredAccount, bool, error) {
			return enabledSeedWithVersion("acct-1", 1), true, nil
		},
		Decrypt:     func([]byte, []byte) ([]byte, error) { return []byte("cookie"), nil },
		Fetch:       func(context.Context, Account, string) (Snapshot, error) { fetched.Add(1); return Snapshot{}, nil },
		Now:         func() time.Time { return clock },
		BackoffBase: time.Minute,
		BackoffMax:  time.Hour,
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collector.RefreshAccount(requestCtx, "acct-1"); !errors.Is(err, ErrServiceNotStarted) {
		t.Fatalf("err = %v, want ErrServiceNotStarted", err)
	}
	if fetched.Load() != 0 {
		t.Fatalf("a pre-start refresh scraped: fetches = %d", fetched.Load())
	}
	// Triggers queued before Start stay inert until the worker runs under the real
	// lifecycle context.
	collector.Trigger("acct-1")
	if fetched.Load() != 0 {
		t.Fatalf("a pre-start trigger scraped: fetches = %d", fetched.Load())
	}
}

// TestManualRefreshBoundedWhenConcurrencySaturated proves the third manual refresh
// returns by its deadline while both permits are held, rather than queueing
// indefinitely and then scraping. It is deterministic: the two holders block inside
// the injected fetch (ignoring the deadline) so the permits stay taken.
func TestManualRefreshBoundedWhenConcurrencySaturated(t *testing.T) {
	clock := time.Now()
	seeds := map[string]StoredAccount{
		"a": enabledSeedWithVersion("a", 1),
		"b": enabledSeedWithVersion("b", 1),
		"c": enabledSeedWithVersion("c", 1),
	}
	release := make(chan struct{})
	started := make(chan string, 2)
	var thirdFetches atomic.Int32
	collector := newRefreshCollector(&clock, seeds, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		if account.ID == "c" {
			thirdFetches.Add(1)
		}
		if account.ID == "a" || account.ID == "b" {
			started <- account.ID
		}
		<-release
		return successSnapshot(account.ID, clock), nil
	})
	collector.ManualMinGap = -1
	collector.Concurrency = 2
	collector.RefreshTimeout = 50 * time.Millisecond

	holderDone := make(chan struct{}, 2)
	go func() { _, _ = collector.RefreshAccount(context.Background(), "a"); holderDone <- struct{}{} }()
	go func() { _, _ = collector.RefreshAccount(context.Background(), "b"); holderDone <- struct{}{} }()
	for count := 0; count < 2; count++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("the two holding scrapes did not both start")
		}
	}

	began := time.Now()
	result, err := collector.RefreshAccount(context.Background(), "c")
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(began); elapsed > 2*time.Second {
		t.Fatalf("the third refresh did not return by its deadline: %v", elapsed)
	}
	if !result.Suppressed || result.Refreshed {
		t.Fatalf("the third refresh = %+v, want suppressed without a scrape", result)
	}
	if thirdFetches.Load() != 0 {
		t.Fatalf("the third refresh scraped while both permits were held: fetches = %d", thirdFetches.Load())
	}
	close(release)
	<-holderDone
	<-holderDone
}

// TestManualRefreshUsesServiceContextNotRequest proves a scrape and its persistence
// are rooted in the service context: cancelling the request that asked for the
// refresh does not truncate the work or lose the reading.
func TestManualRefreshUsesServiceContextNotRequest(t *testing.T) {
	clock := time.Now()
	proceed := make(chan struct{})
	started := make(chan struct{}, 1)
	collector := newRefreshCollector(&clock, map[string]StoredAccount{"acct-1": enabledSeedWithVersion("acct-1", 1)}, func(ctx context.Context, account Account, _ string) (Snapshot, error) {
		started <- struct{}{}
		<-proceed
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		return successSnapshot(account.ID, clock), nil
	})
	collector.ManualMinGap = -1
	var persisted atomic.Int32
	collector.Save = func(_ context.Context, result CollectionResult) error {
		if result.IsSuccess {
			persisted.Add(1)
		}
		return nil
	}
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _, _ = collector.RefreshAccount(requestCtx, "acct-1"); close(done) }()
	<-started
	cancelRequest()
	close(proceed)
	<-done
	if persisted.Load() != 1 {
		t.Fatalf("the scrape did not survive the cancelled request: persisted = %d", persisted.Load())
	}
}

// TestShutdownCancelsQueuedTrigger proves shutdown cancels queued work: a trigger
// that is waiting for a concurrency permit is dropped rather than scraping after the
// process has stopped, and its worker does not linger.
func TestShutdownCancelsQueuedTrigger(t *testing.T) {
	clock := time.Now()
	seeds := map[string]StoredAccount{
		"a": enabledSeedWithVersion("a", 1),
		"b": enabledSeedWithVersion("b", 1),
		"c": enabledSeedWithVersion("c", 1),
	}
	release := make(chan struct{})
	started := make(chan string, 2)
	var thirdFetches atomic.Int32
	collector := newRefreshCollector(&clock, seeds, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		if account.ID == "c" {
			thirdFetches.Add(1)
		}
		if account.ID == "a" || account.ID == "b" {
			started <- account.ID
		}
		<-release
		return successSnapshot(account.ID, clock), nil
	})
	collector.ManualMinGap = -1
	collector.Concurrency = 2
	collector.RefreshTimeout = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	collector.Start(ctx)
	collector.startWorker(ctx)

	holderDone := make(chan struct{}, 2)
	go func() { _, _ = collector.RefreshAccount(ctx, "a"); holderDone <- struct{}{} }()
	go func() { _, _ = collector.RefreshAccount(ctx, "b"); holderDone <- struct{}{} }()
	for count := 0; count < 2; count++ {
		<-started
	}

	collector.Trigger("c")
	waitUntil(t, func() bool { return collector.Refreshing("c") })
	cancel()
	waitUntil(t, func() bool { return !collector.Refreshing("c") })
	if thirdFetches.Load() != 0 {
		t.Fatalf("a queued trigger scraped after shutdown: fetches = %d", thirdFetches.Load())
	}
	close(release)
	<-holderDone
	<-holderDone
}

// waitUntil spins on a condition with a bounded deadline, so a test never sleeps for
// a fixed period and never hangs.
func waitUntil(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition was not met before the deadline")
		}
		runtime.Gosched()
	}
}
