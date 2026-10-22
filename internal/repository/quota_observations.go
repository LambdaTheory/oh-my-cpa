package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/quota"
	"github.com/oh-my-cpa/oh-my-cpa/internal/usage"
)

// CheckpointQuotaObservations names the passive-observation backfill watermark.
// It shares usage_aggregation_checkpoints, a watermark keyed by name.
const CheckpointQuotaObservations = "quota_observations"

// QuotaObservationRetentionDays is the fixed horizon passive observations roll
// at. It does not follow the request-record retention: an observation is the
// only record of a quota window once its request has rolled, so it is kept for
// a year to make a slow weekly cycle readable.
const QuotaObservationRetentionDays = 365

// quotaObservationHeartbeatMS is the longest silence allowed between two
// otherwise-identical readings. Without it a steady window would vanish from
// history, because only changes were written.
const quotaObservationHeartbeatMS = 15 * 60 * 1000

// QuotaObservationRecord is one stored observation. It is exported because
// read-only analytical capabilities consume the normalized timeline without
// learning the table shape or issuing their own SQL.
type QuotaObservationRecord struct {
	AuthIndex     string
	Provider      string
	WindowKind    string
	Scope         string
	Model         string
	WindowMinutes int
	UsedPercent   float64
	ResetAtMS     *int64
	Status        string
	ObservedAtMS  int64
	Source        string
}

type quotaObservationRow = QuotaObservationRecord

// QuotaObservationFilter narrows an observation read.
type QuotaObservationFilter struct {
	AuthIndex  string
	Provider   string
	WindowKind string
	// Source restricts to "passive" or "active"; empty reads both.
	Source string
	// FromMS and ToMS are inclusive bounds on observed_at_ms.
	FromMS int64
	ToMS   int64
}

// QuotaObservationBucketFilter names one complete window identity. Scope and
// model are part of the filter because a credential can expose several windows
// under the same kind.
type QuotaObservationBucketFilter struct {
	AuthIndex  string
	Provider   string
	WindowKind string
	Scope      string
	Model      string
	// FromMS and ToMS are inclusive bounds on observed_at_ms.
	FromMS int64
	ToMS   int64
}

// QuotaObservationBucket is one fixed-width sample of a window's history.
// Last* describes the newest real reading in the bucket, while Min/Max carry
// the reset evidence that a single reading cannot express.
type QuotaObservationBucket struct {
	StartMS          int64
	RowCount         int
	MinUsedPercent   float64
	LastUsedPercent  float64
	LastObservedAtMS int64
	LastSource       string
	LastResetAtMS    *int64
	MinResetAtMS     *int64
	MaxResetAtMS     *int64
}

// quotaObservationInsert is one row the writer may store. It carries the full
// identity of a window (kind, scope, model) so the passive and active sources
// deduplicate against the same key.
type quotaObservationInsert struct {
	AuthIndex     string
	Provider      string
	WindowKind    string
	Scope         string
	Model         string
	WindowMinutes int
	UsedPercent   float64
	ResetAtMS     *int64
	Status        string
	ObservedAtMS  int64
	EventKey      string
	Source        string
}

// quotaObservationWriter writes deduplicated observations inside one
// transaction. It is prepared once per transaction because the pool has a single
// connection and preparing per row would pay for the same statement repeatedly.
type quotaObservationWriter struct {
	insert *sql.Stmt
	recent *sql.Stmt
}

