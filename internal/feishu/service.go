package feishu

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// defaultReplyBudget bounds a reply send so a slow Feishu API cannot block the
// message pipeline. It is deliberately below the three-second callback budget so
// the bot fails soft rather than letting Feishu time it out.
const defaultReplyBudget = 2500 * time.Millisecond

// maxInflightQueries caps how many queries may render at once. A full channel is
// the concurrency cap; a further query is refused with a short note rather than
// queued without bound.
const maxInflightQueries = 4

// defaultEvalInterval is how often the service re-reads the report and classifies
// it. It is short because it only reads an already-evaluated report.
const defaultEvalInterval = time.Minute

// Options configures a Service. Every external seam is injectable so a test
// constructs a service that never reaches Feishu and never triggers an upstream
// refresh.
type Options struct {
	ConsoleURL string
	// Web is the stored web-configured settings.
	Web Config
	// Overlay is the environment override supplied by the config layer.
	Overlay Overlay
	// Store persists the pending items and outbox. A nil store uses a MemoryStore.
	Store Store
	// Source supplies the newest report. It is read-only.
	Source ReportSource
	// Renderer renders messages. A nil renderer uses PlainReportRenderer.
	Renderer ReportRenderer
	Logger   *slog.Logger
	// Now overrides the clock for tests.
	Now func() time.Time
	// SenderFactory builds the sender for a resolved configuration. It defaults to
	// the official SDK sender; a test injects a fake so nothing connects.
	SenderFactory func(Resolved) Sender
	// SourceFactory builds the long-connection source. It defaults to the Lark
	// websocket; a test injects a fake that never connects.
	SourceFactory func(Resolved) EventSource
	// EvalInterval overrides the classification cadence.
	EvalInterval time.Duration
}

// Service is the Feishu bot. It owns the long connection, answer path and the
// denoise/dispatch loop. A nil or degraded configuration leaves it loaded but
// inert: it never fails process startup.
type Service struct {
	oauthChatID        string
	oauthContactOpenID string
	oauthCodeHandler   func(context.Context, string, string) error
	mu                 sync.RWMutex
	resolved           Resolved
	store              Store
	source             ReportSource
	renderer           ReportRenderer
	logger             *slog.Logger
	now                func() time.Time

	sender      Sender
	dispatcher  *Dispatcher
	eventSource EventSource

	senderFactory func(Resolved) Sender
	sourceFactory func(Resolved) EventSource
	evalInterval  time.Duration

	dedup *dedup
	slots chan struct{}
	tasks *taskGroup

	// lifecycle, guarded by runMu. runCancel/loopCancel stop the two goroutines;
	// runDone/loopDone let Stop wait for them.
	runMu      sync.Mutex
	started    bool
	runCancel  context.CancelFunc
	runDone    chan struct{}
	loopCancel context.CancelFunc
	loopDone   chan struct{}

	lastError string

	// stateMu serializes replacement of the whole runtime document. External
	// report reads and Feishu sends stay outside it.
	stateMu sync.Mutex
	// beforeStateTransitionLock is a test-only observation point for proving a
	// competing document transition has reached the lock boundary.
	beforeStateTransitionLock func()
	deliveryMu                sync.Mutex
	predictionSchedulerActive atomic.Bool
}

func (s *Service) lockStateTransition() {
	if s.beforeStateTransitionLock != nil {
		s.beforeStateTransitionLock()
	}
	s.stateMu.Lock()
}

func (s *Service) unlockStateTransition() {
	s.stateMu.Unlock()
}

// NewService builds the bot. It never returns an error: an invalid configuration
// yields a degraded, inert service, which the caller logs and continues past.
func NewService(opts Options) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	store := opts.Store
	if store == nil {
		store = NewMemoryStore()
	}
	renderer := opts.Renderer
	resolved := resolveConfig(opts.Web, opts.Overlay)
	if renderer == nil {
		renderer = NewPlainReportRenderer(resolved.Location)
		renderer.(*PlainReportRenderer).consoleURL = opts.ConsoleURL
	}
	senderFactory := opts.SenderFactory
	if senderFactory == nil {
		senderFactory = defaultSenderFactory
	}
	sourceFactory := opts.SourceFactory
	if sourceFactory == nil {
		sourceFactory = func(r Resolved) EventSource { return newLarkSource(r, logger) }
	}
	evalInterval := opts.EvalInterval
	if evalInterval <= 0 {
		evalInterval = defaultEvalInterval
	}
	service := &Service{
		resolved:      resolved,
		store:         store,
		source:        opts.Source,
		renderer:      renderer,
		logger:        logger,
		now:           now,
		senderFactory: senderFactory,
		sourceFactory: sourceFactory,
		evalInterval:  evalInterval,
		dedup:         newDedup(dedupCapacity, dedupTTL),
		slots:         make(chan struct{}, maxInflightQueries),
		tasks:         newTaskGroup(),
	}
	service.sender = senderFactory(resolved)
	service.eventSource = sourceFactory(resolved)
	service.dispatcher = NewDispatcher(service.sender, renderer, logger).withClock(now)
	return service
}

