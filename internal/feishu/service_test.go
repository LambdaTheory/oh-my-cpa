package feishu

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeSender records the calls the service makes and can fail a bounded number of
// sends so the retry path can be exercised without a network.
type fakeSender struct {
	mu        sync.Mutex
	sends     int
	replies   int
	texts     []string
	chatIDs   []string
	failSends int
	signal    chan struct{}
}

func newFakeSender() *fakeSender {
	return &fakeSender{signal: make(chan struct{}, 32)}
}

func (f *fakeSender) SendCard(_ context.Context, chatID string, _ map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSends > 0 {
		f.failSends--
		return errors.New("simulated send failure")
	}
	f.sends++
	f.chatIDs = append(f.chatIDs, chatID)
	f.notify()
	return nil
}

func (f *fakeSender) ReplyText(_ context.Context, _ string, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies++
	f.texts = append(f.texts, text)
	f.notify()
	return nil
}

func (f *fakeSender) ReplyCard(_ context.Context, _ string, _ map[string]any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies++
	f.notify()
	return nil
}

func (f *fakeSender) notify() {
	select {
	case f.signal <- struct{}{}:
	default:
	}
}

func (f *fakeSender) counts() (sends, replies int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sends, f.replies
}

func (f *fakeSender) lastChatID() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.chatIDs) == 0 {
		return ""
	}
	return f.chatIDs[len(f.chatIDs)-1]
}

// fakeReportSource counts reads and returns a canned report, so a test can assert
// that a query reads only the local report and never triggers an upstream refresh.
type fakeReportSource struct {
	mu     sync.Mutex
	reads  int
	report Report
	has    bool
}

func (f *fakeReportSource) ReadReport(context.Context) (Report, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	return f.report, f.has
}

func (f *fakeReportSource) readCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reads
}

// fakeEventSource stands in for the websocket client; it records whether it was
// started and never connects.
type fakeEventSource struct {
	ran bool
}

func (f *fakeEventSource) Run(ctx context.Context, _ EventHandler) error {
	f.ran = true
	<-ctx.Done()
	return nil
}

func (f *fakeEventSource) Name() string { return "fake-event-source" }

// countingSourceFactory builds long-connection sources that count how many times
// a connection is opened and closed, so a reload test can prove a reconnect did or
// did not happen. Each transition signals on signal, so the test waits on the
// observable event rather than polling.
type countingSourceFactory struct {
	mu     sync.Mutex
	starts int
	stops  int
	signal chan struct{}
}

func newCountingSourceFactory() *countingSourceFactory {
	return &countingSourceFactory{signal: make(chan struct{}, 32)}
}

func (f *countingSourceFactory) New(Resolved) EventSource { return &countingSource{factory: f} }

func (f *countingSourceFactory) counts() (starts, stops int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.starts, f.stops
}

func (f *countingSourceFactory) notify() {
	select {
	case f.signal <- struct{}{}:
	default:
	}
}

type countingSource struct{ factory *countingSourceFactory }

func (s *countingSource) Run(ctx context.Context, _ EventHandler) error {
	s.factory.mu.Lock()
	s.factory.starts++
	s.factory.mu.Unlock()
	s.factory.notify()
	<-ctx.Done()
	s.factory.mu.Lock()
	s.factory.stops++
	s.factory.mu.Unlock()
	s.factory.notify()
	return nil
}

func (s *countingSource) Name() string { return "counting-source" }

// countingSenderFactory records how often the sender is rebuilt, so a target-group
// change can be shown to reuse the existing connection and credential.
type countingSenderFactory struct {
	mu     sync.Mutex
	calls  int
	sender Sender
}

func (f *countingSenderFactory) New(Resolved) Sender {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.sender
}

