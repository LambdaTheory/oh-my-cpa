package feishu

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestCardEvidenceSeparatesHistoricalAuthQuotaAndLatestAttempt(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	for _, reason := range []string{"network", "parse", "unknown"} {
		for _, kind := range []string{KindMorning, predictionKindPrefix + KindMorning, KindSummary, KindQuery} {
			t.Run(reason+"/"+kind, func(t *testing.T) {
				report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{{
					Key: "synthetic", Label: "synthetic-old", AuthFailed: true, AuthSignalObservedAt: old.Add(time.Minute),
					State: StateStale, OK: true, ObservedAt: old, LastSuccessAt: old, CollectionFailed: true,
					AttemptedAt: now.Add(-time.Minute), AttemptOutcome: "failed", AttemptReason: reason,
					Windows: []Window{accountWindow(100, nil)},
				}}}}}
				msg := Message{Kind: kind, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now, Report: &report,
					Alerts: []Alert{{Kind: predictionKindPrefix, Provider: "codex", Key: "fresh", Title: "预计重置前耗尽", EvidenceSource: EvidenceQuota, ObservedAt: now}}}
				renderer := NewPlainReportRenderer(time.UTC)
				for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
					payload := marshalTestJSON(card)
					for _, want := range []string{"数据质量诊断", "额度观察 2026-10-20 12:00", "最后成功 2026-10-20 12:00", "最近尝试 2026-11-06 11:59", "认证信号 2026-10-20 12:01", "历史未结，当前认证未知", "已过期"} {
						if !strings.Contains(payload, want) {
							t.Fatalf("missing %q: %s", want, payload)
						}
					}
					for _, forbidden := range []string{"去重新登录", "凭证失效", "观察时间 2026-10-20", "额度已耗尽"} {
						if strings.Contains(payload, forbidden) {
							t.Fatalf("history became a current claim %q: %s", forbidden, payload)
						}
					}
				}
			})
		}
	}
}

func TestCardFreshAuthCannotBorrowOldQuotaNumbersOrRecovery(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	reset := now.Add(time.Hour)
	account := AccountReport{Key: "synthetic", Label: "synthetic-auth", State: StateInvalid, AuthFailed: true,
		AuthSignalObservedAt: now.Add(-time.Minute), ObservedAt: old, Windows: []Window{accountWindow(100, &reset)}}
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{account}}}}
	msg := Message{Kind: AlertCredential, EvidenceChecked: true, Report: &report, EvaluatedAt: now, Alerts: []Alert{credentialAlert("codex", account, now)}}
	renderer := NewPlainReportRenderer(time.UTC)
	payload := marshalTestJSON(renderer.Card(msg))
	for _, want := range []string{"凭证失效", "认证信号 2026-11-06 11:59", "额度观察 2026-10-20 12:00", "已过期"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("fresh auth lost its independent evidence %q: %s", want, payload)
		}
	}
	for _, forbidden := range []string{"恢复 11-06 13:00", "已耗尽 0%", "渠道仍可用"} {
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

func TestCardPendingHistoryHasRegistrationTimeAndNoCurrentInstruction(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	msg := Message{Kind: KindSummary, GeneratedAt: now, EvaluatedAt: now, EvidenceChecked: true, Pending: []PendingItem{{
		ID: "historical", Provider: "codex", Alias: "synthetic-history", Kind: AlertCredential, Status: PendingOpen, Since: now.Add(-time.Hour), Action: "synthetic-old-login-instruction",
	}}}
	renderer := NewPlainReportRenderer(time.UTC)
	for _, payload := range []string{marshalTestJSON(renderer.Card(msg)), renderer.Text(msg)} {
		if !strings.Contains(payload, "synthetic-history") || !strings.Contains(payload, "历史待核验") || !strings.Contains(payload, "事件登记 2026-11-06 11:00") || strings.Contains(payload, "synthetic-old-login-instruction") || strings.Contains(payload, "未发现异常") {
			t.Fatalf("pending ownership was relabeled as current risk: %s", payload)
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
