package feishu

import (
	"testing"
	"time"
)

func TestAuthFailureAgeDoesNotResolveHistoricalIncident(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	for _, test := range []struct {
		name     string
		observed time.Time
		want     bool
	}{
		{"fresh", now.Add(-time.Minute), true},
		{"boundary", now.Add(-DefaultFreshWindow), true},
		{"expired", now.Add(-DefaultFreshWindow - time.Millisecond), false},
		{"historical", now.Add(-17 * 24 * time.Hour), false},
		{"unknown", time.Time{}, false},
		{"nonpositive", time.UnixMilli(0), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			account := AccountReport{AuthFailed: true, AuthSignalObservedAt: test.observed, OK: true, State: StateStale, ObservedAt: now}
			if got := account.HasFreshAuthFailure(now); got != test.want {
				t.Fatalf("fresh authentication evidence = %v, want %v", got, test.want)
			}
			if !account.AuthFailed || credentialRecovered(account) {
				t.Fatal("evidence age must not erase or resolve the authentication incident")
			}
		})
	}
	account := AccountReport{AuthSignalObservedAt: now, OK: true, State: StateHealthy, ObservedAt: now}
	if account.HasFreshAuthFailure(now) || !credentialRecovered(account) {
		t.Fatal("positive recovery must not be contradicted by a historical signal timestamp alone")
	}
}
