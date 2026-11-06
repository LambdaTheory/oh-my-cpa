package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
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
	used := 60.0
	reads := 0
	job := &PredictionJob{service: NewService(Options{Web: Config{Enabled: true}, Overlay: Overlay{AppSecret: stringPointer("secret")}}), source: func(context.Context) (PredictionDigest, error) {
		reads++
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
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
		return PredictionDigest{Items: []PredictionDigestItem{{LastsUntilReset: &lastsToReset}}}, nil
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
	used := 60.0
	reads := 0
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		reads++
		return PredictionDigest{Items: []PredictionDigestItem{{
			CredentialLabel: "account", Provider: "codex", WindowKind: "weekly",
			UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds(),
		}}}, nil
	})

	sent, err := job.SendNow(context.Background())
	if err != nil || !sent {
		t.Fatalf("manual prediction sent=%v err=%v", sent, err)
	}
	if reads != 2 {
		// One read to build the card, one immediately before sending, which is
		// the roster and freshness check every delivery performs.
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
		return PredictionDigest{Items: []PredictionDigestItem{{LastsUntilReset: &lastsToReset}}}, nil
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
		return PredictionDigest{Items: []PredictionDigestItem{{LastsUntilReset: &cannotLast, CredentialLabel: "account", ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
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
		return PredictionDigest{Items: []PredictionDigestItem{{LastsUntilReset: &cannotLast, CredentialLabel: "account", ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
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
		if notification.Kind == predictionKindPrefix+KindMorning && notification.State == NotifySent {
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
	used := 60.0
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
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
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
	used := 60.0
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Now: func() time.Time { return now },
		Source: &fakeReportSource{}, SenderFactory: func(Resolved) Sender { return sender },
		SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
	})
	ctx, cancel := context.WithCancel(context.Background())
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		cancel()
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
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
	if payload := sender.lastCard(); payload == nil || !strings.Contains(marshalTestJSON(payload), "1 个窗口撑不到重置") || !strings.Contains(marshalTestJSON(payload), "实际 60.0% · 期望 无法计算") {
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
	used := 60.0
	reads := make(chan struct{}, 1)
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		reads <- struct{}{}
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
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
	var startOnce sync.Once
	used := 60.0
	job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
		// Delivery re-reads the digest before sending, so the source runs twice.
		startOnce.Do(func() { close(started) })
		<-release
		return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
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
			Provider: "claude", Accounts: []AccountReport{{Key: "a1", Label: "account", State: StateInvalid, AuthSignalObservedAt: now, LastSuccessAt: now.Add(-time.Hour)}},
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
				Provider: "claude", Accounts: []AccountReport{{Key: "a1", Label: "account", State: StateInvalid, AuthSignalObservedAt: now, LastSuccessAt: now.Add(-time.Hour)}},
			}}}}
			service := NewService(Options{
				Web: web, Overlay: overlay, Store: store, Source: reportSource, Now: func() time.Time { return now },
				SenderFactory: func(Resolved) Sender { return newFakeSender() }, SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
			})
			lockBoundary := make(chan bool, 2)
			var transitionCount atomic.Int32
			service.beforeStateTransitionLock = func() {
				if transitionCount.Add(1) > 2 {
					return
				}
				isHeld := !service.stateMu.TryLock()
				if !isHeld {
					service.stateMu.Unlock()
				}
				lockBoundary <- isHeld
			}
			service.predictionSchedulerActive.Store(true)
			used := 60.0
			job := NewPredictionJob(service, func(context.Context) (PredictionDigest, error) {
				return PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "prediction", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}}}, nil
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
	used := 60.0
	items := make([]PredictionDigestItem, quotaPredictionFindingsForCardTest)
	for index := range items {
		items[index] = PredictionDigestItem{CredentialLabel: fmt.Sprintf("account-%03d", index), Provider: "codex", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}
	}
	notification := predictionNotification(items, 3, readyPredictionConfig(), now)
	message := Message{Kind: notification.Kind, Alerts: notification.Alerts, GeneratedAt: notification.OccurredAt}
	card := NewPlainReportRenderer(time.UTC).Card(message)
	body := card["body"].(map[string]any)
	elements := body["elements"].([]map[string]any)
	// 原型 t6-redo 定「每行一个凭证、最多 5 行、其余另有 N 项」，旧断言固定 3 行
	// 与方案冲突；断言意图（披露「另有 N 项」）不变，只为对齐 5 行上限。
	// 改测试已按约束取得跟进同意（2026-11-05，见运行记录 2026-11-05-2026-11-05-T21）。
	if len(elements) != 8 || !strings.Contains(marshalTestJSON(card), "另有") {
		t.Fatalf("prediction card elements=%d payload=%s", len(elements), marshalTestJSON(card))
	}
}

const quotaPredictionFindingsForCardTest = 178

func TestPredictionCardRowNamesCredentialAndWindow(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	used := 60.0
	items := []PredictionDigestItem{
		{CredentialLabel: "work", Provider: "codex", Key: "a1", WindowID: "five_hour", WindowKind: "five_hour", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()},
		{CredentialLabel: "work", Provider: "codex", Key: "a1", WindowID: "weekly", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()},
	}
	notification := predictionNotification(items, 0, readyPredictionConfig(), now)
	card := NewPlainReportRenderer(time.UTC).Card(Message{Kind: notification.Kind, Alerts: notification.Alerts, GeneratedAt: notification.OccurredAt})
	payload := marshalTestJSON(card)
	// Same credential, two windows: the rows must be told apart by window type.
	for _, want := range []string{"Codex · work · 5 小时", "Codex · work · 每周"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("prediction card is missing row identity %q: %s", want, payload)
		}
	}
}

