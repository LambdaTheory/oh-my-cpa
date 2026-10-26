package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func readyPredictionConfig() Resolved {
	return resolveConfig(Config{
		Enabled: true, AppID: "app", ChatID: "ou_test", SummaryTimes: []string{"10:00", "17:00"}, Timezone: "UTC",
	}, Overlay{AppSecret: stringPointer("secret")})
}

func stringPointer(value string) *string { return &value }

func TestPredictionDigestQueuesOnceAndSuppressesSummary(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	used, projected := 60.0, 120.0
	reads := 0
	job := &PredictionJob{service: NewService(Options{Web: Config{Enabled: true}, Overlay: Overlay{AppSecret: stringPointer("secret")}}), source: func(context.Context) (PredictionDigest, error) {
		reads++
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}}, nil
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
	job := &PredictionJob{service: NewService(Options{Web: Config{Enabled: true}, Overlay: Overlay{AppSecret: stringPointer("secret")}}), source: func(context.Context) (PredictionDigest, error) {
		return PredictionDigest{Items: []PredictionDigestItem{{Verdict: "on_track"}}}, nil
	}}
	state := EmptyState()
	if job.maybeQueue(context.Background(), state, readyPredictionConfig(), now) || len(state.Outbox) != 0 || !state.LastSummaryAt.IsZero() {
		t.Fatalf("unexpected prediction state = %+v", state)
	}
}

func TestManualPredictionSendsOneCardWithoutClaimingSummaryMinute(t *testing.T) {
	web, overlay := readyConfig()
	now := time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC)
	sender := newFakeSender()
	store := NewMemoryStore()
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Now: func() time.Time { return now },
		Renderer:      NewPlainReportRenderer(time.UTC),
		SenderFactory: func(Resolved) Sender { return sender },
		SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	used, projected := 60.0, 120.0
	reads := 0
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		reads++
		return PredictionDigest{Items: []PredictionDigestItem{{
			CredentialLabel: "account", Provider: "codex", WindowKind: "weekly",
			UsedPercent: &used, ProjectedPercentAtReset: &projected,
			ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust",
		}}}, nil
	})

	sent, err := job.SendNow(context.Background())
	if err != nil || !sent {
		t.Fatalf("manual prediction sent=%v err=%v", sent, err)
	}
	if reads != 1 {
		t.Fatalf("prediction source reads = %d", reads)
	}
	if sends, _ := sender.counts(); sends != 1 {
		t.Fatalf("manual prediction sends = %d", sends)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastSummaryAt.IsZero() || len(state.Outbox) != 1 || state.Outbox[0].State != NotifySent {
		t.Fatalf("manual prediction state = %+v", state)
	}
}

func TestManualPredictionWithNoFindingSendsNothingAndKeepsState(t *testing.T) {
	web, overlay := readyConfig()
	now := time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC)
	sender := newFakeSender()
	store := NewMemoryStore()
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Now: func() time.Time { return now },
		SenderFactory: func(Resolved) Sender { return sender },
		SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		return PredictionDigest{Items: []PredictionDigestItem{{Verdict: "on_track"}}}, nil
	})

	sent, err := job.SendNow(context.Background())
	if err != nil || sent {
		t.Fatalf("manual no-finding sent=%v err=%v", sent, err)
	}
	if sends, _ := sender.counts(); sends != 0 {
		t.Fatalf("manual no-finding sends = %d", sends)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !state.LastSummaryAt.IsZero() || len(state.Outbox) != 0 {
		t.Fatalf("manual no-finding state = %+v", state)
	}
}

