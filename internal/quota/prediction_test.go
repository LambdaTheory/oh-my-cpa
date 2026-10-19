package quota

import (
	"slices"
	"testing"
	"time"
)

func predictionWindow(start time.Time, used float64) (QuotaWindow, int64) {
	reset := start.Add(10 * time.Hour).UnixMilli()
	period := 10.0
	return QuotaWindow{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}, reset
}

func TestPredictWindowUnknownEvidence(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, _ := predictionWindow(start, 20)
	window.ResetAtMS = nil
	got := PredictWindow(PredictionInput{Window: window, ObservedAtMS: start.Add(time.Hour).UnixMilli(), NowMS: start.Add(time.Hour).UnixMilli()})
	if got.Verdict != PredictionVerdictUnknown || !slices.Contains(got.Reasons, PredictionReasonBoundaryUnknown) {
		t.Fatalf("boundary prediction = %+v", got)
	}

	window, _ = predictionWindow(start, 20)
	got = PredictWindow(PredictionInput{Window: window, ObservedAtMS: start.Add(time.Hour).UnixMilli(), NowMS: start.Add(time.Hour).UnixMilli()})
	if !slices.Contains(got.Reasons, PredictionReasonInsufficientObservations) {
		t.Fatalf("observation prediction = %+v", got)
	}

	window.HasMidCycleReset = true
	got = PredictWindow(PredictionInput{Window: window, ObservedAtMS: start.Add(time.Hour).UnixMilli(), NowMS: start.Add(time.Hour).UnixMilli()})
	if !slices.Contains(got.Reasons, PredictionReasonResetMidCycle) {
		t.Fatalf("reset prediction = %+v", got)
	}
}

func TestPredictWindowProjectionAndPricedUsage(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 60)
	observed := start.Add(4 * time.Hour).UnixMilli()
	usage := &WindowUsage{Requests: 100, PricedRequests: 96, Tokens: 6000, CostNanos: 600_000}
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed, NowMS: observed, Usage: usage,
		Observations: []PredictionObservation{{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 15, ResetAtMS: &reset}},
	})
	if got.Verdict != PredictionVerdictWillExhaust || got.Evidence != PredictionEvidenceProjection || got.BurnRatePerHour == nil || got.ExhaustionETAMS == nil || *got.ExhaustionETAMS >= reset {
		t.Fatalf("projection = %+v", got)
	}
	if got.ProjectedTokensAtReset == nil || got.ProjectedCostNanosAtReset == nil {
		t.Fatalf("priced usage missing = %+v", got)
	}
}

func TestPredictWindowUnderUsedAndUsageReasons(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 20)
	observed := start.Add(6 * time.Hour).UnixMilli()
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed, NowMS: observed,
		Observations: []PredictionObservation{{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset}},
		Usage:        &WindowUsage{Requests: 10, PricedRequests: 5, Tokens: 100},
	})
	if got.Verdict != PredictionVerdictUnderUsed || got.Evidence != PredictionEvidenceProjection || got.ProjectedPercentAtReset == nil || !slices.Contains(got.Reasons, PredictionReasonNoPositiveTrend) || !slices.Contains(got.Reasons, PredictionReasonInsufficientPricedUsage) {
		t.Fatalf("under-used = %+v", got)
	}

	low, _ := predictionWindow(start, 4)
	got = PredictWindow(PredictionInput{
		Window: low, ObservedAtMS: observed, NowMS: observed,
		Observations: []PredictionObservation{{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 2, ResetAtMS: low.ResetAtMS}},
	})
	if !slices.Contains(got.Reasons, PredictionReasonLowUsage) || got.ProjectedTokensAtReset != nil {
		t.Fatalf("low usage = %+v", got)
	}
}