// defaultSenderFactory builds the official SDK sender for a ready configuration,
// and a nil sender for a disabled or degraded one.
func defaultSenderFactory(resolved Resolved) Sender {
	if resolved.AppID == "" || resolved.AppSecret == "" {
		return nil
	}
	return newLarkSender(resolved.AppID, resolved.AppSecret)
}

// resolvedNow returns a copy of the current resolved configuration.
func (s *Service) resolvedNow() Resolved {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolved
}

// Start launches the long connection and the classification/dispatch loop. It is
// a no-op when the service is already started or the configuration is not ready;
// it never returns a fatal error for a disabled module.
func (s *Service) Start(ctx context.Context) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	if s.started {
		return nil
	}
	s.started = true
	s.startLocked(ctx)
	return nil
}

func (s *Service) startLocked(ctx context.Context) {
	resolved := s.resolvedNow()
	if !resolved.ConnectionReady() {
		s.logger.InfoContext(ctx, "feishu module inactive",
			"reason", s.inactiveReason(resolved), "source", resolved.Source, "degraded", resolved.Degraded)
		return
	}
	s.ensureSenderLocked(resolved)

	runCtx, runCancel := context.WithCancel(ctx)
	s.runCancel = runCancel
	runDone := make(chan struct{})
	s.runDone = runDone
	source := s.eventSource
	if source == nil {
		source = s.sourceFactory(resolved)
		s.eventSource = source
	}
	go func() {
		defer close(runDone)
		if err := source.Run(runCtx, s); err != nil && runCtx.Err() == nil {
			s.setLastError(err)
			s.logger.ErrorContext(runCtx, "feishu long connection stopped", "error", err)
		}
	}()

	loopCtx, loopCancel := context.WithCancel(ctx)
	s.loopCancel = loopCancel
	loopDone := make(chan struct{})
	s.loopDone = loopDone
	go func() {
		defer close(loopDone)
		s.runLoop(loopCtx)
	}()
	s.logger.InfoContext(ctx, "feishu module started", "source", resolved.Source, "chat_configured", resolved.ChatID != "")
}

// Stop cancels the long connection and the loop, then waits for both, and cancels
// any in-flight query tasks. It is idempotent.
func (s *Service) Stop() {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	s.stopLocked()
}

func (s *Service) stopLocked() {
	if s.loopCancel != nil {
		s.loopCancel()
		s.loopCancel = nil
	}
	if s.runCancel != nil {
		s.runCancel()
		s.runCancel = nil
	}
	s.mu.Lock()
	if s.tasks != nil {
		s.tasks.Close()
		s.tasks = nil
	}
	s.mu.Unlock()
	if s.runDone != nil {
		<-s.runDone
		s.runDone = nil
	}
	if s.loopDone != nil {
		<-s.loopDone
		s.loopDone = nil
	}
	s.started = false
}

// Reload applies a new web configuration and environment overlay without a
// redeploy. A connection-identity change restarts the long connection; a
// policy-only change (quiet hours, digest times, thresholds) applies to the next
// tick. It never fails startup and never resends the one-time startup summary.
func (s *Service) Reload(ctx context.Context, web Config, overlay Overlay) {
	resolved := resolveConfig(web, overlay)
	s.runMu.Lock()
	defer s.runMu.Unlock()
	prev := s.resolvedNow()
	s.mu.Lock()
	s.resolved = resolved
	s.mu.Unlock()

	if resolved.Location != nil {
		if plain, ok := s.renderer.(*PlainReportRenderer); ok {
			plain.SetLocation(resolved.Location)
		}
	}
	if !s.started {
		s.ensureSenderLocked(resolved)
		return
	}
	if !connectionChanged(prev, resolved) && resolved.ConnectionReady() {
		// Policy-only change: keep the connection, apply thresholds on the next pass.
		s.logger.InfoContext(ctx, "feishu configuration reloaded", "source", resolved.Source)
		return
	}
	s.stopLocked()
	s.started = true
	s.ensureSenderLocked(resolved)
	s.startLocked(ctx)
}

