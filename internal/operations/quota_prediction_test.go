package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
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
	resources := make([]domain.DiscoveredResource, 0, 120)
	for index := 0; index < 120; index++ {
		authIndex := fmt.Sprintf("auth-%03d", index)
		resources = append(resources, predictionResource(authIndex, "vincent@example.com", "codex"))
		seedPredictionSnapshot(t, repo, authIndex, "codex", now.UnixMilli(), []quota.QuotaWindow{{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}})
	}
	seedPredictionResources(t, repo, resources)
	service := &Service{Repo: repo}
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
	service := &Service{Repo: repository.New(database)}
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
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("auth-1", "vincent@example.com", "codex")})
	seedPredictionSnapshot(t, repo, "auth-1", "codex", now.UnixMilli(), []quota.QuotaWindow{{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}})
	service := &Service{Repo: repo}
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

func TestQuotaPredictionJoinsStoredObservationKinds(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-kinds.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	observed := start.Add(6 * time.Hour)
	reset := start.Add(10 * time.Hour).UnixMilli()
	period := 10.0
	tests := []struct {
		name, provider, authIndex, windowID, scope, model, wantKind string
	}{
		{"claude standard weekly", "claude", "claude-weekly", "seven_day", "standard", "", "weekly"},
		{"claude sonnet weekly", "claude", "claude-sonnet", "seven_day_sonnet", "model", "claude-sonnet", "model_scoped"},
		{"antigravity group", "antigravity", "ag-group", "ag_claude", "group", "claude-sonnet", "model_scoped"},
	}
	resources := make([]domain.DiscoveredResource, 0, len(tests))
	for _, test := range tests {
		resources = append(resources, predictionResource(test.authIndex, test.name, test.provider))
		first, current := 10.0, 60.0
		window := quota.QuotaWindow{ID: test.windowID, Scope: test.scope, Model: test.model, UsedPercent: &first, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
		seedPredictionObservation(t, repo, test.authIndex, test.provider, start.Add(time.Hour).UnixMilli(), window)
		window.UsedPercent = &current
		seedPredictionObservation(t, repo, test.authIndex, test.provider, observed.UnixMilli(), window)
		seedPredictionSnapshot(t, repo, test.authIndex, test.provider, observed.UnixMilli(), []quota.QuotaWindow{window})
	}
	seedPredictionResources(t, repo, resources)
	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), observed.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != len(tests) {
		t.Fatalf("items = %+v", result.Items)
	}
	for _, test := range tests {
		found := false
		for _, item := range result.Items {
			if item.AuthIndex != test.authIndex {
				continue
			}
			found = true
			if item.WindowKind != test.wantKind || item.Verdict == quota.PredictionVerdictUnknown || item.BurnRatePerHour == nil {
				t.Fatalf("%s prediction = %+v", test.name, item)
			}
		}
		if !found {
			t.Fatalf("missing %s", test.name)
		}
	}
}

func TestQuotaPredictionStableAcrossStoredInputOrder(t *testing.T) {
	build := func(order []string) QuotaPredictionResult {
		t.Helper()
		database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "stable.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		repo := repository.New(database)
		now := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
		reset, period := now.Add(4*time.Hour).UnixMilli(), 10.0
		usedByModel := map[string]float64{"a-model": 21, "m-model": 22, "z-model": 23}
		windows := make([]quota.QuotaWindow, 0, len(order))
		for _, model := range order {
			modelUsed := usedByModel[model]
			windows = append(windows, quota.QuotaWindow{ID: "seven_day_sonnet", Scope: "model", Model: model, UsedPercent: &modelUsed, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"})
		}
		seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("same-auth", "same@example.com", "claude")})
		seedPredictionSnapshot(t, repo, "same-auth", "claude", now.UnixMilli(), windows)
		result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithReadOnly(capability.WithAnchor(context.Background(), now.UnixMilli()), true), QuotaPredictionInput{Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := build([]string{"z-model", "a-model", "m-model"})
	second := build([]string{"m-model", "z-model", "a-model"})
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !slices.Equal(firstJSON, secondJSON) || first.Omitted != 1 || second.Omitted != 1 {
		t.Fatalf("unstable result:\n%s\n%s", firstJSON, secondJSON)
	}
}

func TestBoundQuotaPredictionReturnsMarshalError(t *testing.T) {
	invalid := math.NaN()
	result := QuotaPredictionResult{Items: []QuotaPredictionItem{{BurnRatePerHour: &invalid}}}
	if err := boundQuotaPrediction(&result); err == nil {
		t.Fatal("non-finite prediction marshalled without an error")
	}
}

func TestBoundQuotaPredictionTruncatesByteTail(t *testing.T) {
	large := strings.Repeat("x", 20*1024)
	result := QuotaPredictionResult{Items: []QuotaPredictionItem{
		{CredentialLabel: large, Verdict: quota.PredictionVerdictUnknown},
		{CredentialLabel: large, Verdict: quota.PredictionVerdictUnknown},
	}}
	if err := boundQuotaPrediction(&result); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Omitted != 1 || len(encoded) > quotaPredictionByteLimit {
		t.Fatalf("byte-tail result = %d items, %d omitted, %d bytes", len(result.Items), result.Omitted, len(encoded))
	}
}

func TestBoundQuotaPredictionFindingsTruncatesByteTail(t *testing.T) {
	const alreadyOmitted = 3
	result := QuotaPredictionResult{Items: make([]QuotaPredictionItem, quotaPredictionFindingsLimit), Omitted: alreadyOmitted}
	for index := range result.Items {
		result.Items[index] = QuotaPredictionItem{
			CredentialLabel: strings.Repeat("x", 256),
			WindowKind:      fmt.Sprintf("custom-%03d", index),
			Verdict:         quota.PredictionVerdictWillExhaust,
		}
	}
	if err := boundQuotaPredictionFindings(&result); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) >= quotaPredictionFindingsLimit || result.Omitted != alreadyOmitted+quotaPredictionFindingsLimit-len(result.Items) || len(encoded) > quotaPredictionByteLimit {
		t.Fatalf("findings byte-tail result = %d items, %d omitted, %d bytes", len(result.Items), result.Omitted, len(encoded))
	}
}

func TestQuotaPredictionPassiveOverlayPreservesKnownReset(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-passive.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	now := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	reset, period, used := now.Add(4*time.Hour).UnixMilli(), 10.0, 20.0
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("passive", "passive@example.com", "codex")})
	seedPredictionSnapshot(t, repo, "passive", "codex", now.Add(-time.Hour).UnixMilli(), []quota.QuotaWindow{{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}})
	if _, err := repo.SQL().ExecContext(context.Background(),
		`INSERT INTO quota_observations (auth_index, provider, window_kind, scope, model, window_minutes, used_percent, reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source)
		 VALUES ('passive', 'codex', 'weekly', 'standard', '', 600, 30, NULL, 'healthy', ?, 'passive-weekly', ?, 'passive')`,
		now.UnixMilli(), now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ResetAtMS == nil || *result.Items[0].ResetAtMS != reset || slices.Contains(result.Items[0].Reasons, quota.PredictionReasonBoundaryUnknown) {
		t.Fatalf("passive overlay prediction = %+v", result.Items)
	}
}

