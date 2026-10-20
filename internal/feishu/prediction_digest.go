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
// so it cannot suppress either configured daily digest.
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
	service.deliverOutboxNotification(ctx, resolved, notification.ID)

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
	if !resolved.Ready() || !scheduledMinute(resolved, now) {
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
	if sourceErr != nil || !hasPredictionFindings(digest.Items) {
		if service.source != nil {
			report, hasReport = service.source.ReadReport(ctx)
		}
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
	if !withinQuiet(resolved, now) {
		service.deliverOutbox(ctx, resolved)
	}
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

func hasPredictionFindings(items []PredictionDigestItem) bool {
	for _, item := range items {
		if item.Verdict == "will_exhaust" || item.Verdict == "under_used" {
			return true
		}
	}
	return false
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
	if state == nil || !resolved.Ready() || !scheduledMinute(resolved, now) || sameSummaryMinute(state.LastSummaryAt, resolved, now) {
		return false
	}
	findings := predictionFindings(digest.Items)
	if len(findings) == 0 {
		return false
	}
	state.LastSummaryAt = now
	state.queueNotification(predictionNotification(findings, digest.Omitted, resolved, now))
	return true
}

func predictionNotification(items []PredictionDigestItem, omitted int, resolved Resolved, now time.Time) Notification {
	alerts := make([]Alert, 0, len(items)+1)
	newestObservedAtMS := int64(0)
	for _, item := range items {
		if item.ObservedAtMS > newestObservedAtMS {
			newestObservedAtMS = item.ObservedAtMS
		}
		facts := []string{fmt.Sprintf("当前已用 %s；预计重置时已用 %s；结论 %s", predictionPercent(item.UsedPercent), predictionPercent(item.ProjectedPercentAtReset), item.Verdict)}
		if item.ProjectedTokensAtReset != nil && item.ProjectedCostNanosAtReset != nil {
			facts = append(facts, fmt.Sprintf("预计重置时 %d tokens；成本 $%.4f", *item.ProjectedTokensAtReset, float64(*item.ProjectedCostNanosAtReset)/1_000_000_000))
		}
		alerts = append(alerts, Alert{
			Kind: predictionKindPrefix, Severity: SeverityWarn, Provider: item.Provider,
			Alias: item.CredentialLabel, Title: item.CredentialLabel + " · " + item.WindowKind,
			Facts: facts, OccurredAt: now,
		})
	}
	footer := "数据截至时间未知；预测是基于已记录观测与使用历史的推演，不是保证。"
	if newestObservedAtMS > 0 {
		footer = "数据截至 " + time.UnixMilli(newestObservedAtMS).In(resolved.Location).Format("2006-01-02 15:04") + "；预测是基于已记录观测与使用历史的推演，不是保证。"
	}
	if omitted > 0 {
		footer += fmt.Sprintf(" 另有 %d 项预测发现未展示；请在 OMC 中查看完整预测。", omitted)
	}
	alerts = append(alerts, Alert{Kind: predictionKindPrefix, Title: "数据说明", Facts: []string{footer}, OccurredAt: now})
	localMinute := now.In(resolved.Location).Format("15:04")
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
	if value == nil {
		return "未知"
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
