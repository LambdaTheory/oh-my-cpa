package repository

import (
	"context"
	"fmt"

	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

// RemoveAutomationAccount commits the audit and tombstone together. Keeping the
// original row preserves its association with adoption snapshots and audit history.
func (r *Repository) RemoveAutomationAccount(ctx context.Context, authIndex string, expectedVersion, nowMS int64, isAutomatic bool) (AutomationAccount, error) {
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return AutomationAccount{}, err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `UPDATE account_automation_accounts SET is_adopted = 0, is_enabled = 0,
		removed_at_ms = ?, intent_version = intent_version + 1, updated_at_ms = ?
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
	action := "account_automation.unadopt"
	if isAutomatic {
		action = "account_automation.auto_remove"
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO audit_events (occurred_at_ms, action, target_type, target_id, result, request_id, source_summary, details_json)
		VALUES (?, ?, 'account_automation', ?, 'success', '', '', '{}')`, nowMS, action, security.RedactText(authIndex)); err != nil {
		return AutomationAccount{}, fmt.Errorf("audit account automation removal: %w", err)
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
