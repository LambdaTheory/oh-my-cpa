package repository

import (
	"context"
	"fmt"
	"strings"
)

// QuotaRefreshAttempt is independent of the last successful quota snapshot. Only
// bounded reasons and redacted summaries are retained; upstream bodies may contain secrets.
type QuotaRefreshAttempt struct {
	AuthIndex     string
	Outcome       string
	AttemptedAtMS int64
	HTTPStatus    int
	RetryAtMS     int64
	Reason        string
	ReasonSummary string
}

func (r *Repository) SaveQuotaRefreshAttempt(ctx context.Context, attempt QuotaRefreshAttempt) error {
	if strings.TrimSpace(attempt.AuthIndex) == "" {
		return fmt.Errorf("quota refresh attempt requires auth index")
	}
	_, err := r.SQL().ExecContext(ctx, `INSERT INTO quota_refresh_attempts
        (auth_index, outcome, attempted_at_ms, http_status, retry_at_ms, reason, reason_summary) VALUES (?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(auth_index) DO UPDATE SET outcome = excluded.outcome,
        attempted_at_ms = excluded.attempted_at_ms, http_status = excluded.http_status, retry_at_ms = excluded.retry_at_ms,
        reason = excluded.reason, reason_summary = excluded.reason_summary
        WHERE excluded.attempted_at_ms >= quota_refresh_attempts.attempted_at_ms`,
		attempt.AuthIndex, attempt.Outcome, attempt.AttemptedAtMS, attempt.HTTPStatus, attempt.RetryAtMS, attempt.Reason, attempt.ReasonSummary)
	return err
}

func (r *Repository) GetQuotaRefreshAttempts(ctx context.Context, indexes []string) (map[string]QuotaRefreshAttempt, error) {
	result := map[string]QuotaRefreshAttempt{}
	if len(indexes) == 0 {
		return result, nil
	}
	args := make([]any, len(indexes))
	for i, index := range indexes {
		args[i] = index
	}
	rows, err := r.SQL().QueryContext(ctx, `SELECT auth_index, outcome, attempted_at_ms, http_status, retry_at_ms, reason, reason_summary
        FROM quota_refresh_attempts WHERE auth_index IN (`+strings.TrimSuffix(strings.Repeat("?,", len(indexes)), ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var attempt QuotaRefreshAttempt
		if err := rows.Scan(&attempt.AuthIndex, &attempt.Outcome, &attempt.AttemptedAtMS, &attempt.HTTPStatus, &attempt.RetryAtMS, &attempt.Reason, &attempt.ReasonSummary); err != nil {
			return nil, err
		}
		result[attempt.AuthIndex] = attempt
	}
	return result, rows.Err()
}
