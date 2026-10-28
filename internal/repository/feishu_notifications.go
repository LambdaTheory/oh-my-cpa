package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Feishu notification persistence (migration 045).
//
// The repository stays free of the feishu package: the module-specific payloads
// the bot renders (alert rows, pending items, digests) travel as opaque JSON
// strings that the API-layer adapter marshals and unmarshals. The app secret is a
// separate table and is returned only through LoadFeishuSecret, which the service
// adapter calls when it builds the sender; no ordinary read selects it.

// FeishuSettings is the non-secret web configuration.
type FeishuSettings struct {
	Enabled                 bool
	AppID                   string
	ChatID                  string
	ConfirmedRecipient      string
	SummaryTimesJSON        string
	Timezone                string
	QuietStart              string
	QuietEnd                string
	TightRemainingPercent   float64
	ChannelUnavailableAfter int64 // milliseconds
	MonitorDownAfterMS      int64
	UpdatedAtMS             int64
}

// FeishuPendingItem is one persisted pending item.
type FeishuPendingItem struct {
	ID             string
	Kind           string
	Provider       string
	CredKey        string
	Alias          string
	SinceMS        *int64
	LastSeenMS     *int64
	Action         string
	Status         string
	NotifiedAtMS   *int64
	ResolvedAtMS   *int64
	CycleResetAtMS *int64
	UpdatedAtMS    int64
}

// FeishuNotification is one persisted outbox row: a pending/queued/sent delivery.
type FeishuNotification struct {
	LastError       string
	ID              string
	Kind            string
	ItemIDsJSON     string
	AlertsJSON      string
	PendingJSON     string
	TightJSON       string
	State           string
	Attempts        int
	NextAttemptAtMS *int64
	SentAtMS        *int64
	OccurredAtMS    *int64
	CreatedAtMS     int64
	UpdatedAtMS     int64
}

// FeishuChannelTrack is one provider's monitor_down memory.
type FeishuChannelTrack struct {
	Provider    string
	FirstSeenMS *int64
	LastValidMS *int64
	UpdatedAtMS int64
}

// FeishuRuntime is the whole runtime document the bot loads and saves.
type FeishuRuntime struct {
	Pending         []FeishuPendingItem
	Notifications   []FeishuNotification
	Tracks          []FeishuChannelTrack
	Bootstrapped    bool
	LastSummaryAtMS *int64
}

// LoadFeishuSettings reads the single settings row. found is false before the row
// exists.
func (r *Repository) LoadFeishuSettings(ctx context.Context) (FeishuSettings, bool, error) {
	if r == nil || r.SQL() == nil {
		return FeishuSettings{}, false, errors.New("repository is not initialized")
	}
	var settings FeishuSettings
	err := r.SQL().QueryRowContext(ctx, `
		SELECT enabled, app_id, chat_id, summary_times_json, timezone, quiet_start, quiet_end,
		       tight_remaining_percent, channel_unavailable_after_ms, monitor_down_after_ms, updated_at_ms, confirmed_recipient
		FROM feishu_settings WHERE id = 1`).Scan(
		&settings.Enabled, &settings.AppID, &settings.ChatID, &settings.SummaryTimesJSON,
		&settings.Timezone, &settings.QuietStart, &settings.QuietEnd,
		&settings.TightRemainingPercent, &settings.ChannelUnavailableAfter, &settings.MonitorDownAfterMS,
		&settings.UpdatedAtMS, &settings.ConfirmedRecipient,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return FeishuSettings{}, false, nil
	}
	if err != nil {
		return FeishuSettings{}, false, fmt.Errorf("read feishu settings: %w", err)
	}
	return settings, true, nil
}