// A card that quiet hours or a retry held is re-checked right before sending.
// When the fresh digest no longer reports the finding (the reading went stale,
// the credential was disabled or removed, or the window now lasts to reset),
// nothing is sent and the queued row is closed instead of retried forever.
func TestPredictionCardDropsFindingsThatExpiredWhileQueued(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	now := *env.clock
	used := 60.0
	current := PredictionDigest{Omitted: 3, Items: []PredictionDigestItem{{
		CredentialLabel: "account", Provider: "codex", Key: "a1", WindowID: "weekly", WindowKind: "weekly",
		UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds(),
	}}}
	job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) { return current, nil })
	env.sender.failSends = 2
	if sent, err := job.SendNow(context.Background()); err == nil || sent {
		t.Fatalf("failed prediction send sent=%v err=%v", sent, err)
	}
	state, err := env.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].State != NotifyPending {
		t.Fatalf("queued prediction = %+v", state.Outbox)
	}
	// While the card waits for its retry the reading goes stale.
	current = PredictionDigest{}
	*env.clock = state.Outbox[0].NextAttemptAt
	env.service.deliverOutbox(context.Background(), env.service.resolvedNow())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("stale prediction card was sent %d times", sends)
	}
	state, err = env.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Outbox) != 1 || state.Outbox[0].State != NotifySent || state.Outbox[0].LastError != "" {
		t.Fatalf("dropped prediction row = %+v", state.Outbox)
	}
}

// Only the rows the fresh digest still reports go out; the rest of the card is
// not sent again just because one row survived.
func TestPredictionCardKeepsOnlyRowsThatAreStillFindings(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	now := *env.clock
	used := 60.0
	row := func(label, key, window string) PredictionDigestItem {
		return PredictionDigestItem{CredentialLabel: label, Provider: "codex", Key: key, WindowID: window, WindowKind: "weekly",
			UsedPercent: &used, LastsUntilReset: &cannotLast, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds()}
	}
	alpha, beta := row("alpha", "a1", "weekly"), row("beta", "b1", "weekly")
	current := PredictionDigest{Items: []PredictionDigestItem{alpha, beta}}
	job := NewPredictionJob(env.service, func(context.Context) (PredictionDigest, error) { return current, nil })
	env.sender.failSends = 2
	if sent, err := job.SendNow(context.Background()); err == nil || sent {
		t.Fatalf("failed prediction send sent=%v err=%v", sent, err)
	}
	state, err := env.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current = PredictionDigest{Items: []PredictionDigestItem{alpha}}
	*env.clock = state.Outbox[0].NextAttemptAt
	env.service.deliverOutbox(context.Background(), env.service.resolvedNow())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("retried prediction sends = %d", sends)
	}
	payload := marshalTestJSON(env.sender.lastCard())
	if !strings.Contains(payload, "alpha") || strings.Contains(payload, "beta") {
		t.Fatalf("prediction card carried a row that is no longer a finding: %s", payload)
	}
}

func marshalTestJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// A surplus forecast is console context. The push exists only for a window that
// cannot last until its reset.
func TestPredictionDigestDoesNotPushSurplus(t *testing.T) {
	morning := mustTime("2026-10-27T01:00:00Z")
	job := &PredictionJob{service: NewService(Options{})}
	used := 12.0
	digest := PredictionDigest{Items: []PredictionDigestItem{{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, LastsUntilReset: &lastsToReset}}}
	if job.queueFindings(EmptyState(), readyPredictionConfig(), morning, digest) {
		t.Fatal("surplus forecast reached the push path")
	}
}

// An observation that has aged past the freshness bound the prediction used is
// no longer evidence, so a retry must not render it.
func TestPredictionDigestDropsStaleObservation(t *testing.T) {
	now := mustTime("2026-10-27T09:00:00Z")
	used := 95.0
	item := PredictionDigestItem{CredentialLabel: "account", Provider: "codex", WindowKind: "weekly",
		UsedPercent: &used, ObservedAtMS: now.Add(-3 * time.Hour).UnixMilli(), FreshWindowMS: int64(time.Hour / time.Millisecond), LastsUntilReset: &cannotLast}
	if findings := predictionFindings([]PredictionDigestItem{item}, now); len(findings) != 0 {
		t.Fatalf("stale finding survived: %+v", findings)
	}
	item.ObservedAtMS = now.Add(-30 * time.Minute).UnixMilli()
	if findings := predictionFindings([]PredictionDigestItem{item}, now); len(findings) != 1 {
		t.Fatalf("fresh finding dropped: %+v", findings)
	}
}

// The card states the actual and expected usage and reads the newest observation.
func TestPredictionCardShowsBaselineAndLatestRead(t *testing.T) {
	now := mustTime("2026-10-27T09:00:00Z")
	used, expected := 80.0, 60.5
	older := PredictionDigestItem{CredentialLabel: "older", Provider: "codex", WindowKind: "weekly", UsedPercent: &used, ExpectedUsedPercent: &expected, ObservedAtMS: now.Add(-2 * time.Hour).UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds(), LastsUntilReset: &cannotLast}
	newer := PredictionDigestItem{CredentialLabel: "newer", Provider: "claude", WindowKind: "five_hour", UsedPercent: &used, ExpectedUsedPercent: &expected, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds(), LastsUntilReset: &cannotLast}
	notification := predictionNotification([]PredictionDigestItem{older, newer}, 2, readyPredictionConfig(), now)
	payload := marshalTestJSON(NewPlainReportRenderer(time.UTC).Card(Message{Alerts: notification.Alerts, Omitted: notification.Omitted, Kind: notification.Kind}))
	if !strings.Contains(payload, "实际 80.0%") || !strings.Contains(payload, "期望 60.5%") {
		t.Fatalf("baseline missing: %s", payload)
	}
	if !strings.Contains(payload, "另有 2 项") {
		t.Fatalf("omitted count missing: %s", payload)
	}
	if !strings.Contains(payload, "读数 10-27 09:00") {
		t.Fatalf("read time is not the newest observation: %s", payload)
	}
}

// F4: the row states the actual usage and an absolute exhaustion time, matching
// the console's wording instead of a relative countdown.
func TestPredictionCardStatesAbsoluteExhaustionTime(t *testing.T) {
	now := mustTime("2026-10-27T09:00:00Z")
	eta := now.Add(2 * time.Hour).UnixMilli()
	used, expected := 88.0, 52.0
	notification := predictionNotification([]PredictionDigestItem{{
		CredentialLabel: "account", Provider: "codex", WindowKind: "weekly",
		UsedPercent: &used, ExpectedUsedPercent: &expected, ExhaustionETAMS: &eta,
		ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds(), LastsUntilReset: &cannotLast,
	}}, 0, readyPredictionConfig(), now)
	payload := marshalTestJSON(NewPlainReportRenderer(time.UTC).Card(Message{Kind: notification.Kind, Alerts: notification.Alerts}))
	if !strings.Contains(payload, "实际 88.0%") || !strings.Contains(payload, "期望 52.0%") {
		t.Fatalf("actual/expected wording missing: %s", payload)
	}
	if !strings.Contains(payload, "预计 10-27 11:00 耗尽 · 撑不到重置") {
		t.Fatalf("absolute exhaustion time missing: %s", payload)
	}
	if strings.Contains(payload, "预计还可用") || strings.Contains(payload, "当前已用") {
		t.Fatalf("old relative wording survived: %s", payload)
	}
}

