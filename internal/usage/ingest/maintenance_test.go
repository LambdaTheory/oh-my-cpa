package ingest

import (
	"context"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

func TestMaintenanceBackfillsQuotaObservations(t *testing.T) {
	store := newStore(t)
	ctx := context.Background()
	headers := usage.EncodeResponseHeaders([]usage.ResponseHeader{
		{Name: "x-codex-primary-used-percent", Value: "42"},
		{Name: "x-codex-primary-window-minutes", Value: "10080"},
		{Name: "x-codex-primary-reset-at", Value: "1700003600"},
	})
	if _, err := store.SQL().Exec(`INSERT INTO usage_events (instance_id, event_key, auth_index, api_group_key, timestamp_ms, created_at_ms, response_headers)
		VALUES ('default', 'backfill-me', 'c-1', 'group', 1000, 0, ?)`, headers); err != nil {
		t.Fatal(err)
	}
	maintenance, err := NewMaintenance(store, nil, time.Second, 90, 7)
	if err != nil {
		t.Fatal(err)
	}
	maintenance.backfillQuotaOnce(ctx)
	if count, err := store.CountQuotaObservations(ctx); err != nil || count != 1 {
		t.Fatalf("observations = %d (%v), want 1", count, err)
	}
	if observed := maintenance.Status().Observed; observed != 1 {
		t.Fatalf("reported observed = %d, want 1", observed)
	}
	// A second sweep has no work left.
	maintenance.backfillQuotaOnce(ctx)
	if observed := maintenance.Status().Observed; observed != 1 {
		t.Fatalf("a repeated sweep reprocessed records: %d", observed)
	}
}

func TestMaintenanceKeepsQuotaObservationsBeyondRequestRetention(t *testing.T) {
	store := newStore(t)
	// Request retention off must not remove the fixed observation horizon.
	maintenance, err := NewMaintenance(store, nil, time.Second, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	maintenance.now = func() time.Time { return now }
	cutoffs := maintenance.lifecycleCutoffs()
	horizon, declared := cutoffs[repository.LifecycleQuotaObservations]
	if !declared {
		t.Fatalf("quota observation horizon is not published: %+v", cutoffs)
	}
	want := now.Add(-365 * 24 * time.Hour).UnixMilli()
	if horizon != want {
		t.Fatalf("quota observation horizon = %d, want %d", horizon, want)
	}
	if _, published := cutoffs[repository.LifecycleUsageDetail]; published {
		t.Fatal("request-record retention was published despite being off")
	}
}
