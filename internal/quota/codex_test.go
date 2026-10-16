package quota

import (
	"fmt"
	"math"
	"testing"
	"time"
)

func TestParseCodexUsageCurrentPayload(t *testing.T) {
	raw := []byte(`{
		"plan_type": "pro",
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {
				"used_percent": 1,
				"limit_window_seconds": 604800,
				"reset_after_seconds": 601888,
				"reset_at": 1785902974
			},
			"secondary_window": null
		},
		"code_review_rate_limit": null,
		"additional_rate_limits": [
			{
				"limit_name": "GPT-5.3-Codex-Spark",
				"metered_feature": "codex_bengalfox",
				"rate_limit": {
					"allowed": true,
					"limit_reached": false,
					"primary_window": {
						"used_percent": 0,
						"limit_window_seconds": 604800,
						"reset_after_seconds": 602111,
						"reset_at": 1785903197
					},
					"secondary_window": null
				}
			}
		],
		"rate_limit_reset_credits": {
			"available_count": 1,
			"applicable_available_count": 0
		}
	}`)

	nowMS := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	plan, windows, credits, err := ParseCodexUsage(raw, nowMS)
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}

	if plan.PlanType != "pro" || plan.Tier != "elite" || plan.PlanLabel != "Pro 200" {
		t.Errorf("plan = %+v, want pro/elite/Pro 200", plan)
	}

	if len(windows) != 2 {
		t.Fatalf("len(windows) = %d, want 2", len(windows))
	}

	// First window classified as weekly due to 604800s
	w1 := windows[0]
	if w1.ID != "weekly" {
		t.Errorf("w1.ID = %q, want weekly", w1.ID)
	}
	if w1.UsedPercent == nil || *w1.UsedPercent != 1.0 {
		t.Errorf("w1.UsedPercent = %v, want 1.0", w1.UsedPercent)
	}
	if w1.RemainingPercent == nil || *w1.RemainingPercent != 99.0 {
		t.Errorf("w1.RemainingPercent = %v, want 99.0", w1.RemainingPercent)
	}

	// Second window additional
	w2 := windows[1]
	if w2.Model != "GPT-5.3-Codex-Spark" {
		t.Errorf("w2.Model = %q, want GPT-5.3-Codex-Spark", w2.Model)
	}
	if w2.UsedPercent == nil || *w2.UsedPercent != 0.0 {
		t.Errorf("w2.UsedPercent = %v, want 0.0", w2.UsedPercent)
	}
	if w2.RemainingPercent == nil || *w2.RemainingPercent != 100.0 {
		t.Errorf("w2.RemainingPercent = %v, want 100.0", w2.RemainingPercent)
	}

	// Credits
	if credits == nil || credits.AvailableCount != 1 || credits.ApplicableAvailableCount != 0 {
		t.Errorf("credits = %+v, want available: 1, applicable: 0", credits)
	}
}

func TestParseCodexUsageCamelCaseReadsTheReportedShare(t *testing.T) {
	raw := []byte(`{
		"planType": "plus",
		"rateLimit": {
			"primaryWindow": {
				"usedPercent": 40,
				"limitWindowSeconds": 18000,
				"resetAfterSeconds": 120
			}
		}
	}`)

	nowMS := time.Now().UnixMilli()
	plan, windows, _, err := ParseCodexUsage(raw, nowMS)
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}

	if plan.Tier != "standard" || plan.PlanLabel != "Plus" {
		t.Errorf("plan = %+v, want standard/Plus", plan)
	}

	if len(windows) != 1 {
		t.Fatalf("len(windows) = %d, want 1", len(windows))
	}

	w := windows[0]
	if w.ID != "five_hour" {
		t.Errorf("w.ID = %q, want five_hour", w.ID)
	}
	if w.UsedPercent == nil || *w.UsedPercent != 40.0 {
		t.Errorf("w.UsedPercent = %v, want the reported 40.0", w.UsedPercent)
	}
	if w.RemainingPercent == nil || *w.RemainingPercent != 60.0 {
		t.Errorf("w.RemainingPercent = %v, want 60.0", w.RemainingPercent)
	}
	if w.ResetAccuracy != "derived" {
		t.Errorf("w.ResetAccuracy = %q, want derived", w.ResetAccuracy)
	}
}