// ensureSenderLocked rebuilds the sender and dispatcher for a new configuration.
func (s *Service) ensureSenderLocked(resolved Resolved) {
	sender := s.senderFactory(resolved)
	if sender == nil && !resolved.Ready() {
		sender = s.sender
	}
	s.mu.Lock()
	s.sender = sender
	s.dispatcher = NewDispatcher(sender, s.renderer, s.logger).withClock(s.now)
	s.mu.Unlock()
}

// connectionChanged reports whether the long-connection identity changed, which is
// the only thing that requires a reconnect.
//
// The quota recipient is deliberately absent: the sender takes its id as an
// argument on every send, so changing ChatID applies to the next delivery without
// dropping and rebuilding the websocket. Rebuilding on a recipient change would make an
// operator's target edit briefly deaf, and it would needlessly discard a healthy
// connection and re-present the app secret to the SDK. Only the app identity
// (appID, secret) and the enabled/ready state are connection identity.
func connectionChanged(a, b Resolved) bool {
	return a.Enabled != b.Enabled ||
		a.AppID != b.AppID ||
		a.AppSecret != b.AppSecret ||
		a.ConnectionReady() != b.ConnectionReady()
}

func (s *Service) inactiveReason(resolved Resolved) string {
	switch {
	case resolved.ForcedOff:
		return "forced off by environment"
	case resolved.Degraded:
		return "configuration invalid: " + resolved.Reason
	case !resolved.Enabled:
		return "disabled"
	default:
		return "not configured"
	}
}

// HandleMessage processes an inbound message event. It dedupes and classifies,
// then hands the slow render/send half to a goroutine and returns promptly, so a
// redelivered event cannot produce two replies.
func (s *Service) HandleMessage(ctx context.Context, in Inbound) error {
	if s.handleOAuthMessage(ctx, in) {
		return nil
	}
	if in.FromBot || in.ChatID == "" {
		return nil
	}
	if !in.IsDirect() || in.SenderOpenID != s.resolvedNow().ChatID {
		return nil
	}
	key := messageEventKey(in.EventID, in.MessageID)
	if !s.dedup.mark(key) {
		s.logger.InfoContext(ctx, "feishu duplicate message ignored",
			"action", "query", "result", "duplicate", "message_id", in.MessageID)
		return nil
	}
	parsed := parseIntent(stripMentions(in.Text))
	accepted := s.now()
	if !s.taskGroup().Go(func(taskCtx context.Context) {
		s.serveQuery(taskCtx, in, parsed, accepted)
	}) {
		// The task was refused (shutting down): release the reservation so a
		// replay can still be answered.
		s.dedup.forget(key)
	}
	return nil
}

// taskGroup lazily creates the async task group so handlers work in a test that
// never calls Start.
func (s *Service) taskGroup() *taskGroup {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tasks == nil {
		s.tasks = newTaskGroup()
	}
	return s.tasks
}

// HandleCardAction processes a card button callback. This batch ships no
// interactive actions, so an unknown action is acknowledged silently; the dedup
// reservation still guards against a double-pushed callback.
func (s *Service) HandleCardAction(ctx context.Context, action CardAction) error {
	if action.Action == "" {
		return nil
	}
	if !s.dedup.mark(cardEventKey(action)) {
		s.logger.InfoContext(ctx, "feishu duplicate card action ignored",
			"action", "card_action", "result", "duplicate")
		return nil
	}
	s.logger.InfoContext(ctx, "feishu unsupported card action ignored",
		"action", "card_action", "detail", action.Action)
	return nil
}

// serveQuery runs one query to completion on a detached goroutine.
func (s *Service) serveQuery(ctx context.Context, in Inbound, parsed intent, accepted time.Time) {
	if parsed.kind == intentHelp || parsed.kind == intentUnknown {
		text := helpText
		if parsed.kind == intentUnknown {
			text = "没看懂这条问法。\n" + helpText
		}
		if err := s.replyText(ctx, in.MessageID, text); err != nil {
			s.logger.WarnContext(ctx, "feishu reply failed", "action", "query", "result", "error", "error", err)
			return
		}
		s.audit(ctx, "query", in, parsed, "ok", accepted)
		return
	}
	if !s.acquireQuery() {
		if err := s.replyText(ctx, in.MessageID, "当前查询较多，请稍后再试。"); err != nil {
			s.logger.WarnContext(ctx, "feishu reply failed", "action", "query", "result", "error", "error", err)
		}
		s.audit(ctx, "query", in, parsed, "busy", accepted)
		return
	}
	defer s.releaseQuery()

	if s.source == nil {
		if err := s.replyText(ctx, in.MessageID, "数据缺失，请稍后再试。"); err != nil {
			s.logger.WarnContext(ctx, "feishu reply failed", "action", "query", "result", "error", "error", err)
		}
		return
	}
	rep, has := s.source.ReadReport(ctx)
	if !has {
		if err := s.replyText(ctx, in.MessageID, "数据缺失，请等待首次采集完成。"); err != nil {
			s.logger.WarnContext(ctx, "feishu reply failed", "action", "query", "result", "error", "error", err)
		}
		return
	}
	fresh := freshnessOf(rep, defaultFreshWindow, s.now())
	msg := queryMessage(parsed, rep, fresh)
	msg.EvaluatedAt = s.now()
	if err := s.replyCard(ctx, in.MessageID, msg); err != nil {
		s.logger.WarnContext(ctx, "feishu card reply failed", "action", "query", "result", "error", "error", err)
		return
	}
	s.audit(ctx, "query", in, parsed, auditResult(fresh), accepted)
}