func (f *countingSenderFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// waitEvent waits on an observable state transition signalled on signal, with a
// single hang-protection deadline. It never sleeps: a fake fires signal as it
// changes the state the condition reads.
func waitEvent(t *testing.T, what string, signal <-chan struct{}, condition func() bool) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if condition() {
			return
		}
		select {
		case <-signal:
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func readyConfig() (Config, Overlay) {
	secret := "app-secret"
	return Config{
		Enabled:                 true,
		AppID:                   "cli_test",
		ChatID:                  "oc_test",
		Timezone:                "UTC",
		QuietStart:              "23:00",
		QuietEnd:                "08:00",
		TightRemainingPercent:   20,
		ChannelUnavailableAfter: 30 * time.Minute,
		MonitorDownAfter:        30 * time.Minute,
	}, Overlay{AppSecret: &secret}
}

type testEnv struct {
	service *Service
	sender  *fakeSender
	source  *fakeReportSource
	events  *fakeEventSource
	clock   *time.Time
	store   *MemoryStore
}

func newTestEnv(t *testing.T, report Report, has bool) *testEnv {
	t.Helper()
	web, overlay := readyConfig()
	sender := newFakeSender()
	source := &fakeReportSource{report: report, has: has}
	events := &fakeEventSource{}
	clock := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	// The one-time startup summary is suppressed by default so a test can focus on
	// one behavior; a test that exercises it seeds an un-bootstrapped store instead.
	_ = store.Save(context.Background(), &State{Bootstrapped: true, ChannelTracks: map[string]ChannelTrack{}})

	service := NewService(Options{
		Web:           web,
		Overlay:       overlay,
		Store:         store,
		Source:        source,
		Renderer:      NewPlainReportRenderer(time.UTC),
		Now:           func() time.Time { return clock },
		SenderFactory: func(Resolved) Sender { return sender },
		SourceFactory: func(Resolved) EventSource { return events },
		EvalInterval:  time.Hour,
	})
	return &testEnv{service: service, sender: sender, source: source, events: events, clock: &clock, store: store}
}

func waitSignal(t *testing.T, ch chan struct{}) bool {
	t.Helper()
	select {
	case <-ch:
		return true
	case <-time.After(2 * time.Second):
		return false
	}
}

func invalidReport(now time.Time) Report {
	return Report{GeneratedAt: now, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", Label: "acct", State: StateInvalid, LastSuccessAt: now.Add(-time.Hour)}},
	}}}
}

func TestPrivateChatQueryRepliesWithoutUpstreamRefresh(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	env := newTestEnv(t, invalidReport(now), true)
	if err := env.service.HandleMessage(context.Background(), Inbound{
		EventID: "e1", MessageID: "m1", ChatID: "oc_x", ChatType: "p2p", Text: "额度",
	}); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(t, env.sender.signal) {
		t.Fatal("expected a reply")
	}
	_, replies := env.sender.counts()
	if replies != 1 {
		t.Fatalf("replies = %d, want 1", replies)
	}
	if reads := env.source.readCount(); reads != 1 {
		t.Fatalf("source reads = %d, want 1 (no extra upstream refresh)", reads)
	}
}

func TestGroupMessageNeedsMention(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	env := newTestEnv(t, invalidReport(now), true)
	// A group message without an @-mention is ignored entirely.
	_ = env.service.HandleMessage(context.Background(), Inbound{
		EventID: "e2", MessageID: "m2", ChatID: "oc_x", ChatType: "group", Text: "额度",
	})
	if waitSignal(t, env.sender.signal) {
		t.Fatal("a group message without an @-mention must not be answered")
	}
	if env.source.readCount() != 0 {
		t.Fatal("an ignored message must not read the report")
	}
	// With an @-mention it is answered.
	_ = env.service.HandleMessage(context.Background(), Inbound{
		EventID: "e3", MessageID: "m3", ChatID: "oc_x", ChatType: "group", Text: "额度", BotMentioned: true,
	})
	if !waitSignal(t, env.sender.signal) {
		t.Fatal("a mentioned group message should be answered")
	}
}

