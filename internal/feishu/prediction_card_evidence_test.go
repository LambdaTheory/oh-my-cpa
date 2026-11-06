package feishu

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func predictionCardEvidenceForecast(observed time.Time) Alert {
	return Alert{
		Kind: predictionKindPrefix, Severity: SeverityWarn, Provider: "codex", Key: "forecast", Alias: "synthetic-forecast",
		Title: "每周窗口", Facts: []string{"实际 88.0%", "期望 52.0%"},
		Detail: "预计 11-06 14:00 耗尽 · 撑不到重置", Advice: "提前准备备用凭证，按需分流",
		EvidenceSource: EvidenceQuota, ObservedAt: observed, FreshWindowMS: defaultFreshWindow.Milliseconds(),
	}
}

func TestPredictionCardEvidenceKeepsForecastBesideBoundedHistory(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	for _, attempt := range []struct{ reason, label string }{{"network", "网络"}, {"parse", "解析"}, {"unknown", "原因未知"}} {
		t.Run(attempt.reason, func(t *testing.T) {
			report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity"}}}
			msg := Message{Kind: predictionKindPrefix + KindMorning, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: old,
				Report: &report, Alerts: []Alert{predictionCardEvidenceForecast(now.Add(-time.Minute))}}
			for index := 0; index < 3; index++ {
				key := fmt.Sprintf("synthetic-history-%d", index)
				report.Providers[0].Accounts = append(report.Providers[0].Accounts, AccountReport{
					Key: key, Label: key, State: StateStale, OK: true, ObservedAt: old, LastSuccessAt: old.Add(-time.Minute),
					AuthFailed: true, AuthSignalObservedAt: old.Add(2 * time.Minute), CollectionFailed: true,
					AttemptedAt: now.Add(-2 * time.Minute), AttemptOutcome: "failed", AttemptReason: attempt.reason,
					Windows: []Window{accountWindow(100, nil)},
				})
				msg.Pending = append(msg.Pending, PendingItem{ID: ItemID(AlertCredential, "antigravity", key, ""),
					Kind: AlertCredential, Provider: "antigravity", Key: key, Alias: key, Status: PendingOpen, Since: old, Action: "synthetic-old-login"})
			}
			renderer := NewPlainReportRenderer(time.UTC)
			for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
				payload := marshalTestJSON(card)
				for _, want := range []string{
					"1 个窗口撑不到重置", "Codex · synthetic-forecast · 每周窗口", "实际 88.0% · 期望 52.0%",
					"预计 11-06 14:00 耗尽 · 撑不到重置", "额度观察 2026-11-06 11:59", "最新读数 11-06 11:59",
					"数据质量诊断（与当前风险分列）", "额度观察 2026-10-20 12:00", "最后成功 2026-10-20 11:59",
					"最近尝试 2026-11-06 11:58（失败/" + attempt.label + "）", "认证信号 2026-10-20 12:02",
					"已过期", "历史未结，当前认证未知", "另有 1 项诊断见控制台",
				} {
					if !strings.Contains(payload, want) {
						t.Fatalf("missing forecast or independent diagnostic %q: %s", want, payload)
					}
				}
				for _, forbidden := range []string{"其他需要处理", "待处理：", "凭证失效", "额度已耗尽", "synthetic-old-login", "synthetic-history-2"} {
					if strings.Contains(payload, forbidden) {
						t.Fatalf("history escaped its bounded diagnostic as %q: %s", forbidden, payload)
					}
				}
				elements := card["body"].(map[string]any)["elements"].([]map[string]any)
				if len(elements) != 4 {
					t.Fatalf("diagnostics must share the existing meta element: %s", payload)
				}
				for _, element := range elements {
					content, _ := element["content"].(string)
					if strings.Contains(content, "synthetic-history-") && !strings.Contains(content, "数据质量诊断") {
						t.Fatalf("historical account became a current row: %s", content)
					}
				}
			}
		})
	}
}

