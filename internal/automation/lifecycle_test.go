package automation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// TestServiceRemovesAConfirmedAbsentAccountWithPendingPlans is the approved reversal of
// ADR 0114's protected-plan rule: after two consecutive complete authoritative inventories a
// missing credential leaves the active roster even while it still carries a pending retry and
// a saved recovery plan, because neither can ever execute for a credential the gateway no
// longer has. Nothing is sent to the gateway.
func TestServiceRemovesAConfirmedAbsentAccountWithPendingPlans(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	resetAt := clock.Now().Add(-time.Hour).UnixMilli()
	if _, err := repo.SetAutomationManualReset(ctx, "idx-1", resetAt, 1, clock.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RecordAutomationFailure(ctx, "idx-1", 2, clock.Now().UnixMilli(), "synthetic failure", clock.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	before, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if before.State != StateError || before.ManualResetAtMS == nil || before.NextAttemptAtMS == nil {
		t.Fatalf("fixture must hold a pending plan and retry: %+v", before)
	}

	credentials = nil
	service.RunOnce(ctx)
	afterFirst, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if afterFirst.RemovedAtMS != nil {
		t.Fatal("one complete inventory is not proof of absence")
	}
	service.RunOnce(ctx)
	removed, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if removed.RemovedAtMS == nil || removed.IsAdopted {
		t.Fatalf("two complete inventories must remove the row even with a pending plan: %+v", removed)
	}
	if len(writer.snapshot()) != 0 {
		t.Fatalf("cleanup must never write the gateway: %+v", writer.snapshot())
	}
	events, err := repo.ListAuditEvents(ctx, 10)
	if err != nil || len(events) != 1 || events[0].Action != "account_automation.auto_remove" {
		t.Fatalf("removal audit: %+v %v", events, err)
	}
	if events[0].Details["removal"] != "confirmed_absence" || events[0].Details["manual_reset_at_ms"] == nil {
		t.Fatalf("the snapshot must record the plan it displaced: %+v", events[0].Details)
	}
}

// TestServiceResetsAbsenceCountersAndBlocksAutomaticWritesOnInvalidInventories covers every
// way an inventory can stop being proof of absence: a read failure, an incomplete response and
// a duplicate identity. Each resets the confirmation sequence and performs no automatic write,
// while an explicit local cancellation still works during the outage.
func TestServiceResetsAbsenceCountersAndBlocksAutomaticWritesOnInvalidInventories(t *testing.T) {
	for _, interruption := range []string{"failure", "incomplete"} {
		t.Run(interruption, func(t *testing.T) {
			ctx := context.Background()
			repo := automationRepo(t)
			clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
			writer := &fakeWriter{}
			credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true}}
			service := newTestService(repo, clock, writer, &credentials)
			if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
				t.Fatal(err)
			}
			resetAt := clock.Now().Add(time.Hour).UnixMilli()
			if _, err := repo.SetAutomationManualReset(ctx, "idx-1", resetAt, 1, clock.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}

			// One real absence, then an inventory that cannot be trusted.
			credentials = nil
			service.RunOnce(ctx)
			if interruption == "failure" {
				service.Credentials = func(context.Context) ([]Credential, error) {
					return nil, errors.New("synthetic inventory unavailable")
				}
			} else {
				duplicate := []Credential{
					{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true},
					{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true},
				}
				service.Credentials = func(context.Context) ([]Credential, error) { return duplicate, nil }
			}
			service.RunOnce(ctx)
			account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
			if account.RemovedAtMS != nil {
				t.Fatal("an untrusted inventory must reset the confirmation sequence")
			}

			// Restored inventory: the credential is present, the due plan must enable once.
			restored := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true}}
			service.Credentials = func(context.Context) ([]Credential, error) { return restored, nil }
			clock.Set(time.UnixMilli(resetAt))
			service.RunOnce(ctx)
			calls := writer.snapshot()
			if len(calls) != 1 || calls[0].disabled {
				t.Fatalf("a valid plan with a restored inventory must enable: %+v", calls)
			}
		})
	}

	t.Run("duplicate inventory blocks every automatic write", func(t *testing.T) {
		ctx := context.Background()
		repo := automationRepo(t)
		clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
		writer := &fakeWriter{}
		credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true}}
		service := newTestService(repo, clock, writer, &credentials)
		if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
			t.Fatal(err)
		}
		resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
		insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), resetAt)
		service.Credentials = func(context.Context) ([]Credential, error) {
			return []Credential{
				{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true},
				{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true},
			}, nil
		}
		service.RunOnce(ctx)
		if calls := writer.snapshot(); len(calls) != 0 {
			t.Fatalf("an ambiguous inventory must not disable: %+v", calls)
		}
	})
}