func TestDuplicateEventIsAnsweredOnce(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	env := newTestEnv(t, invalidReport(now), true)
	in := Inbound{EventID: "dup-1", MessageID: "m1", ChatID: "oc_x", ChatType: "p2p", Text: "额度"}
	_ = env.service.HandleMessage(context.Background(), in)
	if !waitSignal(t, env.sender.signal) {
		t.Fatal("expected a reply")
	}
	_ = env.service.HandleMessage(context.Background(), in)
	if waitSignal(t, env.sender.signal) {
		t.Fatal("a redelivered event must not produce a second reply")
	}
	if _, replies := env.sender.counts(); replies != 1 {
		t.Fatalf("replies = %d, want 1", replies)
	}
	if reads := env.source.readCount(); reads != 1 {
		t.Fatalf("source reads = %d, want 1", reads)
	}
}

func TestHelpReplyDoesNotReadReport(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	env := newTestEnv(t, invalidReport(now), true)
	_ = env.service.HandleMessage(context.Background(), Inbound{
		EventID: "h1", MessageID: "m1", ChatID: "oc_x", ChatType: "p2p", Text: "帮助",
	})
	if !waitSignal(t, env.sender.signal) {
		t.Fatal("expected a help reply")
	}
	if reads := env.source.readCount(); reads != 0 {
		t.Fatalf("help must not read the report, got %d reads", reads)
	}
}

func TestBotMessageIsIgnoredToPreventLoop(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	env := newTestEnv(t, invalidReport(now), true)
	_ = env.service.HandleMessage(context.Background(), Inbound{
		EventID: "b1", MessageID: "m1", ChatID: "oc_x", ChatType: "p2p", Text: "额度", FromBot: true,
	})
	if waitSignal(t, env.sender.signal) {
		t.Fatal("the bot must never answer its own message")
	}
}

func TestQuietQueueMergesAndSendsAtQuietEnd(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	*env.clock = time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	service := env.service

	report := invalidReport(*env.clock)
	env.source.report = report
	env.source.has = true

	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("quiet hours must not send, got %d", sends)
	}
	state, _ := env.store.Load(context.Background())
	if len(state.Outbox) != 1 || state.Outbox[0].State != NotifyQueued {
		t.Fatalf("expected one queued notification, got %+v", state.Outbox)
	}

	*env.clock = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("quiet end should send the merged notification, got %d", sends)
	}
	state, _ = env.store.Load(context.Background())
	if countState(state.Outbox, NotifyQueued) != 0 {
		t.Fatalf("queued notifications should be promoted, got %+v", state.Outbox)
	}
	// A second pass must not resend.
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("a settled problem must not resend, got %d", sends)
	}
}

func TestQuietQueueDropsFullyRecoveredProblem(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	*env.clock = time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	service := env.service

	report := invalidReport(*env.clock)
	env.source.report = report
	env.source.has = true
	service.tick(context.Background())

	// The credential recovers before the quiet end: nothing must be sent.
	healthy := Report{GeneratedAt: *env.clock, Providers: []ProviderReport{{
		Provider: "codex",
		Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true}},
	}}}
	env.source.report = healthy
	*env.clock = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("a fully recovered problem must not be sent, got %d", sends)
	}
}

func TestFailedNotificationIsRetriedAndPersisted(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	base := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	*env.clock = base
	service := env.service
	env.sender.failSends = 2 // fail both the full card and the simplified fallback

	report := invalidReport(base)
	env.source.report = report
	env.source.has = true

	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("the first send failed, sends = %d", sends)
	}
	state, _ := env.store.Load(context.Background())
	if len(state.Outbox) != 1 || state.Outbox[0].Attempts != 1 || state.Outbox[0].State != NotifyPending {
		t.Fatalf("a failed notification must stay pending with an attempt count, got %+v", state.Outbox)
	}
	nextAttempt := state.Outbox[0].NextAttemptAt
	if !nextAttempt.After(base) {
		t.Fatalf("a failed send should back off, next attempt = %v", nextAttempt)
	}

	// Before the retry time: still not sent.
	*env.clock = base.Add(time.Second)
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("retry before the backoff must not send, got %d", sends)
	}

	// After the retry time: delivered.
	*env.clock = nextAttempt.Add(time.Second)
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("the retry should deliver, got %d", sends)
	}
	state, _ = env.store.Load(context.Background())
	if state.Outbox[0].State != NotifySent {
		t.Fatalf("a delivered notification must be marked sent, got %+v", state.Outbox)
	}
}

