package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/pricing"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

// The JSON log is consumed by the repository regression to run the real HTTP
// DTO through the browser's TPS function, without recreating the DTO in JS.
func TestLegacyTotalEvidenceThroughDTO(t *testing.T) {
	client, baseURL, repo := startDashboardTestServer(t, nil)
	ctx := context.Background()
	if err := repo.UpsertModelPrices(ctx, []pricing.ModelPrice{{Model: "tps-evidence", PromptPricePer1M: 1, CompletionPer1M: 2, PriceMultiplier: 1, Source: pricing.SourceManual}}); err != nil {
		t.Fatal(err)
	}
	events := make([]usage.Event, 0)
	for _, tc := range []struct {
		name, total string
		reasoning   int64
	}{
		{"missing", "", 20}, {"zero", `,"total_tokens":0`, 20},
		{"inclusive", `,"total_tokens":120`, 20}, {"separate", `,"total_tokens":140`, 20},
		{"historical", "", 20}, {"without-reasoning", "", 0},
	} {
		raw := fmt.Sprintf(`{"request_id":%q,"model":"tps-evidence","generate":true,"latency_ms":2000,"ttft_ms":1000,"tokens":{"input_tokens":20,"output_tokens":100,"reasoning_tokens":%d%s}}`, tc.name, tc.reasoning, tc.total)
		event, err := usage.DecodeEvent(raw, "default", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if tc.name == "historical" {
			event.Accounting = nil
		}
		events = append(events, event)
	}
	if _, err := repo.InsertUsageEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get(baseURL + "/omc/api/v1/usage/events?limit=100")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", response.StatusCode)
	}
	var page struct {
		Items []usageEventResponse `json:"items"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != len(events) {
		t.Fatalf("items: %d", len(page.Items))
	}
	for _, item := range page.Items {
		if item.CostUSD == nil || math.Abs(*item.CostUSD-0.00022) > 1e-12 {
			t.Fatalf("legacy cost changed: %+v", item)
		}
		if item.AccountingStatus != "legacy" {
			t.Fatalf("legacy status: %+v", item)
		}
		if item.RequestID == "historical" {
			if item.Accounting != nil || item.Tokens.Total != 120 {
				t.Fatalf("historical evidence was fabricated: %+v", item)
			}
			continue
		}
		if item.Accounting == nil {
			t.Fatalf("raw evidence lost: %+v", item)
		}
		wantPresent := item.RequestID == "zero" || item.RequestID == "inclusive" || item.RequestID == "separate"
		var wantRaw int64
		if item.RequestID == "inclusive" {
			wantRaw = 120
		}
		if item.RequestID == "separate" {
			wantRaw = 140
		}
		if item.Accounting.RawTokens.TotalTokensPresent != wantPresent || item.Accounting.RawTokens.TotalTokens != wantRaw {
			t.Fatalf("total presence/value changed: %+v", item.Accounting.RawTokens)
		}
	}
	encoded, err := json.Marshal(page.Items)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("TPS_DTO %s", encoded)
}
