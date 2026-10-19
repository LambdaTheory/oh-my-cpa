package feishu

import (
	"context"
	"strings"
	"testing"
	"time"
)

func readyPredictionConfig() Resolved {
	return resolveConfig(Config{
		Enabled: true, AppID: "app", ChatID: "chat", SummaryTimes: []string{"10:00", "17:00"}, Timezone: "UTC",
	}, Overlay{AppSecret: stringPointer("secret")})
}

func stringPointer(value string) *string { return &value }

func TestPredictionDigestQueuesOnceAndSuppressesSummary(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	used, projected := 60.0, 120.0
	reads := 0
	job := &PredictionJob{service: NewService(Options{Web: Config{Enabled: true}, Overlay: Overlay{AppSecret: stringPointer("secret")}}), source: func(context.Context) ([]PredictionDigestItem, error) {
		reads++
		return []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}, nil
	}}
	state := EmptyState()
	resolved := readyPredictionConfig()
	if !job.maybeQueue(context.Background(), state, resolved, now) {
		t.Fatal("prediction was not queued")
	}
	if reads != 1 || len(state.Outbox) != 1 || !sameSummaryMinute(state.LastSummaryAt, resolved, now) {
		t.Fatalf("state = %+v reads=%d", state, reads)
	}
	if job.maybeQueue(context.Background(), state, resolved, now) || reads != 1 || len(state.Outbox) != 1 {
		t.Fatal("same minute queued twice")
	}
}

func TestPredictionDigestLeavesSummaryWhenNoFinding(t *testing.T) {
	now := time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)
	job := &PredictionJob{service: NewService(Options{Web: Config{Enabled: true}, Overlay: Overlay{AppSecret: stringPointer("secret")}}), source: func(context.Context) ([]PredictionDigestItem, error) {
		return []PredictionDigestItem{{Verdict: "on_track"}}, nil
	}}
	state := EmptyState()
	if job.maybeQueue(context.Background(), state, readyPredictionConfig(), now) || len(state.Outbox) != 0 || !state.LastSummaryAt.IsZero() {
		t.Fatalf("unexpected prediction state = %+v", state)
	}
}

func TestPredictionDigestDoesNotRunOutsideReadyService(t *testing.T) {
	reads := 0
	job := &PredictionJob{service: NewService(Options{}), source: func(context.Context) ([]PredictionDigestItem, error) { reads++; return nil, nil }}
	state := EmptyState()
	disabled := resolveConfig(Config{Enabled: false, SummaryTimes: []string{"10:00"}, Timezone: "UTC"}, Overlay{})
	if job.maybeQueue(context.Background(), state, disabled, time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("disabled job queued")
	}
	if reads != 0 {
		t.Fatalf("direct job reads = %d", reads)
	}
}

func TestPredictionDigestUsesOutboxAndSuppressesScheduledSummary(t *testing.T) {
	web, overlay := readyConfig()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	sender := newFakeSender()
	store := NewMemoryStore()
	used, projected := 60.0, 120.0
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store,
		Source: &fakeReportSource{report: Report{GeneratedAt: now, Providers: []ProviderReport{{
			Provider: "claude", Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(95, nil)}}},
		}}}, has: true},
		Renderer: NewPlainReportRenderer(time.UTC), Now: func() time.Time { return now },
		SenderFactory: func(Resolved) Sender { return sender }, SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state.Bootstrapped = true
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	job := NewPredictionJob(service, func(context.Context) ([]PredictionDigestItem, error) {
		return []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}, nil
	})
	job.tick(context.Background())
	if sends, _ := sender.counts(); sends != 1 {
		t.Fatalf("prediction minute sends = %d", sends)
	}
	state, err = store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	predictions, summaries := 0, 0
	for _, notification := range state.Outbox {
		if strings.HasPrefix(notification.Kind, predictionKindPrefix) {
			predictions++
		}
		if notification.Kind == KindSummary {
			summaries++
		}
	}
	if predictions != 1 || summaries != 0 {
		t.Fatalf("outbox predictions=%d summaries=%d: %+v", predictions, summaries, state.Outbox)
	}
}

func TestPredictionDigestSourceIsNotReadByInactiveService(t *testing.T) {
	reads := 0
	service := NewService(Options{
		Web: Config{Enabled: false, SummaryTimes: []string{"10:00"}, Timezone: "UTC"},
		Now: func() time.Time { return time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC) },
	})
	job := NewPredictionJob(service, func(context.Context) ([]PredictionDigestItem, error) { reads++; return nil, nil })
	job.tick(context.Background())
	if reads != 0 {
		t.Fatalf("inactive prediction reads = %d", reads)
	}
}

func TestPredictionJobClaimsScheduleAndRunsImmediately(t *testing.T) {
	web, overlay := readyConfig()
	now := time.Date(2026, 10, 19, 10, 0, 30, 0, time.UTC)
	store := NewMemoryStore()
	used, projected := 60.0, 120.0
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Now: func() time.Time { return now },
	})
	ctx, cancel := context.WithCancel(context.Background())
	job := NewPredictionJob(service, func(context.Context) ([]PredictionDigestItem, error) {
		cancel()
		return []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}, nil
	})
	job.Activate()
	if !service.isPredictionSchedulerActive {
		t.Fatal("prediction job did not claim the scheduled summary")
	}
	if err := job.Run(ctx); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || !strings.HasPrefix(state.Outbox[0].Kind, predictionKindPrefix) {
		t.Fatalf("immediate prediction outbox = %+v", state.Outbox)
	}
	if service.isPredictionSchedulerActive {
		t.Fatal("prediction job retained schedule ownership after stopping")
	}
}
