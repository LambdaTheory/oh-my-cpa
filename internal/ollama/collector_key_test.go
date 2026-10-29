package ollama

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// keyedCollector builds a collector whose decryptor distinguishes the cookie from
// the API key by nonce, so a test can prove which credential a path actually used.
func keyedCollector(now time.Time, account StoredAccount, fetch func(context.Context, Account, string) (Snapshot, error), fetchBalance func(context.Context, Account, string) (Snapshot, error)) (*Collector, *collectorCapture) {
	capture := &collectorCapture{}
	collector := &Collector{
		Accounts: func(context.Context) ([]StoredAccount, error) { return []StoredAccount{account}, nil },
		Save: func(_ context.Context, result CollectionResult) error {
			capture.results = append(capture.results, result)
			return nil
		},
		Decrypt: func(ciphertext, nonce []byte) ([]byte, error) {
			if string(nonce) == "key-nonce" {
				return []byte("stored-api-key"), nil
			}
			return []byte("stored-cookie"), nil
		},
		Fetch: func(ctx context.Context, acct Account, cookie string) (Snapshot, error) {
			capture.fetches++
			capture.cookies = append(capture.cookies, cookie)
			return fetch(ctx, acct, cookie)
		},
		FetchBalance: func(ctx context.Context, acct Account, apiKey string) (Snapshot, error) {
			return fetchBalance(ctx, acct, apiKey)
		},
		Now:         func() time.Time { return now },
		BackoffBase: time.Minute,
		BackoffMax:  time.Hour,
	}
	return collector, capture
}

func balanceSuccess(now time.Time, accountID string) Snapshot {
	remaining := int64(1250)
	used := int64(750)
	total := int64(2000)
	percent := 37.5
	return Snapshot{
		AccountID:  accountID,
		ObservedAt: now,
		Credits:    &quota.OllamaCredits{Source: "balance_api", RemainingCents: &remaining, UsedCents: &used, TotalCents: &total, UsedPercent: &percent},
	}
}

func keyedAccount(id string) StoredAccount {
	return StoredAccount{
		Account:           Account{ID: id, Enabled: true},
		Ciphertext:        []byte("cookie-cipher"),
		Nonce:             []byte("cookie-nonce"),
		APIKeyCiphertext:  []byte("key-cipher"),
		APIKeyNonce:       []byte("key-nonce"),
		CookieUpdatedAtMS: 100,
		APIKeyUpdatedAtMS: 200,
	}
}

// TestCollectorPrefersAPIKeyOverCookie pins the preference order: when a key is
// stored, a successful balance read decides the result and the cookie path is never
// touched.
func TestCollectorPrefersAPIKeyOverCookie(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	var usedKey string
	collector, capture := keyedCollector(now, keyedAccount("acct-key"),
		func(context.Context, Account, string) (Snapshot, error) {
			t.Fatal("the cookie path must not run when the key path succeeds")
			return Snapshot{}, nil
		},
		func(_ context.Context, account Account, apiKey string) (Snapshot, error) {
			usedKey = apiKey
			return balanceSuccess(now, account.ID), nil
		},
	)
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if usedKey != "stored-api-key" {
		t.Fatalf("key path used %q", usedKey)
	}
	if capture.fetches != 0 {
		t.Fatalf("the cookie path ran %d time(s)", capture.fetches)
	}
	if len(capture.results) != 1 || !capture.results[0].IsSuccess {
		t.Fatalf("result = %+v", capture.results)
	}
	result := capture.results[0]
	if result.APIKeyUpdatedAtMS == nil || *result.APIKeyUpdatedAtMS != 200 {
		t.Fatalf("key version guard = %v", result.APIKeyUpdatedAtMS)
	}
	if result.CookieUpdatedAtMS != nil {
		t.Fatalf("a key-path success must not carry the cookie version: %v", *result.CookieUpdatedAtMS)
	}
}

// TestCollectorFallsBackToCookieWhenKeyFails pins the fallback: a failed key read
// hands the account to the cookie path, whose success is the final verdict with no
// key error mixed in.
func TestCollectorFallsBackToCookieWhenKeyFails(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	collector, capture := keyedCollector(now, keyedAccount("acct-fallback"),
		func(_ context.Context, account Account, cookie string) (Snapshot, error) {
			if cookie != "stored-cookie" {
				t.Fatalf("cookie path saw %q", cookie)
			}
			return Snapshot{AccountID: account.ID, ObservedAt: now, Windows: []Window{{Name: "7d", UsedPercent: floatPtr(10)}}}, nil
		},
		func(_ context.Context, account Account, _ string) (Snapshot, error) {
			return Snapshot{AccountID: account.ID, ObservedAt: now, Failure: FailureAuth, Err: errBalanceUnauthorized}, nil
		},
	)
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if capture.fetches != 1 {
		t.Fatalf("the cookie fallback ran %d time(s)", capture.fetches)
	}
	result := capture.results[0]
	if !result.IsSuccess {
		t.Fatalf("cookie success must be final: %+v", result)
	}
	if result.Error != "" {
		t.Fatalf("a key failure contaminated the cookie success: %q", result.Error)
	}
	if result.CookieUpdatedAtMS == nil || *result.CookieUpdatedAtMS != 100 {
		t.Fatalf("cookie version guard = %v", result.CookieUpdatedAtMS)
	}
}

// TestCollectorBothPathsFailNamesBothPaths pins the failure message when neither
// credential produced a reading: both paths are named, and neither secret leaks.
func TestCollectorBothPathsFailNamesBothPaths(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	collector, capture := keyedCollector(now, keyedAccount("acct-broken"),
		func(_ context.Context, account Account, _ string) (Snapshot, error) {
			return Snapshot{AccountID: account.ID, ObservedAt: now, Failure: FailureAuth, Err: errSessionInvalid}, nil
		},
		func(_ context.Context, account Account, _ string) (Snapshot, error) {
			return Snapshot{AccountID: account.ID, ObservedAt: now, Failure: FailureAuth, Err: errBalanceUnauthorized}, nil
		},
	)
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	result := capture.results[0]
	if result.Status != StatusAuthInvalid {
		t.Fatalf("status = %q", result.Status)
	}
	if !strings.Contains(result.Error, "API key") || !strings.Contains(result.Error, "session cookie") {
		t.Fatalf("both paths must be named: %q", result.Error)
	}
	if strings.Contains(result.Error, "stored-api-key") || strings.Contains(result.Error, "stored-cookie") {
		t.Fatalf("a credential leaked into the error: %q", result.Error)
	}
}

// TestCollectorUsesCookieWhenNoKeyStored pins that accounts predating the key path
// keep working: no key ciphertext means the cookie path runs directly.
func TestCollectorUsesCookieWhenNoKeyStored(t *testing.T) {
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	account := keyedAccount("acct-cookie-only")
	account.APIKeyCiphertext = nil
	account.APIKeyNonce = nil
	collector, capture := keyedCollector(now, account,
		func(_ context.Context, acct Account, _ string) (Snapshot, error) {
			return Snapshot{AccountID: acct.ID, ObservedAt: now, Windows: []Window{{Name: "7d", UsedPercent: floatPtr(5)}}}, nil
		},
		func(context.Context, Account, string) (Snapshot, error) {
			t.Fatal("the key path must not run without a stored key")
			return Snapshot{}, nil
		},
	)
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if capture.fetches != 1 || !capture.results[0].IsSuccess {
		t.Fatalf("cookie-only account = fetches %d results %+v", capture.fetches, capture.results)
	}
}