// SaveFeishuSettings upserts the settings row. It never touches the secret.
func (r *Repository) SaveFeishuSettings(ctx context.Context, settings FeishuSettings) error {
	if r == nil || r.SQL() == nil {
		return errors.New("repository is not initialized")
	}
	nowMS := settings.UpdatedAtMS
	if nowMS <= 0 {
		nowMS = time.Now().UnixMilli()
	}
	_, err := r.SQL().ExecContext(ctx, `
		INSERT INTO feishu_settings (
			id, enabled, app_id, chat_id, summary_times_json, timezone, quiet_start, quiet_end,
			tight_remaining_percent, channel_unavailable_after_ms, monitor_down_after_ms, updated_at_ms, confirmed_recipient
		) VALUES (1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			enabled = excluded.enabled,
			app_id = excluded.app_id,
			chat_id = excluded.chat_id,
			summary_times_json = excluded.summary_times_json,
			timezone = excluded.timezone,
			quiet_start = excluded.quiet_start,
			quiet_end = excluded.quiet_end,
			tight_remaining_percent = excluded.tight_remaining_percent,
			channel_unavailable_after_ms = excluded.channel_unavailable_after_ms,
			monitor_down_after_ms = excluded.monitor_down_after_ms,
			updated_at_ms = excluded.updated_at_ms,
			confirmed_recipient = excluded.confirmed_recipient`,
		boolToInt(settings.Enabled), strings.TrimSpace(settings.AppID), strings.TrimSpace(settings.ChatID),
		defaultJSON(settings.SummaryTimesJSON, "[]"), strings.TrimSpace(settings.Timezone),
		strings.TrimSpace(settings.QuietStart), strings.TrimSpace(settings.QuietEnd),
		settings.TightRemainingPercent, settings.ChannelUnavailableAfter, settings.MonitorDownAfterMS, nowMS, settings.ConfirmedRecipient)
	if err != nil {
		return fmt.Errorf("save feishu settings: %w", err)
	}
	return nil
}

// LoadFeishuSecret returns the encrypted app secret. has is false when none is
// stored. It is the only read that selects the ciphertext.
func (r *Repository) LoadFeishuSecret(ctx context.Context) (ciphertext, nonce []byte, has bool, err error) {
	if r == nil || r.SQL() == nil {
		return nil, nil, false, errors.New("repository is not initialized")
	}
	err = r.SQL().QueryRowContext(ctx, `
		SELECT app_secret_ciphertext, app_secret_nonce FROM feishu_secrets WHERE id = 1`).
		Scan(&ciphertext, &nonce)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("read feishu secret: %w", err)
	}
	if len(ciphertext) == 0 {
		return nil, nil, false, nil
	}
	return ciphertext, nonce, true, nil
}

// SaveFeishuSecret upserts the encrypted app secret.
func (r *Repository) SaveFeishuSecret(ctx context.Context, ciphertext, nonce []byte) error {
	if r == nil || r.SQL() == nil {
		return errors.New("repository is not initialized")
	}
	if len(ciphertext) == 0 {
		return errors.New("encrypted app secret is required")
	}
	nowMS := time.Now().UnixMilli()
	_, err := r.SQL().ExecContext(ctx, `
		INSERT INTO feishu_secrets (id, app_secret_ciphertext, app_secret_nonce, app_secret_updated_at_ms, updated_at_ms)
		VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			app_secret_ciphertext = excluded.app_secret_ciphertext,
			app_secret_nonce = excluded.app_secret_nonce,
			app_secret_updated_at_ms = excluded.app_secret_updated_at_ms,
			updated_at_ms = excluded.updated_at_ms`,
		ciphertext, nonce, nowMS, nowMS)
	if err != nil {
		return fmt.Errorf("save feishu secret: %w", err)
	}
	return nil
}

// LoadFeishuRuntime reads the whole runtime document. A missing document yields an
// empty one.
func (r *Repository) LoadFeishuRuntime(ctx context.Context) (FeishuRuntime, error) {
	if r == nil || r.SQL() == nil {
		return FeishuRuntime{}, errors.New("repository is not initialized")
	}
	runtime := FeishuRuntime{}
	if err := r.loadFeishuState(ctx, &runtime); err != nil {
		return FeishuRuntime{}, err
	}
	pending, err := r.listFeishuPendingItems(ctx)
	if err != nil {
		return FeishuRuntime{}, err
	}
	notifications, err := r.listFeishuNotifications(ctx)
	if err != nil {
		return FeishuRuntime{}, err
	}
	tracks, err := r.listFeishuChannelTracks(ctx)
	if err != nil {
		return FeishuRuntime{}, err
	}
	runtime.Pending = pending
	runtime.Notifications = notifications
	runtime.Tracks = tracks
	return runtime, nil
}

