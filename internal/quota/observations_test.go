package quota

import (
	"testing"
	"time"
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

// The observation timeline records only measured windows. The parser must hand it a weekly
// window with no reading, or with a non-finite/out-of-range one, as nil, so a global rate-limit
// flag or an untrustworthy number cannot become a recorded weekly 100% - the row that otherwise
// drives the account auto-manager. A true hundred is still recorded as the measured 100 it is.
func TestActiveObservationsFromQuotaDoesNotRecordAnUntrustworthyWeeklyWindow(t *testing.T) {
	observedAtMS := time.Now().UnixMilli()
	cases := []struct {
		name       string
		weeklyUsed string
		wantWeekly bool
		wantUsed   float64
	}{
		{"a window that published nothing", ``, false, 0},
		{"a weekly window one over the scale", `"used_percent": 101,`, false, 0},
		{"a negative weekly window", `"used_percent": -1,`, false, 0},
		{"a measured weekly window", `"used_percent": 100,`, true, 100},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			raw := []byte(`{
				"plan_type": "plus",
				"rate_limit": {
					"allowed": false,
					"limit_reached": true,
					"primary_window": {"used_percent": 100, "limit_window_seconds": 18000, "reset_after_seconds": 3600},
					"secondary_window": {` + testCase.weeklyUsed + ` "limit_window_seconds": 604800, "reset_after_seconds": 400000}
				}
			}`)
			_, windows, _, err := ParseCodexUsage(raw, observedAtMS)
			if err != nil {
				t.Fatalf("ParseCodexUsage failed: %v", err)
			}
			value := &NormalizedQuota{AuthIndex: "idx-1", Provider: "codex", ObservedAtMS: observedAtMS, Windows: windows}
			observations := ActiveObservationsFromQuota(value)

			// The measured five-hour window is always recorded at its real 100%.
			if len(observations) == 0 || observations[0].WindowKind != PassiveWindowFiveHour || observations[0].UsedPercent != 100 {
				t.Fatalf("observations = %+v, want the measured five-hour window at 100", observations)
			}
			weekly, found := findActiveObservation(observations, PassiveWindowWeekly)
			if found != testCase.wantWeekly {
				t.Fatalf("weekly observation found = %v, want %v (%+v)", found, testCase.wantWeekly, observations)
			}
			if testCase.wantWeekly && weekly.UsedPercent != testCase.wantUsed {
				t.Fatalf("weekly observation = %+v, want used %v", weekly, testCase.wantUsed)
			}
		})
	}
}

func findActiveObservation(observations []ActiveObservation, windowKind string) (ActiveObservation, bool) {
	for _, observation := range observations {
		if observation.WindowKind == windowKind {
			return observation, true
		}
	}
	return ActiveObservation{}, false
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

// The same trust rule applies to the Claude parser: an out-of-range or non-numeric
// utilization is not a weekly reading, so it produces no observation and cannot become
// account-level exhaustion evidence, while a true hundred is recorded as the measured 100.
func TestActiveObservationsFromClaudeRejectUntrustworthyWeeklyUtilization(t *testing.T) {
	observedAtMS := time.Now().UnixMilli()
	reset := time.UnixMilli(observedAtMS).Add(48 * time.Hour).UTC().Format(time.RFC3339)
	cases := []struct {
		name       string
		util       string
		wantWeekly bool
		wantUsed   float64
	}{
		{"a utilization one over the scale", `101`, false, 0},
		{"a negative utilization", `-1`, false, 0},
		{"a non-numeric utilization", `"not-a-number"`, false, 0},
		{"a measured weekly window", `100`, true, 100},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			raw := []byte(`{"seven_day":{"utilization":` + testCase.util + `,"resets_at":"` + reset + `"}}`)
			windows, _, err := ParseClaudeUsage(raw, observedAtMS)
			if err != nil {
				t.Fatalf("ParseClaudeUsage failed: %v", err)
			}
			observations := ActiveObservationsFromQuota(&NormalizedQuota{
				AuthIndex: "cl-1", Provider: "claude", ObservedAtMS: observedAtMS, Windows: windows,
			})
			weekly, found := findActiveObservation(observations, PassiveWindowWeekly)
			if found != testCase.wantWeekly {
				t.Fatalf("weekly observation found = %v, want %v (%+v)", found, testCase.wantWeekly, observations)
			}
			if testCase.wantWeekly && weekly.UsedPercent != testCase.wantUsed {
				t.Fatalf("weekly observation = %+v, want used %v", weekly, testCase.wantUsed)
			}
		})
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
