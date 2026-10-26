package feishu

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const predictionKindPrefix = "prediction:"

const defaultPredictionInterval = time.Minute

type PredictionDigestItem struct {
	Key                       string
	WindowID                  string
	ResetAccuracy             string
	ResetAtMS                 *int64
	ExhaustionETAMS           *int64
	WindowScope               string
	CredentialLabel           string
	Provider                  string
	WindowKind                string
	UsedPercent               *float64
	ProjectedPercentAtReset   *float64
	ProjectedTokensAtReset    *int64
	ProjectedCostNanosAtReset *int64
	ObservedAtMS              int64
	Verdict                   string
}

type PredictionDigest struct {
	Items   []PredictionDigestItem
	Omitted int
}

type PredictionSource func(context.Context) (PredictionDigest, error)

type PredictionJob struct {
	service  *Service
	source   PredictionSource
	interval time.Duration
}

func NewPredictionJob(service *Service, source PredictionSource) *PredictionJob {
	if service == nil || source == nil {
		return nil
	}
	return &PredictionJob{service: service, source: source, interval: defaultPredictionInterval}
}

func (job *PredictionJob) Ready() bool {
	return job != nil && job.service != nil && job.service.resolvedNow().Ready()
}

// SendNow queues and delivers one operator-requested prediction card. Unlike a
// scheduled digest it does not claim the summary minute or alter LastSummaryAt,
// so it cannot suppress either configured daily digest. An explicit operator send
// bypasses quiet hours; scheduled delivery and retries still honor them.
func (job *PredictionJob) SendNow(ctx context.Context) (bool, error) {
	if job == nil || job.service == nil || job.source == nil {
		return false, fmt.Errorf("feishu prediction sender is unavailable")
	}
	service := job.service
	resolved := service.resolvedNow()
	if !resolved.Ready() {
		return false, fmt.Errorf("feishu is not ready: %s", service.inactiveReason(resolved))
	}
	digest, err := job.source(ctx)
	if err != nil {
		return false, err
	}
	findings := predictionFindings(digest.Items)
	if len(findings) == 0 {
		return false, nil
	}

	now := service.now()
	notification := predictionNotification(findings, digest.Omitted, resolved, now)
	service.lockStateTransition()
	state, err := service.store.Load(ctx)
	if err == nil {
		state.queueNotification(notification)
		state.UpdatedAt = now
		err = service.store.Save(ctx, state)
	}
	service.unlockStateTransition()
	if err != nil {
		return false, err
	}
	if err := service.deliverOutboxNotification(ctx, resolved, notification.ID); err != nil {
		return false, err
	}

	service.lockStateTransition()
	latest, loadErr := service.store.Load(ctx)
	service.unlockStateTransition()
	if loadErr != nil {
		return false, loadErr
	}
	for _, candidate := range latest.Outbox {
		if candidate.ID != notification.ID {
			continue
		}
		if candidate.State == NotifySent {
			return true, nil
		}
		return false, fmt.Errorf("feishu prediction delivery is pending")
	}
	return false, fmt.Errorf("feishu prediction notification was not persisted")
}

// Activate transfers scheduled-summary ownership before the service loop starts,
// so its immediate first tick cannot race the prediction job and send both cards.
func (job *PredictionJob) Activate() {
	if job == nil || job.service == nil {
		return
	}
	job.service.predictionSchedulerActive.Store(true)
}

func (job *PredictionJob) Run(ctx context.Context) error {
	if job == nil || job.service == nil {
		return nil
	}
	job.Activate()
	defer job.service.predictionSchedulerActive.Store(false)
	job.tick(ctx)
	interval := job.interval
	if interval <= 0 {
		interval = defaultPredictionInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			job.tick(ctx)
		}
	}
}

