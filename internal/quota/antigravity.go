package quota

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type RawAntigravityBucket struct {
	BucketID          string `json:"bucketId"`
	BucketIDAlt       string `json:"bucket_id"`
	DisplayName       string `json:"displayName"`
	DisplayNameAlt    string `json:"display_name"`
	Window            string `json:"window"`
	ResetTime         string `json:"resetTime"`
	ResetTimeAlt      string `json:"reset_time"`
	RemainingFraction any    `json:"remainingFraction"`
	RemainingFracAlt  any    `json:"remaining_fraction"`
	Description       string `json:"description"`
}

type RawAntigravityGroup struct {
	DisplayName    string                 `json:"displayName"`
	DisplayNameAlt string                 `json:"display_name"`
	Description    string                 `json:"description"`
	Buckets        []RawAntigravityBucket `json:"buckets"`
}

type RawAntigravityPayload struct {
	Groups []RawAntigravityGroup `json:"groups"`
}

func parseAntigravityWindowHours(window string) float64 {
	// A substring such as "week" also matches "biweekly"; guessing a shorter
	// period would mix unrelated traffic into the new capacity numerator.
	switch strings.ToLower(strings.TrimSpace(window)) {
	case "5h", "5 hour", "5 hours", "five_hour", "five-hour":
		return 5
	case "24h", "24 hours", "1d", "day", "daily":
		return 24
	case "7d", "7 days", "1w", "week", "weekly":
		return 168
	case "30d", "30 days":
		return 720
	default:
		// A calendar month is not a fixed 30-day duration.
		return 0
	}
}

var antigravityRemainingSuffix = regexp.MustCompile(`(?i)\s*[-:]?\s*remaining$`)

// translateAntigravityBucketLabel names a bucket by its period. Upstream's
// display names drift ("Five Hour Limit Remaining"), so the name is matched
// after dropping the "Remaining" suffix, and the structured window field
// decides when the name matches nothing.
func translateAntigravityBucketLabel(groupName, bucketName, window string) string {
	b := strings.ToLower(strings.TrimSpace(antigravityRemainingSuffix.ReplaceAllString(bucketName, "")))
	windowLabel := strings.TrimSpace(antigravityRemainingSuffix.ReplaceAllString(bucketName, ""))
	switch {
	case (strings.Contains(b, "5") || strings.Contains(b, "five")) && strings.Contains(b, "hour"):
		windowLabel = "5小时限制 (5-Hour)"
	case strings.Contains(b, "daily") || strings.Contains(b, "day"):
		windowLabel = "每日限制 (Daily)"
	case strings.Contains(b, "week"):
		windowLabel = "每周限制 (Weekly)"
	case strings.Contains(b, "month"):
		windowLabel = "每月限制 (Monthly)"
	default:
		switch parseAntigravityWindowHours(window) {
		case 5:
			windowLabel = "5小时限制 (5-Hour)"
		case 24:
			windowLabel = "每日限制 (Daily)"
		case 168:
			windowLabel = "每周限制 (Weekly)"
		}
	}

	if groupName != "" {
		return fmt.Sprintf("%s · %s", groupName, windowLabel)
	}
	return windowLabel
}

// antigravityBucketWindowOrder ranks buckets inside a group: the 5-hour
// window leads, then weekly, then anything else — matching CPAMC's builder,
// since upstream lists the weekly bucket first.
func antigravityBucketWindowOrder(bucket RawAntigravityBucket) int {
	switch hours := parseAntigravityWindowHours(bucket.Window); hours {
	case 5:
		return 0
	case 168:
		return 1
	default:
		return 2
	}
}

// ParseAntigravityUsage parses Antigravity quota response and returns normalized windows.
func ParseAntigravityUsage(raw []byte, nowMS int64, serverOffsetMS int64) ([]QuotaWindow, error) {
	windows, _, _, err := parseQuotaWindowGroups(raw, nowMS, serverOffsetMS, false)
	return windows, err
}

// ParsePluginQuotaWindows parses a plugin quota payload. The payload shares
// Antigravity's groups[].buckets[] shape but carries its display text in
// description and sends no bucket ids, so the parser follows the plugin's
// conventions:
//
//   - a missing bucket displayName falls back to the description's first
//     segment ("5-hour session", "Weekly", "Rolling"), still prefixed with the
//     group name;
//   - a "stale" segment marks the window, and the payload's stale result, as
//     the plugin's last successful reading;
//   - "read at <RFC3339>" / "last read at <RFC3339>" is that reading's
//     observation time (observedAtMS, 0 when absent);
//   - the remaining description segments are the window's Note;
//   - a non-RFC3339 resetTime is kept as ResetLabel instead of dropped;
//   - rolling and monthly windows keep a nil PeriodHours rather than guessing.
//
// Plugin buckets are not reordered: the plugin already sends them in its
// display order (rolling, weekly, monthly).
func ParsePluginQuotaWindows(raw []byte, nowMS int64, serverOffsetMS int64) (windows []QuotaWindow, observedAtMS int64, stale bool, err error) {
	return parseQuotaWindowGroups(raw, nowMS, serverOffsetMS, true)
}

