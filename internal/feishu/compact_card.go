package feishu

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
)

type compactBlock struct {
	provider, kind     string
	accounts           map[string]bool
	alias              string
	remaining          []float64
	recovery, observed time.Time
}

func usesCompactCard(kind string) bool {
	return kind == KindQuery || kind == KindAlertBatch || isActionableKind(kind)
}

func isActionableKind(kind string) bool {
	return kind == AlertQuotaExhausted || kind == AlertCredential || kind == AlertChannelUnavailable || kind == AlertMonitorDown
}

func compactRank(kind string) int {
	switch kind {
	case AlertQuotaExhausted:
		return 0
	case AlertChannelUnavailable:
		return 1
	case AlertCredential:
		return 2
	case AlertMonitorDown:
		return 3
	default:
		return 4
	}
}

// The display projection groups credential windows into one account first, then
// groups accounts by channel and state. Window multiplicity cannot inflate counts.
func compactBlocks(msg Message) ([]compactBlock, int) {
	groups := map[string]*compactBlock{}
	omitted := map[string]bool{}
	alerts := decisionAlerts(msg)
	if msg.Report != nil {
		for _, provider := range msg.Report.Providers {
			for _, account := range provider.Accounts {
				if !account.Disabled && !account.IsUnmonitored && account.CollectionFailed {
					omitted[provider.Provider+"/"+AlertReadingMissing] = true
				}
			}
		}
		now := msg.EvaluatedAt
		if now.IsZero() {
			now = msg.GeneratedAt
		}
		threshold := msg.ChannelUnavailableAfter
		if threshold <= 0 {
			threshold = DefaultChannelUnavailableAfter
		}
		info := analyzeReport(PolicyConfig{ChannelUnavailableAfter: threshold}, *msg.Report, now, nil)
		for _, provider := range info.providers {
			hasExhaustion := false
			for _, alert := range alerts {
				if alert.Provider == provider.provider && alert.Kind == AlertQuotaExhausted {
					hasExhaustion = true
				}
			}
			if provider.channelUnavailable && !hasExhaustion {
				alerts = append(alerts, channelAlert(provider, now))
			}
		}
	}
	for _, item := range msg.Pending {
		if item.Kind == predictionKindPrefix && item.ResolvedAt.IsZero() {
			omitted[item.Provider+"/"+predictionKindPrefix] = true
		}
	}
	for _, alert := range alerts {
		if !isActionableKind(alert.Kind) && !(msg.Kind == KindQuery && alert.Kind == AlertQuotaLow) {
			if alert.Kind != statusHealthy && alert.Kind != statusReported {
				omitted[alert.Provider+"/"+alert.Kind] = true
			}
			continue
		}
		identity := alert.Provider + "/" + alert.Kind
		if alert.Kind == AlertMonitorDown {
			identity = AlertMonitorDown
		}
		block := groups[identity]
		if block == nil {
			block = &compactBlock{provider: alert.Provider, kind: alert.Kind, accounts: map[string]bool{}}
			groups[identity] = block
		}
		key := alert.Key
		if alert.Kind == AlertMonitorDown {
			key = alert.Provider
		}
		if key == "" {
			key = alert.Provider
		}
		if block.accounts[key] {
			if !alert.ObservedAt.IsZero() && (block.observed.IsZero() || alert.ObservedAt.Before(block.observed)) {
				block.observed = alert.ObservedAt
			}
			continue
		}
		block.accounts[key] = true
		block.alias = alert.Alias
		if !alert.ObservedAt.IsZero() && (block.observed.IsZero() || alert.ObservedAt.Before(block.observed)) {
			block.observed = alert.ObservedAt
		}
		if alert.Kind == AlertQuotaExhausted {
			block.remaining = append(block.remaining, 0)
		}
		if msg.Report != nil {
			for _, provider := range msg.Report.Providers {
				if provider.Provider != alert.Provider {
					continue
				}
				for _, account := range provider.Accounts {
					if alert.Kind == AlertChannelUnavailable {
						if _, recovery := accountUnavailable(account, msg.EvaluatedAt); recovery != nil && (block.recovery.IsZero() || recovery.Before(block.recovery)) {
							block.recovery = *recovery
						}
					}
					if account.Key != alert.Key {
						continue
					}
					remaining, reset, has := compactAccountNumbers(account, alert.Kind)
					if alert.Kind == AlertQuotaLow && has {
						block.remaining = append(block.remaining, remaining)
					}
					if !reset.IsZero() && (block.recovery.IsZero() || reset.Before(block.recovery)) {
						block.recovery = reset
					}
					if alert.Kind == AlertMonitorDown && !account.LastSuccessAt.IsZero() && (block.observed.IsZero() || account.LastSuccessAt.Before(block.observed)) {
						block.observed = account.LastSuccessAt
					}
				}
			}
		}
	}
	blocks := make([]compactBlock, 0, len(groups))
	for _, block := range groups {
		blocks = append(blocks, *block)
	}
	sort.Slice(blocks, func(i, j int) bool {
		if compactRank(blocks[i].kind) != compactRank(blocks[j].kind) {
			return compactRank(blocks[i].kind) < compactRank(blocks[j].kind)
		}
		return blocks[i].provider < blocks[j].provider
	})
	return blocks, len(omitted)
}

