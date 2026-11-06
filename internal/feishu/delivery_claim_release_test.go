package feishu

import (
	"context"
	"testing"
	"time"
)

// A quiet-end batch selects both an expired and a fresh identity but can only
// deliver the fresh half. The never-delivered half must not keep its reservation;
// its next fresh evidence gets one send, without replaying the delivered half.
func TestMixedBatchReleasesOnlyItsNeverDeliveredSelection(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, Report{}, true)
	base := mustTime("2026-11-06T07:40:00Z")
	*env.clock = base
	env.source.report = invalidReportWith(base, "expired-half")
	env.service.tick(ctx)
	state := loadClaimState(t, env)
	id := ItemID(AlertCredential, "codex", "expired-half", "")
	original := claimItem(t, state, id)
	if len(state.Outbox) != 1 || state.Outbox[0].State != NotifyQueued {
		t.Fatalf("first quiet reservation missing: %+v", state)
	}

	*env.clock = base.Add(10 * time.Minute)
	env.source.report = invalidReportWith(*env.clock, "expired-half", "delivered-half")
	env.source.report.Providers[0].Accounts[0].AuthSignalObservedAt = base
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	if sends, _ := env.sender.counts(); sends != 0 || countState(state.Outbox, NotifyQueued) != 2 {
		t.Fatalf("expected two distinct quiet reservations: sends=%d state=%+v", sends, state)
	}

	*env.clock = mustTime("2026-11-06T08:00:00Z")
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	batch, exists := notificationOfKind(state.Outbox, KindAlertBatch)
	if sends, _ := env.sender.counts(); sends != 1 || !exists || batch.SentAt.IsZero() || len(batch.Alerts) != 1 || batch.Alerts[0].Key != "delivered-half" || deliveredRiskCount(state, AlertCredential, "expired-half") != 0 {
		t.Fatalf("the batch did not deliver only its fresh half: sends=%d state=%+v", sends, state)
	}
	closed := claimItem(t, state, id)
	requireSameOpenIncident(t, original, closed)
	if !closed.NotifiedAt.IsZero() {
		t.Errorf("partial batch delivery consumed the never-delivered identity: %+v", closed)
	}

	restartMonitorService(env)
	*env.clock = env.clock.Add(time.Minute)
	env.source.report = invalidReportWith(*env.clock, "expired-half", "delivered-half")
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	requireSameOpenIncident(t, original, claimItem(t, state, id))
	if sends, _ := env.sender.counts(); sends != 2 || deliveredRiskCount(state, AlertCredential, "expired-half") != 1 || deliveredRiskCount(state, AlertCredential, "delivered-half") != 1 {
		t.Errorf("fresh evidence must deliver the dropped half once, without replaying the delivered half: sends=%d state=%+v", sends, state)
	}
	*env.clock = env.clock.Add(time.Minute)
	env.source.report = invalidReportWith(*env.clock, "expired-half", "delivered-half")
	env.service.tick(ctx)
	if sends, _ := env.sender.counts(); sends > 2 {
		t.Errorf("an actually delivered half replayed: sends=%d", sends)
	}
}

func claimQuotaReport(at time.Time, used float64, reset time.Time) Report {
	return Report{GeneratedAt: at, Providers: []ProviderReport{{
		Provider: "codex", Accounts: []AccountReport{{
			Key: "forecast-then-exhausted", Label: "synthetic", State: StateHealthy,
			OK: true, ObservedAt: at, LastSuccessAt: at,
			Windows: []Window{accountWindow(used, &reset)},
		}},
	}}}
}

