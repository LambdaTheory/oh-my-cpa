package quota

import (
	"strings"
	"testing"
	"time"
)

func TestParseAntigravityUsage(t *testing.T) {
	raw := []byte(`{
		"groups": [
			{
				"displayName": "Gemini models",
				"buckets": [
					{
						"bucketId": "five_hour",
						"displayName": "5 hour limit",
						"window": "5h",
						"remainingFraction": 0.85,
						"resetTime": "2026-01-01T12:00:00Z"
					},
					{
						"bucketId": "daily",
						"displayName": "Daily limit",
						"window": "24h",
						"remainingFraction": 0.50,
						"resetTime": "2026-01-02T00:00:00Z"
					}
				]
			}
		]
	}`)

	nowMS := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	windows, err := ParseAntigravityUsage(raw, nowMS, 0)
	if err != nil {
		t.Fatalf("ParseAntigravityUsage failed: %v", err)
	}

	if len(windows) != 2 {
		t.Fatalf("len(windows) = %d, want 2", len(windows))
	}

	w0 := windows[0]
	if *w0.RemainingPercent != 85.0 || *w0.UsedPercent != 15.0 {
		t.Errorf("w0 percent = rem:%v used:%v, want 85 and 15", w0.RemainingPercent, w0.UsedPercent)
	}
	if w0.PeriodHours == nil || *w0.PeriodHours != 5.0 {
		t.Errorf("w0 period = %v, want 5", w0.PeriodHours)
	}

	w1 := windows[1]
	if *w1.RemainingPercent != 50.0 || *w1.UsedPercent != 50.0 {
		t.Errorf("w1 percent = rem:%v used:%v, want 50 and 50", w1.RemainingPercent, w1.UsedPercent)
	}

	plan := ResolveAntigravityPlan("ultra")
	if plan.Tier != "elite" || plan.PlanLabel != "Ultra" {
		t.Errorf("plan = %+v, want elite/Ultra", plan)
	}
}

func TestParseAntigravityUsageOrdersFiveHourFirst(t *testing.T) {
	raw := []byte(`{
		"groups": [
			{
				"displayName": "Gemini Models",
				"buckets": [
					{ "bucketId": "gem-week", "displayName": "Weekly Limit", "window": "weekly", "remainingFraction": 0.51, "resetTime": "2026-01-08T04:00:00Z" },
					{ "bucketId": "gem-5h", "displayName": "Five Hour Limit", "window": "5h", "remainingFraction": 1.0, "resetTime": "2026-01-01T17:00:00Z" }
				]
			},
			{
				"displayName": "Claude and GPT models",
				"buckets": [
					{ "bucketId": "ag-week", "displayName": "Weekly Limit", "window": "weekly", "remainingFraction": 1.0, "resetTime": "2026-01-09T04:00:00Z" },
					{ "bucketId": "ag-5h", "displayName": "Five Hour Limit", "window": "5h", "remainingFraction": 1.0, "resetTime": "2026-01-01T17:00:00Z" }
				]
			}
		]
	}`)

	nowMS := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	windows, err := ParseAntigravityUsage(raw, nowMS, 0)
	if err != nil {
		t.Fatalf("ParseAntigravityUsage failed: %v", err)
	}
	if len(windows) != 4 {
		t.Fatalf("len(windows) = %d, want 4", len(windows))
	}

	// Within each group the 5-hour window must lead the weekly one, while
	// group blocks stay contiguous (Gemini before Claude and GPT).
	type expectation struct {
		labelContains string
		periodHours   float64
	}
	want := []expectation{
		{"Gemini Models", 5},
		{"Gemini Models", 168},
		{"Claude and GPT models", 5},
		{"Claude and GPT models", 168},
	}
	for i, exp := range want {
		w := windows[i]
		if !strings.Contains(w.Label, exp.labelContains) {
			t.Errorf("windows[%d].Label = %q, want group %q", i, w.Label, exp.labelContains)
		}
		if w.PeriodHours == nil || *w.PeriodHours != exp.periodHours {
			t.Errorf("windows[%d].PeriodHours = %v, want %v", i, w.PeriodHours, exp.periodHours)
		}
	}
}

