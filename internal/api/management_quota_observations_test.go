package api

import (
	"context"
	"strconv"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
)

func insertPassiveObservation(t *testing.T, handler *Handler, authIndex, kind string, usedPercent float64, atMS int64) {
	t.Helper()
	eventKey := "passive-" + kind + "-" + strconv.FormatInt(atMS, 10)
	if _, err := handler.repo.SQL().ExecContext(context.Background(),
		`INSERT INTO quota_observations (auth_index, provider, window_kind, scope, model, window_minutes, used_percent, reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source)
		 VALUES (?, 'claude', ?, 'standard', '', 300, ?, ?, '', ?, ?, 0, 'passive')`,
		authIndex, kind, usedPercent, atMS+3600_000, atMS, eventKey); err != nil {
		t.Fatal(err)
	}
}

func TestOverlayPassiveObservationsPrefersTheFresherHeaderReading(t *testing.T) {
	handler := capacityTestHandler(t)
	ctx := context.Background()
	insertPassiveObservation(t, handler, "cl-1", "five_hour", 52, 2000)

	snapshotAt := int64(1000)
	used, remaining, resetAt, periodHours := 20.0, 80.0, int64(9000), 5.0
	value := &quota.NormalizedQuota{
		AuthIndex: "cl-1", Provider: "claude", ObservedAtMS: snapshotAt,
		Windows: []quota.QuotaWindow{
			{ID: "five_hour", Scope: "standard", UsedPercent: &used, RemainingPercent: &remaining, ResetAtMS: &resetAt, PeriodHours: &periodHours},
			// A model-scoped window has no passive counterpart and is untouched.
			{ID: "seven_day_sonnet", Kind: "model_scoped", Scope: "model", Model: "sonnet", UsedPercent: &used, PeriodHours: &periodHours},
		},
	}
	handler.overlayPassiveObservations(ctx, value)

	fiveHour := value.Windows[0]
	if fiveHour.UsedPercent == nil || *fiveHour.UsedPercent != 52 {
		t.Fatalf("used_percent = %v, want the passive 52", fiveHour.UsedPercent)
	}
	if fiveHour.RemainingPercent == nil || *fiveHour.RemainingPercent != 48 {
		t.Fatalf("remaining_percent = %v, want 48", fiveHour.RemainingPercent)
	}
	if fiveHour.ResetAtMS == nil || *fiveHour.ResetAtMS != 2000+3600_000 {
		t.Fatalf("reset_at_ms = %v, want the passive reset", fiveHour.ResetAtMS)
	}
	if fiveHour.ObservedAtMS == nil || *fiveHour.ObservedAtMS != 2000 || fiveHour.Source != "passive" {
		t.Fatalf("provenance = %v/%q, want the passive reading", fiveHour.ObservedAtMS, fiveHour.Source)
	}
	model := value.Windows[1]
	if model.UsedPercent == nil || *model.UsedPercent != 20 || model.Source != "" {
		t.Fatalf("model window = %+v, want it untouched", model)
	}
}

func TestOverlayPassiveObservationsKeepsAStaleHeaderReadingOut(t *testing.T) {
	handler := capacityTestHandler(t)
	ctx := context.Background()
	insertPassiveObservation(t, handler, "cl-1", "five_hour", 52, 500)

	used, periodHours := 20.0, 5.0
	value := &quota.NormalizedQuota{
		AuthIndex: "cl-1", Provider: "claude", ObservedAtMS: 1000,
		Windows: []quota.QuotaWindow{{ID: "five_hour", Scope: "standard", UsedPercent: &used, PeriodHours: &periodHours}},
	}
	handler.overlayPassiveObservations(ctx, value)
	if value.Windows[0].Source != "" || *value.Windows[0].UsedPercent != 20 {
		t.Fatalf("a passive reading older than the snapshot replaced it: %+v", value.Windows[0])
	}
}
