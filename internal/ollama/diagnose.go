package ollama

import (
	"context"
	"regexp"
	"strings"
)

// Diagnosis is a redacted, structural view of one settings-page response.
//
// It exists because the upstream page stops yielding a quota window while still
// answering 200 with a valid session: the parser then cannot tell whether the
// numbers moved to another route, became client-rendered, or turned into an
// interstitial. The account owner cannot share the page, and the cookie must
// never leave the process, so this reports shape instead of content - counts,
// booleans, masked sample lines and same-origin links. It carries no body, no
// cookie, no digits and no addresses.
//
// This is a temporary diagnostic. Delete it, its route and its tests once the
// parser tracks the current page.
type Diagnosis struct {
	Status           int
	ContentType      string
	RedirectLocation string
	BodyBytes        int
	LooksLikeLogin   bool

	LineCount    int
	ScriptCount  int
	PercentCount int
	MoneyCount   int

	HasSessionWord bool
	HasWeeklyWord  bool
	HasBalanceWord bool
	HasCreditWord  bool
	HasQuotaWord   bool

	Headings         []string
	InterestingLines []string
	HrefPaths        []string
}

const (
	diagnosisLineLimit    = 40
	diagnosisHeadingLimit = 10
	diagnosisHrefLimit    = 20
	diagnosisTextLimit    = 120
)

var (
	reDiagnosisHeading     = regexp.MustCompile(`(?is)<h[1-6]\b[^>]*>(.*?)</h[1-6]>`)
	reDiagnosisHref        = regexp.MustCompile(`(?i)href\s*=\s*"([^"]*)"`)
	reDiagnosisTag         = regexp.MustCompile(`(?s)<[^>]*>`)
	reDiagnosisDigits      = regexp.MustCompile(`\d+`)
	reDiagnosisAddress     = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+`)
	reDiagnosisInteresting = regexp.MustCompile(`(?i)%|\$|€|£|usage|limit|balance|credit|plan|reset|week|hour|session|quota`)
)

// Diagnose fetches the settings page through the same guarded transport as
// Fetch - fixed origin, cookie attached here, redirects refused - and returns
// only the redacted structure. A transport failure is returned as an error; a
// login page, a redirect and an unparseable page are all reported in the
// Diagnosis so the caller can distinguish them.
func (c *Client) Diagnose(ctx context.Context, cookie string) (Diagnosis, error) {
	var diagnosis Diagnosis
	if strings.TrimSpace(cookie) == "" {
		return diagnosis, nil
	}
	body, status, header, err := c.get(ctx, cookie)
	if err != nil {
		return diagnosis, err
	}
	text := string(body)
	diagnosis.Status = status
	diagnosis.ContentType = header.Get("Content-Type")
	diagnosis.RedirectLocation = header.Get("Location")
	diagnosis.BodyBytes = len(body)
	diagnosis.LooksLikeLogin = looksLikeLogin(body)
	diagnosis.ScriptCount = strings.Count(text, "<script")
	diagnosis.PercentCount = strings.Count(text, "%")
	diagnosis.MoneyCount = strings.Count(text, "$") + strings.Count(text, "€") + strings.Count(text, "£")

	lowered := strings.ToLower(text)
	diagnosis.HasSessionWord = strings.Contains(lowered, "session")
	diagnosis.HasWeeklyWord = strings.Contains(lowered, "weekly")
	diagnosis.HasBalanceWord = strings.Contains(lowered, "balance")
	diagnosis.HasCreditWord = strings.Contains(lowered, "credit")
	diagnosis.HasQuotaWord = strings.Contains(lowered, "quota")

	diagnosis.Headings = diagnosisHeadings(text)
	diagnosis.InterestingLines = diagnosisInterestingLines(text)
	diagnosis.HrefPaths = diagnosisHrefPaths(text)
	return diagnosis, nil
}

// diagnosisHeadings lifts the page's section titles. They are redacted like every
// other string so a heading naming an account cannot leak it.
func diagnosisHeadings(document string) []string {
	matches := reDiagnosisHeading.FindAllStringSubmatch(document, diagnosisHeadingLimit)
	if len(matches) == 0 {
		return nil
	}
	headings := make([]string, 0, len(matches))
	for _, match := range matches {
		text := reDiagnosisTag.ReplaceAllString(match[1], " ")
		if heading := redactDiagnosisText(text); heading != "" {
			headings = append(headings, heading)
		}
	}
	if len(headings) == 0 {
		return nil
	}
	return headings
}

// diagnosisInterestingLines keeps only lines that look like quota, plan or reset
// copy. A page that renders its numbers in the browser keeps its labels here but
// loses the percentages, which is the distinction this diagnostic is for.
func diagnosisInterestingLines(document string) []string {
	lines := textLines(document)
	var interesting []string
	for _, line := range lines {
		if !reDiagnosisInteresting.MatchString(line) {
			continue
		}
		if redacted := redactDiagnosisText(line); redacted != "" {
			interesting = append(interesting, redacted)
		}
		if len(interesting) >= diagnosisLineLimit {
			break
		}
	}
	return interesting
}

// diagnosisHrefPaths lists same-origin paths the page links to, so a quota page
// that moved to another route shows up without reading any text.
func diagnosisHrefPaths(document string) []string {
	matches := reDiagnosisHref.FindAllStringSubmatch(document, -1)
	seen := make(map[string]bool, len(matches))
	var paths []string
	for _, match := range matches {
		raw := strings.TrimSpace(match[1])
		if !strings.HasPrefix(raw, "/") {
			continue
		}
		if index := strings.IndexAny(raw, "?#"); index >= 0 {
			raw = raw[:index]
		}
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		paths = append(paths, raw)
		if len(paths) >= diagnosisHrefLimit {
			break
		}
	}
	return paths
}

// redactDiagnosisText masks every digit run, every address and every run of
// whitespace, then bounds the result by rune count so a multi-byte character is
// never split.
func redactDiagnosisText(value string) string {
	masked := reDiagnosisAddress.ReplaceAllString(value, "<email>")
	masked = reDiagnosisDigits.ReplaceAllString(masked, "#")
	masked = strings.Join(strings.Fields(masked), " ")
	runes := []rune(masked)
	if len(runes) > diagnosisTextLimit {
		masked = string(runes[:diagnosisTextLimit])
	}
	return masked
}