func (job *PredictionJob) tick(ctx context.Context) {
	if job == nil || job.service == nil {
		return
	}
	service := job.service
	resolved := service.resolvedNow()
	now := service.now()
	if !resolved.Ready() {
		return
	}
	// The prediction source is deliberately read before the persisted state. A
	// held source cannot block the service loop, and the later load incorporates
	// any immediate alerts the service saved while prediction was evaluating.
	digest, sourceErr := job.source(ctx)
	if sourceErr != nil {
		service.logger.WarnContext(ctx, "feishu prediction digest read failed", "error", sourceErr)
	}
	var report Report
	var hasReport bool
	if service.source != nil {
		report, hasReport = service.source.ReadReport(ctx)
	}
	service.lockStateTransition()
	state, err := service.store.Load(ctx)
	if err != nil {
		service.unlockStateTransition()
		service.setLastError(err)
		service.logger.WarnContext(ctx, "feishu prediction state load failed", "error", err)
		return
	}
	queued := sourceErr == nil && job.queueFindings(state, resolved, now, digest)
	if queued && morningMinute(resolved, now) {
		for index := range state.Outbox {
			notification := &state.Outbox[index]
			if notification.CreatedAt.Equal(now) && strings.HasPrefix(notification.Kind, predictionKindPrefix) {
				for _, item := range openItems(state.PendingItems) {
					if !announcedInMinute(state, item.ID, notification.ID, now) {
						notification.Pending = append(notification.Pending, item)
					}
				}
				for _, alert := range reportDecisionAlerts(report, hasReport, now) {
					if alert.Kind != statusHealthy {
						if announcedInMinute(state, alertItemID(alert), notification.ID, now) {
							continue
						}
						notification.Alerts = append(notification.Alerts, alert)
						rememberReportedAlerts(state, []Alert{alert}, report, now)
					}
				}
			}
		}
	}
	if !queued {
		service.maybeScheduledSummary(state, resolved, report, hasReport, now)
	}
	state.UpdatedAt = now
	if err := service.store.Save(ctx, state); err != nil {
		service.unlockStateTransition()
		service.setLastError(err)
		service.logger.WarnContext(ctx, "feishu prediction state save failed", "error", err)
		return
	}
	service.unlockStateTransition()
	service.deliverOutbox(ctx, resolved)
}

func (job *PredictionJob) maybeQueue(ctx context.Context, state *State, resolved Resolved, now time.Time) bool {
	if job.source == nil || state == nil || !resolved.Ready() || !scheduledMinute(resolved, now) || sameSummaryMinute(state.LastSummaryAt, resolved, now) {
		return false
	}
	digest, err := job.source(ctx)
	if err != nil {
		job.service.logger.WarnContext(ctx, "feishu prediction digest read failed", "error", err)
		return false
	}
	return job.queueFindings(state, resolved, now, digest)
}

func predictionFindings(items []PredictionDigestItem) []PredictionDigestItem {
	findings := make([]PredictionDigestItem, 0, len(items))
	for _, item := range items {
		if item.Verdict == "will_exhaust" || item.Verdict == "under_used" {
			findings = append(findings, item)
		}
	}
	return findings
}

func (job *PredictionJob) queueFindings(state *State, resolved Resolved, now time.Time, digest PredictionDigest) bool {
	if state == nil || !resolved.Ready() || sameSummaryMinute(state.LastSummaryAt, resolved, now) {
		return false
	}
	var findings []PredictionDigestItem
	for _, item := range digest.Items {
		identity := predictionItemID(item)
		previous, exists := state.pendingByID(identity)
		reset := time.Time{}
		if item.ResetAccuracy == "exact" && item.ResetAtMS != nil {
			reset = time.UnixMilli(*item.ResetAtMS).UTC()
		}
		if exists && previous.Kind == predictionKindPrefix && (item.Verdict == "on_track" || item.Verdict == "under_used") {
			for index := range state.PendingItems {
				marker := &state.PendingItems[index]
				if marker.ID == identity && marker.ResolvedAt.IsZero() {
					marker.ResolvedAt = now
				}
			}
		}
		if item.Verdict != "will_exhaust" {
			continue
		}
		isNewCycle := exists && hasNewQuotaCycle(previous, reset, time.UnixMilli(item.ObservedAtMS))
		isRecurrence := exists && previous.Status == PendingResolved && previous.CycleResetAt.IsZero() && !previous.ResolvedAt.IsZero()
		if exists && !isNewCycle && !isRecurrence {
			for index := range state.PendingItems {
				if state.PendingItems[index].ID == identity {
					state.PendingItems[index].LastSeen = now
					if state.PendingItems[index].CycleResetAt.IsZero() {
						state.PendingItems[index].CycleResetAt = reset
					}
				}
			}
			if morningMinute(resolved, now) {
				findings = append(findings, item)
			}
			continue
		}
		// A resolved marker remembers the announced risk without turning a
		// forecast into a permanent incident in the ordinary pending list.
		marker := PendingItem{ID: identity, Kind: predictionKindPrefix,
			Provider: item.Provider, Key: predictionKey(item), Alias: item.CredentialLabel, Status: PendingResolved,
			Action: "提前准备备用凭证，按需分流", Since: now, LastSeen: now, NotifiedAt: now, CycleResetAt: reset}
		if exists {
			for index := range state.PendingItems {
				if state.PendingItems[index].ID == identity {
					state.PendingItems[index] = marker
				}
			}
		} else {
			state.PendingItems = append(state.PendingItems, marker)
		}
		findings = append(findings, item)
	}
	if len(findings) == 0 {
		return false
	}
	if morningMinute(resolved, now) {
		state.LastSummaryAt = now
	}
	notification := predictionNotification(findings, digest.Omitted, resolved, now)
	state.queueNotification(notification)
	return true
}

