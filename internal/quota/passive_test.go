package quota

import (
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

func storedHeaders(t *testing.T, headers map[string]string) string {
	t.Helper()
	ordered := make([]usage.ResponseHeader, 0, len(headers))
	for name, value := range headers {
		ordered = append(ordered, usage.ResponseHeader{Name: name, Value: value})
	}
	return usage.EncodeResponseHeaders(ordered)
}

func observationEvent(t *testing.T, authIndex string, atMS int64, headers map[string]string) usage.Event {
	t.Helper()
	return usage.Event{AuthIndex: authIndex, EventKey: "event-1", TimestampMS: atMS, ResponseHeaders: storedHeaders(t, headers)}
}

func findObservation(t *testing.T, observations []PassiveObservation, provider, kind string) PassiveObservation {
	t.Helper()
	for _, observation := range observations {
		if observation.Provider == provider && observation.WindowKind == kind {
			return observation
		}
	}
	t.Fatalf("no %s/%s observation in %+v", provider, kind, observations)
	return PassiveObservation{}
}

func TestObservationsFromCodexPrimaryWeekly(t *testing.T) {
	event := observationEvent(t, "c-1", 1_700_000_000_000, map[string]string{
		"x-codex-primary-used-percent":     "37",
		"x-codex-primary-window-minutes":   "10080",
		"x-codex-primary-reset-at":         "1700003600",
		"x-codex-secondary-used-percent":   "82",
		"x-codex-secondary-window-minutes": "300",
		"x-codex-secondary-reset-at":       "1700001800",
		"anthropic-representative-claim":   "five_hour",
		"anthropic-ratelimit-unified-fake": "ignored",
	})
	observations := ObservationsFromHeaders(event)
	if len(observations) != 2 {
		t.Fatalf("observations = %+v, want 2", observations)
	}
	primary := findObservation(t, observations, PassiveProviderCodex, PassiveWindowWeekly)
	if primary.WindowMinutes != passiveWeeklyMinutes || primary.UsedPercent != 37 || primary.EventKey != "event-1" || primary.ObservedAtMS != 1_700_000_000_000 || primary.AuthIndex != "c-1" {
		t.Fatalf("primary = %+v", primary)
	}
	if primary.ResetAtMS == nil || *primary.ResetAtMS != 1_700_003_600_000 {
		t.Fatalf("primary reset = %v", primary.ResetAtMS)
	}
	secondary := findObservation(t, observations, PassiveProviderCodex, PassiveWindowFiveHour)
	if secondary.WindowMinutes != passiveFiveHourMinutes || secondary.UsedPercent != 82 {
		t.Fatalf("secondary = %+v", secondary)
	}
}

func TestObservationsSkipCodexSecondaryWithoutWindow(t *testing.T) {
	event := observationEvent(t, "c-1", 1_700_000_000_000, map[string]string{
		"x-codex-primary-used-percent":     "10",
		"x-codex-primary-window-minutes":   "10080",
		"x-codex-secondary-used-percent":   "0",
		"x-codex-secondary-window-minutes": "0",
	})
	observations := ObservationsFromHeaders(event)
	if len(observations) != 1 || observations[0].WindowKind != PassiveWindowWeekly {
		t.Fatalf("observations = %+v, want only the weekly window", observations)
	}
}

func TestObservationsFromClaudeUtilizationScalesToPercent(t *testing.T) {
	event := observationEvent(t, "cl-1", 1_700_000_000_000, map[string]string{
		"anthropic-ratelimit-unified-5h-utilization": "0.52",
		"anthropic-ratelimit-unified-5h-reset":       "1700001800",
		"anthropic-ratelimit-unified-5h-status":      "allowed",
		"anthropic-ratelimit-unified-7d-utilization": "0.9",
		"anthropic-ratelimit-unified-7d-reset":       "1700604800",
		"anthropic-ratelimit-unified-7d-status":      "rejected",
	})
	observations := ObservationsFromHeaders(event)
	if len(observations) != 2 {
		t.Fatalf("observations = %+v, want 2", observations)
	}
	fiveHour := findObservation(t, observations, PassiveProviderClaude, PassiveWindowFiveHour)
	if fiveHour.UsedPercent != 52 || fiveHour.WindowMinutes != 300 || fiveHour.Status != "allowed" {
		t.Fatalf("five hour = %+v", fiveHour)
	}
	weekly := findObservation(t, observations, PassiveProviderClaude, PassiveWindowWeekly)
	if weekly.UsedPercent != 90 || weekly.WindowMinutes != 10080 || weekly.Status != "rejected" {
		t.Fatalf("weekly = %+v", weekly)
	}
}

func TestObservationsReturnEmptyWithoutQuotaHeaders(t *testing.T) {
	event := observationEvent(t, "c-1", 1_700_000_000_000, map[string]string{
		"x-request-id": "abc",
		"anthropic-ratelimit-unified-overage-status": "enabled",
		"x-codex-primary-reset-after-seconds":        "3600",
	})
	if observations := ObservationsFromHeaders(event); len(observations) != 0 {
		t.Fatalf("observations = %+v, want none", observations)
	}
}

func TestObservationsSkipMalformedWindows(t *testing.T) {
	cases := map[string]map[string]string{
		"empty auth index": {
			"x-codex-primary-used-percent":   "10",
			"x-codex-primary-window-minutes": "10080",
		},
		"percent out of range": {
			"x-codex-primary-used-percent":   "150",
			"x-codex-primary-window-minutes": "10080",
		},
		"percent unparsable": {
			"x-codex-primary-used-percent":   "lots",
			"x-codex-primary-window-minutes": "10080",
		},
		"negative reset": {
			"x-codex-primary-used-percent":   "10",
			"x-codex-primary-window-minutes": "10080",
			"x-codex-primary-reset-at":       "-5",
		},
		"unknown window length": {
			"x-codex-primary-used-percent":   "10",
			"x-codex-primary-window-minutes": "1440",
		},
		"claude utilization above one": {
			"anthropic-ratelimit-unified-5h-utilization": "52",
			"anthropic-ratelimit-unified-5h-reset":       "1700001800",
		},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			authIndex := "c-1"
			if name == "empty auth index" {
				authIndex = ""
			}
			event := observationEvent(t, authIndex, 1_700_000_000_000, headers)
			if observations := ObservationsFromHeaders(event); len(observations) != 0 {
				t.Fatalf("observations = %+v, want none", observations)
			}
		})
	}
}

func TestObservationsRequireARecordedInstant(t *testing.T) {
	event := observationEvent(t, "c-1", 0, map[string]string{
		"x-codex-primary-used-percent":   "10",
		"x-codex-primary-window-minutes": "10080",
	})
	if observations := ObservationsFromHeaders(event); len(observations) != 0 {
		t.Fatalf("observations = %+v, want none without a recorded instant", observations)
	}
}
