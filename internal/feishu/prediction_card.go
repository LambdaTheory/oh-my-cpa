package feishu

import (
	"fmt"
	"strings"
	"time"
)

// The prediction card is deliberately its own layout. The generic decision card
// concatenates identity, title, detail and advice into one long sentence per
// point; a quota forecast is a short list of windows, so the row carries the
// identity, two numbers, one sentence and its evidence clock.
const (
	predictionCardRowLimit = 5
	predictionSentenceMax  = 120
	predictionIdentityMax  = 48
	predictionPendingMax   = 5
)

func (r *PlainReportRenderer) predictionCard(msg Message) map[string]any {
	rows := make([]Alert, 0, len(msg.Alerts))
	others := make([]Alert, 0, len(msg.Alerts))
	for _, alert := range decisionRiskAlerts(msg.Alerts) {
		if strings.HasPrefix(alert.Kind, predictionKindPrefix) {
			rows = append(rows, alert)
		} else {
			others = append(others, alert)
		}
	}
	total := len(rows) + msg.Omitted
	count := min(len(rows), predictionCardRowLimit)
	visible := make([]Alert, 0, count+min(len(others), maxDecisionPoints))
	config := map[string]any{"update_multi": true, "width_mode": "compact"}
	elements := make([]map[string]any, 0, len(msg.Alerts)+4)
	switch {
	case total > 0:
		elements = append(elements, markdown(fmt.Sprintf("**%d 个窗口撑不到重置**", total)))
	case len(others) > 0:
		elements = append(elements, markdown("**其他需要处理**"))
	default:
		elements = append(elements, markdown("**"+decisionConclusion(msg, msg.Alerts)+"**"))
	}
	for _, alert := range rows[:count] {
		elements = append(elements, markdown(predictionRow(alert)+"\n"+r.alertClock(alert)))
		visible = append(visible, alert)
	}
	// The morning card appends actionable non-prediction alerts (channel
	// unavailable, credential invalid) to the same notification. They are what a
	// person has to act on, so they follow the forecast rows instead of being
	// filtered away with the prediction-kind check above.
	if len(others) > 0 {
		if total > 0 {
			elements = append(elements, markdown("**其他需要处理**"))
		}
		otherCount := min(len(others), maxDecisionPoints)
		for _, alert := range others[:otherCount] {
			elements = append(elements, markdown("• "+decisionPoint(alert)+"\n"+r.alertClock(alert)))
			visible = append(visible, alert)
		}
		if omitted := len(others) - otherCount; omitted > 0 {
			elements = append(elements, markdown(fmt.Sprintf("另有 %d 项需要处理，见控制台", omitted)))
		}
	}
	if pending := predictionPendingLine(msg.Pending, visible); pending != "" {
		elements = append(elements, markdown(pending))
	}
	meta := ""
	if count > 0 {
		meta = "已展示预测最新读数 " + r.predictionReadTime(rows[:count]) + " · 各项时间仅对应标注的证据来源"
	}
	if omitted := total - count; omitted > 0 {
		if meta != "" {
			meta += " · "
		}
		meta += fmt.Sprintf("另有 %d 项，见控制台", omitted)
	}
	if diagnostic := r.diagnosticText(msg, visible); diagnostic != "" {
		if meta != "" {
			meta += "\n"
		}
		meta += diagnostic
	}
	if meta != "" {
		elements = append(elements, markdown(meta))
	}
	elements = append(elements, r.consoleButton())
	return map[string]any{"schema": "2.0", "config": config, "header": map[string]any{
		"title": map[string]any{"tag": "plain_text", "content": r.Title(msg)}, "template": "orange",
	}, "body": map[string]any{"elements": elements}}
}

func predictionRow(alert Alert) string {
	identity := providerName(alert.Provider)
	if label := strings.TrimSpace(alert.Alias); label != "" {
		identity += " · " + label
	}
	if alert.Title != "" {
		identity += " · " + alert.Title
	}
	lines := []string{"• " + compactText(identity, predictionIdentityMax)}
	if len(alert.Facts) > 0 {
		lines = append(lines, "<font color='grey'>"+compactText(strings.Join(alert.Facts, " · "), predictionSentenceMax)+"</font>")
	}
	if alert.Detail != "" {
		lines = append(lines, compactText(alert.Detail, predictionSentenceMax))
	}
	return strings.Join(lines, "\n")
}

// Pending ownership earns a current-action label only when a displayed alert
// supplies its evidence. Other open items stay in the bounded history diagnostic.
func predictionPendingLine(items []PendingItem, visible []Alert) string {
	current := make(map[string]bool, len(visible))
	for _, alert := range visible {
		current[alertItemID(alert)] = true
	}
	labels := make([]string, 0, min(len(items), predictionPendingMax))
	count := 0
	for _, item := range items {
		if item.Status != PendingOpen || !current[item.ID] {
			continue
		}
		count++
		if len(labels) >= predictionPendingMax {
			continue
		}
		label := providerName(item.Provider)
		if item.Alias != "" {
			label += " · " + item.Alias
		}
		labels = append(labels, compactText(label, predictionIdentityMax))
	}
	if count == 0 {
		return ""
	}
	line := "<font color='grey'>待处理：" + strings.Join(labels, "、")
	if count > len(labels) {
		line += fmt.Sprintf(" 等 %d 项", count)
	}
	return line + "</font>"
}

// The latest-read hint summarizes only displayed forecast rows. Each row's
// source-specific clock, including diagnostics, remains independent of it.
func (r *PlainReportRenderer) predictionReadTime(rows []Alert) string {
	latest := time.Time{}
	for _, alert := range rows {
		if isMonitoringTimeValid(alert.ObservedAt) && alert.ObservedAt.After(latest) {
			latest = alert.ObservedAt
		}
	}
	if latest.IsZero() {
		return "时间未知"
	}
	return r.formatShort(latest)
}