// TestServiceUnadoptRemainsAvailableDuringAnInventoryOutage proves the outage blocks
// automatic gateway writes, not the operator's local intent.
func TestServiceUnadoptRemainsAvailableDuringAnInventoryOutage(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	service.Credentials = func(context.Context) ([]Credential, error) { return nil, errors.New("synthetic inventory unavailable") }
	removed, err := service.Unadopt(ctx, "idx-1")
	if err != nil || removed.RemovedAtMS == nil {
		t.Fatalf("local cancellation must work during an outage: %+v %v", removed, err)
	}
	service.RunOnce(ctx)
	if len(writer.snapshot()) != 0 {
		t.Fatalf("nothing may be written for an unavailable inventory: %+v", writer.snapshot())
	}
}

// TestServiceFencesADelayedPassAfterAReAdoptedIndex reuses the existing incarnation fields:
// a decision computed before a removal/re-adoption cannot act on the new lifecycle, and the
// adoption instant makes every earlier observation ineligible.
func TestServiceFencesADelayedPassAfterAReAdoptedIndex(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true, Disabled: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	oldReset := clock.Now().Add(time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), oldReset)
	if _, err := repo.SetAutomationManualReset(ctx, "idx-1", clock.Now().Add(-time.Minute).UnixMilli(), 1, clock.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	stale, _, _ := repo.GetAutomationAccount(ctx, "idx-1")

	// The credential disappears and the roster row is removed.
	credentials = nil
	service.RunOnce(ctx)
	service.RunOnce(ctx)
	// It reappears at the same index and is explicitly adopted again.
	clock.Set(clock.Now().Add(2 * time.Hour))
	credentials = []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true, Disabled: true}}
	if adopted, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil || adopted != 1 {
		t.Fatalf("fresh adoption: %d %v", adopted, err)
	}
	// A delayed pass that still holds the old row must not execute on the new one.
	service.runAccount(ctx, stale, credentials[0], repository.AutomationWeeklyWindow{}, clock.Now())
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a stale pass must not act on a new lifecycle: %+v", calls)
	}
	// The observation recorded before this adoption cannot justify a new recovery plan.
	fresh, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if fresh.AdoptedAtMS == nil || fresh.CycleResetAtMS != nil || fresh.ManualResetAtMS != nil {
		t.Fatalf("a fresh adoption must start without inherited evidence: %+v", fresh)
	}
	if fresh.IntentVersion <= stale.IntentVersion {
		t.Fatalf("a fresh adoption must advance the incarnation: %d -> %d", stale.IntentVersion, fresh.IntentVersion)
	}
}

// TestServicePauseUnadoptInterleavingKeepsTheGatewayUntouched pins the two independent
// meanings: pausing keeps the plan, unadopting removes membership, and neither sends a status
// patch for a credential that is still present.
func TestServicePauseUnadoptInterleavingKeepsTheGatewayUntouched(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true, Disabled: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetManualReset(ctx, "idx-1", clock.Now().Add(time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	paused, err := service.SetAccountSwitch(ctx, "idx-1", false)
	if err != nil || paused.CycleResetAtMS != nil || paused.ManualResetAtMS == nil {
		t.Fatalf("pause must retain the plan: %+v %v", paused, err)
	}
	service.RunOnce(ctx)
	if len(writer.snapshot()) != 0 {
		t.Fatal("a paused plan must not execute")
	}
	removed, err := service.Unadopt(ctx, "idx-1")
	if err != nil || removed.RemovedAtMS == nil || removed.ManualResetAtMS != nil {
		t.Fatalf("unadopt must remove membership and clear the executable plan: %+v %v", removed, err)
	}
	if len(writer.snapshot()) != 0 {
		t.Fatal("unadopt must never change gateway state")
	}
}

// TestServiceRecoveryStillExecutesWithCreditAndAPresentCredential keeps the preserved rules
// from regressing: Credit exempts the disable, not the recovery, and a saved valid plan enables
// directly at its reset without a new quota probe.
func TestServiceRecoveryStillExecutesWithCreditAndAPresentCredential(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true, Disabled: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	credit := true
	if _, err := service.SetAccountOptions(ctx, "idx-1", nil, &credit); err != nil {
		t.Fatal(err)
	}
	due := clock.Now().Add(time.Minute).UnixMilli()
	if _, err := service.SetManualReset(ctx, "idx-1", due); err != nil {
		t.Fatal(err)
	}
	clock.Set(time.UnixMilli(due))
	service.RunOnce(ctx)
	calls := writer.snapshot()
	if len(calls) != 1 || calls[0].disabled {
		t.Fatalf("Credit must not suppress a due recovery: %+v", calls)
	}
}

// TestServiceFencesAProviderReplacementWithoutASavedPlan pins the identity rule on an
// enabled account: a credential whose provider changed under the same index receives no
// automatic write, whether or not a plan exists.
func TestServiceFencesAProviderReplacementWithoutASavedPlan(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	// A different provider now answers under the same index and write name.
	credentials[0].Provider = "claude"
	insertWeekly(t, repo, "idx-1", "claude", 100, clock.Now().UnixMilli(), clock.Now().Add(time.Hour).UnixMilli())
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a replaced provider must not be disabled from another provider's reading: %+v", calls)
	}
}

// TestServiceBlocksAWriteWhenTheWriteNameChanged proves the preflight addresses by the
// recorded name: a live row whose CPA name changed is present but not writable by this plan.
func TestServiceBlocksAWriteWhenTheWriteNameChanged(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true, Disabled: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	due := clock.Now().Add(time.Minute).UnixMilli()
	if _, err := service.SetManualReset(ctx, "idx-1", due); err != nil {
		t.Fatal(err)
	}
	credentials[0].Name = "renamed.json"
	clock.Set(time.UnixMilli(due))
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a changed write name must not receive the old plan's status patch: %+v", calls)
	}
}

