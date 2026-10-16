package feishu

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func renderReport() Report {
	used := 82.0
	reset := time.Date(2026, 10, 16, 3, 0, 0, 0, time.UTC)
	return Report{
		GeneratedAt: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC),
		Providers: []ProviderReport{{
			Provider: "codex",
			Accounts: []AccountReport{{
				Key: "a1", Label: "work", State: StateWarning, OK: true,
				Windows: []Window{{Name: "7d", Label: "7d", Scope: ScopeAccount, UsedPercent: &used, ResetAt: &reset}},
			}},
		}},
	}
}

func TestCardIsValidSchema(t *testing.T) {
	renderer := NewPlainReportRenderer(time.UTC)
	report := renderReport()
	msg := Message{Kind: KindQuery, Report: &report, Freshness: "实时", GeneratedAt: report.GeneratedAt}

	for name, card := range map[string]map[string]any{
		"full":   renderer.Card(msg),
		"simple": renderer.CardSimple(msg),
	} {
		if card["schema"] != "2.0" {
			t.Fatalf("%s card schema = %v, want 2.0", name, card["schema"])
		}
		header, ok := card["header"].(map[string]any)
		if !ok {
			t.Fatalf("%s card missing header", name)
		}
		if _, ok := header["title"].(map[string]any); !ok {
			t.Fatalf("%s card missing header title", name)
		}
		body, ok := card["body"].(map[string]any)
		if !ok {
			t.Fatalf("%s card missing body", name)
		}
		elements, ok := body["elements"].([]map[string]any)
		if !ok || len(elements) == 0 {
			t.Fatalf("%s card has no elements", name)
		}
		// The card must survive a JSON round trip, which is what strict mode checks.
		if _, err := json.Marshal(card); err != nil {
			t.Fatalf("%s card does not marshal: %v", name, err)
		}
	}
}

func TestCardRendersExactEvidenceWithoutImpliedFreshness(t *testing.T) {
	renderer := NewPlainReportRenderer(time.UTC)
	report := renderReport()
	msg := Message{Kind: KindQuery, Report: &report, Freshness: "缓存 · 40 分钟前", Notice: "以下为 40 分钟前的观察数据，可能已过期。", GeneratedAt: report.GeneratedAt}

	encoded, _ := json.Marshal(renderer.Card(msg))
	text := string(encoded)
	for _, want := range []string{"82.0%", "work", "缓存 · 40 分钟前", "可能已过期"} {
		if !strings.Contains(text, want) {
			t.Fatalf("card is missing %q: %s", want, text)
		}
	}
}

func TestTextRendersThePlainAnswer(t *testing.T) {
	renderer := NewPlainReportRenderer(time.UTC)
	report := renderReport()
	msg := Message{Kind: KindQuery, Report: &report, Freshness: "实时", GeneratedAt: report.GeneratedAt}
	text := renderer.Text(msg)
	if !strings.Contains(text, "额度查询") {
		t.Fatalf("text missing title: %s", text)
	}
	if !strings.Contains(text, "82.0%") || !strings.Contains(text, "Codex") {
		t.Fatalf("text missing evidence: %s", text)
	}
}

func TestAlertCardIsColouredBySeverity(t *testing.T) {
	renderer := NewPlainReportRenderer(time.UTC)
	msg := Message{Kind: AlertCredential, Alerts: []Alert{{Kind: AlertCredential, Severity: SeverityUrgent, Title: "凭证失效"}}}
	card := renderer.Card(msg)
	header := card["header"].(map[string]any)
	if header["template"] != "red" {
		t.Fatalf("urgent alert header template = %v, want red", header["template"])
	}
}