func TestStartupSummarySentOnceAndNotOnReload(t *testing.T) {
	web, overlay := readyConfig()
	sender := newFakeSender()
	clock := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	source := &fakeReportSource{report: invalidReport(clock), has: true}
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Source: source,
		Renderer:      NewPlainReportRenderer(time.UTC),
		Now:           func() time.Time { return clock },
		SenderFactory: func(Resolved) Sender { return sender },
		SourceFactory: func(Resolved) EventSource { return &fakeEventSource{} },
		EvalInterval:  time.Hour,
	})
	service.tick(context.Background())
	firstSends, _ := sender.counts()
	if firstSends < 1 {
		t.Fatalf("the startup summary should send, got %d", firstSends)
	}
	state, _ := store.Load(context.Background())
	if !state.Bootstrapped {
		t.Fatal("the startup summary flag must persist")
	}

	service.Reload(context.Background(), web, overlay)
	service.tick(context.Background())
	if secondSends, _ := sender.counts(); secondSends != firstSends {
		t.Fatalf("a settings reload must not resend the startup summary: %d -> %d", firstSends, secondSends)
	}
}

func TestScheduledSummaryOnlyWhenThereIsATodo(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	// 10:00 UTC is a configured summary time and outside quiet hours.
	*env.clock = time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	service := env.service
	env.source.report = Report{GeneratedAt: *env.clock, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true}}}}}
	env.source.has = true
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("a scheduled summary with no to-do and nothing tight must stay silent, got %d", sends)
	}
}

// TestScheduledSummarySendsForFreshTightChannel pins that a fresh tight reading
// still fires the digest, so the age gate does not silence a real warning.
func TestScheduledSummarySendsForFreshTightChannel(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	*env.clock = time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	service := env.service
	env.source.report = Report{GeneratedAt: *env.clock, Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true, ObservedAt: *env.clock, Windows: []Window{accountWindow(95, nil)}}},
	}}}
	env.source.has = true
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("a fresh tight channel should fire the digest, got %d", sends)
	}
	state, _ := env.store.Load(context.Background())
	summary, found := notificationOfKind(state.Outbox, KindSummary)
	if !found || len(summary.Tight) != 1 || summary.Tight[0].Provider != "claude" {
		t.Fatalf("expected one tight channel in the digest, got %+v", summary.Tight)
	}
}

// TestScheduledSummaryIgnoresStaleTightSample pins the defect: a stale report
// whose account window is below the tight threshold must not push a tight digest.
func TestScheduledSummaryIgnoresStaleTightSample(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	stale := time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
	*env.clock = time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	service := env.service
	env.source.report = Report{GeneratedAt: stale, Providers: []ProviderReport{{
		Provider: "claude",
		Accounts: []AccountReport{{Key: "a1", State: StateHealthy, OK: true, ObservedAt: stale, Windows: []Window{accountWindow(95, nil)}}},
	}}}
	env.source.has = true
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("a stale tight sample must not fire a tight digest, got %d", sends)
	}
}

