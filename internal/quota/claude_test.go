package quota

import (
	"testing"
	"time"
)

func TestParseClaudeUsageAndFable(t *testing.T) {
	modernReset := "2026-07-27T10:00:00.000Z"
	raw := []byte(`{
		"five_hour": {
			"utilization": 25.5,
			"resets_at": "` + modernReset + `"
		},
		"seven_day": {
			"utilization": 80.0,
			"resets_at": "` + modernReset + `"
		},
		"limits": [
			{
				"kind": "weekly_scoped",
				"percent": 64,
				"resets_at": "` + modernReset + `",
				"is_active": true,
				"scope": {
					"model": {
						"id": null,
						"display_name": "Fable"
					}
				}
			}
		],
		"extra_usage": {
			"is_enabled": true,
			"monthly_limit": 5000,
			"used_credits": 2500,
			"utilization": 50.0
		}
	}`)

	nowMS := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	windows, extraUsage, err := ParseClaudeUsage(raw, nowMS)
	if err != nil {
		t.Fatalf("ParseClaudeUsage failed: %v", err)
	}

	if len(windows) != 3 {
		t.Fatalf("len(windows) = %d, want 3", len(windows))
	}

	// 5-hour
	w0 := windows[0]
	if w0.ID != "five_hour" || *w0.UsedPercent != 25.5 || *w0.RemainingPercent != 74.5 {
		t.Errorf("w0 = %+v", w0)
	}

	// seven_day
	w1 := windows[1]
	if w1.ID != "seven_day" || *w1.UsedPercent != 80.0 || *w1.RemainingPercent != 20.0 {
		t.Errorf("w1 = %+v", w1)
	}

	// Fable
	w2 := windows[2]
	if w2.ID != "seven_day_fable" || *w2.UsedPercent != 64.0 || *w2.RemainingPercent != 36.0 {
		t.Errorf("w2 = %+v", w2)
	}

	// Extra usage
	if extraUsage == nil || !extraUsage.IsEnabled || extraUsage.MonthlyLimitCents != 5000 || extraUsage.UsedCreditsCents != 2500 {
		t.Errorf("extraUsage = %+v", extraUsage)
	}
}

// A utilization share is trustworthy only inside 0..100. A non-finite or out-of-range reading
// is unknown, not a 100% exhaustion: clamping 101 (or a negative value) to 100 would manufacture
// the weekly exhaustion evidence that closes the account. The account seven-day window keeps its
// standard scope and a true 0/100 stays exactly as reported.
func TestParseClaudeUsageRejectsOutOfRangeUtilization(t *testing.T) {
	cases := []struct {
		name    string
		util    string
		wantNil bool
		want    float64
	}{
		{"a utilization one over the scale", `101`, true, 0},
		{"a negative utilization", `-1`, true, 0},
		{"a non-numeric utilization", `"not-a-number"`, true, 0},
		{"a true zero", `0`, false, 0},
		{"a true hundred", `100`, false, 100},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			raw := []byte(`{"seven_day":{"utilization":` + testCase.util + `,"resets_at":"2026-07-27T10:00:00Z"}}`)
			windows, _, err := ParseClaudeUsage(raw, time.Now().UnixMilli())
			if err != nil {
				t.Fatalf("ParseClaudeUsage failed: %v", err)
			}
			if len(windows) != 1 {
				t.Fatalf("len(windows) = %d, want 1", len(windows))
			}
			window := windows[0]
			if window.ID != "seven_day" || window.Scope != "standard" || window.PeriodHours == nil || *window.PeriodHours != 168 {
				t.Fatalf("window = %+v, want the standard seven-day account window", window)
			}
			if testCase.wantNil {
				if window.UsedPercent != nil {
					t.Fatalf("used = %v, want unknown for an untrustworthy utilization", *window.UsedPercent)
				}
				if window.RemainingPercent != nil {
					t.Fatalf("remaining = %v, want unknown", *window.RemainingPercent)
				}
				return
			}
			if window.UsedPercent == nil || *window.UsedPercent != testCase.want {
				t.Fatalf("used = %v, want %v", window.UsedPercent, testCase.want)
			}
			if window.RemainingPercent == nil || *window.RemainingPercent != 100-testCase.want {
				t.Fatalf("remaining = %v, want %v", window.RemainingPercent, 100-testCase.want)
			}
		})
	}
}

