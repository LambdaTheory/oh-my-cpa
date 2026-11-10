package feishu

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestCardDiagnosticsKeepOneRelevantHistoricalClock pins the compact diagnostic
// rule: a stale credential gets one clock (the auth signal when present), not a
// replay of quota, last-success, attempt and collection times.
func TestCardDiagnosticsKeepOneRelevantHistoricalClock(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	for _, kind := range []string{KindMorning, predictionKindPrefix + KindMorning, KindSummary, KindQuery} {
		t.Run(kind, func(t *testing.T) {
			report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{{
				Key: "synthetic", Label: "synthetic-old.json", AuthFailed: true, AuthSignalObservedAt: old.Add(time.Minute),
				State: StateStale, OK: true, ObservedAt: old, LastSuccessAt: old, CollectionFailed: true,
				AttemptedAt: now.Add(-time.Minute), AttemptOutcome: "failed", AttemptReason: "network",
				Windows: []Window{accountWindow(100, nil)},
			}}}}}
			msg := Message{Kind: kind, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now, Report: &report,
				Alerts: []Alert{{Kind: predictionKindPrefix, Provider: "codex", Key: "fresh", Title: "预计重置前耗尽", EvidenceSource: EvidenceQuota, ObservedAt: now}}}
			renderer := NewPlainReportRenderer(time.UTC)
			for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
				payload := marshalTestJSON(card)
				for _, want := range []string{"数据质量诊断", "Antigravity · synthetic-old；", "认证信号 2026-10-20 12:01", "天前", "（历史未结）"} {
					if !strings.Contains(payload, want) {
						t.Fatalf("missing %q: %s", want, payload)
					}
				}
				for _, forbidden := range []string{"去重新登录", "凭证失效", "额度观察 2026-10-20", "最后成功", "最近尝试", "已过期", ".json", "额度已耗尽"} {
					if strings.Contains(payload, forbidden) {
						t.Fatalf("history became a current claim or replayed another clock as %q: %s", forbidden, payload)
					}
				}
			}
		})
	}
}

// TestCardDiagnosticsStayCurrentAndBounded pins the card-level contract: removed
// credentials are absent, only current credentials occupy the two diagnostic
// rows, each row carries one clock and a display name without the file extension,
// and the overflow count covers current credentials only.
func TestCardDiagnosticsStayCurrentAndBounded(t *testing.T) {
	now := mustTime("2026-11-09T10:00:00Z")
	old := now.Add(-19 * 24 * time.Hour)
	currentMetadata := now.Add(-time.Hour)
	window := []Window{accountWindow(90, nil)}
	removed := []AccountReport{
		{Key: "removed-1", Label: "antigravity-cedricnolan996@gmail.com.json", State: StateStale, OK: true,
			ObservedAt: old, AuthFailed: true, AuthSignalObservedAt: old, MetadataObservedAt: old, Windows: window},
		{Key: "removed-2", Label: "antigravity-nolancendric996@gmail.com.json", State: StateStale, OK: true,
			ObservedAt: old, AuthFailed: true, AuthSignalObservedAt: old, MetadataObservedAt: old, Windows: window},
	}
	current := []AccountReport{
		{Key: "current-1", Label: "antigravity-current-a.json", State: StateStale, OK: true,
			ObservedAt: now.Add(-48 * time.Hour), MetadataObservedAt: currentMetadata, Windows: window},
		{Key: "current-2", Label: "antigravity-current-b.json", State: StateStale, OK: true,
			ObservedAt: now.Add(-72 * time.Hour), AuthFailed: true, AuthSignalObservedAt: now.Add(-25 * time.Hour),
			MetadataObservedAt: currentMetadata, Windows: window},
		{Key: "current-3", Label: "antigravity-current-c.json", State: StateStale, OK: true,
			ObservedAt: now.Add(-96 * time.Hour), MetadataObservedAt: currentMetadata, Windows: window},
		{Key: "current-4", Label: "antigravity-current-d.json", State: StateStale, OK: true,
			ObservedAt: now.Add(-120 * time.Hour), MetadataObservedAt: currentMetadata, Windows: window},
	}
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity", Accounts: append(removed, current...)}}}
	msg := Message{Kind: KindMorning, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now, Report: &report}
	renderer := NewPlainReportRenderer(time.UTC)
	for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
		payload := marshalTestJSON(card)
		for _, banned := range []string{"cedricnolan996", "nolancendric996", ".json"} {
			if strings.Contains(payload, banned) {
				t.Fatalf("a removed credential or file name reached the card as %q: %s", banned, payload)
			}
		}
		block := diagnosticBlock(t, card)
		rows := diagnosticRows(block)
		if len(rows) != 2 {
			t.Fatalf("diagnostic rows = %d, want 2: %s", len(rows), block)
		}
		for _, row := range rows {
			if clocks := strings.Count(row, "额度观察") + strings.Count(row, "认证信号"); clocks != 1 {
				t.Fatalf("diagnostic row must carry exactly one relevant clock, got %d: %s", clocks, row)
			}
			if length := utf8.RuneCountInString(row); length > 96 {
				t.Fatalf("diagnostic row is too long for a phone (%d runes): %s", length, row)
			}
		}
		if !strings.Contains(block, "另有 2 项诊断见控制台") {
			t.Fatalf("overflow must count current credentials only: %s", block)
		}
		for _, forbidden := range []string{"已过期", "最后成功", "最近尝试", "非认证采集事件"} {
			if strings.Contains(block, forbidden) {
				t.Fatalf("diagnostic block replayed %q: %s", forbidden, block)
			}
		}
	}
}

