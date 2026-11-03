package operations

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

func TestSafeQuotaIncludesSubscriptionWithoutRawSignals(t *testing.T) {
	isActive := false
	value := &quota.NormalizedQuota{AuthIndex: "fixture-meta", Provider: "meta", Plan: &quota.QuotaPlan{PlanType: "meta", PlanLabel: "Muse Pro", Tier: "unknown", IsSubscriptionActive: &isActive}, RawSignals: map[string]string{"dca_token": "synthetic-private-token"}}
	safe := SafeQuota(value)
	if safe.Plan == nil || safe.Plan.PlanLabel != "Muse Pro" || safe.Plan.IsSubscriptionActive == nil || *safe.Plan.IsSubscriptionActive {
		t.Fatalf("subscription missing: %+v", safe)
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "synthetic-private-token") || strings.Contains(string(encoded), "raw_signals") {
		t.Fatalf("unsafe agent result: %s", encoded)
	}
	if SafeQuota(&quota.NormalizedQuota{}).Plan != nil {
		t.Fatal("fabricated plan for unobserved credential")
	}
}

func TestSafeQuotaCarriesMonetaryBalanceOnlyInternally(t *testing.T) {
	cents := int64(4114)
	cases := []struct {
		name   string
		value  quota.NormalizedQuota
		amount string
	}{
		{name: "ollama", value: quota.NormalizedQuota{OllamaCredits: &quota.OllamaCredits{RemainingCents: &cents}}, amount: "41.14"},
		{name: "codex precision", value: quota.NormalizedQuota{Plan: &quota.QuotaPlan{Credits: &quota.QuotaCredits{Balance: "106.118123456789012345"}}}, amount: "106.118123456789012345"},
		{name: "zero", value: quota.NormalizedQuota{Plan: &quota.QuotaPlan{Credits: &quota.QuotaCredits{Balance: "0"}}}, amount: "0"},
		{name: "unlimited", value: quota.NormalizedQuota{Plan: &quota.QuotaPlan{Credits: &quota.QuotaCredits{IsUnlimited: true, Balance: "100"}}}},
		{name: "no balance", value: quota.NormalizedQuota{}},
		{name: "window only", value: quota.NormalizedQuota{Windows: []quota.QuotaWindow{{RemainingPercent: advicePercent(80)}}}},
		{name: "invalid", value: quota.NormalizedQuota{Plan: &quota.QuotaPlan{Credits: &quota.QuotaCredits{Balance: "1e3"}}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			safe := SafeQuota(&test.value)
			if test.amount == "" {
				if safe.Balance != nil {
					t.Fatalf("invented balance: %+v", safe.Balance)
				}
			} else if safe.Balance == nil || safe.Balance.Amount != test.amount || safe.Balance.Currency != "USD" {
				t.Fatalf("balance = %+v, want %s USD", safe.Balance, test.amount)
			}
			encoded, err := json.Marshal(safe)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "balance") {
				t.Fatalf("quota_list response changed: %s", encoded)
			}
		})
	}
}