func TestAntigravityCapacityScopeAndUnknownPeriod(t *testing.T) {
	windows, err := ParseAntigravityUsage([]byte(`{"groups":[{"displayName":"Gemini models","buckets":[{"bucketId":"five","window":"5h","remainingFraction":0.8,"resetTime":"2026-01-01T17:00:00Z"}]},{"displayName":"Gemini and future models","buckets":[{"bucketId":"future","window":"unreported","remainingFraction":0.8,"resetTime":"2026-01-01T17:00:00Z"}]}]}`), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 || len(windows[0].ModelFamilies) != 1 || windows[0].ModelFamilies[0] != "gemini" || len(windows[1].ModelFamilies) != 0 || *windows[1].PeriodHours != 0 {
		t.Fatalf("scope or period guessed: %+v", windows)
	}
}

func TestAntigravityPeriodsRequireCompleteFixedDuration(t *testing.T) {
	for _, label := range []string{"15h", "25 hours", "biweekly", "2 weeks", "monthly", "1m", "month", "unknown"} {
		if hours := parseAntigravityWindowHours(label); hours != 0 {
			t.Errorf("unreviewed period %q guessed as %v hours", label, hours)
		}
	}
	for label, expected := range map[string]float64{"5h": 5, "daily": 24, "weekly": 168, "30d": 720} {
		if hours := parseAntigravityWindowHours(label); hours != expected {
			t.Errorf("period %q=%v, want %v", label, hours, expected)
		}
	}
}

// Upstream renamed buckets to "<period> Limit Remaining"; the suffix is not
// part of the name, and a name that says nothing falls back to the window.
func TestAntigravityBucketLabelsFollowRenamedBuckets(t *testing.T) {
	for _, scenario := range []struct{ name, window, want string }{
		{"Five Hour Limit Remaining", "5h", "Gemini Models · 5小时限制 (5-Hour)"},
		{"Weekly Limit - Remaining", "weekly", "Gemini Models · 每周限制 (Weekly)"},
		{"Quota", "5h", "Gemini Models · 5小时限制 (5-Hour)"},
		{"Burst Remaining", "unreported", "Gemini Models · Burst"},
	} {
		if got := translateAntigravityBucketLabel("Gemini Models", scenario.name, scenario.window); got != scenario.want {
			t.Errorf("label(%q, %q) = %q, want %q", scenario.name, scenario.window, got, scenario.want)
		}
	}
}

// Shapes copied from the v0.3.0 plugin's quotaResponse: buckets carry no
// displayName or bucketId, and description holds the display text, the read
// time and any explanation.
const (
	pluginOllamaCookiePayload = `{
		"summary": [{"key": "balance", "label": "Balance", "value": 12.34, "format": "currency", "currency": "USD"}],
		"groups": [{
			"displayName": "Ollama",
			"buckets": [
				{"window": "5h", "remainingFraction": 0.6, "resetTime": "2026-11-12T13:00:00Z", "description": "5-hour session · read at 2026-11-12T08:00:00Z"},
				{"window": "7d", "remainingFraction": 0.8, "resetTime": "Resets in 3 hours", "description": "Weekly · read at 2026-11-12T08:00:00Z"}
			]
		}]
	}`

	pluginOllamaBalanceOnlyPayload = `{
		"summary": [{"key": "balance", "label": "Balance", "value": 5.5, "format": "currency", "currency": "USD"}]
	}`

	pluginCommandCodePayload = `{
		"subscription": {"plan": "Pro"},
		"summary": [{"key": "balance", "label": "Balance", "value": 321, "format": "number", "unit": "credits"}],
		"groups": [{
			"displayName": "Command Code",
			"buckets": [
				{"window": "5h", "remainingFraction": 0.8, "resetTime": "2026-11-12T13:00:00Z", "description": "5-hour session · read at 2026-11-12T08:00:00Z"},
				{"window": "7d", "remainingFraction": 0.55, "resetTime": "2026-11-15T00:00:00Z", "description": "Weekly · read at 2026-11-12T08:00:00Z"}
			]
		}]
	}`

	pluginOpenCodePayload = `{
		"groups": [{
			"displayName": "OpenCode Go",
			"buckets": [
				{"window": "rolling", "remainingFraction": 0.0, "resetTime": "2026-11-12T09:00:00Z", "description": "Rolling · read at 2026-11-12T08:00:00Z · upstream reports this window rate limited"},
				{"window": "weekly", "remainingFraction": 0.42, "resetTime": "2026-11-15T00:00:00Z", "description": "Weekly · read at 2026-11-12T08:00:00Z"},
				{"window": "monthly", "remainingFraction": 0.21, "resetTime": "2026-12-01T00:00:00Z", "description": "Monthly · read at 2026-11-12T08:00:00Z"}
			]
		}]
	}`

	pluginStalePayload = `{
		"groups": [{
			"displayName": "Ollama",
			"buckets": [
				{"window": "5h", "remainingFraction": 0.4, "resetTime": "2026-11-12T13:00:00Z", "description": "5-hour session · stale · last read at 2026-11-12T07:55:00Z · upstream request failed: 429 Too Many Requests"}
			]
		}]
	}`
)

func pluginTestNowMS() int64 {
	return time.Date(2026, 11, 12, 8, 30, 0, 0, time.UTC).UnixMilli()
}

func TestParsePluginQuotaWindowsOllamaCookie(t *testing.T) {
	windows, observedAtMS, stale, err := ParsePluginQuotaWindows([]byte(pluginOllamaCookiePayload), pluginTestNowMS(), 0)
	if err != nil {
		t.Fatalf("ParsePluginQuotaWindows failed: %v", err)
	}
	if stale {
		t.Error("stale = true for a fresh payload")
	}
	wantObserved := time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC).UnixMilli()
	if observedAtMS != wantObserved {
		t.Errorf("observedAtMS = %d, want %d", observedAtMS, wantObserved)
	}
	if len(windows) != 2 {
		t.Fatalf("len(windows) = %d, want 2: %+v", len(windows), windows)
	}

	fiveHour := windows[0]
	if fiveHour.ID != "plugin_Ollama_0" || fiveHour.Label != "Ollama · 5-hour session" {
		t.Errorf("five-hour window = (%q, %q), want (plugin_Ollama_0, Ollama · 5-hour session)", fiveHour.ID, fiveHour.Label)
	}
	if fiveHour.PeriodHours == nil || *fiveHour.PeriodHours != 5 {
		t.Errorf("five-hour period = %v, want 5", fiveHour.PeriodHours)
	}
	if fiveHour.RemainingPercent == nil || *fiveHour.RemainingPercent != 60 || fiveHour.UsedPercent == nil || *fiveHour.UsedPercent != 40 {
		t.Errorf("five-hour percent = rem:%v used:%v, want 60 and 40", fiveHour.RemainingPercent, fiveHour.UsedPercent)
	}
	wantReset := time.Date(2026, 11, 12, 13, 0, 0, 0, time.UTC).UnixMilli()
	if fiveHour.ResetAtMS == nil || *fiveHour.ResetAtMS != wantReset {
		t.Errorf("five-hour reset = %v, want %d", fiveHour.ResetAtMS, wantReset)
	}
	if !strings.Contains(fiveHour.ResetLabel, "13:00") || fiveHour.ResetAccuracy != "exact" {
		t.Errorf("five-hour reset label/accuracy = %q/%q, want a relative label and exact", fiveHour.ResetLabel, fiveHour.ResetAccuracy)
	}
	if fiveHour.Stale || fiveHour.Note != "" {
		t.Errorf("five-hour stale/note = %v/%q, want fresh without a note", fiveHour.Stale, fiveHour.Note)
	}

	weekly := windows[1]
	if weekly.Label != "Ollama · Weekly" || weekly.PeriodHours == nil || *weekly.PeriodHours != 168 {
		t.Errorf("weekly window = (%q, %v), want (Ollama · Weekly, 168)", weekly.Label, weekly.PeriodHours)
	}
	if weekly.ResetAtMS != nil || weekly.ResetLabel != "Resets in 3 hours" || weekly.ResetAccuracy != "" {
		t.Errorf("weekly reset = %v/%q/%q, want the upstream text kept without an exact boundary", weekly.ResetAtMS, weekly.ResetLabel, weekly.ResetAccuracy)
	}
}

