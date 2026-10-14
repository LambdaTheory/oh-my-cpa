package quota

import (
	"fmt"
	"strings"
)

// Observation sources. A passive observation is read from a request's own
// response headers; an active observation is the result of a refresh the process
// asked for.
const (
	ObservationSourcePassive = "passive"
	ObservationSourceActive  = "active"
)

// ObservationScopeStandard is the scope of a provider-wide window. It is the
// default so a row written before scopes existed still reads as a standard one.
const ObservationScopeStandard = "standard"

// ActiveObservation is one window of a completed active refresh, ready to be
// recorded in the observation timeline.
type ActiveObservation struct {
	AuthIndex     string
	Provider      string
	WindowKind    string
	WindowMinutes int
	UsedPercent   float64
	ResetAtMS     *int64
	Scope         string
	Model         string
	ObservedAtMS  int64
	EventKey      string
}

// PassiveWindowKindFor maps a normalized standard window onto the passive
// stream's window kind, so an active reading and the passive headers that
// describe the same window share one identity. Only the standard five-hour and
// weekly windows are shared; a model-scoped window has no passive counterpart.
func PassiveWindowKindFor(provider string, window QuotaWindow) (string, bool) {
	if window.Scope != ObservationScopeStandard || window.Model != "" {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case PassiveProviderClaude:
		switch window.ID {
		case PassiveWindowFiveHour:
			return PassiveWindowFiveHour, true
		case "seven_day":
			return PassiveWindowWeekly, true
		}
		return "", false
	case PassiveProviderCodex:
		if window.ID == PassiveWindowWeekly || window.Kind == PassiveWindowWeekly {
			return PassiveWindowWeekly, true
		}
		return "", false
	default:
		return "", false
	}
}

// ActiveObservationEventKey builds the unique key for one active window row. The
// scope and model are part of it because a single refresh can produce more than
// one window of the same kind, and the idempotence guard is keyed on event_key.
func ActiveObservationEventKey(authIndex string, observedAtMS int64, windowKind, scope, model string) string {
	return fmt.Sprintf("active:%s:%d:%s:%s:%s", strings.TrimSpace(authIndex), observedAtMS, windowKind, scope, model)
}

// WindowObservationKind names the window a refresh produced for storage.
//
// The window's own kind wins when it states one; a standard five-hour or weekly
// window otherwise takes the passive stream's identity so the two sources describe
// the same window; and a window with neither falls back to its id, which is
// always present.
func WindowObservationKind(provider string, window QuotaWindow) string {
	if kind := strings.ToLower(strings.TrimSpace(window.Kind)); kind != "" {
		if kind == PassiveWindowWeekly || kind == PassiveWindowFiveHour {
			return kind
		}
		return kind
	}
	if kind, ok := PassiveWindowKindFor(provider, window); ok {
		return kind
	}
	return strings.ToLower(strings.TrimSpace(window.ID))
}

// ActiveObservationsFromQuota turns a completed refresh into the rows to record.
// Only a window with a stated period and a used share qualifies: a window the
// provider published without a reading is not evidence of anything and must not
// become a zero row.
func ActiveObservationsFromQuota(value *NormalizedQuota) []ActiveObservation {
	if value == nil || strings.TrimSpace(value.AuthIndex) == "" || value.ObservedAtMS <= 0 {
		return nil
	}
	observations := make([]ActiveObservation, 0, len(value.Windows))
	for _, window := range value.Windows {
		if window.PeriodHours == nil || *window.PeriodHours <= 0 || window.UsedPercent == nil {
			continue
		}
		usedPercent := *window.UsedPercent
		if usedPercent < 0 {
			usedPercent = 0
		}
		if usedPercent > 100 {
			usedPercent = 100
		}
		kind := WindowObservationKind(value.Provider, window)
		if kind == "" {
			continue
		}
		scope := strings.TrimSpace(window.Scope)
		if scope == "" {
			scope = ObservationScopeStandard
		}
		model := strings.TrimSpace(window.Model)
		observations = append(observations, ActiveObservation{
			AuthIndex:     strings.TrimSpace(value.AuthIndex),
			Provider:      strings.TrimSpace(value.Provider),
			WindowKind:    kind,
			WindowMinutes: int(*window.PeriodHours * 60),
			UsedPercent:   usedPercent,
			ResetAtMS:     window.ResetAtMS,
			ObservedAtMS:  value.ObservedAtMS,
			Scope:         scope,
			Model:         model,
			EventKey:      ActiveObservationEventKey(value.AuthIndex, value.ObservedAtMS, kind, scope, model),
		})
	}
	return observations
}