// A window's used share is trustworthy only inside 0..100. A non-finite or out-of-range
// reading is unknown, not a 100% exhaustion: clamping it would manufacture the very weekly
// exhaustion evidence that closes the account. A true 0 and a true 100 are kept unchanged.
func TestParseCodexUsageRejectsOutOfRangeShares(t *testing.T) {
	cases := []struct {
		name    string
		used    string
		wantNil bool
		want    float64
	}{
		{"a negative reading is rejected", `-1`, true, 0},
		{"one over the scale is rejected", `101`, true, 0},
		{"far over the scale is rejected", `140`, true, 0},
		{"a string over the scale is rejected", `"101"`, true, 0},
		{"a true zero is kept", `0`, false, 0},
		{"a true hundred is kept", `100`, false, 100},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			raw := []byte(`{"plan_type":"plus","rate_limit":{"primary_window":{"used_percent":` +
				testCase.used + `,"limit_window_seconds":18000}}}`)
			_, windows, _, err := ParseCodexUsage(raw, time.Now().UnixMilli())
			if err != nil {
				t.Fatalf("ParseCodexUsage failed: %v", err)
			}
			if len(windows) != 1 {
				t.Fatalf("len(windows) = %d, want 1", len(windows))
			}
			if testCase.wantNil {
				if windows[0].UsedPercent != nil {
					t.Fatalf("used = %v, want unknown for an untrustworthy reading", *windows[0].UsedPercent)
				}
				if windows[0].RemainingPercent != nil {
					t.Fatalf("remaining = %v, want unknown", *windows[0].RemainingPercent)
				}
				return
			}
			if windows[0].UsedPercent == nil || *windows[0].UsedPercent != testCase.want {
				t.Fatalf("used = %v, want %v", windows[0].UsedPercent, testCase.want)
			}
			if windows[0].RemainingPercent == nil || *windows[0].RemainingPercent != 100-testCase.want {
				t.Fatalf("remaining = %v, want %v", windows[0].RemainingPercent, 100-testCase.want)
			}
		})
	}
}

// A global rate-limit flag covers the rate limit as a whole, never a single window's
// reading. A window that published a real 0..100 share keeps it (a genuine 100% still
// reports 100%), and a window that published nothing stays unknown instead of being
// pinned to a fabricated 100%.
func TestParseCodexUsageKeepsReportedReadingsWhenTheRateLimitIsReached(t *testing.T) {
	raw := []byte(`{
		"plan_type": "pro",
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {
				"used_percent": "NaN",
				"limit_window_seconds": 18000
			}
		},
		"code_review_rate_limit": {
			"allowed": false,
			"primary_window": {
				"used_percent": 10,
				"limit_window_seconds": 18000
			}
		}
	}`)

	_, windows, _, err := ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("len(windows) = %d, want 2", len(windows))
	}
	if windows[0].UsedPercent != nil {
		t.Fatalf("non-finite used_percent = %v, want it omitted", windows[0].UsedPercent)
	}
	if windows[1].ID != "code_review_5h" {
		t.Fatalf("code-review window id = %q, want code_review_5h", windows[1].ID)
	}
	if windows[1].UsedPercent == nil || *windows[1].UsedPercent != 10 {
		t.Fatalf("limit-reached code-review used_percent = %v, want the reported 10", windows[1].UsedPercent)
	}
}