// The api_key path publishes a balance and no windows: the parser reports no
// windows rather than inventing any, and the balance projection is the
// plugin-plan layer's job.
func TestParsePluginQuotaWindowsBalanceOnly(t *testing.T) {
	windows, observedAtMS, stale, err := ParsePluginQuotaWindows([]byte(pluginOllamaBalanceOnlyPayload), pluginTestNowMS(), 0)
	if err != nil {
		t.Fatalf("ParsePluginQuotaWindows failed: %v", err)
	}
	if len(windows) != 0 || observedAtMS != 0 || stale {
		t.Fatalf("windows/observed/stale = %d/%d/%v, want empty/0/false", len(windows), observedAtMS, stale)
	}
}

func TestParsePluginQuotaWindowsCommandCode(t *testing.T) {
	windows, observedAtMS, stale, err := ParsePluginQuotaWindows([]byte(pluginCommandCodePayload), pluginTestNowMS(), 0)
	if err != nil {
		t.Fatalf("ParsePluginQuotaWindows failed: %v", err)
	}
	if stale || observedAtMS != time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("stale/observedAtMS = %v/%d, want false and the read-at instant", stale, observedAtMS)
	}
	if len(windows) != 2 {
		t.Fatalf("len(windows) = %d, want 2: %+v", len(windows), windows)
	}
	for i, want := range []struct {
		label   string
		period  float64
		resetAt time.Time
	}{
		{"Command Code · 5-hour session", 5, time.Date(2026, 11, 12, 13, 0, 0, 0, time.UTC)},
		{"Command Code · Weekly", 168, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)},
	} {
		window := windows[i]
		if window.Label != want.label || window.PeriodHours == nil || *window.PeriodHours != want.period {
			t.Errorf("windows[%d] = (%q, %v), want (%q, %v)", i, window.Label, window.PeriodHours, want.label, want.period)
		}
		if window.ResetAtMS == nil || *window.ResetAtMS != want.resetAt.UnixMilli() {
			t.Errorf("windows[%d].ResetAtMS = %v, want %d", i, window.ResetAtMS, want.resetAt.UnixMilli())
		}
	}
}

