package quota

import (
	"testing"
)

func TestActiveObservationsFromQuotaKeepsOnlyMeasuredWindows(t *testing.T) {
	fiveHours := 5.0
	weeklyHours := 168.0
	used := 52.0
	resetAt := int64(1_700_001_800_000)
	value := &NormalizedQuota{
		AuthIndex:    "cl-1",
		Provider:     "claude",
		ObservedAtMS: 1_700_000_000_000,
		Windows: []QuotaWindow{
			{ID: "five_hour", Scope: "standard", UsedPercent: &used, ResetAtMS: &resetAt, PeriodHours: &fiveHours},
			{ID: "seven_day", Scope: "standard", UsedPercent: &used, PeriodHours: &weeklyHours},
			// A model-scoped window is recorded under its own kind and model.
			{ID: "seven_day_sonnet", Kind: "model_scoped", Scope: "model", Model: "claude-3-5-sonnet", UsedPercent: &used, PeriodHours: &weeklyHours},
			// No period and no used share: not evidence, so no row.
			{ID: "unknown", Scope: "standard"},
		},
	}
	observations := ActiveObservationsFromQuota(value)
	if len(observations) != 3 {
		t.Fatalf("observations = %+v, want 3", observations)
	}
	fiveHour := observations[0]
	if fiveHour.WindowKind != PassiveWindowFiveHour || fiveHour.Scope != "standard" || fiveHour.WindowMinutes != 300 || fiveHour.UsedPercent != 52 {
		t.Fatalf("five hour = %+v", fiveHour)
	}
	if fiveHour.ResetAtMS == nil || *fiveHour.ResetAtMS != resetAt || fiveHour.ObservedAtMS != 1_700_000_000_000 {
		t.Fatalf("five hour reset/time = %+v", fiveHour)
	}
	weekly := observations[1]
	if weekly.WindowKind != PassiveWindowWeekly || weekly.WindowMinutes != 10080 {
		t.Fatalf("weekly = %+v", weekly)
	}
	scoped := observations[2]
	if scoped.WindowKind != "model_scoped" || scoped.Scope != "model" || scoped.Model != "claude-3-5-sonnet" {
		t.Fatalf("model window = %+v", scoped)
	}
	// Every key is unique, which is what makes a repeated refresh idempotent.
	seen := map[string]bool{}
	for _, observation := range observations {
		if seen[observation.EventKey] {
			t.Fatalf("duplicate event key %q", observation.EventKey)
		}
		seen[observation.EventKey] = true
	}
}

func TestActiveObservationsFromQuotaRejectsIncompleteInput(t *testing.T) {
	if got := ActiveObservationsFromQuota(nil); got != nil {
		t.Fatalf("nil quota = %+v", got)
	}
	if got := ActiveObservationsFromQuota(&NormalizedQuota{Provider: "claude", ObservedAtMS: 1}); got != nil {
		t.Fatalf("empty auth index = %+v", got)
	}
	if got := ActiveObservationsFromQuota(&NormalizedQuota{AuthIndex: "cl-1", Provider: "claude"}); got != nil {
		t.Fatalf("missing instant = %+v", got)
	}
}

func TestPassiveWindowKindForRecognizesOnlyStandardWindows(t *testing.T) {
	cases := []struct {
		provider string
		window   QuotaWindow
		kind     string
		ok       bool
	}{
		{"claude", QuotaWindow{ID: "five_hour", Scope: "standard"}, PassiveWindowFiveHour, true},
		{"claude", QuotaWindow{ID: "seven_day", Scope: "standard"}, PassiveWindowWeekly, true},
		{"codex", QuotaWindow{ID: "weekly", Scope: "standard"}, PassiveWindowWeekly, true},
		{"claude", QuotaWindow{ID: "seven_day_sonnet", Scope: "model", Model: "sonnet"}, "", false},
		{"kimi", QuotaWindow{ID: "weekly", Scope: "standard"}, "", false},
	}
	for _, test := range cases {
		kind, ok := PassiveWindowKindFor(test.provider, test.window)
		if ok != test.ok || kind != test.kind {
			t.Fatalf("PassiveWindowKindFor(%s, %+v) = %q,%v want %q,%v", test.provider, test.window, kind, ok, test.kind, test.ok)
		}
	}
}