func predictionNotification(items []PredictionDigestItem, omitted int, resolved Resolved, now time.Time) Notification {
	alerts := make([]Alert, 0, len(items)+1)
	for _, item := range items {
		title, advice := "预计重置前耗尽", "提前准备备用凭证，按需分流"
		if item.Verdict == "under_used" {
			title, advice = "预计额度有余量", "按需使用现有额度"
		}
		detail := item.WindowKind + " · 当前已用 " + predictionPercent(item.UsedPercent)
		if item.ExhaustionETAMS != nil && *item.ExhaustionETAMS > now.UnixMilli() {
			detail += "；预计还可用 " + remainingDuration(time.UnixMilli(*item.ExhaustionETAMS).Sub(now))
		} else {
			detail += "；耗尽时间数据缺失"
		}
		if item.WindowScope != "" {
			detail = scopeLabel(item.WindowScope) + " · " + detail
		}
		observed := time.Time{}
		if item.ObservedAtMS > 0 {
			observed = time.UnixMilli(item.ObservedAtMS).UTC()
		}
		alerts = append(alerts, Alert{Kind: predictionKindPrefix, Severity: SeverityWarn, Provider: item.Provider, Key: predictionKey(item), Scope: predictionScope(item), ObservedAt: observed,
			Alias: item.CredentialLabel, Title: title, Detail: detail + "（预测）", Advice: advice, OccurredAt: now})
	}
	if omitted > 0 {
		alerts = append(alerts, Alert{Kind: "omitted", Title: fmt.Sprintf("另有 %d 项预测发现未展示", omitted), Advice: "打开控制台查看完整预测"})
	}
	localMinute := now.In(resolved.Location).Format("15:04")
	if morningMinute(resolved, now) {
		localMinute = KindMorning
	}
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, strings.Join([]string{item.Provider, item.CredentialLabel, item.WindowKind, item.Verdict}, ":"))
	}
	return Notification{
		ID: episodeID(predictionKindPrefix, ids, now), Kind: predictionKindPrefix + localMinute,
		Alerts: alerts, State: NotifyPending, OccurredAt: now, CreatedAt: now,
	}
}

func predictionPercent(value *float64) string {
	if !validPercent(value) {
		return "数据缺失"
	}
	return fmt.Sprintf("%.1f%%", *value)
}

func scheduledMinute(resolved Resolved, now time.Time) bool {
	local := now.In(resolved.Location)
	minutes := local.Hour()*60 + local.Minute()
	for _, value := range resolved.SummaryTimes {
		if parsed, ok := parseClock(value); ok && int(parsed) == minutes {
			return true
		}
	}
	return false
}

func sameSummaryMinute(last time.Time, resolved Resolved, now time.Time) bool {
	return !last.IsZero() && last.In(resolved.Location).Format("2006-01-02 15:04") == now.In(resolved.Location).Format("2006-01-02 15:04")
}

// The earliest configured slot is the daily fallback; later slots stay event driven.
func morningMinute(resolved Resolved, now time.Time) bool {
	first := clock(24 * 60)
	for _, value := range resolved.SummaryTimes {
		if parsed, ok := parseClock(value); ok && parsed < first {
			first = parsed
		}
	}
	local := now.In(resolved.Location)
	return local.Hour()*60+local.Minute() == int(first)
}

func predictionKey(item PredictionDigestItem) string {
	if item.Key != "" {
		return item.Key
	}
	return item.CredentialLabel
}

func predictionScope(item PredictionDigestItem) string {
	window := item.WindowID
	if window == "" {
		window = item.WindowKind
	}
	return window
}

func predictionItemID(item PredictionDigestItem) string {
	return ItemID(AlertQuotaLow, item.Provider, predictionKey(item), predictionScope(item))
}

func announcedInMinute(state *State, identity, exceptID string, now time.Time) bool {
	for _, notification := range state.Outbox {
		if notification.ID == exceptID || notification.CreatedAt.UTC().Truncate(time.Minute) != now.UTC().Truncate(time.Minute) {
			continue
		}
		for _, alert := range notification.Alerts {
			if alertItemID(alert) == identity {
				return true
			}
		}
	}
	return false
}
