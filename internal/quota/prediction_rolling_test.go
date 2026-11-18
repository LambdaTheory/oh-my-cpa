package quota

import (
	"slices"
	"testing"
	"time"
)

func rollingPredictionWindow(observed time.Time, used float64) QuotaWindow {
	reset := observed.Add(5 * time.Hour).Add(253 * time.Millisecond).UnixMilli()
	period := 5.0
	return QuotaWindow{
		ID: "five_hour", Kind: "five_hour", Scope: "standard",
		UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact",
	}
}

func TestPredictWindowRollingZeroTrafficIsFact(t *testing.T) {
	observed := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	window := rollingPredictionWindow(observed, 0)
	result := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 0},
	})
	if result.LastsUntilReset == nil || !*result.LastsUntilReset || result.Evidence != PredictionEvidenceFact {
		t.Fatalf("zero-traffic rolling prediction = %+v", result)
	}
	if result.UnknownReason != "" || slices.Contains(result.Reasons, PredictionReasonRollingWindow) ||
		slices.Contains(result.Reasons, PredictionReasonLowUsage) {
		t.Fatalf("zero-traffic fact carried an unknown/rolling/low-usage reason: %+v", result)
	}
	if !result.Accounting.IsRollingWindow || result.Accounting.UsageRequests == nil || *result.Accounting.UsageRequests != 0 {
		t.Fatalf("zero-traffic accounting = %+v", result.Accounting)
	}
	if result.Accounting.WindowStartMS != nil || result.Accounting.RemainingPercent == nil || *result.Accounting.RemainingPercent != 100 {
		t.Fatalf("rolling accounting invented a cycle start: %+v", result.Accounting)
	}
}

func TestPredictWindowRollingZeroUsageWithTrafficDoesNotShortcut(t *testing.T) {
	observed := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	window := rollingPredictionWindow(observed, 0)
	result := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 3},
	})
	if result.LastsUntilReset != nil || result.Evidence != PredictionEvidenceUnknown {
		t.Fatalf("zero-used window with traffic took the shortcut: %+v", result)
	}
	if result.UnknownReason != PredictionUnknownRollingWindow || !slices.Contains(result.Reasons, PredictionReasonRollingWindow) {
		t.Fatalf("rolling reason = %q reasons %v", result.UnknownReason, result.Reasons)
	}
	if result.Accounting.UsageRequests == nil || *result.Accounting.UsageRequests != 3 || !result.Accounting.IsRollingWindow {
		t.Fatalf("rolling traffic accounting = %+v", result.Accounting)
	}
}

func TestPredictWindowRollingUsageDoesNotFitAFakeCycle(t *testing.T) {
	observed := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	window := rollingPredictionWindow(observed, 40)
	reset := *window.ResetAtMS
	result := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 5},
		Observations: []PredictionObservation{
			{ObservedAtMS: observed.Add(-2 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
			{ObservedAtMS: observed.Add(-time.Hour).UnixMilli(), UsedPercent: 30, ResetAtMS: &reset},
			{ObservedAtMS: observed.Add(-30 * time.Minute).UnixMilli(), UsedPercent: 35, ResetAtMS: &reset},
		},
	})
	if result.LastsUntilReset != nil || result.ExhaustionETAMS != nil || result.BurnRatePerHour != nil ||
		result.ProjectedPercentAtReset != nil || result.ProjectedTokensAtReset != nil {
		t.Fatalf("rolling window fitted a projection: %+v", result)
	}
	if result.UnknownReason != PredictionUnknownRollingWindow || !slices.Contains(result.Reasons, PredictionReasonRollingWindow) {
		t.Fatalf("rolling reason = %q reasons %v", result.UnknownReason, result.Reasons)
	}
	if result.Accounting.UniformProjectedPercentAtReset != nil || result.Accounting.WindowStartMS != nil || !result.Accounting.IsRollingWindow {
		t.Fatalf("rolling accounting = %+v", result.Accounting)
	}
}

