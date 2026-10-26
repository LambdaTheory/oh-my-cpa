package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/operations"
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

func TestDashboardPredictionAccountingPreservesCapabilityWireContract(t *testing.T) {
	handler := testHandler(t, "")
	now := time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC)
	handler.nowFn = func() time.Time { return now }
	if err := handler.repo.UpsertInstance(context.Background(), domain.CPAInstance{ID: defaultInstanceID(), Name: "Fixture", BaseURL: "http://fixture.invalid", ManagementKeyCiphertext: []byte("fake"), ManagementKeyNonce: []byte("fake"), CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.repo.UpsertDiscoveredResources(context.Background(), defaultInstanceID(), []domain.DiscoveredResource{
		{ResourceKey: "auth-index:codex:fixture", CPAResourceType: "auth-file", CPAAuthIndex: "fixture", CPAResourceName: "fixture@example.test", CPADriver: "codex", Status: domain.ResourceStatusUnclaimed},
	}, now, false); err != nil {
		t.Fatal(err)
	}
	period, used, reset := 10.0, 30.0, now.Add(5*time.Hour).UnixMilli()
	windows, err := json.Marshal([]quota.QuotaWindow{{ID: "window", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: "fixture", Provider: "codex", Status: "healthy", WindowsJSON: string(windows), ObservedAtMS: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	result, err := handler.operationsService().QuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()), operations.QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var capabilityFields map[string]any
	if err := json.Unmarshal(encoded, &capabilityFields); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.dashboardPredictions(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/management/dashboard/predictions", nil))
	var dashboard struct {
		Providers []struct {
			Items []map[string]any `json:"items"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &dashboard); err != nil {
		t.Fatal(err)
	}
	fields := dashboard.Providers[0].Items[0]
	for key, want := range map[string]float64{"window_start_ms": float64(now.Add(-5 * time.Hour).UnixMilli()), "remaining_percent": 70, "uniform_projected_percent_at_reset": 60} {
		if fields[key] != want {
			t.Fatalf("%s = %v, want %v", key, fields[key], want)
		}
		if _, exists := capabilityFields[key]; exists {
			t.Fatalf("console accounting leaked into quota_prediction: %s", key)
		}
		delete(fields, key)
	}
	if !reflect.DeepEqual(fields, capabilityFields) {
		t.Fatalf("dashboard/capability facts differ: dashboard=%v capability=%v", fields, capabilityFields)
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