// F3: the morning card carries actionable non-prediction alerts alongside the
// forecast; the prediction-kind filter used to drop them.
func TestPredictionCardKeepsNonPredictionAlerts(t *testing.T) {
	now := mustTime("2026-10-27T09:00:00Z")
	used, expected := 80.0, 60.0
	notification := predictionNotification([]PredictionDigestItem{{
		CredentialLabel: "account", Provider: "codex", WindowKind: "weekly",
		UsedPercent: &used, ExpectedUsedPercent: &expected, ObservedAtMS: now.UnixMilli(), FreshWindowMS: (2 * time.Hour).Milliseconds(), LastsUntilReset: &cannotLast,
	}}, 0, readyPredictionConfig(), now)
	channel := Alert{Kind: AlertChannelUnavailable, Provider: "claude", Title: "渠道不可用", Detail: "连续失败 3 次", ObservedAt: now, EvidenceSource: EvidenceAvailability, FreshWindowMS: defaultFreshWindow.Milliseconds()}
	card := NewPlainReportRenderer(time.UTC).Card(Message{
		Kind: notification.Kind, Alerts: append(notification.Alerts, channel),
	})
	payload := marshalTestJSON(card)
	if !strings.Contains(payload, "渠道不可用") {
		t.Fatalf("non-prediction alert was dropped: %s", payload)
	}
	if !strings.Contains(payload, "其他需要处理") || !strings.Contains(payload, "1 个窗口撑不到重置") {
		t.Fatalf("combined card structure missing: %s", payload)
	}
}

// F2: a quiet-hours release or retry must re-check the reading age. An expired
// row is dropped, and a card left with nothing evidence-based is not sent.
func TestPredictionDeliveryDropsExpiredRows(t *testing.T) {
	now := mustTime("2026-10-27T09:00:00Z")
	sender := newFakeSender()
	dispatcher := NewDispatcher(sender, NewPlainReportRenderer(time.UTC), nil).withClock(func() time.Time { return now })
	bound := int64((time.Hour / time.Millisecond))
	stale := Alert{Kind: predictionKindPrefix, Provider: "codex", Title: "每周窗口", ObservedAt: now.Add(-3 * time.Hour), EvidenceSource: EvidenceQuota, FreshWindowMS: bound}
	fresh := Alert{Kind: predictionKindPrefix, Provider: "claude", Title: "5 小时窗口", ObservedAt: now.Add(-10 * time.Minute), EvidenceSource: EvidenceQuota, FreshWindowMS: bound}
	outbox := []Notification{
		{ID: "stale", Kind: predictionKindPrefix + KindMorning, Alerts: []Alert{stale}, State: NotifyPending, OccurredAt: now.Add(-3 * time.Hour), CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "mixed", Kind: predictionKindPrefix + KindMorning, Alerts: []Alert{stale, fresh}, State: NotifyPending, OccurredAt: now, CreatedAt: now},
	}
	out := dispatcher.deliverDue(context.Background(), "chat", outbox)
	if sends, _ := sender.counts(); sends != 1 {
		t.Fatalf("stale-only card should not send, mixed card should: sends=%d", sends)
	}
	if out[0].State != NotifySent || out[1].State != NotifySent {
		t.Fatalf("delivery states = %+v", out)
	}
	payload := marshalTestJSON(sender.lastCard())
	if strings.Contains(payload, "每周窗口") || !strings.Contains(payload, "5 小时窗口") {
		t.Fatalf("expired row survived delivery: %s", payload)
	}
}

// A credential deleted from CPA leaves a stored open item behind. Re-announcing
// it would describe an account that no longer exists.
func TestPredictionPendingSkipsDeletedCredential(t *testing.T) {
	deleted := PendingItem{ID: ItemID(AlertQuotaLow, "codex", "gone", "weekly"), Kind: AlertQuotaLow, Provider: "codex", Key: "gone", Alias: "gone"}
	report := Report{Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "kept", Label: "kept"}}}}}
	if pendingStillKnown(deleted, report, true) {
		t.Fatal("deleted credential still announced")
	}
	kept := deleted
	kept.Key, kept.Alias = "kept", "kept"
	if !pendingStillKnown(kept, report, true) {
		t.Fatal("live credential was dropped")
	}
	if !pendingStillKnown(deleted, Report{}, false) {
		t.Fatal("a failed report read must not drop stored items")
	}
}