func TestParsePluginQuotaWindowsOpenCode(t *testing.T) {
	windows, observedAtMS, stale, err := ParsePluginQuotaWindows([]byte(pluginOpenCodePayload), pluginTestNowMS(), 0)
	if err != nil {
		t.Fatalf("ParsePluginQuotaWindows failed: %v", err)
	}
	if stale || observedAtMS != time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("stale/observedAtMS = %v/%d, want false and the read-at instant", stale, observedAtMS)
	}
	if len(windows) != 3 {
		t.Fatalf("len(windows) = %d, want 3: %+v", len(windows), windows)
	}

	// The plugin sends rolling, weekly, monthly; the parser must not reorder
	// them, and must not guess a period for rolling or monthly.
	for i, want := range []struct {
		label  string
		period *float64
	}{{"OpenCode Go · Rolling", nil}, {"OpenCode Go · Weekly", ptrFloat(168)}, {"OpenCode Go · Monthly", nil}} {
		window := windows[i]
		if window.Label != want.label {
			t.Errorf("windows[%d].Label = %q, want %q", i, window.Label, want.label)
		}
		if (want.period == nil) != (window.PeriodHours == nil) {
			t.Errorf("windows[%d].PeriodHours = %v, want %v", i, window.PeriodHours, want.period)
		} else if want.period != nil && *window.PeriodHours != *want.period {
			t.Errorf("windows[%d].PeriodHours = %v, want %v", i, *window.PeriodHours, *want.period)
		}
	}

	rolling := windows[0]
	if rolling.RemainingPercent == nil || *rolling.RemainingPercent != 0 || rolling.UsedPercent == nil || *rolling.UsedPercent != 100 {
		t.Errorf("rolling percent = rem:%v used:%v, want 0 and 100", rolling.RemainingPercent, rolling.UsedPercent)
	}
	if rolling.Note != "upstream reports this window rate limited" {
		t.Errorf("rolling note = %q, want the upstream rate-limit explanation", rolling.Note)
	}
	if rolling.Stale {
		t.Error("rolling window marked stale without a stale segment")
	}
	if windows[2].ResetAtMS == nil || *windows[2].ResetAtMS != time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("monthly reset = %v, want 2026-12-01", windows[2].ResetAtMS)
	}
}

