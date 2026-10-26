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
	dashboard, err := service.DashboardQuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	if len(dashboard.Items) != 120 || dashboard.Omitted != 0 {
		t.Fatalf("dashboard lost stored credentials: %d omitted %d", len(dashboard.Items), dashboard.Omitted)
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

func TestQuotaPredictionIncludesFiveProvidersAndOllamaUnknownCycle(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-five-providers.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	now := time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC)
	reset, period, used := now.Add(5*time.Hour).UnixMilli(), 10.0, 95.0
	window := quota.QuotaWindow{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
	resources := []domain.DiscoveredResource{}
	for _, provider := range []string{"codex", "claude", "antigravity", "step"} {
		resources = append(resources, predictionResource(provider+"-fixture", provider+"@example.test", provider))
		seedPredictionSnapshot(t, repo, provider+"-fixture", provider, now.UnixMilli(), []quota.QuotaWindow{window})
	}
	seedPredictionResources(t, repo, resources)
	account, err := repo.CreateOllamaAccount(context.Background(), repository.OllamaAccountCreate{ID: "fixture-ollama", Label: "ollama@example.test", Enabled: true, Ciphertext: []byte("synthetic"), Nonce: []byte("synthetic"), NowMS: now.UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	window.PeriodHours = nil
	encoded, err := json.Marshal(quota.NormalizedQuota{AuthIndex: "ollama:" + account.ID, Provider: "ollama", Status: "healthy", ObservedAtMS: now.UnixMilli(), Windows: []quota.QuotaWindow{window}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.RecordOllamaResult(context.Background(), repository.OllamaCollectionResult{ID: account.ID, Status: "ok", ObservedAtMS: now.UnixMilli(), IsSuccess: true, SnapshotJSON: string(encoded)}); err != nil {
		t.Fatal(err)
	}
	service := &Service{Repo: repo}
	result, err := service.DashboardQuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 5 {
		t.Fatalf("five-provider roster = %+v", result)
	}
	for _, item := range result.Items {
		if item.UsedPercent == nil || *item.UsedPercent != 95 || item.Accounting.RemainingPercent == nil || *item.Accounting.RemainingPercent != 5 {
			t.Fatalf("provider lost current balance: %+v", item)
		}
		if item.Provider == "ollama" {
			if item.Accounting.RemainingWindowHours != nil || item.ExhaustionETAMS != nil || !slices.Contains(item.Reasons, quota.PredictionReasonBoundaryUnknown) {
				t.Fatalf("Ollama invented a period: %+v", item)
			}
		} else if item.Accounting.RemainingWindowHours == nil || *item.Accounting.RemainingWindowHours != 0.5 {
			t.Fatalf("missing allowance: %+v", item)
		}
	}
	if _, _, err := repo.RecordOllamaResult(context.Background(), repository.OllamaCollectionResult{ID: account.ID, Status: "transport_error", ObservedAtMS: now.Add(time.Minute).UnixMilli(), Error: "synthetic failure"}); err != nil {
		t.Fatal(err)
	}
	result, err = service.DashboardQuotaPrediction(capability.WithAnchor(context.Background(), now.Add(time.Minute).UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result.Items {
		if item.Provider == "ollama" && (item.UsedPercent != nil || !slices.Contains(item.Reasons, quota.PredictionReasonReadingError)) {
			t.Fatalf("failed Ollama collection still claims balance: %+v", item)
		}
	}
}

func TestQuotaPredictionIncludesMissingCredentialReadings(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	seedPredictionResources(t, repo, []domain.DiscoveredResource{
		predictionResource("unread", "unread@example.test", "codex"),
		predictionResource("empty", "empty@example.test", "codex"),
		predictionResource("corrupt", "corrupt@example.test", "codex"),
	})
	now := time.Date(2026, 10, 26, 6, 0, 0, 0, time.UTC)
	seedPredictionSnapshot(t, repo, "empty", "codex", now.UnixMilli(), []quota.QuotaWindow{})
	if err := repo.SaveQuotaSnapshot(context.Background(), repository.QuotaSnapshotRecord{AuthIndex: "corrupt", Provider: "codex", Status: "healthy", WindowsJSON: "invalid", ObservedAtMS: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("missing rows: %+v", result)
	}
	for _, item := range result.Items {
		wantReason := quota.CapacityReasonNoReading
		if item.AuthIndex == "corrupt" {
			wantReason = quota.PredictionReasonReadingError
		}
		if item.Verdict != quota.PredictionVerdictUnknown || item.UsedPercent != nil || item.ProjectedPercentAtReset != nil ||
			item.Accounting.RemainingPercent != nil || !slices.Contains(item.Reasons, wantReason) {
			t.Fatalf("missing reading invented data: %+v", item)
		}
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
		first, middle, later, current := 10.0, 30.0, 45.0, 60.0
		window := quota.QuotaWindow{ID: test.windowID, Scope: test.scope, Model: test.model, UsedPercent: &first, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
		seedPredictionObservation(t, repo, test.authIndex, test.provider, start.Add(time.Hour).UnixMilli(), window)
		window.UsedPercent = &middle
		seedPredictionObservation(t, repo, test.authIndex, test.provider, start.Add(3*time.Hour).UnixMilli(), window)
		window.UsedPercent = &later
		seedPredictionObservation(t, repo, test.authIndex, test.provider, start.Add(4*time.Hour+30*time.Minute).UnixMilli(), window)
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
			if item.WindowID != test.windowID || item.WindowScope != test.scope || item.WindowModel != test.model {
				t.Fatalf("%s source window identity = %+v", test.name, item)
			}
			if item.WindowKind != test.wantKind || item.Verdict == quota.PredictionVerdictUnknown || item.BurnRatePerHour == nil {
				t.Fatalf("%s prediction = %+v", test.name, item)
			}
		}
		if !found {
			t.Fatalf("missing %s", test.name)
		}
	}
}

func TestQuotaPredictionBucketsBeforeHistoryLimit(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-bulk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	observed := start.Add(9 * time.Hour)
	reset, period, used := start.Add(10*time.Hour).UnixMilli(), 10.0, 60.0
	window := quota.QuotaWindow{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("bulk", "bulk@example.com", "codex")})
	seedPredictionSnapshot(t, repo, "bulk", "codex", observed.UnixMilli(), []quota.QuotaWindow{window})

	// One hundred and eight non-current buckets, each with a sample reading at
	// second ten. Filler rows keep the raw count above 5,000 inside the first
	// bucket, and a sentinel at second twenty of the last non-current bucket
	// moves the final sample past the five-thousandth raw row.
	const nonCurrentBuckets = 108
	rows := make([]seededQuotaObservation, 0, 5001+nonCurrentBuckets+1)
	for index := 0; index < 5001; index++ {
		rows = append(rows, seededQuotaObservation{
			ObservedAtMS: start.UnixMilli() + int64(index), UsedPercent: 5,
			ResetAtMS: &reset, Source: quota.ObservationSourceActive, EventKey: fmt.Sprintf("filler-%04d", index),
		})
	}
	for index := 0; index < nonCurrentBuckets; index++ {
		percent := 5 + 55*float64(index)/float64(nonCurrentBuckets-1)
		rows = append(rows, seededQuotaObservation{
			ObservedAtMS: start.UnixMilli() + int64(index)*quota.PredictionBucketMS + 10_000,
			UsedPercent:  percent, ResetAtMS: &reset, Source: quota.ObservationSourceActive,
			EventKey: fmt.Sprintf("bucket-%03d", index),
		})
	}
	rows = append(rows, seededQuotaObservation{
		ObservedAtMS: start.UnixMilli() + int64(nonCurrentBuckets-1)*quota.PredictionBucketMS + 20_000,
		UsedPercent:  59.5, ResetAtMS: &reset, Source: quota.ObservationSourceActive, EventKey: "tail-sentinel",
	})
	seedQuotaObservationRows(t, repo, "bulk", "codex", "weekly", "standard", "", rows)

	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), observed.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v", result.Items)
	}
	item := result.Items[0]
	if slices.Contains(item.Reasons, quota.PredictionReasonIncompleteHistory) {
		t.Fatalf("5110 raw rows were treated as incomplete history: %+v", item)
	}
	if item.SampleCount != 109 {
		t.Fatalf("sample count = %d, want 109 (108 non-current buckets plus current): %+v", item.SampleCount, item)
	}
	if item.CoverageSeconds == nil || *item.CoverageSeconds != int64((9*time.Hour-10*time.Second)/time.Second) {
		coverage := int64(-1)
		if item.CoverageSeconds != nil {
			coverage = *item.CoverageSeconds
		}
		t.Fatalf("coverage = %ds, want 32390s from the first sample to the current reading: %+v", coverage, item)
	}
	if item.MaxGapSeconds == nil || *item.MaxGapSeconds != 310 {
		maximumGap := int64(-1)
		if item.MaxGapSeconds != nil {
			maximumGap = *item.MaxGapSeconds
		}
		t.Fatalf("max gap = %ds, want 310s with the tail sentinel: %+v", maximumGap, item)
	}
	if item.Verdict == quota.PredictionVerdictUnknown {
		t.Fatalf("sampled history still unknown: %+v", item)
	}
}

func TestQuotaPredictionFiltersCompleteWindowIdentity(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-identity.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	observed := start.Add(6 * time.Hour)
	reset, period := start.Add(10*time.Hour).UnixMilli(), 10.0
	firstUsed, secondUsed := 40.0, 90.0
	windows := []quota.QuotaWindow{
		{ID: "seven_day_sonnet", Kind: "model_scoped", Scope: "model", Model: "claude-a", UsedPercent: &firstUsed, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"},
		{ID: "seven_day_sonnet", Kind: "model_scoped", Scope: "model", Model: "claude-b", UsedPercent: &secondUsed, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"},
	}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("shared", "shared@example.com", "claude")})
	seedPredictionSnapshot(t, repo, "shared", "claude", observed.UnixMilli(), windows)
	for index, used := range []float64{10, 20, 30} {
		window := windows[0]
		window.UsedPercent = &used
		seedPredictionObservation(t, repo, "shared", "claude", start.Add(time.Duration(index*2+1)*time.Hour).UnixMilli(), window)
	}
	for minute := 60; minute <= 300; minute += 5 {
		used := 50.0 + float64(minute)/20
		window := windows[1]
		window.UsedPercent = &used
		seedPredictionObservation(t, repo, "shared", "claude", start.Add(time.Duration(minute)*time.Minute).UnixMilli(), window)
	}

	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), observed.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v", result.Items)
	}
	for _, item := range result.Items {
		switch item.WindowModel {
		case "claude-a":
			if item.SampleCount != 4 {
				t.Fatalf("claude-a mixed another model into its buckets: %+v", item)
			}
		case "claude-b":
			if item.SampleCount <= 4 {
				t.Fatalf("claude-b sample was unexpectedly small: %+v", item)
			}
		default:
			t.Fatalf("unexpected window identity: %+v", item)
		}
	}
}

func TestQuotaPredictionDetectsBucketReset(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-bucket-reset.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	observed := start.Add(6 * time.Hour)
	reset, period, current := start.Add(10*time.Hour).UnixMilli(), 10.0, 35.0
	window := quota.QuotaWindow{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &current, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("reset", "reset@example.com", "codex")})
	for _, observation := range []struct {
		at   time.Duration
		used float64
	}{
		{time.Hour, 50},
		{time.Hour + 5*time.Minute, 10},
		{3 * time.Hour, 20},
		{5 * time.Hour, 30},
	} {
		stored := window
		stored.UsedPercent = &observation.used
		seedPredictionObservation(t, repo, "reset", "codex", start.Add(observation.at).UnixMilli(), stored)
	}
	seedPredictionSnapshot(t, repo, "reset", "codex", observed.UnixMilli(), []quota.QuotaWindow{window})

	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), observed.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || !slices.Contains(result.Items[0].Reasons, quota.PredictionReasonResetMidCycle) {
		t.Fatalf("bucket reset was not detected: %+v", result.Items)
	}
}

func TestQuotaPredictionDetectsInBucketReset(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-in-bucket-reset.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	observed := start.Add(6 * time.Hour)
	reset, period, current := start.Add(10*time.Hour).UnixMilli(), 10.0, 50.0
	window := quota.QuotaWindow{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &current, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("in-bucket", "in-bucket@example.com", "codex")})
	seedQuotaObservationRows(t, repo, "in-bucket", "codex", "weekly", "standard", "", []seededQuotaObservation{
		{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset, EventKey: "in-1"},
		{ObservedAtMS: start.Add(time.Hour + time.Minute).UnixMilli(), UsedPercent: 80, ResetAtMS: &reset, EventKey: "in-2"},
		{ObservedAtMS: start.Add(time.Hour + 2*time.Minute).UnixMilli(), UsedPercent: 20, ResetAtMS: &reset, EventKey: "in-3"},
		{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 30, ResetAtMS: &reset, EventKey: "in-4"},
		{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 50, ResetAtMS: &reset, EventKey: "in-5"},
	})
	seedPredictionSnapshot(t, repo, "in-bucket", "codex", observed.UnixMilli(), []quota.QuotaWindow{window})

	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), observed.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || !slices.Contains(result.Items[0].Reasons, quota.PredictionReasonResetMidCycle) {
		t.Fatalf("within-bucket reset was not detected: %+v", result.Items)
	}
}

