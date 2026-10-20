package repository

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"
)

func TestAdoptAutomationAccountsKeepsAnExistingSwitch(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()

	first := AutomationBatch{ID: "batch-1", CreatedAtMS: now, SelectedCount: 2, SelectedJSON: `["a","b"]`, Note: "first"}
	if err := repo.AdoptAutomationAccounts(ctx, first, []AutomationAccount{
		{AuthIndex: "a", Provider: "codex", Name: "a.json", AccountLabel: "one@example.test"},
		{AuthIndex: "b", Provider: "claude", Name: "b.json"},
	}, false, now); err != nil {
		t.Fatal(err)
	}
	accounts, err := repo.ListAutomationAccounts(ctx)
	if err != nil || len(accounts) != 2 {
		t.Fatalf("adopted = %d, %v", len(accounts), err)
	}
	for _, account := range accounts {
		if !account.IsAdopted || account.IsEnabled {
			t.Fatalf("new adoption must be adopted and off: %+v", account)
		}
		if account.IntentVersion != 1 {
			t.Fatalf("new adoption must carry an intent version: %+v", account)
		}
	}

	// Turning the switch on, then re-adopting, must not reset the operator's choice.
	if _, err := repo.SetAutomationSwitch(ctx, "a", true, 1, now); err != nil {
		t.Fatal(err)
	}
	second := AutomationBatch{ID: "batch-2", CreatedAtMS: now, SelectedCount: 1, SelectedJSON: `["a"]`}
	if err := repo.AdoptAutomationAccounts(ctx, second, []AutomationAccount{{AuthIndex: "a", Provider: "codex", Name: "a.json"}}, false, now); err != nil {
		t.Fatal(err)
	}
	account, found, err := repo.GetAutomationAccount(ctx, "a")
	if err != nil || !found {
		t.Fatalf("read account: %v %v", found, err)
	}
	if !account.IsEnabled {
		t.Fatalf("re-adoption must keep an enabled switch: %+v", account)
	}
	if account.AdoptBatchID != "batch-1" {
		t.Fatalf("re-adoption must keep the original batch: %+v", account)
	}
}

func TestSetAutomationSwitchRejectsAStaleVersion(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	if err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "b", CreatedAtMS: now}, []AutomationAccount{{AuthIndex: "a"}}, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetAutomationSwitch(ctx, "a", true, 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetAutomationSwitch(ctx, "a", false, 1, now); !errors.Is(err, ErrAutomationConflict) {
		t.Fatalf("stale version = %v, want conflict", err)
	}
}

