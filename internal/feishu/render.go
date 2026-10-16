package feishu

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// KindQuery marks an on-demand status reply (the @-mention / private-chat query
// path).
const KindQuery = "query"

// KindSummary marks a scheduled or quiet-end digest.
const KindSummary = "summary"

// KindAlertBatch marks a quiet-end merge that carries several held immediate
// alerts in a single notification.
const KindAlertBatch = "alert_batch"

// defaultTitles map an alert kind onto its human title. A summary or a query has
// its own title.
var defaultTitles = map[string]string{
	AlertCredential:         "凭证失效",
	AlertChannelUnavailable: "渠道不可用",
	AlertMonitorDown:        "监控停摆",
	KindQuery:               "额度查询",
	KindSummary:             "额度汇总",
	KindAlertBatch:          "异常提醒",
}

// Message is the channel-agnostic view model both the text reply and the card are
// rendered from. Keeping one projection is what stops the two surfaces from
// drifting apart.
type Message struct {
	Kind string
	// Title, when set, overrides the kind's default title.
	Title string
	// Report is present for query and digest messages.
	Report *Report
	// Body is a short human note shown above the report.
	Body string
	// Freshness is the label shown next to the data time.
	Freshness string
	// Notice is the prominent warning line, empty when the data is current.
	Notice string
	// Detailed expands every window of a single channel.
	Detailed bool
	// Alerts are the alert rows of an alert notice (immediate push) or digest.
	Alerts []Alert
	// Pending and Tight describe a scheduled digest: the open items and the tight
	// channels. They are display-only.
	Pending []PendingItem
	Tight   []TightChannel
	// GeneratedAt is the report's observation time.
	GeneratedAt time.Time
}

// ReportRenderer renders a Message. PlainReportRenderer is the production hook;
// a later designer can replace it with a richer card without touching the bot.
type ReportRenderer interface {
	// Text renders the plain-text form used by chat replies and logs.
	Text(msg Message) string
	// Card renders the full Feishu interactive card (schema 2.0).
	Card(msg Message) map[string]any
	// CardSimple renders the degraded card used when the full card is rejected.
	CardSimple(msg Message) map[string]any
}

// PlainReportRenderer is a plain, i18n-neutral renderer: it prints the exact
// numbers, window scopes, reset times and data time, and never implies freshness
// it does not have. It sets no layout beyond Feishu's markdown elements, so the
// later card design can replace it wholesale.
//
// The location is stored atomically so a settings reload can retarget the display
// time zone while a reply is being rendered.
type PlainReportRenderer struct {
	loc atomic.Pointer[time.Location]
}

// NewPlainReportRenderer builds a renderer. A nil location uses the server
// location.
func NewPlainReportRenderer(loc *time.Location) *PlainReportRenderer {
	renderer := &PlainReportRenderer{}
	renderer.SetLocation(loc)
	return renderer
}

// SetLocation retargets the display time zone. A nil location is ignored.
func (r *PlainReportRenderer) SetLocation(loc *time.Location) {
	if loc == nil {
		loc = time.Local
	}
	r.loc.Store(loc)
}

func (r *PlainReportRenderer) location() *time.Location {
	if loc := r.loc.Load(); loc != nil {
		return loc
	}
	return time.Local
}

// Title resolves the message title: its own, then the kind default.
func (r *PlainReportRenderer) Title(msg Message) string {
	if strings.TrimSpace(msg.Title) != "" {
		return strings.TrimSpace(msg.Title)
	}
	if title, ok := defaultTitles[msg.Kind]; ok {
		return title
	}
	return "额度通知"
}

