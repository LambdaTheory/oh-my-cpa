package feishu

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDeliveryStoredRereadCannotExtendTheSameObservation(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	observed := now.Add(-2 * time.Minute)
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: observed, Windows: []Window{accountWindow(95, nil)}}}}}}
	alert := attentionSignals(report, 10, now)[0]
	alert.FreshWindowMS = time.Minute.Milliseconds()
	notification := Notification{Kind: AlertQuotaLow, Alerts: []Alert{alert}, ItemIDs: []string{alertItemID(alert)}}
	if got := eligibleNotificationAlerts(notification, &report, nil, policyCfg(), now); len(got) != 0 {
		t.Fatalf("same stored observation extended a narrower bound: %+v", got)
	}
	report.Providers[0].Accounts[0].ObservedAt = now
	if got := eligibleNotificationAlerts(notification, &report, nil, policyCfg(), now); len(got) != 1 || !got[0].ObservedAt.Equal(now) {
		t.Fatalf("genuine new observation cannot requalify the episode: %+v", got)
	}
}

func TestDeliveryQuietEndRequiresCurrentEvidenceAndKeepsHistoricalOwnership(t *testing.T) {
	for _, renewed := range []bool{false, true} {
		t.Run(fmt.Sprint(renewed), func(t *testing.T) {
			env := newTestEnv(t, Report{}, true)
			*env.clock = mustTime("2026-11-05T23:30:00Z")
			env.source.report = invalidReport(*env.clock)
			env.service.tick(context.Background())
			*env.clock = mustTime("2026-11-06T09:00:00Z")
			if renewed {
				env.source.report = invalidReport(*env.clock)
			}
			env.service.tick(context.Background())
			want := 0
			if renewed {
				want = 1
			}
			state, _ := env.store.Load(context.Background())
			if sends, _ := env.sender.counts(); sends != want || len(openItems(state.PendingItems)) != 1 {
				t.Fatalf("quiet end sends=%d want=%d, ownership=%+v", sends, want, state.PendingItems)
			}
			for _, row := range state.Outbox {
				if !renewed && (row.State != NotifySuppressed || !row.SentAt.IsZero()) {
					t.Fatalf("expired episode was acknowledged: %+v", row)
				}
			}
		})
	}
}

func TestDeliveryMorningPathsShareIndependentEvidenceAcrossDays(t *testing.T) {
	for _, forecast := range []bool{false, true} {
		t.Run(fmt.Sprint(forecast), func(t *testing.T) {
			env := newTestEnv(t, Report{}, true)
			*env.clock = mustTime("2026-11-06T10:00:00Z")
			old := env.clock.Add(-17 * 24 * time.Hour)
			accounts := []AccountReport{}
			state := EmptyState()
			state.Bootstrapped = true
			for index := 0; index < 3; index++ {
				key := fmt.Sprintf("historical-%d", index)
				accounts = append(accounts, AccountReport{Key: key, Label: key, State: StateStale, AuthFailed: true, AuthSignalObservedAt: old, ObservedAt: old, Windows: []Window{accountWindow(100, nil)}})
				state.PendingItems = append(state.PendingItems, PendingItem{ID: ItemID(AlertCredential, "codex", key, ""), Kind: AlertCredential, Provider: "codex", Key: key, Since: old, Status: PendingOpen})
			}
			if err := env.store.Save(context.Background(), state); err != nil {
				t.Fatal(err)
			}
			env.source.report = Report{Providers: []ProviderReport{{Provider: "codex", Accounts: accounts}}}
			job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) {
				if !forecast {
					return PredictionDigest{}, nil
				}
				return PredictionDigest{Items: []PredictionDigestItem{{Key: "fresh-forecast", Provider: "claude", Verdict: "will_exhaust", ObservedAtMS: env.clock.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
			})
			job.Activate()
			for day := 0; day < 2; day++ {
				job.tick(context.Background())
				state, _ = env.store.Load(context.Background())
				if sends, _ := env.sender.counts(); sends != day+1 {
					t.Fatalf("daily morning sends=%d want=%d", sends, day+1)
				}
				row := state.Outbox[len(state.Outbox)-1]
				predictions := 0
				for _, alert := range row.Alerts {
					if alert.Kind == AlertCredential || alert.Kind == AlertReadingMissing || alert.Kind == AlertQuotaExhausted {
						t.Fatalf("historical ownership became current morning risk: %+v", row)
					}
					if strings.HasPrefix(alert.Kind, predictionKindPrefix) {
						predictions++
						if alert.FreshWindowMS != (2*time.Hour).Milliseconds() || !alert.ObservedAt.Equal(*env.clock) {
							t.Fatalf("prediction lost its evidence bound: %+v", alert)
						}
					}
				}
				if forecast && predictions != 1 || len(openItems(state.PendingItems)) != 3 {
					t.Fatalf("current forecast or historical ownership lost: %+v", state)
				}
				*env.clock = env.clock.Add(24 * time.Hour)
			}
		})
	}
}

func TestDeliverySuppressionPersistsWithoutRevivalOrAcknowledgement(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	store := NewMemoryStore()
	state := EmptyState()
	row := Notification{ID: "suppressed", Kind: AlertCredential, State: NotifySuppressed, CreatedAt: now}
	state.Outbox = []Notification{row}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := store.Load(context.Background())
	row.State = NotifyPending
	reloaded.queueNotification(row)
	if len(reloaded.Outbox) != 1 || reloaded.Outbox[0].State != NotifySuppressed || !reloaded.Outbox[0].SentAt.IsZero() {
		t.Fatalf("suppressed row revived or acknowledged: %+v", reloaded)
	}
	reloaded.prune(now.Add(sentRetention + time.Millisecond))
	if len(reloaded.Outbox) != 0 {
		t.Fatalf("terminal suppression was retained indefinitely: %+v", reloaded)
	}
}

func TestDeliveryChannelAgeExpiryDoesNotResolveHistoricalIncident(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	reset := now.Add(2 * time.Hour)
	report := Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(100, &reset)}}}}}}
	opened := evaluatePolicy(policyCfg(), PolicyInput{Report: report, Now: now})
	if !hasItem(opened.Items, AlertChannelUnavailable) {
		t.Fatalf("current channel incident was not opened: %+v", opened)
	}
	later := now.Add(defaultFreshWindow + time.Millisecond)
	retained := evaluatePolicy(policyCfg(), PolicyInput{Report: report, Prev: opened.Items, Now: later})
	if len(retained.Resolved) != 0 || !hasItem(retained.Items, AlertChannelUnavailable) {
		t.Fatalf("age expiry falsely resolved incident: %+v", retained)
	}
	report.Providers[0].Accounts[0].ObservedAt = later
	report.Providers[0].Accounts[0].Windows = []Window{accountWindow(20, nil)}
	recovered := evaluatePolicy(policyCfg(), PolicyInput{Report: report, Prev: retained.Items, Now: later})
	if len(recovered.Resolved) != 1 {
		t.Fatalf("positive recovery was not recognized: %+v", recovered)
	}
}

