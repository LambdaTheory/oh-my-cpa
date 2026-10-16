package ollama

import (
	"math"
	"testing"
	"time"
)

// TestParseSessionUsageLayout covers the settings page's current wording: the
// five-hour window is labelled "Session usage" and reset times arrive as inline
// machine-readable stamps. An earlier wording-only parser never found this window.
func TestParseSessionUsageLayout(t *testing.T) {
	now := time.Date(2026, 10, 8, 6, 0, 0, 0, time.UTC)
	page := `<html><body>
<h2>Cloud usage</h2><p>Pro</p>
<div><span>Session usage</span><span>42% used</span>
  <span>Resets <time datetime="2026-10-08T09:00:00Z">in 3 hours</time></span></div>
<div><span>Weekly usage</span><span>12.5% used</span>
  <span>Resets <local-time data-time="2026-10-12T00:00:00Z">Oct 12</local-time></span></div>
</body></html>`
	got := parse(page, now)
	if len(got.windows) != 2 {
		t.Fatalf("windows = %d, want 2: %+v", len(got.windows), got.windows)
	}
	want := map[string]struct {
		used  float64
		reset time.Time
	}{
		"5h": {42, time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)},
		"7d": {12.5, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)},
	}
	for _, window := range got.windows {
		expected, ok := want[window.Name]
		if !ok {
			t.Fatalf("unexpected window %q", window.Name)
		}
		if window.UsedPercent == nil || *window.UsedPercent != expected.used {
			t.Errorf("%s used = %v, want %v", window.Name, window.UsedPercent, expected.used)
		}
		if window.ResetAt == nil || !window.ResetAt.Equal(expected.reset) {
			t.Errorf("%s reset = %v, want %v", window.Name, window.ResetAt, expected.reset)
		}
		if window.ResetDerived {
			t.Errorf("%s reset came from a stamp and must not be derived", window.Name)
		}
		if window.Label == "" {
			t.Errorf("%s must carry a label: %+v", window.Name, window)
		}
	}
}

// TestParseRemainingIsConvertedToUsed pins that a page showing what is left is not
// read as usage.
func TestParseRemainingIsConvertedToUsed(t *testing.T) {
	got := parse(`<div><p>Session usage</p><p>70% remaining</p></div>`, time.Now())
	if len(got.windows) != 1 || got.windows[0].UsedPercent == nil || *got.windows[0].UsedPercent != 30 {
		t.Fatalf("remaining not converted to used: %+v", got.windows)
	}
}

func TestParseRelativeResetIsDerived(t *testing.T) {
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	got := parse(`<section><h3>Weekly usage</h3><span>88% used</span><p>Resets in 6 days</p></section>`, now)
	if len(got.windows) != 1 {
		t.Fatalf("windows = %+v", got.windows)
	}
	window := got.windows[0]
	if window.ResetAt == nil || !window.ResetAt.Equal(now.Add(6*24*time.Hour)) {
		t.Fatalf("relative reset = %v", window.ResetAt)
	}
	if !window.ResetDerived {
		t.Fatal("a relative reset must be marked derived")
	}
}

// TestParseResetOnlyIsNotAWindow pins that a reset hint alone never fabricates a
// window without a numeric reading.
func TestParseResetOnlyIsNotAWindow(t *testing.T) {
	got := parse(`<section><h3>Weekly usage</h3><p>Resets in 2 days</p></section>`, time.Now())
	if len(got.windows) != 0 {
		t.Fatalf("a reset hint must not become a window: %+v", got.windows)
	}
}

func TestParsePlanOnlyIsNotAWindow(t *testing.T) {
	got := parse(`<html><body><div>Plan: Pro</div><div>Credit balance: $5</div></body></html>`, time.Now())
	if len(got.windows) != 0 {
		t.Fatalf("plan and balance alone must not be a reading: %+v", got.windows)
	}
	if got.plan != "Pro" {
		t.Errorf("plan = %q", got.plan)
	}
	if got.balance != "$5" {
		t.Errorf("balance = %q", got.balance)
	}
}