func TestPredictionCardEvidenceUsesAuthClockBesideOldQuota(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	reset := now.Add(time.Hour)
	account := AccountReport{
		Key: "auth", Label: "synthetic-auth", State: StateInvalid, AuthFailed: true, AuthSignalObservedAt: now.Add(-time.Minute),
		ObservedAt: old, LastSuccessAt: old, AttemptedAt: now.Add(-2 * time.Minute), AttemptOutcome: "failed", AttemptReason: "auth",
		Windows: []Window{accountWindow(100, &reset)},
	}
	auth := credentialAlert("antigravity", account, now)
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{account}}}}
	msg := Message{Kind: predictionKindPrefix + KindMorning, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now,
		Report: &report, Alerts: []Alert{predictionCardEvidenceForecast(now.Add(-5 * time.Minute)), auth},
		Pending: []PendingItem{{ID: alertItemID(auth), Kind: AlertCredential, Provider: "antigravity", Key: account.Key,
			Alias: account.Label, Status: PendingOpen, Since: old}}}
	renderer := NewPlainReportRenderer(time.UTC)
	for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
		payload := marshalTestJSON(card)
		for _, want := range []string{"其他需要处理", "凭证失效", "认证信号 2026-11-06 11:59", "额度观察 2026-11-06 11:55",
			"最新读数 11-06 11:55", "额度观察 2026-10-20 12:00", "已过期", "最近尝试 2026-11-06 11:58（失败/认证拒绝）", "待处理："} {
			if !strings.Contains(payload, want) {
				t.Fatalf("fresh auth lost its own clock or actionable pending hint %q: %s", want, payload)
			}
		}
		for _, forbidden := range []string{"历史未结，当前认证未知", "恢复 11-06 13:00", "最新读数 11-06 11:59", "已耗尽 0%"} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("independent auth/quota evidence was conflated as %q: %s", forbidden, payload)
			}
		}
		for _, element := range card["body"].(map[string]any)["elements"].([]map[string]any) {
			content, _ := element["content"].(string)
			if strings.Contains(content, "凭证失效") && (!strings.Contains(content, "认证信号 2026-11-06 11:59") || strings.Contains(content, "2026-10-20")) {
				t.Fatalf("current auth row borrowed historical quota time: %s", content)
			}
		}
	}
}

func TestPredictionCardEvidenceUsesOnlyVisibleRowsForLatestRead(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	msg := Message{Kind: predictionKindPrefix + KindMorning, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now,
		Report: &Report{GeneratedAt: now}, Omitted: 2}
	for index := 0; index < 6; index++ {
		alert := predictionCardEvidenceForecast(now.Add(time.Duration(index-10) * time.Minute))
		alert.Key, alert.Alias = fmt.Sprint(index), fmt.Sprintf("synthetic-window-%d", index)
		if index == 5 {
			alert.ObservedAt = now.Add(-time.Minute)
			msg.Pending = append(msg.Pending, PendingItem{ID: alertItemID(alert), Kind: predictionKindPrefix,
				Provider: alert.Provider, Key: alert.Key, Alias: alert.Alias, Status: PendingOpen})
		}
		msg.Alerts = append(msg.Alerts, alert)
	}
	for _, kind := range []string{statusHealthy, statusUnknown, statusReported, "omitted"} {
		msg.Alerts = append(msg.Alerts, Alert{Kind: kind, Title: "synthetic-status", ObservedAt: now})
	}
	renderer := NewPlainReportRenderer(time.UTC)
	for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
		payload := marshalTestJSON(card)
		elements := card["body"].(map[string]any)["elements"].([]map[string]any)
		if len(elements) != 8 || card["schema"] != "2.0" || card["config"].(map[string]any)["width_mode"] != "compact" {
			t.Fatalf("five-row schema/layout contract changed: elements=%d card=%s", len(elements), payload)
		}
		for _, want := range []string{"8 个窗口撑不到重置", "另有 3 项，见控制台", "已展示预测最新读数 11-06 11:54", "各项时间仅对应标注的证据来源", "打开控制台"} {
			if !strings.Contains(payload, want) {
				t.Fatalf("visible forecast hint missing %q: %s", want, payload)
			}
		}
		for index := 0; index < 5; index++ {
			content := elements[index+1]["content"].(string)
			clock := "额度观察 " + now.Add(time.Duration(index-10)*time.Minute).Format("2006-01-02 15:04")
			if !strings.Contains(content, fmt.Sprintf("synthetic-window-%d", index)) || !strings.Contains(content, clock) {
				t.Fatalf("forecast row lost its own observation: %s", content)
			}
		}
		for _, forbidden := range []string{"synthetic-window-5", "11-06 11:59", "11-06 12:00", "synthetic-status", "其他需要处理", "待处理："} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("hidden row, status or aggregate time leaked as %q: %s", forbidden, payload)
			}
		}
	}
}