// queryMessage projects a parsed intent and a report onto the channel-agnostic
// Message both the text reply and the card are rendered from.
func queryMessage(parsed intent, rep Report, fresh freshness) Message {
	filtered, note := filterReport(rep, parsed)
	return Message{
		Kind:        KindQuery,
		Report:      &filtered,
		Body:        note,
		Freshness:   fresh.label(),
		Notice:      fresh.notice(),
		Detailed:    parsed.kind == intentProvider,
		GeneratedAt: filtered.GeneratedAt,
	}
}

// filterReport narrows a report for a query and returns a short human note when
// the result is empty.
func filterReport(rep Report, parsed intent) (Report, string) {
	switch parsed.kind {
	case intentAbnormal:
		out := rep
		out.Providers = nil
		for _, p := range rep.Providers {
			if providerAbnormal(p) {
				out.Providers = append(out.Providers, p)
			}
		}
		if len(out.Providers) == 0 {
			return Report{GeneratedAt: rep.GeneratedAt}, "当前没有异常项。"
		}
		return out, ""
	case intentProvider:
		out := rep
		out.Providers = nil
		for _, p := range rep.Providers {
			if p.Provider == parsed.provider {
				out.Providers = append(out.Providers, p)
			}
		}
		if len(out.Providers) == 0 {
			return Report{GeneratedAt: rep.GeneratedAt}, "未找到该渠道的数据。"
		}
		return out, ""
	default:
		return rep, ""
	}
}

// providerAbnormal reports whether any non-disabled account needs attention.
func providerAbnormal(p ProviderReport) bool {
	for _, account := range p.Accounts {
		if account.Disabled {
			continue
		}
		if isAbnormal(account.State) {
			return true
		}
	}
	return p.Error != ""
}

func (s *Service) replyText(ctx context.Context, messageID, text string) error {
	sender := s.currentSender()
	if sender == nil {
		return errors.New("feishu sender is not configured")
	}
	rctx, cancel := context.WithTimeout(ctx, defaultReplyBudget)
	defer cancel()
	return sender.ReplyText(rctx, messageID, text)
}

func (s *Service) replyCard(ctx context.Context, messageID string, msg Message) error {
	sender := s.currentSender()
	if sender == nil {
		return errors.New("feishu sender is not configured")
	}
	rctx, cancel := context.WithTimeout(ctx, defaultReplyBudget)
	defer cancel()
	if err := sender.ReplyCard(rctx, messageID, s.renderer.Card(msg)); err != nil {
		s.logger.WarnContext(ctx, "feishu full card reply rejected, retrying simplified",
			"action", "query", "result", "fallback", "error", err)
		return sender.ReplyCard(rctx, messageID, s.renderer.CardSimple(msg))
	}
	return nil
}

// currentSender and currentDispatcher read the seams under the lock so a Reload
// that swaps them cannot race a request or the loop.
func (s *Service) currentSender() Sender {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sender
}

func (s *Service) currentDispatcher() *Dispatcher {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dispatcher
}