func TestParseMissingFields(t *testing.T) {
	got := parse(`<html><body><div>Nothing here at all</div></body></html>`, time.Now())
	if len(got.windows) != 0 || got.plan != "" || got.balance != "" {
		t.Fatalf("unexpected parse: %+v", got)
	}
}

func TestParseBalanceRequiresExplicitMonetaryField(t *testing.T) {
	cases := []struct {
		page string
		want string
	}{
		{`<div>Session usage 25% used</div><div>Legacy subscription $0/month</div>`, ""},
		{`<div>Weekly usage 40% used</div><div>Buy credits for $20</div>`, ""},
		{`<div>Credit reset: 0</div><div>Upgrade $99</div>`, ""},
		{`<div>Credit balance: $0</div>`, "$0"},
		{`<div>Account balance: €12.50</div>`, "€12.50"},
		{`<div>Credit balance: 0</div>`, "0"},
		{`<div>Credit balance</div><div>$0</div>`, "$0"},
		{`<div>Credit balance</div><div>Legacy subscription $0/month</div>`, ""},
	}
	for _, testCase := range cases {
		got := parse(testCase.page, time.Unix(0, 0))
		if got.balance != testCase.want {
			t.Errorf("parse(%q).balance = %q, want %q", testCase.page, got.balance, testCase.want)
		}
		normalized := (Snapshot{Balance: got.balance}).ToQuota("Synthetic account")
		if testCase.want == "" && normalized.Plan != nil && normalized.Plan.Credits != nil {
			t.Errorf("missing balance acquired credits: %+v", normalized.Plan)
		}
		if testCase.want != "" && normalized.RawSignals["ollama_balance_source"] != "settings_balance" {
			t.Errorf("explicit balance lost provenance: %+v", normalized.RawSignals)
		}
	}
}

// TestParsePercentAcceptsOnlyTheRealRange pins that 0 and 100 are legitimate
// readings while a value past the range or a non-finite value is refused instead of
// being clamped to a fabricated 0 or 100.
func TestParsePercentAcceptsOnlyTheRealRange(t *testing.T) {
	cases := []struct {
		name  string
		value float64
		want  float64
		ok    bool
	}{
		{"zero", 0, 0, true},
		{"full", 100, 100, true},
		{"midpoint", 42.5, 42.5, true},
		{"above range", 101, 0, false},
		{"negative", -1, 0, false},
		{"nan", math.NaN(), 0, false},
		{"positive infinity", math.Inf(1), 0, false},
		{"negative infinity", math.Inf(-1), 0, false},
	}
	for _, testCase := range cases {
		got, ok := parsePercent(testCase.value)
		if ok != testCase.ok || (ok && got != testCase.want) {
			t.Errorf("%s: parsePercent(%v) = %v,%v want %v,%v",
				testCase.name, testCase.value, got, ok, testCase.want, testCase.ok)
		}
	}
}

// TestParseRefusesFabricatedPercentages pins that a page stating an impossible
// percentage or no percentage at all yields no window, never a clamp to 100 or 1.
func TestParseRefusesFabricatedPercentages(t *testing.T) {
	cases := map[string]string{
		"above range":   `<div><p>Session usage</p><p>101% used</p></div>`,
		"negative used": `<div><p>Session usage</p><p>-1% used</p></div>`,
		"malformed":     `<div><p>Session usage</p><p>not a number</p></div>`,
	}
	for name, page := range cases {
		got := parse(page, time.Now())
		if len(got.windows) != 0 {
			t.Errorf("%s: a refused percentage must not become a window: %+v", name, got.windows)
		}
	}
}

// TestParseRemainingBoundaries pins that the remaining-to-used conversion stays
// valid at both ends of the range.
func TestParseRemainingBoundaries(t *testing.T) {
	cases := map[string]float64{
		"0% remaining":   100,
		"100% remaining": 0,
		"25% remaining":  75,
	}
	for page, want := range cases {
		got := parse(`<div><p>Session usage</p><p>`+page+`</p></div>`, time.Now())
		if len(got.windows) != 1 || got.windows[0].UsedPercent == nil || *got.windows[0].UsedPercent != want {
			t.Errorf("%s: got %+v, want used %v", page, got.windows, want)
		}
	}
}
