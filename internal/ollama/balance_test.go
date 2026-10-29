package ollama

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func balanceServer(t *testing.T, status int, body string, seen *string) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if seen != nil {
			*seen = request.Header.Get("Authorization")
		}
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return NewClient(5*time.Second, WithBalanceURL(server.URL))
}

// TestParseBalanceNumbersAndDecimalStrings pins that the balance endpoint's two
// observed spellings of a USD amount both map onto the shared credits shape, and
// that used/total derive from allowance minus remaining.
func TestParseBalanceNumbersAndDecimalStrings(t *testing.T) {
	for _, testCase := range []struct {
		name string
		body string
	}{
		{name: "json_numbers", body: `{"included":{"balance_usd":12.5,"allowance_usd":20,"period":{"from":"2026-10-01T00:00:00Z","until":"2026-11-01T00:00:00Z"}},"purchased":{"balance_usd":3}}`},
		{name: "decimal_strings", body: `{"included":{"balance_usd":"12.50","allowance_usd":"20.00"},"purchased":{"balance_usd":"3.00"}}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			credits, err := parseBalance([]byte(testCase.body))
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if credits.Source != "balance_api" {
				t.Fatalf("source = %q", credits.Source)
			}
			if credits.RemainingCents == nil || *credits.RemainingCents != 1250 {
				t.Fatalf("remaining = %v, want 1250", credits.RemainingCents)
			}
			if credits.UsedCents == nil || *credits.UsedCents != 750 {
				t.Fatalf("used = %v, want 750", credits.UsedCents)
			}
			if credits.TotalCents == nil || *credits.TotalCents != 2000 {
				t.Fatalf("total = %v, want 2000", credits.TotalCents)
			}
			if credits.UsedPercent == nil || *credits.UsedPercent != 37.5 {
				t.Fatalf("percent = %v, want 37.5", credits.UsedPercent)
			}
		})
	}
}

// TestParseBalancePurchasedIsSeparate pins that purchased.balance_usd is not folded
// into remaining/used/total.
func TestParseBalancePurchasedIsSeparate(t *testing.T) {
	credits, err := parseBalance([]byte(`{"included":{"balance_usd":1,"allowance_usd":2},"purchased":{"balance_usd":500}}`))
	if err != nil {
		t.Fatal(err)
	}
	if credits.RemainingCents == nil || *credits.RemainingCents != 100 {
		t.Fatalf("remaining = %v", credits.RemainingCents)
	}
	if credits.TotalCents == nil || *credits.TotalCents != 200 {
		t.Fatalf("total = %v, purchased must not be added", credits.TotalCents)
	}
}

// TestFetchBalanceUnauthorizedNamesAPIKeyPath pins that a 401 on the Bearer request
// surfaces as an auth failure whose message names the API-key path.
func TestFetchBalanceUnauthorizedNamesAPIKeyPath(t *testing.T) {
	client := balanceServer(t, http.StatusUnauthorized, `{"error":"unauthorized"}`, nil)
	snapshot, err := client.FetchBalance(context.Background(), "acct", "sk-secret")
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if snapshot.IsSuccess() || snapshot.Failure != FailureAuth {
		t.Fatalf("401 must be an auth failure: %+v", snapshot)
	}
	if !strings.Contains(snapshot.Err, "API key") {
		t.Fatalf("error must name the API-key path: %q", snapshot.Err)
	}
	if strings.Contains(snapshot.Err, "sk-secret") {
		t.Fatal("the API key leaked into the error")
	}
}

// TestFetchBalanceSendsBearerHeader pins the wire contract and that the key stays
// out of the returned value.
func TestFetchBalanceSendsBearerHeader(t *testing.T) {
	var authorization string
	client := balanceServer(t, http.StatusOK, `{"included":{"balance_usd":1,"allowance_usd":2}}`, &authorization)
	snapshot, err := client.FetchBalance(context.Background(), "acct", "sk-live-key")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if authorization != "Bearer sk-live-key" {
		t.Fatalf("Authorization = %q", authorization)
	}
	if !snapshot.IsSuccess() {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if strings.Contains(snapshot.Err, "sk-live-key") {
		t.Fatal("the API key leaked into the snapshot")
	}
}

// TestParseBalanceMalformedJSON pins a parse failure instead of a nil success.
func TestParseBalanceMalformedJSON(t *testing.T) {
	if _, err := parseBalance([]byte(`{"included": `)); err == nil {
		t.Fatal("malformed JSON must be rejected")
	}
}

// TestParseBalanceMissingIncludedLeavesUsedNil pins that an absent included section
// keeps used nil rather than reading it as zero, so a missing number is never a
// believable reading.
func TestParseBalanceMissingIncludedLeavesUsedNil(t *testing.T) {
	credits, err := parseBalance([]byte(`{"purchased":{"balance_usd":5}}`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if credits == nil || credits.RemainingCents != nil || credits.UsedCents != nil || credits.TotalCents != nil {
		t.Fatalf("missing included must stay absent: %+v", credits)
	}
}

// TestParseBalanceRejectsNegativeAndNonFiniteAmounts pins that an unusable figure is
// refused rather than coerced into a plausible one.
func TestParseBalanceRejectsNegativeAndNonFiniteAmounts(t *testing.T) {
	for _, body := range []string{
		`{"included":{"balance_usd":-1,"allowance_usd":2}}`,
		`{"included":{"balance_usd":1,"allowance_usd":-2}}`,
		`{"included":{"balance_usd":"NaN","allowance_usd":2}}`,
		`{"included":{"balance_usd":1,"allowance_usd":"Inf"}}`,
	} {
		if _, err := parseBalance([]byte(body)); err == nil {
			t.Fatalf("body %q must be rejected", body)
		}
	}
}

// TestFetchBalanceOverdrawnClampsUsedToZero pins the clamp: allowance below the
// remaining balance yields zero used, not a negative spend.
func TestFetchBalanceOverdrawnClampsUsedToZero(t *testing.T) {
	client := balanceServer(t, http.StatusOK, `{"included":{"balance_usd":10,"allowance_usd":4}}`, nil)
	snapshot, err := client.FetchBalance(context.Background(), "acct", "k")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Credits == nil || snapshot.Credits.UsedCents == nil || *snapshot.Credits.UsedCents != 0 {
		t.Fatalf("used must clamp to zero: %+v", snapshot.Credits)
	}
}