// parseQuotaWindowGroups normalizes the groups[].buckets[] shape shared by the
// Antigravity response and plugin payloads. pluginShape selects the plugin's
// conventions; the Antigravity path keeps its output unchanged.
func parseQuotaWindowGroups(raw []byte, nowMS int64, serverOffsetMS int64, pluginShape bool) ([]QuotaWindow, int64, bool, error) {
	var payload RawAntigravityPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, 0, false, fmt.Errorf("invalid antigravity payload: %w", err)
	}

	windows := make([]QuotaWindow, 0)
	var observedAtMS int64
	stale := false

	for groupIndex, group := range payload.Groups {
		gName := group.DisplayName
		if gName == "" {
			gName = group.DisplayNameAlt
		}
		if gName == "" {
			gName = fmt.Sprintf("Group %d", groupIndex+1)
		}

		// Keep each group's windows contiguous while ordering 5h before weekly.
		// Plugin buckets already arrive in the plugin's display order, so only
		// the Antigravity response is sorted.
		buckets := group.Buckets
		if !pluginShape {
			buckets = make([]RawAntigravityBucket, len(group.Buckets))
			copy(buckets, group.Buckets)
			sort.SliceStable(buckets, func(i, j int) bool {
				return antigravityBucketWindowOrder(buckets[i]) < antigravityBucketWindowOrder(buckets[j])
			})
		}

		for bucketIndex, bucket := range buckets {
			bucketID := bucket.BucketID
			if bucketID == "" {
				bucketID = bucket.BucketIDAlt
			}
			if bucketID == "" {
				bucketID = fmt.Sprintf("bucket_%d_%d", groupIndex, bucketIndex)
			}

			bName := bucket.DisplayName
			if bName == "" {
				bName = bucket.DisplayNameAlt
			}

			var (
				fullLabel   string
				windowStale bool
				note        string
			)
			if pluginShape {
				segments := splitPluginDescription(bucket.Description)
				if len(segments) > 0 {
					if bName == "" {
						bName = segments[0]
					}
					// The leading segment repeats the bucket's display text; it
					// names the window, it is not an explanation.
					segments = segments[1:]
				}
				if bName == "" {
					bName = strings.TrimSpace(bucket.Window)
				}
				if bName == "" {
					bName = bucketID
				}
				var windowObservedAtMS int64
				windowStale, windowObservedAtMS, note = pluginDescriptionDetails(segments)
				if windowStale {
					stale = true
				}
				if windowObservedAtMS > observedAtMS {
					observedAtMS = windowObservedAtMS
				}
				fullLabel = bName
				if gName != "" {
					fullLabel = fmt.Sprintf("%s · %s", gName, bName)
				}
			} else {
				if bName == "" {
					bName = bucketID
				}
				fullLabel = translateAntigravityBucketLabel(gName, bName, bucket.Window)
			}

			remainingPercent, usedPercent := antigravityBucketPercents(bucket)

			resetTimeStr := bucket.ResetTime
			if resetTimeStr == "" {
				resetTimeStr = bucket.ResetTimeAlt
			}
			resetAtMS, resetLabel := antigravityBucketReset(resetTimeStr, nowMS, serverOffsetMS, pluginShape)

			resetAccuracy := "exact"
			if pluginShape && resetAtMS == nil {
				// A reset kept only as page text states no boundary the cycle
				// arithmetic can use.
				resetAccuracy = ""
			}

			windowID := fmt.Sprintf("ag_%s_%s", gName, bucketID)
			periodHours := parseAntigravityWindowHours(bucket.Window)
			var periodHoursPtr *float64
			if pluginShape {
				// The plugin payload has no bucket id; derive a stable one from
				// the group and the bucket's position in the payload.
				windowID = fmt.Sprintf("plugin_%s_%d", gName, bucketIndex)
				if periodHours > 0 {
					periodHoursPtr = &periodHours
				}
			} else {
				periodHoursPtr = &periodHours
			}

			windows = append(windows, QuotaWindow{
				ID:               windowID,
				Label:            fullLabel,
				Scope:            "group",
				ModelFamilies:    AntigravityModelFamilies(gName),
				UsedPercent:      usedPercent,
				RemainingPercent: remainingPercent,
				ResetAtMS:        resetAtMS,
				ResetLabel:       resetLabel,
				PeriodHours:      periodHoursPtr,
				ResetAccuracy:    resetAccuracy,
				Stale:            windowStale,
				Note:             note,
			})
		}
	}

	return windows, observedAtMS, stale, nil
}

