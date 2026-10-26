package feishu

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	AlertQuotaLow       = "quota_low"
	AlertQuotaExhausted = "quota_exhausted"
	AlertReadingMissing = "reading_missing"
	maxDecisionPoints   = 3
)

// Reset time is a recovery clock, never an estimate of remaining traffic time.
func attentionSignals(rep Report, threshold float64, now time.Time) []Alert {
	if threshold <= 0 {
		threshold = DefaultTightRemainingPercent
	}
	var alerts []Alert
	for _, provider := range rep.Providers {
		if provider.Error != "" || len(provider.Accounts) == 0 {
			alerts = append(alerts, Alert{Kind: AlertReadingMissing, Provider: provider.Provider, Title: "数据缺失", Advice: "检查采集链路后再决定额度安排", Severity: SeverityWarn, OccurredAt: now})
			continue
		}
		for _, account := range provider.Accounts {
			if account.Disabled || account.IsUnmonitored || account.State == StateInvalid {
				continue
			}
			alert := Alert{Provider: provider.Provider, Key: account.Key, Alias: account.Label, Severity: SeverityWarn, OccurredAt: now}
			alert.ObservedAt = account.ObservedAt
			if account.AuthFailed {
				alert.Kind, alert.Title, alert.Advice = AlertReadingMissing, "数据缺失（认证状态待确认）", "检查凭证认证与最新采集结果"
				alerts = append(alerts, alert)
				continue
			}
			if !account.OK || !observationFresh(account, now, defaultFreshWindow) || !usableState(account.State) && account.State != StateExhausted {
				alert.Kind, alert.Title, alert.Advice = AlertReadingMissing, "数据缺失（读数异常或已过期）", "检查采集链路后再决定额度安排"
				alerts = append(alerts, alert)
				continue
			}
			hasReading, hasMissing := false, false
			for _, window := range account.Windows {
				if !validPercent(window.UsedPercent) {
					hasMissing = true
					continue
				}
				hasReading = true
				remaining := 100 - *window.UsedPercent
				if remaining > threshold {
					continue
				}
				candidate := alert
				candidate.Kind, candidate.Title, candidate.Advice = AlertQuotaLow, "额度将耗尽", "提前准备备用凭证，按需调整使用"
				candidate.Scope = quotaSignalScope(window)
				if remaining <= 0 {
					candidate.Kind, candidate.Title, candidate.Advice, candidate.Severity = AlertQuotaExhausted, "额度已耗尽", "使用备用凭证或等待重置", SeverityUrgent
				}
				candidate.Detail = fmt.Sprintf("%s · %s · 剩余 %.1f%%；%s", scopeLabel(window.Scope), windowLabel(window), remaining, resetDeadline(window.ResetAt, now))
				alerts = append(alerts, candidate)
			}
			if !hasReading || hasMissing {
				alert.Kind, alert.Title, alert.Advice = AlertReadingMissing, "数据缺失（未上报额度）", "检查采集结果后再决定额度安排"
				alerts = append(alerts, alert)
			}
		}
	}
	return alerts
}

func validPercent(value *float64) bool {
	return value != nil && !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0 && *value <= 100
}

func resetDeadline(reset *time.Time, now time.Time) string {
	if reset == nil || reset.IsZero() {
		return "重置时间数据缺失，耗尽时间无法估计"
	}
	if !reset.After(now) {
		return "重置时间已到，等待新读数"
	}
	return "距重置 " + remainingDuration(reset.Sub(now))
}

func remainingDuration(duration time.Duration) string {
	minutes := int(math.Ceil(duration.Minutes()))
	if minutes < 60 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return fmt.Sprintf("%d 小时 %d 分钟", minutes/60, minutes%60)
}

func attentionRecovered(item PendingItem, rep Report, threshold float64, now time.Time) bool {
	if item.Kind != AlertQuotaLow && item.Kind != AlertQuotaExhausted && item.Kind != AlertReadingMissing {
		return false
	}
	if threshold <= 0 {
		threshold = DefaultTightRemainingPercent
	}
	for _, provider := range rep.Providers {
		if provider.Provider != item.Provider {
			continue
		}
		if provider.Error != "" {
			return false
		}
		for _, account := range provider.Accounts {
			if item.Key != "" && account.Key != item.Key {
				continue
			}
			if account.Disabled || account.IsUnmonitored {
				return true
			}
			if !credentialRecovered(account) || !observationFresh(account, now, defaultFreshWindow) {
				return false
			}
			hasReading := false
			for _, window := range account.Windows {
				if !validPercent(window.UsedPercent) {
					return false
				}
				hasReading = true
				if (item.Kind == AlertQuotaLow || item.Kind == AlertQuotaExhausted) && ItemID(AlertQuotaLow, provider.Provider, account.Key, quotaSignalScope(window)) != item.ID {
					continue
				}
				if item.Kind == AlertQuotaLow && 100-*window.UsedPercent <= threshold || item.Kind == AlertQuotaExhausted && *window.UsedPercent >= 100 {
					return false
				}
			}
			if hasReading {
				return true
			}
		}
	}
	return false
}

func rankAlert(alert Alert) int {
	if alert.Severity == SeverityUrgent {
		return 0
	}
	if alert.Kind == AlertReadingMissing {
		return 1
	}
	return 2
}

