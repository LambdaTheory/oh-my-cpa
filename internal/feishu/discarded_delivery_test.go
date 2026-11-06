package feishu

import (
	"context"
	"testing"
	"time"
)

func TestDiscardedQuietAlertAllowsOneFreshDeliveryWithoutResolvingOwnership(t *testing.T) {
	env := newTestEnv(t, Report{}, true)
	*env.clock = mustTime("2026-11-05T23:30:00Z")
	env.source.report = invalidReport(*env.clock)
	env.service.tick(context.Background())
	state, _ := env.store.Load(context.Background())
	identity := ItemID(AlertCredential, "codex", "a1", "")
	original, exists := state.pendingByID(identity)
	if !exists || original.NotifiedAt.IsZero() || len(state.Outbox) != 1 || state.Outbox[0].State != NotifyQueued {
		t.Fatalf("initial quiet reservation missing: %+v", state)
	}

	*env.clock = mustTime("2026-11-06T08:00:00Z")
	env.service.tick(context.Background())
	state, _ = env.store.Load(context.Background())
	item, exists := state.pendingByID(identity)
	if sends, _ := env.sender.counts(); sends != 0 || !exists || item.Status != PendingOpen || !item.ResolvedAt.IsZero() || !item.Since.Equal(original.Since) || !item.NotifiedAt.IsZero() {
		t.Fatalf("discard acknowledged or resolved an undelivered incident: sends=%d state=%+v", sends, state)
	}
	if len(state.Outbox) != 1 || !isDiscardedNotification(state.Outbox[0]) || !state.Outbox[0].NextAttemptAt.IsZero() {
		t.Fatalf("discard must close without delivery or retry cooldown: %+v", state.Outbox)
	}

	// Only persisted state, not an in-memory retry flag, may restore eligibility.
	restartMonitorService(env)
	*env.clock = mustTime("2026-11-06T09:00:00Z")
	env.source.report = invalidReport(*env.clock)
	env.service.tick(context.Background())
	state, _ = env.store.Load(context.Background())
	item, _ = state.pendingByID(identity)
	if sends, _ := env.sender.counts(); sends != 1 || !item.NotifiedAt.Equal(*env.clock) || item.Status != PendingOpen || !item.Since.Equal(original.Since) || !item.ResolvedAt.IsZero() {
		t.Fatalf("fresh evidence did not get one delivery on the retained incident: sends=%d state=%+v", sends, state)
	}
	*env.clock = env.clock.Add(time.Minute)
	env.source.report = invalidReport(*env.clock)
	env.service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("a delivered incident replayed: %d", sends)
	}
}

func TestDiscardedDeliveryNeverReleasesAnActuallyDeliveredIncident(t *testing.T) {
	env := newTestEnv(t, Report{}, true)
	*env.clock = mustTime("2026-11-06T12:00:00Z")
	env.source.report = invalidReport(*env.clock)
	env.service.tick(context.Background())
	state, _ := env.store.Load(context.Background())
	original := state.PendingItems[0]
	if sends, _ := env.sender.counts(); sends != 1 || len(state.Outbox) != 1 || state.Outbox[0].SentAt.IsZero() {
		t.Fatalf("initial actual delivery missing: sends=%d state=%+v", sends, state)
	}
	*env.clock = env.clock.Add(16 * time.Minute)
	env.service.tick(context.Background())
	*env.clock = env.clock.Add(time.Minute)
	env.source.report = invalidReport(*env.clock)
	env.service.tick(context.Background())
	state, _ = env.store.Load(context.Background())
	item, _ := state.pendingByID(original.ID)
	if sends, _ := env.sender.counts(); sends != 1 || !item.NotifiedAt.Equal(original.NotifiedAt) || !item.Since.Equal(original.Since) || item.Status != PendingOpen {
		t.Fatalf("age expiry or a newer rejection replayed actual delivery: sends=%d state=%+v", sends, state)
	}
}