func TestQuotaPredictionDetectsCurrentSnapshotReset(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-current-reset.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	observed := start.Add(6 * time.Hour)
	reset, period, current := start.Add(10*time.Hour).UnixMilli(), 10.0, 20.0
	window := quota.QuotaWindow{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &current, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("snapshot-reset", "snapshot-reset@example.com", "codex")})
	seedQuotaObservationRows(t, repo, "snapshot-reset", "codex", "weekly", "standard", "", []seededQuotaObservation{
		{ObservedAtMS: start.Add(time.Hour).UnixMilli(), UsedPercent: 5, ResetAtMS: &reset, EventKey: "cur-1"},
		{ObservedAtMS: start.Add(3 * time.Hour).UnixMilli(), UsedPercent: 7, ResetAtMS: &reset, EventKey: "cur-2"},
		{ObservedAtMS: start.Add(5 * time.Hour).UnixMilli(), UsedPercent: 9, ResetAtMS: &reset, EventKey: "cur-3"},
		{ObservedAtMS: start.Add(5*time.Hour + 55*time.Minute).UnixMilli(), UsedPercent: 10, ResetAtMS: &reset, EventKey: "cur-4"},
		{ObservedAtMS: observed.UnixMilli(), UsedPercent: 80, ResetAtMS: &reset, EventKey: "cur-5"},
	})
	seedPredictionSnapshot(t, repo, "snapshot-reset", "codex", observed.UnixMilli(), []quota.QuotaWindow{window})

	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), observed.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("items = %+v", result.Items)
	}
	item := result.Items[0]
	if !slices.Contains(item.Reasons, quota.PredictionReasonResetMidCycle) {
		t.Fatalf("current snapshot reset was not detected: %+v", item)
	}
	if item.SampleCount != 5 {
		t.Fatalf("current bucket replacement duplicated a sample: %+v", item)
	}
}