func compactAccountNumbers(account AccountReport, kind string) (float64, time.Time, bool) {
	remaining, has := 100.0, false
	var reset time.Time
	for _, window := range account.Windows {
		if !validPercent(window.UsedPercent) {
			continue
		}
		value := 100 - *window.UsedPercent
		if kind == AlertQuotaExhausted && value > 0 {
			continue
		}
		if value < remaining {
			remaining = value
		}
		has = true
		if window.ResetAt != nil && window.ResetAt.After(reset) {
			reset = *window.ResetAt
		}
	}
	return remaining, reset, has
}

func compactLength(value string) int {
	count := 0
	for _, character := range value {
		if !unicode.IsSpace(character) {
			count++
		}
	}
	return count
}

func compactLabel(value string, limit int) string {
	value = compactText(value, 1000)
	if compactLength(value) <= limit {
		return value
	}
	var result strings.Builder
	count := 0
	for _, character := range value {
		if !unicode.IsSpace(character) {
			count++
		}
		if count >= limit {
			break
		}
		result.WriteRune(character)
	}
	return result.String() + "…"
}

func (r *PlainReportRenderer) compactBlockLines(block compactBlock) (string, string) {
	name := providerName(block.provider)
	count := len(block.accounts)
	if block.kind == AlertMonitorDown && count > 1 {
		name = fmt.Sprintf("%d 个渠道", count)
	} else if count > 1 {
		name += fmt.Sprintf(" %d 个号", count)
	} else if block.alias != "" {
		name += " " + compactLabel(block.alias, 6)
	}
	status := map[string]string{AlertQuotaExhausted: "已耗尽", AlertCredential: "凭证失效", AlertChannelUnavailable: "渠道不可用", AlertMonitorDown: "监控停摆", AlertQuotaLow: "将耗尽"}[block.kind]
	number := ""
	if len(block.remaining) > 0 {
		sum, differs := 0.0, false
		for _, value := range block.remaining {
			sum += value
			differs = differs || value != block.remaining[0]
		}
		average := sum / float64(len(block.remaining))
		number = fmt.Sprintf(" %.0f%%", math.Round(average))
		if average > 0 && average < 1 {
			number = " <1%"
		}
		if differs {
			number = " 平均" + number
		}
	}
	if compactLength(name+" "+status+number) > 24 {
		name = providerName(block.provider)
		if count > 1 {
			name += fmt.Sprintf(" %d 个号", count)
		}
	}
	name = compactLabel(name, 24-compactLength(status+number))
	line := name + " " + status + number
	second := "恢复时间未知"
	switch block.kind {
	case AlertCredential:
		second = "去重新登录"
	case AlertMonitorDown:
		second = "检查采集"
		if !block.observed.IsZero() {
			second = "最后有效数据 " + block.observed.In(r.location()).Format("15:04") + " · 检查采集"
		}
	default:
		if !block.recovery.IsZero() {
			second = "恢复 " + block.recovery.In(r.location()).Format("01-02 15:04")
		}
	}
	return line, second
}

func compactHealthy(msg Message) (string, int, float64) {
	if msg.Report == nil {
		return "", 0, 0
	}
	now := msg.EvaluatedAt
	if now.IsZero() {
		now = msg.GeneratedAt
	}
	type candidate struct {
		name  string
		sum   float64
		count int
	}
	var candidates []candidate
	total, accounts := 0.0, 0
	for _, provider := range msg.Report.Providers {
		hasOpenProblem := false
		for _, alert := range msg.Alerts {
			if alert.Provider == provider.Provider && isActionableKind(alert.Kind) {
				hasOpenProblem = true
			}
		}
		for _, item := range msg.Pending {
			if item.Provider == provider.Provider && isActionableKind(item.Kind) {
				hasOpenProblem = true
			}
		}
		if hasOpenProblem {
			continue
		}
		if provider.Error != "" || len(provider.Accounts) == 0 {
			continue
		}
		value := candidate{name: provider.Provider}
		isHealthy := true
		for _, account := range provider.Accounts {
			if account.Disabled || account.IsUnmonitored {
				continue
			}
			remaining, _, has := compactAccountNumbers(account, "")
			if !has || !account.OK || account.CollectionFailed || account.AuthFailed || !usableState(account.State) || !observationFresh(account, now, defaultFreshWindow) || remaining <= DefaultTightRemainingPercent || account.CooldownUntil != nil && account.CooldownUntil.After(now) {
				isHealthy = false
				break
			}
			value.sum += remaining
			value.count++
		}
		if isHealthy && value.count > 0 {
			candidates = append(candidates, value)
			total += value.sum
			accounts += value.count
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sum != candidates[j].sum {
			return candidates[i].sum > candidates[j].sum
		}
		return candidates[i].count > candidates[j].count
	})
	best := ""
	if len(candidates) > 0 {
		best = candidates[0].name
	}
	if len(candidates) > 1 && candidates[0].sum == candidates[1].sum && candidates[0].count == candidates[1].count {
		best = ""
	}
	average := 0.0
	if accounts > 0 {
		average = total / float64(accounts)
	}
	return best, len(candidates), average
}

