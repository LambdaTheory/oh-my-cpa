package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

// Removal of a roster row, explicit or confirmed by absence.
//
// The approved rule is that a confirmed-absent credential leaves the active roster even
// when it still carries a pending recovery or retry plan: the plan can never execute for a
// credential the gateway no longer has. What must not be lost is why it was there, so the
// intent and plan are captured in an allowlisted audit snapshot before the tombstone, in
// one transaction. An unwritable snapshot refuses the removal; it never leaves a cleared
// row without its explanation.

// removalSnapshot is the allowlisted record of a removed plan. It deliberately omits the
// account label, which CPA often fills with an email address (ADR 0105).
func removalSnapshot(account AutomationAccount, removal string, nowMS int64) map[string]any {
	snapshot := map[string]any{
		"removal":                removal,
		"removed_at_ms":          nowMS,
		"provider":               account.Provider,
		"name":                   account.Name,
		"state":                  account.State,
		"monitor_reason":         account.MonitorReason,
		"is_enabled":             account.IsEnabled,
		"burn_credit":            account.BurnCredit,
		"intent_version":         account.IntentVersion,
		"adopt_batch_id":         account.AdoptBatchID,
		"adopted_at_ms":          account.AdoptedAtMS,
		"cycle_reset_at_ms":      account.CycleResetAtMS,
		"cycle_key":              account.CycleKey,
		"disable_observed_at_ms": account.DisableObservedAtMS,
		"manual_reset_at_ms":     account.ManualResetAtMS,
		"manual_reset_consumed":  account.ManualResetConsumed,
		"next_attempt_at_ms":     account.NextAttemptAtMS,
		"attempt_count":          account.AttemptCount,
		"last_error":             account.LastError,
		"last_error_at_ms":       account.LastErrorAtMS,
		"last_disable_at_ms":     account.LastDisableAtMS,
		"last_enable_at_ms":      account.LastEnableAtMS,
	}
	return snapshot
}

// RemoveAutomationAccount commits the audit snapshot and the tombstone together. The
// snapshot and the cleared plan fields are written from the row as it was read under the
// caller's intent guard, so a concurrent operator intent conflicts rather than being
// recorded as a plan that never existed.
func (r *Repository) RemoveAutomationAccount(ctx context.Context, authIndex string, expectedVersion, nowMS int64, isAutomatic bool) (AutomationAccount, error) {
	if r == nil || r.SQL() == nil {
		return AutomationAccount{}, fmt.Errorf("repository is not initialized")
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return AutomationAccount{}, err
	}
	defer func() { _ = tx.Rollback() }()
	authIndex = strings.TrimSpace(authIndex)
	existing, found, err := getAutomationAccountTx(ctx, tx, authIndex)
	if err != nil {
		return AutomationAccount{}, err
	}
	if !found || existing.RemovedAtMS != nil || existing.IntentVersion != expectedVersion {
		return AutomationAccount{}, ErrAutomationConflict
	}
	action := "account_automation.unadopt"
	removal := "operator"
	if isAutomatic {
		action = "account_automation.auto_remove"
		removal = "confirmed_absence"
	}
	details, err := json.Marshal(removalSnapshot(existing, removal, nowMS))
	if err != nil {
		return AutomationAccount{}, fmt.Errorf("encode account automation removal snapshot: %w", err)
	}
	// The snapshot is the transaction's first write: a store that refuses it aborts the
	// removal before any plan field is touched.
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events (occurred_at_ms, action, target_type, target_id, result, request_id, source_summary, details_json)
		VALUES (?, ?, 'account_automation', ?, 'success', '', '', ?)`, nowMS, action, security.RedactText(authIndex), string(details)); err != nil {
		return AutomationAccount{}, fmt.Errorf("audit account automation removal: %w", err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE account_automation_accounts SET
			is_adopted = 0, is_enabled = 0, burn_credit = 0,
			removed_at_ms = ?, intent_version = intent_version + 1, updated_at_ms = ?,
			state = 'idle', monitor_reason = '',
			cycle_reset_at_ms = NULL, cycle_key = '', disable_observed_at_ms = NULL,
			manual_reset_at_ms = NULL, manual_reset_consumed = 0,
			next_attempt_at_ms = NULL, attempt_count = 0, last_attempt_at_ms = NULL,
			last_error = '', last_error_at_ms = NULL
		WHERE auth_index = ? AND intent_version = ? AND removed_at_ms IS NULL`, nowMS, nowMS, authIndex, expectedVersion)
	if err != nil {
		return AutomationAccount{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AutomationAccount{}, err
	}
	if affected != 1 {
		return AutomationAccount{}, ErrAutomationConflict
	}
	account, err := scanAutomationAccount(tx.QueryRowContext(ctx, `SELECT `+automationAccountColumns+` FROM account_automation_accounts WHERE auth_index = ?`, authIndex))
	if err != nil {
		return AutomationAccount{}, err
	}
	if err := tx.Commit(); err != nil {
		return AutomationAccount{}, err
	}
	return account, nil
}
