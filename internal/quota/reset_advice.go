package quota

import (
	"math"
	"slices"
	"time"
)

const (
	DefaultResetAdviceExpiryWindow   = 72 * time.Hour
	ResetAdviceNearExhaustionPercent = 90.0
	resetAdviceGrantFreshness        = 2 * time.Hour

	ResetAdviceUseBeforeExpiry  = "use_before_expiry"
	ResetAdviceKeepForEmergency = "keep_for_emergency"
	ResetAdviceNoAction         = "no_action"
	ResetAdviceUnknown          = "unknown"
)

type ResetAdviceInput struct {
	Provider     string
	Window       QuotaWindow
	ResetCredits *CodexResetCreditsInfo
	ResetGrants  []ClaudeResetGrant
	Status       string
	ObservedAtMS int64
	NowMS        int64
	ExpiryWindow time.Duration
}

type ResetAdvice struct {
	RemainingResets  *int
	EarliestExpiryMS *int64
	UsedPercent      *float64
	Verdict          string
	Evidence         string
	Reasons          []string
}

// AdviseReset evaluates stored evidence only. A newer quota header cannot renew
// the independently observed entitlement balance.
func AdviseReset(input ResetAdviceInput) ResetAdvice {
	result := ResetAdvice{Verdict: ResetAdviceUnknown, Evidence: "unknown", Reasons: []string{}}
	unknown := func(reason string) ResetAdvice {
		result.Reasons = []string{reason}
		return result
	}
	if input.Provider != "codex" && input.Provider != "claude" {
		return unknown("provider_unsupported")
	}
	if input.Status == "error" || input.Status == "stale" {
		return unknown("reading_error")
	}
	if input.ObservedAtMS <= 0 {
		return unknown("reading_missing")
	}
	if input.ObservedAtMS > input.NowMS || input.NowMS-input.ObservedAtMS > resetAdviceGrantFreshness.Milliseconds() {
		return unknown("reset_reading_stale")
	}
	remaining, expiry, reason := resetEntitlements(input)
	if reason != "" {
		return unknown(reason)
	}
	result.RemainingResets, result.EarliestExpiryMS = &remaining, expiry
	observedAtMS := input.ObservedAtMS
	if input.Window.ObservedAtMS != nil {
		observedAtMS = *input.Window.ObservedAtMS
	}
	if input.Window.UsedPercent == nil {
		return unknown("quota_reading_missing")
	}
	if !validPredictionPercent(*input.Window.UsedPercent) {
		return unknown("invalid_used_percent")
	}
	if observedAtMS <= 0 || observedAtMS > input.NowMS || input.NowMS-observedAtMS > predictionFreshWindow(input.Window).Milliseconds() {
		return unknown("quota_reading_stale")
	}
	if input.Window.ResetAtMS == nil {
		return unknown("quota_boundary_unknown")
	}
	if *input.Window.ResetAtMS <= input.NowMS {
		return unknown("quota_cycle_expired")
	}
	result.UsedPercent = input.Window.UsedPercent
	if remaining == 0 {
		result.Verdict, result.Evidence, result.Reasons = ResetAdviceNoAction, "fact", []string{"no_remaining_resets"}
		return result
	}
	if expiry == nil {
		return unknown("reset_expiry_unknown")
	}
	result.Evidence = "projection"
	if *result.UsedPercent >= ResetAdviceNearExhaustionPercent {
		result.Verdict, result.Reasons = ResetAdviceKeepForEmergency, []string{"quota_near_exhaustion"}
		return result
	}
	expiryWindow := input.ExpiryWindow
	if expiryWindow == 0 {
		expiryWindow = DefaultResetAdviceExpiryWindow
	}
	if *expiry-input.NowMS <= expiryWindow.Milliseconds() {
		result.Verdict, result.Reasons = ResetAdviceUseBeforeExpiry, []string{"reset_expiring_soon", "quota_below_emergency_threshold"}
	} else {
		result.Verdict, result.Reasons = ResetAdviceNoAction, []string{"reset_not_expiring_soon"}
	}
	return result
}

func resetEntitlements(input ResetAdviceInput) (int, *int64, string) {
	if input.Window.Scope != "standard" {
		return 0, nil, "reset_scope_unknown"
	}
	remaining := 0
	var earliest *int64
	hasUnknownExpiry := false
	add := func(count int, expiry *int64) bool {
		if count < 0 || count > math.MaxInt-remaining {
			return false
		}
		if count == 0 || expiry != nil && *expiry <= input.NowMS {
			return true
		}
		remaining += count
		if expiry == nil {
			hasUnknownExpiry = true
		} else if earliest == nil || *expiry < *earliest {
			earliest = expiry
		}
		return true
	}
	switch input.Provider {
	case "codex":
		credits := input.ResetCredits
		if credits == nil {
			return 0, nil, "reset_reading_missing"
		}
		if credits.Error != "" || credits.AvailableCount < 0 {
			return 0, nil, "reset_reading_invalid"
		}
		if len(credits.Credits) != credits.AvailableCount {
			return 0, nil, "reset_details_incomplete"
		}
		for _, credit := range credits.Credits {
			if credit.Status != "available" || !add(1, credit.ExpiresAtMS) {
				return 0, nil, "reset_reading_invalid"
			}
		}
	case "claude":
		if len(input.ResetGrants) == 0 {
			return 0, nil, "reset_reading_missing"
		}
		hasMatchingGrant := false
		for _, grant := range input.ResetGrants {
			if len(grant.Clears) == 0 {
				return 0, nil, "reset_scope_unknown"
			}
			if !slices.Contains(grant.Clears, input.Window.ID) {
				continue
			}
			hasMatchingGrant = true
			if grant.ResetsLeft > grant.ResetsTotal || !add(grant.ResetsLeft, grant.EndsAtMS) {
				return 0, nil, "reset_reading_invalid"
			}
		}
		if !hasMatchingGrant {
			return 0, nil, "no_matching_reset_grant"
		}
	}
	if hasUnknownExpiry {
		earliest = nil
	}
	return remaining, earliest, ""
}
