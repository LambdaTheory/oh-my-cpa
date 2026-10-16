package ollama

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

type collectorCapture struct {
	results []CollectionResult
	cookies []string
	fetches int
}

func newTestCollector(now time.Time, accounts []StoredAccount, fetch func(ctx context.Context, account Account, cookie string) (Snapshot, error)) (*Collector, *collectorCapture) {
	capture := &collectorCapture{}
	collector := &Collector{
		Accounts: func(context.Context) ([]StoredAccount, error) { return accounts, nil },
		Save: func(_ context.Context, result CollectionResult) error {
			capture.results = append(capture.results, result)
			return nil
		},
		Decrypt: func([]byte, []byte) ([]byte, error) { return []byte("decrypted-cookie"), nil },
		Fetch: func(ctx context.Context, account Account, cookie string) (Snapshot, error) {
			capture.fetches++
			capture.cookies = append(capture.cookies, cookie)
			return fetch(ctx, account, cookie)
		},
		Now:         func() time.Time { return now },
		BackoffBase: time.Minute,
		BackoffMax:  time.Hour,
	}
	return collector, capture
}

func floatPtr(value float64) *float64 { return &value }

func timePtr(value time.Time) *time.Time { return &value }

func enabledAccount(id string) StoredAccount {
	return StoredAccount{Account: Account{ID: id, Enabled: true}, Ciphertext: []byte("cipher"), Nonce: []byte("nonce")}
}

func TestCollectorSkipsDisabledAccounts(t *testing.T) {
	now := time.Now()
	collector, capture := newTestCollector(now, []StoredAccount{
		{Account: Account{ID: "off", Enabled: false}, Ciphertext: []byte("c"), Nonce: []byte("n")},
	}, func(context.Context, Account, string) (Snapshot, error) {
		t.Fatal("a disabled account must not be fetched")
		return Snapshot{}, nil
	})
	attempted, err := collector.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if attempted != 0 || capture.fetches != 0 || len(capture.results) != 0 {
		t.Fatalf("disabled account was collected: attempted=%d fetches=%d results=%d", attempted, capture.fetches, len(capture.results))
	}
}

func TestCollectorSuccessPersistsOllamaQuota(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	reset := now.Add(3 * time.Hour)
	collector, capture := newTestCollector(now, []StoredAccount{enabledAccount("acct-1")}, func(_ context.Context, account Account, cookie string) (Snapshot, error) {
		if cookie != "decrypted-cookie" {
			t.Fatalf("fetch cookie = %q", cookie)
		}
		return Snapshot{
			AccountID:  account.ID,
			ObservedAt: now,
			Windows: []Window{{
				Name: "5h", Label: "Ollama 5-hour", UsedPercent: floatPtr(42), ResetAt: timePtr(reset), ResetDerived: true,
			}},
		}, nil
	})
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(capture.results) != 1 {
		t.Fatalf("results = %d", len(capture.results))
	}
	result := capture.results[0]
	if result.Status != StatusOK || !result.IsSuccess {
		t.Fatalf("result = %+v", result)
	}
	if !strings.Contains(result.SnapshotJSON, `"auth_index":"ollama:acct-1"`) || !strings.Contains(result.SnapshotJSON, `"provider":"ollama"`) {
		t.Fatalf("snapshot identity = %s", result.SnapshotJSON)
	}
	if result.Error != "" || result.NextAttemptAtMS != nil {
		t.Fatalf("a success must clear the retry state: %+v", result)
	}
}

func TestCollectorAuthFailureDoesNotBackoff(t *testing.T) {
	now := time.Now()
	collector, capture := newTestCollector(now, []StoredAccount{enabledAccount("acct-1")}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		return Snapshot{AccountID: account.ID, ObservedAt: now, Failure: FailureAuth, Err: errSessionInvalid}, nil
	})
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := capture.results[0]
	if result.Status != StatusAuthInvalid || result.NextAttemptAtMS != nil || result.AttemptCount != 0 {
		t.Fatalf("auth failure must not back off: %+v", result)
	}
}

func TestCollectorTransportErrorBacksOff(t *testing.T) {
	now := time.Now()
	collector, capture := newTestCollector(now, []StoredAccount{enabledAccount("acct-1")}, func(context.Context, Account, string) (Snapshot, error) {
		return Snapshot{AccountID: "acct-1", ObservedAt: now}, errors.New("dial tcp: connection refused")
	})
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := capture.results[0]
	if result.Status != StatusTransportError {
		t.Fatalf("status = %q", result.Status)
	}
	if result.NextAttemptAtMS == nil || *result.NextAttemptAtMS != now.Add(time.Minute).UnixMilli() {
		t.Fatalf("next attempt = %v, want +1m", result.NextAttemptAtMS)
	}
	if result.AttemptCount != 1 {
		t.Fatalf("attempt count = %d", result.AttemptCount)
	}
}