func TestPredictionCardEvidenceKeepsHiddenAuthOutOfDiagnostics(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	msg := Message{Kind: predictionKindPrefix + KindMorning, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now,
		Alerts: []Alert{predictionCardEvidenceForecast(now.Add(-10 * time.Minute))}}
	for index := 0; index < 4; index++ {
		account := AccountReport{Key: fmt.Sprint(index), Label: fmt.Sprintf("synthetic-auth-%d", index), State: StateInvalid,
			AuthFailed: true, AuthSignalObservedAt: now.Add(time.Duration(index-4) * time.Minute), ObservedAt: old}
		alert := credentialAlert("antigravity", account, now)
		msg.Alerts = append(msg.Alerts, alert)
		if index == 3 {
			msg.Report = &Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{account}}}}
			msg.Pending = []PendingItem{{ID: alertItemID(alert), Kind: AlertCredential, Provider: "antigravity",
				Key: account.Key, Alias: account.Label, Status: PendingOpen, Since: old}}
		}
	}
	renderer := NewPlainReportRenderer(time.UTC)
	for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
		payload := marshalTestJSON(card)
		for _, want := range []string{"synthetic-auth-0", "synthetic-auth-1", "synthetic-auth-2", "认证信号 2026-11-06 11:58", "另有 1 项需要处理，见控制台", "最新读数 11-06 11:50"} {
			if !strings.Contains(payload, want) {
				t.Fatalf("visible non-prediction alert or cap hint lost %q: %s", want, payload)
			}
		}
		for _, forbidden := range []string{"synthetic-auth-3", "11-06 11:59", "2026-10-20", "数据质量诊断", "待处理："} {
			if strings.Contains(payload, forbidden) {
				t.Fatalf("hidden auth returned through clocks, pending or diagnostics as %q: %s", forbidden, payload)
			}
		}
	}
}

func TestPredictionCardEvidenceUnknownClocksDoNotBorrowAggregateTime(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	for _, observed := range []time.Time{{}, time.UnixMilli(0)} {
		t.Run(observed.String(), func(t *testing.T) {
			report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "antigravity", Accounts: []AccountReport{{
				Key: "untimed", Label: "synthetic-untimed", State: StateStale, AuthFailed: true, OK: true, Windows: []Window{accountWindow(20, nil)},
			}}}}}
			msg := Message{Kind: predictionKindPrefix + KindMorning, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now,
				Report: &report, Alerts: []Alert{predictionCardEvidenceForecast(observed)}}
			renderer := NewPlainReportRenderer(time.UTC)
			for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
				payload := marshalTestJSON(card)
				for _, want := range []string{"额度观察：时间未知", "最新读数 时间未知", "最后成功：时间未知", "最近尝试：时间未知", "认证信号：时间未知", "历史未结，当前认证未知"} {
					if !strings.Contains(payload, want) {
						t.Fatalf("unknown evidence was not explicit %q: %s", want, payload)
					}
				}
				for _, forbidden := range []string{"11-06 12:00", "1970-01-01", "0001-01-01", "未发现异常", "全部正常", "其他需要处理"} {
					if strings.Contains(payload, forbidden) {
						t.Fatalf("unknown clock borrowed aggregate context as %q: %s", forbidden, payload)
					}
				}
			}
		})
	}
}

func TestPredictionCardEvidencePendingStaysHistoricalWithoutEligibleAlert(t *testing.T) {
	now := mustTime("2026-11-06T12:00:00Z")
	old := now.Add(-17 * 24 * time.Hour)
	for _, hasForecast := range []bool{false, true} {
		t.Run(fmt.Sprint(hasForecast), func(t *testing.T) {
			msg := Message{Kind: predictionKindPrefix + KindMorning, EvidenceChecked: true, EvaluatedAt: now, GeneratedAt: now}
			if hasForecast {
				msg.Alerts = []Alert{predictionCardEvidenceForecast(now)}
			}
			for index := 0; index < 6; index++ {
				key := fmt.Sprintf("synthetic-pending-%d", index)
				msg.Pending = append(msg.Pending, PendingItem{ID: ItemID(AlertCredential, "antigravity", key, ""), Kind: AlertCredential,
					Provider: "antigravity", Key: key, Alias: key, Status: PendingOpen, Since: old, LastSeen: now, Action: "synthetic-old-login"})
			}
			msg.Pending = append(msg.Pending, PendingItem{ID: "resolved", Provider: "antigravity", Alias: "synthetic-resolved", Status: PendingResolved})
			renderer := NewPlainReportRenderer(time.UTC)
			for _, card := range []map[string]any{renderer.Card(msg), renderer.CardSimple(msg)} {
				payload := marshalTestJSON(card)
				for _, want := range []string{"数据质量诊断（与当前风险分列）", "synthetic-pending-0", "synthetic-pending-1", "历史待核验", "事件登记 2026-10-20 12:00", "当前证据未知，不代表当前风险", "另有 4 项诊断见控制台"} {
					if !strings.Contains(payload, want) {
						t.Fatalf("pending history lost its diagnostic meaning %q: %s", want, payload)
					}
				}
				for _, forbidden := range []string{"待处理：", "其他需要处理", "synthetic-pending-2", "synthetic-resolved", "synthetic-old-login", "凭证失效", "未发现异常"} {
					if strings.Contains(payload, forbidden) {
						t.Fatalf("pending invented or exceeded a current risk row as %q: %s", forbidden, payload)
					}
				}
				if !hasForecast && !strings.Contains(payload, "当前额度与认证状态未知") {
					t.Fatalf("history-only card retained a risk/healthy conclusion: %s", payload)
				}
			}
		})
	}
}
