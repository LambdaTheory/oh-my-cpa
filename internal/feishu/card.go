package feishu

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

func (r *PlainReportRenderer) Card(msg Message) map[string]any       { return r.card(msg, true) }
func (r *PlainReportRenderer) CardSimple(msg Message) map[string]any { return r.card(msg, false) }

func (r *PlainReportRenderer) card(msg Message, isFull bool) map[string]any {
	if usesCompactCard(msg.Kind) {
		return r.compactCard(msg)
	}
	config := map[string]any{"update_multi": true}
	if isFull {
		config["width_mode"] = "compact"
	}
	alerts := decisionAlerts(msg)
	elements := []map[string]any{markdown("**" + decisionConclusion(msg, alerts) + "**")}
	if msg.Kind == KindQuery {
		if msg.Body != "" && decisionConclusion(msg, alerts) != msg.Body {
			elements = append(elements, markdown(msg.Body))
		}
		if msg.Notice != "" {
			elements = append(elements, markdown(msg.Notice))
		}
	}
	count := min(len(alerts), maxDecisionPoints)
	for _, alert := range alerts[:count] {
		elements = append(elements, markdown("• "+decisionPoint(alert)))
	}
	observedAt := time.Time{}
	if msg.Report != nil {
		observedAt = msg.Report.GeneratedAt
	}
	for _, alert := range alerts {
		if !alert.ObservedAt.IsZero() && (observedAt.IsZero() || alert.ObservedAt.Before(observedAt)) {
			observedAt = alert.ObservedAt
		}
	}
	meta := "观察时间数据缺失"
	if !observedAt.IsZero() {
		meta = "观察时间 " + r.formatTime(observedAt)
	}
	if msg.Freshness != "" {
		meta += " · " + compactText(msg.Freshness, 40)
	}
	if len(alerts) > count {
		meta += fmt.Sprintf(" · 另有 %d 项，请在控制台查看", len(alerts)-count)
	}
	elements = append(elements, markdown(meta))
	elements = append(elements, r.consoleButton())
	return map[string]any{"schema": "2.0", "config": config, "header": map[string]any{
		"title": map[string]any{"tag": "plain_text", "content": r.Title(msg)}, "template": cardColor(msg),
	}, "body": map[string]any{"elements": elements}}
}

func (r *PlainReportRenderer) consoleButton() map[string]any {
	button := map[string]any{"tag": "button", "type": "primary", "width": "fill", "text": map[string]any{"tag": "plain_text", "content": "打开控制台"}}
	parsed, err := url.Parse(r.consoleURL)
	if err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" {
		button["behaviors"] = []map[string]any{{"type": "open_url", "default_url": parsed.String()}}
	} else {
		button["disabled"] = true
		button["disabled_tips"] = map[string]any{"tag": "plain_text", "content": "请配置 OMCPA_PUBLIC_URL"}
	}
	return button
}

func markdown(content string) map[string]any {
	return map[string]any{"tag": "markdown", "content": content}
}

func cardColor(msg Message) string {
	color := "blue"
	for _, alert := range decisionAlerts(msg) {
		if alert.Severity == SeverityUrgent {
			return "red"
		}
		if alert.Kind != statusHealthy {
			color = "orange"
		}
	}
	return color
}

// This marker distinguishes a healthy morning digest from absent report data.
const statusHealthy = "status_healthy"

func reportDecisionAlerts(rep Report, has bool, now time.Time) []Alert {
	if !has || len(rep.Providers) == 0 {
		return []Alert{{Kind: AlertReadingMissing, Title: "数据缺失", Advice: "检查采集链路后再决定额度安排", Severity: SeverityWarn, OccurredAt: now}}
	}
	alerts := decisionAlerts(Message{Report: &rep, EvaluatedAt: now})
	if len(alerts) == 0 {
		return []Alert{{Kind: statusHealthy, Title: "当前读数未发现异常", Advice: "保持现有额度安排", OccurredAt: now, ObservedAt: rep.GeneratedAt}}
	}
	return alerts
}

func ConsoleAddress(publicURL, basePath string) string {
	address := strings.TrimRight(publicURL, "/")
	basePath = strings.TrimRight(basePath, "/")
	if address != "" && basePath != "" && !strings.HasSuffix(address, basePath) {
		address += basePath
	}
	return address
}