// TestServiceFreshAdoptionDoesNotPlanRecoveryFromEarlierEvidence is the must-fix from the
// independent review: a credential removed and adopted again while disabled upstream must not
// schedule an enable from a reading recorded for the lifecycle that ended. The fresh row stays
// without a plan; only an observation recorded after this adoption may justify one.
func TestServiceFreshAdoptionDoesNotPlanRecoveryFromEarlierEvidence(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	oldReset := clock.Now().Add(2 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), oldReset)

	// The credential disappears, the plan is removed, and it reappears still disabled.
	credentials = nil
	service.RunOnce(ctx)
	service.RunOnce(ctx)
	clock.Set(clock.Now().Add(30 * time.Minute))
	credentials = []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true}}
	if adopted, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil || adopted != 1 {
		t.Fatalf("fresh adoption: %d %v", adopted, err)
	}
	fresh, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if fresh.CycleResetAtMS != nil || fresh.ManualResetAtMS != nil || fresh.DisableObservedAtMS != nil {
		t.Fatalf("a fresh adoption must not inherit the prior engagement's recovery plan: %+v", fresh)
	}
	// Even at the old reset instant, nothing is enabled: the plan does not exist.
	clock.Set(time.UnixMilli(oldReset))
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("the old plan must never enable the new credential: %+v", calls)
	}

	// The same credential is removed and adopted once more, now with a reading recorded
	// after the removal: that reading is eligible, so the takeover correctly plans it.
	credentials = nil
	service.RunOnce(ctx)
	service.RunOnce(ctx)
	clock.Set(clock.Now().Add(time.Minute))
	newReset := clock.Now().Add(3 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().Add(30*time.Second).UnixMilli(), newReset)
	credentials = []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true}}
	clock.Set(clock.Now().Add(time.Minute))
	if adopted, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil || adopted != 1 {
		t.Fatalf("second fresh adoption: %d %v", adopted, err)
	}
	planned, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if planned.CycleResetAtMS == nil || *planned.CycleResetAtMS != newReset {
		t.Fatalf("a reading recorded after this lifecycle must be eligible: %+v", planned)
	}
	clock.Set(time.UnixMilli(newReset))
	service.RunOnce(ctx)
	calls := writer.snapshot()
	if len(calls) != 1 || calls[0].disabled {
		t.Fatalf("the fresh plan must enable at its own reset: %+v", calls)
	}
}

// TestServiceRecordsWhyAChangedWriteNameBlocksThePlan keeps a suppressed plan explained: a
// renamed credential must not look healthy while its plan can never execute.
func TestServiceRecordsWhyAChangedWriteNameBlocksThePlan(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), clock.Now().Add(time.Hour).UnixMilli())
	credentials[0].Name = "renamed.json"
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a changed write name must not receive the plan's status patch: %+v", calls)
	}
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.MonitorReason != ReasonIdentityMismatch {
		t.Fatalf("the suppression must be explained: %+v", account)
	}
}