// TestCardDiagnosticsDropBatchOlderThanWindow reproduces production with no
// automatic discovery: all six rows stopped being seen 19 days ago, while four
// credentials still have fresh healthy readings. The two stale failure rows must
// not take diagnostic slots or flip the card to "data missing".
func TestCardDiagnosticsDropBatchOlderThanWindow(t *testing.T) {
	now := mustTime("2026-11-09T10:00:00Z")
	old := now.Add(-19 * 24 * time.Hour)
	window := []Window{accountWindow(42, nil)}
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{
		{Key: "live-1", Label: "antigravity-hongwane3.json", State: StateHealthy, OK: true,
			ObservedAt: now.Add(-10 * time.Minute), MetadataObservedAt: old, Windows: window},
		{Key: "live-2", Label: "antigravity-leacanva92.json", State: StateHealthy, OK: true,
			ObservedAt: now.Add(-10 * time.Minute), MetadataObservedAt: old, Windows: window},
		{Key: "live-3", Label: "antigravity-leejhyijiaace5.json", State: StateHealthy, OK: true,
			ObservedAt: now.Add(-10 * time.Minute), MetadataObservedAt: old, Windows: window},
		{Key: "live-4", Label: "antigravity-newoahil.json", State: StateHealthy, OK: true,
			ObservedAt: now.Add(-10 * time.Minute), MetadataObservedAt: old, Windows: window},
		{Key: "removed-1", Label: "antigravity-cedricnolan996@gmail.com.json", State: StateStale, OK: true,
			ObservedAt: old, AuthFailed: true, AuthSignalObservedAt: old, MetadataObservedAt: old, Windows: window},
		{Key: "removed-2", Label: "antigravity-nolancendric996@gmail.com.json", State: StateStale, OK: true,
			ObservedAt: old, AuthFailed: true, AuthSignalObservedAt: old, MetadataObservedAt: old, Windows: window},
	}}}}
	msg := Message{Kind: KindMorning, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now, Report: &report}
	renderer := NewPlainReportRenderer(time.UTC)
	for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
		payload := marshalTestJSON(card)
		for _, banned := range []string{"数据质量诊断", "cedricnolan996", "nolancendric996", ".json"} {
			if strings.Contains(payload, banned) {
				t.Fatalf("a stopped batch reached the card as %q: %s", banned, payload)
			}
		}
		if !strings.Contains(payload, "今天暂不需要调整额度，未发现异常") {
			t.Fatalf("four healthy readings must stay healthy: %s", payload)
		}
	}
}

