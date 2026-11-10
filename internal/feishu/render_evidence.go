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

// The diagnostic block is deliberately short: two current credentials, one
// relevant clock each. Its job is to say why today's card cannot fully trust a
// reading, not to replay every stored timestamp. Removed credentials never reach
// it (the report source filters them), and open history is one summary line so it
// cannot consume a current diagnostic slot.
const diagnosticRowLimit = 2

func (r *PlainReportRenderer) diagnosticText(msg Message) string {
	now := messageTime(msg)
	current := map[string]bool{}
	for _, alert := range decisionRiskAlerts(decisionAlerts(msg)) {
		current[alertItemID(alert)] = true
	}
	rows := []string{}
	count := 0
	accounts := map[string]bool{}
	sweepSeen := newestCredentialMetadata(msg.Report)
	if msg.Report != nil {
		for _, provider := range msg.Report.Providers {
			if provider.Error != "" && len(provider.Accounts) == 0 {
				count++
				if len(rows) < diagnosticRowLimit {
					rows = append(rows, compactText(providerName(provider.Provider), 40)+"：采集结果未知")
				}
				continue
			}
			for _, account := range provider.Accounts {
				key := provider.Provider + "/" + account.Key
				if account.Disabled || account.IsUnmonitored || hasFreshQuotaReading(account, now) {
					continue
				}
				if !IsCredentialMetadataCurrent(account.MetadataObservedAt, sweepSeen) {
					continue
				}
				// The batch-relative check cannot see a stopped collector: when
				// every row is equally old, sweepSeen is old too and no row looks
				// behind. The absolute age bound keeps a months-old credential out
				// of the current diagnostic even then, while the report still
				// carries it (stored-only evidence cannot prove removal).
				if !IsCredentialMetadataCurrent(account.MetadataObservedAt, now) {
					continue
				}
				// A fresh auth failure is current risk, already named by the
				// decision rows or their overflow hint; repeating it here would
				// spend a diagnostic slot on the same fact.
				if account.HasFreshAuthFailure(now) {
					continue
				}
				accounts[key] = true
				count++
				if len(rows) >= diagnosticRowLimit {
					continue
				}
				rows = append(rows, r.diagnosticAccountRow(provider.Provider, account, now))
			}
		}
	}
	history := 0
	for _, item := range msg.Pending {
		key := item.Provider + "/" + item.Key
		if item.Status != PendingOpen || current[item.ID] || accounts[key] {
			continue
		}
		history++
	}
	if count == 0 && history == 0 {
		return ""
	}
	lines := []string{"数据质量诊断（与当前风险分列）"}
	lines = append(lines, rows...)
	if count > len(rows) {
		lines = append(lines, fmt.Sprintf("另有 %d 项诊断见控制台", count-len(rows)))
	}
	if history > 0 {
		lines = append(lines, fmt.Sprintf("另有 %d 项历史待核验，见控制台", history))
	}
	return strings.Join(lines, "\n")
}

// diagnosticAccountRow keeps one credential to one line and one clock. A stale
// auth episode is the most relevant evidence when present; otherwise the newest
// quota observation answers why the reading is not current. Last-success and
// attempt clocks stay in the console.
func (r *PlainReportRenderer) diagnosticAccountRow(provider string, account AccountReport, now time.Time) string {
	identity := diagnosticAccountIdentity(provider, account)
	if account.AuthFailed || account.State == StateInvalid {
		clock := r.diagnosticRowClock("认证信号", account.AuthSignalObservedAt, now)
		if !account.HasFreshAuthFailure(now) {
			clock += "（历史未结）"
		}
		return identity + "；" + clock
	}
	return identity + "；" + r.diagnosticRowClock("额度观察", account.ObservedAt, now)
}

// diagnosticAccountIdentity is the human-facing account name. Discovery labels
// auth files with their file name, so the extension is removed here; a raw
// `.json` name is storage detail, not something a person needs on a phone.
func diagnosticAccountIdentity(provider string, account AccountReport) string {
	label := strings.TrimSpace(account.Label)
	if len(label) > len(".json") && strings.EqualFold(label[len(label)-len(".json"):], ".json") {
		label = strings.TrimSpace(label[:len(label)-len(".json")])
	}
	identity := providerName(provider)
	if label != "" && label != identity {
		identity += " · " + label
	}
	return compactText(identity, 56)
}

// diagnosticRowClock is the compact form used inside the diagnostic block. The
// full date stays explicit while the age is stated once, and the repeated
// "expired" wording of the detailed evidence clock is dropped.
func (r *PlainReportRenderer) diagnosticRowClock(label string, observed, now time.Time) string {
	if !isMonitoringTimeValid(observed) {
		return label + "：时间未知"
	}
	text := label + " " + r.formatTime(observed)
	if !evidenceFresh(observed, now, defaultFreshWindow) {
		age := now.Sub(observed)
		if age >= 24*time.Hour {
			text += fmt.Sprintf(" · %d 天前", int(age.Hours()/24))
		} else {
			minutes := int(age.Minutes())
			if minutes < 1 {
				minutes = 1
			}
			text += fmt.Sprintf(" · %d 分钟前", minutes)
		}
	}
	return text
}

// newestCredentialMetadata is the discovery sweep's own freshness signal for a
// report. Zero times are unknown and never become the reference.
func newestCredentialMetadata(report *Report) time.Time {
	newest := time.Time{}
	if report == nil {
		return newest
	}
	for _, provider := range report.Providers {
		for _, account := range provider.Accounts {
			if account.MetadataObservedAt.After(newest) {
				newest = account.MetadataObservedAt
			}
		}
	}
	return newest
}

func (r *PlainReportRenderer) healthyClock(msg Message) string {
	_, _, _, earliest := compactHealthyEvidence(msg)
	if earliest.IsZero() {
		return "证据时间未知"
	}
	return "健康额度观察（组内最早） " + r.formatTime(earliest)
}
