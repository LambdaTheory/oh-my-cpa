package repository

import (
	"context"
	"errors"
	"testing"
)

// TestRemoveAutomationAccountSnapshotsThePlanBeforeTheTombstone pins the approved removal
// rule: the audit snapshot of the intent and plan that is about to be discarded is written
// in the same transaction as the tombstone, so nothing is cleared invisibly. The snapshot is
// an allowlist, so credential material can never be recorded through it.
func TestRemoveAutomationAccountSnapshotsThePlanBeforeTheTombstone(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	now := int64(1_000_000_000_000)
	reset := now + 3_600_000
	observed := now - 60_000
	attempt := now + 60_000
	manual := now + 7_200_000
	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "batch-snapshot", CreatedAtMS: now - 1, SelectedJSON: `["synthetic"]`}, []AutomationAccount{{
		AuthIndex: "synthetic", Provider: "codex", Name: "synthetic.json", AccountLabel: "synthetic@example.test",
		State: AutomationStateWaiting, MonitorReason: automationReasonRecoveryScheduled,
		CycleResetAtMS: &reset, CycleKey: "synthetic:codex:cycle", DisableObservedAtMS: &observed, ManualResetAtMS: &manual,
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordAutomationFailure(ctx, "synthetic", 1, now, "synthetic upstream failure", attempt); err != nil {
		t.Fatal(err)
	}
	account, found, err := repo.GetAutomationAccount(ctx, "synthetic")
	if err != nil || !found {
		t.Fatalf("read account: %v %v", found, err)
	}
	if _, err := repo.RemoveAutomationAccount(ctx, "synthetic", account.IntentVersion, now+1, true); err != nil {
		t.Fatal(err)
	}
	stored, _, _ := repo.GetAutomationAccount(ctx, "synthetic")
	if stored.IsAdopted || stored.RemovedAtMS == nil {
		t.Fatalf("the row must be tombstoned: %+v", stored)
	}
	if stored.CycleResetAtMS != nil || stored.ManualResetAtMS != nil || stored.NextAttemptAtMS != nil ||
		stored.AttemptCount != 0 || stored.LastError != "" || stored.CycleKey != "" || stored.DisableObservedAtMS != nil {
		t.Fatalf("removal must clear executable plan and retry ownership: %+v", stored)
	}
	events, err := repo.ListAuditEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot map[string]any
	for _, event := range events {
		if event.Action == "account_automation.auto_remove" {
			snapshot = event.Details
		}
	}
	if snapshot == nil {
		t.Fatalf("removal must record its snapshot: %+v", events)
	}
	for key, want := range map[string]any{
		"provider": "codex", "name": "synthetic.json", "state": AutomationStateError,
		"monitor_reason": automationReasonRecoveryScheduled, "cycle_reset_at_ms": float64(reset),
		"cycle_key": "synthetic:codex:cycle", "disable_observed_at_ms": float64(observed),
		"manual_reset_at_ms": float64(manual), "next_attempt_at_ms": float64(attempt),
		"last_error": "synthetic upstream failure", "removal": "confirmed_absence",
	} {
		if snapshot[key] != want {
			t.Fatalf("snapshot[%s] = %v, want %v (snapshot %+v)", key, snapshot[key], want, snapshot)
		}
	}
	if _, leaked := snapshot["account_label"]; leaked {
		t.Fatalf("the snapshot must not carry the account label: %+v", snapshot)
	}
}

// TestRemoveAutomationAccountRollsBackWhenTheSnapshotCannotBeWritten proves the audit is a
// precondition, not a best effort: a store that refuses the snapshot leaves the plan intact.
func TestRemoveAutomationAccountRollsBackWhenTheSnapshotCannotBeWritten(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "b", CreatedAtMS: 1000}, []AutomationAccount{{AuthIndex: "synthetic"}}, true, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SQL().ExecContext(ctx, `CREATE TRIGGER reject_automation_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(ABORT, 'synthetic audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RemoveAutomationAccount(ctx, "synthetic", 1, 1001, true); err == nil {
		t.Fatal("audit failure must reject removal")
	}
	account, _, _ := repo.GetAutomationAccount(ctx, "synthetic")
	if !account.IsAdopted || account.RemovedAtMS != nil || account.IntentVersion != 1 {
		t.Fatalf("an unwritable snapshot must roll the removal back: %+v", account)
	}
}

// TestFreshAdoptionOfARemovedRowStartsANewLifecycle proves a reappearing credential is a
// fresh engagement: a new adoption instant and batch, the requested switch, Credit off, and
// no inherited recovery, retry, failure or action ownership. History rows stay readable.
func TestFreshAdoptionOfARemovedRowStartsANewLifecycle(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	first := int64(1_000_000_000_000)
	reset := first + 3_600_000
	observed := first - 60_000
	manual := first + 7_200_000
	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "batch-old", CreatedAtMS: first - 10_000}, []AutomationAccount{{
		AuthIndex: "synthetic", Provider: "codex", Name: "synthetic.json",
		CycleResetAtMS: &reset, CycleKey: "synthetic:codex:cycle", DisableObservedAtMS: &observed,
		State: AutomationStateDisabled,
	}}, true, first); err != nil {
		t.Fatal(err)
	}
	credit := true
	account, err := repo.SetAutomationOptions(ctx, "synthetic", nil, &credit, 1, first+1)
	if err != nil {
		t.Fatal(err)
	}
	account, err = repo.RecordAutomationFailure(ctx, "synthetic", account.IntentVersion, first+2, "synthetic failure", first+120_000)
	if err != nil {
		t.Fatal(err)
	}
	account, err = repo.SetAutomationManualReset(ctx, "synthetic", manual, account.IntentVersion, first+3)
	if err != nil {
		t.Fatal(err)
	}
	stored, _, _ := repo.GetAutomationAccount(ctx, "synthetic")
	if _, err := repo.RemoveAutomationAccount(ctx, "synthetic", account.IntentVersion, first+4, true); err != nil {
		t.Fatal(err)
	}
	oldEvents, err := repo.ListAuditEvents(ctx, 10)
	if err != nil || len(oldEvents) == 0 {
		t.Fatalf("removal history: %+v %v", oldEvents, err)
	}

	// The credential reappears and is explicitly adopted again, with the switch off.
	second := first + 100_000
	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "batch-new", CreatedAtMS: second, SelectedJSON: `["synthetic"]`}, []AutomationAccount{{
		AuthIndex: "synthetic", Provider: "codex", Name: "synthetic.json", State: AutomationStateIdle,
	}}, false, second); err != nil {
		t.Fatal(err)
	}
	fresh, found, err := repo.GetAutomationAccount(ctx, "synthetic")
	if err != nil || !found {
		t.Fatalf("read fresh row: %v %v", found, err)
	}
	if !fresh.IsAdopted || fresh.RemovedAtMS != nil {
		t.Fatalf("fresh adoption must reactivate the row: %+v", fresh)
	}
	if fresh.AdoptBatchID != "batch-new" || fresh.AdoptedAtMS == nil || *fresh.AdoptedAtMS != second {
		t.Fatalf("fresh adoption must record its own batch and instant: %+v", fresh)
	}
	if fresh.IsEnabled || fresh.BurnCredit {
		t.Fatalf("fresh adoption takes the requested switch and Credit off: %+v", fresh)
	}
	if fresh.CycleResetAtMS != nil || fresh.CycleKey != "" || fresh.DisableObservedAtMS != nil ||
		fresh.ManualResetAtMS != nil || fresh.ManualResetConsumed || fresh.NextAttemptAtMS != nil ||
		fresh.AttemptCount != 0 || fresh.LastError != "" || fresh.LastDisableAtMS != nil || fresh.LastEnableAtMS != nil {
		t.Fatalf("fresh adoption must not inherit plan, Credit, retry or action ownership: %+v", fresh)
	}
	if fresh.IntentVersion <= stored.IntentVersion {
		t.Fatalf("fresh adoption must advance the intent version: %d -> %d", stored.IntentVersion, fresh.IntentVersion)
	}
	events, err := repo.ListAuditEvents(ctx, 10)
	if err != nil || len(events) < len(oldEvents) {
		t.Fatalf("removal history must survive re-adoption: %+v %v", events, err)
	}
}

// TestActiveIdenticalAdoptionPreservesThePlanAndARelabelledIdentityIsRefused pins the
// idempotent half: re-adopting the same current credential changes nothing, while the same
// auth index observed with a different provider or name is not silently relabelled under the
// old plan.
func TestActiveIdenticalAdoptionPreservesThePlanAndARelabelledIdentityIsRefused(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	now := int64(1_000_000_000_000)
	reset := now + 3_600_000
	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "batch-1", CreatedAtMS: now}, []AutomationAccount{{
		AuthIndex: "synthetic", Provider: "codex", Name: "synthetic.json", CycleResetAtMS: &reset, State: AutomationStateWaiting,
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	before, _, _ := repo.GetAutomationAccount(ctx, "synthetic")
	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "batch-2", CreatedAtMS: now + 1}, []AutomationAccount{{
		AuthIndex: "synthetic", Provider: "codex", Name: "synthetic.json", State: AutomationStateMonitoring,
	}}, true, now+1); err != nil {
		t.Fatalf("identical re-adoption must be idempotent: %v", err)
	}
	after, _, _ := repo.GetAutomationAccount(ctx, "synthetic")
	if after.AdoptBatchID != before.AdoptBatchID || after.AdoptedAtMS == nil || *after.AdoptedAtMS != *before.AdoptedAtMS ||
		after.CycleResetAtMS == nil || *after.CycleResetAtMS != reset || after.State != before.State || after.IntentVersion != before.IntentVersion {
		t.Fatalf("identical re-adoption changed the plan: before=%+v after=%+v", before, after)
	}

	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "batch-3", CreatedAtMS: now + 2}, []AutomationAccount{{
		AuthIndex: "synthetic", Provider: "claude", Name: "renamed.json",
	}}, true, now+2); !errors.Is(err, ErrAutomationIdentityConflict) {
		t.Fatalf("relabelled adoption = %v, want ErrAutomationIdentityConflict", err)
	}
	unchanged, _, _ := repo.GetAutomationAccount(ctx, "synthetic")
	if unchanged.Provider != "codex" || unchanged.Name != "synthetic.json" || unchanged.CycleResetAtMS == nil {
		t.Fatalf("a refused relabel must leave the plan untouched: %+v", unchanged)
	}
}

// TestFreshAdoptionRefusesAPlanFromTheEndedLifecycle pins the store-side half of the adoption
// race: a caller that read the previous lifecycle's removal instant before this row was
// tombstoned can offer a plan built from a reading that belongs to the engagement that just
// ended. The transaction re-reads the row and refuses such a plan instead of persisting it as
// the new lifecycle's recovery, so no old reading can ever schedule an action for a new
// incarnation of the same auth index.
func TestFreshAdoptionRefusesAPlanFromTheEndedLifecycle(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	first := int64(1_000_000_000_000)
	oldObserved := first - 60_000
	oldReset := first - 1_000
	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "old", CreatedAtMS: first - 10_000}, []AutomationAccount{{
		AuthIndex: "synthetic", Provider: "codex", Name: "synthetic.json",
	}}, true, first); err != nil {
		t.Fatal(err)
	}
	removedAt := first + 5_000
	if _, err := repo.RemoveAutomationAccount(ctx, "synthetic", 1, removedAt, true); err != nil {
		t.Fatal(err)
	}

	// A plan derived from the ended lifecycle's reading. Its observation predates the removal,
	// so it can only describe the engagement that has already been tombstoned.
	offered := AutomationAccount{
		AuthIndex: "synthetic", Provider: "codex", Name: "synthetic.json",
		CycleResetAtMS: &oldReset, CycleKey: "synthetic:codex:old", DisableObservedAtMS: &oldObserved,
		State: AutomationStateWaiting,
	}
	if _, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "new", CreatedAtMS: removedAt + 1}, []AutomationAccount{offered}, true, removedAt+1); !errors.Is(err, ErrAutomationConflict) {
		t.Fatalf("a plan from the ended lifecycle = %v, want ErrAutomationConflict", err)
	}
	unchanged, _, _ := repo.GetAutomationAccount(ctx, "synthetic")
	if unchanged.RemovedAtMS == nil || unchanged.IsAdopted {
		t.Fatalf("a refused fresh adoption must leave the tombstone untouched: %+v", unchanged)
	}

	// A reading recorded after the removal belongs to the new engagement and is admitted, so
	// the guard refuses only what the previous lifecycle produced.
	freshObserved := removedAt + 1_000
	freshReset := removedAt + 3_600_000
	offered.DisableObservedAtMS = &freshObserved
	offered.CycleResetAtMS = &freshReset
	offered.CycleKey = "synthetic:codex:new"
	if adopted, _, err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "new-2", CreatedAtMS: freshObserved + 1}, []AutomationAccount{offered}, true, freshObserved+1); err != nil || adopted != 1 {
		t.Fatalf("a reading recorded after the removal must be admitted: %d %v", adopted, err)
	}
	fresh, _, _ := repo.GetAutomationAccount(ctx, "synthetic")
	if fresh.RemovedAtMS != nil || !fresh.IsAdopted || fresh.DisableObservedAtMS == nil || *fresh.DisableObservedAtMS != freshObserved {
		t.Fatalf("the admitted plan must be the post-removal reading: %+v", fresh)
	}
}
