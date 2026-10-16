package feishu

import (
	"testing"
	"time"
)

func policyCfg() PolicyConfig {
	return PolicyConfig{ChannelUnavailableAfter: 30 * time.Minute, MonitorDownAfter: 30 * time.Minute}
}

func accountWindow(used float64, reset *time.Time) Window {
	return Window{Name: "7d", Label: "7d", Scope: ScopeAccount, UsedPercent: &used, ResetAt: reset}
}

func TestCredentialInvalidAlertsOncePerEpisode(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	rep := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", Label: "acct", State: StateInvalid, OK: false, LastSuccessAt: now.Add(-time.Hour)}},
	}}}

	first := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Now: now})
	if len(first.Items) != 1 || first.Items[0].Kind != AlertCredential {
		t.Fatalf("expected one open credential item, got %+v", first.Items)
	}
	if len(first.Alerts) != 1 {
		t.Fatalf("expected one immediate alert, got %d", len(first.Alerts))
	}
	if first.Items[0].NotifiedAt.IsZero() {
		t.Fatal("opened item should be stamped notified")
	}

	second := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Prev: first.Items, Now: now.Add(time.Minute)})
	if len(second.Alerts) != 0 {
		t.Fatalf("ongoing problem must not re-alert, got %d", len(second.Alerts))
	}
	if len(second.Items) != 1 || second.Items[0].Status != PendingOpen {
		t.Fatalf("item should stay open, got %+v", second.Items)
	}

	// Recovery: a healthy reading resolves the item and never pushes.
	healthy := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true}},
	}}}
	third := evaluatePolicy(policyCfg(), PolicyInput{Report: healthy, Prev: second.Items, Now: now.Add(2 * time.Minute)})
	if len(third.Resolved) != 1 {
		t.Fatalf("expected one resolved item, got %d", len(third.Resolved))
	}
	if len(third.Alerts) != 0 {
		t.Fatalf("recovery must not push, got %d", len(third.Alerts))
	}
}

// TestOpenCredentialItemIsRetainedWithoutPositiveRecovery pins that an open
// credential item is not resolved just because the credential stopped reading as
// invalid: an unknown or stale state is not positive evidence of recovery.
// TestCredentialItemNotRecoveredWhileAuthFailed pins that even a stale state with a
// valid-looking reading does not close an open credential item while the newest
// collection evidence is an auth failure; clearing that evidence with a healthy
// reading does.
func TestCredentialItemNotRecoveredWhileAuthFailed(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	invalid := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", State: StateInvalid, OK: false, LastSuccessAt: now.Add(-time.Hour)}},
	}}}
	first := evaluatePolicy(policyCfg(), PolicyInput{Report: invalid, Now: now})

	unrefuted := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", State: StateStale, OK: true, AuthFailed: true}},
	}}}
	res := evaluatePolicy(policyCfg(), PolicyInput{Report: unrefuted, Prev: first.Items, Now: now.Add(time.Minute)})
	if len(res.Resolved) != 0 || len(res.Items) != 1 || res.Items[0].Status != PendingOpen {
		t.Fatalf("an unrefuted auth failure must retain the open item, got %+v", res)
	}

	recovered := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true, AuthFailed: false}},
	}}}
	res = evaluatePolicy(policyCfg(), PolicyInput{Report: recovered, Prev: res.Items, Now: now.Add(2 * time.Minute)})
	if len(res.Resolved) != 1 {
		t.Fatalf("a healthy reading with the auth failure cleared must resolve, got %d", len(res.Resolved))
	}
}

func TestOpenCredentialItemIsRetainedWithoutPositiveRecovery(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	invalid := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", State: StateInvalid, OK: false, LastSuccessAt: now.Add(-time.Hour)}},
	}}}
	first := evaluatePolicy(policyCfg(), PolicyInput{Report: invalid, Now: now})
	if len(first.Items) != 1 || first.Items[0].Status != PendingOpen {
		t.Fatalf("expected one open credential item, got %+v", first.Items)
	}
	for _, state := range []string{StateUnknown, StateStale, StateSuspect} {
		rep := Report{GeneratedAt: now, Providers: []ProviderReport{{
			Provider: "codex",
			Accounts: []AccountReport{{Key: "a1", State: state, OK: false}},
		}}}
		res := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Prev: first.Items, Now: now.Add(time.Minute)})
		if len(res.Resolved) != 0 {
			t.Fatalf("state %q is not recovery; the item must stay open, got %+v", state, res.Resolved)
		}
		if len(res.Items) != 1 || res.Items[0].Status != PendingOpen {
			t.Fatalf("state %q must retain the open item, got %+v", state, res.Items)
		}
	}
	// A valid reading is positive recovery and resolves the item.
	recovered := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true}},
	}}}
	res := evaluatePolicy(policyCfg(), PolicyInput{Report: recovered, Prev: first.Items, Now: now.Add(2 * time.Minute)})
	if len(res.Resolved) != 1 {
		t.Fatalf("a valid reading must resolve the item, got %d", len(res.Resolved))
	}
}

func TestChannelUnavailableWhenEarliestRecoveryBeyondThreshold(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Hour)
	rep := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{{
			Key: "a1", State: StateExhausted, OK: true,
			Windows: []Window{accountWindow(100, &reset)},
		}},
	}}}
	res := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Now: now})
	if !hasItem(res.Items, AlertChannelUnavailable) {
		t.Fatalf("expected channel_unavailable item, got %+v", res.Items)
	}
	if hasItem(res.Items, AlertCredential) {
		t.Fatal("an exhausted account is not a credential failure")
	}
}