// diagnosticBlock returns the one markdown element that owns the block.
func diagnosticBlock(t *testing.T, card map[string]any) string {
	t.Helper()
	body, _ := card["body"].(map[string]any)
	elements, _ := body["elements"].([]map[string]any)
	for _, element := range elements {
		content, _ := element["content"].(string)
		if strings.Contains(content, "数据质量诊断") {
			return content
		}
	}
	t.Fatalf("card has no diagnostic block: %s", marshalTestJSON(card))
	return ""
}

func diagnosticRows(block string) []string {
	var rows []string
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "Antigravity · ") {
			rows = append(rows, line)
		}
	}
	return rows
}

func TestCardFreshAuthDoesNotBorrowOldQuotaOrRecovery(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	reset := now.Add(time.Hour)
	account := AccountReport{Key: "synthetic", Label: "synthetic-auth", State: StateInvalid, AuthFailed: true,
		AuthSignalObservedAt: now.Add(-time.Minute), ObservedAt: old, Windows: []Window{accountWindow(100, &reset)}}
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{account}}}}
	msg := Message{Kind: AlertCredential, EvidenceChecked: true, Report: &report, EvaluatedAt: now, Alerts: []Alert{credentialAlert("codex", account, now)}}
	renderer := NewPlainReportRenderer(time.UTC)
	payload := marshalTestJSON(renderer.Card(msg))
	for _, want := range []string{"凭证失效", "认证信号 2026-11-06 11:59"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("fresh auth lost its independent evidence %q: %s", want, payload)
		}
	}
	for _, forbidden := range []string{"2026-10-20", "额度观察 2026-10-20", "已过期", "恢复 11-06 13:00", "已耗尽 0%", "渠道仍可用"} {
		if strings.Contains(payload, forbidden) {
			t.Fatalf("auth borrowed an unproven quota claim %q: %s", forbidden, payload)
		}
	}
}

func TestCardHiddenRiskCannotChooseDisplayedEvidenceTime(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	for _, kind := range []string{KindMorning, KindAlertBatch} {
		msg := Message{Kind: kind, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now}
		for index := 0; index < 4; index++ {
			observed := now
			if index == 3 {
				observed = now.Add(-17 * 24 * time.Hour)
			}
			msg.Alerts = append(msg.Alerts, Alert{Kind: AlertCredential, Provider: fmt.Sprintf("synthetic-%d", index), Key: fmt.Sprint(index), Title: "凭证失效", EvidenceSource: EvidenceAuthentication, ObservedAt: observed})
		}
		renderer := NewPlainReportRenderer(time.UTC)
		for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
			payload := marshalTestJSON(card)
			if strings.Contains(payload, "2026-10-20") || strings.Contains(payload, "synthetic-3") || !strings.Contains(payload, "认证信号 2026-11-06 12:00") || !strings.Contains(payload, "另有 1 项") {
				t.Fatalf("hidden row contaminated visible evidence clocks: %s", payload)
			}
		}
	}
}

func TestCardUnknownSamplesCannotBorrowAggregateTimeOrClaimHealthy(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "synthetic", OK: true, State: StateHealthy, Windows: []Window{accountWindow(20, nil)}}}}}}
	for _, kind := range []string{KindMorning, predictionKindPrefix + KindMorning, KindQuery} {
		payload := marshalTestJSON(NewPlainReportRenderer(time.UTC).Card(Message{Kind: kind, Report: &report, EvaluatedAt: now, GeneratedAt: now, EvidenceChecked: true}))
		if strings.Contains(payload, "未发现异常") || strings.Contains(payload, "全部正常") || strings.Contains(payload, "额度观察 2026-11-06") || !strings.Contains(payload, "额度观察：时间未知") {
			t.Fatalf("aggregate time renewed unknown samples: %s", payload)
		}
	}
}