func compactConclusion(msg Message, blocks []compactBlock) string {
	blocked := map[string]bool{}
	for _, block := range blocks {
		if block.kind == AlertQuotaExhausted || block.kind == AlertChannelUnavailable {
			blocked[block.provider] = true
		}
	}
	if len(blocked) > 0 {
		best, _, _ := compactHealthy(msg)
		if best == "" {
			return "暂无可替代渠道，请尽快处理"
		}
		name := fmt.Sprintf("%d 个渠道", len(blocked))
		if len(blocked) == 1 {
			for provider := range blocked {
				name = providerName(provider)
			}
		}
		line := name + " 先别用，改用 " + providerName(best)
		if compactLength(line) > 24 {
			return "部分渠道不可用，请切换健康渠道"
		}
		return line
	}
	for _, block := range blocks {
		if block.kind == AlertCredential {
			return "部分账号需要处理，渠道仍可用"
		}
	}
	for _, block := range blocks {
		if block.kind == AlertMonitorDown {
			return "监控停摆，额度状态暂不可信"
		}
	}
	if len(blocks) > 0 {
		return "有额度快用完，建议提前安排"
	}
	if msg.Body != "" {
		return compactLabel(msg.Body, 24)
	}
	if len(blocks) == 0 {
		for _, alert := range decisionAlerts(msg) {
			if alert.Kind == AlertReadingMissing {
				return "数据待确认，额度状态暂不可信"
			}
		}
	}
	if msg.Report != nil {
		now := msg.EvaluatedAt
		if now.IsZero() {
			now = msg.GeneratedAt
		}
		if fresh := freshnessOf(*msg.Report, defaultFreshWindow, now); fresh.expired || !fresh.available {
			return "数据待确认，额度状态暂不可信"
		}
	}
	return "今天暂不需要调整，未发现异常"
}

func (r *PlainReportRenderer) compactCard(msg Message) map[string]any {
	blocks, omitted := compactBlocks(msg)
	limit := 3
	if msg.Kind == KindQuery {
		limit = 8
	}
	count := min(len(blocks), limit)
	omitted += len(blocks) - count
	elements := []map[string]any{markdown("**" + compactConclusion(msg, blocks) + "**")}
	color, hasAction := "blue", false
	for _, block := range blocks[:count] {
		first, second := r.compactBlockLines(block)
		elements = append(elements, markdown(first+"\n<font color='grey'>"+second+"</font>"))
		if isActionableKind(block.kind) {
			hasAction = true
			color = "red"
		} else if color != "red" {
			color = "orange"
		}
	}
	if msg.Kind == KindQuery {
		_, healthy, average := compactHealthy(msg)
		if healthy > 0 {
			elements = append(elements, markdown(fmt.Sprintf("%d 个渠道 全部正常 平均 %.0f%%", healthy, math.Round(average))))
		}
	}
	observed := msg.GeneratedAt
	if msg.Report != nil {
		observed = msg.Report.GeneratedAt
	}
	for _, block := range blocks {
		if !block.observed.IsZero() && (observed.IsZero() || block.observed.Before(observed)) {
			observed = block.observed
		}
	}
	meta := "观察时间未知"
	if !observed.IsZero() {
		meta = "观察 " + observed.In(r.location()).Format("15:04")
	}
	now := msg.EvaluatedAt
	if now.IsZero() {
		now = msg.GeneratedAt
	}
	if !observed.IsZero() && now.Sub(observed) > defaultFreshWindow {
		meta += fmt.Sprintf(" · 数据 %d 分钟前", int(now.Sub(observed).Minutes()))
	}
	if omitted > 0 {
		meta += fmt.Sprintf(" · 另有 %d 项见控制台", omitted)
	}
	if compactLength(meta) > 24 && !observed.IsZero() && now.Sub(observed) > defaultFreshWindow {
		meta = "观察 " + observed.In(r.location()).Format("15:04") + fmt.Sprintf(" · 过期%d分", int(now.Sub(observed).Minutes()))
		if omitted > 0 {
			meta += fmt.Sprintf(" · 另有 %d 项见控制台", omitted)
		}
	}
	elements = append(elements, markdown(meta))
	if hasAction {
		elements = append(elements, r.consoleButton())
	}
	title := "额度提醒"
	if msg.Kind == KindQuery {
		title = "额度查询"
	}
	return map[string]any{"schema": "2.0", "header": map[string]any{"title": map[string]any{"tag": "plain_text", "content": title}, "template": color}, "body": map[string]any{"elements": elements}}
}
