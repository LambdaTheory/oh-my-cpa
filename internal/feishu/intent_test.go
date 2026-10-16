package feishu

import "testing"

func TestParseIntentRules(t *testing.T) {
	cases := []struct {
		text string
		kind intentKind
	}{
		{"", intentStatus},
		{"帮助", intentHelp},
		{"help", intentHelp},
		{"额度", intentStatus},
		{"status", intentStatus},
		{"异常", intentAbnormal},
		{"告警", intentAbnormal},
		{"codex", intentProvider},
		{"gemini 额度", intentProvider},
		{"antigravity", intentProvider},
		{"ollama", intentProvider},
		{"今天天气", intentUnknown},
	}
	for _, tc := range cases {
		if got := parseIntent(tc.text).kind; got != tc.kind {
			t.Errorf("parseIntent(%q).kind = %v, want %v", tc.text, got, tc.kind)
		}
	}
}

func TestParseIntentProviderScopedBeforeStatus(t *testing.T) {
	got := parseIntent("codex 额度")
	if got.kind != intentProvider || got.provider != "codex" {
		t.Fatalf("codex 额度 should be scoped to codex, got %+v", got)
	}
}

func TestMatchWordRespectsLatinBoundaries(t *testing.T) {
	if matchWord("quotax", "quota") {
		t.Fatal("latin token should not match inside a word")
	}
	if !matchWord("my quota now", "quota") {
		t.Fatal("latin token should match on a boundary")
	}
	if !matchWord("查看额度", "额度") {
		t.Fatal("CJK should match by substring")
	}
}

func TestStripMentions(t *testing.T) {
	if got := stripMentions("@_user_1 额度"); got != "额度" {
		t.Fatalf("stripMentions = %q, want %q", got, "额度")
	}
}