func TestDeliveryEvidenceRejectsMissingAndExpiredBoundsForEveryRisk(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	for _, kind := range []string{AlertCredential, AlertQuotaLow, AlertQuotaExhausted, AlertReadingMissing, predictionKindPrefix} {
		for _, missing := range []string{"time", "bound", "expired"} {
			t.Run(kind+"/"+missing, func(t *testing.T) {
				alert := Alert{Kind: kind, Provider: "codex", Key: "synthetic", ObservedAt: now, FreshWindowMS: time.Minute.Milliseconds(), EvidenceSource: EvidenceQuota}
				if kind == AlertCredential {
					alert.EvidenceSource = EvidenceAuthentication
				}
				switch missing {
				case "time":
					alert.ObservedAt = time.Time{}
				case "bound":
					alert.FreshWindowMS = 0
				case "expired":
					alert.ObservedAt = now.Add(-time.Minute - time.Millisecond)
				}
				sender := newFakeSender()
				dispatcher := NewDispatcher(sender, NewPlainReportRenderer(time.UTC), nil).withClock(func() time.Time { return now })
				rows := dispatcher.deliverDue(context.Background(), "ou_synthetic", []Notification{{ID: "synthetic", Kind: predictionKindPrefix + KindMorning, Alerts: []Alert{alert}, State: NotifyPending, CreatedAt: now}})
				if sends, _ := sender.counts(); sends != 0 || rows[0].State != NotifySuppressed || !rows[0].SentAt.IsZero() {
					t.Fatalf("unknown/expired evidence delivered or falsely acknowledged: %+v", rows)
				}
			})
		}
	}
}

