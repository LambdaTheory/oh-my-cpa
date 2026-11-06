package feishu

import (
	"strings"
	"testing"
	"time"
)

func TestAttentionDoesNotPromoteHistoricalAuthIntoCurrentRisk(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity"}}}
	for _, key := range []string{"synthetic-A", "synthetic-B", "synthetic-C"} {
		report.Providers[0].Accounts = append(report.Providers[0].Accounts, AccountReport{
			Key: key, Label: key, State: StateStale, OK: true, AuthFailed: true,
			AuthSignalObservedAt: old.Add(time.Minute), ObservedAt: old, LastSuccessAt: old,
			Windows: []Window{accountWindow(100, nil)},
		})
	}
	report.Providers = append(report.Providers, ProviderReport{Provider: "codex", Accounts: []AccountReport{{
		Key: "fresh", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(95, nil)},
	}}})
	alerts := attentionSignals(report, 10, now)
	if len(alerts) != 1 || alerts[0].Key != "fresh" || alerts[0].Kind != AlertQuotaLow {
		t.Fatalf("historical authentication displaced a current risk: %+v", alerts)
	}
	for _, account := range report.Providers[0].Accounts {
		if !account.AuthFailed || credentialRecovered(account) {
			t.Fatal("suppressing stale risk must not mark the historical incident recovered")
		}
	}
}

func TestFreshAuthUsesAuthenticationClockInsteadOfOldQuota(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	authAt := now.Add(-time.Minute)
	report := Report{Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{{
		Key: "synthetic", State: StateInvalid, AuthFailed: true, AuthSignalObservedAt: authAt,
		ObservedAt: now.Add(-17 * 24 * time.Hour),
	}}}}}
	alerts := decisionAlerts(Message{Report: &report, EvaluatedAt: now})
	if len(alerts) != 1 || alerts[0].Kind != AlertCredential || !alerts[0].ObservedAt.Equal(authAt) || alerts[0].FreshWindowMS <= 0 || alerts[0].EvidenceSource != EvidenceAuthentication {
		t.Fatalf("fresh rejection lost its own bounded evidence: %+v", alerts)
	}
}

func TestLatestNonAuthAttemptDoesNotRenewHistoricalAuthEvidence(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	report := Report{Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{{
		Key: "synthetic", State: StateStale, AuthFailed: true, AuthSignalObservedAt: old,
		ObservedAt: old, LastSuccessAt: old, AttemptedAt: now, CollectionFailed: true,
		AttemptOutcome: "failed", AttemptReason: "network",
	}}}}}
	alerts := attentionSignals(report, 10, now)
	if len(alerts) != 1 || alerts[0].Kind != AlertReadingMissing || !alerts[0].ObservedAt.Equal(now) || alerts[0].FreshWindowMS <= 0 || alerts[0].EvidenceSource != EvidenceAttempt {
		t.Fatalf("current collection failure must use the independent attempt: %+v", alerts)
	}
	if strings.Contains(alerts[0].Title+alerts[0].Advice, "认证失败") || strings.Contains(alerts[0].Advice, "登录") {
		t.Fatalf("transport evidence became a credential diagnosis: %+v", alerts[0])
	}
}

func TestHistoricalPendingCannotReintroduceCurrentRisk(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	report := Report{Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{{
		Key: "synthetic", State: StateStale, AuthFailed: true, AuthSignalObservedAt: old, ObservedAt: old,
	}}}}}
	var pending []PendingItem
	for _, kind := range []string{AlertCredential, AlertReadingMissing, AlertQuotaLow, AlertQuotaExhausted, predictionKindPrefix} {
		pending = append(pending, PendingItem{
			ID: ItemID(kind, "antigravity", "synthetic", ""), Kind: kind, Provider: "antigravity", Key: "synthetic",
			Status: PendingOpen, Since: old, LastSeen: now, Action: "synthetic historical instruction",
		})
	}
	alerts := decisionAlerts(Message{Report: &report, Pending: pending, EvaluatedAt: now})
	for _, alert := range alerts {
		if alert.Kind == AlertCredential || alert.Kind == AlertReadingMissing || alert.Kind == AlertQuotaLow || alert.Kind == AlertQuotaExhausted || alert.Kind == predictionKindPrefix {
			t.Fatalf("pending ownership became fresh evidence: %+v", alerts)
		}
	}
}

func TestHistoricalAuthDoesNotHideSustainedNonAuthMonitoringOutage(t *testing.T) {
	base := mustTime("2026-11-06T02:00:00Z")
	old := base.Add(-17 * 24 * time.Hour)
	report := Report{Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{{
		Key: "synthetic", State: StateStale, AuthFailed: true, AuthSignalObservedAt: old,
		ObservedAt: old, LastSuccessAt: old, CollectionFailed: true, AttemptOutcome: "failed", AttemptReason: "network",
	}}}}}
	tracks := map[string]ChannelTrack{}
	var result PolicyResult
	for index := 0; index < 3; index++ {
		now := base.Add(time.Duration(index) * time.Minute)
		report.Providers[0].Accounts[0].AttemptedAt = now
		tracks = updateTracks(tracks, report, true, now)
		result = evaluatePolicy(policyCfg(), PolicyInput{Report: report, Tracks: tracks, Now: now})
		if index < 2 && hasItem(result.Items, AlertMonitorDown) {
			t.Fatal("monitor outage bypassed the distinct-failure threshold")
		}
	}
	if tracks["antigravity"].ConsecutiveFailures != 3 || !hasItem(result.Items, AlertMonitorDown) || hasItem(result.Items, AlertCredential) {
		t.Fatalf("old auth latch hid current monitoring failure: tracks=%+v result=%+v", tracks, result)
	}
}
