package operations

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

func TestPredictionResetSignalsCarryComparedValues(t *testing.T) {
	reset := func(value int64) *int64 { return &value }
	buckets := []repository.QuotaObservationBucket{
		{StartMS: 0, MinUsedPercent: 50, LastUsedPercent: 50, LastResetAtMS: reset(10_000)},
		{
			StartMS: 300_000, MinUsedPercent: 40, LastUsedPercent: 60,
			LastResetAtMS: reset(10_000), MinResetAtMS: reset(8_000), MaxResetAtMS: reset(10_000),
		},
	}
	signal := predictionBucketsResetSignal(buckets)
	if signal == nil || signal.Check != predictionResetCheckBucketUsed {
		t.Fatalf("used-drop signal = %+v", signal)
	}
	if signal.PreviousStartMS != 0 || signal.CurrentStartMS != 300_000 ||
		signal.PreviousLastUsedPercent != 50 || signal.CurrentMinUsedPercent != 40 ||
		signal.usedDifference() != 10 || signal.resetDifferenceMS() != 2_000 {
		t.Fatalf("used-drop values = %+v", signal)
	}
	if signal.PreviousLastResetAtMS == nil || *signal.PreviousLastResetAtMS != 10_000 ||
		signal.CurrentMinResetAtMS == nil || *signal.CurrentMinResetAtMS != 8_000 {
		t.Fatalf("used-drop reset values = %+v", signal)
	}

	buckets[1].MinUsedPercent = 55
	signal = predictionBucketsResetSignal(buckets)
	if signal == nil || signal.Check != predictionResetCheckBucketResetAt || signal.resetDifferenceMS() != 2_000 {
		t.Fatalf("reset-moved signal = %+v", signal)
	}
}

func TestPredictionCurrentResetSignalCarriesComparedValues(t *testing.T) {
	reset := func(value int64) *int64 { return &value }
	buckets := []repository.QuotaObservationBucket{{
		StartMS: 300_000, MinUsedPercent: 40, LastUsedPercent: 50,
		LastResetAtMS: reset(10_000), MinResetAtMS: reset(10_000), MaxResetAtMS: reset(10_000),
	}}
	window := quota.QuotaWindow{UsedPercent: floatPointer(40), ResetAtMS: reset(10_000)}
	signal := predictionCurrentReadingResetSignal(buckets, window, 300_000)
	if signal == nil || signal.Check != predictionResetCheckCurrentUsed ||
		signal.PreviousStartMS != 300_000 || signal.CurrentStartMS != 300_000 ||
		signal.PreviousLastUsedPercent != 50 || signal.CurrentMinUsedPercent != 40 {
		t.Fatalf("current used-drop signal = %+v", signal)
	}
}

func TestPredictionResetDiagnosticLogContainsLocatorFields(t *testing.T) {
	var logs bytes.Buffer
	service := &Service{Logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo}))}
	previousReset, currentReset := int64(10_000), int64(8_000)
	service.logPredictionReset(
		Quota{AuthIndex: "auth-x", Provider: "antigravity"},
		quota.QuotaWindow{ID: "ag_g_b", Kind: "five_hour", Scope: "group", ModelFamilies: []string{"claude"}},
		"five_hour",
		predictionResetSignal{
			Check:           predictionResetCheckBucketUsed,
			PreviousStartMS: 0, CurrentStartMS: 300_000,
			PreviousLastUsedPercent: 50, CurrentMinUsedPercent: 40,
			PreviousLastResetAtMS: &previousReset, CurrentMinResetAtMS: &currentReset,
		},
	)
	output := logs.String()
	for _, field := range []string{
		"quota prediction mid-cycle reset detected",
		"auth_index=auth-x",
		"provider=antigravity",
		"window_kind=five_hour",
		"window_id=ag_g_b",
		"window_scope=group",
		"check=bucket_used_drop",
		"previous_bucket_start_ms=0",
		"current_bucket_start_ms=300000",
		"previous_last_used_percent=50",
		"current_min_used_percent=40",
		"used_difference=10",
		"previous_last_reset_at_ms=10000",
		"current_min_reset_at_ms=8000",
		"reset_difference_ms=2000",
	} {
		if !strings.Contains(output, field) {
			t.Fatalf("diagnostic log missing %q:\n%s", field, output)
		}
	}
}

