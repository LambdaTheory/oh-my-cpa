package feishu

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestAC25AC26AC27DistinctFailuresAndTimeBothRequired(t *testing.T) {
	base := mustTime("2026-10-28T12:00:00Z")
	report := Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateStale, LastSuccessAt: base, CollectionFailed: true}}}}}
	tracks := map[string]ChannelTrack{}
	for _, minute := range []int{10, 10, 20, 29, 30} {
		now := base.Add(time.Duration(minute) * time.Minute)
		report.Providers[0].Accounts[0].AttemptedAt = now
		tracks = updateTracks(tracks, report, true, now)
		result := evaluatePolicy(policyCfg(), PolicyInput{Report: report, Tracks: tracks, Now: now})
		if hasItem(result.Items, AlertMonitorDown) != (minute >= 30) {
			t.Fatalf("minute=%d tracks=%+v result=%+v", minute, tracks, result)
		}
		if minute == 10 && tracks["codex"].ConsecutiveFailures != 1 {
			t.Fatal("cached failure counted twice")
		}
	}
	account := &report.Providers[0].Accounts[0]
	account.OK, account.CollectionFailed, account.State = true, false, StateHealthy
	account.ObservedAt, account.AttemptedAt = base.Add(31*time.Minute), base.Add(31*time.Minute)
	tracks = updateTracks(tracks, report, true, account.AttemptedAt)
	if tracks["codex"].ConsecutiveFailures != 0 {
		t.Fatal("one success did not reset failures")
	}
}

func TestAC26ExplicitAuthFailureDoesNotCountAsMonitorFailure(t *testing.T) {
	now := mustTime("2026-10-28T12:00:00Z")
	report := Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateInvalid, AuthFailed: true, AttemptedAt: now}}}}}
	tracks := updateTracks(map[string]ChannelTrack{"codex": {FirstSeen: now.Add(-time.Hour), ConsecutiveFailures: 2}}, report, true, now)
	if tracks["codex"].ConsecutiveFailures != 0 {
		t.Fatal("explicit upstream problem counted as missing response")
	}
}

func TestAC14AC25LowQuotaAndSingleFailureDoNotPush(t *testing.T) {
	env := newTestEnv(t, Report{}, true)
	now := *env.clock
	env.source.report = Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", OK: true, State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(95, nil)}}}}}}
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatal("low quota pushed")
	}
	account := &env.source.report.Providers[0].Accounts[0]
	account.OK, account.CollectionFailed = false, true
	account.AttemptedAt = now.Add(time.Minute)
	*env.clock = account.AttemptedAt
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatal("single failure pushed")
	}
}

func TestAC14ForecastTrackingDoesNotSuppressFirstExhaustion(t *testing.T) {
	now := mustTime("2026-10-28T12:00:00Z")
	reset := now.Add(time.Hour)
	resetMS := reset.UnixMilli()
	state := EmptyState()
	job := &PredictionJob{service: NewService(Options{})}
	item := PredictionDigestItem{Provider: "codex", Key: "synthetic", WindowID: "7d", Verdict: "will_exhaust", ObservedAtMS: now.UnixMilli(), ResetAtMS: &resetMS, ResetAccuracy: "exact"}
	if job.queueFindings(state, readyPredictionConfig(), now, PredictionDigest{Items: []PredictionDigestItem{item}}) {
		t.Fatal("forecast pushed")
	}
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", OK: true, State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(100, &reset)}}}}}}
	result := evaluatePolicy(PolicyConfig{EnableAttention: true}, PolicyInput{Report: report, Prev: state.PendingItems, Now: now})
	if len(result.Alerts) != 1 || result.Alerts[0].Kind != AlertQuotaExhausted {
		t.Fatal("tracked forecast suppressed actual exhaustion")
	}
	if repeated := evaluatePolicy(PolicyConfig{EnableAttention: true}, PolicyInput{Report: report, Prev: result.Items, Now: now}); len(repeated.Alerts) != 0 {
		t.Fatal("exhaustion repeated")
	}
}

func TestAC27AC28OutagePushesOnceAndRecoversSilently(t *testing.T) {
	env := newTestEnv(t, Report{}, true)
	base := *env.clock
	state := EmptyState()
	state.Bootstrapped = true
	for _, provider := range []string{"codex", "claude"} {
		state.ChannelTracks[provider] = ChannelTrack{FirstSeen: base.Add(-time.Hour), LastValid: base.Add(-time.Hour), ConsecutiveFailures: 2, LastAttempt: base.Add(-time.Minute)}
		env.source.report.Providers = append(env.source.report.Providers, ProviderReport{Provider: provider, Accounts: []AccountReport{{Key: provider, State: StateStale, AttemptedAt: base, CollectionFailed: true}}})
	}
	if err := env.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	env.service.tick(context.Background())
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("outage sent %d cards", sends)
	}
	if payload := marshalTestJSON(env.sender.lastCard()); !strings.Contains(payload, "2 个渠道 监控停摆") {
		t.Fatal(payload)
	}
	*env.clock = base.Add(time.Minute)
	for index := range env.source.report.Providers {
		account := &env.source.report.Providers[index].Accounts[0]
		account.OK, account.CollectionFailed, account.State = true, false, StateHealthy
		account.AttemptedAt, account.ObservedAt = *env.clock, *env.clock
		account.Windows = []Window{accountWindow(20, nil)}
	}
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatal("recovery sent a card")
	}
	loaded, err := env.store.Load(context.Background())
	if err != nil || len(openItems(loaded.PendingItems)) != 0 {
		t.Fatal("one valid observation did not close outage")
	}
}
