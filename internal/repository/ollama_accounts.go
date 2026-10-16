package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Ollama account persistence (migration 044).
//
// The encrypted cookie and the safe quota snapshot live in separate columns so an
// ordinary metadata read never selects the secret. Reads project an explicit column
// list rather than SELECT *; the cookie ciphertext is returned only to the collector
// through ListEnabledOllamaAccountSecrets.

// OllamaAccount is one configured Ollama account's metadata, without its cookie.
type OllamaAccount struct {
	ID                string
	Label             string
	Enabled           bool
	HasCookie         bool
	Status            string
	LastSuccessAtMS   *int64
	LastObservedAtMS  *int64
	NewestError       string
	NewestErrorAtMS   *int64
	NextAttemptAtMS   *int64
	AttemptCount      int
	QuotaSnapshotJSON string
	QuotaObservedAtMS *int64
	CreatedAtMS       int64
	UpdatedAtMS       int64
}

// OllamaAccountSecret pairs an account's metadata with the encrypted cookie the
// collector must decrypt. It is never serialized into an API response.
type OllamaAccountSecret struct {
	ID              string
	Label           string
	Enabled         bool
	Ciphertext      []byte
	Nonce           []byte
	NextAttemptAtMS *int64
	AttemptCount    int
	// CookieUpdatedAtMS is the version of this cookie. A collection result carries
	// it back so a scrape that read an older cookie cannot overwrite a newer one.
	CookieUpdatedAtMS int64
}

// OllamaAccountCreate is the input to CreateOllamaAccount. An empty ID mints a
// stable opaque UUID, so the account identity is never derived from an email,
// label or cookie.
type OllamaAccountCreate struct {
	ID         string
	Label      string
	Enabled    bool
	Ciphertext []byte
	Nonce      []byte
	NowMS      int64
}

// OllamaAccountUpdate is the input to UpdateOllamaAccount. A nil Label or Enabled
// leaves that field unchanged; SetCookie replaces the encrypted cookie.
type OllamaAccountUpdate struct {
	Label      *string
	Enabled    *bool
	SetCookie  bool
	Ciphertext []byte
	Nonce      []byte
	NowMS      int64
}

// OllamaCollectionResult is what RecordOllamaResult writes back after a scrape.
type OllamaCollectionResult struct {
	ID              string
	Status          string
	ObservedAtMS    int64
	IsSuccess       bool
	SnapshotJSON    string
	Error           string
	ErrorAtMS       int64
	NextAttemptAtMS *int64
	AttemptCount    int
	// CookieUpdatedAtMS, when set, gates the write: a result whose cookie version no
	// longer matches the stored one is dropped instead of overwriting a reading of
	// the newer cookie. Nil writes unconditionally.
	CookieUpdatedAtMS *int64
	// Auto marks a result produced by the periodic pass or a create/update trigger.
	// An automatic result is dropped while the account is disabled; a manual refresh
	// of a paused account passes Auto false and is still written.
	Auto bool
}

// ollamaAccountColumns is the explicit metadata projection. It never names the
// cookie ciphertext or nonce; has_cookie is derived without reading the blob.
const ollamaAccountColumns = `id, label, enabled, LENGTH(cookie_ciphertext) > 0, status,
	last_success_at_ms, last_observed_at_ms, newest_error, newest_error_at_ms,
	next_attempt_at_ms, attempt_count, quota_snapshot_json, quota_observed_at_ms,
	created_at_ms, updated_at_ms`

type ollamaAccountScanner interface {
	Scan(dest ...any) error
}