func (r *Repository) loadFeishuState(ctx context.Context, runtime *FeishuRuntime) error {
	var bootstrapped int
	var lastSummary sql.NullInt64
	err := r.SQL().QueryRowContext(ctx, `
		SELECT bootstrapped, last_summary_at_ms FROM feishu_state WHERE id = 1`).
		Scan(&bootstrapped, &lastSummary)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read feishu state: %w", err)
	}
	runtime.Bootstrapped = bootstrapped != 0
	runtime.LastSummaryAtMS = nullInt64Ptr(lastSummary)
	return nil
}

// SaveFeishuRuntime replaces the runtime document transactionally: the single-row
// memory is upserted and the three list tables are rewritten.
func (r *Repository) SaveFeishuRuntime(ctx context.Context, runtime FeishuRuntime) error {
	if r == nil || r.SQL() == nil {
		return errors.New("repository is not initialized")
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin feishu runtime transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	nowMS := time.Now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO feishu_state (id, bootstrapped, last_summary_at_ms, updated_at_ms)
		VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			bootstrapped = excluded.bootstrapped,
			last_summary_at_ms = excluded.last_summary_at_ms,
			updated_at_ms = excluded.updated_at_ms`,
		boolToInt(runtime.Bootstrapped), nullMS(runtime.LastSummaryAtMS), nowMS); err != nil {
		return fmt.Errorf("save feishu state: %w", err)
	}

	for _, table := range []string{"feishu_pending_items", "feishu_notifications", "feishu_channel_tracks"} {
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+table); err != nil {
			return fmt.Errorf("clear %s: %w", table, err)
		}
	}
	for _, item := range runtime.Pending {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO feishu_pending_items (
				id, kind, provider, cred_key, alias, since_ms, last_seen_ms, action, status,
				notified_at_ms, resolved_at_ms, updated_at_ms, cycle_reset_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.Kind, item.Provider, item.CredKey, item.Alias,
			nullMS(item.SinceMS), nullMS(item.LastSeenMS), item.Action, defaultString(item.Status, "open"),
			nullMS(item.NotifiedAtMS), nullMS(item.ResolvedAtMS), nowMS, nullMS(item.CycleResetAtMS)); err != nil {
			return fmt.Errorf("insert feishu pending item %q: %w", item.ID, err)
		}
	}
	for _, notification := range runtime.Notifications {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO feishu_notifications (
				id, kind, item_ids_json, alerts_json, pending_json, tight_json, state, attempts,
				next_attempt_at_ms, sent_at_ms, occurred_at_ms, created_at_ms, updated_at_ms, last_error
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			notification.ID, notification.Kind,
			defaultJSON(notification.ItemIDsJSON, "[]"), defaultJSON(notification.AlertsJSON, "[]"),
			defaultJSON(notification.PendingJSON, "[]"), defaultJSON(notification.TightJSON, "[]"),
			defaultString(notification.State, "pending"), notification.Attempts,
			nullMS(notification.NextAttemptAtMS), nullMS(notification.SentAtMS), nullMS(notification.OccurredAtMS),
			notification.CreatedAtMS, nowMS, notification.LastError); err != nil {
			return fmt.Errorf("insert feishu notification %q: %w", notification.ID, err)
		}
	}
	for _, track := range runtime.Tracks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO feishu_channel_tracks (provider, first_seen_ms, last_valid_ms, updated_at_ms)
			VALUES (?, ?, ?, ?)
			ON CONFLICT(provider) DO UPDATE SET
				first_seen_ms = excluded.first_seen_ms,
				last_valid_ms = excluded.last_valid_ms,
				updated_at_ms = excluded.updated_at_ms`,
			track.Provider, nullMS(track.FirstSeenMS), nullMS(track.LastValidMS), nowMS); err != nil {
			return fmt.Errorf("insert feishu channel track %q: %w", track.Provider, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit feishu runtime: %w", err)
	}
	return nil
}

func (r *Repository) listFeishuPendingItems(ctx context.Context) ([]FeishuPendingItem, error) {
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT id, kind, provider, cred_key, alias, since_ms, last_seen_ms, action, status,
		       notified_at_ms, resolved_at_ms, updated_at_ms, cycle_reset_at_ms
		FROM feishu_pending_items ORDER BY since_ms ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list feishu pending items: %w", err)
	}
	defer rows.Close()
	items := []FeishuPendingItem{}
	for rows.Next() {
		var item FeishuPendingItem
		var since, lastSeen, notified, resolved, updated, cycleReset sql.NullInt64
		if err := rows.Scan(&item.ID, &item.Kind, &item.Provider, &item.CredKey, &item.Alias,
			&since, &lastSeen, &item.Action, &item.Status, &notified, &resolved, &updated, &cycleReset); err != nil {
			return nil, fmt.Errorf("scan feishu pending item: %w", err)
		}
		item.SinceMS = nullInt64Ptr(since)
		item.LastSeenMS = nullInt64Ptr(lastSeen)
		item.NotifiedAtMS = nullInt64Ptr(notified)
		item.ResolvedAtMS = nullInt64Ptr(resolved)
		item.CycleResetAtMS = nullInt64Ptr(cycleReset)
		item.UpdatedAtMS = updated.Int64
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *Repository) listFeishuNotifications(ctx context.Context) ([]FeishuNotification, error) {
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT id, kind, item_ids_json, alerts_json, pending_json, tight_json, state, attempts,
		       next_attempt_at_ms, sent_at_ms, occurred_at_ms, created_at_ms, updated_at_ms, last_error
		FROM feishu_notifications ORDER BY created_at_ms ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list feishu notifications: %w", err)
	}
	defer rows.Close()
	notifications := []FeishuNotification{}
	for rows.Next() {
		var notification FeishuNotification
		var nextAttempt, sentAt, occurred, updated sql.NullInt64
		if err := rows.Scan(&notification.ID, &notification.Kind, &notification.ItemIDsJSON,
			&notification.AlertsJSON, &notification.PendingJSON, &notification.TightJSON,
			&notification.State, &notification.Attempts, &nextAttempt, &sentAt, &occurred,
			&notification.CreatedAtMS, &updated, &notification.LastError); err != nil {
			return nil, fmt.Errorf("scan feishu notification: %w", err)
		}
		notification.NextAttemptAtMS = nullInt64Ptr(nextAttempt)
		notification.SentAtMS = nullInt64Ptr(sentAt)
		notification.OccurredAtMS = nullInt64Ptr(occurred)
		notification.UpdatedAtMS = updated.Int64
		notifications = append(notifications, notification)
	}
	return notifications, rows.Err()
}

func (r *Repository) listFeishuChannelTracks(ctx context.Context) ([]FeishuChannelTrack, error) {
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT provider, first_seen_ms, last_valid_ms, updated_at_ms
		FROM feishu_channel_tracks ORDER BY provider ASC`)
	if err != nil {
		return nil, fmt.Errorf("list feishu channel tracks: %w", err)
	}
	defer rows.Close()
	tracks := []FeishuChannelTrack{}
	for rows.Next() {
		var track FeishuChannelTrack
		var firstSeen, lastValid, updated sql.NullInt64
		if err := rows.Scan(&track.Provider, &firstSeen, &lastValid, &updated); err != nil {
			return nil, fmt.Errorf("scan feishu channel track: %w", err)
		}
		track.FirstSeenMS = nullInt64Ptr(firstSeen)
		track.LastValidMS = nullInt64Ptr(lastValid)
		track.UpdatedAtMS = updated.Int64
		tracks = append(tracks, track)
	}
	return tracks, rows.Err()
}

func nullMS(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func defaultJSON(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
