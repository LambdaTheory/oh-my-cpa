package quota

import (
	"context"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
)

func testSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		WatchInterval:        5 * time.Minute,
		ActiveInterval:       15 * time.Minute,
		NormalInterval:       30 * time.Minute,
		IdleInterval:         2 * time.Hour,
		WatchUsedPercent:     80,
		WatchWindow:          15 * time.Minute,
		NormalWindow:         2 * time.Hour,
		WindowEndMargin:      15 * time.Minute,
		PassiveFreshInterval: 60 * time.Minute,
		CredentialFloor:      10 * time.Minute,
		GlobalConcurrency:    4,
		ResetGrace:           time.Minute,
		ResetLookback:        time.Hour,
	}
}

func TestPlanRefreshTiers(t *testing.T) {
	cfg := testSchedulerConfig()
	now := time.Now()
	usedHigh, usedLow := 85.0, 10.0
	cases := []struct {
		name       string
		state      SchedulerState
		last       time.Time
		wantDue    bool
		wantWindow time.Duration
	}{
		{"watched due", SchedulerState{RequestsWatchWindow: 3, Windows: []SchedulerWindow{{UsedPercent: &usedHigh}}}, now.Add(-11 * time.Minute), true, 5 * time.Minute},
		{"active due", SchedulerState{RequestsWatchWindow: 3, Windows: []SchedulerWindow{{UsedPercent: &usedLow}}}, now.Add(-16 * time.Minute), true, 15 * time.Minute},
		{"active not due", SchedulerState{RequestsWatchWindow: 3, Windows: []SchedulerWindow{{UsedPercent: &usedLow}}}, now.Add(-14 * time.Minute), false, 15 * time.Minute},
		{"normal due", SchedulerState{RequestsNormalWindow: 2}, now.Add(-31 * time.Minute), true, 30 * time.Minute},
		{"idle due", SchedulerState{}, now.Add(-3 * time.Hour), true, 2 * time.Hour},
		{"idle not due", SchedulerState{}, now.Add(-1 * time.Hour), false, 2 * time.Hour},
	}
	for _, test := range cases {
		plan := PlanRefresh(test.state, cfg, now, test.last)
		if plan.ShouldRefresh != test.wantDue || plan.Interval != test.wantWindow {
			t.Fatalf("%s: plan = %+v, want due=%v interval=%v", test.name, plan, test.wantDue, test.wantWindow)
		}
	}
}

func TestPlanRefreshWidensToThePassiveFreshFloor(t *testing.T) {
	cfg := testSchedulerConfig()
	now := time.Now()
	usedHigh := 90.0
	state := SchedulerState{
		RequestsWatchWindow: 5,
		LastSnapshotAtMS:    now.Add(-2 * time.Minute).UnixMilli(),
		LatestPassiveAtMS:   now.Add(-30 * time.Second).UnixMilli(),
		Windows:             []SchedulerWindow{{UsedPercent: &usedHigh}},
	}
	plan := PlanRefresh(state, cfg, now, now.Add(-6*time.Minute))
	if plan.Interval != cfg.PassiveFreshInterval {
		t.Fatalf("interval = %v, want the passive floor %v", plan.Interval, cfg.PassiveFreshInterval)
	}
	if plan.ShouldRefresh {
		t.Fatal("a freshly passive-covered credential was actively refreshed early")
	}

	// A passive reading newer than the snapshot but older than the tier interval
	// does not widen it: the stream has stalled, so the active read is due again.
	stale := state
	stale.LastSnapshotAtMS = now.Add(-25 * time.Minute).UnixMilli()
	stale.LatestPassiveAtMS = now.Add(-20 * time.Minute).UnixMilli()
	stalePlan := PlanRefresh(stale, cfg, now, now.Add(-11*time.Minute))
	if stalePlan.Interval != cfg.WatchInterval || !stalePlan.ShouldRefresh {
		t.Fatalf("stale passive plan = %+v, want the watch interval to apply", stalePlan)
	}
}

func TestPlanRefreshForcesAReadNearTheWindowEnd(t *testing.T) {
	cfg := testSchedulerConfig()
	now := time.Now()
	usedLow := 3.0
	resetAt := now.Add(5 * time.Minute).UnixMilli()
	state := SchedulerState{
		RequestsNormalWindow: 4,
		Windows: []SchedulerWindow{{
			Kind:                    PassiveWindowWeekly,
			ResetAtMS:               &resetAt,
			UsedPercent:             &usedLow,
			HasTrafficInCycle:       true,
			HasRecentEnoughSnapshot: false,
		}},
	}
	plan := PlanRefresh(state, cfg, now, now.Add(-31*time.Minute))
	if !plan.ShouldRefresh || plan.Reason != "window_end" {
		t.Fatalf("plan = %+v, want a window_end read", plan)
	}
}

func TestSchedulerLoopRefreshesAndRespectsBackoff(t *testing.T) {
	cfg := testSchedulerConfig()
	now := time.Now()
	backoff := NewBackoff(time.Minute, time.Hour)
	scheduler := NewScheduler(cfg, nil, backoff)
	scheduler.Now = func() time.Time { return now }

	refreshes, persisted := 0, 0
	rateLimited := false
	scheduler.ListFiles = func(context.Context) ([]management.AuthFile, error) {
		return []management.AuthFile{{AuthIndex: "c-1", Type: "codex", Provider: "codex"}}, nil
	}
	scheduler.RequestCounts = func(context.Context, int64) (map[string]int64, error) {
		return map[string]int64{}, nil
	}
	scheduler.Prior = func(context.Context, string) *NormalizedQuota { return nil }
	scheduler.State = func(context.Context, management.AuthFile, *NormalizedQuota, int64, int64) (SchedulerState, error) {
		return SchedulerState{}, nil
	}
	scheduler.Refresh = func(context.Context, management.AuthFile, *NormalizedQuota) (*NormalizedQuota, error) {
		refreshes++
		if rateLimited {
			return nil, &RateLimitError{AuthIndex: "c-1", RetryAfter: 30 * time.Second, HasRetryAfter: true}
		}
		return &NormalizedQuota{AuthIndex: "c-1", Provider: "codex", ObservedAtMS: now.UnixMilli()}, nil
	}
	scheduler.Persist = func(context.Context, *NormalizedQuota) error { persisted++; return nil }

	ctx := context.Background()
	scheduler.runOnce(ctx)
	if refreshes != 1 || persisted != 1 {
		t.Fatalf("first pass refreshes=%d persisted=%d, want 1/1", refreshes, persisted)
	}
	// The same credential is not re-queried inside the floor.
	scheduler.runOnce(ctx)
	if refreshes != 1 {
		t.Fatalf("second pass refreshes=%d, want the credential floor to hold it", refreshes)
	}

	// A 429 parks the credential; a later tick past the floor must not hit it.
	rateLimited = true
	now = now.Add(3 * time.Hour)
	scheduler.runOnce(ctx)
	if refreshes != 2 {
		t.Fatalf("refreshes=%d, want the 429 probe to have run", refreshes)
	}
	if blocked, _ := backoff.CredentialBlocked("c-1", now); !blocked {
		t.Fatal("a 429 did not park the credential")
	}
	now = now.Add(time.Second)
	scheduler.runOnce(ctx)
	if refreshes != 2 {
		t.Fatalf("refreshes=%d, want the parked credential skipped", refreshes)
	}
}