func TestDeliveryRebuildsLegacyEvidenceOnlyFromStoredAccount(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	for _, age := range []time.Duration{time.Minute, 17 * 24 * time.Hour} {
		t.Run(age.String(), func(t *testing.T) {
			sender := newFakeSender()
			dispatcher := NewDispatcher(sender, NewPlainReportRenderer(time.UTC), nil).withClock(func() time.Time { return now })
			dispatcher.source = &fakeReportSource{has: true, report: Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", AuthFailed: true, State: StateStale, AuthSignalObservedAt: now.Add(-age)}}}}}}
			rows := dispatcher.deliverDue(context.Background(), "ou_synthetic", []Notification{{ID: "legacy", Kind: AlertCredential, State: NotifyPending, CreatedAt: now, Alerts: []Alert{{Kind: AlertCredential, Provider: "codex", Key: "synthetic"}}}})
			want := 0
			if age == time.Minute {
				want = 1
			}
			if sends, _ := sender.counts(); sends != want {
				t.Fatalf("legacy send count=%d want=%d rows=%+v", sends, want, rows)
			}
		})
	}
}

func TestDeliveryRetryDoesNotRefreshEvidenceFromNotificationTime(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	sender := newFakeSender()
	sender.failSends = 2
	dispatcher := NewDispatcher(sender, NewPlainReportRenderer(time.UTC), nil).withClock(func() time.Time { return now })
	alert := Alert{Kind: predictionKindPrefix, Provider: "codex", Key: "synthetic", EvidenceSource: EvidenceQuota, ObservedAt: now, FreshWindowMS: time.Minute.Milliseconds()}
	rows := dispatcher.deliverDue(context.Background(), "ou_synthetic", []Notification{{ID: "retry", Kind: predictionKindPrefix + KindMorning, Alerts: []Alert{alert}, State: NotifyPending, CreatedAt: now}})
	if rows[0].State != NotifyPending || rows[0].Attempts != 1 {
		t.Fatalf("initial failure did not persist: %+v", rows)
	}
	now = now.Add(2 * time.Minute)
	rows = dispatcher.deliverDue(context.Background(), "ou_synthetic", rows)
	if sends, _ := sender.counts(); sends != 0 || rows[0].State != NotifySuppressed || rows[0].Attempts != 1 {
		t.Fatalf("retry renewed expired evidence: %+v", rows)
	}
}

type evidenceCrossingSender struct {
	*fakeSender
	clock *time.Time
	calls int
}

func (s *evidenceCrossingSender) SendCard(context.Context, string, map[string]any) error {
	s.calls++
	*s.clock = s.clock.Add(2 * time.Minute)
	return errors.New("synthetic delayed rejection")
}

func TestDeliveryRechecksBeforeSimplifiedFallback(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	sender := &evidenceCrossingSender{fakeSender: newFakeSender(), clock: &now}
	dispatcher := NewDispatcher(sender, NewPlainReportRenderer(time.UTC), nil).withClock(func() time.Time { return now })
	alert := Alert{Kind: predictionKindPrefix, EvidenceSource: EvidenceQuota, ObservedAt: now, FreshWindowMS: time.Minute.Milliseconds()}
	rows := dispatcher.deliverDue(context.Background(), "ou_synthetic", []Notification{{ID: "fallback", Kind: predictionKindPrefix + KindMorning, Alerts: []Alert{alert}, State: NotifyPending}})
	if sender.calls != 1 || rows[0].State != NotifySuppressed || !rows[0].SentAt.IsZero() {
		t.Fatalf("simplified send used expired evidence: calls=%d rows=%+v", sender.calls, rows)
	}
}

func TestDeliveryRetainsOngoingMonitorPolicyWithoutRefreshingItsLastSuccess(t *testing.T) {
	now := mustTime("2026-11-06T02:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	sender := newFakeSender()
	dispatcher := NewDispatcher(sender, NewPlainReportRenderer(time.UTC), nil).withClock(func() time.Time { return now })
	dispatcher.monitorDownAfter = 30 * time.Minute
	dispatcher.source = &fakeReportSource{has: true, report: Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", State: StateStale, LastSuccessAt: old, AttemptedAt: old, CollectionFailed: true}}}}}}
	store := NewMemoryStore()
	state := EmptyState()
	state.ChannelTracks["codex"] = ChannelTrack{FirstSeen: old, LastValid: old, ConsecutiveFailures: 3, LastAttempt: old}
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	dispatcher.store = store
	rows := dispatcher.deliverDue(context.Background(), "ou_synthetic", []Notification{{ID: "monitor", Kind: AlertMonitorDown, State: NotifyPending, Alerts: []Alert{{Kind: AlertMonitorDown, Provider: "codex"}}}})
	if sends, _ := sender.counts(); sends != 1 || rows[0].State != NotifySent || !rows[0].Alerts[0].ObservedAt.Equal(old) {
		t.Fatalf("ongoing outage was dropped or restamped: %+v", rows)
	}
	account := &dispatcher.source.(*fakeReportSource).report.Providers[0].Accounts[0]
	account.OK, account.CollectionFailed, account.State = true, false, StateHealthy
	account.ObservedAt = now
	rows[0].ID, rows[0].State = "recovered", NotifyPending
	rows = dispatcher.deliverDue(context.Background(), "ou_synthetic", rows)
	if sends, _ := sender.counts(); sends != 1 || rows[0].State != NotifySuppressed {
		t.Fatalf("recovered monitor incident still delivered: %+v", rows)
	}
}
