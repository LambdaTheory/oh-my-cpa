package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/pricing"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

func TestManagementRepoKeyRegressionReviewShowsApprovedFingerprint(t *testing.T) {
	fixture := newProviderTestFixture(t)
	ctx := context.Background()
	service := fixture.handler.operationsService()
	first, err := service.ProxyRepoKeyApplication(ctx, "org/fingerprint-review", "synthetic use", repoKeyPublicKey(t), "", "agent", "operator")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.ApproveRepoKeyApplication(ctx, first.ApplicationID, "operator", "")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := service.ProxyRepoKeyApplication(ctx, "org/fingerprint-review", "replace public key", repoKeyPublicKey(t), "", "agent", "operator")
	if err != nil {
		t.Fatal(err)
	}
	response, payload := getJSON(t, fixture.client, fixture.baseURL+"/omc/api/v1/management/repo-keys/applications/"+changed.ApplicationID+"/review")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("review status=%d", response.StatusCode)
	}
	var review RepoKeyReviewDTO
	if err := json.Unmarshal(payload, &review); err != nil {
		t.Fatal(err)
	}
	if !review.PublicKeyChanged || review.PreviousPublicKeyFingerprint != approved.PublicKeyFingerprint {
		t.Fatalf("changed key was not compared with the approved fingerprint: changed=%t previous_matches=%t", review.PublicKeyChanged, review.PreviousPublicKeyFingerprint == approved.PublicKeyFingerprint)
	}
}

func TestManagementRepoKeyRegressionListReportsSevenDayCost(t *testing.T) {
	fixture := newProviderTestFixture(t)
	ctx := context.Background()
	service := fixture.handler.operationsService()
	applied, err := service.ProxyRepoKeyApplication(ctx, "org/cost-list", "synthetic use", repoKeyPublicKey(t), "", "agent", "operator")
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.ApproveRepoKeyApplication(ctx, applied.ApplicationID, "operator", "")
	if err != nil {
		t.Fatal(err)
	}
	repo := fixture.handler.repo
	prices := []pricing.ModelPrice{{Model: "synthetic-repo-cost", PromptPricePer1M: 2, PriceMultiplier: 1, Source: pricing.SourceManual}}
	if err := repo.UpsertModelPrices(ctx, prices); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := repo.SeedModelPriceHistoryBackfill(ctx, prices, now.Add(-9*24*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var events []usage.Event
	for i, at := range []time.Time{now.Add(-time.Hour), now.Add(-72 * time.Hour), now.Add(-8 * 24 * time.Hour)} {
		events = append(events, usage.Event{InstanceID: "default", EventKey: []string{"repo-cost-recent", "repo-cost-three-days", "repo-cost-old"}[i],
			APIGroupKey: approved.KeyFingerprint, APIGroupLabel: "api_key", Model: "synthetic-repo-cost",
			TimestampMS: at.UnixMilli(), Generate: true, InputTokens: 1_000_000, TotalTokens: 1_000_000})
	}
	if _, err := repo.InsertUsageEvents(ctx, events); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "?window=24h", "?window=30d"} {
		response, payload := getJSON(t, fixture.client, fixture.baseURL+"/omc/api/v1/management/repo-keys"+suffix)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("list status=%d", response.StatusCode)
		}
		var listed struct {
			Keys []RepoKeyBindingDTO `json:"keys"`
		}
		if err := json.Unmarshal(payload, &listed); err != nil || len(listed.Keys) != 1 {
			t.Fatalf("could not read key list: %v", err)
		}
		got := listed.Keys[0].Usage7D
		if got.Window != "7d" || got.Requests != 2 || got.CostNanos != 4_000_000_000 {
			t.Errorf("%q: seven-day window=%s requests=%d cost_nanos=%d, want 7d/2/4000000000", suffix, got.Window, got.Requests, got.CostNanos)
		}
	}
	response, payload := getJSON(t, fixture.client, fixture.baseURL+"/omc/api/v1/management/repo-keys/"+approved.BindingID)
	var detail struct {
		Usage RepoKeyUsageDTO `json:"usage"`
	}
	if response.StatusCode != http.StatusOK || json.Unmarshal(payload, &detail) != nil || detail.Usage.CostNanos != 4_000_000_000 {
		t.Fatal("list and detail do not share the stored seven-day cost")
	}
}