// antigravityBucketPercents converts remainingFraction into the pair the UI
// renders. A missing or unreadable fraction stays nil: a window the payload
// does not state must not read as an empty window.
func antigravityBucketPercents(bucket RawAntigravityBucket) (*float64, *float64) {
	rawFrac := bucket.RemainingFraction
	if rawFrac == nil {
		rawFrac = bucket.RemainingFracAlt
	}
	frac, ok := toFloat(rawFrac)
	if !ok {
		return nil, nil
	}
	// remainingFraction is 0.0 .. 1.0 (or sometimes 0..100)
	var rem float64
	if frac <= 1.0 && frac >= 0.0 {
		rem = clamp(frac*100, 0, 100)
	} else {
		rem = clamp(frac, 0, 100)
	}
	used := clamp(100-rem, 0, 100)
	return &rem, &used
}

// antigravityBucketReset resolves a bucket's resetTime. RFC3339 instants become
// ResetAtMS plus the relative label; plugin payloads keep any other text (for
// example Ollama's "Resets in 3 hours") as the label instead of dropping it.
func antigravityBucketReset(resetTimeStr string, nowMS int64, serverOffsetMS int64, keepText bool) (*int64, string) {
	if resetTimeStr == "" {
		return nil, ""
	}
	if ms, ok := parseRFC3339MS(resetTimeStr); ok {
		ms += serverOffsetMS
		return &ms, formatResetInstant(ms, nowMS)
	}
	if keepText {
		return nil, strings.TrimSpace(resetTimeStr)
	}
	return nil, ""
}

// pluginDescriptionSeparator is how a plugin bucket joins the parts of its
// description: the window label, the read time / stale marker, then notes.
const pluginDescriptionSeparator = " · "

// splitPluginDescription returns the non-empty segments of a plugin bucket
// description. Empty parts are dropped so a doubled separator cannot become a
// blank window name or note.
func splitPluginDescription(description string) []string {
	parts := strings.Split(description, pluginDescriptionSeparator)
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			segments = append(segments, part)
		}
	}
	return segments
}

// pluginDescriptionDetails reads the description segments left after the
// window's display text. A "stale" segment marks the plugin's cached reading;
// "read at …" / "last read at …" states when that reading was taken; every
// other segment explains the reading to the operator and is kept in the note.
func pluginDescriptionDetails(segments []string) (stale bool, observedAtMS int64, note string) {
	notes := make([]string, 0, len(segments))
	for _, segment := range segments {
		lower := strings.ToLower(segment)
		if lower == "stale" {
			stale = true
			continue
		}
		if value, ok := pluginReadTime(lower, segment); ok {
			if ms, ok := parseRFC3339MS(value); ok {
				if ms > observedAtMS {
					observedAtMS = ms
				}
				continue
			}
		}
		notes = append(notes, segment)
	}
	return stale, observedAtMS, strings.Join(notes, pluginDescriptionSeparator)
}

// pluginReadTime strips the observation prefix from a description segment. The
// plugin writes "read at <RFC3339>" for a fresh reading and
// "last read at <RFC3339>" for a stale one.
func pluginReadTime(lower, segment string) (string, bool) {
	for _, prefix := range []string{"last read at ", "read at "} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(segment[len(prefix):]), true
		}
	}
	return "", false
}

// parseRFC3339MS parses the plugin's UTC timestamps. Both layouts are tried
// because a payload may state fractional seconds.
func parseRFC3339MS(value string) (int64, bool) {
	for _, layout := range []string{time.RFC3339, time.RFC3339Nano} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}

// ResolveAntigravityPlan creates the plan descriptor for Antigravity subscriptions.
func ResolveAntigravityPlan(planType string) *QuotaPlan {
	norm := strings.ToLower(strings.TrimSpace(planType))
	switch norm {
	case "ultra":
		return &QuotaPlan{
			PlanType:  "ultra",
			PlanLabel: "Ultra",
			Tier:      "elite",
		}
	case "ultra-lite", "ultra_lite":
		return &QuotaPlan{
			PlanType:  "ultra-lite",
			PlanLabel: "Ultra Lite",
			Tier:      "premium",
		}
	case "pro":
		return &QuotaPlan{
			PlanType:  "pro",
			PlanLabel: "Pro",
			Tier:      "standard",
		}
	case "free":
		return &QuotaPlan{
			PlanType:  "free",
			PlanLabel: "Free",
			Tier:      "free",
		}
	default:
		if norm != "" {
			return &QuotaPlan{
				PlanType:  norm,
				PlanLabel: strings.ToUpper(norm[:1]) + norm[1:],
				Tier:      "standard",
			}
		}
		return &QuotaPlan{
			PlanType:  "unknown",
			PlanLabel: "未知套餐",
			Tier:      "unknown",
		}
	}
}