func TestTextMissingEvidenceExplainsUnknownInsteadOfOnlyATitle(t *testing.T) {
	text := NewPlainReportRenderer(time.UTC).Text(Message{Kind: KindQuery, EvidenceChecked: true})
	if !strings.Contains(text, "状态未知") || strings.Contains(text, "未发现异常") {
		t.Fatalf("missing evidence was not explained: %s", text)
	}
}

func TestCardPendingHistoryIsAnAnonymousSummaryWithoutCurrentInstruction(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	msg := Message{Kind: KindSummary, GeneratedAt: now, EvaluatedAt: now, EvidenceChecked: true, Pending: []PendingItem{{
		ID: "historical", Provider: "codex", Alias: "synthetic-history", Kind: AlertCredential, Status: PendingOpen, Since: now.Add(-time.Hour), Action: "synthetic-old-login-instruction",
	}}}
	renderer := NewPlainReportRenderer(time.UTC)
	for _, payload := range []string{marshalTestJSON(renderer.Card(msg)), renderer.Text(msg)} {
		if !strings.Contains(payload, "另有 1 项历史待核验，见控制台") {
			t.Fatalf("pending history was not summarized: %s", payload)
		}
		for _, forbidden := range []string{"synthetic-history", "事件登记", "synthetic-old-login-instruction", "未发现异常", "待处理："} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("pending history became a current instruction or named row as %q: %s", forbidden, payload)
			}
		}
	}
}

func TestCardHealthySummaryDatesOnlyItsQualifyingAccounts(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	healthy := now.Add(-time.Minute)
	low := now.Add(-10 * time.Minute)
	report := Report{GeneratedAt: now, Providers: []ProviderReport{
		{Provider: "codex", Accounts: []AccountReport{{Key: "healthy", State: StateHealthy, OK: true, ObservedAt: healthy, Windows: []Window{accountWindow(20, nil)}}}},
		{Provider: "claude", Accounts: []AccountReport{{Key: "low", State: StateWarning, OK: true, ObservedAt: low, Windows: []Window{accountWindow(95, nil)}}}},
	}}
	msg := Message{Kind: KindQuery, Report: &report, EvaluatedAt: now, GeneratedAt: now, Pending: []PendingItem{{ID: "history", Provider: "codex", Kind: AlertCredential, Status: PendingOpen}}}
	best, count, _ := compactHealthy(msg)
	if best != "codex" || count != 1 {
		t.Fatalf("historical ownership hid a genuinely healthy alternative: %s/%d", best, count)
	}
	clock := NewPlainReportRenderer(time.UTC).healthyClock(msg)
	if !strings.Contains(clock, "2026-11-06 11:59") || strings.Contains(clock, "11:50") {
		t.Fatalf("healthy summary borrowed another account's clock: %s", clock)
	}
}

func TestCardClockLinesAndDecisionFormattingKeepTheirMeaning(t *testing.T) {
	renderer := NewPlainReportRenderer(time.UTC)
	now := mustTime("2026-11-06T12:00:00Z")
	for _, source := range []string{EvidenceQuota, EvidenceAuthentication, EvidenceAttempt, EvidenceMonitoring, EvidenceAvailability} {
		block := compactBlock{evidenceSource: source, observed: now, accounts: map[string]bool{"a": true, "b": true}}
		for _, line := range strings.Split(renderer.compactBlockClock(block), "\n") {
			if compactLength(line) > 24 {
				t.Fatalf("evidence line exceeded budget: %s", line)
			}
		}
	}
	point := decisionPoint(Alert{Kind: AlertMonitorDown, Provider: "codex", Title: "Codex 监控停摆", Advice: "检查采集"})
	if strings.Contains(point, "Codex：Codex") || strings.Contains(point, "。；") || !strings.Contains(point, "Codex：监控停摆") {
		t.Fatal(point)
	}
}
