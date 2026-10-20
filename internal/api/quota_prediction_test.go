package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/operations"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestQuotaPredictionUsesOnlyStoredEvidence(t *testing.T) {
	var managementCalls atomic.Int64
	cpaServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		managementCalls.Add(1)
		http.Error(writer, "CPA unavailable", http.StatusServiceUnavailable)
	}))
	defer cpaServer.Close()

	handler := testHandler(t, "")
	ciphertext, nonce, err := handler.cipher.Encrypt([]byte("management-secret"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{
		ID: defaultInstanceID(), Name: "CPA", BaseURL: cpaServer.URL,
		ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.repo.UpsertDiscoveredResources(context.Background(), defaultInstanceID(), []domain.DiscoveredResource{{
		ResourceKey: "auth-index:auth-file:stored", CPAResourceType: "auth-file", CPAAuthIndex: "stored",
		CPAResourceName: "stored@example.com", CPADriver: "claude", Status: domain.ResourceStatusUnclaimed,
	}}, now, false); err != nil {
		t.Fatal(err)
	}
	reset := now.Add(4 * time.Hour).UnixMilli()
	period, first, current := 10.0, 10.0, 60.0
	window := quota.QuotaWindow{ID: "seven_day", Scope: "standard", UsedPercent: &first, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
	if _, err := handler.repo.RecordActiveQuotaObservations(context.Background(), &quota.NormalizedQuota{AuthIndex: "stored", Provider: "claude", Status: "healthy", ObservedAtMS: now.Add(-5 * time.Hour).UnixMilli(), Windows: []quota.QuotaWindow{window}}); err != nil {
		t.Fatal(err)
	}
	window.UsedPercent = &current
	if _, err := handler.repo.RecordActiveQuotaObservations(context.Background(), &quota.NormalizedQuota{AuthIndex: "stored", Provider: "claude", Status: "healthy", ObservedAtMS: now.UnixMilli(), Windows: []quota.QuotaWindow{window}}); err != nil {
		t.Fatal(err)
	}
	windowsJSON, err := json.Marshal([]quota.QuotaWindow{window})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		AuthIndex: "stored", Provider: "claude", Status: "healthy", WindowsJSON: string(windowsJSON), ObservedAtMS: now.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}

	result, err := handler.operationsService().QuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()), operations.QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if managementCalls.Load() != 0 {
		t.Fatalf("prediction made %d CPA management calls", managementCalls.Load())
	}
	if len(result.Items) != 1 || result.Items[0].Verdict == quota.PredictionVerdictUnknown {
		t.Fatalf("stored prediction = %+v", result)
	}
}
