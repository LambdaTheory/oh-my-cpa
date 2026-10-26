package quota

import (
	"math"
	"sort"
	"time"
)

const (
	predictionMinimumFreshWindow = 2 * time.Hour
	predictionMaximumFreshWindow = 24 * time.Hour

	// PredictionBucketMS is the observation sample width used before the
	// history limit is applied. Seven days therefore produce roughly 2,016
	// buckets rather than one row per request.
	PredictionBucketMS int64 = 5 * 60 * 1000

	// PredictionMinimumEvidenceMS is the floor for both the covered span and
	// the largest permitted silence between samples.
	PredictionMinimumEvidenceMS int64 = 30 * 60 * 1000
	// predictionMinimumCoverageDivisor turns a cycle length into the minimum
	// covered span (one twentieth, or five percent). A weekly window therefore
	// needs more than a single half-hour burst.
	predictionMinimumCoverageDivisor int64 = 20
)

const (
	PredictionVerdictWillExhaust = "will_exhaust"
	PredictionVerdictUnderUsed   = "under_used"
	PredictionVerdictOnTrack     = "on_track"
	PredictionVerdictUnknown     = "unknown"

	PredictionEvidenceFact       = "fact"
	PredictionEvidenceProjection = "projection"
	PredictionEvidenceUnknown    = "unknown"

	PredictionReasonBoundaryUnknown   = "boundary_unknown"
	PredictionReasonCycleExpired      = "cycle_expired"
	PredictionReasonReadingStale      = "stale_reading"
	PredictionReasonReadingError      = "reading_error"
	PredictionReasonIncompleteHistory = "incomplete_history"
	PredictionReasonResetMidCycle     = "mid_cycle_reset"
	// PredictionReasonInsufficientObservations is retained for consumers that
	// stored the old reason code; the current path emits the bucket-specific
	// codes below instead.
	PredictionReasonInsufficientObservations = "fewer_than_two_observations"
	PredictionReasonFewerThanThreeBuckets    = "fewer_than_three_buckets"
	PredictionReasonInsufficientCoverage     = "insufficient_coverage"
	PredictionReasonObservationGap           = "observation_gap"
	PredictionReasonCycleTooEarly            = "cycle_too_early"
	PredictionReasonNoPositiveTrend          = "no_positive_trend"
	PredictionReasonLowUsage                 = "used_percent_below_five"
	PredictionReasonNoTraffic                = "no_traffic"
	PredictionReasonNoPrice                  = "no_price"
	PredictionReasonInsufficientPricedUsage  = "insufficient_priced_usage"
	PredictionReasonUsageUnavailable         = "usage_unavailable"
	PredictionReasonInvalidUsedPercent       = "invalid_used_percent"
	PredictionReasonInvalidObservation       = "invalid_observation"
	PredictionReasonArithmeticUnsafe         = "arithmetic_unsafe"
)

type PredictionObservation struct {
	ObservedAtMS int64
	UsedPercent  float64
	ResetAtMS    *int64
	Source       string
}

type PredictionInput struct {
	Window       QuotaWindow
	ObservedAtMS int64
	Status       string
	NowMS        int64
	Observations []PredictionObservation
	Usage        *WindowUsage
	UsageReason  string
}

type WindowPrediction struct {
	Accounting                PredictionAccounting
	UsedPercent               *float64
	ResetAtMS                 *int64
	ObservedAtMS              int64
	DataAgeSeconds            *int64
	BurnRatePerHour           *float64
	ProjectedPercentAtReset   *float64
	ProjectedTokensAtReset    *int64
	ProjectedCostNanosAtReset *int64
	ExhaustionETAMS           *int64
	Verdict                   string
	Evidence                  string
	Reasons                   []string
	// Quality evidence describes the sample the projection rests on. It is
	// deliberately additive: omissions stay null rather than becoming zero.
	SampleCount     int
	CoverageSeconds *int64
	MaxGapSeconds   *int64
}