// TestScheduledSummaryKeepsOpenItemsWithoutReport pins that has=false does not
// push a tight digest but still summarizes a real open item, so the age gate
// never silences genuine work.
func TestScheduledSummaryKeepsOpenItemsWithoutReport(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	*env.clock = time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	service := env.service
	// has stays false: no report has been produced.
	state, _ := env.store.Load(context.Background())
	state.PendingItems = []PendingItem{{
		ID: ItemID(AlertCredential, "codex", "a1", ""), Kind: AlertCredential,
		Provider: "codex", Key: "a1", Alias: "acct", Status: PendingOpen,
		Since: *env.clock, LastSeen: *env.clock,
	}}
	if err := env.store.Save(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("an open item must still be summarized with no report, got %d", sends)
	}
	state, _ = env.store.Load(context.Background())
	summary, found := notificationOfKind(state.Outbox, KindSummary)
	if !found || len(summary.Pending) != 1 {
		t.Fatalf("expected the open item in the digest, got %+v", summary.Pending)
	}
	if len(summary.Tight) != 0 {
		t.Fatalf("has=false must not report a tight channel, got %+v", summary.Tight)
	}
	if len(openItems(state.PendingItems)) != 1 {
		t.Fatalf("the open item must stay open, got %+v", state.PendingItems)
	}
}

func TestInvalidConfigurationKeepsModuleInert(t *testing.T) {
	web := Config{Enabled: true, AppID: "", ChatID: ""} // enabled but unusable
	events := &fakeEventSource{}
	service := NewService(Options{
		Web:           web,
		Store:         NewMemoryStore(),
		Renderer:      NewPlainReportRenderer(time.UTC),
		SenderFactory: func(Resolved) Sender { return newFakeSender() },
		SourceFactory: func(Resolved) EventSource { return events },
	})
	status := service.StatusSnapshot(context.Background())
	if status.Ready || !status.Degraded {
		t.Fatalf("an invalid enabled config must be degraded and not ready, got %+v", status)
	}
	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("start must not fail on an invalid config: %v", err)
	}
	if events.ran {
		t.Fatal("an inert module must not start the long connection")
	}
	service.Stop()
}

func TestInjectedFactoriesNeverBuildTheRealAdapters(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	if _, isLark := env.service.eventSource.(*larkSource); isLark {
		t.Fatal("a test must never construct the real websocket source")
	}
	if _, isLark := env.service.sender.(*larkSender); isLark {
		t.Fatal("a test must never construct the real SDK sender")
	}
}