// A delivered forecast shares the window identity with a later exhaustion
// incident. Its receipt acknowledges only the forecast; the expired exhaustion
// row is released and the fresh escalation is delivered once.
func TestForecastDeliveryDoesNotAcknowledgeExhaustion(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, Report{}, true)
	base := mustTime("2026-11-06T10:00:00Z")
	*env.clock = base
	reset := base.Add(8 * time.Hour)
	resetMS := reset.UnixMilli()
	used := 60.0
	env.source.report = claimQuotaReport(base, used, reset)
	web, overlay := readyConfig()
	web.QuietStart, web.QuietEnd = "09:00", "11:00"
	web.SummaryTimes = []string{"10:00", "17:00"}
	env.service.Reload(ctx, web, overlay)
	digest := PredictionDigest{Items: []PredictionDigestItem{{
		Provider: "codex", Key: "forecast-then-exhausted", CredentialLabel: "synthetic",
		WindowID: "7d", WindowKind: "weekly", WindowScope: ScopeAccount,
		ResetAccuracy: "exact", ResetAtMS: &resetMS, UsedPercent: &used,
		LastsUntilReset: &cannotLast, ObservedAtMS: base.UnixMilli(),
		FreshWindowMS: (2 * time.Hour).Milliseconds(),
	}}}
	job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) {
		return digest, nil
	})
	job.Activate()
	env.sender.failSends = 2
	job.tick(ctx)
	state := loadClaimState(t, env)
	if sends, _ := env.sender.counts(); sends != 0 || len(state.Outbox) != 1 || state.Outbox[0].Kind != predictionKindPrefix+KindMorning || state.Outbox[0].State != NotifyPending || state.Outbox[0].Attempts != 1 {
		t.Fatalf("forecast did not remain pending after a rejected full/simple attempt: sends=%d state=%+v", sends, state)
	}

	*env.clock = base.Add(15 * time.Second)
	env.source.report = claimQuotaReport(*env.clock, 100, reset)
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	id := ItemID(AlertQuotaLow, "codex", "forecast-then-exhausted", "7d")
	original := claimItem(t, state, id)
	if sends, _ := env.sender.counts(); sends != 0 || original.Kind != AlertQuotaExhausted || original.Status != PendingOpen || !original.NotifiedAt.Equal(*env.clock) || countState(state.Outbox, NotifyQueued) != 1 {
		t.Fatalf("a distinct exhaustion incident was not queued during quiet: sends=%d state=%+v", sends, state)
	}

	*env.clock = base.Add(30 * time.Second)
	job.tick(ctx)
	state = loadClaimState(t, env)
	if sends, _ := env.sender.counts(); sends != 1 || deliveredRiskCount(state, predictionKindPrefix, "forecast-then-exhausted") != 1 || deliveredRiskCount(state, AlertQuotaExhausted, "forecast-then-exhausted") != 0 {
		t.Fatalf("quiet-time retry must deliver only the older forecast: sends=%d state=%+v", sends, state)
	}

	*env.clock = mustTime("2026-11-06T11:00:00Z")
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	dropped, exists := notificationOfKind(state.Outbox, AlertQuotaExhausted)
	if !exists || !isDiscardedNotification(dropped) || deliveredRiskCount(state, AlertQuotaExhausted, "forecast-then-exhausted") != 0 {
		t.Fatalf("stale exhaustion must close without delivery: %+v", state)
	}
	closed := claimItem(t, state, id)
	requireSameOpenIncident(t, original, closed)
	if !closed.NotifiedAt.IsZero() {
		t.Errorf("a delivered forecast retained the distinct, never-delivered exhaustion reservation: %+v", closed)
	}

	*env.clock = env.clock.Add(time.Minute)
	env.source.report = claimQuotaReport(*env.clock, 100, reset)
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	requireSameOpenIncident(t, original, claimItem(t, state, id))
	if sends, _ := env.sender.counts(); sends != 2 || deliveredRiskCount(state, AlertQuotaExhausted, "forecast-then-exhausted") != 1 {
		t.Errorf("fresh exhaustion was swallowed by an older forecast receipt: sends=%d state=%+v", sends, state)
	}
	*env.clock = env.clock.Add(time.Minute)
	env.source.report = claimQuotaReport(*env.clock, 100, reset)
	env.service.tick(ctx)
	if sends, _ := env.sender.counts(); sends > 2 {
		t.Errorf("a delivered exhaustion replayed: sends=%d", sends)
	}
}

// Delivered claims are scoped to their incident kind: a forecast receipt never
// fills or protects a quota incident even though both share a window identity.
func TestDeliveredClaimsAreScopedToTheirIncidentKind(t *testing.T) {
	now := mustTime("2026-11-06T10:00:30Z")
	id := ItemID(AlertQuotaLow, "codex", "shared-window", "7d")
	forecast := Alert{Kind: predictionKindPrefix, Provider: "codex", Key: "shared-window", Scope: "7d",
		ObservedAt: now.Add(-time.Minute), OccurredAt: now, FreshWindowMS: time.Hour.Milliseconds()}
	exhaustion := PendingItem{ID: id, Kind: AlertQuotaExhausted, Provider: "codex", Key: "shared-window",
		Status: PendingOpen, Since: now.Add(-15 * time.Second)}
	state := EmptyState()
	state.PendingItems = []PendingItem{exhaustion}
	state.Outbox = []Notification{{ID: "forecast", Kind: predictionKindPrefix + KindMorning, State: NotifySent,
		SentAt: now, CreatedAt: now.Add(-time.Minute), Alerts: []Alert{forecast}}}
	acknowledgeDeliveredClaims(state, state.Outbox[0])
	if !state.PendingItems[0].NotifiedAt.IsZero() {
		t.Fatalf("a delivered forecast acknowledged a quota incident: %+v", state.PendingItems[0])
	}
	if hasDeliveredClaim(state, exhaustion) {
		t.Fatal("a delivered forecast protected a quota incident")
	}

	quota := forecast
	quota.Kind = AlertQuotaLow
	state.Outbox[0].Alerts = []Alert{quota}
	acknowledgeDeliveredClaims(state, state.Outbox[0])
	if !state.PendingItems[0].NotifiedAt.Equal(now) {
		t.Fatalf("a delivered quota alert did not acknowledge its incident: %+v", state.PendingItems[0])
	}
	state.PendingItems[0].NotifiedAt = now.Add(-time.Second)
	if !hasDeliveredClaim(state, state.PendingItems[0]) {
		t.Fatal("a delivered quota alert stopped protecting the same incident")
	}
}
