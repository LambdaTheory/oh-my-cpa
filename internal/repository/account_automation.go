package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Account-level auto-management persistence (migration 043).
//
// The roster, each account's plan and the adoption snapshots live in three
// tables. Everything here is an ordinary repository method so the console
// handlers, the background loop and the capability adapter share one write
// path; the optimistic intent_version guard is what keeps a returned cancel
// from being undone by a background pass that read the row before it.

// ErrAutomationConflict is returned when an automation write is offered against
// an intent_version that has since changed. The caller re-reads and decides
// again rather than forcing the write.
var ErrAutomationConflict = errors.New("automation_conflict")

// Automation state vocabulary. The strings are stored, so they are part of the
// schema contract with migration 043.
const (
	AutomationStateIdle       = "idle"
	AutomationStateMonitoring = "monitoring"
	AutomationStateDisabled   = "disabled"
	AutomationStateWaiting    = "waiting"
	AutomationStateError      = "error"
)

// AutomationAccount is one row of account_automation_accounts.
type AutomationAccount struct {
	AuthIndex     string
	Provider      string
	Name          string
	AccountLabel  string
	IsAdopted     bool
	IsEnabled     bool
	AdoptBatchID  string
	AdoptedAtMS   *int64
	State         string
	MonitorReason string
	// disable_observed_at_ms and cycle_key together are the evidence watermark:
	// a new disable needs an observation newer than both.
	DisableObservedAtMS *int64
	CycleResetAtMS      *int64
	CycleKey            string
	// ManualResetAtMS is the operator's one-time absolute recovery time, used
	// only while ManualResetConsumed is false and no cycle reset is known.
	ManualResetAtMS     *int64
	ManualResetConsumed bool
	NextAttemptAtMS     *int64
	AttemptCount        int
	LastAttemptAtMS     *int64
	LastError           string
	LastErrorAtMS       *int64
	LastDisableAtMS     *int64
	LastEnableAtMS      *int64
	IntentVersion       int64
	CreatedAtMS         int64
	UpdatedAtMS         int64
}

// IsDisabledByAutomation reports whether the newest automation action on this
// account was a disable. An adopted account that was already disabled upstream
// has no last_disable_at_ms, so it is never reported as disabled by OMC.
func (a AutomationAccount) IsDisabledByAutomation() bool {
	if a.LastDisableAtMS == nil {
		return false
	}
	if a.LastEnableAtMS == nil {
		return true
	}
	return *a.LastDisableAtMS > *a.LastEnableAtMS
}

// AutomationWeeklyWindow is the newest account-level weekly quota reading for one
// credential, normalized from quota_observations.
type AutomationWeeklyWindow struct {
	AuthIndex     string
	Provider      string
	WindowKind    string
	ObservedAtMS  int64
	WindowMinutes int
	UsedPercent   float64
	ResetAtMS     *int64
	Source        string
}

// AutomationSettings is the single-row web switch for account auto-management.
// The process-level master switch is an environment variable; this row is only
// the operator's web preference.
type AutomationSettings struct {
	WebEnabled  bool
	UpdatedAtMS int64
}

// AutomationBatch is the one-time adoption snapshot.
type AutomationBatch struct {
	ID            string
	CreatedAtMS   int64
	SelectedCount int
	SelectedJSON  string
	Note          string
}

// automationWeeklyMinutes is the real seven-day period (168h) an account-level
// weekly window must state exactly. The period is the window's own identity: a
// Codex primary slot that meters five hours is not a weekly window, a
// model-scoped seven-day window is filtered by scope, and a reading that merely
// comes within an hour of seven days (the old ±1h tolerance) is not accepted -
// the capability registry names the provider's real duration, so a near-miss is
// a different window rather than the same one.
const (
	automationWeeklyMinutes = 10080
	// automationSevenDayKind is Claude's own account-level weekly window id. The
	// normalizer folds a standard seven_day window onto the shared "weekly" kind, but
	// evidence accepts either spelling so a row that predates that fold, or one written
	// by another path, still counts as the same seven-day window.
	automationSevenDayKind   = "seven_day"
	automationAccountColumns = `auth_index, provider, name, account_label, is_adopted, is_enabled, adopt_batch_id, adopted_at_ms, state, monitor_reason, disable_observed_at_ms, cycle_reset_at_ms, cycle_key, manual_reset_at_ms, manual_reset_consumed, next_attempt_at_ms, attempt_count, last_attempt_at_ms, last_error, last_error_at_ms, last_disable_at_ms, last_enable_at_ms, intent_version, created_at_ms, updated_at_ms`
)