// TestReloadTargetGroupDoesNotReconnectButIdentityDoes pins the reconnect rule: the
// target group is not connection identity, so changing it keeps the long connection
// and the built sender and the next send uses the new group; only the app identity or
// the enabled state opens a new connection.
func TestReloadTargetGroupDoesNotReconnectButIdentityDoes(t *testing.T) {
	web, overlay := readyConfig()
	clock := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	store := NewMemoryStore()
	if err := store.Save(context.Background(), &State{Bootstrapped: true, ChannelTracks: map[string]ChannelTrack{}}); err != nil {
		t.Fatal(err)
	}
	sender := newFakeSender()
	source := &fakeReportSource{report: invalidReport(clock), has: true}
	sources := newCountingSourceFactory()
	senders := &countingSenderFactory{sender: sender}
	service := NewService(Options{
		Web: web, Overlay: overlay, Store: store, Source: source,
		Renderer:      NewPlainReportRenderer(time.UTC),
		Now:           func() time.Time { return clock },
		SenderFactory: senders.New,
		SourceFactory: sources.New,
		EvalInterval:  time.Hour,
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer service.Stop()
	waitEvent(t, "the long connection to start", sources.signal, func() bool { starts, _ := sources.counts(); return starts == 1 })
	// The loop's first tick delivers the open credential alert to the original
	// group; wait for it so the target-group assertion below is unambiguous.
	if !waitSignal(t, sender.signal) {
		t.Fatal("timed out waiting for the first tick to deliver")
	}
	baselineSenders := senders.count()

	// A target-group change applies on the next send without a reconnect.
	chatOnly := web
	chatOnly.ChatID = "oc_new"
	service.Reload(context.Background(), chatOnly, overlay)
	if starts, stops := sources.counts(); starts != 1 || stops != 0 {
		t.Fatalf("a target-group change must not reconnect: starts=%d stops=%d", starts, stops)
	}
	if senders.count() != baselineSenders {
		t.Fatalf("a target-group change must not rebuild the sender: %d -> %d", baselineSenders, senders.count())
	}
	if err := service.SendTest(context.Background()); err != nil {
		t.Fatalf("send test after a target-group change: %v", err)
	}
	if last := sender.lastChatID(); last != "oc_new" {
		t.Fatalf("the next send must use the new target group, got %q", last)
	}

	// A policy-only change (quiet hours, thresholds) must not reconnect either.
	policyOnly := chatOnly
	policyOnly.QuietEnd = "07:00"
	policyOnly.TightRemainingPercent = 10
	service.Reload(context.Background(), policyOnly, overlay)
	if starts, stops := sources.counts(); starts != 1 || stops != 0 {
		t.Fatalf("a policy-only change must not reconnect: starts=%d stops=%d", starts, stops)
	}

	// An app-identity change reconnects: stop the old connection, open a new one.
	identity := policyOnly
	identity.AppID = "cli_other"
	service.Reload(context.Background(), identity, overlay)
	waitEvent(t, "the reconnected long connection", sources.signal, func() bool { starts, _ := sources.counts(); return starts == 2 })
	if _, stops := sources.counts(); stops != 1 {
		t.Fatalf("the old connection must be stopped on an identity change: stops=%d", stops)
	}

	// Disabling the module stops the connection and opens no new one.
	off := identity
	off.Enabled = false
	service.Reload(context.Background(), off, overlay)
	waitEvent(t, "the connection to stop", sources.signal, func() bool { _, stops := sources.counts(); return stops == 2 })
	if starts, _ := sources.counts(); starts != 2 {
		t.Fatalf("disabling must not open a new connection: starts=%d", starts)
	}
	if status := service.StatusSnapshot(context.Background()); status.Connected {
		t.Fatalf("a disabled module must not report connected: %+v", status)
	}
}

func countState(outbox []Notification, state string) int {
	count := 0
	for _, notification := range outbox {
		if notification.State == state {
			count++
		}
	}
	return count
}

func countKind(outbox []Notification, kind string) int {
	count := 0
	for _, notification := range outbox {
		if notification.Kind == kind {
			count++
		}
	}
	return count
}

func notificationOfKind(outbox []Notification, kind string) (Notification, bool) {
	for _, notification := range outbox {
		if notification.Kind == kind {
			return notification, true
		}
	}
	return Notification{}, false
}

// invalidReportWith builds a report where every listed account is invalid, which
// opens one credential_invalid alert per account.
func invalidReportWith(now time.Time, accounts ...string) Report {
	reports := make([]AccountReport, 0, len(accounts))
	for _, key := range accounts {
		reports = append(reports, AccountReport{Key: key, Label: key, State: StateInvalid, LastSuccessAt: now.Add(-time.Hour)})
	}
	return Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: reports}}}
}

// mixedReport builds a report from an explicit key -> state map. A healthy account
// reports OK true so a previously open item resolves rather than merely going
// silent.
func mixedReport(now time.Time, states map[string]string) Report {
	keys := make([]string, 0, len(states))
	for key := range states {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	reports := make([]AccountReport, 0, len(keys))
	for _, key := range keys {
		state := states[key]
		reports = append(reports, AccountReport{
			Key: key, Label: key, State: state,
			OK: state == StateHealthy, LastSuccessAt: now.Add(-time.Hour),
		})
	}
	return Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: reports}}}
}

// quietQueued builds the persisted queued notification a quiet-opened credential
// alert would leave behind, as if it had been reloaded after a restart.
func quietQueued(itemID, key string, at time.Time) Notification {
	return Notification{
		ID:         episodeID("alert", []string{itemID}, at),
		Kind:       AlertCredential,
		ItemIDs:    []string{itemID},
		Alerts:     []Alert{{Kind: AlertCredential, Severity: SeverityUrgent, Title: "凭证失效", Key: key}},
		State:      NotifyQueued,
		OccurredAt: at,
		CreatedAt:  at,
	}
}

