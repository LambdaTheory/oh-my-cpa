package ollama

import (
	"html"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// parsed is the raw result of one settings-page scrape.
type parsed struct {
	windows []Window
	plan    string
	balance string
}

var (
	// Markup is stripped with a small, dependency-free flattener: the module must
	// not take a new module dependency, so the tag handling lives here rather than
	// in an HTML package.
	reScript   = regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`)
	reStyle    = regexp.MustCompile(`(?is)<style\b[^>]*>.*?</style>`)
	reDataTime = regexp.MustCompile(`(?is)<(?:time|local-time)\b[^>]*?\bdata-time\s*=\s*"([^"]*)"[^>]*>`)
	reDateTime = regexp.MustCompile(`(?is)<(?:time|local-time)\b[^>]*?\bdatetime\s*=\s*"([^"]*)"[^>]*>`)
	reBlock    = regexp.MustCompile(`(?is)</?(?:address|article|aside|blockquote|br|dd|div|dl|dt|fieldset|figcaption|figure|footer|form|h[1-6]|header|hr|li|main|nav|ol|p|pre|section|table|td|th|tr|ul)\b[^>]*>`)
	reAnyTag   = regexp.MustCompile(`(?s)<[^>]*>`)

	// reStamp matches the machine-readable reset time the flattener writes inline.
	reStamp = regexp.MustCompile(`\[ts:([^\]]+)\]`)
	// rePercent matches a usage share, used or remaining. The optional sign is
	// captured so a negative value such as "-1%" is refused rather than silently
	// read as "+1".
	rePercent = regexp.MustCompile(`(-?\d+(?:\.\d+)?)\s*%`)
	// reReset reads a "Resets in 3h" / "Resets at <time>" hint.
	reReset = regexp.MustCompile(`(?i)resets?\s+(?:in|on|at)?\s*(.+)`)
	// "Session usage" is the current label for the five-hour window; the rest cover
	// older and alternative wordings.
	reLabel5h     = regexp.MustCompile(`(?i)(session usage|session limit|rolling|5[\s-]?hour|five[\s-]?hour|hourly)`)
	reLabel7d     = regexp.MustCompile(`(?i)(weekly|7[\s-]?day|seven[\s-]?day)`)
	reDur         = regexp.MustCompile(`(?i)(\d+)\s*([a-z]+)`)
	rePlanLn      = regexp.MustCompile(`(?i)\bplan\b[:\s]+([A-Za-z][\w -]{0,20})`)
	reCredit      = regexp.MustCompile(`(?i)^(?:credit balance|account balance|balance)\s*:?\s*([$€£]?\s?\d[\d.,]*)\s*$`)
	reCreditLabel = regexp.MustCompile(`(?i)^(?:credit balance|account balance|balance)\s*:?\s*$`)
	reBalance     = regexp.MustCompile(`^[$€£]?\s?\d[\d.,]*$`)
)

// parse flattens the settings HTML to text and applies heuristics to recover the
// plan, a rolling five-hour window and a weekly window. now anchors relative reset
// text such as "Resets in 3h" to an absolute instant.
func parse(document string, now time.Time) parsed {
	var out parsed
	if strings.TrimSpace(document) == "" {
		return out
	}
	lines := textLines(document)
	out.plan = extractPlan(lines)
	out.balance = extractBalance(lines)

	used := map[int]bool{}
	for _, spec := range []struct {
		name  string
		label *regexp.Regexp
		title string
	}{{"5h", reLabel5h, "Ollama 5-hour"}, {"7d", reLabel7d, "Ollama weekly"}} {
		idx := indexLabel(lines, spec.label)
		if idx < 0 {
			continue
		}
		window := Window{Name: spec.name, Label: spec.title}
		if percent, percentIndex := findPercent(lines, idx, used); percentIndex >= 0 {
			used[percentIndex] = true
			if value, ok := parsePercent(percent); ok {
				// The page may show what is left rather than what is used.
				context := strings.ToLower(lines[idx] + " " + lines[percentIndex])
				if (strings.Contains(context, "remaining") || strings.Contains(context, " left")) && !strings.Contains(context, "used") {
					value = 100 - value
				}
				window.UsedPercent = &value
			}
		}
		if at, ok := findStamp(lines, idx); ok {
			window.ResetAt = &at
		} else if reset := findReset(lines, idx); reset != "" {
			if at, derived, ok := parseReset(reset, now); ok {
				window.ResetAt = &at
				window.ResetDerived = derived
			} else {
				window.ResetText = reset
			}
		}
		// Only a numeric percentage makes a window real; a lone reset hint would
		// otherwise masquerade as a successful quota reading.
		if window.UsedPercent == nil {
			continue
		}
		out.windows = append(out.windows, window)
	}
	return out
}