// PredictionAccounting keeps cycle arithmetic separate from the fitted forecast.
// It is console context, not an addition to the quota_prediction wire contract.
type PredictionAccounting struct {
	WindowStartMS                  *int64   `json:"window_start_ms"`
	RemainingPercent               *float64 `json:"remaining_percent"`
	UniformProjectedPercentAtReset *float64 `json:"uniform_projected_percent_at_reset"`
}

func PredictWindow(input PredictionInput) (result WindowPrediction) {
	defer func() { result.Accounting = calculatePredictionAccounting(input, result) }()
	result = WindowPrediction{
		UsedPercent:  nil,
		ResetAtMS:    input.Window.ResetAtMS,
		ObservedAtMS: input.ObservedAtMS,
		Verdict:      PredictionVerdictUnknown,
		Evidence:     PredictionEvidenceUnknown,
		Reasons:      []string{},
	}
	if input.ObservedAtMS > 0 && input.NowMS >= input.ObservedAtMS {
		age := (input.NowMS - input.ObservedAtMS) / 1000
		result.DataAgeSeconds = &age
	}
	if input.Status == "error" || input.Status == "stale" {
		result.Reasons = append(result.Reasons, PredictionReasonReadingError)
		if input.Status == "stale" {
			result.Reasons[0] = PredictionReasonReadingStale
		}
		return result
	}
	if input.Window.UsedPercent == nil {
		result.Reasons = append(result.Reasons, CapacityReasonNoReading)
		return result
	}
	if !validPredictionPercent(*input.Window.UsedPercent) {
		result.Reasons = append(result.Reasons, PredictionReasonInvalidUsedPercent)
		return result
	}
	usedPercent := *input.Window.UsedPercent
	result.UsedPercent = &usedPercent
	defer appendPredictionUsage(&result, input)
	if input.ObservedAtMS <= 0 || input.NowMS < input.ObservedAtMS || input.NowMS-input.ObservedAtMS > predictionFreshWindow(input.Window).Milliseconds() {
		result.Reasons = append(result.Reasons, PredictionReasonReadingStale)
		return result
	}
	fromMS, toMS, reason := WindowCycleRange(input.Window, input.ObservedAtMS, input.NowMS)
	if reason != "" {
		if reason == CapacityReasonExpired {
			reason = PredictionReasonCycleExpired
		} else {
			reason = PredictionReasonBoundaryUnknown
		}
		result.Reasons = append(result.Reasons, reason)
		return result
	}
	if input.ObservedAtMS < fromMS || input.Window.ResetAtMS == nil || input.ObservedAtMS > *input.Window.ResetAtMS {
		result.Reasons = append(result.Reasons, PredictionReasonBoundaryUnknown)
		return result
	}
	// The current reading itself is the proof of exhaustion. This fact outranks
	// incomplete or reset history, which describe older samples rather than the
	// fresh reading that has already reached the ceiling.
	if *input.Window.UsedPercent >= 100 {
		result.Verdict = PredictionVerdictWillExhaust
		result.Evidence = PredictionEvidenceFact
		result.ExhaustionETAMS = &result.ObservedAtMS
		return result
	}
	if input.Window.HasIncompleteHistory {
		result.Reasons = append(result.Reasons, PredictionReasonIncompleteHistory)
		return result
	}
	observations, invalidObservations := collapsePredictionObservations(input, fromMS, toMS)
	if invalidObservations {
		result.Reasons = append(result.Reasons, PredictionReasonInvalidObservation)
		return result
	}
	points := predictionPoints(observations, input.Window.ResetAtMS)
	coverageMS, maxGapMS, evidenceSafe := applyPredictionEvidence(&result, points)
	if !evidenceSafe {
		result.Reasons = append(result.Reasons, PredictionReasonArithmeticUnsafe)
		return result
	}
	if input.Window.HasMidCycleReset || predictionHasReset(observations) {
		result.Reasons = append(result.Reasons, PredictionReasonResetMidCycle)
		return result
	}
	if reason, failed := predictionMinimumEvidence(input.Window, len(points), coverageMS, maxGapMS); failed {
		result.Reasons = append(result.Reasons, reason)
		return result
	}
	rate, ok := predictionSlope(points)
	if !ok {
		result.Reasons = append(result.Reasons, PredictionReasonArithmeticUnsafe)
		return result
	}
	cycleDurationMS := *input.Window.ResetAtMS - fromMS
	elapsedFraction := float64(input.ObservedAtMS-fromMS) / float64(cycleDurationMS)
	if !isFinite(elapsedFraction) {
		result.Reasons = append(result.Reasons, PredictionReasonArithmeticUnsafe)
		return result
	}
	if rate <= 0 {
		result.Reasons = append(result.Reasons, PredictionReasonNoPositiveTrend)
		if elapsedFraction < 0.25 {
			result.Verdict = PredictionVerdictUnknown
			result.Evidence = PredictionEvidenceUnknown
			result.Reasons = append(result.Reasons, PredictionReasonCycleTooEarly)
			return result
		}
		result.Evidence = PredictionEvidenceProjection
		result.BurnRatePerHour = floatPointer(0)
		projected, ok := roundPrediction(*input.Window.UsedPercent, 2)
		if !ok {
			result.Reasons = append(result.Reasons, PredictionReasonArithmeticUnsafe)
			return result
		}
		result.ProjectedPercentAtReset = floatPointer(projected)
		if input.Window.UsedPercent != nil && elapsedFraction > 0.5 && *input.Window.UsedPercent < 50 {
			result.Verdict = PredictionVerdictUnderUsed
		} else {
			result.Verdict = PredictionVerdictOnTrack
		}
		return result
	}

	result.Evidence = PredictionEvidenceProjection
	roundedRate, ok := roundPrediction(rate, 4)
	if !ok {
		result.Reasons = append(result.Reasons, PredictionReasonArithmeticUnsafe)
		return result
	}
	result.BurnRatePerHour = floatPointer(roundedRate)
	hoursToReset := float64(*input.Window.ResetAtMS-input.ObservedAtMS) / float64(time.Hour/time.Millisecond)
	projected := *input.Window.UsedPercent + rate*hoursToReset
	projected, ok = roundPrediction(projected, 2)
	if !ok {
		result.BurnRatePerHour = nil
		result.Reasons = append(result.Reasons, PredictionReasonArithmeticUnsafe)
		return result
	}
	result.ProjectedPercentAtReset = floatPointer(projected)
	hoursToExhaustion := (100 - *input.Window.UsedPercent) / rate
	if isFinite(hoursToExhaustion) && hoursToExhaustion >= 0 {
		if durationMS, safe := roundedInt64(hoursToExhaustion * float64(time.Hour/time.Millisecond)); safe {
			if eta, safe := addInt64(input.ObservedAtMS, durationMS); safe {
				result.ExhaustionETAMS = &eta
			} else {
				appendPredictionReason(&result, PredictionReasonArithmeticUnsafe)
			}
		} else {
			appendPredictionReason(&result, PredictionReasonArithmeticUnsafe)
		}
	} else {
		appendPredictionReason(&result, PredictionReasonArithmeticUnsafe)
	}
	switch {
	case result.ExhaustionETAMS != nil && projected >= 100 && *result.ExhaustionETAMS < *input.Window.ResetAtMS:
		result.Verdict = PredictionVerdictWillExhaust
	case projected < 50 && elapsedFraction > 0.5:
		result.Verdict = PredictionVerdictUnderUsed
	default:
		result.Verdict = PredictionVerdictOnTrack
	}
	return result
}

