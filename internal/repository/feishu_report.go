package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/domain"
)

// Safe collection-signal states the Feishu report reads. They are a fixed
// vocabulary: no raw upstream error, token or secret is ever stored, only the
// classified outcome of one collection.
const (
	// FeishuSignalHealthy marks a collection that completed successfully.
	FeishuSignalHealthy = "healthy"
	// FeishuSignalAuthInvalid marks an explicit authentication failure.
	FeishuSignalAuthInvalid = "auth_invalid"
	// FeishuSignalTransport marks a non-auth transport or upstream failure.
	FeishuSignalTransport = "transport"
	// FeishuSignalParse marks a response that could not be parsed.
	FeishuSignalParse = "parse"
	// FeishuSignalUnknown marks a result the classifier could not place.
	FeishuSignalUnknown = "unknown"
)

// FeishuAccountSignal is the newest safe collection outcome for one credential. It
// carries only the classified state word and the instant it was observed, so the
// Feishu report can tell an auth failure from a transport one without a live read.
type FeishuAccountSignal struct {
	AuthIndex string
	Provider  string
	State     string
	// ObservedAt is when the classified collection outcome was observed, never a
	// fabricated "now". The zero value means unknown.
	ObservedAt time.Time
}

// SaveFeishuAccountSignal upserts the newest collection outcome for a credential.
// One row per auth index keeps the table bounded; only the fixed state vocabulary
// and the observation instant are stored.
func (r *Repository) SaveFeishuAccountSignal(ctx context.Context, signal FeishuAccountSignal) error {
	if r == nil || r.SQL() == nil {
		return errors.New("repository is not initialized")
	}
	authIndex := strings.TrimSpace(signal.AuthIndex)
	if authIndex == "" {
		return errors.New("auth_index is required")
	}
	state := strings.TrimSpace(signal.State)
	switch state {
	case FeishuSignalHealthy, FeishuSignalAuthInvalid, FeishuSignalTransport, FeishuSignalParse, FeishuSignalUnknown:
	default:
		state = FeishuSignalUnknown
	}
	nowMS := time.Now().UnixMilli()
	observedMS := int64(0)
	if !signal.ObservedAt.IsZero() {
		observedMS = signal.ObservedAt.UnixMilli()
	}
	_, err := r.SQL().ExecContext(ctx, `
		INSERT INTO feishu_account_signals (auth_index, provider, state, observed_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(auth_index) DO UPDATE SET
			provider = excluded.provider,
			state = excluded.state,
			observed_at_ms = excluded.observed_at_ms,
			updated_at_ms = excluded.updated_at_ms
		WHERE feishu_account_signals.state <> '`+FeishuSignalAuthInvalid+`'
		   OR excluded.state = '`+FeishuSignalAuthInvalid+`'
		   OR excluded.state = '`+FeishuSignalHealthy+`'`,
		authIndex, strings.TrimSpace(signal.Provider), state, observedMS, nowMS)
	if err != nil {
		return fmt.Errorf("save feishu account signal: %w", err)
	}
	return nil
}

// GetFeishuAccountSignals reads the newest collection outcome for each auth index
// in one batch. A credential with no stored signal is absent from the map.
func (r *Repository) GetFeishuAccountSignals(ctx context.Context, authIndexes []string) (map[string]FeishuAccountSignal, error) {
	result := map[string]FeishuAccountSignal{}
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
	args := make([]any, 0, len(valid))
	for _, authIndex := range valid {
		args = append(args, authIndex)
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT auth_index, provider, state, observed_at_ms
		FROM feishu_account_signals
		WHERE auth_index IN (`+placeholders+`)`, args...)
	if err != nil {
		return result, fmt.Errorf("read feishu account signals: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var signal FeishuAccountSignal
		var observedMS int64
		if err := rows.Scan(&signal.AuthIndex, &signal.Provider, &signal.State, &observedMS); err != nil {
			return result, fmt.Errorf("scan feishu account signal: %w", err)
		}
		if observedMS > 0 {
			signal.ObservedAt = time.UnixMilli(observedMS).UTC()
		}
		result[signal.AuthIndex] = signal
	}
	return result, rows.Err()
}

// ListKnownQuotaProviders returns every provider label the append-only quota
// observation timeline has ever recorded, alphabetically.
//
// It exists for the Feishu report source: when the gateway's credential list
// cannot be read, this is how a channel that was configured earlier is still
// named so a collector-level outage can be timed from its persisted first-seen
// instant. The observation timeline is kept for a year, so a channel remains
// known across a gateway outage.
func (r *Repository) ListKnownQuotaProviders(ctx context.Context) ([]string, error) {
	if r == nil || r.SQL() == nil {
		return nil, errors.New("repository is not initialized")
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT DISTINCT provider FROM quota_observations
		WHERE provider <> ''
		ORDER BY provider ASC`)
	if err != nil {
		return nil, fmt.Errorf("list known quota providers: %w", err)
	}
	defer rows.Close()
	providers := []string{}
	for rows.Next() {
		var provider string
		if err := rows.Scan(&provider); err != nil {
			return nil, fmt.Errorf("scan known quota provider: %w", err)
		}
		if trimmed := strings.TrimSpace(provider); trimmed != "" {
			providers = append(providers, trimmed)
		}
	}
	return providers, rows.Err()
}

