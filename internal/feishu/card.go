package feishu

import (
	"strconv"
	"strings"
)

// maxCardElements bounds the number of card elements so a provider with many
// accounts cannot exceed Feishu's per-card component ceiling and make the whole
// send fail. The renderer keeps the summary lines and drops the tail.
const maxCardElements = 180

// Card renders the full Feishu interactive card (schema 2.0).
//
// The structure is deliberately minimal — a header and markdown lines — so a
// later designer can take it over without untangling charts or tables. It carries
// the same exact evidence as Text: numbers, scopes, reset times and data time.
func (r *PlainReportRenderer) Card(msg Message) map[string]any {
	return r.card(msg, true)
}

// CardSimple renders the degraded card: the same evidence as markdown, with no
// extra config. It exists because strict JSON 2.0 rejects a card wholesale when
// one element is malformed, so the sender retries with this shape.
func (r *PlainReportRenderer) CardSimple(msg Message) map[string]any {
	return r.card(msg, false)
}

func (r *PlainReportRenderer) card(msg Message, full bool) map[string]any {
	config := map[string]any{"update_multi": true}
	if full {
		// The documented compact width fits the short lines; the fallback keeps
		// the bare config so it can never be rejected over this field.
		config["width_mode"] = "compact"
	}
	return map[string]any{
		"schema": "2.0",
		"config": config,
		"header": map[string]any{
			"title":    map[string]any{"tag": "plain_text", "content": r.Title(msg)},
			"template": cardColor(msg),
		},
		"body": map[string]any{"elements": r.cardElements(msg, full)},
	}
}

func (r *PlainReportRenderer) cardElements(msg Message, full bool) []map[string]any {
	elements := make([]map[string]any, 0, 8)

	meta := ""
	if !msg.GeneratedAt.IsZero() {
		meta = "观察时间 " + r.formatTime(msg.GeneratedAt)
	}
	if msg.Freshness != "" {
		meta += " · " + msg.Freshness
	}
	if meta != "" {
		elements = append(elements, markdown(meta))
	}
	if msg.Notice != "" {
		elements = append(elements, markdown("⚠️ "+oneLine(msg.Notice)))
	}
	if msg.Body != "" {
		elements = append(elements, markdown(oneLine(msg.Body)))
	}
	if msg.Report != nil {
		for _, p := range msg.Report.Providers {
			content := strings.TrimRight(r.providerCardBlock(p, msg.Detailed), "\n")
			if content != "" {
				elements = append(elements, markdown(content))
			}
		}
	}
	for _, alert := range msg.Alerts {
		elements = append(elements, markdown(strings.TrimRight(r.alertCardBlock(alert), "\n")))
	}
	if len(msg.Pending) > 0 {
		elements = append(elements, markdown(pendingBlock(msg.Pending)))
	}
	if len(msg.Tight) > 0 {
		elements = append(elements, markdown(tightBlock(msg.Tight)))
	}
	if len(elements) == 0 {
		elements = append(elements, markdown("暂无可展示的额度信息。"))
	}
	if len(elements) > maxCardElements {
		elements = elements[:maxCardElements]
	}
	return elements
}

func (r *PlainReportRenderer) providerCardBlock(p ProviderReport, detailed bool) string {
	var b strings.Builder
	b.WriteString("**" + providerName(p.Provider) + "**\n")
	if p.Error != "" {
		b.WriteString("渠道错误：" + oneLine(p.Error) + "\n")
		return b.String()
	}
	for _, account := range p.Accounts {
		label := account.Label
		if label == "" {
			label = "账号"
		}
		b.WriteString(label + " · " + stateLabel(account.State) + disabledSuffix(account.Disabled) + "\n")
		shown := account.Windows
		if !detailed && len(shown) > 2 {
			shown = shown[:2]
		}
		for _, window := range shown {
			b.WriteString("　" + r.windowLine(window) + "\n")
		}
		if !detailed && len(account.Windows) > len(shown) {
			b.WriteString("　…另有 " + strconv.Itoa(len(account.Windows)-len(shown)) + " 个窗口\n")
		}
	}
	return b.String()
}

func (r *PlainReportRenderer) alertCardBlock(alert Alert) string {
	var b strings.Builder
	b.WriteString("**【" + severityLabel(alert.Severity) + "】" + oneLine(alert.Title) + "**\n")
	if alert.Detail != "" {
		b.WriteString(oneLine(alert.Detail) + "\n")
	}
	for _, fact := range alert.Facts {
		b.WriteString("- " + oneLine(fact) + "\n")
	}
	if alert.Advice != "" {
		b.WriteString("建议：" + oneLine(alert.Advice) + "\n")
	}
	return b.String()
}

func pendingBlock(items []PendingItem) string {
	var b strings.Builder
	b.WriteString("**待处理**\n")
	for _, item := range items {
		b.WriteString("- [" + item.Kind + "] " + oneLine(item.Action) + "\n")
	}
	return b.String()
}

func tightBlock(tight []TightChannel) string {
	var b strings.Builder
	b.WriteString("**额度偏紧**\n")
	for _, channel := range tight {
		b.WriteString("- " + providerName(channel.Provider) + " 最佳账号剩余 " +
			formatRemaining(channel.BestRemaining) + "\n")
	}
	return b.String()
}

func markdown(content string) map[string]any {
	return map[string]any{"tag": "markdown", "content": content}
}

func cardColor(msg Message) string {
	for _, alert := range msg.Alerts {
		switch alert.Severity {
		case SeverityUrgent:
			return "red"
		case SeverityWarn:
			return "orange"
		}
	}
	if len(msg.Pending) > 0 {
		return "orange"
	}
	return "blue"
}

func formatRemaining(value float64) string {
	return strconv.Itoa(int(value+0.5)) + "%"
}