func TestManualPredictionFailureRemainsPendingForRetry(t *testing.T) {
	web, overlay := readyConfig()
	now := time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC)
	sender := newFakeSender()
	sender.failSends = 2 // Both the full card and simplified fallback must fail.
	store := NewMemoryStore()
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Now: func() time.Time { return now },
		SenderFactory: func(Resolved) Sender { return sender },
		SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Verdict: "will_exhaust"}}}, nil
	})
	sent, err := job.SendNow(context.Background())
	if sent || err == nil || !strings.Contains(err.Error(), "pending") {
		t.Fatalf("failed delivery sent=%v err=%v", sent, err)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].State != NotifyPending || !state.Outbox[0].NextAttemptAt.After(now) {
		t.Fatalf("failed delivery must remain retryable: %+v", state.Outbox)
	}
	if sends, _ := sender.counts(); sends != 0 {
		t.Fatalf("successful sends = %d", sends)
	}
}

func TestManualPredictionIsolatesOutboxAndPreservesScheduledDigest(t *testing.T) {
	web, overlay := readyConfig()
	web.Timezone = "UTC"
	web.SummaryTimes = []string{"10:00", "17:00"}
	web.QuietStart, web.QuietEnd = "09:00", "09:59"
	now := time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC)
	sender := newFakeSender()
	store := NewMemoryStore()
	state := EmptyState()
	state.Outbox = append(state.Outbox, Notification{ID: "unrelated", Kind: AlertCredential, State: NotifyPending, CreatedAt: now, OccurredAt: now})
	if err := store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Now: func() time.Time { return now },
		SenderFactory: func(Resolved) Sender { return sender },
		SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Verdict: "will_exhaust"}}}, nil
	})
	if sent, err := job.SendNow(context.Background()); err != nil || !sent {
		t.Fatalf("explicit send during quiet hours sent=%v err=%v", sent, err)
	}
	state, err := store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 2 || state.Outbox[0].ID != "unrelated" || state.Outbox[0].State != NotifyPending || !state.LastSummaryAt.IsZero() {
		t.Fatalf("manual send altered unrelated state: %+v", state)
	}
	if sends, _ := sender.counts(); sends != 1 {
		t.Fatalf("manual sends = %d", sends)
	}
	now = time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	job.tick(context.Background())
	state, err = store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var scheduledSent bool
	for _, notification := range state.Outbox {
		if notification.Kind == predictionKindPrefix+"10:00" && notification.State == NotifySent {
			scheduledSent = true
		}
	}
	if !scheduledSent || !sameSummaryMinute(state.LastSummaryAt, service.resolvedNow(), now) {
		t.Fatalf("manual send suppressed scheduled digest: %+v", state)
	}
}

func TestPredictionDigestDoesNotRunOutsideReadyService(t *testing.T) {
	reads := 0
	job := &PredictionJob{service: NewService(Options{}), source: func(context.Context) (PredictionDigest, error) { reads++; return PredictionDigest{}, nil }}
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
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}}, nil
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
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) { reads++; return PredictionDigest{}, nil })
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
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		cancel()
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}}, nil
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
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		reads <- struct{}{}
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}}, nil
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
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		close(started)
		<-release
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}}, nil
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

type blockingFirstLoadStore struct {
	base     *MemoryStore
	mu       sync.Mutex
	blocked  bool
	loaded   chan struct{}
	release  chan struct{}
	saved    chan struct{}
	saveOnce sync.Once
}

func newBlockingFirstLoadStore() *blockingFirstLoadStore {
	return &blockingFirstLoadStore{base: NewMemoryStore(), loaded: make(chan struct{}), release: make(chan struct{}), saved: make(chan struct{})}
}