func TestChannelUnavailableNearRecoveryIsNotImmediate(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	reset := now.Add(5 * time.Minute)
	rep := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{{Key: "a1", State: StateExhausted, OK: true, Windows: []Window{accountWindow(100, &reset)}}},
	}}}
	res := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Now: now})
	if hasItem(res.Items, AlertChannelUnavailable) {
		t.Fatal("a channel recovering within the threshold must not alert")
	}
}

func TestMonitorDownOnCollectorOutageWithNoAccounts(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	rep := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Error: "collector failed"}}}
	tracks := map[string]ChannelTrack{"codex": {FirstSeen: now.Add(-time.Hour)}}
	res := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Tracks: tracks, Now: now})
	if !hasItem(res.Items, AlertMonitorDown) {
		t.Fatalf("expected monitor_down for a collector outage, got %+v", res.Items)
	}
	if hasItem(res.Items, AlertChannelUnavailable) {
		t.Fatal("a channel with no enabled account is not channel_unavailable")
	}
}

func TestModelWindowDoesNotMakeAccountUnavailable(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	used := 100.0
	rep := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{{
			Key: "a1", State: StateHealthy, OK: true,
			Windows: []Window{{Name: "model", Label: "Opus", Scope: "model", UsedPercent: &used}},
		}},
	}}}
	res := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Now: now})
	if len(res.Items) != 0 {
		t.Fatalf("a model-scoped window must not make the whole account unavailable, got %+v", res.Items)
	}
}

func TestQuietStillProducesTheAlertForTheCallerToHold(t *testing.T) {
	now := time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	rep := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", State: StateInvalid}},
	}}}
	res := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Now: now, Quiet: true})
	// The classification is the same during quiet: the alert is produced and the
	// item is stamped once. The service persists it as a queued notification rather
	// than sending it.
	if len(res.Alerts) != 1 {
		t.Fatalf("quiet classification must still produce the alert, got %d", len(res.Alerts))
	}
	if len(res.Items) != 1 || res.Items[0].NotifiedAt.IsZero() {
		t.Fatalf("quiet-opened item must be stamped once, got %+v", res.Items)
	}
}

func TestTightChannelsUsesBestAccountWindowOnly(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	rep := Report{Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{
			{Key: "a1", State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(95, nil)}}, // 5% left
			{Key: "a2", State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(85, nil)}}, // 15% left
		},
	}}}
	tight := tightChannels(rep, 20, now, summaryFreshWindow)
	if len(tight) != 1 || tight[0].Provider != "claude" {
		t.Fatalf("expected claude tight, got %+v", tight)
	}
	// The judged account is the one with the most headroom (15%, not 5%).
	if tight[0].BestRemaining < 14 || tight[0].BestRemaining > 16 {
		t.Fatalf("best account remaining should be ~15, got %v", tight[0].BestRemaining)
	}
}

func TestTightChannelsIgnoresAChannelWithAnyRoom(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	rep := Report{Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{
			{Key: "a1", State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(95, nil)}}, // 5% left
			{Key: "a2", State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(10, nil)}}, // 90% left
		},
	}}}
	if tight := tightChannels(rep, 20, now, summaryFreshWindow); len(tight) != 0 {
		t.Fatalf("a channel with a roomy account is not tight, got %+v", tight)
	}
}

// TestTightChannelsSkipsStaleSample pins that a maxed window in a stale sample is
// not read as "0 remaining": the reading's age, not the report's assembly time,
// decides whether it may judge tightness.
func TestTightChannelsSkipsStaleSample(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	rep := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{{
			Key: "a1", State: StateHealthy, OK: true,
			ObservedAt: now.Add(-2 * time.Hour), // stale maxed sample
			Windows:    []Window{accountWindow(100, nil)},
		}},
	}}}
	if tight := tightChannels(rep, 20, now, summaryFreshWindow); len(tight) != 0 {
		t.Fatalf("a stale maxed sample must not make a channel tight, got %+v", tight)
	}
	// A fresh sample of the same reading is judged, so the gate is about age.
	fresh := rep
	fresh.Providers[0].Accounts[0].ObservedAt = now
	if tight := tightChannels(fresh, 20, now, summaryFreshWindow); len(tight) != 1 {
		t.Fatalf("a fresh maxed sample should be tight, got %+v", tight)
	}
}

// TestTightChannelsSkipsUnknownObservationTime pins that an account with no
// observation instant is unknown, never fresh enough to warn about.
func TestTightChannelsSkipsUnknownObservationTime(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	rep := Report{Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{{
			Key: "a1", State: StateHealthy, OK: true,
			Windows: []Window{accountWindow(100, nil)},
		}},
	}}}
	if tight := tightChannels(rep, 20, now, summaryFreshWindow); len(tight) != 0 {
		t.Fatalf("an untimed reading must not be tight, got %+v", tight)
	}
}

func TestDisabledAccountIsNeverChannelEvidence(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	reset := now.Add(4 * time.Hour)
	rep := Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", Disabled: true, State: StateExhausted, OK: true, Windows: []Window{accountWindow(100, &reset)}}},
	}}}
	res := evaluatePolicy(policyCfg(), PolicyInput{Report: rep, Now: now})
	if len(res.Items) != 0 {
		t.Fatalf("a disabled account must not drive alerts, got %+v", res.Items)
	}
}

func hasItem(items []PendingItem, kind string) bool {
	for _, item := range items {
		if item.Kind == kind {
			return true
		}
	}
	return false
}
