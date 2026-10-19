package quota

import (
	"math"
	"sort"
	"time"
)

const PredictionFreshWindow = 15 * time.Minute

const (
	PredictionVerdictWillExhaust = "will_exhaust"
	PredictionVerdictUnderUsed   = "under_used"
	PredictionVerdictOnTrack     = "on_track"
	PredictionVerdictUnknown     = "unknown"

	PredictionEvidenceFact       = "fact"
	PredictionEvidenceProjection = "projection"
	PredictionEvidenceUnknown    = "unknown"

	PredictionReasonBoundaryUnknown          = "boundary_unknown"
	PredictionReasonCycleExpired             = "cycle_expired"
	PredictionReasonReadingStale             = "stale_reading"
	PredictionReasonReadingError             = "reading_error"
	PredictionReasonIncompleteHistory        = "incomplete_history"
	PredictionReasonResetMidCycle            = "mid_cycle_reset"
	PredictionReasonInsufficientObservations = "fewer_than_two_observations"
	PredictionReasonCycleTooEarly            = "cycle_too_early"
	PredictionReasonNoPositiveTrend          = "no_positive_trend"
	PredictionReasonLowUsage                 = "used_percent_below_five"
	PredictionReasonNoTraffic                = "no_traffic"
	PredictionReasonNoPrice                  = "no_price"
	PredictionReasonInsufficientPricedUsage  = "insufficient_priced_usage"
	PredictionReasonUsageUnavailable         = "usage_unavailable"
)

type PredictionObservation struct {
	ObservedAtMS int64
	UsedPercent  float64
	ResetAtMS    *int64
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
}

