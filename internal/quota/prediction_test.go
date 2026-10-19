package quota

import (
	"math"
	"slices"
	"testing"
	"time"
)

func predictionWindow(start time.Time, used float64) (QuotaWindow, int64) {
	reset := start.Add(10 * time.Hour).UnixMilli()
	period := 10.0
	return QuotaWindow{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}, reset
}

func TestPredictWindowRejectsUnsafePercentages(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for _, used := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		window, _ := predictionWindow(start, used)
		got := PredictWindow(PredictionInput{Window: window, ObservedAtMS: start.Add(time.Hour).UnixMilli(), NowMS: start.Add(time.Hour).UnixMilli()})
		if got.UsedPercent != nil || !slices.Contains(got.Reasons, PredictionReasonInvalidUsedPercent) {
			t.Fatalf("used %v prediction = %+v", used, got)
		}
	}
	for _, historical := range []float64{-1, 101, math.NaN(), math.Inf(-1)} {
		window, reset := predictionWindow(start, 20)
		got := PredictWindow(PredictionInput{
			Window: window, ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), NowMS: start.Add(4 * time.Hour).UnixMilli(),
			Observations: []PredictionObservation{{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: historical, ResetAtMS: &reset}},
		})
		if got.Verdict != PredictionVerdictUnknown || !slices.Contains(got.Reasons, PredictionReasonInvalidObservation) {
			t.Fatalf("historical %v = %+v", historical, got)
		}
	}
}

func TestPredictWindowCollapsesDuplicateSourcesDeterministically(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 60)
	observed := start.Add(6 * time.Hour).UnixMilli()
	duplicates := []PredictionObservation{
		{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset, Source: ObservationSourceActive},
		{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 60, ResetAtMS: &reset, Source: ObservationSourcePassive},
		{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 50, ResetAtMS: &reset, Source: ObservationSourceActive},
	}
	first := PredictWindow(PredictionInput{Window: window, ObservedAtMS: observed, NowMS: observed, Observations: duplicates})
	slices.Reverse(duplicates)
	second := PredictWindow(PredictionInput{Window: window, ObservedAtMS: observed, NowMS: observed, Observations: duplicates})
	if first.Verdict == PredictionVerdictUnknown || second.Verdict != first.Verdict || slices.Contains(first.Reasons, PredictionReasonResetMidCycle) || slices.Contains(second.Reasons, PredictionReasonResetMidCycle) {
		t.Fatalf("duplicate order changed prediction: first=%+v second=%+v", first, second)
	}
}

func TestPredictionResetDetectionMatchesCycleSemantics(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 30)
	observed := start.Add(6 * time.Hour).UnixMilli()
	earlierReset := start.Add(2 * time.Hour).UnixMilli()
	valid := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed, NowMS: observed,
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &earlierReset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
		},
	})
	if slices.Contains(valid.Reasons, PredictionReasonResetMidCycle) {
		t.Fatalf("completed rolling boundary tainted cycle: %+v", valid)
	}
	futureReset := start.Add(9 * time.Hour).UnixMilli()
	movedEarly := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed, NowMS: observed,
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &futureReset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
		},
	})
	if !slices.Contains(movedEarly.Reasons, PredictionReasonResetMidCycle) {
		t.Fatalf("moved-early reset not detected: %+v", movedEarly)
	}
}

func TestPredictionFreshnessFollowsWindowCadence(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name          string
		period        time.Duration
		observedAfter time.Duration
		age           time.Duration
	}{
		{"five hour", 5 * time.Hour, 2*time.Hour + 45*time.Minute, 2 * time.Hour},
		{"weekly", 7 * 24 * time.Hour, 4 * 24 * time.Hour, 6 * time.Hour},
	} {
		periodHours := test.period.Hours()
		reset := start.Add(test.period).UnixMilli()
		used := 20.0
		observed := start.Add(test.observedAfter)
		window := QuotaWindow{ID: test.name, Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &periodHours, ResetAccuracy: "exact"}
		got := PredictWindow(PredictionInput{
			Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.Add(test.age).UnixMilli(),
			Observations: []PredictionObservation{{ObservedAtMS: observed.Add(-time.Hour).UnixMilli(), UsedPercent: used, ResetAtMS: &reset}},
		})
		if got.Verdict != PredictionVerdictUnderUsed || slices.Contains(got.Reasons, PredictionReasonReadingStale) {
			t.Fatalf("%s cadence prediction = %+v", test.name, got)
		}
	}
}

func TestPredictionArithmeticFailsClosed(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 50)
	observed := start.Add(6 * time.Hour).UnixMilli()
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed, NowMS: observed,
		Observations: []PredictionObservation{{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset}},
		Usage:        &WindowUsage{Requests: 1, PricedRequests: 1, Tokens: math.MaxInt64, CostNanos: math.MaxInt64},
	})
	if got.ProjectedTokensAtReset != nil || got.ProjectedCostNanosAtReset != nil || !slices.Contains(got.Reasons, PredictionReasonArithmeticUnsafe) {
		t.Fatalf("overflow projection = %+v", got)
	}
	if _, ok := roundedInt64(math.Inf(1)); ok {
		t.Fatal("infinite float converted to int64")
	}
	if _, ok := roundedInt64(float64(math.MaxInt64) * 2); ok {
		t.Fatal("overflowing float converted to int64")
	}
	if _, ok := addInt64(math.MaxInt64, 1); ok {
		t.Fatal("overflowing int64 addition succeeded")
	}
	duplicate := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed, NowMS: observed,
		Observations: []PredictionObservation{{ObservedAtMS: observed, UsedPercent: 40, ResetAtMS: &reset}},
	})
	if !slices.Contains(duplicate.Reasons, PredictionReasonInsufficientObservations) {
		t.Fatalf("zero-denominator history = %+v", duplicate)
	}
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

func TestPredictWindowPreservesSpecificUsageReason(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 60)
	observed := start.Add(6 * time.Hour).UnixMilli()
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed, NowMS: observed,
		Observations: []PredictionObservation{{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset}},
		UsageReason:  CapacityReasonScopeUnknown,
	})
	if !slices.Contains(got.Reasons, CapacityReasonScopeUnknown) || slices.Contains(got.Reasons, PredictionReasonUsageUnavailable) {
		t.Fatalf("usage reasons = %v", got.Reasons)
	}
}