func TestDiscardedBatchReleasesOnlyItsUnsentCoveredClaims(t *testing.T) {
	env := newTestEnv(t, Report{}, true)
	*env.clock = mustTime("2026-11-06T09:00:00Z")
	opened := env.clock.Add(-time.Hour)
	env.source.report = invalidReportWith(opened, "delivered", "waiting", "uncovered")
	state := EmptyState()
	state.Bootstrapped = true
	alerts := []Alert{}
	for _, key := range []string{"delivered", "waiting", "uncovered"} {
		id := ItemID(AlertCredential, "codex", key, "")
		state.PendingItems = append(state.PendingItems, PendingItem{ID: id, Kind: AlertCredential, Provider: "codex", Key: key, Alias: key,
			Status: PendingOpen, Since: opened, LastSeen: opened, NotifiedAt: opened})
		alerts = append(alerts, Alert{Kind: AlertCredential, Provider: "codex", Key: key, ObservedAt: opened, OccurredAt: opened,
			EvidenceSource: EvidenceAuthentication, FreshWindowMS: defaultFreshWindow.Milliseconds()})
	}
	state.Outbox = []Notification{
		{ID: "actual", Kind: AlertCredential, State: NotifySent, SentAt: opened.Add(time.Minute), CreatedAt: opened,
			ItemIDs: []string{alertItemID(alerts[0])}, Alerts: alerts[:1]},
		{ID: "drop", Kind: KindAlertBatch, State: NotifyPending, CreatedAt: opened, OccurredAt: opened,
			ItemIDs: []string{alertItemID(alerts[0]), alertItemID(alerts[1])}, Alerts: alerts[:2]},
	}
	if err := env.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	env.service.tick(context.Background())
	state, _ = env.store.Load(context.Background())
	for _, key := range []string{"delivered", "waiting", "uncovered"} {
		item, _ := state.pendingByID(ItemID(AlertCredential, "codex", key, ""))
		if item.NotifiedAt.IsZero() != (key == "waiting") || item.Status != PendingOpen || !item.Since.Equal(opened) || !item.ResolvedAt.IsZero() {
			t.Fatalf("discard released the wrong claim %s: %+v", key, state.PendingItems)
		}
	}
	*env.clock = env.clock.Add(time.Minute)
	env.source.report = invalidReportWith(*env.clock, "delivered", "waiting", "uncovered")
	env.service.tick(context.Background())
	state, _ = env.store.Load(context.Background())
	last := state.Outbox[len(state.Outbox)-1]
	if sends, _ := env.sender.counts(); sends != 1 || len(last.Alerts) != 1 || last.Alerts[0].Key != "waiting" || last.SentAt.IsZero() {
		t.Fatalf("claim release did not preserve delivery/scope boundaries: sends=%d outbox=%+v", sends, state.Outbox)
	}
}

func TestDiscardedRowsDoNotClaimRecentOrStartupAnnouncements(t *testing.T) {
	now := mustTime("2026-11-06T09:50:00Z")
	alert := Alert{Kind: AlertCredential, Provider: "codex", Key: "synthetic", ObservedAt: now, OccurredAt: now,
		EvidenceSource: EvidenceAuthentication, FreshWindowMS: defaultFreshWindow.Milliseconds()}
	item := PendingItem{ID: alertItemID(alert), Kind: AlertCredential, Provider: "codex", Key: alert.Key, Status: PendingOpen, Since: now.Add(-time.Hour)}
	state := EmptyState()
	state.PendingItems = []PendingItem{item}
	state.Outbox = []Notification{{ID: "discarded-summary", Kind: KindSummary, State: NotifySent, CreatedAt: now, Alerts: []Alert{alert}, Pending: []PendingItem{item}}}
	if announcedInMinute(state, item.ID, "later", now) {
		t.Fatal("an unsent closure consumed the same minute's valid alert")
	}
	later := Notification{ID: "later", Kind: KindMorning, CreatedAt: now.Add(10 * time.Minute), ItemIDs: []string{item.ID}, Alerts: []Alert{alert}, Pending: []PendingItem{item}}
	omitStartupEpisodes(&later, state, readyPredictionConfig(), later.CreatedAt)
	if len(later.Alerts) != 1 || len(later.Pending) != 1 || len(later.ItemIDs) != 1 {
		t.Fatalf("an unsent startup row consumed a later valid morning: %+v", later)
	}
	state.Outbox[0].SentAt = now
	if !announcedInMinute(state, item.ID, "later", now) {
		t.Fatal("actual delivery lost same-minute deduplication")
	}
	omitStartupEpisodes(&later, state, readyPredictionConfig(), later.CreatedAt)
	if len(later.Pending) != 0 || len(later.ItemIDs) != 0 {
		t.Fatalf("actual startup delivery lost its episode deduplication: %+v", later)
	}
}