func PredictWindow(input PredictionInput) (result WindowPrediction) {
	result = WindowPrediction{
		UsedPercent:  input.Window.UsedPercent,
		ResetAtMS:    input.Window.ResetAtMS,
		ObservedAtMS: input.ObservedAtMS,
		Verdict:      PredictionVerdictUnknown,
		Evidence:     PredictionEvidenceUnknown,
		Reasons:      []string{},
	}
	if input.ObservedAtMS > 0 {
		age := max(int64(0), (input.NowMS-input.ObservedAtMS)/1000)
		result.DataAgeSeconds = &age
	}
	if input.Status == "error" || input.Status == "stale" {
		result.Reasons = append(result.Reasons, PredictionReasonReadingError)
		if input.Status == "stale" {
			result.Reasons[0] = PredictionReasonReadingStale
		}
		return result
	}
	if input.Window.UsedPercent == nil || math.IsNaN(*input.Window.UsedPercent) || math.IsInf(*input.Window.UsedPercent, 0) {
		result.Reasons = append(result.Reasons, CapacityReasonNoReading)
		return result
	}
	defer appendPredictionUsage(&result, input)
	if input.ObservedAtMS <= 0 || input.NowMS-input.ObservedAtMS > PredictionFreshWindow.Milliseconds() {
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
	if input.Window.HasIncompleteHistory {
		result.Reasons = append(result.Reasons, PredictionReasonIncompleteHistory)
		return result
	}
	if input.Window.HasMidCycleReset || predictionHasReset(input.Observations, fromMS, toMS) {
		result.Reasons = append(result.Reasons, PredictionReasonResetMidCycle)
		return result
	}
	if *input.Window.UsedPercent >= 100 {
		result.Verdict = PredictionVerdictWillExhaust
		result.Evidence = PredictionEvidenceFact
		result.ExhaustionETAMS = &result.ObservedAtMS
		return result
	}
	points := predictionPoints(input, fromMS, toMS)
	if len(points) < 2 {
		result.Reasons = append(result.Reasons, PredictionReasonInsufficientObservations)
		return result
	}
	rate := predictionSlope(points)
	cycleDurationMS := *input.Window.ResetAtMS - fromMS
	elapsedFraction := float64(input.ObservedAtMS-fromMS) / float64(cycleDurationMS)
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
		result.ProjectedPercentAtReset = floatPointer(roundPrediction(*input.Window.UsedPercent, 2))
		if input.Window.UsedPercent != nil && elapsedFraction > 0.5 && *input.Window.UsedPercent < 50 {
			result.Verdict = PredictionVerdictUnderUsed
		} else {
			result.Verdict = PredictionVerdictOnTrack
		}
		return result
	}

	result.Evidence = PredictionEvidenceProjection
	result.BurnRatePerHour = floatPointer(roundPrediction(rate, 4))
	hoursToReset := float64(*input.Window.ResetAtMS-input.ObservedAtMS) / float64(time.Hour/time.Millisecond)
	projected := *input.Window.UsedPercent + rate*hoursToReset
	result.ProjectedPercentAtReset = floatPointer(roundPrediction(projected, 2))
	hoursToExhaustion := (100 - *input.Window.UsedPercent) / rate
	if hoursToExhaustion >= 0 {
		eta := input.ObservedAtMS + int64(hoursToExhaustion*float64(time.Hour/time.Millisecond))
		result.ExhaustionETAMS = &eta
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

func predictionPoints(input PredictionInput, fromMS, toMS int64) []PredictionObservation {
	points := make([]PredictionObservation, 0, len(input.Observations)+1)
	for _, observation := range input.Observations {
		if observation.ObservedAtMS < fromMS || observation.ObservedAtMS > toMS || math.IsNaN(observation.UsedPercent) || math.IsInf(observation.UsedPercent, 0) {
			continue
		}
		if observation.ResetAtMS != nil && input.Window.ResetAtMS != nil && absInt64(*observation.ResetAtMS-*input.Window.ResetAtMS) > 1000 {
			continue
		}
		points = append(points, observation)
	}
	if input.Window.UsedPercent != nil {
		points = append(points, PredictionObservation{ObservedAtMS: input.ObservedAtMS, UsedPercent: *input.Window.UsedPercent, ResetAtMS: input.Window.ResetAtMS})
	}
	sort.SliceStable(points, func(first, second int) bool { return points[first].ObservedAtMS < points[second].ObservedAtMS })
	deduplicated := points[:0]
	for _, point := range points {
		if len(deduplicated) > 0 && deduplicated[len(deduplicated)-1].ObservedAtMS == point.ObservedAtMS {
			deduplicated[len(deduplicated)-1] = point
			continue
		}
		deduplicated = append(deduplicated, point)
	}
	return deduplicated
}

func predictionHasReset(observations []PredictionObservation, fromMS, toMS int64) bool {
	points := append([]PredictionObservation(nil), observations...)
	sort.SliceStable(points, func(first, second int) bool { return points[first].ObservedAtMS < points[second].ObservedAtMS })
	var previous *PredictionObservation
	for index := range points {
		point := &points[index]
		if point.ObservedAtMS < fromMS || point.ObservedAtMS > toMS {
			continue
		}
		if previous != nil {
			if previous.UsedPercent > point.UsedPercent+capacityResetTolerancePercent {
				return true
			}
			if previous.ResetAtMS != nil && point.ResetAtMS != nil && absInt64(*previous.ResetAtMS-*point.ResetAtMS) > 1000 {
				return true
			}
		}
		previous = point
	}
	return false
}

func predictionSlope(points []PredictionObservation) float64 {
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
	if denominator <= 0 {
		return 0
	}
	return (count*sumXY - sumX*sumY) / denominator
}

func appendPredictionUsage(result *WindowPrediction, input PredictionInput) {
	if input.Window.UsedPercent == nil || *input.Window.UsedPercent < CapacityMinUsedPercent {
		appendPredictionReason(result, PredictionReasonLowUsage)
		return
	}
	if input.UsageReason != "" {
		appendPredictionReason(result, PredictionReasonUsageUnavailable)
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
	tokens := int64(math.Round(float64(input.Usage.Tokens) * scale))
	cost := int64(math.Round(float64(input.Usage.CostNanos) * scale))
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

func roundPrediction(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

func floatPointer(value float64) *float64 { return &value }

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