func TestPredictionCurrentReadingHasResetConditions(t *testing.T) {
	reset := func(value int64) *int64 { return &value }
	buckets := []repository.QuotaObservationBucket{{
		StartMS: 300_000, MinUsedPercent: 40, LastUsedPercent: 50,
		LastResetAtMS: reset(10_000), MinResetAtMS: reset(10_000), MaxResetAtMS: reset(10_000),
	}}
	for _, test := range []struct {
		name   string
		window quota.QuotaWindow
		want   bool
	}{
		{"drop from bucket last", quota.QuotaWindow{UsedPercent: floatPointer(40), ResetAtMS: reset(10_000)}, true},
		{"reset moved earlier", quota.QuotaWindow{UsedPercent: floatPointer(50), ResetAtMS: reset(8_000)}, true},
		{"exact tolerance", quota.QuotaWindow{UsedPercent: floatPointer(49), ResetAtMS: reset(9_000)}, false},
		{"no current bucket", quota.QuotaWindow{UsedPercent: floatPointer(40), ResetAtMS: reset(10_000)}, false},
		{"no current reading", quota.QuotaWindow{ResetAtMS: reset(10_000)}, false},
	} {
		observedAtMS := int64(300_000)
		if test.name == "no current bucket" {
			observedAtMS = 600_000
		}
		if got := predictionCurrentReadingHasReset(buckets, test.window, observedAtMS); got != test.want {
			t.Fatalf("%s reset = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestPredictionBucketsHaveResetConditions(t *testing.T) {
	reset := func(value int64) *int64 { return &value }
	buckets := func(mutate func(*repository.QuotaObservationBucket)) []repository.QuotaObservationBucket {
		first := repository.QuotaObservationBucket{StartMS: 0, MinUsedPercent: 50, LastUsedPercent: 50, LastResetAtMS: reset(10_000)}
		second := repository.QuotaObservationBucket{
			StartMS: 300_000, MinUsedPercent: 55, LastUsedPercent: 60,
			LastResetAtMS: reset(10_000), MinResetAtMS: reset(10_000), MaxResetAtMS: reset(10_000),
		}
		mutate(&second)
		return []repository.QuotaObservationBucket{first, second}
	}
	for _, test := range []struct {
		name   string
		mutate func(*repository.QuotaObservationBucket)
		want   bool
	}{
		{"used drop", func(bucket *repository.QuotaObservationBucket) { bucket.MinUsedPercent = 40 }, true},
		{"reset moved earlier", func(bucket *repository.QuotaObservationBucket) { bucket.MinResetAtMS = reset(8_000) }, true},
		{"reset spread within bucket", func(bucket *repository.QuotaObservationBucket) { bucket.MaxResetAtMS = reset(12_001) }, true},
		{"within tolerance", func(bucket *repository.QuotaObservationBucket) {
			bucket.MinUsedPercent = 49
			bucket.MinResetAtMS = reset(9_500)
		}, false},
	} {
		if got := predictionBucketsHaveReset(buckets(test.mutate)); got != test.want {
			t.Fatalf("%s reset = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestQuotaPredictionQualityFieldsSerializeNulls(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-quality.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	start := time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC)
	observed := start.Add(6 * time.Hour)
	reset, period := start.Add(10*time.Hour).UnixMilli(), 10.0
	projected, full := 40.0, 100.0
	projectionWindow := quota.QuotaWindow{ID: "weekly", Kind: "weekly", Scope: "standard", UsedPercent: &projected, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
	factWindow := quota.QuotaWindow{ID: "five_hour", Kind: "five_hour", Scope: "standard", UsedPercent: &full, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"}
	resources := []domain.DiscoveredResource{
		predictionResource("projected", "projected@example.com", "codex"),
		predictionResource("fact", "fact@example.com", "codex"),
	}
	seedPredictionResources(t, repo, resources)
	seedPredictionSnapshot(t, repo, "projected", "codex", observed.UnixMilli(), []quota.QuotaWindow{projectionWindow})
	seedPredictionSnapshot(t, repo, "fact", "codex", observed.UnixMilli(), []quota.QuotaWindow{factWindow})
	for index, used := range []float64{10, 20, 30} {
		window := projectionWindow
		window.UsedPercent = &used
		seedPredictionObservation(t, repo, "projected", "codex", start.Add(time.Duration(index*2+1)*time.Hour).UnixMilli(), window)
	}

	result, err := (&Service{Repo: repo}).QuotaPrediction(capability.WithAnchor(context.Background(), observed.UnixMilli()), QuotaPredictionInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("items = %+v", result.Items)
	}
	for _, item := range result.Items {
		encoded, err := json.Marshal(item)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			t.Fatal(err)
		}
		coverage, coveragePresent := fields["coverage_seconds"]
		maximumGap, maximumGapPresent := fields["max_gap_seconds"]
		if !coveragePresent || !maximumGapPresent {
			t.Fatalf("quality keys missing: %s", encoded)
		}
		switch item.AuthIndex {
		case "projected":
			if string(fields["sample_count"]) != "4" || string(coverage) != "18000" || string(maximumGap) != "7200" {
				t.Fatalf("projection quality fields = %s", encoded)
			}
		case "fact":
			if item.Evidence != quota.PredictionEvidenceFact || string(fields["sample_count"]) != "0" || string(coverage) != "null" || string(maximumGap) != "null" {
				t.Fatalf("fact quality fields = %s", encoded)
			}
		default:
			t.Fatalf("unexpected item = %s", encoded)
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
	middleWindows := make([]quota.QuotaWindow, 0, 181)
	currentWindows := make([]quota.QuotaWindow, 0, 181)
	for index := 0; index < 181; index++ {
		id := fmt.Sprintf("custom-%03d", index)
		middle, current := 40.0, 70.0
		if index < 3 {
			middle, current = 15, 20
		}
		firstWindows = append(firstWindows, quota.QuotaWindow{ID: id, Scope: "standard", UsedPercent: &first, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"})
		middleWindows = append(middleWindows, quota.QuotaWindow{ID: id, Scope: "standard", UsedPercent: &middle, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"})
		currentWindows = append(currentWindows, quota.QuotaWindow{ID: id, Scope: "standard", UsedPercent: &current, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact"})
	}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{predictionResource("many", "many@example.com", "custom")})
	for _, observation := range []struct {
		at      int64
		windows []quota.QuotaWindow
	}{{now.Add(-5 * time.Hour).UnixMilli(), firstWindows}, {now.Add(-150 * time.Minute).UnixMilli(), middleWindows}, {now.UnixMilli(), currentWindows}} {
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

type seededQuotaObservation struct {
	ObservedAtMS int64
	UsedPercent  float64
	ResetAtMS    *int64
	Source       string
	EventKey     string
}

// seedQuotaObservationRows writes an explicit timeline in one transaction so a
// test can place readings at exact bucket boundaries without the writer's
// heartbeat dedup changing the fixture.
func seedQuotaObservationRows(t *testing.T, repo *repository.Repository, authIndex, provider, windowKind, scope, model string, rows []seededQuotaObservation) {
	t.Helper()
	tx, err := repo.SQL().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	statement, err := tx.Prepare(`INSERT INTO quota_observations (
		auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
		reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	for _, row := range rows {
		var resetValue any
		if row.ResetAtMS != nil {
			resetValue = *row.ResetAtMS
		}
		source := row.Source
		if source == "" {
			source = quota.ObservationSourceActive
		}
		if _, err := statement.Exec(authIndex, provider, windowKind, scope, model, 600,
			row.UsedPercent, resetValue, "healthy", row.ObservedAtMS, row.EventKey, 0, source); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