// A limit_reached / allowed=false flag must never fabricate a window's usage. A real
// 100% reading is kept and an untouched window keeps its own share; a window that
// published no reading stays unknown even when it is the most-used one.
func TestParseCodexUsageLimitReachedNeverFabricatesWindowUsage(t *testing.T) {
	raw := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"allowed": false,
			"limit_reached": true,
			"primary_window": {"used_percent": 100, "limit_window_seconds": 18000, "reset_after_seconds": 10000},
			"secondary_window": {"used_percent": 37, "limit_window_seconds": 604800, "reset_after_seconds": 400000}
		}
	}`)
	_, windows, _, err := ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}
	if len(windows) != 2 {
		t.Fatalf("len(windows) = %d, want 2", len(windows))
	}
	if windows[0].ID != "five_hour" || windows[0].RemainingPercent == nil || *windows[0].RemainingPercent != 0 {
		t.Fatalf("five_hour = %+v, want the true 100%% reading kept", windows[0])
	}
	if windows[1].ID != "weekly" || windows[1].UsedPercent == nil || *windows[1].UsedPercent != 37 {
		t.Fatalf("weekly used = %v, want the reported 37", windows[1].UsedPercent)
	}

	// The most-used window is not promoted to 100: a rounded 99 is still 99, not spent.
	raw = []byte(`{"rate_limit": {"limit_reached": true,
		"primary_window": {"used_percent": 20, "limit_window_seconds": 18000},
		"secondary_window": {"used_percent": 99, "limit_window_seconds": 604800}}}`)
	_, windows, _, err = ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}
	if *windows[0].UsedPercent != 20 || *windows[1].UsedPercent != 99 {
		t.Fatalf("used = %v / %v, want the reported 20 / 99", *windows[0].UsedPercent, *windows[1].UsedPercent)
	}

	// A window that published no reading stays unknown; a real weekly share beside a
	// global flag keeps its value.
	raw = []byte(`{"rate_limit": {"allowed": false,
		"primary_window": {"limit_window_seconds": 18000},
		"secondary_window": {"used_percent": 43, "limit_window_seconds": 604800}}}`)
	_, windows, _, err = ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}
	if windows[0].UsedPercent != nil {
		t.Fatalf("unreported five-hour used = %v, want unknown", *windows[0].UsedPercent)
	}
	if windows[1].UsedPercent == nil || *windows[1].UsedPercent != 43 {
		t.Fatalf("weekly used = %v, want the reported 43", windows[1].UsedPercent)
	}
}

// The reachable false positive: the primary five-hour window is genuinely at 100%, the
// account-level weekly window published a future reset but no used_percent, and the rate
// limit is flagged reached. The weekly window is unknown, not spent, so no synthetic
// weekly 100% may appear. Under the former attribution this input recorded a weekly 100%
// observation, which is what drove the account auto-manager to disconnect the credential.
func TestParseCodexUsageGlobalLimitLeavesAnUnreportedWeeklyWindowUnknown(t *testing.T) {
	nowMS := time.Now().UnixMilli()
	// Keep the reset on a whole second: the wire field carries epoch seconds, so the
	// parser reconstructs it as resetSeconds*1000.
	resetInFutureMS := (nowMS/1000)*1000 + int64(72*time.Hour/time.Millisecond)
	resetSeconds := fmt.Sprintf("%d", resetInFutureMS/1000)

	cases := []struct {
		name string
		raw  string
	}{
		{
			name: "snake_case",
			raw: `{
				"plan_type": "plus",
				"rate_limit": {
					"allowed": false,
					"limit_reached": true,
					"primary_window": {"used_percent": 100, "limit_window_seconds": 18000, "reset_after_seconds": 3600},
					"secondary_window": {"limit_window_seconds": 604800, "reset_at": ` + resetSeconds + `}
				}
			}`,
		},
		{
			name: "camelCase",
			raw: `{
				"planType": "plus",
				"rateLimit": {
					"allowed": false,
					"limitReached": true,
					"primaryWindow": {"usedPercent": 100, "limitWindowSeconds": 18000, "resetAfterSeconds": 3600},
					"secondaryWindow": {"limitWindowSeconds": 604800, "resetAt": ` + resetSeconds + `}
				}
			}`,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, windows, _, err := ParseCodexUsage([]byte(testCase.raw), nowMS)
			if err != nil {
				t.Fatalf("ParseCodexUsage failed: %v", err)
			}
			byID := map[string]QuotaWindow{}
			for _, window := range windows {
				byID[window.ID] = window
			}
			fiveHour, ok := byID["five_hour"]
			if !ok || fiveHour.UsedPercent == nil || *fiveHour.UsedPercent != 100 {
				t.Fatalf("five-hour = %+v, want the real 100%% reading kept", fiveHour)
			}
			weekly, ok := byID["weekly"]
			if !ok {
				t.Fatalf("weekly window missing from %+v", windows)
			}
			if weekly.UsedPercent != nil {
				t.Fatalf("weekly used = %v, want unknown (nil) rather than a synthetic 100", *weekly.UsedPercent)
			}
			if weekly.ResetAtMS == nil {
				t.Fatalf("weekly reset missing, want the published future reset %d", resetInFutureMS)
			}
			if *weekly.ResetAtMS != resetInFutureMS {
				t.Fatalf("weekly reset = %d, want %d", *weekly.ResetAtMS, resetInFutureMS)
			}
		})
	}
}

// A code-review or model-scoped limit is not the account-level weekly window: even when
// its own rate limit is flagged reached and its windows publish nothing, the standard
// weekly window must keep the share it reported.
func TestParseCodexUsageScopedLimitsDoNotElevateTheStandardWeeklyWindow(t *testing.T) {
	raw := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {"used_percent": 100, "limit_window_seconds": 18000},
			"secondary_window": {"used_percent": 40, "limit_window_seconds": 604800, "reset_after_seconds": 500000}
		},
		"code_review_rate_limit": {
			"allowed": false,
			"limit_reached": true,
			"primary_window": {"limit_window_seconds": 18000}
		},
		"additional_rate_limits": [
			{
				"limit_name": "gpt-reserve",
				"rate_limit": {
					"allowed": false,
					"limit_reached": true,
					"primary_window": {"limit_window_seconds": 604800, "reset_after_seconds": 400000}
				}
			}
		]
	}`)
	_, windows, _, err := ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}
	byID := map[string]QuotaWindow{}
	for _, window := range windows {
		byID[window.ID] = window
	}
	if weekly := byID["weekly"]; weekly.UsedPercent == nil || *weekly.UsedPercent != 40 {
		t.Fatalf("standard weekly used = %v, want the reported 40", weekly.UsedPercent)
	}
	if cr := byID["code_review_5h"]; cr.Scope != "code_review" || cr.UsedPercent != nil {
		t.Fatalf("code-review 5h = %+v, want code_review scope and an unknown reading", cr)
	}
	if reserve := byID["addl_0_p"]; reserve.Scope != "model" || reserve.UsedPercent != nil {
		t.Fatalf("model reserve = %+v, want model scope and an unknown reading", reserve)
	}
}

