package quota

import (
	"math"
	"slices"
	"testing"
	"time"
)

func TestPredictionRefitsAfterMidCycleReset(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 30)
	observed := start.Add(6 * time.Hour)
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(30 * time.Minute).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(90 * time.Minute).UnixMilli(), UsedPercent: 40, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(150 * time.Minute).UnixMilli(), UsedPercent: 80, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 5, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 25, ResetAtMS: &reset},
		},
	})
	if got.LastsUntilReset == nil || !*got.LastsUntilReset || got.Evidence != PredictionEvidenceProjection {
		t.Fatalf("reset refit prediction = %+v", got)
	}
	if got.BurnRatePerHour == nil || math.Abs(*got.BurnRatePerHour-5) > 0.0001 {
		t.Fatalf("reset pre-history leaked into burn rate: %+v", got.BurnRatePerHour)
	}
	if got.SampleCount != 3 || got.CoverageSeconds == nil || *got.CoverageSeconds != int64(2*time.Hour/time.Second) {
		t.Fatalf("reset sample = %+v", got)
	}
	if !slices.Contains(got.Reasons, PredictionReasonResetMidCycle) {
		t.Fatalf("reset diagnostic lost: %v", got.Reasons)
	}
}

func TestPredictionResetAtTailFallsBackToEvidenceReason(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 6)
	observed := start.Add(5*time.Hour + 5*time.Minute)
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 30, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), UsedPercent: 40, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 5, ResetAtMS: &reset},
		},
	})
	if got.LastsUntilReset != nil || got.UnknownReason != PredictionUnknownHistoryInsufficient {
		t.Fatalf("tail reset prediction = %+v", got)
	}
	if !slices.Contains(got.Reasons, PredictionReasonFewerThanThreeBuckets) {
		t.Fatalf("tail reset evidence reason = %v", got.Reasons)
	}
	if got.SampleCount != 1 {
		t.Fatalf("tail reset kept pre-reset samples: %+v", got)
	}
}

func TestPredictionRefitsAfterResetAtMovesEarlier(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 60)
	earlierReset := start.Add(9 * time.Hour).UnixMilli()
	observed := start.Add(6 * time.Hour)
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &earlierReset},
			{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &earlierReset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 30, ResetAtMS: &earlierReset},
			{ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), UsedPercent: 40, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(4*time.Hour + 30*time.Minute).UnixMilli(), UsedPercent: 45, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 50, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5*time.Hour + 30*time.Minute).UnixMilli(), UsedPercent: 55, ResetAtMS: &reset},
		},
	})
	if got.LastsUntilReset == nil || !*got.LastsUntilReset {
		t.Fatalf("moved-reset prediction = %+v", got)
	}
	if got.BurnRatePerHour == nil || math.Abs(*got.BurnRatePerHour-10) > 0.0001 {
		t.Fatalf("moved-reset burn rate = %+v", got.BurnRatePerHour)
	}
	if got.SampleCount != 4 || !slices.Contains(got.Reasons, PredictionReasonResetMidCycle) {
		t.Fatalf("moved-reset sample = %+v", got)
	}
}

func TestPredictionUsesSnapshotResetInstantWhenObservationsHaveNoTransition(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 20)
	resetAt := start.Add(3 * time.Hour).UnixMilli()
	window.MidCycleResetAtMS = &resetAt
	observed := start.Add(6 * time.Hour)
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 5, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 15, ResetAtMS: &reset},
		},
	})
	if got.LastsUntilReset == nil || !*got.LastsUntilReset || got.BurnRatePerHour == nil || math.Abs(*got.BurnRatePerHour-5) > 0.0001 {
		t.Fatalf("snapshot reset instant prediction = %+v", got)
	}
	if got.SampleCount != 3 || !slices.Contains(got.Reasons, PredictionReasonResetMidCycle) {
		t.Fatalf("snapshot reset instant sample = %+v", got)
	}
}

func TestPredictionBucketResetOutranksSnapshotResetInstant(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	window, reset := predictionWindow(start, 30)
	snapshotResetAt := start.Add(4 * time.Hour).UnixMilli()
	window.MidCycleResetAtMS = &snapshotResetAt
	observed := start.Add(5 * time.Hour)
	got := PredictWindow(PredictionInput{
		Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli(),
		Observations: []PredictionObservation{
			{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 50, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(2 * time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset},
			{ObservedAtMS: start.Add(4 * time.Hour).UnixMilli(), UsedPercent: 25, ResetAtMS: &reset},
		},
	})
	if got.LastsUntilReset == nil || !*got.LastsUntilReset || got.BurnRatePerHour == nil || math.Abs(*got.BurnRatePerHour-5) > 0.0001 {
		t.Fatalf("bucket reset did not outrank snapshot: %+v", got)
	}
}

func TestPredictionLegacyResetFlagWithoutInstantStaysUnknown(t *testing.T) {
	start := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	window, _ := predictionWindow(start, 30)
	window.HasMidCycleReset = true
	observed := start.Add(6 * time.Hour)
	got := PredictWindow(PredictionInput{Window: window, ObservedAtMS: observed.UnixMilli(), NowMS: observed.UnixMilli()})
	if got.LastsUntilReset != nil || got.UnknownReason != PredictionUnknownHistoryInsufficient {
		t.Fatalf("legacy reset prediction = %+v", got)
	}
	if !slices.Contains(got.Reasons, PredictionReasonResetMidCycle) {
		t.Fatalf("legacy reset diagnostic = %v", got.Reasons)
	}
}