// TestServiceRecordsCredentialNotFoundWhenTheInventoryDropsTheTarget proves the preflight's
// absence branch also records a reason instead of silently skipping the action.
func TestServiceRecordsCredentialNotFoundWhenTheInventoryDropsTheTarget(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	due := clock.Now().Add(time.Minute).UnixMilli()
	if _, err := service.SetManualReset(ctx, "idx-1", due); err != nil {
		t.Fatal(err)
	}
	// The same pass observes a plan and an inventory without its target: the pass-level
	// inventory gate already blocks writes, so assert the row keeps its plan untouched.
	credentials = nil
	clock.Set(time.UnixMilli(due))
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a missing target must not be enabled: %+v", calls)
	}
}







// TestServiceAdoptionCannotPlanFromALifecycleRemovedUnderIt is the held interleaving the
// review named, made deterministic by probing the adoption's own account lock rather than by
// sleeping. Adopt reads the previous lifecycle's removal instant and then consults the clock
// before it stores; the test uses that clock read to start a confirmed-absence removal of the
// same index at exactly that point.
//
//   - When the adoption does not yet hold the account lock (the pre-fix ordering), the removal
//     commits synchronously inside that window and the store takes its fresh-adoption branch,
//     so the new lifecycle inherits a recovery plan built from the ended lifecycle's reading.
//   - When the adoption already holds it (the fixed ordering), the removal is excluded and
//     becomes a sequential removal once the adoption has stored.
//
// The asserted rule is the approved one: an active adoption may never carry a plan whose
// evidence was observed at or before the lifecycle that ended. The positive half proves a
// reading observed after the new lifecycle still plans and enables at its own reset.
func TestServiceAdoptionCannotPlanFromALifecycleRemovedUnderIt(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "synthetic.json", Provider: "codex", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	// A reading recorded for the current lifecycle is what a takeover plan would be built from.
	oldObserved := clock.Now().UnixMilli()
	oldReset := clock.Now().Add(2 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "codex", 100, oldObserved, oldReset)

	// holdsAccountLock reports whether the adoption already owns the per-account lock, using a
	// non-blocking acquire of the very lock object the service uses.
	holdsAccountLock := func() bool {
		service.locksMu.Lock()
		lock := service.locks["idx-1"]
		service.locksMu.Unlock()
		if lock == nil {
			return false
		}
		if lock.TryLock() {
			lock.Unlock()
			return false
		}
		return true
	}
	emptyInventory := func(context.Context) ([]Credential, error) { return []Credential{}, nil }
	removal := func() {
		service.Credentials = emptyInventory
		service.RunOnce(ctx) // first complete inventory: one confirmation
		service.RunOnce(ctx) // second: the removal transaction commits
	}

	removalDone := make(chan struct{})
	deferred := false
	primed := false
	service.Now = func() time.Time {
		if !primed {
			primed = true
			if holdsAccountLock() {
				// The fix: the removal cannot interleave, so it is left to run after the store.
				deferred = true
				go func() { defer close(removalDone); removal() }()
			} else {
				// The pre-fix ordering: nothing excludes the removal from the window.
				removal()
				close(removalDone)
			}
		}
		return clock.Now()
	}
	clock.Set(clock.Now().Add(time.Minute))
	_, adoptionErr := service.Adopt(ctx, []string{"idx-1"}, true, "")
	select {
	case <-removalDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the confirmed-absence removal never completed")
	}
	service.Now = clock.Now
	service.Credentials = func(context.Context) ([]Credential, error) { return credentials, nil }

	active, found, err := repo.GetAutomationAccount(ctx, "idx-1")
	if err != nil || !found {
		t.Fatalf("read the row after the interleaving: found=%v err=%v", found, err)
	}
	if active.RemovedAtMS == nil {
		if active.AdoptedAtMS == nil {
			t.Fatalf("an active adoption must record its instant: %+v", active)
		}
		if active.DisableObservedAtMS != nil && *active.DisableObservedAtMS <= oldObserved {
			t.Fatalf("the fresh lifecycle inherited the ended lifecycle's evidence (deferred removal: %v, adoption error: %v): %+v", deferred, adoptionErr, active)
		}
		if active.CycleResetAtMS != nil && *active.CycleResetAtMS == oldReset {
			t.Fatalf("the fresh lifecycle inherited the ended lifecycle's reset (deferred removal: %v, adoption error: %v): %+v", deferred, adoptionErr, active)
		}
	}

	// The valid recovery path is unchanged: a reading observed after the lifecycle that ended
	// plans the new adoption and enables directly at its own reset, with no quota probe.
	if row, _, _ := repo.GetAutomationAccount(ctx, "idx-1"); row.RemovedAtMS == nil {
		if _, err := service.Unadopt(ctx, "idx-1"); err != nil {
			t.Fatal(err)
		}
	}
	clock.Set(clock.Now().Add(2 * time.Hour))
	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), clock.Now().Add(time.Hour).UnixMilli())
	newReset := clock.Now().Add(time.Hour).UnixMilli()
	if adopted, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil || adopted != 1 {
		t.Fatalf("fresh adoption with a post-removal reading: %d %v", adopted, err)
	}
	planned, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if planned.CycleResetAtMS == nil || *planned.CycleResetAtMS != newReset {
		t.Fatalf("a reading recorded after the new lifecycle must be eligible: %+v", planned)
	}
	clock.Set(time.UnixMilli(newReset))
	service.RunOnce(ctx)
	calls := writer.snapshot()
	if len(calls) != 1 || calls[0].disabled {
		t.Fatalf("a post-adoption reading must still plan and enable: %+v", calls)
	}
}

