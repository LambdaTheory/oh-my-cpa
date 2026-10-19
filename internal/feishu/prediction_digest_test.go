package feishu

import (
	"context"
	"encoding/json"
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
	sender := newFakeSender()
	used, projected := 60.0, 120.0
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Now: func() time.Time { return now },
		Source: &fakeReportSource{}, SenderFactory: func(Resolved) Sender { return sender },
		SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	ctx, cancel := context.WithCancel(context.Background())
	job := NewPredictionJob(service, func(context.Context) ([]PredictionDigestItem, error) {
		cancel()
		return []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}, nil
	})
	job.Activate()
	if !service.predictionSchedulerActive.Load() {
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
	if service.predictionSchedulerActive.Load() {
		t.Fatal("prediction job retained schedule ownership after stopping")
	}
	if sends, _ := sender.counts(); sends != 1 {
		t.Fatalf("prediction sends = %d", sends)
	}
	if payload := sender.lastCard(); payload == nil || !strings.Contains(marshalTestJSON(payload), "OMC 额度预测") {
		t.Fatalf("prediction payload = %+v", payload)
	}
}

func TestPredictionJobBecomesReadyAfterReload(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	sender := newFakeSender()
	service := NewService(Options{
		Web:   Config{Enabled: false, SummaryTimes: []string{"10:00"}, Timezone: "UTC"},
		Store: NewMemoryStore(), Source: &fakeReportSource{}, Now: func() time.Time { return now },
		SenderFactory: func(Resolved) Sender { return sender }, SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	used, projected := 60.0, 120.0
	reads := make(chan struct{}, 1)
	job := NewPredictionJob(service, func(context.Context) ([]PredictionDigestItem, error) {
		reads <- struct{}{}
		return []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}, nil
	})
	job.tick(context.Background())
	if len(reads) != 0 {
		t.Fatal("disabled job read its source")
	}
	job.interval = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- job.Run(ctx) }()
	web, overlay := readyConfig()
	service.Reload(context.Background(), web, overlay)
	select {
	case <-reads:
	case <-time.After(time.Second):
		t.Fatal("prediction did not become active after reload")
	}
	deadline := time.After(time.Second)
	for {
		if sends, _ := sender.counts(); sends == 1 {
			break
		}
		select {
		case <-sender.signal:
		case <-deadline:
			t.Fatal("prediction was not sent after reload")
		}
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPredictionMinuteSuppressesConcurrentStartupDigest(t *testing.T) {
	web, overlay := readyConfig()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	sender := newFakeSender()
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: NewMemoryStore(),
		Source: &fakeReportSource{report: Report{GeneratedAt: now, Providers: []ProviderReport{{
			Provider: "claude", Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true, ObservedAt: now, Windows: []Window{accountWindow(95, nil)}}},
		}}}, has: true},
		Renderer: NewPlainReportRenderer(time.UTC), Now: func() time.Time { return now },
		SenderFactory: func(Resolved) Sender { return sender }, SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	started := make(chan struct{})
	release := make(chan struct{})
	used, projected := 60.0, 120.0
	job := NewPredictionJob(service, func(context.Context) ([]PredictionDigestItem, error) {
		close(started)
		<-release
		return []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}, nil
	})
	job.Activate()
	done := make(chan struct{})
	go func() {
		defer close(done)
		job.tick(context.Background())
	}()
	<-started
	service.tick(context.Background())
	if sends, _ := sender.counts(); sends != 0 {
		t.Fatalf("startup loop sent while prediction owned the minute: %d", sends)
	}
	close(release)
	<-done
	if sends, _ := sender.counts(); sends != 1 {
		t.Fatalf("combined loops sent %d digests, want exactly one", sends)
	}
	state, err := service.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.Bootstrapped || !sameSummaryMinute(state.LastSummaryAt, readyPredictionConfig(), now) {
		t.Fatalf("persisted minute claim = %+v", state)
	}
}

func TestPredictionOwnershipDoesNotSuppressImmediateAlert(t *testing.T) {
	web, overlay := readyConfig()
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	sender := newFakeSender()
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: NewMemoryStore(),
		Source: &fakeReportSource{report: Report{GeneratedAt: now, Providers: []ProviderReport{{
			Provider: "claude", Accounts: []AccountReport{{Key: "a1", Label: "account", State: StateInvalid, LastSuccessAt: now.Add(-time.Hour)}},
		}}}, has: true},
		Renderer: NewPlainReportRenderer(time.UTC), Now: func() time.Time { return now },
		SenderFactory: func(Resolved) Sender { return sender }, SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	service.predictionSchedulerActive.Store(true)
	service.tick(context.Background())
	if sends, _ := sender.counts(); sends != 1 {
		t.Fatalf("immediate alert sends = %d", sends)
	}
	state, err := service.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].Kind != AlertCredential {
		t.Fatalf("outbox = %+v", state.Outbox)
	}
}

func marshalTestJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