func calculatePredictionAccounting(input PredictionInput, prediction WindowPrediction) PredictionAccounting {
	result := PredictionAccounting{}
	fromMS, _, reason := WindowCycleRange(input.Window, input.ObservedAtMS, input.NowMS)
	if reason == "" && input.ObservedAtMS >= fromMS {
		result.WindowStartMS = &fromMS
	}
	if prediction.UsedPercent == nil {
		return result
	}
	result.RemainingPercent = floatPointer(100 - *prediction.UsedPercent)
	if result.WindowStartMS == nil {
		result.RemainingPercent = nil
		return result
	}
	for _, reason := range prediction.Reasons {
		if reason == PredictionReasonResetMidCycle || reason == PredictionReasonReadingStale {
			if reason == PredictionReasonReadingStale {
				result.RemainingPercent = nil
			}
			return result
		}
	}
	if input.Window.HasIncompleteHistory || input.Window.HasMidCycleReset ||
		input.ObservedAtMS <= 0 || input.NowMS < input.ObservedAtMS ||
		input.NowMS-input.ObservedAtMS > predictionFreshWindow(input.Window).Milliseconds() {
		return result
	}
	periodMS := *input.Window.ResetAtMS - fromMS
	elapsedMS := input.ObservedAtMS - fromMS
	// Reuse the existing evidence floor so a just-reset denominator cannot turn
	// upstream rounding into a misleading full-cycle estimate.
	if elapsedMS < max(PredictionMinimumEvidenceMS, periodMS/predictionMinimumCoverageDivisor) {
		return result
	}
	projected, isSafe := roundPrediction(*prediction.UsedPercent/float64(elapsedMS)*float64(periodMS), 2)
	if isSafe {
		result.UniformProjectedPercentAtReset = &projected
	}
	return result
}

