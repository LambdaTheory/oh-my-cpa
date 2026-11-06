package feishu

import (
	"context"
	"testing"
	"time"
)

func loadClaimState(t *testing.T, env *testEnv) *State {
	t.Helper()
	state, err := env.store.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func claimItem(t *testing.T, state *State, id string) PendingItem {
	t.Helper()
	item, exists := state.pendingByID(id)
	if !exists {
		t.Fatalf("missing incident %s in %+v", id, state.PendingItems)
	}
	return item
}

func deliveredRiskCount(state *State, kind, key string) int {
	count := 0
	for _, row := range state.Outbox {
		if row.State != NotifySent || row.SentAt.IsZero() {
			continue
		}
		for _, alert := range row.Alerts {
			if alert.Kind == kind && alert.Key == key {
				count++
			}
		}
	}
	return count
}

func requireSameOpenIncident(t *testing.T, before, after PendingItem) {
	t.Helper()
	if after.Status != PendingOpen || !after.ResolvedAt.IsZero() || !after.Since.Equal(before.Since) {
		t.Errorf("expiry changed ownership: before=%+v after=%+v", before, after)
	}
}

// A failed transport attempt re-reads the report and requalifies the alert. That
// retry must not move the claim instant: the zero-send expiry still releases the
// reservation, so a genuinely fresh rejection is sent exactly once afterwards.
func TestFailedRetryReleasesItsReservationForOneFreshRejection(t *testing.T) {
	ctx := context.Background()
	env := newTestEnv(t, Report{}, true)
	base := mustTime("2026-11-06T12:00:00Z")
	*env.clock = base
	env.source.report = invalidReport(base)
	env.sender.failSends = 4
	env.service.tick(ctx)
	state := loadClaimState(t, env)
	id := ItemID(AlertCredential, "codex", "a1", "")
	original := claimItem(t, state, id)
	if sends, _ := env.sender.counts(); sends != 0 || len(state.Outbox) != 1 || state.Outbox[0].State != NotifyPending || state.Outbox[0].Attempts != 1 || !original.NotifiedAt.Equal(base) {
		t.Fatalf("first full/simple rejection was not persisted: sends=%d state=%+v", sends, state)
	}

	*env.clock = base.Add(time.Minute)
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	if sends, _ := env.sender.counts(); sends != 0 || len(state.Outbox) != 1 || state.Outbox[0].State != NotifyPending || state.Outbox[0].Attempts != 2 || len(state.Outbox[0].Alerts) != 1 {
		t.Fatalf("second full/simple rejection was not persisted: sends=%d state=%+v", sends, state)
	}
	if got := state.Outbox[0].Alerts[0].OccurredAt; !sameClaimInstant(got, original.NotifiedAt) {
		t.Errorf("requalification moved the claim instant: reservation=%s alert=%s", original.NotifiedAt, got)
	}

	restartMonitorService(env)
	*env.clock = base.Add(16 * time.Minute)
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	closed := claimItem(t, state, id)
	if sends, _ := env.sender.counts(); sends != 0 || len(state.Outbox) != 1 || !isDiscardedNotification(state.Outbox[0]) {
		t.Fatalf("expiry did not close with zero actual sends: sends=%d state=%+v", sends, state)
	}
	requireSameOpenIncident(t, original, closed)
	if !closed.NotifiedAt.IsZero() {
		t.Errorf("two failed attempts followed by expiry retained an undelivered reservation: %+v", closed)
	}

	*env.clock = base.Add(17 * time.Minute)
	env.source.report = invalidReport(*env.clock)
	env.service.tick(ctx)
	state = loadClaimState(t, env)
	requireSameOpenIncident(t, original, claimItem(t, state, id))
	if sends, _ := env.sender.counts(); sends != 1 || deliveredRiskCount(state, AlertCredential, "a1") != 1 {
		t.Errorf("a genuinely fresh rejection was swallowed after zero delivery: sends=%d state=%+v", sends, state)
	}
	*env.clock = env.clock.Add(time.Minute)
	env.source.report = invalidReport(*env.clock)
	env.service.tick(ctx)
	if sends, _ := env.sender.counts(); sends > 1 {
		t.Errorf("later fresh evidence replayed a delivered incident: sends=%d", sends)
	}
}
