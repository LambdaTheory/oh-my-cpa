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
		{ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), UsedPercent: 55, ResetAtMS: &reset, Source: ObservationSourceActive},
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
		offsets       []time.Duration
	}{
		{"five hour", 5 * time.Hour, 2*time.Hour + 45*time.Minute, 2 * time.Hour, []time.Duration{-2 * time.Hour, -time.Hour}},
		{"weekly", 7 * 24 * time.Hour, 4 * 24 * time.Hour, 6 * time.Hour, []time.Duration{-16 * time.Hour, -12 * time.Hour, -8 * time.Hour, -4 * time.Hour, -time.Hour}},
	} {
		periodHours := test.period.Hours()
		reset := start.Add(test.period).UnixMilli()
		used := 20.0
		observed := start.Add(test.observedAfter)
		window := QuotaWindow{ID: test.name, Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &periodHours, ResetAccuracy: "exact"}
		observations := make([]PredictionObservation, 0, len(test.offsets))
		for _, offset := range test.offsets {
			observations = append(observations, PredictionObservation{ObservedAtMS: observed.Add(offset).UnixMilli(), UsedPercent: used, ResetAtMS: &reset})
		}
		got := PredictWindow(PredictionInput{
			Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.Add(test.age).UnixMilli(),
			Observations: observations,
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
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 30, ResetAtMS: &reset},
		},
		Usage: &WindowUsage{Requests: 1, PricedRequests: 1, Tokens: math.MaxInt64, CostNanos: math.MaxInt64},
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
	if !slices.Contains(duplicate.Reasons, PredictionReasonFewerThanThreeBuckets) {
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
	if !slices.Contains(got.Reasons, PredictionReasonFewerThanThreeBuckets) {
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
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 15, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 30, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 45, ResetAtMS: &reset},
		},
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
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
		},
		Usage: &WindowUsage{Requests: 10, PricedRequests: 5, Tokens: 100},
	})
	if got.Verdict != PredictionVerdictUnderUsed || got.Evidence != PredictionEvidenceProjection || got.ProjectedPercentAtReset == nil || !slices.Contains(got.Reasons, PredictionReasonNoPositiveTrend) || !slices.Contains(got.Reasons, PredictionReasonInsufficientPricedUsage) {
		t.Fatalf("under-used = %+v", got)
	}

	low, _ := predictionWindow(start, 4)
	got = PredictWindow(PredictionInput{
		Window: low, ObservedAtMS: observed, NowMS: observed,
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 2, ResetAtMS: low.ResetAtMS},
			{ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), UsedPercent: 2, ResetAtMS: low.ResetAtMS},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 2, ResetAtMS: low.ResetAtMS},
		},
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
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 35, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 50, ResetAtMS: &reset},
		},
		UsageReason: CapacityReasonScopeUnknown,
	})
	if !slices.Contains(got.Reasons, CapacityReasonScopeUnknown) || slices.Contains(got.Reasons, PredictionReasonUsageUnavailable) {
		t.Fatalf("usage reasons = %v", got.Reasons)
	}
}

func TestPredictWindowMinimumEvidenceReasons(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 20)
	observed := start.Add(6 * time.Hour)

	fewer := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset}},
	})
	if !slices.Contains(fewer.Reasons, PredictionReasonFewerThanThreeBuckets) {
		t.Fatalf("fewer-than-three prediction = %+v", fewer)
	}

	short := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: observed.Add(-20 * time.Minute).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: observed.Add(-10 * time.Minute).UnixMilli(), UsedPercent: 15, ResetAtMS: &reset},
		},
	})
	if !slices.Contains(short.Reasons, PredictionReasonInsufficientCoverage) {
		t.Fatalf("short-coverage prediction = %+v", short)
	}

	gapped := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(time.Hour + 5*time.Minute).UnixMilli(), UsedPercent: 11, ResetAtMS: &reset},
		},
	})
	if !slices.Contains(gapped.Reasons, PredictionReasonObservationGap) {
		t.Fatalf("gapped prediction = %+v", gapped)
	}
}

