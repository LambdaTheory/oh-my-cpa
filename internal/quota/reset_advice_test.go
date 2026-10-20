package quota

import (
	"math"
	"slices"
	"testing"
	"time"
)

func TestAdviseReset(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	expiry, reset := now.Add(48*time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli()
	for _, test := range []struct {
		name                      string
		change                    func(*ResetAdviceInput)
		verdict, evidence, reason string
	}{
		{"expiring with room", func(*ResetAdviceInput) {}, ResetAdviceUseBeforeExpiry, "projection", "reset_expiring_soon"},
		{"emergency takes precedence", func(i *ResetAdviceInput) { i.Window.UsedPercent = floatPointer(90) }, ResetAdviceKeepForEmergency, "projection", "quota_near_exhaustion"},
		{"exhausted", func(i *ResetAdviceInput) { i.Window.UsedPercent = floatPointer(100) }, ResetAdviceKeepForEmergency, "projection", "quota_near_exhaustion"},
		{"below threshold", func(i *ResetAdviceInput) { i.Window.UsedPercent = floatPointer(89.99) }, ResetAdviceUseBeforeExpiry, "projection", "reset_expiring_soon"},
		{"configured shorter horizon", func(i *ResetAdviceInput) { i.ExpiryWindow = 24 * time.Hour }, ResetAdviceNoAction, "projection", "reset_not_expiring_soon"},
		{"inclusive horizon", func(i *ResetAdviceInput) { i.ExpiryWindow = 48 * time.Hour }, ResetAdviceUseBeforeExpiry, "projection", "reset_expiring_soon"},
		{"missing snapshot", func(i *ResetAdviceInput) { i.ObservedAtMS = 0 }, ResetAdviceUnknown, "unknown", "reading_missing"},
		{"stale entitlement despite fresh quota", func(i *ResetAdviceInput) {
			i.ObservedAtMS = now.Add(-3 * time.Hour).UnixMilli()
			at := now.UnixMilli()
			i.Window.ObservedAtMS = &at
		}, ResetAdviceUnknown, "unknown", "reset_reading_stale"},
		{"future snapshot", func(i *ResetAdviceInput) { i.ObservedAtMS++ }, ResetAdviceUnknown, "unknown", "reset_reading_stale"},
		{"missing reset reading", func(i *ResetAdviceInput) { i.ResetCredits = nil }, ResetAdviceUnknown, "unknown", "reset_reading_missing"},
		{"missing quota", func(i *ResetAdviceInput) { i.Window.UsedPercent = nil }, ResetAdviceUnknown, "unknown", "quota_reading_missing"},
		{"stale quota", func(i *ResetAdviceInput) { at := now.Add(-3 * time.Hour).UnixMilli(); i.Window.ObservedAtMS = &at }, ResetAdviceUnknown, "unknown", "quota_reading_stale"},
		{"invalid quota", func(i *ResetAdviceInput) { i.Window.UsedPercent = floatPointer(math.NaN()) }, ResetAdviceUnknown, "unknown", "invalid_used_percent"},
		{"expired quota cycle", func(i *ResetAdviceInput) { at := now.UnixMilli(); i.Window.ResetAtMS = &at }, ResetAdviceUnknown, "unknown", "quota_cycle_expired"},
		{"missing quota boundary", func(i *ResetAdviceInput) { i.Window.ResetAtMS = nil }, ResetAdviceUnknown, "unknown", "quota_boundary_unknown"},
		{"failed reading", func(i *ResetAdviceInput) { i.Status = "error" }, ResetAdviceUnknown, "unknown", "reading_error"},
		{"expired entitlement", func(i *ResetAdviceInput) { at := now.UnixMilli(); i.ResetCredits.Credits[0].ExpiresAtMS = &at }, ResetAdviceNoAction, "fact", "no_remaining_resets"},
		{"published zero", func(i *ResetAdviceInput) { i.ResetCredits = &CodexResetCreditsInfo{} }, ResetAdviceNoAction, "fact", "no_remaining_resets"},
		{"missing expiry", func(i *ResetAdviceInput) { i.ResetCredits.Credits[0].ExpiresAtMS = nil }, ResetAdviceUnknown, "unknown", "reset_expiry_unknown"},
		{"missing credit details", func(i *ResetAdviceInput) { i.ResetCredits.AvailableCount = 2 }, ResetAdviceUnknown, "unknown", "reset_details_incomplete"},
		{"scoped quota", func(i *ResetAdviceInput) { i.Window.Scope = "model" }, ResetAdviceUnknown, "unknown", "reset_scope_unknown"},
		{"unsupported provider", func(i *ResetAdviceInput) { i.Provider = "other" }, ResetAdviceUnknown, "unknown", "provider_unsupported"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := ResetAdviceInput{Provider: "codex", Status: "healthy", ObservedAtMS: now.UnixMilli(), NowMS: now.UnixMilli(),
				Window:       QuotaWindow{ID: "five_hour", Kind: "five_hour", Scope: "standard", UsedPercent: floatPointer(30), ResetAtMS: &reset},
				ResetCredits: &CodexResetCreditsInfo{AvailableCount: 1, Credits: []CodexResetCredit{{ID: "credit-1", Status: "available", ExpiresAtMS: &expiry}}}}
			test.change(&input)
			result := AdviseReset(input)
			if result.Verdict != test.verdict || result.Evidence != test.evidence || !slices.Contains(result.Reasons, test.reason) {
				t.Fatalf("advice = %+v", result)
			}
			if result.RemainingResets != nil && *result.RemainingResets == 0 && test.verdict == ResetAdviceUnknown {
				t.Fatalf("unknown became zero: %+v", result)
			}
		})
	}
}