// TestQuietEndMergesStillOpenItemsIntoOneNotification pins the quiet-end merge: a
// quiet window opens several items, one recovers before the window ends, and the
// still-open ones are delivered as one batch rather than one card each.
func TestQuietEndMergesStillOpenItemsIntoOneNotification(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	service := env.service

	*env.clock = time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	env.source.report = invalidReportWith(*env.clock, "a1", "a2")
	env.source.has = true
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("quiet hours must not send, got %d", sends)
	}
	state, _ := env.store.Load(context.Background())
	if countState(state.Outbox, NotifyQueued) != 2 {
		t.Fatalf("expected two queued notifications, got %+v", state.Outbox)
	}

	// A third problem opens later in the same quiet window.
	*env.clock = time.Date(2026, 10, 16, 2, 0, 0, 0, time.UTC)
	env.source.report = invalidReportWith(*env.clock, "a1", "a2", "a3")
	service.tick(context.Background())
	state, _ = env.store.Load(context.Background())
	if countState(state.Outbox, NotifyQueued) != 3 {
		t.Fatalf("expected three queued notifications, got %+v", state.Outbox)
	}

	// At the quiet end a3 has recovered; a1 and a2 are still open and must arrive as
	// one merged notification.
	resolved := ItemID(AlertCredential, "codex", "a3", "")
	*env.clock = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	env.source.report = mixedReport(*env.clock, map[string]string{
		"a1": StateInvalid, "a2": StateInvalid, "a3": StateHealthy,
	})
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("the quiet end must send exactly one merged notification, got %d", sends)
	}
	state, _ = env.store.Load(context.Background())
	if countState(state.Outbox, NotifyQueued) != 0 {
		t.Fatalf("queued notifications should be drained, got %+v", state.Outbox)
	}
	merged, found := notificationOfKind(state.Outbox, KindAlertBatch)
	if !found || countKind(state.Outbox, KindAlertBatch) != 1 {
		t.Fatalf("expected exactly one merged batch, got %+v", state.Outbox)
	}
	if merged.State != NotifySent {
		t.Fatalf("a delivered batch must be marked sent, got %s", merged.State)
	}
	if len(merged.ItemIDs) != 2 || len(merged.Alerts) != 2 {
		t.Fatalf("the batch must carry both still-open items once each: %+v", merged)
	}
	for _, id := range merged.ItemIDs {
		if id == resolved {
			t.Fatal("a recovered item must be dropped from the batch")
		}
	}
}

// TestQuietEndAllResolvedSendsNothingAndDrainsQueue pins that a fully recovered
// quiet queue is dropped without sending.
func TestQuietEndAllResolvedSendsNothingAndDrainsQueue(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	service := env.service

	*env.clock = time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	env.source.report = invalidReportWith(*env.clock, "a1", "a2")
	env.source.has = true
	service.tick(context.Background())
	state, _ := env.store.Load(context.Background())
	if countState(state.Outbox, NotifyQueued) != 2 {
		t.Fatalf("expected two queued notifications, got %+v", state.Outbox)
	}

	*env.clock = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	env.source.report = mixedReport(*env.clock, map[string]string{"a1": StateHealthy, "a2": StateHealthy})
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("an all-recovered batch must not send, got %d", sends)
	}
	state, _ = env.store.Load(context.Background())
	if countState(state.Outbox, NotifyQueued) != 0 {
		t.Fatalf("a recovered queue must be drained, got %+v", state.Outbox)
	}
	if _, found := notificationOfKind(state.Outbox, KindAlertBatch); found {
		t.Fatal("no merged batch should be queued when everything recovered")
	}
}