func predictionPoints(observations []PredictionObservation, resetAtMS *int64) []PredictionObservation {
	points := make([]PredictionObservation, 0, len(observations))
	for _, observation := range observations {
		if observation.ResetAtMS != nil && resetAtMS != nil && resetDifferenceExceeds(*observation.ResetAtMS, *resetAtMS, 1000) {
			continue
		}
		points = append(points, observation)
	}
	return points
}

func predictionHasReset(points []PredictionObservation) bool {
	var previous *PredictionObservation
	for index := range points {
		point := &points[index]
		if previous != nil {
			if previous.UsedPercent > point.UsedPercent+capacityResetTolerancePercent {
				return true
			}
			if previous.ResetAtMS != nil && point.ResetAtMS != nil && *previous.ResetAtMS > point.ObservedAtMS && resetDifferenceExceeds(*previous.ResetAtMS, *point.ResetAtMS, 1000) {
				return true
			}
		}
		previous = point
	}
	return false
}

// applyPredictionEvidence records the span and largest silence of the fitted
// sample. Both figures stay null unless the sample has at least two points,
// because a single reading cannot describe an interval.
func applyPredictionEvidence(result *WindowPrediction, points []PredictionObservation) (coverageMS, maxGapMS int64, ok bool) {
	result.SampleCount = len(points)
	if len(points) < 2 {
		return 0, 0, true
	}
	coverageMS, ok = subtractInt64(points[len(points)-1].ObservedAtMS, points[0].ObservedAtMS)
	if !ok {
		return 0, 0, false
	}
	coverageSeconds := coverageMS / 1000
	result.CoverageSeconds = &coverageSeconds
	for index := 1; index < len(points); index++ {
		gapMS, safe := subtractInt64(points[index].ObservedAtMS, points[index-1].ObservedAtMS)
		if !safe {
			return 0, 0, false
		}
		if gapMS > maxGapMS {
			maxGapMS = gapMS
		}
	}
	maxGapSeconds := maxGapMS / 1000
	result.MaxGapSeconds = &maxGapSeconds
	return coverageMS, maxGapMS, true
}

