package api

import (
	"context"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

func TestStepFunBalanceSnapshotRoundTrip(t *testing.T) {
	handler := capacityTestHandler(t)
	remaining := 75.0
	reading := &quota.NormalizedQuota{
		AuthIndex: "synthetic-step", Provider: "stepfun", Status: "healthy", ObservedAtMS: time.Now().UnixMilli(),
		Plan: &quota.QuotaPlan{PlanType: "prepaid", Tier: "unknown", AccountBalance: &quota.QuotaAccountBalance{
			AccountType: "prepaid", Currency: "CNY", Balance: 75, RemainingPercent: &remaining,
		}},
	}
	if err := handler.persistNormalizedQuotaSnapshot(context.Background(), reading); err != nil {
		t.Fatal(err)
	}
	restored := handler.loadPriorNormalizedQuota(context.Background(), reading.AuthIndex)
	if restored == nil || restored.Plan == nil || restored.Plan.AccountBalance == nil {
		t.Fatal("account balance lost after snapshot reload")
	}
	if restored.Plan.AccountBalance.Balance != 75 || restored.Plan.AccountBalance.RemainingPercent == nil || *restored.Plan.AccountBalance.RemainingPercent != 75 || len(restored.Windows) != 0 {
		t.Fatalf("incorrect restored balance: %+v", restored)
	}
	quota.EvaluateStatusAndRecommendation(restored, reading.ObservedAtMS)
	if restored.Status != "healthy" {
		t.Fatalf("balance-only reading became %s", restored.Status)
	}
}
