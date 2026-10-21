package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestDashboardPredictionsGroupsStoredOperatorRowsWithoutCallingCPA(t *testing.T) {
	var managementCalls atomic.Int64
	cpaServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		managementCalls.Add(1)
		http.Error(writer, "unexpected management call", http.StatusServiceUnavailable)
	}))
	defer cpaServer.Close()

	handler := testHandler(t, "")
	now := time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	handler.nowFn = func() time.Time { return now }
	ciphertext, nonce, err := handler.cipher.Encrypt([]byte("management-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{
		ID: defaultInstanceID(), Name: "CPA", BaseURL: cpaServer.URL,
		ManagementKeyCiphertext: ciphertext, ManagementKeyNonce: nonce,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	resources := []domain.DiscoveredResource{
		{ResourceKey: "auth-index:claude:operator", CPAResourceType: "auth-file", CPAAuthIndex: "claude-operator", CPAResourceName: "operator@example.com", CPADriver: "claude", Status: domain.ResourceStatusUnclaimed},
		{ResourceKey: "auth-index:codex:second", CPAResourceType: "auth-file", CPAAuthIndex: "codex-second", CPAResourceName: "second@example.com", CPADriver: "codex", Status: domain.ResourceStatusUnclaimed},
	}
	if _, err := handler.repo.UpsertDiscoveredResources(context.Background(), defaultInstanceID(), resources, now, false); err != nil {
		t.Fatal(err)
	}
	seedDashboardPredictionSnapshot(t, handler.repo, "claude-operator", "claude", now)
	seedDashboardPredictionSnapshot(t, handler.repo, "codex-second", "codex", now)

	recorder := httptest.NewRecorder()
	handler.dashboardPredictions(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/management/dashboard/predictions", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body dashboardPredictionsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if managementCalls.Load() != 0 {
		t.Fatalf("dashboard prediction made %d CPA calls", managementCalls.Load())
	}
	if body.GeneratedAtMS != now.UnixMilli() || body.DurationMS < 0 || body.Omitted != 0 || len(body.Providers) != 2 {
		t.Fatalf("prediction response = %+v", body)
	}
	if body.Providers[0].Provider != "claude" || body.Providers[1].Provider != "codex" {
		t.Fatalf("provider groups = %+v", body.Providers)
	}
	item := body.Providers[0].Items[0]
	if item.CredentialLabel != "operator@example.com" || item.AuthIndex != "claude-operator" {
		t.Fatalf("operator identity was masked: %+v", item)
	}
	if item.Verdict != quota.PredictionVerdictUnknown || len(item.Reasons) == 0 {
		t.Fatalf("unknown row = %+v", item)
	}
}

func TestDashboardPredictionsReturnsExplicitEmptyShape(t *testing.T) {
	handler := testHandler(t, "")
	recorder := httptest.NewRecorder()
	handler.dashboardPredictions(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/management/dashboard/predictions", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var body dashboardPredictionsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Providers == nil || len(body.Providers) != 0 || body.Omitted != 0 {
		t.Fatalf("empty response = %+v", body)
	}
}

func seedDashboardPredictionSnapshot(t *testing.T, repo *repository.Repository, authIndex, provider string, observedAt time.Time) {
	t.Helper()
	used := 25.0
	windowsJSON, err := json.Marshal([]quota.QuotaWindow{{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used}})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{
		AuthIndex: authIndex, Provider: provider, Status: "healthy", WindowsJSON: string(windowsJSON), ObservedAtMS: observedAt.UnixMilli(),
	}); err != nil {
		t.Fatal(err)
	}
}