func scanOllamaAccount(scanner ollamaAccountScanner) (OllamaAccount, error) {
	var account OllamaAccount
	var enabled, hasCookie int
	var lastSuccess, lastObserved, newestErrorAt, nextAttempt, quotaObserved sql.NullInt64
	if err := scanner.Scan(
		&account.ID, &account.Label, &enabled, &hasCookie, &account.Status,
		&lastSuccess, &lastObserved, &account.NewestError, &newestErrorAt,
		&nextAttempt, &account.AttemptCount, &account.QuotaSnapshotJSON, &quotaObserved,
		&account.CreatedAtMS, &account.UpdatedAtMS,
	); err != nil {
		return OllamaAccount{}, err
	}
	account.Enabled = enabled != 0
	account.HasCookie = hasCookie != 0
	account.LastSuccessAtMS = nullInt64Ptr(lastSuccess)
	account.LastObservedAtMS = nullInt64Ptr(lastObserved)
	account.NewestErrorAtMS = nullInt64Ptr(newestErrorAt)
	account.NextAttemptAtMS = nullInt64Ptr(nextAttempt)
	account.QuotaObservedAtMS = nullInt64Ptr(quotaObserved)
	return account, nil
}

// CreateOllamaAccount stores a new Ollama account with its encrypted cookie.
func (r *Repository) CreateOllamaAccount(ctx context.Context, input OllamaAccountCreate) (OllamaAccount, error) {
	if r == nil || r.SQL() == nil {
		return OllamaAccount{}, errors.New("repository is not initialized")
	}
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = uuid.NewString()
	}
	if len(input.Ciphertext) == 0 {
		return OllamaAccount{}, errors.New("encrypted cookie is required")
	}
	nowMS := input.NowMS
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	if _, err := r.SQL().ExecContext(ctx, `
		INSERT INTO ollama_accounts (
			id, label, enabled, cookie_ciphertext, cookie_nonce, cookie_updated_at_ms,
			created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, strings.TrimSpace(input.Label), boolToInt(input.Enabled), input.Ciphertext, input.Nonce,
		nowMS, nowMS, nowMS); err != nil {
		return OllamaAccount{}, fmt.Errorf("insert ollama account: %w", err)
	}
	account, found, err := r.GetOllamaAccount(ctx, id)
	if err != nil {
		return OllamaAccount{}, err
	}
	if !found {
		return OllamaAccount{}, errors.New("ollama account disappeared after insert")
	}
	return account, nil
}

// ListOllamaAccounts returns every configured account, oldest first, without any
// cookie material.
func (r *Repository) ListOllamaAccounts(ctx context.Context) ([]OllamaAccount, error) {
	if r == nil || r.SQL() == nil {
		return nil, errors.New("repository is not initialized")
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT `+ollamaAccountColumns+` FROM ollama_accounts ORDER BY created_at_ms ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list ollama accounts: %w", err)
	}
	defer rows.Close()
	accounts := []OllamaAccount{}
	for rows.Next() {
		account, scanErr := scanOllamaAccount(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan ollama account: %w", scanErr)
		}
		accounts = append(accounts, account)
	}
	return accounts, rows.Err()
}

// GetOllamaAccount reads one account's metadata.
func (r *Repository) GetOllamaAccount(ctx context.Context, id string) (OllamaAccount, bool, error) {
	if r == nil || r.SQL() == nil {
		return OllamaAccount{}, false, errors.New("repository is not initialized")
	}
	account, err := scanOllamaAccount(r.SQL().QueryRowContext(ctx, `
		SELECT `+ollamaAccountColumns+` FROM ollama_accounts WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return OllamaAccount{}, false, nil
	}
	if err != nil {
		return OllamaAccount{}, false, fmt.Errorf("read ollama account: %w", err)
	}
	return account, true, nil
}

