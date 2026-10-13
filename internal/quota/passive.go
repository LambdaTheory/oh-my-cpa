package quota

import (
	"math"
	"strconv"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

// Passive observation vocabulary. The product and window are read from the
// upstream headers themselves rather than from the request's provider label,
// so a gateway that files a quota header under a different product name cannot
// attribute the reading to the wrong provider.
const (
	PassiveProviderCodex  = "codex"
	PassiveProviderClaude = "claude"

	PassiveWindowFiveHour = "five_hour"
	PassiveWindowWeekly   = "weekly"

	// The two window lengths the passive stream classifies. Anything else is
	// dropped rather than guessed: the storage enum admits only these kinds.
	passiveFiveHourMinutes = 300
	passiveWeeklyMinutes   = 10080

	// passiveMaxUnixSeconds bounds a reset instant to a value that cannot
	// overflow the milliseconds conversion.
	passiveMaxUnixSeconds = int64(1) << 42
)

// PassiveObservation is one quota window an upstream published on a request.
// It is evidence about the instant the request was answered, not a stored
// snapshot of current quota: observations accumulate as history.
type PassiveObservation struct {
	AuthIndex     string
	Provider      string
	WindowKind    string
	WindowMinutes int
	UsedPercent   float64
	ResetAtMS     *int64
	Status        string
	ObservedAtMS  int64
	EventKey      string
}

// ObservationsFromHeaders reads the quota windows one request's saved response
// headers published.
//
// A reading is only produced when every part of it is present and sane: a
// credential-less event, an event with no recognized quota headers, and any
// window whose length, percentage or reset instant is missing, unparsable or
// out of range yields no observation for that window. A malformed reading is
// dropped, never guessed, because a fabricated zero would misreport quota.
func ObservationsFromHeaders(event usage.Event) []PassiveObservation {
	if strings.TrimSpace(event.AuthIndex) == "" || event.TimestampMS <= 0 {
		return nil
	}
	headers := storedHeaderMap(event.ResponseHeaders)
	if len(headers) == 0 {
		return nil
	}
	observations := codexObservations(headers, event)
	return append(observations, claudeObservations(headers, event)...)
}

func storedHeaderMap(stored string) map[string]string {
	decoded := usage.DecodeResponseHeaders(stored)
	if len(decoded) == 0 {
		return nil
	}
	result := make(map[string]string, len(decoded))
	for _, header := range decoded {
		name := strings.ToLower(strings.TrimSpace(header.Name))
		if name == "" {
			continue
		}
		result[name] = strings.TrimSpace(header.Value)
	}
	return result
}

// passiveWindowKind maps a window length in minutes onto a stored kind. Only the
// two canonical lengths are recognized; a provider that meters a different
// period has no place in this stream yet.
func passiveWindowKind(minutes int) (string, bool) {
	switch minutes {
	case passiveFiveHourMinutes:
		return PassiveWindowFiveHour, true
	case passiveWeeklyMinutes:
		return PassiveWindowWeekly, true
	default:
		return "", false
	}
}

// codexObservations reads the primary and secondary windows of a Codex response.
// A secondary window with a zero length means there is no secondary limit, so
// it produces no observation.
func codexObservations(headers map[string]string, event usage.Event) []PassiveObservation {
	var observations []PassiveObservation
	for _, slot := range []string{"primary", "secondary"} {
		prefix := "x-codex-" + slot
		minutesRaw, present := headers[prefix+"-window-minutes"]
		if !present {
			continue
		}
		minutes, err := strconv.Atoi(strings.TrimSpace(minutesRaw))
		if err != nil || minutes <= 0 {
			continue
		}
		kind, classified := passiveWindowKind(minutes)
		if !classified {
			continue
		}
		usedPercent, ok := passivePercent(headers[prefix+"-used-percent"])
		if !ok {
			continue
		}
		resetAtMS, ok := passiveResetAt(headers[prefix+"-reset-at"])
		if !ok {
			continue
		}
		observations = append(observations, PassiveObservation{
			AuthIndex:     event.AuthIndex,
			Provider:      PassiveProviderCodex,
			WindowKind:    kind,
			WindowMinutes: minutes,
			UsedPercent:   usedPercent,
			ResetAtMS:     resetAtMS,
			ObservedAtMS:  event.TimestampMS,
			EventKey:      event.EventKey,
		})
	}
	return observations
}

// claudeObservations reads Anthropic's unified five-hour and seven-day windows.
// Utilization is published as a fraction of the limit, so it is scaled to the
// stored 0..100 percentage.
func claudeObservations(headers map[string]string, event usage.Event) []PassiveObservation {
	var observations []PassiveObservation
	for _, slot := range []struct {
		key     string
		kind    string
		minutes int
	}{
		{"5h", PassiveWindowFiveHour, passiveFiveHourMinutes},
		{"7d", PassiveWindowWeekly, passiveWeeklyMinutes},
	} {
		prefix := "anthropic-ratelimit-unified-" + slot.key
		utilizationRaw, present := headers[prefix+"-utilization"]
		if !present {
			continue
		}
		utilization, err := strconv.ParseFloat(strings.TrimSpace(utilizationRaw), 64)
		if err != nil || math.IsNaN(utilization) || math.IsInf(utilization, 0) {
			continue
		}
		usedPercent := utilization * 100
		if usedPercent < 0 || usedPercent > 100 {
			continue
		}
		resetAtMS, ok := passiveResetAt(headers[prefix+"-reset"])
		if !ok {
			continue
		}
		observations = append(observations, PassiveObservation{
			AuthIndex:     event.AuthIndex,
			Provider:      PassiveProviderClaude,
			WindowKind:    slot.kind,
			WindowMinutes: slot.minutes,
			UsedPercent:   usedPercent,
			ResetAtMS:     resetAtMS,
			Status:        boundedStatus(headers[prefix+"-status"]),
			ObservedAtMS:  event.TimestampMS,
			EventKey:      event.EventKey,
		})
	}
	return observations
}

// passivePercent parses a used-percent header already expressed in 0..100.
func passivePercent(raw string) (float64, bool) {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 100 {
		return 0, false
	}
	return value, true
}

// passiveResetAt parses a Unix-seconds reset instant. An absent header yields a
// nil instant; a present but negative or out-of-range one is malformed, which
// the caller treats as "drop this window" rather than "no reset".
func passiveResetAt(raw string) (*int64, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, true
	}
	seconds, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || seconds < 0 || seconds > passiveMaxUnixSeconds {
		return nil, false
	}
	milliseconds := seconds * 1000
	return &milliseconds, true
}

// boundedStatus keeps a provider status token short and free of control
// characters; the stored form is display evidence, not an enumerated value.
func boundedStatus(raw string) string {
	cleaned := strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return -1
		}
		return character
	}, strings.TrimSpace(raw))
	if len([]rune(cleaned)) > 64 {
		cleaned = string([]rune(cleaned)[:64])
	}
	return cleaned
}