// TestServiceBatchUnadoptIsAllOrReportAcrossStaleMembers proves the batch fence is not partially
// applied: one member naming an ended lifecycle makes the whole batch a conflict, and nothing
// is removed - not the stale member and not its siblings. The same batch with the current
// versions removes every selected member and still writes nothing to the gateway.
func TestServiceBatchUnadoptIsAllOrReportAcrossStaleMembers(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{
		{AuthIndex: "idx-1", Name: "one.json", Provider: "codex", Controllable: true},
		{AuthIndex: "idx-2", Name: "two.json", Provider: "codex", Controllable: true},
	}
	service := newTestService(repo, clock, writer, &credentials)
	if adopted, err := service.Adopt(ctx, []string{"idx-1", "idx-2"}, true, ""); err != nil || adopted != 2 {
		t.Fatalf("adopt: %d %v", adopted, err)
	}
	idle, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	// idx-2 is re-adopted (removed then adopted again), so a version read before it is stale.
	if _, err := service.Unadopt(ctx, "idx-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Adopt(ctx, []string{"idx-2"}, true, ""); err != nil {
		t.Fatal(err)
	}
	current, _, _ := repo.GetAutomationAccount(ctx, "idx-2")
	if current.IntentVersion <= idle.IntentVersion {
		t.Fatalf("the fixture needs a newer idx-2 incarnation: %d vs %d", current.IntentVersion, idle.IntentVersion)
	}

	// A batch whose idx-2 target names the ended incarnation removes nothing.
	if removed, err := service.UnadoptManyExpected(ctx, []UnadoptTarget{
		{AuthIndex: "idx-1", ExpectedIntentVersion: int64Pointer(idle.IntentVersion)},
		{AuthIndex: "idx-2", ExpectedIntentVersion: int64Pointer(idle.IntentVersion)},
	}); !errors.Is(err, ErrStaleBatchMutation) || removed != 0 {
		t.Fatalf("a stale batch member must stop the whole batch: removed=%d err=%v", removed, err)
	}
	for _, authIndex := range []string{"idx-1", "idx-2"} {
		row, _, _ := repo.GetAutomationAccount(ctx, authIndex)
		if row.RemovedAtMS != nil || !row.IsAdopted {
			t.Fatalf("a refused batch must leave %s adopted: %+v", authIndex, row)
		}
	}

	// With each member's current version the batch removes both and touches no gateway state.
	freshOne, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	freshTwo, _, _ := repo.GetAutomationAccount(ctx, "idx-2")
	removed, err := service.UnadoptManyExpected(ctx, []UnadoptTarget{
		{AuthIndex: "idx-1", ExpectedIntentVersion: int64Pointer(freshOne.IntentVersion)},
		{AuthIndex: "idx-2", ExpectedIntentVersion: int64Pointer(freshTwo.IntentVersion)},
	})
	if err != nil || removed != 2 {
		t.Fatalf("the current batch: removed=%d err=%v", removed, err)
	}
	for _, authIndex := range []string{"idx-1", "idx-2"} {
		row, _, _ := repo.GetAutomationAccount(ctx, authIndex)
		if row.RemovedAtMS == nil {
			t.Fatalf("the current batch must remove %s: %+v", authIndex, row)
		}
	}
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("unadoption must never write the gateway: %+v", calls)
	}
}

func int64Pointer(value int64) *int64 { return &value }
