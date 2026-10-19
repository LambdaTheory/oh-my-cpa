package operations

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func TestQuotaPredictionMasksReadOnlyIdentityAndBoundsRows(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	now := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	reset := now.Add(4 * time.Hour).UnixMilli()
	period := 10.0
	used := 60.0
	service := &Service{Repo: repo, Quotas: func(context.Context) ([]Quota, error) {
		items := make([]Quota, 0, 120)
		for index := 0; index < 120; index++ {
			items = append(items, Quota{AuthIndex: "auth-" + strings.Repeat("x", index%20), Name: "vincent@example.com", Provider: "codex", Status: "healthy", ObservedAtMS: now.UnixMilli(), Windows: []quota.QuotaWindow{{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}}})
		}
		return items, nil
	}}
	result, err := service.QuotaPrediction(capability.WithReadOnly(capability.WithAnchor(context.Background(), now.UnixMilli()), true), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) > 100 || len(result.Items)+result.Omitted != 120 || result.Omitted == 0 {
		t.Fatalf("bounded result = %d omitted %d", len(result.Items), result.Omitted)
	}
	for _, item := range result.Items {
		if item.AuthIndex != "" || item.CredentialLabel != "vin***@example.com" {
			t.Fatalf("read-only identity leaked: %+v", item)
		}
	}
	encoded, _ := json.Marshal(result)
	if len(encoded) > quotaPredictionByteLimit {
		t.Fatalf("result bytes = %d", len(encoded))
	}
}

func TestQuotaPredictionRejectsReadOnlyAuthIndex(t *testing.T) {
	database, openErr := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	service := &Service{Repo: repository.New(database), Quotas: func(context.Context) ([]Quota, error) { return nil, nil }}
	_, err := service.QuotaPrediction(capability.WithReadOnly(context.Background(), true), QuotaPredictionInput{AuthIndex: "private"})
	if err == nil || err.Error() != "invalid_parameters" {
		t.Fatalf("error = %v", err)
	}
}

func TestQuotaPredictionRegistersForAgentAndReadOnlyMCP(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-adapters.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	now := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	reset := now.Add(4 * time.Hour).UnixMilli()
	period, used := 10.0, 25.0
	service := &Service{Repo: repo, Quotas: func(context.Context) ([]Quota, error) {
		return []Quota{{AuthIndex: "auth-1", Name: "vincent@example.com", Provider: "codex", Status: "healthy", ObservedAtMS: now.UnixMilli(), Windows: []quota.QuotaWindow{{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}}}}, nil
	}}
	registry := capability.NewRegistry()
	if err := service.registerQuotaPrediction(registry); err != nil {
		t.Fatal(err)
	}
	executor := &capability.Executor{Registry: registry, Store: repository.AgentStore{Repo: repo}, Authorize: func(context.Context, string, string) bool { return true }}
	for _, principal := range []capability.Principal{
		{ID: "agent", Adapter: "agent", IsAdmin: true},
		{ID: "readonly", Adapter: "mcp", IsReadOnly: true, Allowed: map[string]bool{"quota_prediction": true}},
	} {
		result, err := executor.Invoke(capability.WithAnchor(context.Background(), now.UnixMilli()), principal, "quota_prediction", json.RawMessage(`{}`), "")
		if err != nil || result.Status != "success" {
			t.Fatalf("%s result=%+v err=%v", principal.Adapter, result, err)
		}
		var output QuotaPredictionResult
		if err := json.Unmarshal(result.Data, &output); err != nil || len(output.Items) != 1 {
			t.Fatalf("%s output=%+v err=%v", principal.Adapter, output, err)
		}
		item := output.Items[0]
		if principal.IsReadOnly && (item.AuthIndex != "" || item.CredentialLabel != "vin***@example.com") {
			t.Fatalf("read-only output = %+v", item)
		}
		if !principal.IsReadOnly && (item.AuthIndex != "auth-1" || item.CredentialLabel != "vincent@example.com") {
			t.Fatalf("agent output = %+v", item)
		}
	}
}