// A plan whose own window is not the usual five-hour one is described by the limit it actually
// has. A free account reports a monthly limit as the primary window, so the slot's five-hour name
// both mislabelled a 30-day limit and filed it under the id every cycle calculation keys on.
func TestParseCodexUsageNamesTheStandardWindowByItsOwnPeriod(t *testing.T) {
	raw := []byte(`{
		"plan_type": "free",
		"rate_limit": {
			"allowed": true,
			"limit_reached": false,
			"primary_window": {"used_percent": 0, "limit_window_seconds": 2592000, "reset_at": 1793180280}
		}
	}`)

	_, windows, _, err := ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}
	if len(windows) != 1 {
		t.Fatalf("len(windows) = %d, want 1", len(windows))
	}
	if windows[0].ID != "monthly" || windows[0].Kind != "monthly" {
		t.Errorf("id/kind = %q/%q, want monthly/monthly", windows[0].ID, windows[0].Kind)
	}
	if windows[0].PeriodHours == nil || *windows[0].PeriodHours != 720 {
		t.Errorf("PeriodHours = %v, want 720", windows[0].PeriodHours)
	}

	// The paid shape is unaffected: a five-hour primary window keeps the five-hour identity.
	raw = []byte(`{"plan_type": "plus", "rate_limit": {"primary_window": {"used_percent": 12, "limit_window_seconds": 18000}}}`)
	_, windows, _, err = ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}
	if windows[0].ID != "five_hour" || windows[0].Kind != "five_hour" {
		t.Errorf("five-hour id/kind = %q/%q, want five_hour/five_hour", windows[0].ID, windows[0].Kind)
	}
}

