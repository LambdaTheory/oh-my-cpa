package feishu

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAC15AC17AC20CompactBlocksMergeAccountsAndRespectLines(t *testing.T) {
	now := mustTime("2026-10-28T12:00:00Z")
	provider := ProviderReport{Provider: "codex"}
	for index, remaining := range []float64{1, 3, 8} {
		reset := now.Add(time.Duration(index+1) * time.Hour)
		provider.Accounts = append(provider.Accounts, AccountReport{Key: fmt.Sprint(index), Label: strings.Repeat("长账号", 30), OK: true, State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(100-remaining, &reset)}})
	}
	msg := Message{Kind: KindQuery, Report: &Report{GeneratedAt: now, Providers: []ProviderReport{provider}}, GeneratedAt: now, EvaluatedAt: now}
	blocks, _ := compactBlocks(msg)
	if len(blocks) != 1 || len(blocks[0].accounts) != 3 {
		t.Fatalf("blocks=%+v", blocks)
	}
	first, second := NewPlainReportRenderer(time.UTC).compactBlockLines(blocks[0])
	if !strings.Contains(first, "3 个号") || !strings.Contains(first, "平均 4%") || !strings.Contains(second, "13:00") {
		t.Fatalf("%s / %s", first, second)
	}
	if compactLength(first) > 24 || compactLength(second) > 24 {
		t.Fatalf("line budget exceeded: %s / %s", first, second)
	}
}

func TestAC21AlternativeUsesChannelTotal(t *testing.T) {
	now := mustTime("2026-10-28T12:00:00Z")
	providers := []ProviderReport{}
	for _, test := range []struct {
		name      string
		remaining []float64
	}{{"antigravity", []float64{82, 75, 68}}, {"qwen", []float64{60, 55}}, {"kimi", []float64{90}}} {
		provider := ProviderReport{Provider: test.name}
		for index, remaining := range test.remaining {
			provider.Accounts = append(provider.Accounts, AccountReport{Key: fmt.Sprint(index), OK: true, State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(100-remaining, nil)}})
		}
		providers = append(providers, provider)
	}
	msg := Message{Kind: KindAlertBatch, Report: &Report{GeneratedAt: now, Providers: providers}, EvaluatedAt: now}
	conclusion := compactConclusion(msg, []compactBlock{{provider: "codex", kind: AlertQuotaExhausted}})
	if conclusion != "Codex 先别用，改用 Antigravity" {
		t.Fatal(conclusion)
	}
}

func TestAC22AC24AC29QueryColorsAndConditionalButton(t *testing.T) {
	for _, test := range []struct {
		kind, color string
		button      bool
	}{{AlertCredential, "red", true}, {AlertQuotaLow, "orange", false}, {statusHealthy, "blue", false}} {
		card := NewPlainReportRenderer(time.UTC).Card(Message{Kind: KindQuery, Alerts: []Alert{{Kind: test.kind, Provider: "codex", Key: "synthetic"}}})
		header := card["header"].(map[string]any)
		if header["template"] != test.color {
			t.Fatalf("color=%v want=%s", header["template"], test.color)
		}
		buttons := 0
		for _, element := range card["body"].(map[string]any)["elements"].([]map[string]any) {
			if element["tag"] == "button" {
				buttons++
				if element["disabled"] != true {
					t.Fatal("unconfigured URL enabled")
				}
			}
		}
		if (buttons == 1) != test.button {
			t.Fatalf("buttons=%d", buttons)
		}
	}
}

func TestAC16AC23AC29BlockLimitsAndOmissionCount(t *testing.T) {
	for _, kind := range []string{KindAlertBatch, KindQuery} {
		alerts := []Alert{}
		for i := 0; i < 10; i++ {
			alerts = append(alerts, Alert{Kind: AlertCredential, Provider: fmt.Sprintf("provider-%d", i), Key: fmt.Sprint(i)})
		}
		card := NewPlainReportRenderer(time.UTC).Card(Message{Kind: kind, Alerts: alerts})
		payload := marshalTestJSON(card)
		want := 3
		if kind == KindQuery {
			want = 8
		}
		if strings.Count(payload, "去重新登录") != want || !strings.Contains(payload, fmt.Sprintf("另有 %d 项见控制台", 10-want)) {
			t.Fatal(payload)
		}
	}
}