// TestQuietQueueSurvivesRestartAndMergesOnce pins that a queue persisted before a
// restart still merges into a single send and cannot be turned into a duplicate by
// reloading the outbox.
func TestQuietQueueSurvivesRestartAndMergesOnce(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	service := env.service

	id1 := ItemID(AlertCredential, "codex", "a1", "")
	id2 := ItemID(AlertCredential, "codex", "a2", "")
	queuedAt := time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	seed := EmptyState()
	seed.Bootstrapped = true
	seed.PendingItems = []PendingItem{
		{ID: id1, Kind: AlertCredential, Provider: "codex", Key: "a1", Status: PendingOpen},
		{ID: id2, Kind: AlertCredential, Provider: "codex", Key: "a2", Status: PendingOpen},
	}
	seed.Outbox = []Notification{quietQueued(id1, "a1", queuedAt), quietQueued(id2, "a2", queuedAt)}
	if err := env.store.Save(context.Background(), seed); err != nil {
		t.Fatal(err)
	}

	// The quiet window ends after the restart: the persisted queue must still merge.
	*env.clock = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	env.source.report = invalidReportWith(*env.clock, "a1", "a2")
	env.source.has = true
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("a restarted quiet queue should merge and send once, got %d", sends)
	}
	state, _ := env.store.Load(context.Background())
	if countKind(state.Outbox, KindAlertBatch) != 1 {
		t.Fatalf("expected exactly one merged batch, got %+v", state.Outbox)
	}

	// A second pass must not manufacture a duplicate from the reloaded outbox.
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("a settled batch must not resend, got %d", sends)
	}
	state, _ = env.store.Load(context.Background())
	if countKind(state.Outbox, KindAlertBatch) != 1 || countState(state.Outbox, NotifyQueued) != 0 {
		t.Fatalf("merged batch duplicated or requeued on reload: %+v", state.Outbox)
	}
}

// TestFailedMergedQuietSendRetriesWithoutLosingOrDuplicating pins that a merged
// quiet batch is persisted before sending, stays pending on failure, and is
// delivered exactly once on retry.
func TestFailedMergedQuietSendRetriesWithoutLosingOrDuplicating(t *testing.T) {
	env := newTestEnv(t, Report{}, false)
	service := env.service

	*env.clock = time.Date(2026, 10, 15, 23, 30, 0, 0, time.UTC)
	env.source.report = invalidReportWith(*env.clock, "a1", "a2")
	env.source.has = true
	service.tick(context.Background())

	env.sender.failSends = 2 // fail the full card and its simplified fallback
	*env.clock = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 0 {
		t.Fatalf("a failed batch send must not count as sent, got %d", sends)
	}
	state, _ := env.store.Load(context.Background())
	merged, found := notificationOfKind(state.Outbox, KindAlertBatch)
	if !found || countKind(state.Outbox, KindAlertBatch) != 1 {
		t.Fatalf("a failed batch must remain persisted exactly once, got %+v", state.Outbox)
	}
	if merged.State != NotifyPending || merged.Attempts != 1 {
		t.Fatalf("a failed batch must stay pending with one attempt, got %+v", merged)
	}
	if len(merged.ItemIDs) != 2 || len(merged.Alerts) != 2 {
		t.Fatalf("the retried batch must keep both items, got %+v", merged)
	}
	if countState(state.Outbox, NotifyQueued) != 0 {
		t.Fatalf("the individual queued rows should be gone, got %+v", state.Outbox)
	}

	// After the backoff the same persisted batch is delivered exactly once.
	*env.clock = merged.NextAttemptAt.Add(time.Second)
	service.tick(context.Background())
	if sends, _ := env.sender.counts(); sends != 1 {
		t.Fatalf("the retry should deliver exactly once, got %d", sends)
	}
	state, _ = env.store.Load(context.Background())
	if delivered, _ := notificationOfKind(state.Outbox, KindAlertBatch); delivered.State != NotifySent {
		t.Fatalf("a confirmed delivery must mark the batch sent, got %+v", delivered)
	}
	if countKind(state.Outbox, KindAlertBatch) != 1 {
		t.Fatalf("the batch must not duplicate after retry: %+v", state.Outbox)
	}
}
