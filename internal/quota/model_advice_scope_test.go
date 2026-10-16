package quota

import "testing"

func TestIsWindowRelevant(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		window     QuotaWindow
		model      string
		isRelevant bool
	}{
		{name: "standard", window: QuotaWindow{Scope: "standard"}, model: "custom-model", isRelevant: true},
		{name: "exact model", window: QuotaWindow{Scope: "model", Model: "gpt-5"}, model: "gpt-5", isRelevant: true},
		{name: "different model", window: QuotaWindow{Scope: "model", Model: "gpt-5"}, model: "gpt-5-mini"},
		{name: "empty model", window: QuotaWindow{Scope: "model"}},
		{name: "group family", window: QuotaWindow{Scope: "group", ModelFamilies: []string{"claude-sonnet"}}, model: "claude-sonnet-4", isRelevant: true},
		{name: "different family", window: QuotaWindow{Scope: "group", ModelFamilies: []string{"claude-opus"}}, model: "claude-sonnet-4"},
		{name: "empty families", window: QuotaWindow{Scope: "group"}, model: "claude-sonnet-4"},
		{name: "unknown scope", window: QuotaWindow{Scope: "unreviewed"}, model: "gpt-5"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if got := IsWindowRelevant(scenario.window, scenario.model); got != scenario.isRelevant {
				t.Fatalf("relevant = %v, want %v", got, scenario.isRelevant)
			}
		})
	}
}