func TestParseCodexResetCreditsPayload(t *testing.T) {
	raw := []byte(`{
		"available_count": 3,
		"applicable_available_count": 1,
		"credits": [
			{
				"id": "credit-1",
				"status": "available",
				"reset_type": "codex_rate_limits",
				"granted_at": "1790000000",
				"expires_at": "1810000000"
			},
			{
				"id": "credit-2",
				"status": "used",
				"reset_type": "codex_rate_limits",
				"granted_at": "1790000000",
				"expires_at": "1805000000"
			},
			{
				"id": "credit-3",
				"status": "available",
				"reset_type": "sponsor_perk",
				"granted_at": "1790000000",
				"expires_at": "1820000000"
			},
			{
				"id": "credit-4",
				"status": "available",
				"grantedAt": "1790000000",
				"expiresAt": "1830000000"
			}
		]
	}`)

	info, err := ParseCodexResetCreditsPayload(raw)
	if err != nil {
		t.Fatalf("ParseCodexResetCreditsPayload returned error: %v", err)
	}
	if info.AvailableCount != 3 {
		t.Errorf("AvailableCount = %d, want 3", info.AvailableCount)
	}
	if info.ApplicableAvailableCount != 1 {
		t.Errorf("ApplicableAvailableCount = %d, want 1", info.ApplicableAvailableCount)
	}
	// Only codex_rate_limits + available credits survive: credit-1 and credit-4.
	if len(info.Credits) != 2 {
		t.Fatalf("len(credits) = %d, want 2", len(info.Credits))
	}
	first := info.Credits[0]
	if first.ID != "credit-1" || first.ExpiresAtMS == nil || *first.ExpiresAtMS != 1810000000000 {
		t.Errorf("first credit = %+v, want credit-1 expiring at 1810000000000", first)
	}
	if second := info.Credits[1]; second.ID != "credit-4" {
		t.Errorf("second credit = %+v, want credit-4 (camelCase fallback)", second)
	}
}

// float64(math.MaxInt64) rounds up to 2^63, one past the largest int64, so the
// upper bound has to be inclusive: a value of exactly 2^63 used to pass the guard
// and then convert to a negative int64.
func TestToInt64RejectsTheRoundedMaxInt64Boundary(t *testing.T) {
	const twoToTheSixtyThree = 9223372036854775808.0

	if got, ok := toInt64(twoToTheSixtyThree); ok {
		t.Fatalf("toInt64(2^63) = %d, want it rejected", got)
	}

	largest := math.Nextafter(twoToTheSixtyThree, 0)
	got, ok := toInt64(largest)
	if !ok {
		t.Fatalf("toInt64(%v) was rejected", largest)
	}
	if got < 0 {
		t.Fatalf("toInt64(%v) = %d, want a positive value", largest, got)
	}

	minimum, ok := toInt64(-twoToTheSixtyThree)
	if !ok || minimum != math.MinInt64 {
		t.Fatalf("toInt64(-2^63) = %d, %v; want %d, true", minimum, ok, int64(math.MinInt64))
	}

	for _, value := range []any{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got, ok := toInt64(value); ok {
			t.Fatalf("toInt64(%v) = %d, want it rejected", value, got)
		}
	}
}