// predictionMinimumEvidence rejects samples that are too short, cover too
// little of a long cycle, or hide a long silence inside otherwise sufficient
// coverage. The half-coverage gap ceiling prevents one dense burst from
// standing in for a whole window.
func predictionMinimumEvidence(window QuotaWindow, pointCount int, coverageMS, maxGapMS int64) (string, bool) {
	if pointCount < 3 {
		return PredictionReasonFewerThanThreeBuckets, true
	}
	if window.PeriodHours == nil || !isFinite(*window.PeriodHours) || *window.PeriodHours <= 0 {
		return PredictionReasonArithmeticUnsafe, true
	}
	periodMSFloat := *window.PeriodHours * float64(time.Hour/time.Millisecond)
	if !isFinite(periodMSFloat) || periodMSFloat <= 0 || periodMSFloat >= float64(math.MaxInt64) {
		return PredictionReasonArithmeticUnsafe, true
	}
	minimumCoverageMS := PredictionMinimumEvidenceMS
	// Five percent is computed as an integer division so a large period cannot
	// overflow or lose precision on the way to the comparison.
	if periodCoverageMS := int64(periodMSFloat) / predictionMinimumCoverageDivisor; periodCoverageMS > minimumCoverageMS {
		minimumCoverageMS = periodCoverageMS
	}
	if coverageMS < minimumCoverageMS {
		return PredictionReasonInsufficientCoverage, true
	}
	maxGapLimitMS := PredictionMinimumEvidenceMS
	if halfCoverageMS := coverageMS / 2; halfCoverageMS > maxGapLimitMS {
		maxGapLimitMS = halfCoverageMS
	}
	if maxGapMS > maxGapLimitMS {
		return PredictionReasonObservationGap, true
	}
	return "", false
}

