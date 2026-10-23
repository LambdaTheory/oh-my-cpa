package repository

import (
	"context"
	"database/sql"
	"errors"
)

// ReauthJob contains only safe task metadata. Email OTPs never enter SQLite; device user codes are display metadata.
type ReauthJob struct {
	Flow, CodeKind, UserCode, VerificationURL    string
	ExpiresAtMS                                  int64
	ID, AuthIndex, Provider, AccountLabel, State string
	Attempts                                     int
	CreatedAtMS, UpdatedAtMS                     int64
	LastError                                    string
	CycleClosedAtMS, NextAttemptAtMS             int64
	Notification, MessageID                      string
	CodeConsumed                                 bool
}

const REAUTH_COLUMNS = `id, auth_index, provider, account_label, state, attempts, created_at_ms, updated_at_ms, last_error, cycle_closed_at_ms, next_attempt_at_ms, notification, message_id, code_consumed, flow, code_kind, user_code, verification_url, expires_at_ms`

func scanReauthJob(row interface{ Scan(...any) error }) (ReauthJob, error) {
	var j ReauthJob
	err := row.Scan(&j.ID, &j.AuthIndex, &j.Provider, &j.AccountLabel, &j.State, &j.Attempts, &j.CreatedAtMS, &j.UpdatedAtMS, &j.LastError, &j.CycleClosedAtMS, &j.NextAttemptAtMS, &j.Notification, &j.MessageID, &j.CodeConsumed, &j.Flow, &j.CodeKind, &j.UserCode, &j.VerificationURL, &j.ExpiresAtMS)
	return j, err
}

func (r *Repository) CreateReauthJob(ctx context.Context, j ReauthJob) error {
	_, err := r.SQL().ExecContext(ctx, `INSERT INTO reauth_jobs (`+REAUTH_COLUMNS+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`, j.ID, j.AuthIndex, j.Provider, j.AccountLabel, j.State, j.Attempts, j.CreatedAtMS, j.UpdatedAtMS, j.LastError, j.CycleClosedAtMS, j.NextAttemptAtMS, j.Notification, j.MessageID, j.CodeConsumed, j.Flow, j.CodeKind, j.UserCode, j.VerificationURL, j.ExpiresAtMS)
	return err
}

func (r *Repository) GetReauthJob(ctx context.Context, id string) (ReauthJob, error) {
	return scanReauthJob(r.SQL().QueryRowContext(ctx, `SELECT `+REAUTH_COLUMNS+` FROM reauth_jobs WHERE id=?`, id))
}

func (r *Repository) NextReauthJob(ctx context.Context, provider string) (ReauthJob, error) {
	return scanReauthJob(r.SQL().QueryRowContext(ctx, `SELECT `+REAUTH_COLUMNS+` FROM reauth_jobs WHERE state NOT IN ('succeeded','failed','expired') AND (?='' OR provider=?) AND (state<>'pending' OR NOT EXISTS (SELECT 1 FROM reauth_jobs WHERE state NOT IN ('pending','succeeded','failed','expired'))) ORDER BY CASE WHEN state='pending' THEN 1 ELSE 0 END, created_at_ms,id LIMIT 1`, provider, provider))
}

func (r *Repository) ListReauthWork(ctx context.Context) ([]ReauthJob, error) {
	rows, err := r.SQL().QueryContext(ctx, `SELECT `+REAUTH_COLUMNS+` FROM reauth_jobs WHERE state NOT IN ('pending','succeeded','failed','expired') OR notification<>'' OR next_attempt_at_ms>0 ORDER BY created_at_ms,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []ReauthJob
	for rows.Next() {
		j, err := scanReauthJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// SaveReauthJob refuses stale transitions rather than replaying a consumed code.
func (r *Repository) SaveReauthJob(ctx context.Context, j ReauthJob, previous string) error {
	result, err := r.SQL().ExecContext(ctx, `UPDATE reauth_jobs SET state=?,attempts=?,updated_at_ms=?,last_error=?,next_attempt_at_ms=?,notification=?,message_id=?,code_consumed=?,user_code=?,verification_url=?,expires_at_ms=? WHERE id=? AND state=?`, j.State, j.Attempts, j.UpdatedAtMS, j.LastError, j.NextAttemptAtMS, j.Notification, j.MessageID, j.CodeConsumed, j.UserCode, j.VerificationURL, j.ExpiresAtMS, j.ID, previous)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("reauth transition conflict")
	}
	return nil
}

func (r *Repository) CloseReauthCycle(ctx context.Context, authIndex string, nowMS int64) error {
	// Episode ownership ends on health; active work keeps its contact slot until it settles.
	_, err := r.SQL().ExecContext(ctx, `UPDATE reauth_jobs SET cycle_closed_at_ms=?, state=CASE WHEN state='pending' THEN 'expired' ELSE state END WHERE auth_index=? AND cycle_closed_at_ms=0`, nowMS, authIndex)
	return err
}

func (r *Repository) ReauthMessageUsed(ctx context.Context, id string) (bool, error) {
	var exists int
	err := r.SQL().QueryRowContext(ctx, `SELECT 1 FROM reauth_jobs WHERE message_id=?`, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return exists == 1, err
}

// DismissReauthJob preserves the job and its audit history while retiring the
// explicitly reviewed episode and its false-positive signal atomically.
func (r *Repository) DismissReauthJob(ctx context.Context, j ReauthJob, nowMS int64) error {
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE reauth_jobs SET state='failed', last_error='misclassified_auth_failure', cycle_closed_at_ms=?, updated_at_ms=?, next_attempt_at_ms=0, notification='', user_code='', verification_url='', expires_at_ms=0 WHERE id=? AND state=? AND cycle_closed_at_ms=0`, nowMS, nowMS, j.ID, j.State)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("reauth transition conflict")
	}
	if _, err = tx.ExecContext(ctx, `UPDATE feishu_account_signals SET state='unknown', observed_at_ms=?, updated_at_ms=? WHERE auth_index=? AND state='auth_invalid'`, nowMS, nowMS, j.AuthIndex); err != nil {
		return err
	}
	return tx.Commit()
}
