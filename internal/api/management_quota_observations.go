package api

import (
	"context"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

// overlayPassiveObservations replaces a standard five-hour or weekly window's
// reading with a fresher passive observation.
//
// A refresh can be minutes or hours behind the traffic a credential is serving,
// and every request already carried the window's real state in its headers. When
// that header reading is newer than the stored snapshot, the console and the
// quota tools show it, marked with the instant it was taken and its source. The
// capacity estimate is deliberately left alone: it is derived from the snapshot's
// cycle, and splicing a second reading into it would mix two observations.
func (h *Handler) overlayPassiveObservations(ctx context.Context, observation *quota.NormalizedQuota) {
	if h.repo == nil || observation == nil || observation.AuthIndex == "" || len(observation.Windows) == 0 {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(observation.Provider))
	if provider != quota.PassiveProviderClaude && provider != quota.PassiveProviderCodex {
		return
	}
	latest, err := h.repo.LatestStandardPassiveObservations(ctx, []string{observation.AuthIndex})
	if err != nil {
		return
	}
	byKind := latest[observation.AuthIndex]
	if len(byKind) == 0 {
		return
	}
	for i := range observation.Windows {
		window := &observation.Windows[i]
		kind, ok := quota.PassiveWindowKindFor(provider, *window)
		if !ok {
			continue
		}
		passive, exists := byKind[kind]
		if !exists || passive.ObservedAtMS <= observation.ObservedAtMS {
			continue
		}
		used := passive.UsedPercent
		window.UsedPercent = &used
		remaining := 100 - used
		if remaining < 0 {
			remaining = 0
		}
		window.RemainingPercent = &remaining
		if passive.ResetAtMS != nil {
			window.ResetAtMS = passive.ResetAtMS
		}
		observedAt := passive.ObservedAtMS
		window.ObservedAtMS = &observedAt
		window.Source = quota.ObservationSourcePassive
	}
}
