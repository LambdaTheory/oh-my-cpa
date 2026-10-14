package repository

import (
	"context"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

func quotaHeaders(t *testing.T, headers map[string]string) string {
	t.Helper()
	ordered := make([]usage.ResponseHeader, 0, len(headers))
	for name, value := range headers {
		ordered = append(ordered, usage.ResponseHeader{Name: name, Value: value})
	}
	return usage.EncodeResponseHeaders(ordered)
}

func codexWeeklyHeaders(t *testing.T, percent string, resetSeconds string) string {
	t.Helper()
	return quotaHeaders(t, map[string]string{
		"x-codex-primary-used-percent":   percent,
		"x-codex-primary-window-minutes": "10080",
		"x-codex-primary-reset-at":       resetSeconds,
	})
}

func commitQuotaEvent(t *testing.T, repo *Repository, eventKey, authIndex string, atMS int64, storedHeaders string) {
	t.Helper()
	if _, err := repo.CommitUsageDecoded(context.Background(), []UsageDecoded{{Event: usage.Event{
		InstanceID: "default", EventKey: eventKey, AuthIndex: authIndex, TimestampMS: atMS, ResponseHeaders: storedHeaders,
	}}}); err != nil {
		t.Fatalf("commit %s: %v", eventKey, err)
	}
}

func TestCommitUsageDecodedDeduplicatesQuotaObservations(t *testing.T) {
	repo := usageTestRepository(t)
	const base = int64(1_700_000_000_000)
	commitQuotaEvent(t, repo, "a", "c-1", base, codexWeeklyHeaders(t, "37", "1700003600"))
	if count, _ := repo.CountQuotaObservations(context.Background()); count != 1 {
		t.Fatalf("after first reading count = %d, want 1", count)
	}
	// A repeat within the heartbeat window is dropped.
	commitQuotaEvent(t, repo, "b", "c-1", base+60_000, codexWeeklyHeaders(t, "37", "1700003600"))
	if count, _ := repo.CountQuotaObservations(context.Background()); count != 1 {
		t.Fatalf("a repeated reading was stored, count = %d", count)
	}
	// A changed percentage is kept.
	commitQuotaEvent(t, repo, "c", "c-1", base+120_000, codexWeeklyHeaders(t, "41", "1700003600"))
	if count, _ := repo.CountQuotaObservations(context.Background()); count != 2 {
		t.Fatalf("a changed percentage was dropped, count = %d", count)
	}
	// An unchanged reading after the heartbeat interval is kept.
	commitQuotaEvent(t, repo, "d", "c-1", base+18*60_000, codexWeeklyHeaders(t, "41", "1700003600"))
	if count, _ := repo.CountQuotaObservations(context.Background()); count != 3 {
		t.Fatalf("a heartbeat reading was dropped, count = %d", count)
	}
	// A changed reset instant with the same percentage is kept.
	commitQuotaEvent(t, repo, "e", "c-1", base+19*60_000, codexWeeklyHeaders(t, "41", "1700007200"))
	if count, _ := repo.CountQuotaObservations(context.Background()); count != 4 {
		t.Fatalf("a changed reset instant was dropped, count = %d", count)
	}
	// A different credential and provider start their own history.
	claude := quotaHeaders(t, map[string]string{
		"anthropic-ratelimit-unified-5h-utilization": "0.5",
		"anthropic-ratelimit-unified-5h-reset":       "1700001800",
	})
	commitQuotaEvent(t, repo, "f", "cl-1", base+20*60_000, claude)
	if count, _ := repo.CountQuotaObservations(context.Background()); count != 5 {
		t.Fatalf("a second credential was dropped, count = %d", count)
	}
	// A request with no recognized quota headers stores no observation.
	commitQuotaEvent(t, repo, "g", "c-1", base+21*60_000, quotaHeaders(t, map[string]string{"x-request-id": "abc"}))
	if count, _ := repo.CountQuotaObservations(context.Background()); count != 5 {
		t.Fatalf("a headerless request stored an observation, count = %d", count)
	}
}

func TestCommitUsageDecodedOrdersEventsWithinABatch(t *testing.T) {
	repo := usageTestRepository(t)
	const base = int64(1_700_000_000_000)
	// The newest event is first in the slice; the writer must still treat the
	// later (older) 37% reading as the newest row so the next change is compared
	// against it, not against the 90% that was written first.
	decoded := []UsageDecoded{
		{Event: usage.Event{InstanceID: "default", EventKey: "late", AuthIndex: "c-1", TimestampMS: base + 60_000, ResponseHeaders: codexWeeklyHeaders(t, "90", "1700003600")}},
		{Event: usage.Event{InstanceID: "default", EventKey: "early", AuthIndex: "c-1", TimestampMS: base, ResponseHeaders: codexWeeklyHeaders(t, "37", "1700003600")}},
	}
	if _, err := repo.CommitUsageDecoded(context.Background(), decoded); err != nil {
		t.Fatal(err)
	}
	var newestPercent float64
	if err := repo.SQL().QueryRow(`SELECT used_percent FROM quota_observations WHERE auth_index = 'c-1' ORDER BY observed_at_ms DESC, id DESC LIMIT 1`).Scan(&newestPercent); err != nil {
		t.Fatal(err)
	}
	if newestPercent != 90 {
		t.Fatalf("newest observation = %v, want 90 from the later event", newestPercent)
	}
}

func TestBackfillQuotaObservationsSweepsRetainedRecords(t *testing.T) {
	repo := usageTestRepository(t)
	ctx := context.Background()
	insert := func(eventKey, authIndex string, atMS int64, storedHeaders string) {
		t.Helper()
		if _, err := repo.SQL().Exec(`INSERT INTO usage_events (instance_id, event_key, auth_index, api_group_key, timestamp_ms, created_at_ms, response_headers)
			VALUES ('default', ?, ?, 'group', ?, 0, ?)`, eventKey, authIndex, atMS, storedHeaders); err != nil {
			t.Fatal(err)
		}
	}
	insert("e1", "c-1", 1000, codexWeeklyHeaders(t, "20", "1700003600"))
	insert("e2", "c-1", 2000, codexWeeklyHeaders(t, "60", "1700003600"))
	insert("e3", "c-1", 3000, "") // no headers: skipped but the watermark still advances
	insert("e4", "cl-1", 4000, quotaHeaders(t, map[string]string{
		"anthropic-ratelimit-unified-7d-utilization": "0.4",
		"anthropic-ratelimit-unified-7d-reset":       "1700604800",
	}))

	observed, err := repo.BackfillQuotaObservations(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if observed != 4 {
		t.Fatalf("backfill considered %d records, want 4", observed)
	}
	rows, hasMore, err := repo.ListQuotaObservations(ctx, QuotaObservationFilter{AuthIndex: "c-1", FromMS: 0, ToMS: 1_000_000}, 10)
	if err != nil || hasMore {
		t.Fatalf("list c-1: %v hasMore=%v", err, hasMore)
	}
	if len(rows) != 2 || rows[0].UsedPercent != 20 || rows[1].UsedPercent != 60 {
		t.Fatalf("c-1 observations = %+v", rows)
	}
	// A repeated pass is a no-op: the checkpoint has no work left.
	observed, err = repo.BackfillQuotaObservations(ctx, 10)
	if err != nil || observed != 0 {
		t.Fatalf("second backfill = %d %v, want 0", observed, err)
	}
	if count, _ := repo.CountQuotaObservations(ctx); count != 3 {
		t.Fatalf("unexpected observation count %d", count)
	}
}

func TestQuotaObservationsRollOnTheirFixedHorizon(t *testing.T) {
	repo := usageTestRepository(t)
	ctx := context.Background()
	if _, err := repo.SQL().Exec(`INSERT INTO quota_observations (auth_index, provider, window_kind, window_minutes, used_percent, status, observed_at_ms, event_key, created_at_ms)
		VALUES ('c-1', 'codex', 'weekly', 10080, 50, '', 1000, 'old', 0),
		       ('c-1', 'codex', 'weekly', 10080, 60, '', 9999999999999, 'new', 0)`); err != nil {
		t.Fatal(err)
	}
	cutoffs := map[string]int64{LifecycleQuotaObservations: 5000}
	for pass := 0; pass < 2; pass++ {
		if _, err := repo.RunLifecycle(ctx, cutoffs, 10); err != nil {
			t.Fatal(err)
		}
	}
	var remaining int
	if err := repo.SQL().QueryRow(`SELECT COUNT(1) FROM quota_observations`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 1 {
		t.Fatalf("remaining observations = %d, want only the recent one", remaining)
	}
}

func TestListQuotaObservationsReportsOverflow(t *testing.T) {
	repo := usageTestRepository(t)
	ctx := context.Background()
	for _, key := range []string{"a", "b", "c"} {
		if _, err := repo.SQL().Exec(`INSERT INTO quota_observations (auth_index, provider, window_kind, window_minutes, used_percent, reset_at_ms, status, observed_at_ms, event_key, created_at_ms)
			VALUES ('c-1', 'codex', 'weekly', 10080, 10, NULL, '', ?, ?, 0)`, 1000, key); err != nil {
			t.Fatal(err)
		}
	}
	rows, hasMore, err := repo.ListQuotaObservations(ctx, QuotaObservationFilter{FromMS: 0, ToMS: 1_000_000}, 2)
	if err != nil || !hasMore || len(rows) != 2 {
		t.Fatalf("overflow read = %d rows hasMore=%v %v", len(rows), hasMore, err)
	}
	if rows[0].Provider != "codex" || rows[0].WindowKind != "weekly" || rows[0].WindowMinutes != 10080 {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestRecordActiveQuotaObservationsWritesTheTimeline(t *testing.T) {
	repo := usageTestRepository(t)
	ctx := context.Background()
	fiveHours, weekly, used := 5.0, 168.0, 52.0
	value := &quota.NormalizedQuota{
		AuthIndex:    "cl-1",
		Provider:     "claude",
		ObservedAtMS: 1_700_000_000_000,
		Windows: []quota.QuotaWindow{
			{ID: "five_hour", Scope: "standard", UsedPercent: &used, PeriodHours: &fiveHours},
			{ID: "seven_day", Scope: "standard", UsedPercent: &used, PeriodHours: &weekly},
			{ID: "seven_day_sonnet", Kind: "model_scoped", Scope: "model", Model: "claude-3-5-sonnet", UsedPercent: &used, PeriodHours: &weekly},
		},
	}
	recorded, err := repo.RecordActiveQuotaObservations(ctx, value)
	if err != nil || recorded != 3 {
		t.Fatalf("recorded = %d (%v), want 3", recorded, err)
	}
	var active, passive int
	if err := repo.SQL().QueryRow(`SELECT SUM(source = 'active'), SUM(source = 'passive') FROM quota_observations`).Scan(&active, &passive); err != nil {
		t.Fatal(err)
	}
	if active != 3 || passive != 0 {
		t.Fatalf("sources = active %d passive %d, want 3/0", active, passive)
	}
	var scopedScope, scopedModel, kind string
	if err := repo.SQL().QueryRow(`SELECT scope, model, window_kind FROM quota_observations WHERE window_kind = 'model_scoped'`).Scan(&scopedScope, &scopedModel, &kind); err != nil {
		t.Fatal(err)
	}
	if scopedScope != "model" || scopedModel != "claude-3-5-sonnet" || kind != "model_scoped" {
		t.Fatalf("model window identity = %q/%q/%q", scopedScope, scopedModel, kind)
	}
	// A repeated write at the same instant is idempotent.
	if _, err := repo.RecordActiveQuotaObservations(ctx, value); err != nil {
		t.Fatal(err)
	}
	if count, _ := repo.CountQuotaObservations(ctx); count != 3 {
		t.Fatalf("after a repeated write count = %d, want 3", count)
	}
}

func TestLatestStandardPassiveObservationsPrefersNewestPerKind(t *testing.T) {
	repo := usageTestRepository(t)
	ctx := context.Background()
	insert := func(eventKey, authIndex, kind, source string, used float64, atMS int64) {
		t.Helper()
		if _, err := repo.SQL().Exec(`INSERT INTO quota_observations (auth_index, provider, window_kind, scope, model, window_minutes, used_percent, reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source)
			VALUES (?, 'claude', ?, 'standard', '', 10080, ?, NULL, '', ?, ?, 0, ?)`, authIndex, kind, used, atMS, eventKey, source); err != nil {
			t.Fatal(err)
		}
	}
	insert("p1", "cl-1", "five_hour", "passive", 10, 1000)
	insert("p2", "cl-1", "five_hour", "passive", 20, 2000)
	insert("a1", "cl-1", "five_hour", "active", 99, 3000)
	insert("p3", "cl-1", "weekly", "passive", 30, 1500)
	insert("p4", "cl-2", "weekly", "passive", 40, 1200)

	result, err := repo.LatestStandardPassiveObservations(ctx, []string{"cl-1", "cl-2"})
	if err != nil {
		t.Fatal(err)
	}
	if result["cl-1"]["five_hour"].UsedPercent != 20 || result["cl-1"]["five_hour"].ObservedAtMS != 2000 {
		t.Fatalf("cl-1 five_hour = %+v", result["cl-1"]["five_hour"])
	}
	if result["cl-1"]["weekly"].UsedPercent != 30 {
		t.Fatalf("cl-1 weekly = %+v", result["cl-1"]["weekly"])
	}
	if result["cl-2"]["weekly"].UsedPercent != 40 {
		t.Fatalf("cl-2 weekly = %+v", result["cl-2"]["weekly"])
	}
}

func TestCountUsageEventsByAuthIndexGroupsAndFilters(t *testing.T) {
	repo := usageTestRepository(t)
	ctx := context.Background()
	insert := func(eventKey, authIndex string, atMS int64) {
		t.Helper()
		if _, err := repo.SQL().Exec(`INSERT INTO usage_events (instance_id, event_key, api_group_key, auth_index, timestamp_ms, created_at_ms)
			VALUES ('default', ?, 'group', ?, ?, 0)`, eventKey, authIndex, atMS); err != nil {
			t.Fatal(err)
		}
	}
	insert("e1", "c-1", 5000)
	insert("e2", "c-1", 8000)
	insert("e3", "c-2", 9000)
	insert("e4", "c-1", 100) // outside the window

	counts, err := repo.CountUsageEventsByAuthIndex(ctx, "default", 1000)
	if err != nil {
		t.Fatal(err)
	}
	if counts["c-1"] != 2 || counts["c-2"] != 1 {
		t.Fatalf("counts = %+v", counts)
	}
}