func TestCollectorHonoursRetryAfter(t *testing.T) {
	now := time.Now()
	collector, capture := newTestCollector(now, []StoredAccount{enabledAccount("acct-1")}, func(_ context.Context, account Account, _ string) (Snapshot, error) {
		return Snapshot{
			AccountID: account.ID, ObservedAt: now, Failure: FailureTransport,
			Err: "rate limited", RetryAfter: 90 * time.Second, HasRetryAfter: true,
		}, nil
	})
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := capture.results[0]
	if result.NextAttemptAtMS == nil || *result.NextAttemptAtMS != now.Add(90*time.Second).UnixMilli() {
		t.Fatalf("Retry-After not honoured: %v", result.NextAttemptAtMS)
	}
}

func TestCollectorHonoursPersistedBackoff(t *testing.T) {
	now := time.Now()
	future := now.Add(10 * time.Minute).UnixMilli()
	account := enabledAccount("acct-1")
	account.NextAttemptAtMS = &future
	collector, capture := newTestCollector(now, []StoredAccount{account}, func(context.Context, Account, string) (Snapshot, error) {
		t.Fatal("a persisted backoff must suppress the fetch")
		return Snapshot{}, nil
	})
	attempted, err := collector.RunOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if attempted != 0 || capture.fetches != 0 {
		t.Fatalf("backoff ignored: attempted=%d fetches=%d", attempted, capture.fetches)
	}
}

func TestCollectorStopsOnCancelledContext(t *testing.T) {
	now := time.Now()
	collector, capture := newTestCollector(now, []StoredAccount{enabledAccount("acct-1")}, func(context.Context, Account, string) (Snapshot, error) {
		t.Fatal("a cancelled context must stop before fetching")
		return Snapshot{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := collector.RunOnce(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want Canceled", err)
	}
	if capture.fetches != 0 {
		t.Fatalf("fetch ran under a cancelled context")
	}
}

// TestCollectorRunWaitsOnInjectedAfter proves Run waits on the injected wait, not
// the wall clock: the test cancels the context as soon as the wait is requested.
func TestCollectorRunWaitsOnInjectedAfter(t *testing.T) {
	collector, _ := newTestCollector(time.Now(), nil, func(context.Context, Account, string) (Snapshot, error) {
		return Snapshot{}, nil
	})
	waited := make(chan struct{}, 1)
	never := make(chan time.Time)
	collector.After = func(time.Duration) <-chan time.Time {
		waited <- struct{}{}
		return never
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- collector.Run(ctx) }()
	select {
	case <-waited:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("Run never requested its injected wait")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop on cancellation")
	}
}

func TestSnapshotToQuotaMapsOllamaIdentity(t *testing.T) {
	reset := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	value := Snapshot{
		AccountID:  "opaque-id",
		ObservedAt: time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC),
		Plan:       "Pro",
		Balance:    "$12.50",
		Windows: []Window{
			{Name: "5h", Label: "Ollama 5-hour", UsedPercent: floatPtr(42), ResetDerived: true, ResetAt: timePtr(reset)},
			{Name: "7d", Label: "Ollama weekly", UsedPercent: floatPtr(88), ResetAt: timePtr(reset)},
		},
	}.ToQuota("My account")

	if value.AuthIndex != "ollama:opaque-id" {
		t.Fatalf("auth index = %q", value.AuthIndex)
	}
	if value.Provider != Provider || value.Type != Provider {
		t.Fatalf("provider identity = %q/%q", value.Provider, value.Type)
	}
	if value.Name != "My account" || value.Status != "healthy" {
		t.Fatalf("quota header = %+v", value)
	}
	if value.Plan == nil || value.Plan.PlanLabel != "Pro" || value.Plan.Credits == nil || value.Plan.Credits.Balance != "$12.50" {
		t.Fatalf("plan = %+v", value.Plan)
	}
	if len(value.Windows) != 2 {
		t.Fatalf("windows = %+v", value.Windows)
	}
	for _, window := range value.Windows {
		if window.Scope != quota.ObservationScopeStandard {
			t.Errorf("%s scope = %q", window.ID, window.Scope)
		}
		if window.PeriodHours != nil {
			t.Errorf("%s must not fabricate a duration: %v", window.ID, *window.PeriodHours)
		}
	}
	if value.Windows[0].Kind != "five_hour" || value.Windows[1].Kind != "weekly" {
		t.Fatalf("kinds = %q/%q", value.Windows[0].Kind, value.Windows[1].Kind)
	}
	if value.Windows[0].ResetAccuracy != "derived" || value.Windows[1].ResetAccuracy != "exact" {
		t.Fatalf("reset accuracy = %q/%q", value.Windows[0].ResetAccuracy, value.Windows[1].ResetAccuracy)
	}
	if value.Windows[0].RemainingPercent == nil || *value.Windows[0].RemainingPercent != 58 {
		t.Fatalf("remaining = %v", value.Windows[0].RemainingPercent)
	}
}