// FeishuCredentialMetadata is the safe, stored identity of one CPA credential
// that the Feishu report source may answer from without contacting CPA.
//
// It is read from discovered_resources, the table the discovery flow writes.
// Disabled and Unavailable are cached observations from the last successful
// sync, so ObservedAt records how fresh that metadata is. It is the last-synced
// instant, never the instant of the query: a query must not pretend the cached
// disable state is live.
type FeishuCredentialMetadata struct {
	AuthIndex    string
	Name         string
	Driver       string
	ResourceType string
	// Disabled and Unavailable are the last discovery sync's view of the
	// credential. They are the cache the report must consult instead of asking
	// CPA, because a query must not trigger a management read.
	Disabled    bool
	Unavailable bool
	// ObservedAt is when the credential metadata was last synced. The zero value
	// means unknown.
	ObservedAt time.Time
}

// ListFeishuCredentialMetadata returns the stored auth-file credentials the
// Feishu report may name: one row per non-missing discovered auth file that has
// a non-empty auth index (the key quota snapshots are stored under). It performs
// no CPA request, so it is safe to call on every query and tick.
//
// A credential CPA has since removed is excluded via status = 'missing' rather
// than reported as still configured. A resource whose auth index is empty cannot
// be joined to a snapshot and is skipped; that collision is surfaced elsewhere.
func (r *Repository) ListFeishuCredentialMetadata(ctx context.Context) ([]FeishuCredentialMetadata, error) {
	if r == nil || r.SQL() == nil {
		return nil, errors.New("repository is not initialized")
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT cpa_auth_index, cpa_resource_name, cpa_driver, cpa_resource_type,
		       details_json, last_seen_at
		FROM discovered_resources
		WHERE cpa_auth_index <> ''
		  AND cpa_resource_type = 'auth-file'
		  AND status <> 'missing'
		ORDER BY cpa_auth_index ASC`)
	if err != nil {
		return nil, fmt.Errorf("list feishu credential metadata: %w", err)
	}
	defer rows.Close()

	result := []FeishuCredentialMetadata{}
	seen := map[string]bool{}
	for rows.Next() {
		var (
			authIndex    string
			name         sql.NullString
			driver       sql.NullString
			resourceType sql.NullString
			detailsJSON  sql.NullString
			lastSeen     sql.NullInt64
		)
		if err := rows.Scan(&authIndex, &name, &driver, &resourceType, &detailsJSON, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan feishu credential metadata: %w", err)
		}
		authIndex = strings.TrimSpace(authIndex)
		if authIndex == "" || seen[authIndex] {
			// Two discovered rows can share an auth index (an identity
			// collision). The first, in auth-index order, is the reporting
			// identity; the join key is unique either way.
			continue
		}
		seen[authIndex] = true
		meta := FeishuCredentialMetadata{
			AuthIndex:    authIndex,
			Name:         strings.TrimSpace(name.String),
			Driver:       strings.TrimSpace(driver.String),
			ResourceType: strings.TrimSpace(resourceType.String),
		}
		if detailsJSON.Valid && strings.TrimSpace(detailsJSON.String) != "" {
			var details domain.ResourceDetails
			if err := json.Unmarshal([]byte(detailsJSON.String), &details); err == nil {
				// Reuse the same allowlist the resource UI reads, so the report
				// can never widen the credential metadata a caller sees.
				details = sanitizeResourceDetails(details)
				meta.Disabled = details.Disabled
				meta.Unavailable = details.Unavailable
			}
		}
		if lastSeen.Valid && lastSeen.Int64 > 0 {
			meta.ObservedAt = time.Unix(lastSeen.Int64, 0).UTC()
		}
		result = append(result, meta)
	}
	return result, rows.Err()
}
