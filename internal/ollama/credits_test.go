package ollama

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

func creditPage(remaining, used string) string {
	return `<html><body><nav>Upgrade for $999</nav><p>Max</p>
<section><h2>Usage credits</h2><div><div><span>` + remaining + `</span> remaining</div>
<div><span>` + used + `</span> used this month</div></div>
<p>Monthly credits refill <time>in 2 weeks</time></p><p>Auto-reload: Off</p></section>
<section><h3>Models used this month</h3><div><span>fake-model:1017</span> <span>15 requests</span></div></section>
<script>const balance = 12345;</script></body></html>`
}

func TestFetchUSDCredits(t *testing.T) {
	for _, testCase := range []struct {
		name                    string
		status                  int
		remaining, used         string
		wantFailure             Failure
		wantRemaining, wantUsed int64
	}{
		{name: "normal", status: 200, remaining: "$299.98", used: "$0.02", wantRemaining: 29998, wantUsed: 2},
		{name: "zero", status: 200, remaining: "$0.00", used: "$0.00"},
		{name: "fetch_failure", status: 503, wantFailure: FailureTransport},
		{name: "mixed_path", status: 200, remaining: "$299.98", used: "/usr/bin/bash.02", wantFailure: FailureParse},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := fixtureServer(t, testCase.status, creditPage(testCase.remaining, testCase.used))
			snapshot, err := client.Fetch(context.Background(), "synthetic", "fake-session")
			if err != nil || snapshot.Failure != testCase.wantFailure {
				t.Fatalf("failure=%q err=%v", snapshot.Failure, err)
			}
			if len(snapshot.Windows) != 0 {
				t.Fatal("USD credits must not become percentage windows")
			}
			if testCase.wantFailure != FailureNone {
				if snapshot.IsSuccess() {
					t.Fatal("failed scrape reported success")
				}
				if snapshot.Credits != nil && snapshot.Credits.UsedCents != nil {
					t.Fatal("damaged amount recovered as a number")
				}
				return
			}
			credits := snapshot.Credits
			if !snapshot.IsSuccess() || credits == nil {
				t.Fatalf("missing credits: %+v", snapshot)
			}
			if credits.RemainingCents == nil || *credits.RemainingCents != testCase.wantRemaining || credits.UsedCents == nil || *credits.UsedCents != testCase.wantUsed {
				t.Fatalf("wrong amounts: %+v", credits)
			}
			if credits.TotalCents == nil || *credits.TotalCents != testCase.wantRemaining+testCase.wantUsed {
				t.Fatal("wrong total")
			}
			if testCase.wantUsed == 0 {
				if credits.UsedPercent != nil {
					t.Fatal("zero denominator fabricated a percentage")
				}
			} else if credits.UsedPercent == nil || math.Abs(*credits.UsedPercent-2.0/30000*100) > 1e-10 {
				t.Fatal("wrong derived share")
			}
			if credits.RefillText != "Monthly credits refill in 2 weeks" || len(credits.Models) != 1 || credits.Models[0].Model != "fake-model:1017" || credits.Models[0].Requests != 15 {
				t.Fatalf("missing refill/model facts: %+v", credits)
			}
			encoded, err := json.Marshal(snapshot.ToQuota("Synthetic account"))
			if err != nil || strings.Contains(string(encoded), "reset_at_ms") || strings.Contains(string(encoded), "fake-session") {
				t.Fatalf("unsafe wire: %s, %v", encoded, err)
			}
		})
	}
}

func TestUSDRejectsDamagedAmounts(t *testing.T) {
	for _, value := range []string{"/usr/bin/bash.02", "/tmp/$0.02", "$0.02/path", "€0.02", "$-1", "NaN", "$1,2", "$0.001", "$1e2", "", "$999999999999999999999999999"} {
		if parseUSDCents(value) != nil {
			t.Errorf("accepted %q", value)
		}
	}
	for _, testCase := range []struct {
		text  string
		cents int64
	}{{"$0", 0}, {"USD 1,234.56", 123456}, {"$0.02", 2}} {
		if got := parseUSDCents(testCase.text); got == nil || *got != testCase.cents {
			t.Errorf("amount %q = %v", testCase.text, got)
		}
	}
}

func TestUSDCreditsNeverFallBackToLegacyWindows(t *testing.T) {
	page := creditPage("$299.98", "/usr/bin/bash.02") + `<section>Session usage 99% used</section>`
	result := parse(page, time.Now())
	if result.credits == nil || result.credits.UsedCents != nil || result.credits.UsedPercent != nil || len(result.windows) != 0 {
		t.Fatalf("fabricated fallback: %+v", result)
	}
}

func TestCollectorPersistsUSDCredits(t *testing.T) {
	client := fixtureServer(t, http.StatusOK, creditPage("$299.98", "$0.02"))
	collector, capture := newTestCollector(time.Now(), []StoredAccount{enabledAccount("synthetic")}, func(ctx context.Context, account Account, cookie string) (Snapshot, error) {
		return client.Fetch(ctx, account.ID, cookie)
	})
	if _, err := collector.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(capture.results) != 1 || !capture.results[0].IsSuccess {
		t.Fatalf("not persisted: %+v", capture.results)
	}
	var stored quota.NormalizedQuota
	if err := json.Unmarshal([]byte(capture.results[0].SnapshotJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.OllamaCredits == nil || *stored.OllamaCredits.UsedCents != 2 || len(stored.Windows) != 0 {
		t.Fatalf("wrong stored snapshot: %+v", stored)
	}
}

func TestUSDCreditsRefusesSiblingFacts(t *testing.T) {
	page := `<html><body><main><section><h2>Usage credits</h2></section>
 <section><p>$900 remaining</p><p>$10 used this month</p></section>
 <section><h3>Models used this month</h3></section>
 <section><p>unrelated-model 500 requests</p></section></main></body></html>`
	for _, markup := range []string{page, strings.ReplaceAll(strings.ReplaceAll(page, "section", "div"), "main", "div")} {
		credits := parseCredits(markup)
		if credits == nil || credits.RemainingCents != nil || credits.UsedCents != nil || len(credits.Models) != 0 {
			t.Fatalf("borrowed sibling facts: %+v", credits)
		}
	}
}

func TestUSDCreditsAcceptsHeadingBadgeAndMonthlyLabel(t *testing.T) {
	page := strings.Replace(creditPage("$99.98", "$0.02"), `<h2>Usage credits</h2>`, `<h2>Usage credits<span>Max</span></h2>`, 1)
	page = strings.Replace(page, `<span>$0.02</span> used this month`, `Monthly credits used <span>$0.02</span>`, 1)
	credits := parseCredits(page)
	if credits == nil || credits.RemainingCents == nil || *credits.RemainingCents != 9998 || credits.UsedCents == nil || *credits.UsedCents != 2 {
		t.Fatalf("missing labelled facts: %+v", credits)
	}
}