func TestQuotaPredictionRollingWindowZeroTrafficAndTraffic(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-rolling.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	reset, period, used := now.Add(5*time.Hour+253*time.Millisecond).UnixMilli(), 5.0, 0.0
	window := quota.QuotaWindow{
		ID: "five_hour", Kind: "five_hour", Scope: "standard",
		UsedPercent: &used, ResetAtMS: &reset, PeriodHours: &period, ResetAccuracy: "exact",
	}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{
		predictionResource("rolling-empty", "empty@example.test", "antigravity"),
		predictionResource("rolling-traffic", "traffic@example.test", "antigravity"),
	})
	seedPredictionSnapshot(t, repo, "rolling-empty", "antigravity", now.UnixMilli(), []quota.QuotaWindow{window})
	seedPredictionSnapshot(t, repo, "rolling-traffic", "antigravity", now.UnixMilli(), []quota.QuotaWindow{window})
	if _, err := repo.InsertUsageEvents(context.Background(), []usage.Event{{
		InstanceID: "prediction", EventKey: "rolling-traffic-1", AuthIndex: "rolling-traffic",
		Model: "gemini-3-pro", TimestampMS: now.Add(-time.Hour).UnixMilli(), TotalTokens: 10,
	}}); err != nil {
		t.Fatal(err)
	}

	result, err := (&Service{Repo: repo}).DashboardQuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]QuotaPredictionItem{}
	for _, item := range result.Items {
		items[item.AuthIndex] = item
	}
	empty, found := items["rolling-empty"]
	if !found {
		t.Fatalf("rolling-empty missing from %+v", result.Items)
	}
	if empty.LastsUntilReset == nil || !*empty.LastsUntilReset || empty.Evidence != quota.PredictionEvidenceFact {
		t.Fatalf("rolling zero-traffic prediction = %+v", empty)
	}
	if !empty.Accounting.IsRollingWindow || empty.Accounting.UsageRequests == nil || *empty.Accounting.UsageRequests != 0 {
		t.Fatalf("rolling zero-traffic accounting = %+v", empty.Accounting)
	}

	traffic, found := items["rolling-traffic"]
	if !found {
		t.Fatalf("rolling-traffic missing from %+v", result.Items)
	}
	if traffic.LastsUntilReset != nil || traffic.Evidence != quota.PredictionEvidenceUnknown ||
		traffic.ExhaustionETAMS != nil || traffic.ProjectedPercentAtReset != nil {
		t.Fatalf("rolling traffic prediction = %+v", traffic)
	}
	if traffic.Accounting.IsRollingWindow == false || traffic.Accounting.UsageRequests == nil || *traffic.Accounting.UsageRequests != 1 {
		t.Fatalf("rolling traffic accounting = %+v", traffic.Accounting)
	}
	if !containsString(traffic.Reasons, quota.PredictionReasonRollingWindow) {
		t.Fatalf("rolling traffic reasons = %v", traffic.Reasons)
	}
}

func TestQuotaPredictionUnknownResetUsesTrailingTrafficForZeroUse(t *testing.T) {
	database, err := repository.Open(context.Background(), filepath.Join(t.TempDir(), "prediction-unknown-reset.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repo := repository.New(database)
	now := time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)
	period, used := 5.0, 0.0
	window := quota.QuotaWindow{
		ID: "five_hour", Kind: "five_hour", Scope: "standard",
		UsedPercent: &used, ResetAtMS: nil, PeriodHours: &period, ResetAccuracy: "exact",
	}
	seedPredictionResources(t, repo, []domain.DiscoveredResource{
		predictionResource("unknown-reset-empty", "empty@example.test", "codex"),
		predictionResource("unknown-reset-traffic", "traffic@example.test", "codex"),
	})
	seedPredictionSnapshot(t, repo, "unknown-reset-empty", "codex", now.UnixMilli(), []quota.QuotaWindow{window})
	seedPredictionSnapshot(t, repo, "unknown-reset-traffic", "codex", now.UnixMilli(), []quota.QuotaWindow{window})
	if _, err := repo.InsertUsageEvents(context.Background(), []usage.Event{{
		InstanceID: "prediction", EventKey: "unknown-reset-traffic-1", AuthIndex: "unknown-reset-traffic",
		Model: "gpt-5", TimestampMS: now.Add(-time.Hour).UnixMilli(), TotalTokens: 10,
	}}); err != nil {
		t.Fatal(err)
	}

	result, err := (&Service{Repo: repo}).DashboardQuotaPrediction(capability.WithAnchor(context.Background(), now.UnixMilli()))
	if err != nil {
		t.Fatal(err)
	}
	items := map[string]QuotaPredictionItem{}
	for _, item := range result.Items {
		items[item.AuthIndex] = item
	}
	empty, found := items["unknown-reset-empty"]
	if !found {
		t.Fatalf("unknown-reset-empty missing from %+v", result.Items)
	}
	if empty.LastsUntilReset == nil || !*empty.LastsUntilReset || empty.Evidence != quota.PredictionEvidenceFact {
		t.Fatalf("unknown-reset zero-traffic prediction = %+v", empty)
	}
	if empty.Accounting.UsageRequests == nil || *empty.Accounting.UsageRequests != 0 || empty.Accounting.WindowStartMS != nil {
		t.Fatalf("unknown-reset zero-traffic accounting = %+v", empty.Accounting)
	}

	traffic, found := items["unknown-reset-traffic"]
	if !found {
		t.Fatalf("unknown-reset-traffic missing from %+v", result.Items)
	}
	if traffic.LastsUntilReset != nil || traffic.Evidence == quota.PredictionEvidenceFact ||
		traffic.Accounting.UsageRequests == nil || *traffic.Accounting.UsageRequests != 1 {
		t.Fatalf("unknown-reset traffic prediction = %+v accounting %+v", traffic, traffic.Accounting)
	}
	if !containsString(traffic.Reasons, quota.PredictionReasonBoundaryUnknown) {
		t.Fatalf("unknown-reset traffic reasons = %v", traffic.Reasons)
	}
}