func TestPredictWindowFactTakesPriorityOverHistory(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	observed := start.Add(6 * time.Hour)
	for _, test := range []struct {
		name   string
		mutate func(*QuotaWindow)
	}{
		{"incomplete history", func(window *QuotaWindow) { window.HasIncompleteHistory = true }},
		{"mid-cycle reset", func(window *QuotaWindow) { window.HasMidCycleReset = true }},
	} {
		window, _ := predictionWindow(start, 100)
		test.mutate(&window)
		got := PredictWindow(PredictionInput{Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli()})
		if got.Verdict != PredictionVerdictWillExhaust || got.Evidence != PredictionEvidenceFact || got.ExhaustionETAMS == nil || *got.ExhaustionETAMS != observed.UnixMilli() {
			t.Fatalf("%s prediction = %+v", test.name, got)
		}
	}

	full, _ := predictionWindow(start, 100)
	for _, test := range []struct {
		name   string
		input  PredictionInput
		reason string
	}{
		{"stale", PredictionInput{Window: full, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(), Status: "stale"}, PredictionReasonReadingStale},
		{"error", PredictionInput{Window: full, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(), Status: "error"}, PredictionReasonReadingError},
	} {
		got := PredictWindow(test.input)
		if got.Verdict != PredictionVerdictUnknown || got.Evidence == PredictionEvidenceFact || !slices.Contains(got.Reasons, test.reason) {
			t.Fatalf("%s prediction = %+v", test.name, got)
		}
	}

	unknownBoundary, _ := predictionWindow(start, 100)
	unknownBoundary.ResetAtMS = nil
	got := PredictWindow(PredictionInput{Window: unknownBoundary, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli()})
	if got.Evidence == PredictionEvidenceFact || !slices.Contains(got.Reasons, PredictionReasonBoundaryUnknown) {
		t.Fatalf("unknown boundary prediction = %+v", got)
	}

	expired, _ := predictionWindow(start, 100)
	expiredReset := observed.Add(-time.Hour).UnixMilli()
	expired.ResetAtMS = &expiredReset
	got = PredictWindow(PredictionInput{Window: expired, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli()})
	if got.Evidence == PredictionEvidenceFact || !slices.Contains(got.Reasons, PredictionReasonCycleExpired) {
		t.Fatalf("expired boundary prediction = %+v", got)
	}

	staleAge := PredictWindow(PredictionInput{Window: full, ObservedAtMS: observed.UnixMilli(), NowMS: observed.Add(25 * time.Hour).UnixMilli()})
	if staleAge.Evidence == PredictionEvidenceFact || !slices.Contains(staleAge.Reasons, PredictionReasonReadingStale) {
		t.Fatalf("stale-age 100%% prediction = %+v", staleAge)
	}
}

func TestPredictWindowMinimumEvidenceBoundaries(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 20)
	observed := start.Add(6 * time.Hour)

	// Coverage exactly at the 30-minute floor and both gaps inside the limit.
	exactCoverage := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: observed.Add(-30 * time.Minute).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: observed.Add(-15 * time.Minute).UnixMilli(), UsedPercent: 15, ResetAtMS: &reset},
		},
	})
	if slices.Contains(exactCoverage.Reasons, PredictionReasonInsufficientCoverage) ||
		slices.Contains(exactCoverage.Reasons, PredictionReasonObservationGap) ||
		exactCoverage.Verdict == PredictionVerdictUnknown {
		t.Fatalf("exact coverage floor was rejected: %+v", exactCoverage)
	}

	// A one-hour span with a 30-minute maximum gap is exactly the ceiling.
	gapCeiling := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: observed.Add(-time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: observed.Add(-30 * time.Minute).UnixMilli(), UsedPercent: 15, ResetAtMS: &reset},
		},
	})
	if slices.Contains(gapCeiling.Reasons, PredictionReasonObservationGap) || gapCeiling.Verdict == PredictionVerdictUnknown {
		t.Fatalf("exact gap ceiling was rejected: %+v", gapCeiling)
	}
}

func TestPredictWindowReportsEvidenceQuality(t *testing.T) {
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 40)
	observed := start.Add(6 * time.Hour)
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 30, ResetAtMS: &reset},
		},
	})
	if got.SampleCount != 4 || got.CoverageSeconds == nil || *got.CoverageSeconds != int64(5*time.Hour/time.Second) ||
		got.MaxGapSeconds == nil || *got.MaxGapSeconds != int64(2*time.Hour/time.Second) {
		t.Fatalf("quality evidence = %+v", got)
	}

	twoPoints := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{{ObservedAtMS: observed.Add(-10 * time.Minute).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset}},
	})
	if twoPoints.SampleCount != 2 || twoPoints.CoverageSeconds == nil || twoPoints.MaxGapSeconds == nil {
		t.Fatalf("two-point quality evidence = %+v", twoPoints)
	}

	onePoint := PredictWindow(PredictionInput{Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli()})
	if onePoint.SampleCount != 1 || onePoint.CoverageSeconds != nil || onePoint.MaxGapSeconds != nil {
		t.Fatalf("one-point quality evidence = %+v", onePoint)
	}
}