// textLines flattens markup to whitespace-normalised lines, breaking at block
// boundaries and inlining a machine-readable reset stamp where one is present.
func textLines(document string) []string {
	body := reScript.ReplaceAllString(document, " ")
	body = reStyle.ReplaceAllString(body, " ")
	body = reDataTime.ReplaceAllString(body, " [ts:$1] ")
	body = reDateTime.ReplaceAllString(body, " [ts:$1] ")
	body = reBlock.ReplaceAllString(body, "\n")
	body = reAnyTag.ReplaceAllString(body, " ")
	body = html.UnescapeString(body)

	var lines []string
	for _, raw := range strings.Split(body, "\n") {
		if line := strings.Join(strings.Fields(raw), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func indexLabel(lines []string, re *regexp.Regexp) int {
	for index, line := range lines {
		if re.MatchString(line) {
			return index
		}
	}
	return -1
}

// findPercent looks for a percentage on the label line first, then nearby lines,
// skipping indices another window already claimed.
func findPercent(lines []string, idx int, used map[int]bool) (float64, int) {
	if match := rePercent.FindStringSubmatch(lines[idx]); match != nil {
		if value, err := strconv.ParseFloat(match[1], 64); err == nil {
			return value, idx
		}
	}
	for _, offset := range []int{1, -1, 2, -2, 3, -3} {
		index := idx + offset
		if index < 0 || index >= len(lines) || used[index] {
			continue
		}
		match := rePercent.FindStringSubmatch(lines[index])
		if match == nil {
			continue
		}
		if value, err := strconv.ParseFloat(match[1], 64); err == nil {
			return value, index
		}
	}
	return 0, -1
}

// findStamp reads a machine-readable reset instant near the label.
func findStamp(lines []string, idx int) (time.Time, bool) {
	for _, offset := range []int{0, 1, 2, 3} {
		index := idx + offset
		if index < 0 || index >= len(lines) {
			continue
		}
		if match := reStamp.FindStringSubmatch(lines[index]); match != nil {
			for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
				if at, err := time.Parse(layout, strings.TrimSpace(match[1])); err == nil {
					return at, true
				}
			}
		}
	}
	return time.Time{}, false
}

func findReset(lines []string, idx int) string {
	for _, offset := range []int{0, 1, 2, -1, 3} {
		index := idx + offset
		if index < 0 || index >= len(lines) {
			continue
		}
		if match := reReset.FindStringSubmatch(lines[index]); match != nil {
			if text := strings.TrimSpace(reStamp.ReplaceAllString(match[1], "")); text != "" {
				return text
			}
		}
	}
	return ""
}

// parseReset turns a reset hint into an absolute time. Relative text such as
// "3h" is reported as derived; an absolute instant is not. An unrecognised string
// yields ok false so it is kept verbatim instead of guessed.
func parseReset(text string, now time.Time) (at time.Time, derived bool, ok bool) {
	value := strings.TrimSpace(text)
	if value == "" {
		return time.Time{}, false, false
	}
	if duration, parsed := parseDurationText(value); parsed {
		return now.Add(duration), true, true
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, false, true
		}
	}
	return time.Time{}, false, false
}

// parseDurationText sums "3h", "6 days" and "2 hours 30 minutes" style text.
func parseDurationText(value string) (time.Duration, bool) {
	matches := reDur.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return 0, false
	}
	var total time.Duration
	for _, match := range matches {
		amount, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		// Units arrive singular, plural or abbreviated, so fold the plural away.
		unit := strings.TrimSuffix(strings.ToLower(match[2]), "s")
		switch unit {
		case "d", "day":
			total += time.Duration(amount) * 24 * time.Hour
		case "h", "hr", "hour":
			total += time.Duration(amount) * time.Hour
		case "m", "min", "minute":
			total += time.Duration(amount) * time.Minute
		case "w", "week":
			total += time.Duration(amount) * 7 * 24 * time.Hour
		case "mo", "month":
			total += time.Duration(amount) * 30 * 24 * time.Hour
		}
	}
	if total <= 0 {
		return 0, false
	}
	return total, true
}

// parsePercent validates a percentage scraped from the page. A value outside
// [0,100] or a non-finite value is refused rather than clamped: clamping 101 to
// 100 would fabricate a maxed reading the page never stated, and a negative value
// is not a usage share. The caller then leaves the window unreported.
func parsePercent(value float64) (float64, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	if value < 0 || value > 100 {
		return 0, false
	}
	return value, true
}

func extractPlan(lines []string) string {
	for _, line := range lines {
		if match := rePlanLn.FindStringSubmatch(line); match != nil {
			if plan := strings.TrimSpace(match[1]); plan != "" {
				return plan
			}
		}
	}
	for _, line := range lines {
		lower := strings.ToLower(line)
		for _, plan := range []string{"enterprise", "team", "pro", "max", "free", "trial"} {
			if containsWord(lower, plan) {
				return strings.ToUpper(plan[:1]) + plan[1:]
			}
		}
	}
	return ""
}

// containsWord avoids matching "pro" inside "profile" and "free" inside "freely".
func containsWord(value, word string) bool {
	at := strings.Index(value, word)
	if at < 0 {
		return false
	}
	before := at == 0 || !isLetter(rune(value[at-1]))
	after := at+len(word) == len(value) || !isLetter(rune(value[at+len(word)]))
	return before && after
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func extractBalance(lines []string) string {
	for index, line := range lines {
		if match := reCredit.FindStringSubmatch(line); match != nil {
			return strings.TrimSpace(match[1])
		}
		if reCreditLabel.MatchString(line) && index+1 < len(lines) && reBalance.MatchString(lines[index+1]) {
			return strings.TrimSpace(lines[index+1])
		}
	}
	return ""
}