func TestDiscardedMorningDoesNotConsumeTheSummarySlot(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	*env.clock = mustTime("2026-11-06T10:00:00Z")
	current := PredictionDigest{Items: []PredictionDigestItem{{Provider: "codex", Key: "synthetic", LastsUntilReset: &cannotLast,
		ObservedAtMS: env.clock.UnixMilli(), FreshWindowMS: (10 * time.Second).Milliseconds()}}}
	job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) { return current, nil })
	job.Activate()
	state := EmptyState()
	state.Bootstrapped = true
	if !job.queueFindings(state, env.service.resolvedNow(), *env.clock, current) {
		t.Fatal("fresh synthetic morning was not queued")
	}
	if err := env.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	*env.clock = env.clock.Add(30 * time.Second)
	env.service.deliverOutbox(context.Background(), env.service.resolvedNow())
	state, _ = env.store.Load(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 || !state.LastSummaryAt.IsZero() || !isDiscardedNotification(state.Outbox[0]) {
		t.Fatalf("an unsent morning claimed delivery or schedule cooldown: sends=%d state=%+v", sends, state)
	}
	*env.clock = env.clock.Add(15 * time.Second)
	current.Items[0].ObservedAtMS = env.clock.UnixMilli()
	job.tick(context.Background())
	state, _ = env.store.Load(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 || !state.LastSummaryAt.Equal(*env.clock) {
		t.Fatalf("fresh evidence could not use the released slot: sends=%d state=%+v", sends, state)
	}
	job.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("actual morning delivery repeated: %d", sends)
	}
}

func TestDiscardedClaimReleaseUsesPersistedPrecisionWithoutClearingNewReservations(t *testing.T) {
	at := time.Date(2026, 11, 6, 12, 0, 0, 123456789, time.UTC)
	for _, newer := range []bool{false, true} {
		stored := time.UnixMilli(at.UnixMilli()).UTC()
		if newer {
			stored = stored.Add(time.Millisecond)
		}
		alert := Alert{Kind: AlertCredential, Provider: "codex", Key: "synthetic", OccurredAt: at}
		state := EmptyState()
		state.PendingItems = []PendingItem{{ID: alertItemID(alert), Kind: AlertCredential, Provider: "codex", Key: alert.Key,
			Status: PendingOpen, Since: at.Add(-time.Hour), NotifiedAt: stored}}
		before := Notification{ID: "discarded", Kind: AlertCredential, State: NotifyPending, CreatedAt: at, OccurredAt: at, Alerts: []Alert{alert}}
		outcome := before
		discardNotification(&outcome)
		state.Outbox = []Notification{outcome}
		releaseDiscardedClaims(state, before, outcome)
		if state.PendingItems[0].NotifiedAt.IsZero() == newer || state.PendingItems[0].Status != PendingOpen || !state.PendingItems[0].ResolvedAt.IsZero() {
			t.Fatalf("newer=%v discard lost timestamp precision or cleared a new claim: %+v", newer, state.PendingItems)
		}
	}
}

func TestDiscardedSummaryCannotClearAnotherPendingOrDeliveredSlotOwner(t *testing.T) {
	now := mustTime("2026-11-06T10:00:00Z")
	state := EmptyState()
	state.LastSummaryAt = now
	before := Notification{ID: "discarded", Kind: KindMorning, State: NotifyPending, CreatedAt: now, OccurredAt: now}
	outcome := before
	discardNotification(&outcome)
	state.Outbox = []Notification{outcome, {ID: "other", Kind: predictionKindPrefix + KindMorning, State: NotifyPending, CreatedAt: now}}
	for _, delivered := range []bool{false, true} {
		if delivered {
			state.Outbox[1].State = NotifySent
			state.Outbox[1].SentAt = now
		}
		releaseDiscardedClaims(state, before, outcome)
		if !state.LastSummaryAt.Equal(now) {
			t.Fatalf("discard cleared another pending/actual schedule claim: delivered=%v state=%+v", delivered, state)
		}
	}
	discardNotification(&state.Outbox[1])
	releaseDiscardedClaims(state, before, outcome)
	if !state.LastSummaryAt.IsZero() {
		t.Fatalf("all discarded rows retained the scheduled-minute cooldown: %+v", state)
	}
}

