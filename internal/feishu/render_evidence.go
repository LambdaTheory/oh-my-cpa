package feishu

import (
	"fmt"
	"strings"
	"time"
)

func messageTime(msg Message) time.Time {
	if !msg.EvaluatedAt.IsZero() {
		return msg.EvaluatedAt
	}
	return msg.GeneratedAt
}

func evidenceLabel(source string) string {
	switch source {
	case EvidenceQuota:
		return "额度观察"
	case EvidenceAuthentication:
		return "认证信号"
	case EvidenceAttempt:
		return "最近尝试"
	case EvidenceMonitoring:
		return "最后有效数据"
	case EvidenceAvailability:
		return "可用性证据"
	default:
		return "证据来源未知"
	}
}

func (r *PlainReportRenderer) alertClock(alert Alert) string {
	label := evidenceLabel(alert.EvidenceSource)
	if !isMonitoringTimeValid(alert.ObservedAt) {
		return label + "：时间未知"
	}
	return label + " " + r.formatTime(alert.ObservedAt)
}

func (r *PlainReportRenderer) compactBlockClock(block compactBlock) string {
	label := evidenceLabel(block.evidenceSource)
	if !isMonitoringTimeValid(block.observed) {
		return label + "：时间未知"
	}
	if len(block.accounts) > 1 {
		label = "最早" + label
	}
	if block.hasUnknownTime {
		return label + "（部分未知）\n" + r.formatTime(block.observed)
	}
	return label + " " + r.formatTime(block.observed)
}

func decisionRiskAlerts(alerts []Alert) []Alert {
	var risks []Alert
	for _, alert := range alerts {
		if alert.Kind != statusHealthy && alert.Kind != statusUnknown && alert.Kind != statusReported && alert.Kind != "omitted" {
			risks = append(risks, alert)
		}
	}
	return risks
}

func (r *PlainReportRenderer) diagnosticClock(label string, observed, now time.Time) string {
	if !isMonitoringTimeValid(observed) {
		return label + "：时间未知"
	}
	text := label + " " + r.formatTime(observed)
	if !evidenceFresh(observed, now, defaultFreshWindow) {
		age := now.Sub(observed)
		if age >= 24*time.Hour {
			return text + fmt.Sprintf("（已过期，数据 %d 天前）", int(age.Hours()/24))
		}
		return text + fmt.Sprintf("（已过期，数据 %d 分钟前）", int(age.Minutes()))
	}
	return text
}

func attemptResult(account AccountReport) string {
	outcome := map[string]string{"success": "成功", "failed": "失败", "rate_limited": "限流"}[account.AttemptOutcome]
	if outcome == "" {
		outcome = "结果未知"
	}
	reason := map[string]string{"auth": "认证拒绝", "timeout": "超时", "network": "网络", "http": "HTTP", "parse": "解析", "unknown": "原因未知"}[account.AttemptReason]
	if reason != "" {
		outcome += "/" + reason
	}
	return outcome
}

func accountIdentity(provider string, account AccountReport) string {
	identity := providerName(provider)
	if account.Label != "" && account.Label != identity {
		identity += " · " + account.Label
	}
	return compactText(identity, 70)
}

// Diagnostics retain historical ownership without repeating its old instructions.
// The notification's selected pending items, not the entire persisted backlog,
// determine which history may be displayed (including startup/morning dedup).
func (r *PlainReportRenderer) diagnosticText(msg Message, visible []Alert) string {
	now := messageTime(msg)
	visibleAccounts := map[string]bool{}
	current := map[string]bool{}
	for _, alert := range decisionRiskAlerts(decisionAlerts(msg)) {
		current[alertItemID(alert)] = true
	}
	for _, alert := range visible {
		visibleAccounts[alert.Provider+"/"+alert.Key] = true
	}
	const limit = 2
	rows := []string{}
	count := 0
	accounts := map[string]bool{}
	if msg.Report != nil {
		for _, provider := range msg.Report.Providers {
			if provider.Error != "" && len(provider.Accounts) == 0 {
				count++
				if len(rows) < limit {
					rows = append(rows, compactText(providerName(provider.Provider), 40)+"：采集结果未知，额度观察时间未知")
				}
			}
			for _, account := range provider.Accounts {
				key := provider.Provider + "/" + account.Key
				if account.Disabled || account.IsUnmonitored || hasFreshQuotaReading(account, now) {
					continue
				}
				// A current auth episode omitted by the card's selection/row cap
				// must not reappear through an unrelated numeric diagnostic.
				if account.HasFreshAuthFailure(now) && !visibleAccounts[key] {
					continue
				}
				accounts[key] = true
				count++
				if len(rows) >= limit {
					continue
				}
				lines := []string{accountIdentity(provider.Provider, account),
					r.diagnosticClock("额度观察", account.ObservedAt, now),
					r.diagnosticClock("最后成功", account.LastSuccessAt, now),
					r.diagnosticClock("最近尝试", account.AttemptedAt, now) + "（" + attemptResult(account) + "）"}
				if !isMonitoringTimeValid(account.AttemptedAt) && isMonitoringTimeValid(account.CollectionSignalObservedAt) {
					lines = append(lines, r.diagnosticClock("非认证采集事件", account.CollectionSignalObservedAt, now))
				}
				if account.AuthFailed || account.State == StateInvalid {
					auth := r.diagnosticClock("认证信号", account.AuthSignalObservedAt, now)
					if !account.HasFreshAuthFailure(now) {
						auth += "（历史未结，当前认证未知）"
					}
					lines = append(lines, auth)
				}
				rows = append(rows, strings.Join(lines, "；"))
			}
		}
	}
	for _, item := range msg.Pending {
		key := item.Provider + "/" + item.Key
		if item.Status != PendingOpen || current[item.ID] || accounts[key] {
			continue
		}
		count++
		if len(rows) < limit {
			identity := accountIdentity(item.Provider, AccountReport{Label: item.Alias})
			rows = append(rows, identity+"：历史待核验（事件登记 "+r.formatTime(item.Since)+"；当前证据未知，不代表当前风险）")
		}
	}
	if count == 0 {
		return ""
	}
	text := "数据质量诊断（与当前风险分列）\n" + strings.Join(rows, "\n")
	if count > len(rows) {
		text += fmt.Sprintf("\n另有 %d 项诊断见控制台", count-len(rows))
	}
	return text
}

func (r *PlainReportRenderer) healthyClock(msg Message) string {
	_, _, _, earliest := compactHealthyEvidence(msg)
	if earliest.IsZero() {
		return "证据时间未知"
	}
	return "健康额度观察（组内最早） " + r.formatTime(earliest)
}