func TestParseClaudeProfile(t *testing.T) {
	rawPro := []byte(`{
		"account": { "has_claude_pro": true, "has_claude_max": false },
		"organization": { "rate_limit_tier": "scale" }
	}`)
	planPro := ParseClaudeProfile(rawPro)
	if planPro.PlanType != "pro" || planPro.Tier != "premium" || planPro.PlanLabel != "Claude Pro" {
		t.Errorf("planPro = %+v", planPro)
	}

	rawMax := []byte(`{
		"account": { "has_claude_pro": true, "has_claude_max": true }
	}`)
	planMax := ParseClaudeProfile(rawMax)
	if planMax.PlanType != "max" || planMax.Tier != "elite" || planMax.PlanLabel != "Claude Max" {
		t.Errorf("planMax = %+v", planMax)
	}
}

func TestParseClaudeResetGrants(t *testing.T) {
	raw := `{"cedar_ember":{"eligible":true,"at_limit":false,` +
		`"grants":[` +
		`{"id":"grant-1","label":"Weekly Reset","resets_total":2,"resets_left":1,` +
		`"starts_at":"2026-10-01T00:00:00Z","ends_at":"2026-10-08T00:00:00Z",` +
		`"clears":["five_hour","seven_day"],"paused":false,"usable_now":true,"use_requires_limit":true},` +
		`{"id":"grant-2","resets_total":1,"resets_left":0,"ends_at":null,"clears":["future_window"]}` +
		`]}}`
	grants := ParseClaudeResetGrants([]byte(raw))
	if len(grants) != 2 {
		t.Fatalf("grants = %+v, want 2", grants)
	}
	first := grants[0]
	if first.Label != "Weekly Reset" || first.ResetsTotal != 2 || first.ResetsLeft != 1 {
		t.Errorf("first = %+v, want the weekly grant", first)
	}
	if first.EndsAtMS == nil {
		t.Error("first grant missing expiry")
	}
	if len(first.Clears) != 2 || first.Clears[0] != "five_hour" {
		t.Errorf("first clears = %v, want both windows", first.Clears)
	}
	// Unknown future windows are dropped, not rejected.
	if len(grants[1].Clears) != 0 || grants[1].Label != "" || grants[1].EndsAtMS != nil {
		t.Errorf("second = %+v, want empty clears/label/expiry", grants[1])
	}
}

func TestParseClaudeResetGrantsRejectsMalformedBlock(t *testing.T) {
	for _, testCase := range []struct {
		name string
		raw  string
	}{
		{name: "missing block", raw: `{}`},
		{name: "missing eligible", raw: `{"cedar_ember":{"grants":[]}}`},
		{name: "bad id", raw: `{"cedar_ember":{"eligible":true,"grants":[{"id":"BAD ID!","resets_total":1,"resets_left":1}]}}`},
		{name: "left exceeds total", raw: `{"cedar_ember":{"eligible":true,"grants":[{"id":"g1","resets_total":1,"resets_left":2}]}}`},
		{name: "float count", raw: `{"cedar_ember":{"eligible":true,"grants":[{"id":"g1","resets_total":1.5,"resets_left":1}]}}`},
		{name: "huge count", raw: `{"cedar_ember":{"eligible":true,"grants":[{"id":"g1","resets_total":1e300,"resets_left":1}]}}`},
		{name: "duplicate id", raw: `{"cedar_ember":{"eligible":true,"grants":[{"id":"g1","resets_total":1,"resets_left":1},{"id":"g1","resets_total":1,"resets_left":1}]}}`},
		{name: "bad timestamp", raw: `{"cedar_ember":{"eligible":true,"grants":[{"id":"g1","resets_total":1,"resets_left":1,"ends_at":"yesterday"}]}}`},
		{name: "non-boolean flag", raw: `{"cedar_ember":{"eligible":true,"grants":[{"id":"g1","resets_total":1,"resets_left":1,"paused":"no"}]}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if grants := ParseClaudeResetGrants([]byte(testCase.raw)); grants != nil {
				t.Fatalf("ParseClaudeResetGrants = %+v, want nil", grants)
			}
		})
	}
}
