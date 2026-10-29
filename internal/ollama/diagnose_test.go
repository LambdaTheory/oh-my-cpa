package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newDiagnosisFixture(t *testing.T, status int, body string, header map[string]string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		for name, value := range header {
			writer.Header().Set(name, value)
		}
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewClient(5*time.Second, WithSettingsURL(server.URL))
}

// A client-rendered shell is the case that looks like a successful fetch: the
// page has labels but no numbers, so the parser reports a parse failure while the
// session is still valid. The diagnosis must show ScriptCount rising and
// PercentCount at zero.
func TestDiagnoseReportsClientRenderedShell(t *testing.T) {
	client := newDiagnosisFixture(t, http.StatusOK, `<html><body><div id="root"></div>`+
		`<script src="/assets/app.js"></script></body></html>`, map[string]string{"Content-Type": "text/html"})

	diagnosis, err := client.Diagnose(context.Background(), "session=value")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if diagnosis.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", diagnosis.Status)
	}
	if diagnosis.ScriptCount != 1 {
		t.Errorf("script count = %d, want 1", diagnosis.ScriptCount)
	}
	if diagnosis.PercentCount != 0 || diagnosis.MoneyCount != 0 {
		t.Errorf("counts = %d%% / %d money, want 0/0", diagnosis.PercentCount, diagnosis.MoneyCount)
	}
	if diagnosis.LooksLikeLogin {
		t.Error("a shell was classified as a login page")
	}
	if len(diagnosis.InterestingLines) != 0 {
		t.Errorf("interesting lines = %v, want none", diagnosis.InterestingLines)
	}
}

// A page that kept its labels but moved the numbers to another route is the other
// plausible cause. The link list is what distinguishes it from a redesign in place.
func TestDiagnoseReportsMovedQuotaRoute(t *testing.T) {
	body := `<html><body><nav><a href="/settings/usage?tab=plan">Usage</a>` +
		`<a href="/settings/usage">Usage again</a></nav>` +
		`<h2>Account settings</h2><p>Plan: Team</p></body></html>`
	client := newDiagnosisFixture(t, http.StatusOK, body, map[string]string{"Content-Type": "text/html"})

	diagnosis, err := client.Diagnose(context.Background(), "session=value")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(diagnosis.HrefPaths) != 1 || diagnosis.HrefPaths[0] != "/settings/usage" {
		t.Errorf("href paths = %v, want one deduplicated /settings/usage", diagnosis.HrefPaths)
	}
	if !diagnosis.HasQuotaWord && !diagnosis.HasBalanceWord && !diagnosis.HasCreditWord {
		// The fixture carries none of those words; the assertion documents that the
		// booleans are about the page, not about the parser.
		t.Log("fixture carries no quota/balance/credit word")
	}
	if len(diagnosis.Headings) != 1 || !strings.Contains(diagnosis.Headings[0], "Account settings") {
		t.Errorf("headings = %v, want the section title", diagnosis.Headings)
	}
	if len(diagnosis.InterestingLines) == 0 {
		t.Error("the plan line should be reported as interesting")
	}
}

// A redirect must be surfaced as a redirect, because a 3xx is how an expired
// session is detected. Reading it as a parse failure would send the operator
// looking for a redesign that never happened.
func TestDiagnoseReportsRedirect(t *testing.T) {
	client := newDiagnosisFixture(t, http.StatusSeeOther, "", map[string]string{
		"Location": "https://api.workos.com/user_management/authorize",
	})

	diagnosis, err := client.Diagnose(context.Background(), "session=value")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if diagnosis.Status != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", diagnosis.Status)
	}
	if diagnosis.RedirectLocation != "https://api.workos.com/user_management/authorize" {
		t.Errorf("redirect = %q", diagnosis.RedirectLocation)
	}
}

func TestDiagnoseReportsLoginPage(t *testing.T) {
	client := newDiagnosisFixture(t, http.StatusOK, `<html><body><form><input type="password"></form></body></html>`, nil)

	diagnosis, err := client.Diagnose(context.Background(), "session=value")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if !diagnosis.LooksLikeLogin {
		t.Error("a login form was not classified as a login page")
	}
}

// The diagnosis is what a maintainer reads in a browser tab, so it must not carry
// the account identity or a usable number even when the page is full of both.
func TestDiagnoseRedactsNumbersAndAddresses(t *testing.T) {
	body := `<html><body><div>Weekly usage for someone@example.com is 43.5% and resets in 3h.</div>` +
		`</body></html>`
	client := newDiagnosisFixture(t, http.StatusOK, body, nil)

	diagnosis, err := client.Diagnose(context.Background(), "session=value")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(diagnosis.InterestingLines) != 1 {
		t.Fatalf("interesting lines = %v, want one", diagnosis.InterestingLines)
	}
	line := diagnosis.InterestingLines[0]
	if strings.ContainsAny(line, "0123456789") {
		t.Errorf("line kept digits: %q", line)
	}
	if strings.Contains(line, "someone@example.com") {
		t.Errorf("line kept the address: %q", line)
	}
	if !strings.Contains(line, "<email>") {
		t.Errorf("line lost the address marker: %q", line)
	}
	if !strings.Contains(line, "#") {
		t.Errorf("line lost the number marker: %q", line)
	}
}

// The caps keep a pathological page from turning the response into a copy of it.
func TestDiagnoseCapsSampleLines(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("<html><body>")
	for index := 0; index < 80; index++ {
		builder.WriteString("<div>Weekly usage 12% remaining</div>")
	}
	builder.WriteString("</body></html>")
	client := newDiagnosisFixture(t, http.StatusOK, builder.String(), nil)

	diagnosis, err := client.Diagnose(context.Background(), "session=value")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if len(diagnosis.InterestingLines) > diagnosisLineLimit {
		t.Errorf("interesting lines = %d, want at most %d", len(diagnosis.InterestingLines), diagnosisLineLimit)
	}
	for _, line := range diagnosis.InterestingLines {
		if len([]rune(line)) > diagnosisTextLimit {
			t.Fatalf("line length = %d runes, want at most %d", len([]rune(line)), diagnosisTextLimit)
		}
	}
}

// An empty cookie is refused before any request is made, so the diagnostic can
// never be used to probe the upstream origin without stored credentials.
func TestDiagnoseWithoutCookieMakesNoRequest(t *testing.T) {
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requested = true
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	client := NewClient(5*time.Second, WithSettingsURL(server.URL))

	diagnosis, err := client.Diagnose(context.Background(), "   ")
	if err != nil {
		t.Fatalf("Diagnose() error = %v", err)
	}
	if requested {
		t.Error("an empty cookie still reached the network")
	}
	if diagnosis.Status != 0 {
		t.Errorf("status = %d, want 0", diagnosis.Status)
	}
}