// UpdateOllamaAccount applies a partial update. It returns found false when the
// account does not exist.
func (r *Repository) UpdateOllamaAccount(ctx context.Context, id string, input OllamaAccountUpdate) (OllamaAccount, bool, error) {
	if r == nil || r.SQL() == nil {
		return OllamaAccount{}, false, errors.New("repository is not initialized")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return OllamaAccount{}, false, errors.New("id is required")
	}
	nowMS := input.NowMS
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	// updated_at_ms is ordinary metadata: when the account row was last changed.
	assignments := []string{"updated_at_ms = ?"}
	arguments := []any{nowMS}
	if input.Label != nil {
		assignments = append(assignments, "label = ?")
		arguments = append(arguments, strings.TrimSpace(*input.Label))
	}
	if input.Enabled != nil {
		assignments = append(assignments, "enabled = ?")
		arguments = append(arguments, boolToInt(*input.Enabled))
		if !*input.Enabled {
			// Turning collection off drops any pending backoff, so re-enabling
			// collects on the next pass instead of waiting out a stale window.
			assignments = append(assignments, "next_attempt_at_ms = NULL", "attempt_count = 0")
		}
	}
	if input.SetCookie {
		// The cookie version is raised strictly so a scrape that read an older cookie
		// can never pass the cookie guard because two writes shared a millisecond.
		assignments = append(assignments, "cookie_ciphertext = ?", "cookie_nonce = ?", "cookie_updated_at_ms = MAX(cookie_updated_at_ms + 1, ?)")
		arguments = append(arguments, input.Ciphertext, input.Nonce, nowMS)
	}
	arguments = append(arguments, id)
	result, err := r.SQL().ExecContext(ctx, `UPDATE ollama_accounts SET `+strings.Join(assignments, ", ")+` WHERE id = ?`, arguments...)
	if err != nil {
		return OllamaAccount{}, false, fmt.Errorf("update ollama account: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		account, found, getErr := r.GetOllamaAccount(ctx, id)
		return account, found, getErr
	}
	return r.GetOllamaAccount(ctx, id)
}

// DeleteOllamaAccount removes one account. It reports whether a row was removed.
func (r *Repository) DeleteOllamaAccount(ctx context.Context, id string) (bool, error) {
	if r == nil || r.SQL() == nil {
		return false, errors.New("repository is not initialized")
	}
	result, err := r.SQL().ExecContext(ctx, `DELETE FROM ollama_accounts WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, fmt.Errorf("delete ollama account: %w", err)
	}
	affected, _ := result.RowsAffected()
	return affected > 0, nil
}

// ListEnabledOllamaAccountSecrets returns the encryption material for the enabled
// accounts only. It is the sole read that selects the cookie ciphertext, and it is
// reached only by the collector.
func (r *Repository) ListEnabledOllamaAccountSecrets(ctx context.Context) ([]OllamaAccountSecret, error) {
	if r == nil || r.SQL() == nil {
		return nil, errors.New("repository is not initialized")
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT id, label, enabled, cookie_ciphertext, cookie_nonce, cookie_updated_at_ms, next_attempt_at_ms, attempt_count
		FROM ollama_accounts WHERE enabled = 1 ORDER BY created_at_ms ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list ollama account secrets: %w", err)
	}
	defer rows.Close()
	secrets := []OllamaAccountSecret{}
	for rows.Next() {
		var secret OllamaAccountSecret
		var enabled int
		var nextAttempt sql.NullInt64
		if err := rows.Scan(&secret.ID, &secret.Label, &enabled, &secret.Ciphertext, &secret.Nonce,
			&secret.CookieUpdatedAtMS, &nextAttempt, &secret.AttemptCount); err != nil {
			return nil, fmt.Errorf("scan ollama account secret: %w", err)
		}
		secret.Enabled = enabled != 0
		secret.NextAttemptAtMS = nullInt64Ptr(nextAttempt)
		secrets = append(secrets, secret)
	}
	return secrets, rows.Err()
}

// GetOllamaAccountSecret reads one account's encryption material by id, enabled or
// not. The manual and triggered refresh paths reload through it so a queued scrape
// always reads the newest cookie rather than one captured before an update.
func (r *Repository) GetOllamaAccountSecret(ctx context.Context, id string) (OllamaAccountSecret, bool, error) {
	if r == nil || r.SQL() == nil {
		return OllamaAccountSecret{}, false, errors.New("repository is not initialized")
	}
	var secret OllamaAccountSecret
	var enabled int
	var nextAttempt sql.NullInt64
	err := r.SQL().QueryRowContext(ctx, `
		SELECT id, label, enabled, cookie_ciphertext, cookie_nonce, cookie_updated_at_ms, next_attempt_at_ms, attempt_count
		FROM ollama_accounts WHERE id = ?`, strings.TrimSpace(id)).
		Scan(&secret.ID, &secret.Label, &enabled, &secret.Ciphertext, &secret.Nonce,
			&secret.CookieUpdatedAtMS, &nextAttempt, &secret.AttemptCount)
	if errors.Is(err, sql.ErrNoRows) {
		return OllamaAccountSecret{}, false, nil
	}
	if err != nil {
		return OllamaAccountSecret{}, false, fmt.Errorf("read ollama account secret: %w", err)
	}
	secret.Enabled = enabled != 0
	secret.NextAttemptAtMS = nullInt64Ptr(nextAttempt)
	return secret, true, nil
}

// RecordOllamaResult writes one scrape outcome. A success advances last_success and
// stores the safe snapshot while clearing the error; a failure records the scrubbed
// message and the retry schedule. The snapshot is only overwritten when one is
// supplied, so a transient failure does not erase the last good reading.
func (r *Repository) RecordOllamaResult(ctx context.Context, result OllamaCollectionResult) (OllamaAccount, bool, error) {
	if r == nil || r.SQL() == nil {
		return OllamaAccount{}, false, errors.New("repository is not initialized")
	}
	id := strings.TrimSpace(result.ID)
	if id == "" {
		return OllamaAccount{}, false, errors.New("id is required")
	}
	nowMS := time.Now().UnixMilli()
	observedAt := result.ObservedAtMS
	if observedAt <= 0 {
		observedAt = nowMS
	}
	newestError := ""
	var newestErrorAt any
	if !result.IsSuccess {
		newestError = result.Error
		newestErrorAt = result.ErrorAtMS
		if result.ErrorAtMS <= 0 {
			newestErrorAt = nowMS
		}
	}
	hasSnapshot := result.IsSuccess && strings.TrimSpace(result.SnapshotJSON) != ""
	nextAttempt := any(nil)
	if result.NextAttemptAtMS != nil {
		nextAttempt = *result.NextAttemptAtMS
	}
	// A result carrying a cookie version is applied only while the stored cookie is
	// still that version: a scrape that was reading an old cookie cannot overwrite
	// the reading of a cookie the operator has since replaced.
	cookieVersion := any(nil)
	if result.CookieUpdatedAtMS != nil {
		cookieVersion = *result.CookieUpdatedAtMS
	}
	// An automatic result never writes to a disabled account. A manual refresh of a
	// paused account passes Auto false and still lands, because reading a paused
	// account once is exactly what that path is for.
	autoFlag := 0
	if result.Auto {
		autoFlag = 1
	}
	if _, err := r.SQL().ExecContext(ctx, `
		UPDATE ollama_accounts SET
			status = ?,
			last_observed_at_ms = ?,
			last_success_at_ms = CASE WHEN ? THEN ? ELSE last_success_at_ms END,
			newest_error = ?,
			newest_error_at_ms = ?,
			quota_snapshot_json = CASE WHEN ? THEN ? ELSE quota_snapshot_json END,
			quota_observed_at_ms = CASE WHEN ? THEN ? ELSE quota_observed_at_ms END,
			next_attempt_at_ms = ?,
			attempt_count = ?,
			updated_at_ms = ?
		WHERE id = ?
			AND (? = 0 OR enabled = 1)
			AND (? IS NULL OR cookie_updated_at_ms = ?)`,
		result.Status, observedAt, result.IsSuccess, observedAt,
		newestError, newestErrorAt,
		hasSnapshot, result.SnapshotJSON,
		hasSnapshot, observedAt,
		nextAttempt, result.AttemptCount, nowMS, id,
		autoFlag, cookieVersion, cookieVersion); err != nil {
		return OllamaAccount{}, false, fmt.Errorf("record ollama result: %w", err)
	}
	return r.GetOllamaAccount(ctx, id)
}

// CountOllamaAccounts is the compact read tests use to assert a create or delete.
func (r *Repository) CountOllamaAccounts(ctx context.Context) (int64, error) {
	if r == nil || r.SQL() == nil {
		return 0, errors.New("repository is not initialized")
	}
	var count int64
	if err := r.SQL().QueryRowContext(ctx, `SELECT COUNT(1) FROM ollama_accounts`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count ollama accounts: %w", err)
	}
	return count, nil
}