func TestPredictWindowZeroTrafficFactRequiresAUsageRead(t *testing.T) {
	observed := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	reset := observed.Add(4 * time.Hour).UnixMilli()
	period, used := 5.0, 0.0
	window := QuotaWindow{ID: "five_hour", Kind: "five_hour", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}

	unread := PredictWindow(PredictionInput{Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli()})
	if unread.LastsUntilReset != nil || unread.Evidence == PredictionEvidenceFact {
		t.Fatalf("unread usage became a fact: %+v", unread)
	}
	if unread.Accounting.UsageRequests != nil {
		t.Fatalf("unread usage accounting = %+v", unread.Accounting)
	}

	traffic := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 2},
	})
	if traffic.LastsUntilReset != nil || traffic.Evidence == PredictionEvidenceFact {
		t.Fatalf("zero-used window with traffic became a fact: %+v", traffic)
	}
	if traffic.Accounting.UsageRequests == nil || *traffic.Accounting.UsageRequests != 2 {
		t.Fatalf("traffic accounting = %+v", traffic.Accounting)
	}

	empty := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 0},
	})
	if empty.LastsUntilReset == nil || !*empty.LastsUntilReset || empty.Evidence != PredictionEvidenceFact {
		t.Fatalf("fixed-window zero traffic prediction = %+v", empty)
	}
	if empty.Accounting.IsRollingWindow || empty.Accounting.WindowStartMS == nil {
		t.Fatalf("fixed-window accounting = %+v", empty.Accounting)
	}
}

func TestPredictionAccountingUsageRequestsKeepsZeroDistinctFromUnread(t *testing.T) {
	observed := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	reset := observed.Add(4 * time.Hour).UnixMilli()
	period, used := 5.0, 40.0
	window := QuotaWindow{ID: "five_hour", Kind: "five_hour", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}

	readZero := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 0},
	})
	if readZero.Accounting.UsageRequests == nil || *readZero.Accounting.UsageRequests != 0 {
		t.Fatalf("read-zero accounting = %+v", readZero.Accounting)
	}

	unread := PredictWindow(PredictionInput{Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli()})
	if unread.Accounting.UsageRequests != nil {
		t.Fatalf("unread accounting = %+v", unread.Accounting)
	}
}

func TestPredictWindowUnknownResetWithPeriodUsesTrailingTraffic(t *testing.T) {
	observed := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	period, used := 5.0, 0.0
	window := QuotaWindow{ID: "five_hour", Kind: "five_hour", Scope: "standard", UsedPercent: &used, PeriodHours: &period, ResetAccuracy: "exact"}

	empty := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 0},
	})
	if empty.LastsUntilReset == nil || !*empty.LastsUntilReset || empty.Evidence != PredictionEvidenceFact {
		t.Fatalf("unknown-reset zero-traffic prediction = %+v", empty)
	}
	if empty.Accounting.UsageRequests == nil || *empty.Accounting.UsageRequests != 0 ||
		empty.Accounting.WindowStartMS != nil || empty.Accounting.IsRollingWindow {
		t.Fatalf("unknown-reset zero-traffic accounting = %+v", empty.Accounting)
	}

	// An approximate reset is still an unknown boundary, and the same zero-use
	// fact applies once the period is known and the trailing traffic is empty.
	approximate := window
	approxReset := observed.Add(4 * time.Hour).UnixMilli()
	approximate.ResetAtMS = &approxReset
	approximate.ResetAccuracy = "approximate"
	approximatelyEmpty := PredictWindow(PredictionInput{
		Window: approximate, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 0},
	})
	if approximatelyEmpty.LastsUntilReset == nil || !*approximatelyEmpty.LastsUntilReset ||
		approximatelyEmpty.Evidence != PredictionEvidenceFact || approximatelyEmpty.Accounting.WindowStartMS != nil {
		t.Fatalf("approximate-reset zero-traffic prediction = %+v", approximatelyEmpty)
	}

	traffic := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 2},
	})
	if traffic.LastsUntilReset != nil || traffic.Evidence == PredictionEvidenceFact ||
		traffic.UnknownReason != PredictionUnknownBoundaryUnknown ||
		!slices.Contains(traffic.Reasons, PredictionReasonBoundaryUnknown) {
		t.Fatalf("unknown-reset traffic prediction = %+v", traffic)
	}

	noPeriod := window
	noPeriod.PeriodHours = nil
	unbounded := PredictWindow(PredictionInput{
		Window: noPeriod, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Usage: &WindowUsage{Requests: 0},
	})
	if unbounded.LastsUntilReset != nil || unbounded.Evidence == PredictionEvidenceFact {
		t.Fatalf("window without a period became a fact: %+v", unbounded)
	}
}