// Text renders the plain-text form.
func (r *PlainReportRenderer) Text(msg Message) string {
	var b strings.Builder
	b.WriteString("【" + r.Title(msg) + "】")
	if !msg.GeneratedAt.IsZero() {
		b.WriteString(r.formatTime(msg.GeneratedAt))
	}
	if msg.Freshness != "" {
		b.WriteString(" · " + msg.Freshness)
	}
	b.WriteByte('\n')
	if msg.Notice != "" {
		b.WriteString("⚠️ " + oneLine(msg.Notice) + "\n")
	}
	if msg.Body != "" {
		b.WriteString(msg.Body)
		b.WriteByte('\n')
	}
	if msg.Report != nil && len(msg.Report.Providers) > 0 {
		b.WriteString(r.textProviders(msg.Report, msg.Detailed))
	}
	for _, alert := range msg.Alerts {
		b.WriteString(r.textAlert(alert))
	}
	if len(msg.Pending) > 0 {
		b.WriteString("\n待处理：\n")
		for _, item := range msg.Pending {
			fmt.Fprintf(&b, "[%s] %s\n", item.Kind, oneLine(item.Action))
		}
	}
	if len(msg.Tight) > 0 {
		b.WriteString("\n额度偏紧：\n")
		for _, tight := range msg.Tight {
			fmt.Fprintf(&b, "%s 最佳账号剩余 %.0f%%\n", providerName(tight.Provider), tight.BestRemaining)
		}
	}
	if strings.TrimSpace(b.String()) == "【"+r.Title(msg)+"】\n" {
		b.WriteString("暂无可展示的额度信息。\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func (r *PlainReportRenderer) textProviders(rep *Report, detailed bool) string {
	var b strings.Builder
	for _, p := range rep.Providers {
		b.WriteString(r.providerLine(p, detailed))
	}
	return b.String()
}

func (r *PlainReportRenderer) providerLine(p ProviderReport, detailed bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", providerName(p.Provider))
	if p.Error != "" {
		fmt.Fprintf(&b, "  · 渠道错误：%s\n", oneLine(p.Error))
		return b.String()
	}
	for _, account := range p.Accounts {
		label := account.Label
		if label == "" {
			label = "账号"
		}
		fmt.Fprintf(&b, "  · %s [%s]%s\n", label, stateLabel(account.State), disabledSuffix(account.Disabled))
		shown := account.Windows
		if !detailed && len(shown) > 2 {
			shown = shown[:2]
		}
		for _, window := range shown {
			fmt.Fprintf(&b, "      %s\n", r.windowLine(window))
		}
		if !detailed && len(account.Windows) > len(shown) {
			fmt.Fprintf(&b, "      …另有 %d 个窗口，@我 %s 查看明细\n", len(account.Windows)-len(shown), p.Provider)
		}
	}
	return b.String()
}

func (r *PlainReportRenderer) textAlert(alert Alert) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n[%s] %s\n", severityLabel(alert.Severity), oneLine(alert.Title))
	if alert.Detail != "" {
		fmt.Fprintf(&b, "  %s\n", oneLine(alert.Detail))
	}
	for _, fact := range alert.Facts {
		fmt.Fprintf(&b, "  - %s\n", oneLine(fact))
	}
	if alert.Advice != "" {
		fmt.Fprintf(&b, "  建议：%s\n", oneLine(alert.Advice))
	}
	return b.String()
}

// windowLine renders one window with its exact evidence.
func (r *PlainReportRenderer) windowLine(window Window) string {
	return fmt.Sprintf("[%s] %s 已用 %s（%s 刷新）",
		scopeLabel(window.Scope), windowLabel(window), percent(window.UsedPercent), r.resetText(window))
}

func (r *PlainReportRenderer) resetText(window Window) string {
	if window.ResetAt != nil && !window.ResetAt.IsZero() {
		return r.formatShort(*window.ResetAt)
	}
	return "未上报"
}

func (r *PlainReportRenderer) formatTime(t time.Time) string {
	if t.IsZero() {
		return "时间未知"
	}
	return t.In(r.location()).Format("2006-01-02 15:04")
}

func (r *PlainReportRenderer) formatShort(t time.Time) string {
	if t.IsZero() {
		return "未上报"
	}
	return t.In(r.location()).Format("01-02 15:04")
}

func windowLabel(window Window) string {
	if strings.TrimSpace(window.Label) != "" {
		return window.Label
	}
	if strings.TrimSpace(window.Name) != "" {
		return window.Name
	}
	return "窗口"
}

func scopeLabel(scope string) string {
	switch scope {
	case ScopeAccount:
		return "账号"
	case "model":
		return "模型"
	case "group":
		return "分组"
	default:
		if scope == "" {
			return "范围未申明"
		}
		return scope
	}
}

func stateLabel(state string) string {
	switch state {
	case StateHealthy:
		return "正常"
	case StateLimited:
		return "范围受限"
	case StateNotice:
		return "注意"
	case StateWarning:
		return "告警"
	case StateExhausted:
		return "耗尽"
	case StateInvalid:
		return "失效"
	case StateSuspect:
		return "疑似异常"
	case StateStale:
		return "数据过期"
	case StateUnknown:
		return "未知"
	default:
		if state == "" {
			return "未知"
		}
		return state
	}
}

func severityLabel(severity string) string {
	switch severity {
	case SeverityUrgent:
		return "紧急"
	case SeverityWarn:
		return "警告"
	default:
		if severity == "" {
			return "提示"
		}
		return severity
	}
}

func disabledSuffix(disabled bool) string {
	if disabled {
		return "（已停用）"
	}
	return ""
}

func percent(value *float64) string {
	if value == nil {
		return "未上报"
	}
	return fmt.Sprintf("%.1f%%", *value)
}

// oneLine collapses whitespace so a multi-line upstream message cannot break the
// card layout.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