func (s *blockingFirstLoadStore) Load(ctx context.Context) (*State, error) {
	state, err := s.base.Load(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	isFirst := !s.blocked
	if isFirst {
		s.blocked = true
	}
	s.mu.Unlock()
	if isFirst {
		close(s.loaded)
		<-s.release
	}
	return state, nil
}

func (s *blockingFirstLoadStore) Save(ctx context.Context, state *State) error {
	err := s.base.Save(ctx, state)
	if err == nil {
		s.saveOnce.Do(func() { close(s.saved) })
	}
	return err
}

func TestRuntimeDocumentTransitionsPreserveBothSchedulerWrites(t *testing.T) {
	for _, first := range []string{"prediction", "service"} {
		t.Run(first+"_loads_first", func(t *testing.T) {
			now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
			web, overlay := readyConfig()
			web.QuietStart, web.QuietEnd = "00:00", "23:59"
			store := newBlockingFirstLoadStore()
			reportSource := &fakeReportSource{has: true, report: Report{GeneratedAt: now, Providers: []ProviderReport{{
				Provider: "claude", Accounts: []AccountReport{{Key: "a1", Label: "account", State: StateInvalid, LastSuccessAt: now.Add(-time.Hour)}},
			}}}}
			service := NewService(Options{
				Web: web, Overlay: overlay, Store: store, Source: reportSource, Now: func() time.Time { return now },
				SenderFactory: func(Resolved) Sender { return newFakeSender() }, SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
			})
			lockBoundary := make(chan bool, 2)
			service.beforeStateTransitionLock = func() {
				isHeld := !service.stateMu.TryLock()
				if !isHeld {
					service.stateMu.Unlock()
				}
				lockBoundary <- isHeld
			}
			service.predictionSchedulerActive.Store(true)
			used, projected := 60.0, 120.0
			job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
				return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "prediction", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}}}, nil
			})

			serviceDone, predictionDone := make(chan struct{}), make(chan struct{})
			runService := func() { service.tick(context.Background()); close(serviceDone) }
			runPrediction := func() { job.tick(context.Background()); close(predictionDone) }
			if first == "prediction" {
				go runPrediction()
				<-store.loaded
				<-lockBoundary
				go runService()
			} else {
				go runService()
				<-store.loaded
				<-lockBoundary
				go runPrediction()
			}
			isFirstTransitionHeld := <-lockBoundary
			if !isFirstTransitionHeld {
				// With serialization removed, the contender loads and saves the stale
				// document before the first transition is released, forcing the old
				// last-writer-wins failure instead of relying on goroutine scheduling.
				<-store.saved
			}
			close(store.release)
			<-serviceDone
			<-predictionDone

			state, err := store.base.Load(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			alerts, predictions := 0, 0
			for _, notification := range state.Outbox {
				if notification.Kind == AlertCredential {
					alerts++
				}
				if strings.HasPrefix(notification.Kind, predictionKindPrefix) {
					for _, alert := range notification.Alerts {
						if alert.Kind == AlertCredential {
							alerts++
						}
					}
				}
				if strings.HasPrefix(notification.Kind, predictionKindPrefix) {
					predictions++
				}
			}
			if alerts != 1 || predictions != 1 || !sameSummaryMinute(state.LastSummaryAt, service.resolvedNow(), now) {
				t.Fatalf("runtime document lost a scheduler write: alerts=%d predictions=%d state=%+v", alerts, predictions, state)
			}
		})
	}
}

func TestPredictionDigestDisclosesOmittedFindingsWithinCardLimit(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	used, projected := 60.0, 120.0
	items := make([]PredictionDigestItem, quotaPredictionFindingsForCardTest)
	for index := range items {
		items[index] = PredictionDigestItem{CredentialLabel: fmt.Sprintf("account-%03d", index), Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ProjectedPercentAtReset: &projected, ObservedAtMS: now.UnixMilli(), Verdict: "will_exhaust"}
	}
	notification := predictionNotification(items, 3, readyPredictionConfig(), now)
	message := Message{Kind: notification.Kind, Alerts: notification.Alerts, GeneratedAt: notification.OccurredAt}
	card := NewPlainReportRenderer(time.UTC).Card(message)
	body := card["body"].(map[string]any)
	elements := body["elements"].([]map[string]any)
	if len(elements) != 6 || !strings.Contains(marshalTestJSON(card), "另有") {
		t.Fatalf("prediction card elements=%d payload=%s", len(elements), marshalTestJSON(card))
	}
}

const quotaPredictionFindingsForCardTest = 178

func marshalTestJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