func TestAC28MonitorChannelsShareOneBlock(t *testing.T) {
	now := mustTime("2026-10-28T12:00:00Z")
	msg := Message{Kind: KindAlertBatch, Alerts: []Alert{{Kind: AlertMonitorDown, Provider: "codex", ObservedAt: now.Add(-time.Hour)}, {Kind: AlertMonitorDown, Provider: "claude", ObservedAt: now.Add(-2 * time.Hour)}}}
	blocks, _ := compactBlocks(msg)
	if len(blocks) != 1 {
		t.Fatalf("blocks=%+v", blocks)
	}
	first, second := NewPlainReportRenderer(time.UTC).compactBlockLines(blocks[0])
	if first != "2 个渠道 监控停摆" || !strings.Contains(second, "10:00") {
		t.Fatalf("%s / %s", first, second)
	}
}

func TestAC29HealthyQueryFoldsAccountsIntoOneLine(t *testing.T) {
	now := mustTime("2026-10-28T12:00:00Z")
	report := Report{GeneratedAt: now, Providers: []ProviderReport{{Provider: "codex", Accounts: []AccountReport{{Key: "one", OK: true, State: StateHealthy, ObservedAt: now, Windows: []Window{accountWindow(24, nil)}}}}}}
	card := NewPlainReportRenderer(time.UTC).Card(Message{Kind: KindQuery, Report: &report, EvaluatedAt: now, GeneratedAt: now})
	payload := marshalTestJSON(card)
	if !strings.Contains(payload, "1 个渠道 全部正常 平均 76%") || strings.Contains(payload, "打开控制台") || !strings.Contains(payload, "blue") {
		t.Fatal(payload)
	}
}

func TestAC30SameSampleBodyShrinksBySixtyPercent(t *testing.T) {
	now := mustTime("2026-10-28T12:00:00Z")
	alerts := []Alert{}
	for index := 0; index < 3; index++ {
		alerts = append(alerts, Alert{Kind: AlertQuotaExhausted, Provider: "codex", Key: fmt.Sprint(index), Alias: fmt.Sprintf("work-%d", index), Title: "额度已耗尽", Detail: "账号 · 每周窗口 · 剩余 0.0%；距重置 3 小时 30 分钟，需要全部窗口恢复后才能继续使用", Advice: "使用备用凭证或等待重置，进入控制台查看账号与窗口详情"})
	}
	msg := Message{Kind: KindAlertBatch, Alerts: alerts, GeneratedAt: now, EvaluatedAt: now}
	before := decisionConclusion(msg, alerts)
	for _, alert := range alerts {
		before += decisionPoint(alert)
	}
	before += "观察时间 10-28 12:00 · 打开控制台"
	after := ""
	card := NewPlainReportRenderer(time.UTC).Card(msg)
	for _, element := range card["body"].(map[string]any)["elements"].([]map[string]any) {
		if content, ok := element["content"].(string); ok {
			after += strings.NewReplacer("**", "", "<font color='grey'>", "", "</font>", "").Replace(content)
		}
	}
	after += "打开控制台"
	if compactLength(after)*100 > compactLength(before)*40 {
		t.Fatalf("body reduction below 60%%: %d -> %d", compactLength(before), compactLength(after))
	}
	t.Logf("same synthetic sample body: %d -> %d characters", compactLength(before), compactLength(after))
}

func TestAC17AC19EveryCompactLineKeepsStatusAndRemaining(t *testing.T) {
	renderer := NewPlainReportRenderer(time.UTC)
	for _, kind := range []string{AlertCredential, AlertQuotaExhausted, AlertQuotaLow, AlertChannelUnavailable, AlertMonitorDown} {
		for _, provider := range []string{"antigravity", strings.Repeat("长渠道名", 20)} {
			block := compactBlock{provider: provider, kind: kind, alias: strings.Repeat("长账号名", 20), accounts: map[string]bool{"synthetic": true}}
			if kind == AlertQuotaLow {
				block.remaining = []float64{0.2}
			}
			if kind == AlertQuotaExhausted {
				block.remaining = []float64{0}
			}
			first, second := renderer.compactBlockLines(block)
			if compactLength(first) > 24 || compactLength(second) > 24 || strings.Contains(first, "已用") {
				t.Fatalf("line budget: %s / %s", first, second)
			}
			if kind == AlertQuotaLow && !strings.Contains(first, "<1%") || kind == AlertQuotaExhausted && !strings.Contains(first, "已耗尽 0%") {
				t.Fatal(first)
			}
		}
	}
}