func TestPendingDeliveryDoesNotDuplicateRiskOrBorrowAnotherKindReservation(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	reset := now.Add(time.Hour)
	report := Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{
		Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(100, &reset)},
	}}}}}
	cfg := policyCfg()
	cfg.EnableAttention = true
	first := evaluatePolicy(cfg, PolicyInput{Report: report, Now: now})
	if len(first.Alerts) != 1 || first.Alerts[0].Kind != AlertQuotaExhausted {
		t.Fatalf("synthetic exhaustion must start one incident: %+v", first)
	}
	alert := first.Alerts[0]
	id := alertItemID(alert)
	for _, scenario := range []struct {
		name, kind, state string
		wantAlerts        int
	}{
		{"pending", AlertQuotaExhausted, NotifyPending, 0},
		{"quiet_queue", AlertQuotaExhausted, NotifyQueued, 0},
		{"discarded", AlertQuotaExhausted, NotifySent, 1},
		{"forecast", predictionKindPrefix + KindMorning, NotifyPending, 1},
		{"historical_summary", KindSummary, NotifyPending, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			prev := append([]PendingItem(nil), first.Items...)
			for index := range prev {
				prev[index].NotifiedAt = time.Time{}
			}
			row := Notification{Kind: scenario.kind, State: scenario.state, ItemIDs: []string{id}, OccurredAt: now, CreatedAt: now}
			switch scenario.name {
			case "forecast":
				forecast := alert
				forecast.Kind = predictionKindPrefix
				row.Alerts = []Alert{forecast}
			case "historical_summary":
				row.Alerts = []Alert{unknownDataAlert(now)}
			default:
				row.Alerts = []Alert{alert}
			}
			result := evaluatePolicy(cfg, PolicyInput{Report: report, Prev: prev, Now: now.Add(time.Minute), InFlight: pendingDeliveryClaims([]Notification{row})})
			if len(result.Alerts) != scenario.wantAlerts || len(result.Items) != len(prev) || !result.Items[0].Since.Equal(prev[0].Since) {
				t.Fatalf("wrong admission or changed ownership: %+v", result)
			}
		})
	}
}

func TestActualDeliveryFillsOnlyItsCoveredLegacyClaim(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	alert := Alert{Kind: AlertCredential, Provider: "codex", Key: "synthetic", ObservedAt: now, OccurredAt: now}
	for _, scenario := range []struct {
		name    string
		since   time.Time
		sentAt  time.Time
		wantAck bool
	}{
		{"actual", now, now.Add(2 * time.Minute), true},
		{"discarded", now, time.Time{}, false},
		{"newer_incident", now.Add(time.Minute), now.Add(2 * time.Minute), false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			state := EmptyState()
			state.PendingItems = []PendingItem{
				{ID: alertItemID(alert), Kind: AlertCredential, Status: PendingOpen, Since: scenario.since},
				{ID: "uncovered", Kind: AlertCredential, Status: PendingOpen, Since: now},
			}
			outcome := Notification{Kind: KindAlertBatch, State: NotifySent, SentAt: scenario.sentAt, Alerts: []Alert{alert}, ItemIDs: []string{"uncovered"}}
			acknowledgeDeliveredClaims(state, outcome)
			if state.PendingItems[0].NotifiedAt.IsZero() == scenario.wantAck || !state.PendingItems[1].NotifiedAt.IsZero() {
				t.Fatalf("actual acknowledgement escaped its delivered episode: %+v", state.PendingItems)
			}
		})
	}
}

func TestDiscardedRetentionUsesCreationWithoutExpiringPendingRows(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-sentRetention - time.Millisecond)
	state := EmptyState()
	state.Outbox = []Notification{
		{ID: "discarded", State: NotifySent, CreatedAt: old},
		{ID: "actual", State: NotifySent, CreatedAt: old, SentAt: now.Add(-time.Minute)},
		{ID: "pending", State: NotifyPending, CreatedAt: old},
	}
	state.prune(now)
	if len(state.Outbox) != 2 || state.Outbox[0].ID != "actual" || state.Outbox[1].ID != "pending" {
		t.Fatalf("closed-row retention dropped delivery progress or kept discarded rows: %+v", state.Outbox)
	}
}