func predictionSlope(points []PredictionObservation) (float64, bool) {
	origin := float64(points[0].ObservedAtMS)
	var sumX, sumY, sumXY, sumXX float64
	for _, point := range points {
		x := (float64(point.ObservedAtMS) - origin) / float64(time.Hour/time.Millisecond)
		y := point.UsedPercent
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	count := float64(len(points))
	denominator := count*sumXX - sumX*sumX
	if denominator <= 0 || !isFinite(denominator) {
		return 0, false
	}
	rate := (count*sumXY - sumX*sumY) / denominator
	return rate, isFinite(rate)
}

func appendPredictionUsage(result *WindowPrediction, input PredictionInput) {
	if input.Window.UsedPercent == nil || !validPredictionPercent(*input.Window.UsedPercent) {
		return
	}
	if *input.Window.UsedPercent < CapacityMinUsedPercent {
		appendPredictionReason(result, PredictionReasonLowUsage)
		return
	}
	if input.UsageReason != "" {
		appendPredictionReason(result, input.UsageReason)
		return
	}
	if input.Usage == nil {
		return
	}
	if input.Usage.Requests <= 0 {
		appendPredictionReason(result, PredictionReasonNoTraffic)
		return
	}
	if input.Usage.PricedRequests <= 0 {
		appendPredictionReason(result, PredictionReasonNoPrice)
		return
	}
	if float64(input.Usage.PricedRequests) < float64(input.Usage.Requests)*capacityMinPricedShare {
		appendPredictionReason(result, PredictionReasonInsufficientPricedUsage)
		return
	}
	if result.ProjectedPercentAtReset == nil {
		return
	}
	scale := *result.ProjectedPercentAtReset / *input.Window.UsedPercent
	if !isFinite(scale) || scale < 0 || input.Usage.Tokens < 0 || input.Usage.CostNanos < 0 {
		appendPredictionReason(result, PredictionReasonArithmeticUnsafe)
		return
	}
	tokens, tokensSafe := roundedInt64(float64(input.Usage.Tokens) * scale)
	cost, costSafe := roundedInt64(float64(input.Usage.CostNanos) * scale)
	if !tokensSafe || !costSafe {
		appendPredictionReason(result, PredictionReasonArithmeticUnsafe)
		return
	}
	result.ProjectedTokensAtReset = &tokens
	result.ProjectedCostNanosAtReset = &cost
}

func appendPredictionReason(result *WindowPrediction, reason string) {
	for _, existing := range result.Reasons {
		if existing == reason {
			return
		}
	}
	result.Reasons = append(result.Reasons, reason)
}

func roundPrediction(value float64, places int) (float64, bool) {
	factor := math.Pow10(places)
	if !isFinite(value) || !isFinite(factor) || !isFinite(value*factor) {
		return 0, false
	}
	rounded := math.Round(value*factor) / factor
	return rounded, isFinite(rounded)
}

func floatPointer(value float64) *float64 { return &value }

func predictionFreshWindow(window QuotaWindow) time.Duration {
	bound := predictionMinimumFreshWindow
	if window.PeriodHours != nil && *window.PeriodHours > 0 && isFinite(*window.PeriodHours) {
		windowBoundNanos := *window.PeriodHours * float64(time.Hour) / 4
		if windowBoundNanos >= float64(math.MaxInt64) {
			bound = predictionMaximumFreshWindow
		} else if windowBound := time.Duration(windowBoundNanos); windowBound > bound {
			bound = windowBound
		}
	}
	if bound > predictionMaximumFreshWindow {
		return predictionMaximumFreshWindow
	}
	return bound
}

func collapsePredictionObservations(input PredictionInput, fromMS, toMS int64) ([]PredictionObservation, bool) {
	points := make([]PredictionObservation, 0, len(input.Observations)+1)
	invalid := false
	for _, observation := range input.Observations {
		if observation.ObservedAtMS < fromMS || observation.ObservedAtMS > toMS {
			continue
		}
		if !validPredictionPercent(observation.UsedPercent) {
			invalid = true
			continue
		}
		points = append(points, observation)
	}
	points = append(points, PredictionObservation{
		ObservedAtMS: input.ObservedAtMS, UsedPercent: *input.Window.UsedPercent,
		ResetAtMS: input.Window.ResetAtMS, Source: "current",
	})
	sort.Slice(points, func(first, second int) bool {
		if points[first].ObservedAtMS != points[second].ObservedAtMS {
			return points[first].ObservedAtMS < points[second].ObservedAtMS
		}
		firstPriority, secondPriority := predictionSourcePriority(points[first].Source), predictionSourcePriority(points[second].Source)
		if firstPriority != secondPriority {
			return firstPriority < secondPriority
		}
		if points[first].UsedPercent != points[second].UsedPercent {
			return points[first].UsedPercent < points[second].UsedPercent
		}
		return resetSortValue(points[first].ResetAtMS) < resetSortValue(points[second].ResetAtMS)
	})
	deduplicated := points[:0]
	for _, point := range points {
		if len(deduplicated) > 0 && deduplicated[len(deduplicated)-1].ObservedAtMS == point.ObservedAtMS {
			deduplicated[len(deduplicated)-1] = point
			continue
		}
		deduplicated = append(deduplicated, point)
	}
	return deduplicated, invalid
}

func predictionSourcePriority(source string) int {
	switch source {
	case ObservationSourcePassive:
		return 1
	case ObservationSourceActive:
		return 2
	case "current":
		return 3
	default:
		return 0
	}
}

func resetSortValue(value *int64) int64 {
	if value == nil {
		return math.MinInt64
	}
	return *value
}

func validPredictionPercent(value float64) bool {
	return isFinite(value) && value >= 0 && value <= 100
}

func isFinite(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }

func roundedInt64(value float64) (int64, bool) {
	if !isFinite(value) || value >= float64(math.MaxInt64) || value < float64(math.MinInt64) {
		return 0, false
	}
	rounded := math.Round(value)
	if rounded >= float64(math.MaxInt64) || rounded < float64(math.MinInt64) {
		return 0, false
	}
	return int64(rounded), true
}

func subtractInt64(high, low int64) (int64, bool) {
	if low < 0 && high > math.MaxInt64+low {
		return 0, false
	}
	if low > 0 && high < math.MinInt64+low {
		return 0, false
	}
	return high - low, true
}

func addInt64(first, second int64) (int64, bool) {
	if second > 0 && first > math.MaxInt64-second {
		return 0, false
	}
	if second < 0 && first < math.MinInt64-second {
		return 0, false
	}
	return first + second, true
}

func resetDifferenceExceeds(first, second, tolerance int64) bool {
	difference := math.Abs(float64(first) - float64(second))
	return !isFinite(difference) || difference > float64(tolerance)
}