func (s *Service) acquireQuery() bool {
	select {
	case s.slots <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Service) releaseQuery() { <-s.slots }

// audit emits one structured, secret-free line per user action.
func (s *Service) audit(ctx context.Context, action string, in Inbound, parsed intent, result string, started time.Time) {
	// The dispatcher clock is used rather than the wall clock so a test's virtual
	// time produces a sensible duration.
	s.logger.InfoContext(ctx, "feishu action",
		"action", action,
		"operator_open_id", in.SenderOpenID,
		"chat_id", in.ChatID,
		"detail", intentName(parsed),
		"result", result,
		"duration_ms", s.now().Sub(started).Milliseconds(),
	)
}

func intentName(parsed intent) string {
	switch parsed.kind {
	case intentHelp:
		return "help"
	case intentUnknown:
		return "unknown"
	case intentStatus:
		return "status"
	case intentAbnormal:
		return "abnormal"
	case intentProvider:
		return "provider:" + parsed.provider
	default:
		return "unknown"
	}
}

func auditResult(f freshness) string {
	switch {
	case !f.available:
		return "no_data"
	case f.expired:
		return "expired"
	default:
		return "ok"
	}
}

// runLoop classifies the report, dispatches due notifications and fires the
// scheduled digest.
func (s *Service) runLoop(ctx context.Context) {
	ticker := time.NewTicker(s.evalInterval)
	defer ticker.Stop()
	s.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	resolved := s.resolvedNow()
	if !resolved.Ready() {
		return
	}
	now := s.now()
	quiet := withinQuiet(resolved, now)
	var (
		rep Report
		has bool
	)
	if s.source != nil {
		rep, has = s.source.ReadReport(ctx)
	}

	s.lockStateTransition()
	state, err := s.store.Load(ctx)
	if err != nil {
		s.unlockStateTransition()
		s.setLastError(err)
		s.logger.WarnContext(ctx, "feishu state load failed", "error", err)
		return
	}
	state.ChannelTracks = updateTracks(state.ChannelTracks, rep, has, now)

	result := evaluatePolicy(PolicyConfig{
		EnableAttention:         true,
		TightRemainingPercent:   resolved.TightRemainingPercent,
		ChannelUnavailableAfter: resolved.ChannelUnavailableAfter,
		MonitorDownAfter:        resolved.MonitorDownAfter,
		Location:                resolved.Location,
	}, PolicyInput{
		Report: rep,
		Prev:   state.PendingItems,
		Tracks: state.ChannelTracks,
		Now:    now,
		Quiet:  quiet,
	})
	state.PendingItems = result.Items

	// Immediate alerts retain one identity per episode. During quiet they
	// are persisted as queued notifications and promoted to pending at the quiet
	// end; a problem that recovered in the meantime is dropped, not sent.
	var stalled []Notification
	for _, alert := range result.Alerts {
		if morningMinute(resolved, now) && !sameSummaryMinute(state.LastSummaryAt, resolved, now) && (alert.Kind == AlertQuotaLow || alert.Kind == AlertQuotaExhausted || alert.Kind == AlertReadingMissing) {
			continue
		}
		notification := notificationFromAlert(alert, now)
		if quiet {
			notification.State = NotifyQueued
		} else if alert.Kind == AlertMonitorDown || alert.Kind == AlertQuotaLow || alert.Kind == AlertQuotaExhausted || alert.Kind == AlertReadingMissing {
			stalled = append(stalled, notification)
			continue
		}
		state.queueNotification(notification)
	}
	switch len(stalled) {
	case 0:
	case 1:
		state.queueNotification(stalled[0])
	default:
		state.queueNotification(mergeQueued(stalled, now))
	}
	if !quiet {
		s.promoteQueued(state)
	}

	predictionOwnsMinute := s.predictionSchedulerActive.Load() && scheduledMinute(resolved, now)
	if !predictionOwnsMinute {
		s.maybeStartupSummary(ctx, state, resolved, rep, has, result.Opened, now)
		s.maybeScheduledSummary(state, resolved, rep, has, now)
	} else {
		// The prediction loop owns this startup minute. Mark the one-time startup
		// digest consumed so the next ordinary tick cannot send it late.
		state.Bootstrapped = true
	}

	state.UpdatedAt = now
	if err := s.store.Save(ctx, state); err != nil {
		s.unlockStateTransition()
		s.setLastError(err)
		s.logger.WarnContext(ctx, "feishu state save failed", "error", err)
		return
	}
	s.unlockStateTransition()
	if !quiet {
		s.deliverOutbox(ctx, resolved)
	}
}

// promoteQueued collects the quiet-hours queued notifications whose problems are
// still open and merges them into a single persistent notification, so the quiet
// end delivers one batch rather than one card per item. A queued notification whose
// problem has since recovered is dropped, and a batch that is entirely recovered
// sends nothing. The merged identity is derived from the constituent notification
// ids, so re-reading the same persisted queue after a reload or restart cannot turn
// it into a second report.
func (s *Service) promoteQueued(state *State) {
	if state == nil {
		return
	}
	var held []Notification
	kept := make([]Notification, 0, len(state.Outbox))
	for _, notification := range state.Outbox {
		if notification.State != NotifyQueued {
			kept = append(kept, notification)
			continue
		}
		if allItemsResolved(state, notification) {
			continue
		}
		held = append(held, notification)
	}
	switch len(held) {
	case 0:
		state.Outbox = kept
	case 1:
		// One held item is already the whole batch; promote it in place.
		only := held[0]
		only.State = NotifyPending
		state.Outbox = append(kept, only)
	default:
		state.Outbox = kept
		state.queueNotification(mergeQueued(held, s.now()))
	}
}

// mergeQueued folds same-cycle alerts or still-open quiet notifications into one batch. The
// batch keeps every held item and its alert exactly once; its id is derived from the
// constituent notification ids so the merge stays idempotent.
func mergeQueued(held []Notification, now time.Time) Notification {
	ids := make([]string, 0, len(held))
	itemIDs := make([]string, 0, len(held))
	alerts := make([]Alert, 0, len(held))
	occurred := now
	for _, notification := range held {
		ids = append(ids, notification.ID)
		if !notification.OccurredAt.IsZero() && notification.OccurredAt.Before(occurred) {
			occurred = notification.OccurredAt
		}
		itemIDs = append(itemIDs, notification.ItemIDs...)
		alerts = append(alerts, notification.Alerts...)
	}
	sort.Strings(ids)
	sort.Strings(itemIDs)
	return Notification{
		ID:         episodeID(KindAlertBatch, ids, occurred),
		Kind:       KindAlertBatch,
		ItemIDs:    itemIDs,
		Alerts:     alerts,
		State:      NotifyPending,
		OccurredAt: occurred,
		CreatedAt:  now,
	}
}

// allItemsResolved reports whether every item a queued notification covers is
// resolved. An absent item is not evidence of recovery, so it does not by itself
// count as resolved; only a resolved item does.
func allItemsResolved(state *State, notification Notification) bool {
	if len(notification.ItemIDs) == 0 {
		return false
	}
	sawResolved := false
	for _, id := range notification.ItemIDs {
		item, ok := state.pendingByID(id)
		if !ok {
			continue
		}
		if item.Status == PendingOpen {
			return false
		}
		sawResolved = true
	}
	return sawResolved
}

// maybeStartupSummary sends the one-time startup summary, persisted so a settings
// reload never resends it. Nothing is sent when there is nothing to report.
func (s *Service) maybeStartupSummary(ctx context.Context, state *State, resolved Resolved, rep Report, has bool, opened []PendingItem, now time.Time) {
	if state.Bootstrapped || sameSummaryMinute(state.LastSummaryAt, resolved, now) {
		return
	}
	state.Bootstrapped = true
	newItems := make(map[string]bool, len(opened))
	for _, item := range opened {
		newItems[item.ID] = true
	}
	var open []PendingItem
	for _, item := range openItems(state.PendingItems) {
		// This tick already queued an immediate alert for a new episode.
		if !newItems[item.ID] && item.NotifiedAt.IsZero() {
			open = append(open, item)
		}
	}
	tight := []TightChannel(nil)
	if has {
		if len(open) > 0 {
			tight = tightChannels(rep, resolved.TightRemainingPercent, now, summaryFreshWindow)
		}
	}
	if len(open) == 0 && len(tight) == 0 {
		return
	}
	s.logger.InfoContext(ctx, "feishu startup summary queued", "open_items", len(open), "tight_channels", len(tight))
	notification := summaryNotification(open, tight, now)
	notification.Alerts = reportDecisionAlerts(rep, has, now)
	rememberReportedAlerts(state, notification.Alerts, now)
	state.queueNotification(notification)
}

// The earliest configured slot is a daily fallback even when no report exists.
// Later slots leave delivery to new episodes and actionable prediction findings.
func (s *Service) maybeScheduledSummary(state *State, resolved Resolved, rep Report, has bool, now time.Time) {
	if !scheduledMinute(resolved, now) {
		return
	}
	if sameSummaryMinute(state.LastSummaryAt, resolved, now) {
		return
	}
	isMorning := morningMinute(resolved, now)
	state.LastSummaryAt = now
	open := openItems(state.PendingItems)
	tight := []TightChannel(nil)
	if has {
		tight = tightChannels(rep, resolved.TightRemainingPercent, now, summaryFreshWindow)
	}
	if !isMorning {
		// Open episodes already have their own persisted delivery or acknowledgement.
		return
	}
	notification := summaryNotification(open, tight, now)
	notification.Alerts = reportDecisionAlerts(rep, has, now)
	rememberReportedAlerts(state, notification.Alerts, now)
	state.queueNotification(notification)
}

func (s *Service) deliverOutbox(ctx context.Context, resolved Resolved) {
	s.deliverOutboxMatching(ctx, resolved, "")
}

// deliverOutboxNotification delivers one persisted row without flushing other
// pending notifications. A manual console action therefore sends exactly the
// requested prediction card and leaves scheduled or retry work to the service loop.
func (s *Service) deliverOutboxNotification(ctx context.Context, resolved Resolved, notificationID string) {
	if notificationID == "" {
		return
	}
	s.deliverOutboxMatching(ctx, resolved, notificationID)
}

func (s *Service) deliverOutboxMatching(ctx context.Context, resolved Resolved, notificationID string) {
	dispatcher := s.currentDispatcher()
	if dispatcher == nil || resolved.ChatID == "" {
		return
	}
	// Only delivery itself is serialized. Report and prediction sources are read
	// before this point, so a slow Feishu request cannot block either scheduler.
	s.deliveryMu.Lock()
	defer s.deliveryMu.Unlock()
	s.lockStateTransition()
	state, err := s.store.Load(ctx)
	s.unlockStateTransition()
	if err != nil {
		s.setLastError(err)
		return
	}
	due := state.Outbox
	if notificationID != "" {
		due = nil
		for _, notification := range state.Outbox {
			if notification.ID == notificationID {
				due = append(due, notification)
				break
			}
		}
	}
	delivered := dispatcher.deliverDue(ctx, resolved.ChatID, due)
	s.lockStateTransition()
	defer s.unlockStateTransition()
	latest, err := s.store.Load(ctx)
	if err != nil {
		s.setLastError(err)
		return
	}
	byID := make(map[string]Notification, len(delivered))
	for _, notification := range delivered {
		byID[notification.ID] = notification
	}
	for index, notification := range latest.Outbox {
		if updated, ok := byID[notification.ID]; ok {
			latest.Outbox[index] = updated
		}
	}
	now := s.now()
	latest.prune(now)
	latest.UpdatedAt = now
	if err := s.store.Save(ctx, latest); err != nil {
		s.setLastError(err)
		s.logger.WarnContext(ctx, "feishu outbox save failed", "error", err)
	}
}

// Status is the read-only state the console shows.
type Status struct {
	Enabled       bool      `json:"enabled"`
	Ready         bool      `json:"ready"`
	Degraded      bool      `json:"degraded"`
	Reason        string    `json:"reason,omitempty"`
	Source        string    `json:"source"`
	ForcedOff     bool      `json:"forced_off"`
	Connected     bool      `json:"connected"`
	OpenItems     int       `json:"open_items"`
	PendingSends  int       `json:"pending_sends"`
	QueuedSends   int       `json:"queued_sends"`
	LastSummaryAt time.Time `json:"last_summary_at,omitempty"`
	LastError     string    `json:"last_error,omitempty"`
}

// StatusSnapshot reads the current status. It is read-only and never sends.
func (s *Service) StatusSnapshot(ctx context.Context) Status {
	resolved := s.resolvedNow()
	status := Status{
		Enabled:   resolved.Enabled,
		Ready:     resolved.Ready(),
		Degraded:  resolved.Degraded,
		Reason:    resolved.Reason,
		Source:    resolved.Source,
		ForcedOff: resolved.ForcedOff,
	}

	s.runMu.Lock()
	status.Connected = s.started && s.runCancel != nil
	s.runMu.Unlock()
	s.mu.RLock()
	status.LastError = s.lastError
	s.mu.RUnlock()
	if state, err := s.store.Load(ctx); err == nil {
		status.OpenItems = len(openItems(state.PendingItems))
		for _, notification := range state.Outbox {
			switch notification.State {
			case NotifyPending:
				status.PendingSends++
			case NotifyQueued:
				status.QueuedSends++
			}
		}
		status.LastSummaryAt = state.LastSummaryAt
	}
	return status
}

// SendTest delivers a live status card to the configured person. The console calls
// it only on an explicit operator action; the bot never triggers it on its own.
func (s *Service) SendTest(ctx context.Context) error {
	resolved := s.resolvedNow()
	if !resolved.Ready() {
		return fmt.Errorf("feishu is not ready: %s", s.inactiveReason(resolved))
	}
	dispatcher := s.currentDispatcher()
	if dispatcher == nil {
		return errors.New("feishu sender is not configured")
	}
	rep, has := Report{}, false
	if s.source != nil {
		rep, has = s.source.ReadReport(ctx)
	}
	now := s.now()
	notification := Notification{ID: episodeID("manual", nil, now), Kind: KindQuery, Alerts: reportDecisionAlerts(rep, has, now), State: NotifyPending, CreatedAt: now, OccurredAt: now}
	s.lockStateTransition()
	state, err := s.store.Load(ctx)
	if err == nil {
		state.queueNotification(notification)
		err = s.store.Save(ctx, state)
	}
	s.unlockStateTransition()
	if err != nil {
		return err
	}
	s.deliverOutboxNotification(ctx, resolved, notification.ID)
	s.lockStateTransition()
	defer s.unlockStateTransition()
	state, err = s.store.Load(ctx)
	if err != nil {
		return err
	}
	for _, candidate := range state.Outbox {
		if candidate.ID == notification.ID && candidate.State == NotifySent {
			return nil
		}
	}
	return errors.New("feishu test delivery is pending retry")
}

func (s *Service) setLastError(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastError = err.Error()
	s.mu.Unlock()
}

// withinQuiet reports whether now falls inside the quiet window. A window whose
// start and end are equal is treated as no quiet at all.
func withinQuiet(resolved Resolved, now time.Time) bool {
	local := now.In(resolved.Location)
	minutes := local.Hour()*60 + local.Minute()
	start, end := int(resolved.QuietStart), int(resolved.QuietEnd)
	if start == end {
		return false
	}
	if start < end {
		return minutes >= start && minutes < end
	}
	return minutes >= start || minutes < end
}

// updateTracks advances the per-provider last-valid-data memory that backs
// monitor_down.
func updateTracks(tracks map[string]ChannelTrack, rep Report, has bool, now time.Time) map[string]ChannelTrack {
	if tracks == nil {
		tracks = map[string]ChannelTrack{}
	}
	if !has {
		return tracks
	}
	for _, p := range rep.Providers {
		track := tracks[p.Provider]
		if !isMonitoringTimeValid(track.FirstSeen) {
			track.FirstSeen = now
		}
		if !isMonitoringTimeValid(track.LastValid) {
			track.LastValid = time.Time{}
		}
		isMonitored := len(p.Accounts) == 0 && p.Error != ""
		for _, account := range p.Accounts {
			if account.Disabled || account.IsUnmonitored {
				continue
			}
			isMonitored = true
			lastSuccess := account.LastSuccessAt
			if !isMonitoringTimeValid(lastSuccess) && account.OK {
				lastSuccess = account.ObservedAt
			}
			if isMonitoringTimeValid(lastSuccess) && lastSuccess.After(track.LastValid) {
				track.LastValid = lastSuccess
			}
		}
		if isMonitored {
			tracks[p.Provider] = track
		} else {
			// Time intentionally paused or unsupported is not monitoring downtime.
			delete(tracks, p.Provider)
		}
	}
	return tracks
}

func openItems(items []PendingItem) []PendingItem {
	var out []PendingItem
	for _, item := range items {
		if item.Status == PendingOpen {
			out = append(out, item)
		}
	}
	return out
}

// notificationFromAlert turns one immediate alert into an outbox notification.
func notificationFromAlert(alert Alert, now time.Time) Notification {
	return Notification{
		ID:         episodeID("alert", []string{alertItemID(alert)}, alert.OccurredAt),
		Kind:       alert.Kind,
		ItemIDs:    []string{alertItemID(alert)},
		Alerts:     []Alert{alert},
		State:      NotifyPending,
		OccurredAt: alert.OccurredAt,
		CreatedAt:  now,
	}
}

// summaryNotification builds a digest notification.
func summaryNotification(open []PendingItem, tight []TightChannel, now time.Time) Notification {
	ids := make([]string, 0, len(open))
	for _, item := range open {
		ids = append(ids, item.ID)
	}
	sort.Strings(ids)
	return Notification{
		ID:         episodeID("summary", ids, now),
		Kind:       KindSummary,
		ItemIDs:    ids,
		Pending:    open,
		Tight:      tight,
		State:      NotifyPending,
		OccurredAt: now,
		CreatedAt:  now,
	}
}

// alertItemID is the pending-item identity an alert belongs to.
func alertItemID(alert Alert) string {
	if alert.Kind == AlertQuotaLow || alert.Kind == AlertQuotaExhausted || alert.Kind == predictionKindPrefix {
		return ItemID(AlertQuotaLow, alert.Provider, alert.Key, alert.Scope)
	}
	return ItemID(alert.Kind, alert.Provider, alert.Key, "")
}

// episodeID is a unique, stable key for one notification episode: the kind, the
// item ids involved and the occurrence instant.
func episodeID(kind string, ids []string, at time.Time) string {
	return kind + ":" + strings.Join(ids, ",") + "@" + at.UTC().Format(time.RFC3339Nano)
}