func TestParseCodexSubscription(t *testing.T) {
	until, shouldRenew, ok := ParseCodexSubscription([]byte(`{
		"plan_type": "plus",
		"active_start": "2026-08-29T14:02:31Z",
		"active_until": "2026-09-29T14:02:31Z",
		"will_renew": true
	}`))
	if !ok {
		t.Fatal("ParseCodexSubscription rejected a well-formed payload")
	}
	want := time.Date(2026, 9, 29, 14, 2, 31, 0, time.UTC).UnixMilli()
	if until != want {
		t.Errorf("until = %d, want %d", until, want)
	}
	if shouldRenew == nil || !*shouldRenew {
		t.Errorf("shouldRenew = %v, want true", shouldRenew)
	}

	// A response without a usable window must be reported as absent so the
	// caller keeps its previous source instead of overwriting it with zero.
	for _, raw := range []string{
		`{"active_until": null}`,
		`{"active_until": ""}`,
		`{}`,
		`not json`,
	} {
		if _, _, ok := ParseCodexSubscription([]byte(raw)); ok {
			t.Errorf("ParseCodexSubscription(%s) = ok, want rejected", raw)
		}
	}
}

// A model-scoped limit is named from the period upstream metered. Codex moves the same
// reserve limit between windows, so a label that hardcodes "5小时" would contradict the
// payload: the live account reported `gpt-reserve` under a weekly primary window.
func TestParseCodexUsageNamesModelLimitsByTheirOwnPeriod(t *testing.T) {
	raw := []byte(`{
		"plan_type": "plus",
		"rate_limit": {
			"primary_window": {"used_percent": 0, "limit_window_seconds": 18000, "reset_after_seconds": 3600}
		},
		"additional_rate_limits": [
			{
				"limit_name": "gpt-reserve",
				"rate_limit": {
					"primary_window": {"used_percent": 0, "limit_window_seconds": 604800, "reset_after_seconds": 86400}
				}
			},
			{
				"limit_name": "o-series",
				"rate_limit": {
					"primary_window": {"used_percent": 10, "limit_window_seconds": 18000, "reset_after_seconds": 3600}
				}
			}
		],
		"code_review_rate_limit": {
			"primary_window": {"used_percent": 5, "limit_window_seconds": 18000}
		}
	}`)

	_, windows, _, err := ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}

	labels := make(map[string]string)
	for _, window := range windows {
		labels[window.ID] = window.Label
	}

	if got := labels["addl_0_p"]; got != "gpt-reserve 每周配额" {
		t.Errorf("weekly reserve label = %q, want %q", got, "gpt-reserve 每周配额")
	}
	if got := labels["addl_1_p"]; got != "o-series 5小时配额" {
		t.Errorf("five-hour model label = %q, want %q", got, "o-series 5小时配额")
	}
	if got := labels["code_review_5h"]; got != "代码审查 5小时配额" {
		t.Errorf("code-review label = %q, want %q", got, "代码审查 5小时配额")
	}
	if got := labels["five_hour"]; got != "5小时用量上限 (5-Hour)" {
		t.Errorf("standard five-hour label = %q, want the standard wording", got)
	}
}