func quotaSignalScope(window Window) string {
	scope := window.Name
	if window.ResetAt != nil {
		scope += "@" + fmt.Sprint(window.ResetAt.UnixMilli())
	}
	return scope
}

func quotaEpisodeEnd(item PendingItem, report Report) time.Time {
	for _, provider := range report.Providers {
		if provider.Provider != item.Provider {
			continue
		}
		for _, account := range provider.Accounts {
			if account.Key != item.Key {
				continue
			}
			for _, window := range account.Windows {
				if ItemID(AlertQuotaLow, provider.Provider, account.Key, quotaSignalScope(window)) == item.ID && window.ResetAt != nil {
					return *window.ResetAt
				}
			}
		}
	}
	return time.Time{}
}

func decisionAlerts(msg Message) []Alert {
	alerts := append([]Alert(nil), msg.Alerts...)
	if msg.EvaluatedAt.IsZero() {
		msg.EvaluatedAt = msg.GeneratedAt
	}
	if msg.Report != nil {
		alerts = append(alerts, attentionSignals(*msg.Report, DefaultTightRemainingPercent, msg.EvaluatedAt)...)
		for _, provider := range msg.Report.Providers {
			for _, account := range provider.Accounts {
				if !account.Disabled && account.State == StateInvalid {
					alerts = append(alerts, credentialAlert(provider.Provider, account, msg.EvaluatedAt))
				}
			}
		}
	}
	identities := map[string]int{}
	merged := make([]Alert, 0, len(alerts))
	for _, alert := range alerts {
		identity := alertItemID(alert)
		if position, exists := identities[identity]; exists {
			if rankAlert(alert) < rankAlert(merged[position]) || merged[position].Kind == predictionKindPrefix && alert.Kind != predictionKindPrefix {
				merged[position] = alert
			}
			continue
		}
		identities[identity] = len(merged)
		merged = append(merged, alert)
	}
	alerts = merged
	for _, item := range msg.Pending {
		found := false
		for _, alert := range alerts {
			if alertItemID(alert) == item.ID {
				found = true
				break
			}
		}
		if !found {
			title := defaultTitles[item.Kind]
			if item.Kind == AlertQuotaLow {
				title = "额度偏紧，最新读数待确认"
			}
			if item.Kind == AlertQuotaExhausted {
				title = "额度耗尽，恢复待确认"
			}
			if item.Kind == AlertReadingMissing {
				title = "数据缺失"
			}
			if title == "" {
				title = "异常仍待确认"
			}
			alerts = append(alerts, Alert{Kind: item.Kind, Provider: item.Provider, Key: item.Key, Alias: item.Alias, Title: title, Advice: item.Action, Severity: SeverityWarn})
		}
	}
	for _, tight := range msg.Tight {
		if len(alerts) == 0 {
			alerts = append(alerts, Alert{Kind: AlertQuotaLow, Provider: tight.Provider, Title: "额度偏紧", Detail: "凭证明细数据缺失", Advice: "准备备用凭证并检查最新读数", Severity: SeverityWarn})
		}
	}
	if len(alerts) > 1 {
		filtered := alerts[:0]
		for _, alert := range alerts {
			if alert.Kind != statusHealthy {
				filtered = append(filtered, alert)
			}
		}
		alerts = filtered
	}
	sort.SliceStable(alerts, func(i, j int) bool { return rankAlert(alerts[i]) < rankAlert(alerts[j]) })
	return alerts
}

func decisionConclusion(msg Message, alerts []Alert) string {
	if len(alerts) == 1 && alerts[0].Kind == statusHealthy {
		return "今天暂不需要调整额度，未发现异常"
	}
	for _, alert := range alerts {
		if alert.Kind == AlertQuotaLow || alert.Kind == AlertQuotaExhausted || strings.HasPrefix(alert.Kind, predictionKindPrefix) && alert.Title != "预计额度有余量" || alert.Kind == AlertChannelUnavailable {
			return "今天需要调整额度安排，请处理以下风险"
		}
	}
	if len(alerts) > 0 {
		hasOnlySurplus := true
		for _, alert := range alerts {
			if alert.Title != "预计额度有余量" && alert.Kind != "omitted" {
				hasOnlySurplus = false
			}
		}
		if hasOnlySurplus {
			return "今天暂不需要增加额度，可按需使用余量"
		}
	}
	if len(alerts) > 0 {
		return "今天先处理异常，再决定额度安排"
	}
	if msg.Report == nil || len(msg.Report.Providers) == 0 {
		return "数据缺失，今天是否调整额度尚无法判断"
	}
	return "今天暂不需要调整额度，未发现异常"
}

func decisionPoint(alert Alert) string {
	identity := providerName(alert.Provider)
	if alert.Kind == statusHealthy {
		identity = "额度"
	}
	if alert.Alias != "" {
		identity += " · " + alert.Alias
	}
	advice := alert.Advice
	if advice == "" {
		advice = "检查控制台中的最新读数"
	}
	return compactText(identity+"："+alert.Title+"。"+alert.Detail, 180) + "；建议：" + compactText(advice, 70)
}

func compactText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	// Labels are data, so they must not become mentions, links or formatting.
	value = strings.NewReplacer("<", "〈", ">", "〉", "[", "［", "]", "］", "*", "＊", "_", "＿").Replace(value)
	value = strings.ReplaceAll(value, string(rune(96)), "｀")
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}