// TestLatestStandardWeeklyWindowsFiltersByRealPeriod pins the evidence rule: only
// a standard window whose stated period is a real seven days qualifies, so a
// Codex primary five-hour slot and a model-scoped Claude window are both excluded.
func TestLatestStandardWeeklyWindowsFiltersByRealPeriod(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	insert := func(authIndex, provider, kind, scope string, minutes int, used float64, observedAt int64, reset any) {
		t.Helper()
		if _, err := repo.SQL().ExecContext(ctx, `
			INSERT INTO quota_observations (
				auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
				reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source
			) VALUES (?, ?, ?, ?, '', ?, ?, ?, '', ?, ?, ?, 'active')`,
			authIndex, provider, kind, scope, minutes, used, reset, observedAt, authIndex+"-"+kind+"-"+scope+"-"+strconv.Itoa(minutes), observedAt); err != nil {
			t.Fatal(err)
		}
	}
	reset := int64(9_000_000_000_000)
	insert("idx-1", "codex", "weekly", "standard", 10080, 100, 1000, reset)
	insert("idx-1", "codex", "five_hour", "standard", 300, 100, 2000, reset)
	// A 168h ± 1h near-miss is newer than the real reading but is not the same window.
	insert("idx-1", "codex", "weekly", "standard", 10140, 100, 5000, reset)
	insert("idx-2", "claude", "seven_day", "model", 10080, 100, 3000, reset)
	insert("idx-3", "codex", "weekly", "standard", 10080, 40, 4000, reset)

	windows, err := repo.LatestStandardWeeklyWindows(ctx, []string{"idx-1", "idx-2", "idx-3"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := windows["idx-2"]; ok {
		t.Fatalf("a model-scoped window must not be account-level evidence: %+v", windows["idx-2"])
	}
	if window, ok := windows["idx-1"]; !ok || window.ObservedAtMS != 1000 || window.WindowKind != "weekly" {
		t.Fatalf("the exact seven-day reading is required, not the 5h one or a near-miss: %+v", windows["idx-1"])
	}
	if window, ok := windows["idx-3"]; !ok || window.UsedPercent != 40 {
		t.Fatalf("a partial weekly reading is still a reading: %+v", windows["idx-3"])
	}
}

// TestAutomationAccountLabelIsRedactedFromOperatorQueries records the decision
// that the roster's display label, which CPA often fills with the account's email,
// is not exposed to the raw SQL read path.
func TestAutomationAccountLabelIsRedactedFromOperatorQueries(t *testing.T) {
	repo := queryTestRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	if err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "b", CreatedAtMS: now}, []AutomationAccount{
		{AuthIndex: "a", Provider: "codex", Name: "a.json", AccountLabel: "secret@example.test"},
	}, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.ReadOnlyQuery(ctx, `SELECT account_label FROM account_automation_accounts`, 10); err == nil {
		t.Fatal("a redacted column must not be readable through the operator query path")
	}
	if _, err := repo.ReadOnlyQuery(ctx, `SELECT state, provider FROM account_automation_accounts`, 10); err != nil {
		t.Fatalf("the plan summary must stay readable: %v", err)
	}
}

// TestRecordAutomationEnableConsumesAManualReset pins the one-time semantics: the
// hand-filled recovery time schedules exactly one enable.
func TestRecordAutomationEnableConsumesAManualReset(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	cycleReset := now + 7200_000
	if err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "b", CreatedAtMS: now}, []AutomationAccount{{
		AuthIndex: "a", CycleResetAtMS: &cycleReset, State: AutomationStateWaiting,
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetAutomationManualReset(ctx, "a", now+3600_000, 1, now); err != nil {
		t.Fatal(err)
	}
	account, err := repo.RecordAutomationEnable(ctx, "a", 2, now, true)
	if err != nil {
		t.Fatal(err)
	}
	if !account.ManualResetConsumed {
		t.Fatalf("a manual reset must be consumed by its enable: %+v", account)
	}
	if account.NextAttemptAtMS != nil || account.AttemptCount != 0 || account.LastError != "" {
		t.Fatalf("a completed enable must clear the retry state: %+v", account)
	}
	if account.CycleResetAtMS != nil {
		t.Fatalf("a completed enable must clear the consumed cycle reset: %+v", account)
	}
}

func TestRecordAutomationResetClearsRecoveryTimes(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	resetAt := now + 3600_000
	if err := repo.AdoptAutomationAccounts(ctx, AutomationBatch{ID: "b", CreatedAtMS: now}, []AutomationAccount{{
		AuthIndex: "a", CycleResetAtMS: &resetAt, State: AutomationStateWaiting,
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.SetAutomationManualReset(ctx, "a", resetAt, 1, now); err != nil {
		t.Fatal(err)
	}
	account, err := repo.RecordAutomationReset(ctx, "a", 2, now, AutomationStateIdle, automationReasonExternallyReEnabled)
	if err != nil {
		t.Fatal(err)
	}
	if account.CycleResetAtMS != nil || !account.ManualResetConsumed {
		t.Fatalf("an external re-enable must consume every pending recovery time: %+v", account)
	}
}

// TestAdoptAutomationAccountsPersistsTheTakeoverRecoveryPlan pins the first takeover at
// the store: a plan offered on first adoption is written, it never becomes a disable, and
// a re-adoption keeps the plan the operator first had.
func TestAdoptAutomationAccountsPersistsTheTakeoverRecoveryPlan(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	now := time.Now().UTC().UnixMilli()
	resetAt := now + 48*60*60*1000
	observed := now - 1000
	first := AutomationBatch{ID: "batch-1", CreatedAtMS: now, SelectedCount: 1, SelectedJSON: `["a"]`}
	if err := repo.AdoptAutomationAccounts(ctx, first, []AutomationAccount{{
		AuthIndex: "a", Provider: "claude", Name: "a.json",
		CycleResetAtMS: &resetAt, CycleKey: "a:claude:reset", DisableObservedAtMS: &observed,
	}}, true, now); err != nil {
		t.Fatal(err)
	}
	account, found, err := repo.GetAutomationAccount(ctx, "a")
	if err != nil || !found {
		t.Fatalf("read account: %v %v", found, err)
	}
	if account.CycleResetAtMS == nil || *account.CycleResetAtMS != resetAt ||
		account.CycleKey != "a:claude:reset" || account.DisableObservedAtMS == nil {
		t.Fatalf("the takeover plan must persist: %+v", account)
	}
	if account.LastDisableAtMS != nil || account.IsDisabledByAutomation() {
		t.Fatalf("persisting a plan must not record a disable: %+v", account)
	}

	// A re-adoption keeps the first plan even when offered a different one.
	otherReset := resetAt + 1000
	second := AutomationBatch{ID: "batch-2", CreatedAtMS: now, SelectedCount: 1, SelectedJSON: `["a"]`}
	if err := repo.AdoptAutomationAccounts(ctx, second, []AutomationAccount{{
		AuthIndex: "a", CycleResetAtMS: &otherReset, CycleKey: "other",
	}}, false, now); err != nil {
		t.Fatal(err)
	}
	account, _, _ = repo.GetAutomationAccount(ctx, "a")
	if account.CycleResetAtMS == nil || *account.CycleResetAtMS != resetAt || account.CycleKey != "a:claude:reset" {
		t.Fatalf("a re-adoption must not reset the plan: %+v", account)
	}
}

// TestLatestStandardWeeklyWindowsAcceptsClaudeSevenDay pins that Claude's account-level
// weekly window qualifies whether it is stored under the normalized "weekly" kind or the
// provider's own "seven_day" id, while a model-scoped seven-day window still does not.
func TestLatestStandardWeeklyWindowsAcceptsClaudeSevenDay(t *testing.T) {
	repo, _ := testRepository(t)
	ctx := context.Background()
	insert := func(authIndex, provider, kind, scope, model string, minutes int, used float64, observedAt int64, reset any) {
		t.Helper()
		if _, err := repo.SQL().ExecContext(ctx, `
			INSERT INTO quota_observations (
				auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
				reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?, 'active')`,
			authIndex, provider, kind, scope, model, minutes, used, reset, observedAt, authIndex+"-"+kind+"-"+scope, observedAt); err != nil {
			t.Fatal(err)
		}
	}
	reset := int64(9_000_000_000_000)
	insert("normalized", "claude", "weekly", "standard", "", 10080, 100, 1000, reset)
	insert("seven-day", "claude", "seven_day", "standard", "", 10080, 100, 2000, reset)
	insert("sonnet", "claude", "model_scoped", "model", "claude-3-5-sonnet", 10080, 100, 3000, reset)

	windows, err := repo.LatestStandardWeeklyWindows(ctx, []string{"normalized", "seven-day", "sonnet"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := windows["normalized"]; !ok {
		t.Fatalf("the normalized weekly kind must count: %+v", windows)
	}
	if _, ok := windows["seven-day"]; !ok {
		t.Fatalf("Claude's seven_day kind must count as the same window: %+v", windows)
	}
	if _, ok := windows["sonnet"]; ok {
		t.Fatalf("a model-scoped seven-day window must not count: %+v", windows["sonnet"])
	}
}