func newQuotaObservationWriter(ctx context.Context, tx *sql.Tx) (*quotaObservationWriter, error) {
	insert, err := tx.PrepareContext(ctx, `
		INSERT OR IGNORE INTO quota_observations (
			auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
			reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("prepare quota observation insert: %w", err)
	}
	// The index on (auth_index, window_kind, observed_at_ms) serves this probe
	// directly; scope and model are filtered from the same index prefix, so a
	// batch of events costs one index seek per window.
	recent, err := tx.PrepareContext(ctx, `
		SELECT used_percent, reset_at_ms, observed_at_ms FROM quota_observations
		WHERE auth_index = ? AND window_kind = ? AND scope = ? AND model = ?
		ORDER BY observed_at_ms DESC, id DESC LIMIT 1`)
	if err != nil {
		insert.Close()
		return nil, fmt.Errorf("prepare quota observation probe: %w", err)
	}
	return &quotaObservationWriter{insert: insert, recent: recent}, nil
}

func (writer *quotaObservationWriter) close() {
	if writer == nil {
		return
	}
	writer.insert.Close()
	writer.recent.Close()
}

// write records one observation unless it repeats what the newest row for the
// same credential, window, scope and model already says.
func (writer *quotaObservationWriter) write(ctx context.Context, observation quotaObservationInsert, createdAtMS int64) error {
	var recentUsedPercent float64
	var recentResetAt sql.NullInt64
	var recentObservedAt int64
	err := writer.recent.QueryRowContext(ctx, observation.AuthIndex, observation.WindowKind, observation.Scope, observation.Model).
		Scan(&recentUsedPercent, &recentResetAt, &recentObservedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("probe newest quota observation: %w", err)
	}
	if err == nil && !shouldRecordQuotaObservation(observation, recentUsedPercent, recentResetAt, recentObservedAt) {
		return nil
	}
	source := observation.Source
	if source == "" {
		source = quota.ObservationSourcePassive
	}
	if _, err := writer.insert.ExecContext(ctx,
		observation.AuthIndex, observation.Provider, observation.WindowKind, observation.Scope, observation.Model,
		observation.WindowMinutes, observation.UsedPercent, quotaObservationResetValue(observation.ResetAtMS),
		observation.Status, observation.ObservedAtMS, observation.EventKey, createdAtMS, source); err != nil {
		return fmt.Errorf("insert quota observation %s/%s: %w", observation.EventKey, observation.WindowKind, err)
	}
	return nil
}

// shouldRecordQuotaObservation is the dedup rule: a reading that repeats the
// newest one is dropped, but a changed percentage, a changed reset instant, or a
// heartbeat interval since the newest reading is kept.
func shouldRecordQuotaObservation(observation quotaObservationInsert, recentUsedPercent float64, recentResetAt sql.NullInt64, recentObservedAt int64) bool {
	if observation.UsedPercent != recentUsedPercent {
		return true
	}
	if !quotaObservationResetEqual(observation.ResetAtMS, recentResetAt) {
		return true
	}
	return observation.ObservedAtMS-recentObservedAt >= quotaObservationHeartbeatMS
}

func quotaObservationResetEqual(value *int64, recent sql.NullInt64) bool {
	if value == nil {
		return !recent.Valid
	}
	return recent.Valid && *value == recent.Int64
}

func quotaObservationResetValue(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// passiveObservationInsert converts a parsed header reading into a storable row.
func passiveObservationInsert(observation quota.PassiveObservation) quotaObservationInsert {
	return quotaObservationInsert{
		AuthIndex:     observation.AuthIndex,
		Provider:      observation.Provider,
		WindowKind:    observation.WindowKind,
		Scope:         quota.ObservationScopeStandard,
		Model:         "",
		WindowMinutes: observation.WindowMinutes,
		UsedPercent:   observation.UsedPercent,
		ResetAtMS:     observation.ResetAtMS,
		Status:        observation.Status,
		ObservedAtMS:  observation.ObservedAtMS,
		EventKey:      observation.EventKey,
		Source:        quota.ObservationSourcePassive,
	}
}

// BackfillQuotaObservations folds retained request records that predate the
// passive stream into observations, one bounded id range at a time, and returns
// how many records it considered.
//
// It walks the records by id rather than by "has headers", so the watermark
// advances over headerless records exactly once; the headers of a record are
// read only after it is selected. A repeated pass inserts nothing new because
// UNIQUE(event_key, window_kind) ignores duplicates.
func (r *Repository) BackfillQuotaObservations(ctx context.Context, batchSize int) (int, error) {
	if r == nil || r.SQL() == nil {
		return 0, errors.New("repository is not initialized")
	}
	if batchSize <= 0 {
		batchSize = 2000
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin quota observation backfill: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var lastID int64
	err = tx.QueryRowContext(ctx, `
		SELECT last_usage_event_id FROM usage_aggregation_checkpoints WHERE name = ?`, CheckpointQuotaObservations).Scan(&lastID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("read quota observation checkpoint: %w", err)
	}
	var highID sql.NullInt64
	var considered int
	err = tx.QueryRowContext(ctx, `
		SELECT MAX(id), COUNT(1) FROM (SELECT id FROM usage_events WHERE id > ? ORDER BY id ASC LIMIT ?)`,
		lastID, batchSize).Scan(&highID, &considered)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("find quota observation high water: %w", err)
	}
	if !highID.Valid {
		return 0, nil
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT auth_index, event_key, timestamp_ms, response_headers FROM usage_events
		WHERE id > ? AND id <= ? AND response_headers <> ''
		ORDER BY timestamp_ms ASC, id ASC`,
		lastID, highID.Int64)
	if err != nil {
		return 0, fmt.Errorf("read usage events for quota observations: %w", err)
	}
	writer, err := newQuotaObservationWriter(ctx, tx)
	if err != nil {
		rows.Close()
		return 0, err
	}
	defer writer.close()
	createdMS := time.Now().UnixMilli()
	for rows.Next() {
		var authIndex, eventKey, storedHeaders string
		var observedAt int64
		if err := rows.Scan(&authIndex, &eventKey, &observedAt, &storedHeaders); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan usage event for quota observation: %w", err)
		}
		event := usage.Event{AuthIndex: authIndex, EventKey: eventKey, TimestampMS: observedAt, ResponseHeaders: storedHeaders}
		for _, observation := range quota.ObservationsFromHeaders(event) {
			if err := writer.write(ctx, passiveObservationInsert(observation), createdMS); err != nil {
				rows.Close()
				return 0, err
			}
		}
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate usage events for quota observations: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO usage_aggregation_checkpoints (name, last_usage_event_id, stats_updated_at_ms, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
			last_usage_event_id = MAX(excluded.last_usage_event_id, usage_aggregation_checkpoints.last_usage_event_id),
			stats_updated_at_ms = excluded.stats_updated_at_ms,
			updated_at_ms = excluded.updated_at_ms`,
		CheckpointQuotaObservations, highID.Int64, createdMS, createdMS, createdMS); err != nil {
		return 0, fmt.Errorf("advance quota observation checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit quota observation backfill: %w", err)
	}
	return considered, nil
}

// ListQuotaObservations reads stored observations oldest first within the
// filter. It asks for one row more than the limit so the caller can report an
// over-limit result rather than silently truncate it.
func (r *Repository) ListQuotaObservations(ctx context.Context, filter QuotaObservationFilter, limit int) ([]quotaObservationRow, bool, error) {
	if r == nil || r.SQL() == nil {
		return nil, false, errors.New("repository is not initialized")
	}
	if limit <= 0 {
		return nil, false, errors.New("limit must be positive")
	}
	conditions := []string{"observed_at_ms >= ?", "observed_at_ms <= ?"}
	arguments := []any{filter.FromMS, filter.ToMS}
	if filter.AuthIndex != "" {
		conditions = append(conditions, "auth_index = ?")
		arguments = append(arguments, filter.AuthIndex)
	}
	if filter.Provider != "" {
		conditions = append(conditions, "provider = ?")
		arguments = append(arguments, filter.Provider)
	}
	if filter.WindowKind != "" {
		conditions = append(conditions, "window_kind = ?")
		arguments = append(arguments, filter.WindowKind)
	}
	if filter.Source != "" {
		conditions = append(conditions, "source = ?")
		arguments = append(arguments, filter.Source)
	}
	arguments = append(arguments, limit+1)
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT auth_index, provider, window_kind, scope, model, window_minutes, used_percent, reset_at_ms, status, observed_at_ms, source
		FROM quota_observations
		WHERE `+strings.Join(conditions, " AND ")+`
		ORDER BY observed_at_ms ASC, id ASC
		LIMIT ?`, arguments...)
	if err != nil {
		return nil, false, fmt.Errorf("list quota observations: %w", err)
	}
	defer rows.Close()
	result := []quotaObservationRow{}
	for rows.Next() {
		var observation quotaObservationRow
		var resetAt sql.NullInt64
		if err := rows.Scan(&observation.AuthIndex, &observation.Provider, &observation.WindowKind,
			&observation.Scope, &observation.Model, &observation.WindowMinutes, &observation.UsedPercent,
			&resetAt, &observation.Status, &observation.ObservedAtMS, &observation.Source); err != nil {
			return nil, false, fmt.Errorf("scan quota observation: %w", err)
		}
		if resetAt.Valid {
			value := resetAt.Int64
			observation.ResetAtMS = &value
		}
		result = append(result, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate quota observations: %w", err)
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, nil
}

// ListQuotaObservationBuckets aggregates one complete window identity into
// fixed-width samples before the caller's limit is applied. Filtering and
// aggregation both happen in SQL: applying a row limit first would turn a busy
// window into an artificial incomplete-history verdict.
//
// Empty buckets are not synthesized. Buckets are ordered oldest first, and the
// extra row read beyond limit reports overflow the same way ListQuotaObservations
// does.
func (r *Repository) ListQuotaObservationBuckets(ctx context.Context, filter QuotaObservationBucketFilter, limit int) ([]QuotaObservationBucket, bool, error) {
	if r == nil || r.SQL() == nil {
		return nil, false, errors.New("repository is not initialized")
	}
	if limit <= 0 {
		return nil, false, errors.New("limit must be positive")
	}
	rows, err := r.SQL().QueryContext(ctx, `
		WITH filtered AS (
			SELECT id, used_percent, reset_at_ms, observed_at_ms, source,
			       (observed_at_ms / ?) * ? AS bucket_start_ms
			FROM quota_observations
			WHERE auth_index = ? AND provider = ? AND window_kind = ? AND scope = ? AND model = ?
			  AND observed_at_ms >= ? AND observed_at_ms <= ?
		), ranked AS (
			SELECT bucket_start_ms, used_percent, reset_at_ms, observed_at_ms, source,
			       ROW_NUMBER() OVER (
			           PARTITION BY bucket_start_ms
			           ORDER BY observed_at_ms DESC, id DESC
			       ) AS reading_rank
			FROM filtered
		)
		SELECT bucket_start_ms,
		       COUNT(1),
		       MIN(used_percent),
		       MIN(reset_at_ms),
		       MAX(reset_at_ms),
		       MAX(CASE WHEN reading_rank = 1 THEN used_percent END),
		       MAX(CASE WHEN reading_rank = 1 THEN observed_at_ms END),
		       MAX(CASE WHEN reading_rank = 1 THEN source END),
		       MAX(CASE WHEN reading_rank = 1 THEN reset_at_ms END)
		FROM ranked
		GROUP BY bucket_start_ms
		ORDER BY bucket_start_ms ASC
		LIMIT ?`,
		quota.PredictionBucketMS, quota.PredictionBucketMS,
		filter.AuthIndex, filter.Provider, filter.WindowKind, filter.Scope, filter.Model,
		filter.FromMS, filter.ToMS, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list quota observation buckets: %w", err)
	}
	defer rows.Close()
	result := []QuotaObservationBucket{}
	for rows.Next() {
		var bucket QuotaObservationBucket
		var minReset, maxReset, lastReset sql.NullInt64
		if err := rows.Scan(&bucket.StartMS, &bucket.RowCount, &bucket.MinUsedPercent,
			&minReset, &maxReset, &bucket.LastUsedPercent, &bucket.LastObservedAtMS,
			&bucket.LastSource, &lastReset); err != nil {
			return nil, false, fmt.Errorf("scan quota observation bucket: %w", err)
		}
		if minReset.Valid {
			value := minReset.Int64
			bucket.MinResetAtMS = &value
		}
		if maxReset.Valid {
			value := maxReset.Int64
			bucket.MaxResetAtMS = &value
		}
		if lastReset.Valid {
			value := lastReset.Int64
			bucket.LastResetAtMS = &value
		}
		result = append(result, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate quota observation buckets: %w", err)
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, nil
}

// CountQuotaObservations is the compact check a maintenance test uses to see a
// backfill's effect without depending on row order.
func (r *Repository) CountQuotaObservations(ctx context.Context) (int64, error) {
	if r == nil || r.SQL() == nil {
		return 0, errors.New("repository is not initialized")
	}
	var count int64
	if err := r.SQL().QueryRowContext(ctx, `SELECT COUNT(1) FROM quota_observations`).Scan(&count); err != nil {
		return 0, fmt.Errorf("count quota observations: %w", err)
	}
	return count, nil
}

// RecordActiveQuotaObservations writes the windows a completed refresh produced
// into the observation timeline, one transaction for the batch.
//
// It is called from the same persistence step as the snapshot, so a manual
// refresh, an MCP refresh and the scheduler all leave the same evidence. Rows
// are inserted with OR IGNORE, so re-running a refresh at the same instant is
// idempotent through the synthetic event key.
func (r *Repository) RecordActiveQuotaObservations(ctx context.Context, value *quota.NormalizedQuota) (int, error) {
	if r == nil || r.SQL() == nil {
		return 0, errors.New("repository is not initialized")
	}
	active := quota.ActiveObservationsFromQuota(value)
	if len(active) == 0 {
		return 0, nil
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin active observation write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	writer, err := newQuotaObservationWriter(ctx, tx)
	if err != nil {
		return 0, err
	}
	defer writer.close()
	createdMS := time.Now().UnixMilli()
	for _, observation := range active {
		row := quotaObservationInsert{
			AuthIndex:     observation.AuthIndex,
			Provider:      observation.Provider,
			WindowKind:    observation.WindowKind,
			Scope:         observation.Scope,
			Model:         observation.Model,
			WindowMinutes: observation.WindowMinutes,
			UsedPercent:   observation.UsedPercent,
			ResetAtMS:     observation.ResetAtMS,
			ObservedAtMS:  observation.ObservedAtMS,
			EventKey:      observation.EventKey,
			Source:        quota.ObservationSourceActive,
		}
		if err := writer.write(ctx, row, createdMS); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit active observations: %w", err)
	}
	return len(active), nil
}

// LatestStandardPassiveObservations returns, per auth index and passive window
// kind, the newest standard passive row. It is the read the window overlay and
// the scheduler's passive-freshness check share.
func (r *Repository) LatestStandardPassiveObservations(ctx context.Context, authIndexes []string) (map[string]map[string]quotaObservationRow, error) {
	result := map[string]map[string]quotaObservationRow{}
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
	placeholders := strings.Repeat("?,", len(valid))
	placeholders = placeholders[:len(placeholders)-1]
	arguments := make([]any, 0, len(valid))
	for _, authIndex := range valid {
		arguments = append(arguments, authIndex)
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT auth_index, provider, window_kind, scope, model, window_minutes, used_percent, reset_at_ms, status, observed_at_ms, source
		FROM quota_observations
		WHERE source = '`+quota.ObservationSourcePassive+`' AND scope = '`+quota.ObservationScopeStandard+`'
		  AND window_kind IN (?, ?) AND auth_index IN (`+placeholders+`)
		ORDER BY observed_at_ms DESC, id DESC`,
		append([]any{quota.PassiveWindowFiveHour, quota.PassiveWindowWeekly}, arguments...)...)
	if err != nil {
		return result, fmt.Errorf("query latest passive observations: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var observation quotaObservationRow
		var resetAt sql.NullInt64
		if err := rows.Scan(&observation.AuthIndex, &observation.Provider, &observation.WindowKind,
			&observation.Scope, &observation.Model, &observation.WindowMinutes, &observation.UsedPercent,
			&resetAt, &observation.Status, &observation.ObservedAtMS, &observation.Source); err != nil {
			return result, fmt.Errorf("scan latest passive observation: %w", err)
		}
		if resetAt.Valid {
			value := resetAt.Int64
			observation.ResetAtMS = &value
		}
		byKind, ok := result[observation.AuthIndex]
		if !ok {
			byKind = map[string]quotaObservationRow{}
			result[observation.AuthIndex] = byKind
		}
		// The query is ordered newest-first, so the first row seen for a kind is
		// the one to keep.
		if _, exists := byKind[observation.WindowKind]; !exists {
			byKind[observation.WindowKind] = observation
		}
	}
	return result, rows.Err()
}

// CountUsageEventsByAuthIndex counts the requests each credential served since an
// instant, in one grouped read. The scheduler uses it to tier credentials without
// one query per credential.
func (r *Repository) CountUsageEventsByAuthIndex(ctx context.Context, instanceID string, sinceMS int64) (map[string]int64, error) {
	result := map[string]int64{}
	if r == nil || r.SQL() == nil {
		return result, nil
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT auth_index, COUNT(1) FROM usage_events
		WHERE instance_id = ? AND auth_index <> '' AND timestamp_ms >= ?
		GROUP BY auth_index`, strings.TrimSpace(instanceID), sinceMS)
	if err != nil {
		return result, fmt.Errorf("count usage events by auth index: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var authIndex string
		var count int64
		if err := rows.Scan(&authIndex, &count); err != nil {
			return result, fmt.Errorf("scan usage event count: %w", err)
		}
		result[authIndex] = count
	}
	return result, rows.Err()
}