type automationAccountScanner interface {
	Scan(dest ...any) error
}

func scanAutomationAccount(scanner automationAccountScanner) (AutomationAccount, error) {
	var account AutomationAccount
	var adoptedAt, disableObserved, cycleReset, manualReset, nextAttempt, lastAttempt, lastErrorAt, lastDisable, lastEnable sql.NullInt64
	var isAdopted, isEnabled, manualConsumed int
	if err := scanner.Scan(
		&account.AuthIndex, &account.Provider, &account.Name, &account.AccountLabel,
		&isAdopted, &isEnabled, &account.AdoptBatchID, &adoptedAt,
		&account.State, &account.MonitorReason, &disableObserved, &cycleReset, &account.CycleKey,
		&manualReset, &manualConsumed, &nextAttempt, &account.AttemptCount, &lastAttempt,
		&account.LastError, &lastErrorAt, &lastDisable, &lastEnable,
		&account.IntentVersion, &account.CreatedAtMS, &account.UpdatedAtMS,
	); err != nil {
		return AutomationAccount{}, err
	}
	account.IsAdopted = isAdopted != 0
	account.IsEnabled = isEnabled != 0
	account.ManualResetConsumed = manualConsumed != 0
	account.AdoptedAtMS = nullInt64Ptr(adoptedAt)
	account.DisableObservedAtMS = nullInt64Ptr(disableObserved)
	account.CycleResetAtMS = nullInt64Ptr(cycleReset)
	account.ManualResetAtMS = nullInt64Ptr(manualReset)
	account.NextAttemptAtMS = nullInt64Ptr(nextAttempt)
	account.LastAttemptAtMS = nullInt64Ptr(lastAttempt)
	account.LastErrorAtMS = nullInt64Ptr(lastErrorAt)
	account.LastDisableAtMS = nullInt64Ptr(lastDisable)
	account.LastEnableAtMS = nullInt64Ptr(lastEnable)
	return account, nil
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copied := value.Int64
	return &copied
}

// GetAutomationSettings reads the single web switch row. A database that has
// never had one answers with the zero value rather than an error.
func (r *Repository) GetAutomationSettings(ctx context.Context) (AutomationSettings, error) {
	if r == nil || r.SQL() == nil {
		return AutomationSettings{}, errors.New("repository is not initialized")
	}
	var settings AutomationSettings
	var enabled int
	err := r.SQL().QueryRowContext(ctx, `
		SELECT web_enabled, updated_at_ms FROM account_automation_settings WHERE id = 1`).
		Scan(&enabled, &settings.UpdatedAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationSettings{}, nil
	}
	if err != nil {
		return AutomationSettings{}, fmt.Errorf("read account automation settings: %w", err)
	}
	settings.WebEnabled = enabled != 0
	return settings, nil
}

// SetAutomationWebEnabled writes the operator's web preference. It never touches
// the environment master switch, which lives in config and outranks this row.
func (r *Repository) SetAutomationWebEnabled(ctx context.Context, enabled bool, nowMS int64) error {
	if r == nil || r.SQL() == nil {
		return errors.New("repository is not initialized")
	}
	if _, err := r.SQL().ExecContext(ctx, `
		INSERT INTO account_automation_settings (id, web_enabled, updated_at_ms)
		VALUES (1, ?, ?)
		ON CONFLICT(id) DO UPDATE SET web_enabled = excluded.web_enabled, updated_at_ms = excluded.updated_at_ms`,
		boolToInt(enabled), nowMS); err != nil {
		return fmt.Errorf("write account automation settings: %w", err)
	}
	return nil
}

