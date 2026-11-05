package feishu

import (
	"fmt"
	"strings"
	"time"
)

// The prediction card is deliberately its own layout. The generic decision card
// concatenates identity, title, detail and advice into one long sentence per
// point; a quota forecast is a short list of windows, so the row carries the
// identity, two numbers and one sentence, and nothing else.
const (
	predictionCardRowLimit = 5
	predictionSentenceMax  = 120
	predictionIdentityMax  = 48
	predictionPendingMax   = 5
)

func (r *PlainReportRenderer) predictionCard(msg Message) map[string]any {
	rows := make([]Alert, 0, len(msg.Alerts))
	others := make([]Alert, 0, len(msg.Alerts))
	for _, alert := range msg.Alerts {
		if strings.HasPrefix(alert.Kind, predictionKindPrefix) {
			rows = append(rows, alert)
		} else {
			others = append(others, alert)
		}
	}
	total := len(rows) + msg.Omitted
	count := min(len(rows), predictionCardRowLimit)
	config := map[string]any{"update_multi": true, "width_mode": "compact"}
	elements := make([]map[string]any, 0, len(msg.Alerts)+4)
	switch {
	case total > 0:
		elements = append(elements, markdown(fmt.Sprintf("**%d 个窗口撑不到重置**", total)))
	case len(others) > 0:
		elements = append(elements, markdown("**其他需要处理**"))
	}
	for _, alert := range rows[:count] {
		elements = append(elements, markdown(predictionRow(alert)))
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
			elements = append(elements, markdown("• "+decisionPoint(alert)))
		}
		if omitted := len(others) - otherCount; omitted > 0 {
			elements = append(elements, markdown(fmt.Sprintf("另有 %d 项需要处理，见控制台", omitted)))
		}
	}
	if pending := predictionPendingLine(msg.Pending); pending != "" {
		elements = append(elements, markdown(pending))
	}
	meta := ""
	if len(rows) > 0 {
		meta = "读数 " + r.predictionReadTime(rows)
	}
	if omitted := total - count; omitted > 0 {
		if meta != "" {
			meta += " · "
		}
		meta += fmt.Sprintf("另有 %d 项，见控制台", omitted)
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

// predictionPendingLine keeps the open items that ride along with the card in
// one compact line, so the identity a person needs is still visible without
// spending an element per item.
func predictionPendingLine(items []PendingItem) string {
	if len(items) == 0 {
		return ""
	}
	labels := make([]string, 0, min(len(items), predictionPendingMax))
	for _, item := range items[:min(len(items), predictionPendingMax)] {
		label := providerName(item.Provider)
		if item.Alias != "" {
			label += " · " + item.Alias
		}
		labels = append(labels, compactText(label, predictionIdentityMax))
	}
	line := "<font color='grey'>待处理：" + strings.Join(labels, "、")
	if omitted := len(items) - len(labels); omitted > 0 {
		line += fmt.Sprintf(" 等 %d 项", len(items))
	}
	return line + "</font>"
}

// predictionReadTime reports the newest observation among the rows. An older
// row must never make a fresh card look stale, so the maximum, not the earliest,
// is the honest read age.
func (r *PlainReportRenderer) predictionReadTime(rows []Alert) string {
	latest := time.Time{}
	for _, alert := range rows {
		if alert.ObservedAt.After(latest) {
			latest = alert.ObservedAt
		}
	}
	return r.formatShort(latest)
}