func TestAdviseResetClaudeCountsOnlyMatchingUnexpiredGrants(t *testing.T) {
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	expiry, later, expired, reset := now.Add(time.Hour).UnixMilli(), now.Add(48*time.Hour).UnixMilli(), now.UnixMilli(), now.Add(3*time.Hour).UnixMilli()
	input := ResetAdviceInput{Provider: "claude", ObservedAtMS: now.UnixMilli(), NowMS: now.UnixMilli(),
		Window: QuotaWindow{ID: "seven_day", Kind: "weekly", Scope: "standard", UsedPercent: floatPointer(10), ResetAtMS: &reset},
		ResetGrants: []ClaudeResetGrant{
			{ResetsTotal: 3, ResetsLeft: 2, EndsAtMS: &later, Clears: []string{"seven_day"}},
			{ResetsTotal: 1, ResetsLeft: 1, EndsAtMS: &expiry, Clears: []string{"seven_day", "five_hour"}},
			{ResetsTotal: 9, ResetsLeft: 9, EndsAtMS: &expired, Clears: []string{"seven_day"}},
			{ResetsTotal: 5, ResetsLeft: 5, EndsAtMS: &expiry, Clears: []string{"five_hour"}},
		}}
	result := AdviseReset(input)
	if result.Verdict != ResetAdviceUseBeforeExpiry || result.RemainingResets == nil || *result.RemainingResets != 3 || result.EarliestExpiryMS == nil || *result.EarliestExpiryMS != expiry {
		t.Fatalf("advice = %+v", result)
	}
	input.Window.ID = "seven_day_overage_included"
	result = AdviseReset(input)
	if result.Verdict != ResetAdviceUnknown || result.RemainingResets != nil || !slices.Contains(result.Reasons, "no_matching_reset_grant") {
		t.Fatalf("unmatched = %+v", result)
	}
	input.ResetGrants = nil
	result = AdviseReset(input)
	if result.Verdict != ResetAdviceUnknown || result.RemainingResets != nil || !slices.Contains(result.Reasons, "reset_reading_missing") {
		t.Fatalf("missing = %+v", result)
	}
}