func TestParsePluginQuotaWindowsStale(t *testing.T) {
	windows, observedAtMS, stale, err := ParsePluginQuotaWindows([]byte(pluginStalePayload), pluginTestNowMS(), 0)
	if err != nil {
		t.Fatalf("ParsePluginQuotaWindows failed: %v", err)
	}
	if !stale {
		t.Error("stale = false for a cached reading")
	}
	wantObserved := time.Date(2026, 11, 12, 7, 55, 0, 0, time.UTC).UnixMilli()
	if observedAtMS != wantObserved {
		t.Errorf("observedAtMS = %d, want the last-read instant %d", observedAtMS, wantObserved)
	}
	if len(windows) != 1 {
		t.Fatalf("len(windows) = %d, want 1: %+v", len(windows), windows)
	}
	window := windows[0]
	if !window.Stale {
		t.Error("window.Stale = false, want true")
	}
	if window.Note != "upstream request failed: 429 Too Many Requests" {
		t.Errorf("window.Note = %q, want the stale reason", window.Note)
	}
	if window.Label != "Ollama · 5-hour session" {
		t.Errorf("window.Label = %q, want the description label", window.Label)
	}
	if window.ResetAtMS == nil {
		t.Error("window.ResetAtMS = nil, want the cached reset parsed as usual")
	}
}

// Sparse plugin buckets must not panic and must not be filled in with made-up
// figures: a missing fraction stays nil and an unstated reset stays empty.
func TestParsePluginQuotaWindowsSparseBuckets(t *testing.T) {
	raw := []byte(`{"groups":[{"buckets":[{"window":"rolling"},{"window":"7d","description":""}]}]}`)
	windows, observedAtMS, stale, err := ParsePluginQuotaWindows(raw, pluginTestNowMS(), 0)
	if err != nil {
		t.Fatalf("ParsePluginQuotaWindows failed: %v", err)
	}
	if len(windows) != 2 || observedAtMS != 0 || stale {
		t.Fatalf("windows/observed/stale = %d/%d/%v, want 2/0/false", len(windows), observedAtMS, stale)
	}

	rolling := windows[0]
	if rolling.Label != "Group 1 · rolling" || rolling.ID != "plugin_Group 1_0" {
		t.Errorf("nameless window = (%q, %q), want (Group 1 · rolling, plugin_Group 1_0)", rolling.Label, rolling.ID)
	}
	if rolling.PeriodHours != nil || rolling.RemainingPercent != nil || rolling.UsedPercent != nil {
		t.Errorf("missing fields filled in: %+v", rolling)
	}
	if rolling.ResetAtMS != nil || rolling.ResetLabel != "" || rolling.Note != "" || rolling.Stale {
		t.Errorf("unstated reset/note/stale invented: %+v", rolling)
	}
	if weekly := windows[1]; weekly.Label != "Group 1 · 7d" || weekly.PeriodHours == nil || *weekly.PeriodHours != 168 {
		t.Errorf("weekly window = (%q, %v), want (Group 1 · 7d, 168)", weekly.Label, weekly.PeriodHours)
	}

	// The id must be derived from payload order, not from a random source, so
	// snapshots of the same payload keep the same window ids.
	again, _, _, err := ParsePluginQuotaWindows(raw, pluginTestNowMS(), 0)
	if err != nil {
		t.Fatalf("second ParsePluginQuotaWindows failed: %v", err)
	}
	if len(again) != len(windows) {
		t.Fatalf("second parse returned %d windows, want %d", len(again), len(windows))
	}
	for i := range windows {
		if again[i].ID != windows[i].ID {
			t.Errorf("window %d id changed between parses: %q then %q", i, windows[i].ID, again[i].ID)
		}
	}
}

func ptrFloat(value float64) *float64 {
	return &value
}