func TestQuotaPredictionFindingsArePrioritizedAndCapped(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-findings.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	now := time.Date(2026, 10, 19, 6, 0, 0, 0, time.UTC)
	reset, period := now.Add(4*time.Hour).UnixMilli(), 10.0
	first := 10.0
	firstWindows := make([]quota.QuotaWindow, 0, 181)
	currentWindows := make([]quota.QuotaWindow, 0, 181)
	for index := 0; index < 181; index++ {
		id := fmt.Sprintf("custom-%03d", index)
		current := 70.0
		if index < 3 {
			current = 20
		}
		firstWindows = append(firstWindows, quota.QuotaWindow{ID: id, Scope: "standard", UsedPercent: &first, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"})
		currentWindows = append(currentWindows, quota.QuotaWindow{ID: id, Scope: "standard", UsedPercent: &current, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"})
	}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("many", "many@example.com", "custom")})
	for _, observation := range []struct {
		at      int64
		windows []quota.QuotaWindow
	}{{now.Add(-5 * time.Hour).UnixMilli(), firstWindows}, {now.UnixMilli(), currentWindows}} {
		if _, err := repo.RecordActiveQuotaObservations(context.Background(), &quota.NormalizedQuota{AuthIndex: "many", Provider: "custom", Status: "healthy", ObservedAtMS: observation.at, Windows: observation.windows}); err != nil {
			t.Fatal(err)
		}
	}
	seedPredictionSnapshot(t, repo, "many", "custom", now.UnixMilli(), currentWindows)
	service := &Service{Repo: repo}
	public, err := service.QuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := service.QuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()), QuotaPredictionInput{FindingsOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(public.Items)+public.Omitted != 181 || public.Omitted == 0 || len(findings.Items)+findings.Omitted != 181 || len(findings.Items) > quotaPredictionFindingsLimit || findings.Omitted < 3 {
		t.Fatalf("public=%d/%d findings=%d/%d", len(public.Items), public.Omitted, len(findings.Items), findings.Omitted)
	}
	for _, item := range findings.Items {
		if item.Verdict != quota.PredictionVerdictWillExhaust {
			t.Fatalf("lower-priority finding displaced exhaustion finding: %+v", item)
		}
	}
}

func predictionResource(authIndex, name, driver string) domain.DiscoveredResource {
	return domain.DiscoveredResource{ResourceKey: "auth-index:auth-file:" + authIndex, CPAResourceType: "auth-file", CPAAuthIndex: authIndex, CPAResourceName: name, CPADriver: driver, Status: domain.ResourceStatusUnclaimed}
}

func seedPredictionResources(t *testing.T, repo *repository.Repository, resources []domain.DiscoveredResource) {
	t.Helper()
	if err := repo.UpsertInstance(context.Background(), domain.CPAInstance{ID: "prediction", Name: "Prediction", BaseURL: "http://cpa", ManagementKeyCiphertext: []byte("cipher"), ManagementKeyNonce: []byte("nonce"), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.UpsertDiscoveredResources(context.Background(), "prediction", resources, time.Now().UTC(), false); err != nil {
		t.Fatal(err)
	}
}

func seedPredictionSnapshot(t *testing.T, repo *repository.Repository, authIndex, provider string, observedAtMS int64, windows []quota.QuotaWindow) {
	t.Helper()
	encoded, err := json.Marshal(windows)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: authIndex, Provider: provider, Status: "healthy", WindowsJSON: string(encoded), ObservedAtMS: observedAtMS}); err != nil {
		t.Fatal(err)
	}
}

func seedPredictionObservation(t *testing.T, repo *repository.Repository, authIndex, provider string, observedAtMS int64, window quota.QuotaWindow) {
	t.Helper()
	value := &quota.NormalizedQuota{AuthIndex: authIndex, Provider: provider, Status: "healthy", ObservedAtMS: observedAtMS, Windows: []quota.QuotaWindow{window}}
	if _, err := repo.RecordActiveQuotaObservations(context.Background(), value); err != nil {
		t.Fatal(err)
	}
}