// A window whose size the payload omits cannot be named from its period, so the label the
// caller supplied for that slot stands: an empty or truncated label would lose the only
// description the window has.
func TestParseCodexUsageKeepsSlotLabelWhenWindowSizeIsMissing(t *testing.T) {
	raw := []byte(`{
		"plan_type": "plus",
		"code_review_rate_limit": {
			"primary_window": {"used_percent": 5},
			"secondary_window": {"used_percent": 9}
		},
		"additional_rate_limits": [
			{"limit_name": "o-series", "rate_limit": {"primary_window": {"used_percent": 3}}}
		]
	}`)

	_, windows, _, err := ParseCodexUsage(raw, time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("ParseCodexUsage failed: %v", err)
	}
	labels := make(map[string]string)
	for _, window := range windows {
		labels[window.ID] = window.Label
	}
	if labels["code_review_5h"] != "代码审查 5小时配额" {
		t.Errorf("code-review five-hour label = %q, want the slot wording back", labels["code_review_5h"])
	}
	if labels["code_review_weekly"] != "代码审查 每周配额" {
		t.Errorf("code-review weekly label = %q, want the slot wording back", labels["code_review_weekly"])
	}
	if labels["addl_0_p"] != "o-series" {
		t.Errorf("model label = %q, want the model name alone rather than a guessed slot", labels["addl_0_p"])
	}
}

// A window shorter than an hour is named in minutes. Rounded to hours it reads as "0小时",
// which is not a period at all - and a provider that meters a short window would have it
// described as no window.
func TestWindowPeriodNameKeepsShortWindowsReadable(t *testing.T) {
	for _, one := range []struct {
		seconds float64
		want    string
	}{
		{seconds: 90, want: "2分钟"},
		{seconds: 600, want: "10分钟"},
		{seconds: 1800, want: "30分钟"},
		{seconds: FiveHourSeconds, want: "5小时"},
		{seconds: DailySeconds, want: "每日"},
		{seconds: WeeklySeconds, want: "每周"},
		{seconds: MonthlySeconds, want: "每月"},
		{seconds: 0, want: ""},
	} {
		if got := windowPeriodName(one.seconds); got != one.want {
			t.Errorf("windowPeriodName(%v) = %q, want %q", one.seconds, got, one.want)
		}
	}
}

func TestResolveCodexPlanTierNamesCurrentPlans(t *testing.T) {
	cases := []struct {
		planType  string
		wantTier  string
		wantLabel string
	}{
		{"pro", "elite", "Pro 200"},
		{"ProLite", "premium", "Pro 100"},
		{"pro-lite", "premium", "Pro 100"},
		{"pro_lite", "premium", "Pro 100"},
		{"self_serve_business_prolite", "premium", "Business Premium"},
		{"plus", "standard", "Plus"},
	}
	for _, tc := range cases {
		tier, label := resolveCodexPlanTier(tc.planType)
		if tier != tc.wantTier || label != tc.wantLabel {
			t.Errorf("resolveCodexPlanTier(%q) = %q/%q, want %q/%q", tc.planType, tier, label, tc.wantTier, tc.wantLabel)
		}
	}
}

func TestParseCodexUsageReadsAccountCredits(t *testing.T) {
	cases := []struct {
		name    string
		credits string
		want    *QuotaCredits
	}{
		{"string balance", `{"has_credits":true,"unlimited":false,"balance":" 12.50 "}`, &QuotaCredits{Balance: "12.50"}},
		{"numeric balance", `{"unlimited":false,"balance":7}`, &QuotaCredits{Balance: "7"}},
		{"zero balance is a reading", `{"unlimited":false,"balance":"0"}`, &QuotaCredits{Balance: "0"}},
		{"unlimited", `{"unlimited":true,"balance":null}`, &QuotaCredits{IsUnlimited: true}},
		{"no balance stated", `{"has_credits":false,"unlimited":false}`, nil},
		{"malformed balance", `{"unlimited":false,"balance":"-3"}`, nil},
		{"non-decimal balance", `{"unlimited":false,"balance":"1e3"}`, nil},
		{"absent", `null`, nil},
	}
	for _, tc := range cases {
		raw := []byte(`{"plan_type":"plus","credits":` + tc.credits + `}`)
		plan, _, _, err := ParseCodexUsage(raw, 0)
		if err != nil {
			t.Fatalf("%s: ParseCodexUsage failed: %v", tc.name, err)
		}
		got := plan.Credits
		if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
			t.Errorf("%s: credits = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}