// ListAutomationAccounts returns every adopted or previously adopted account,
// oldest first. The console shows the roster and the plan from one read.
func (r *Repository) ListAutomationAccounts(ctx context.Context) ([]AutomationAccount, error) {
	if r == nil || r.SQL() == nil {
		return nil, errors.New("repository is not initialized")
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT `+automationAccountColumns+` FROM account_automation_accounts
		ORDER BY created_at_ms ASC, auth_index ASC`)
	if err != nil {
		return nil, fmt.Errorf("list account automation accounts: %w", err)
	}
	defer rows.Close()
	accounts := []AutomationAccount{}
	for rows.Next() {
		account, err := scanAutomationAccount(rows)
		if err != nil {
			return nil, fmt.Errorf("scan account automation account: %w", err)
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

// GetAutomationAccount reads one account's plan.
func (r *Repository) GetAutomationAccount(ctx context.Context, authIndex string) (AutomationAccount, bool, error) {
	if r == nil || r.SQL() == nil {
		return AutomationAccount{}, false, errors.New("repository is not initialized")
	}
	account, err := scanAutomationAccount(r.SQL().QueryRowContext(ctx, `
		SELECT `+automationAccountColumns+` FROM account_automation_accounts WHERE auth_index = ?`,
		strings.TrimSpace(authIndex)))
	if errors.Is(err, sql.ErrNoRows) {
		return AutomationAccount{}, false, nil
	}
	if err != nil {
		return AutomationAccount{}, false, fmt.Errorf("read account automation account: %w", err)
	}
	return account, true, nil
}

// AdoptAutomationAccounts records a one-time batch adoption. A credential not
// already on the roster gains a row and an intent_version; one already adopted
// keeps its switch and plan, so re-adopting never resets an operator's choice.
// Adoption alone does not enable automation unless enable is set.
func (r *Repository) AdoptAutomationAccounts(ctx context.Context, batch AutomationBatch, accounts []AutomationAccount, enable bool, nowMS int64) error {
	if r == nil || r.SQL() == nil {
		return errors.New("repository is not initialized")
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account automation adoption: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO account_automation_batches (id, created_at_ms, selected_count, selected_json, note)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		batch.ID, batch.CreatedAtMS, batch.SelectedCount, batch.SelectedJSON, batch.Note); err != nil {
		return fmt.Errorf("record account automation batch: %w", err)
	}

	for _, account := range accounts {
		authIndex := strings.TrimSpace(account.AuthIndex)
		if authIndex == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO account_automation_accounts (
				auth_index, provider, name, account_label, is_adopted, is_enabled, adopt_batch_id,
				adopted_at_ms, state, monitor_reason,
				cycle_reset_at_ms, cycle_key, disable_observed_at_ms,
				intent_version, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, '', ?, ?, ?, 1, ?, ?)
			ON CONFLICT(auth_index) DO UPDATE SET
				provider = excluded.provider,
				name = excluded.name,
				account_label = excluded.account_label,
				is_adopted = 1,
				adopt_batch_id = CASE WHEN account_automation_accounts.is_adopted = 1
					THEN account_automation_accounts.adopt_batch_id ELSE excluded.adopt_batch_id END,
				adopted_at_ms = COALESCE(account_automation_accounts.adopted_at_ms, excluded.adopted_at_ms),
				-- A newly adopted account takes the batch's enable flag; one already
				-- on the roster keeps whatever the operator last set.
				is_enabled = CASE WHEN account_automation_accounts.is_adopted = 1
					THEN account_automation_accounts.is_enabled ELSE excluded.is_enabled END,
				-- A takeover's recovery plan (cycle reset and evidence watermark) is
				-- written only on first adoption; a re-adoption never resets the plan.
				cycle_reset_at_ms = CASE WHEN account_automation_accounts.is_adopted = 1
					THEN account_automation_accounts.cycle_reset_at_ms ELSE excluded.cycle_reset_at_ms END,
				cycle_key = CASE WHEN account_automation_accounts.is_adopted = 1
					THEN account_automation_accounts.cycle_key ELSE excluded.cycle_key END,
				disable_observed_at_ms = CASE WHEN account_automation_accounts.is_adopted = 1
					THEN account_automation_accounts.disable_observed_at_ms ELSE excluded.disable_observed_at_ms END,
				intent_version = CASE WHEN account_automation_accounts.is_adopted = 1
					THEN account_automation_accounts.intent_version ELSE account_automation_accounts.intent_version + 1 END,
				updated_at_ms = excluded.updated_at_ms`,
			authIndex, account.Provider, account.Name, account.AccountLabel,
			boolToInt(enable), batch.ID, batch.CreatedAtMS,
			AutomationStateIdle, account.CycleResetAtMS, account.CycleKey, account.DisableObservedAtMS,
			nowMS, nowMS); err != nil {
			return fmt.Errorf("adopt account automation account %s: %w", authIndex, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit account automation adoption: %w", err)
	}
	return nil
}

// SetAutomationSwitch turns one account's auto-management on or off. Turning it
// off clears the plan (a pending enable/retry is cancelled) without touching a
// credential OMC already disabled, so switching off never silently re-enables.
func (r *Repository) SetAutomationSwitch(ctx context.Context, authIndex string, enabled bool, expectedVersion int64, nowMS int64) (AutomationAccount, error) {
	state := AutomationStateIdle
	reason := ""
	if !enabled {
		reason = "switch_off"
	}
	return r.writeAutomationAccount(ctx, authIndex, expectedVersion, nowMS, `
		is_enabled = ?,
		state = ?,
		monitor_reason = ?,
		next_attempt_at_ms = NULL,
		attempt_count = 0,
		last_error = '',
		last_error_at_ms = NULL,
		intent_version = intent_version + 1,
		updated_at_ms = ?`,
		boolToInt(enabled), state, reason, nowMS)
}

// SetAutomationManualReset records the operator's one-time absolute recovery
// time for an account whose upstream published no reset. It is consumed when the
// enable it scheduled lands.
func (r *Repository) SetAutomationManualReset(ctx context.Context, authIndex string, resetAtMS int64, expectedVersion int64, nowMS int64) (AutomationAccount, error) {
	return r.writeAutomationAccount(ctx, authIndex, expectedVersion, nowMS, `
		manual_reset_at_ms = ?,
		manual_reset_consumed = 0,
		intent_version = intent_version + 1,
		updated_at_ms = ?`,
		resetAtMS, nowMS)
}

// RecordAutomationMonitoring writes the descriptive state a pass computed. It
// does not bump intent_version: it records the loop's reading, not an operator
// intent, and an outcome write must not invalidate a concurrent one.
func (r *Repository) RecordAutomationMonitoring(ctx context.Context, authIndex, state, reason string, expectedVersion int64, nowMS int64) (AutomationAccount, error) {
	return r.writeAutomationAccount(ctx, authIndex, expectedVersion, nowMS, `
		state = ?,
		monitor_reason = ?,
		updated_at_ms = ?`,
		state, reason, nowMS)
}

// RecordAutomationDisable records a confirmed auto-disable and the evidence
// watermark that justified it: the observed instant of the reading and the cycle
// it closed. A later pass needs evidence newer than both.
func (r *Repository) RecordAutomationDisable(ctx context.Context, authIndex string, expectedVersion int64, window AutomationWeeklyWindow, cycleKey string, nowMS int64) (AutomationAccount, error) {
	return r.writeAutomationAccount(ctx, authIndex, expectedVersion, nowMS, `
		state = ?,
		monitor_reason = '',
		disable_observed_at_ms = ?,
		cycle_reset_at_ms = ?,
		cycle_key = ?,
		next_attempt_at_ms = NULL,
		attempt_count = 0,
		last_error = '',
		last_error_at_ms = NULL,
		last_disable_at_ms = ?,
		last_attempt_at_ms = ?,
		updated_at_ms = ?`,
		AutomationStateDisabled, window.ObservedAtMS, window.ResetAtMS, cycleKey, nowMS, nowMS, nowMS)
}

// RecordAutomationEnable records a confirmed enable. viaManual consumes the
// operator's one-time recovery time so it schedules exactly one enable.
func (r *Repository) RecordAutomationEnable(ctx context.Context, authIndex string, expectedVersion int64, nowMS int64, viaManual bool) (AutomationAccount, error) {
	manualConsumed := 0
	if viaManual {
		manualConsumed = 1
	}
	return r.writeAutomationAccount(ctx, authIndex, expectedVersion, nowMS, `
		state = ?,
		monitor_reason = '',
		manual_reset_consumed = CASE WHEN ? = 1 THEN 1 ELSE manual_reset_consumed END,
		next_attempt_at_ms = NULL,
		attempt_count = 0,
		last_error = '',
		last_error_at_ms = NULL,
		last_enable_at_ms = ?,
		last_attempt_at_ms = ?,
		updated_at_ms = ?`,
		AutomationStateIdle, manualConsumed, nowMS, nowMS, nowMS)
}

// RecordAutomationFailure persists a failed side effect and the instant the next
// attempt may run, so a restart resumes the backoff instead of retrying at once.
func (r *Repository) RecordAutomationFailure(ctx context.Context, authIndex string, expectedVersion int64, atMS int64, message string, nextAttemptAtMS int64) (AutomationAccount, error) {
	return r.writeAutomationAccount(ctx, authIndex, expectedVersion, atMS, `
		state = ?,
		last_error = ?,
		last_error_at_ms = ?,
		attempt_count = attempt_count + 1,
		last_attempt_at_ms = ?,
		next_attempt_at_ms = ?,
		updated_at_ms = ?`,
		AutomationStateError, message, atMS, atMS, nextAttemptAtMS, atMS)
}

// RecordAutomationReset clears a plan the process no longer owns: an account
// whose live credential is enabled again, or a switch that was turned off. The
// evidence watermark is deliberately kept, so the closed cycle cannot re-close.
func (r *Repository) RecordAutomationReset(ctx context.Context, authIndex string, expectedVersion int64, nowMS int64, state, reason string) (AutomationAccount, error) {
	return r.writeAutomationAccount(ctx, authIndex, expectedVersion, nowMS, `
		state = ?,
		monitor_reason = ?,
		next_attempt_at_ms = NULL,
		attempt_count = 0,
		last_error = '',
		last_error_at_ms = NULL,
		last_enable_at_ms = CASE WHEN ? THEN ? ELSE last_enable_at_ms END,
		updated_at_ms = ?`,
		state, reason, reason == automationReasonExternallyReEnabled, nowMS, nowMS)
}

// automationReasonExternallyReEnabled mirrors the automation package's reason
// string; the store only needs to recognize it to end the disable episode.
const automationReasonExternallyReEnabled = "externally_re_enabled"

func (r *Repository) writeAutomationAccount(ctx context.Context, authIndex string, expectedVersion int64, nowMS int64, assignments string, args ...any) (AutomationAccount, error) {
	if r == nil || r.SQL() == nil {
		return AutomationAccount{}, errors.New("repository is not initialized")
	}
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return AutomationAccount{}, errors.New("auth_index is required")
	}
	arguments := append([]any{}, args...)
	arguments = append(arguments, authIndex, expectedVersion)
	result, err := r.SQL().ExecContext(ctx, `UPDATE account_automation_accounts SET `+assignments+`
		WHERE auth_index = ? AND intent_version = ?`, arguments...)
	if err != nil {
		return AutomationAccount{}, fmt.Errorf("update account automation account %s: %w", authIndex, err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		if _, found, getErr := r.GetAutomationAccount(ctx, authIndex); getErr != nil {
			return AutomationAccount{}, getErr
		} else if !found {
			return AutomationAccount{}, ErrAutomationConflict
		}
		return AutomationAccount{}, ErrAutomationConflict
	}
	account, found, err := r.GetAutomationAccount(ctx, authIndex)
	if err != nil {
		return AutomationAccount{}, err
	}
	if !found {
		return AutomationAccount{}, ErrAutomationConflict
	}
	return account, nil
}

// LatestStandardWeeklyWindows returns, per auth index, the newest standard
// weekly reading from the observation timeline, whatever its source. The real
// seven-day period is required exactly (10080 minutes, no ±1h tolerance), and
// only the account-level weekly kinds are selected, so a Codex five-hour slot,
// a model-scoped Claude window and a reading that only approximates seven days
// are all excluded here. Both the normalized "weekly" kind and Claude's own
// "seven_day" id are accepted, since they name the same account-level window.
//
// The read is provider-agnostic: it returns the newest qualifying reading for any
// provider, and the caller's admission rule decides whether that provider's
// account-level weekly window is a verified capability before anything acts on it.
func (r *Repository) LatestStandardWeeklyWindows(ctx context.Context, authIndexes []string) (map[string]AutomationWeeklyWindow, error) {
	result := map[string]AutomationWeeklyWindow{}
	if r == nil || r.SQL() == nil || len(authIndexes) == 0 {
		return result, nil
	}
	valid := make([]string, 0, len(authIndexes))
	for _, authIndex := range authIndexes {
		if trimmed := strings.TrimSpace(authIndex); trimmed != "" {
			valid = append(valid, trimmed)
		}
	}
	if len(valid) == 0 {
		return result, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(valid)), ",")
	arguments := make([]any, 0, len(valid)+4)
	arguments = append(arguments, "standard", "weekly", automationSevenDayKind, automationWeeklyMinutes)
	for _, authIndex := range valid {
		arguments = append(arguments, authIndex)
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT auth_index, provider, window_kind, window_minutes, used_percent, reset_at_ms, observed_at_ms, source
		FROM quota_observations
		WHERE scope = ? AND window_kind IN (?, ?)
		  AND window_minutes = ?
		  AND auth_index IN (`+placeholders+`)
		ORDER BY observed_at_ms DESC, id DESC`, arguments...)
	if err != nil {
		return result, fmt.Errorf("query latest standard weekly windows: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var window AutomationWeeklyWindow
		var resetAt sql.NullInt64
		if err := rows.Scan(&window.AuthIndex, &window.Provider, &window.WindowKind, &window.WindowMinutes,
			&window.UsedPercent, &resetAt, &window.ObservedAtMS, &window.Source); err != nil {
			return result, fmt.Errorf("scan standard weekly window: %w", err)
		}
		window.ResetAtMS = nullInt64Ptr(resetAt)
		// Newest first: the first row for an index is the one to keep.
		if _, exists := result[window.AuthIndex]; !exists {
			result[window.AuthIndex] = window
		}
	}
	return result, rows.Err()
}

// CountAutomationAccounts is the compact read tests use to assert an adoption's
// effect without depending on row order.
func (r *Repository) CountAutomationAccounts(ctx context.Context) (int64, error) {
	if r == nil || r.SQL() == nil {
		return 0, errors.New("repository is not initialized")
	}
	var count int64
	if err := r.SQL().QueryRowContext(ctx, `SELECT COUNT(1) FROM account_automation_accounts`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count account automation accounts: %w", err)
	}
	return count, nil
}

// ListAutomationBatches returns the adoption snapshots newest first.
func (r *Repository) ListAutomationBatches(ctx context.Context, limit int) ([]AutomationBatch, error) {
	if r == nil || r.SQL() == nil {
		return nil, errors.New("repository is not initialized")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT id, created_at_ms, selected_count, selected_json, note
		FROM account_automation_batches ORDER BY created_at_ms DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list account automation batches: %w", err)
	}
	defer rows.Close()
	batches := []AutomationBatch{}
	for rows.Next() {
		var batch AutomationBatch
		if err := rows.Scan(&batch.ID, &batch.CreatedAtMS, &batch.SelectedCount, &batch.SelectedJSON, &batch.Note); err != nil {
			return nil, fmt.Errorf("scan account automation batch: %w", err)
		}
		batches = append(batches, batch)
	}
	return batches, rows.Err()
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// timeMS is a small helper for callers that hold a time.Time.
func timeMS(value time.Time) int64 { return value.UnixMilli() }
