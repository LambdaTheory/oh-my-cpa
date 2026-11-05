package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

// Repository-owned caller API keys (PRD plans/prd/omc-repo-api-keys).
//
// CPA authenticates a caller key by its presence in `api-keys`; it has no disabled
// state. Everything about "which repository owns this key, who received it, why it
// was stopped" therefore lives here, keyed by the usage fingerprint the request
// records already carry. The repository layer only stores and moves that metadata:
// issuing, encrypting and delivering a key is the service's job, and the key value
// itself is never written to these tables.

var (
	ErrRepoKeyNotFound             = errors.New("repo_key_not_found")
	ErrRepoKeyConflict             = errors.New("repo_key_conflict")
	ErrRepoKeyApplicationPending   = errors.New("repo_key_application_pending")
	ErrRepoKeyApplicationNotFound  = errors.New("repo_key_application_not_found")
	ErrRepoKeyInvalid              = errors.New("repo_key_invalid")
	ErrRepoKeyBindingNotReenable   = errors.New("repo_key_not_reenable")
	ErrRepoKeyAuditFailed          = errors.New("repo_key_audit_failed")
	ErrRepoKeyDeliveryAlreadyTaken = errors.New("repo_key_application_not_approved")
)

// RepoKeyBinding is one key a repository owns. A stopped binding is retained so the
// repository, recipients and past usage stay attributable after the key left CPA.
type RepoKeyBinding struct {
	ID             string `json:"id"`
	InstanceID     string `json:"instance_id"`
	RepoID         string `json:"repo_id"`
	RepoHasRemote  bool   `json:"repo_has_remote"`
	KeyFingerprint string `json:"key_fingerprint"`
	KeyMask        string `json:"key_mask"`
	Status         string `json:"status"`
	CreatedAtMS    int64  `json:"created_at_ms"`
	CreatedBy      string `json:"created_by"`
	StoppedAtMS    *int64 `json:"stopped_at_ms,omitempty"`
	StoppedBy      string `json:"stopped_by"`
	StopCategory   string `json:"stop_category"`
	StopReason     string `json:"stop_reason"`
}

// RepoKeyCaller is one agent on one device and the public key registered for it.
type RepoKeyCaller struct {
	ID                   string `json:"id"`
	InstanceID           string `json:"instance_id"`
	AgentName            string `json:"agent_name"`
	DeviceName           string `json:"device_name"`
	PublicKey            string `json:"public_key"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	KeyAlgorithm         string `json:"key_algorithm"`
	CreatedAtMS          int64  `json:"created_at_ms"`
	UpdatedAtMS          int64  `json:"updated_at_ms"`
}

// RepoKeyApplication is one request for a key. Status covers the human decision; the
// delivery is a separate row so the ciphertext stays retrievable afterwards.
type RepoKeyApplication struct {
	ID                     string `json:"id"`
	InstanceID             string `json:"instance_id"`
	RepoID                 string `json:"repo_id"`
	RepoHasRemote          bool   `json:"repo_has_remote"`
	AgentName              string `json:"agent_name"`
	DeviceName             string `json:"device_name"`
	Purpose                string `json:"purpose"`
	Kind                   string `json:"kind"`
	Status                 string `json:"status"`
	PublicKey              string `json:"public_key"`
	PublicKeyFingerprint   string `json:"public_key_fingerprint"`
	KeyAlgorithm           string `json:"key_algorithm"`
	Source                 string `json:"source"`
	BindingID              string `json:"binding_id"`
	KeyFingerprint         string `json:"key_fingerprint"`
	KeyMask                string `json:"key_mask"`
	DecidedBy              string `json:"decided_by"`
	DecidedAtMS            *int64 `json:"decided_at_ms,omitempty"`
	RejectReason           string `json:"reject_reason"`
	AuthorizationTokenHash string `json:"-"`
	TokenExpiresAtMS       int64  `json:"token_expires_at_ms"`
	FirstDeliveredAtMS     *int64 `json:"first_delivered_at_ms,omitempty"`
	CreatedAtMS            int64  `json:"created_at_ms"`
	UpdatedAtMS            int64  `json:"updated_at_ms"`
}

// RepoKeyDelivery is one ciphertext hand-out or console plaintext reveal.
type RepoKeyDelivery struct {
	ID                   string `json:"id"`
	InstanceID           string `json:"instance_id"`
	ApplicationID        string `json:"application_id"`
	BindingID            string `json:"binding_id"`
	KeyFingerprint       string `json:"key_fingerprint"`
	KeyMask              string `json:"key_mask"`
	AgentName            string `json:"agent_name"`
	DeviceName           string `json:"device_name"`
	PublicKeyFingerprint string `json:"public_key_fingerprint"`
	Kind                 string `json:"kind"`
	Operator             string `json:"operator"`
	DeliveredAtMS        int64  `json:"delivered_at_ms"`
}

// RepoKeyStop is one stop action on a binding, kept across a later re-enable.
type RepoKeyStop struct {
	ID             string `json:"id"`
	InstanceID     string `json:"instance_id"`
	BindingID      string `json:"binding_id"`
	Category       string `json:"category"`
	Reason         string `json:"reason"`
	StoppedBy      string `json:"stopped_by"`
	StoppedAtMS    int64  `json:"stopped_at_ms"`
	ReenabledBy    string `json:"reenabled_by"`
	ReenabledAtMS  *int64 `json:"reenabled_at_ms,omitempty"`
	ReenableReason string `json:"reenable_reason"`
}

const repoKeyBindingColumns = `id, instance_id, repo_id, repo_has_remote, key_fingerprint, key_mask,
	status, created_at_ms, created_by, stopped_at_ms, stopped_by, stop_category, stop_reason`

const repoKeyApplicationColumns = `id, instance_id, repo_id, repo_has_remote, agent_name, device_name,
	purpose, kind, status, public_key, public_key_fingerprint, key_algorithm, source, binding_id,
	key_fingerprint, key_mask, decided_by, decided_at_ms, reject_reason, authorization_token_hash,
	token_expires_at_ms, first_delivered_at_ms, created_at_ms, updated_at_ms`

func scanRepoKeyBinding(scanner interface{ Scan(...any) error }) (RepoKeyBinding, error) {
	var binding RepoKeyBinding
	var hasRemote int
	var stoppedAt sql.NullInt64
	err := scanner.Scan(
		&binding.ID, &binding.InstanceID, &binding.RepoID, &hasRemote, &binding.KeyFingerprint,
		&binding.KeyMask, &binding.Status, &binding.CreatedAtMS, &binding.CreatedBy,
		&stoppedAt, &binding.StoppedBy, &binding.StopCategory, &binding.StopReason,
	)
	binding.RepoHasRemote = hasRemote == 1
	if stoppedAt.Valid {
		value := stoppedAt.Int64
		binding.StoppedAtMS = &value
	}
	return binding, err
}

func scanRepoKeyApplication(scanner interface{ Scan(...any) error }) (RepoKeyApplication, error) {
	var application RepoKeyApplication
	var hasRemote int
	var decidedAt, deliveredAt sql.NullInt64
	err := scanner.Scan(
		&application.ID, &application.InstanceID, &application.RepoID, &hasRemote,
		&application.AgentName, &application.DeviceName, &application.Purpose,
		&application.Kind, &application.Status, &application.PublicKey,
		&application.PublicKeyFingerprint, &application.KeyAlgorithm, &application.Source,
		&application.BindingID, &application.KeyFingerprint, &application.KeyMask,
		&application.DecidedBy, &decidedAt, &application.RejectReason,
		&application.AuthorizationTokenHash, &application.TokenExpiresAtMS, &deliveredAt,
		&application.CreatedAtMS, &application.UpdatedAtMS,
	)
	application.RepoHasRemote = hasRemote == 1
	if decidedAt.Valid {
		value := decidedAt.Int64
		application.DecidedAtMS = &value
	}
	if deliveredAt.Valid {
		value := deliveredAt.Int64
		application.FirstDeliveredAtMS = &value
	}
	return application, err
}

// GetRepoKeyBindingByFingerprint finds the binding for a key, active or stopped.
func (r *Repository) GetRepoKeyBindingByFingerprint(ctx context.Context, instanceID, fingerprint string) (RepoKeyBinding, error) {
	row := r.SQL().QueryRowContext(ctx, `
		SELECT `+repoKeyBindingColumns+` FROM repo_key_bindings
		WHERE instance_id = ? AND key_fingerprint = ?`, strings.TrimSpace(instanceID), strings.TrimSpace(fingerprint))
	binding, err := scanRepoKeyBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyBinding{}, ErrRepoKeyNotFound
	}
	if err != nil {
		return RepoKeyBinding{}, fmt.Errorf("read repo key binding: %w", err)
	}
	return binding, nil
}

// GetRepoKeyBindingByID finds one binding.
func (r *Repository) GetRepoKeyBindingByID(ctx context.Context, id string) (RepoKeyBinding, error) {
	row := r.SQL().QueryRowContext(ctx, `
		SELECT `+repoKeyBindingColumns+` FROM repo_key_bindings WHERE id = ?`, strings.TrimSpace(id))
	binding, err := scanRepoKeyBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyBinding{}, ErrRepoKeyNotFound
	}
	if err != nil {
		return RepoKeyBinding{}, fmt.Errorf("read repo key binding: %w", err)
	}
	return binding, nil
}

// GetActiveRepoKeyBinding returns the one active key a repository owns.
func (r *Repository) GetActiveRepoKeyBinding(ctx context.Context, instanceID, repoID string) (RepoKeyBinding, error) {
	row := r.SQL().QueryRowContext(ctx, `
		SELECT `+repoKeyBindingColumns+` FROM repo_key_bindings
		WHERE instance_id = ? AND repo_id = ? AND status = 'active'`,
		strings.TrimSpace(instanceID), strings.TrimSpace(repoID))
	binding, err := scanRepoKeyBinding(row)
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyBinding{}, ErrRepoKeyNotFound
	}
	if err != nil {
		return RepoKeyBinding{}, fmt.Errorf("read active repo key: %w", err)
	}
	return binding, nil
}

// RepoKeyRepoStatus is the answer to "does this repository have a usable key?".
type RepoKeyRepoStatus struct {
	State   string           `json:"state"` // active / none / stopped
	Binding *RepoKeyBinding  `json:"binding,omitempty"`
	Stops   []RepoKeyBinding `json:"stopped,omitempty"`
}

// GetRepoKeyRepoStatus resolves the three outcomes AC-3 names. Stopped bindings are
// returned alongside an active one, so the console can show that this repository had
// a key replaced rather than implying it never had one.
func (r *Repository) GetRepoKeyRepoStatus(ctx context.Context, instanceID, repoID string) (RepoKeyRepoStatus, error) {
	active, activeErr := r.GetActiveRepoKeyBinding(ctx, instanceID, repoID)
	if activeErr != nil && !errors.Is(activeErr, ErrRepoKeyNotFound) {
		return RepoKeyRepoStatus{}, activeErr
	}
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT `+repoKeyBindingColumns+` FROM repo_key_bindings
		WHERE instance_id = ? AND repo_id = ? AND status = 'stopped'
		ORDER BY stopped_at_ms DESC`, strings.TrimSpace(instanceID), strings.TrimSpace(repoID))
	if err != nil {
		return RepoKeyRepoStatus{}, fmt.Errorf("read stopped repo keys: %w", err)
	}
	defer rows.Close()
	stopped := []RepoKeyBinding{}
	for rows.Next() {
		binding, scanErr := scanRepoKeyBinding(rows)
		if scanErr != nil {
			return RepoKeyRepoStatus{}, fmt.Errorf("scan stopped repo key: %w", scanErr)
		}
		stopped = append(stopped, binding)
	}
	if err := rows.Err(); err != nil {
		return RepoKeyRepoStatus{}, err
	}
	if activeErr == nil {
		return RepoKeyRepoStatus{State: "active", Binding: &active, Stops: stopped}, nil
	}
	if len(stopped) == 0 {
		return RepoKeyRepoStatus{State: "none"}, nil
	}
	return RepoKeyRepoStatus{State: "stopped", Binding: &stopped[0], Stops: stopped}, nil
}

// ListRepoKeyBindings returns every binding for an instance, newest first.
func (r *Repository) ListRepoKeyBindings(ctx context.Context, instanceID string) ([]RepoKeyBinding, error) {
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT `+repoKeyBindingColumns+` FROM repo_key_bindings
		WHERE instance_id = ? ORDER BY created_at_ms DESC`, strings.TrimSpace(instanceID))
	if err != nil {
		return nil, fmt.Errorf("list repo key bindings: %w", err)
	}
	defer rows.Close()
	bindings := []RepoKeyBinding{}
	for rows.Next() {
		binding, scanErr := scanRepoKeyBinding(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan repo key binding: %w", scanErr)
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

// CreateRepoKeyBinding records the newly issued key's ownership. A repository may
// hold at most one active key; the partial unique index turns a second one into a
// refusal instead of a silent duplicate.
func (r *Repository) CreateRepoKeyBinding(ctx context.Context, binding RepoKeyBinding, nowMS int64, audit AuditEvent) (RepoKeyBinding, error) {
	if strings.TrimSpace(binding.RepoID) == "" || strings.TrimSpace(binding.KeyFingerprint) == "" {
		return RepoKeyBinding{}, ErrRepoKeyInvalid
	}
	binding.ID = uuid.NewString()
	binding.Status = "active"
	binding.CreatedAtMS = nowMS
	binding.StoppedAtMS = nil
	binding.StoppedBy = ""
	binding.StopCategory = ""
	binding.StopReason = ""

	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return RepoKeyBinding{}, fmt.Errorf("begin create repo key binding: %w", err)
	}
	defer tx.Rollback()
	if err = writeRepoKeyAuditTx(ctx, tx, audit, nowMS, bindingKeyTarget(binding.RepoID, binding.KeyFingerprint), map[string]any{
		"repo": binding.RepoID, "repo_has_remote": binding.RepoHasRemote, "key_mask": binding.KeyMask,
	}); err != nil {
		return RepoKeyBinding{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO repo_key_bindings (id, instance_id, repo_id, repo_has_remote, key_fingerprint, key_mask,
			status, created_at_ms, created_by)
		VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?)`,
		binding.ID, binding.InstanceID, binding.RepoID, boolToInt(binding.RepoHasRemote),
		binding.KeyFingerprint, binding.KeyMask, nowMS, binding.CreatedBy); err != nil {
		if isUniqueViolation(err) {
			return RepoKeyBinding{}, ErrRepoKeyConflict
		}
		return RepoKeyBinding{}, fmt.Errorf("create repo key binding: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return RepoKeyBinding{}, fmt.Errorf("commit create repo key binding: %w", err)
	}
	return binding, nil
}

// StopRepoKeyBinding moves an active key to stopped and records the action. The
// binding row is never deleted: its repository, recipients and usage are what the
// console still answers with (AC-10).
func (r *Repository) StopRepoKeyBinding(ctx context.Context, id, stoppedBy, category, reason string, nowMS int64, audit AuditEvent) (RepoKeyBinding, error) {
	bindingID := strings.TrimSpace(id)
	if !validRepoKeyStopCategory(category) || strings.TrimSpace(reason) == "" {
		return RepoKeyBinding{}, ErrRepoKeyInvalid
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return RepoKeyBinding{}, fmt.Errorf("begin stop repo key: %w", err)
	}
	defer tx.Rollback()
	binding, err := scanRepoKeyBinding(tx.QueryRowContext(ctx, `
		SELECT `+repoKeyBindingColumns+` FROM repo_key_bindings WHERE id = ?`, bindingID))
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyBinding{}, ErrRepoKeyNotFound
	}
	if err != nil {
		return RepoKeyBinding{}, fmt.Errorf("read repo key binding: %w", err)
	}
	if binding.Status != "active" {
		return RepoKeyBinding{}, ErrRepoKeyConflict
	}
	if err = writeRepoKeyAuditTx(ctx, tx, audit, nowMS, bindingKeyTarget(binding.RepoID, binding.KeyFingerprint), map[string]any{
		"repo": binding.RepoID, "key_mask": binding.KeyMask, "category": category,
	}); err != nil {
		return RepoKeyBinding{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE repo_key_bindings SET status = 'stopped', stopped_at_ms = ?, stopped_by = ?,
			stop_category = ?, stop_reason = ? WHERE id = ?`,
		nowMS, stoppedBy, category, reason, bindingID); err != nil {
		return RepoKeyBinding{}, fmt.Errorf("stop repo key: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO repo_key_stops (id, instance_id, binding_id, category, reason, stopped_by, stopped_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), binding.InstanceID, bindingID, category, reason, stoppedBy, nowMS); err != nil {
		return RepoKeyBinding{}, fmt.Errorf("record repo key stop: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return RepoKeyBinding{}, fmt.Errorf("commit stop repo key: %w", err)
	}
	binding.Status = "stopped"
	binding.StoppedAtMS = &nowMS
	binding.StoppedBy = stoppedBy
	binding.StopCategory = category
	binding.StopReason = reason
	return binding, nil
}

// ReenableRepoKeyBinding puts a stopped key back to active. A leaked key is refused:
// its plaintext is assumed to be outside OMC's control (AC-31).
func (r *Repository) ReenableRepoKeyBinding(ctx context.Context, id, reenabledBy, reason string, nowMS int64, audit AuditEvent) (RepoKeyBinding, error) {
	bindingID := strings.TrimSpace(id)
	if strings.TrimSpace(reason) == "" {
		return RepoKeyBinding{}, ErrRepoKeyInvalid
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return RepoKeyBinding{}, fmt.Errorf("begin reenable repo key: %w", err)
	}
	defer tx.Rollback()
	binding, err := scanRepoKeyBinding(tx.QueryRowContext(ctx, `
		SELECT `+repoKeyBindingColumns+` FROM repo_key_bindings WHERE id = ?`, bindingID))
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyBinding{}, ErrRepoKeyNotFound
	}
	if err != nil {
		return RepoKeyBinding{}, fmt.Errorf("read repo key binding: %w", err)
	}
	if binding.Status != "stopped" || binding.StopCategory == "leaked" {
		return RepoKeyBinding{}, ErrRepoKeyBindingNotReenable
	}
	if err = writeRepoKeyAuditTx(ctx, tx, audit, nowMS, bindingKeyTarget(binding.RepoID, binding.KeyFingerprint), map[string]any{
		"repo": binding.RepoID, "key_mask": binding.KeyMask, "previous_category": binding.StopCategory,
	}); err != nil {
		return RepoKeyBinding{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE repo_key_bindings SET status = 'active', stopped_at_ms = NULL, stopped_by = '',
			stop_category = '', stop_reason = '' WHERE id = ?`, bindingID); err != nil {
		return RepoKeyBinding{}, fmt.Errorf("reenable repo key: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE repo_key_stops SET reenabled_by = ?, reenabled_at_ms = ?, reenable_reason = ?
		WHERE id = (SELECT id FROM repo_key_stops WHERE binding_id = ? ORDER BY stopped_at_ms DESC LIMIT 1)`,
		reenabledBy, nowMS, reason, bindingID); err != nil {
		return RepoKeyBinding{}, fmt.Errorf("record repo key reenable: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return RepoKeyBinding{}, fmt.Errorf("commit reenable repo key: %w", err)
	}
	binding.Status = "active"
	binding.StoppedAtMS = nil
	binding.StoppedBy = ""
	binding.StopCategory = ""
	binding.StopReason = ""
	return binding, nil
}

// GetRepoKeyCaller finds the registered public key for one agent on one device.
func (r *Repository) GetRepoKeyCaller(ctx context.Context, instanceID, agentName, deviceName string) (RepoKeyCaller, error) {
	row := r.SQL().QueryRowContext(ctx, `
		SELECT id, instance_id, agent_name, device_name, public_key, public_key_fingerprint, key_algorithm,
		       created_at_ms, updated_at_ms
		FROM repo_key_callers WHERE instance_id = ? AND agent_name = ? AND device_name = ?`,
		strings.TrimSpace(instanceID), strings.TrimSpace(agentName), strings.TrimSpace(deviceName))
	var caller RepoKeyCaller
	err := row.Scan(&caller.ID, &caller.InstanceID, &caller.AgentName, &caller.DeviceName, &caller.PublicKey,
		&caller.PublicKeyFingerprint, &caller.KeyAlgorithm, &caller.CreatedAtMS, &caller.UpdatedAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyCaller{}, ErrRepoKeyNotFound
	}
	if err != nil {
		return RepoKeyCaller{}, fmt.Errorf("read repo key caller: %w", err)
	}
	return caller, nil
}

// FindRepoKeyCallerByFingerprint resolves a public-key fingerprint, optionally
// narrowed to an agent name, for a console proxy application that only has the
// fingerprint (PRD 4.7).
func (r *Repository) FindRepoKeyCallerByFingerprint(ctx context.Context, instanceID, fingerprint, agentName string) (RepoKeyCaller, error) {
	query := `
		SELECT id, instance_id, agent_name, device_name, public_key, public_key_fingerprint, key_algorithm,
		       created_at_ms, updated_at_ms
		FROM repo_key_callers WHERE instance_id = ? AND public_key_fingerprint = ?`
	args := []any{strings.TrimSpace(instanceID), strings.TrimSpace(fingerprint)}
	if strings.TrimSpace(agentName) != "" {
		query += ` AND agent_name = ?`
		args = append(args, strings.TrimSpace(agentName))
	}
	query += ` ORDER BY updated_at_ms DESC LIMIT 1`
	var caller RepoKeyCaller
	err := r.SQL().QueryRowContext(ctx, query, args...).Scan(&caller.ID, &caller.InstanceID, &caller.AgentName,
		&caller.DeviceName, &caller.PublicKey, &caller.PublicKeyFingerprint, &caller.KeyAlgorithm,
		&caller.CreatedAtMS, &caller.UpdatedAtMS)
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyCaller{}, ErrRepoKeyNotFound
	}
	if err != nil {
		return RepoKeyCaller{}, fmt.Errorf("read repo key caller: %w", err)
	}
	return caller, nil
}

// UpsertRepoKeyCaller registers or replaces the public key of one agent+device.
func (r *Repository) UpsertRepoKeyCaller(ctx context.Context, caller RepoKeyCaller, nowMS int64) (RepoKeyCaller, error) {
	if strings.TrimSpace(caller.AgentName) == "" || strings.TrimSpace(caller.DeviceName) == "" ||
		strings.TrimSpace(caller.PublicKey) == "" || strings.TrimSpace(caller.PublicKeyFingerprint) == "" {
		return RepoKeyCaller{}, ErrRepoKeyInvalid
	}
	existing, err := r.GetRepoKeyCaller(ctx, caller.InstanceID, caller.AgentName, caller.DeviceName)
	if err != nil && !errors.Is(err, ErrRepoKeyNotFound) {
		return RepoKeyCaller{}, err
	}
	if errors.Is(err, ErrRepoKeyNotFound) {
		caller.ID = uuid.NewString()
		caller.CreatedAtMS = nowMS
	} else {
		caller.ID = existing.ID
		caller.CreatedAtMS = existing.CreatedAtMS
	}
	caller.UpdatedAtMS = nowMS
	_, err = r.SQL().ExecContext(ctx, `
		INSERT INTO repo_key_callers (id, instance_id, agent_name, device_name, public_key,
			public_key_fingerprint, key_algorithm, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(instance_id, agent_name, device_name) DO UPDATE SET
			public_key = excluded.public_key,
			public_key_fingerprint = excluded.public_key_fingerprint,
			key_algorithm = excluded.key_algorithm,
			updated_at_ms = excluded.updated_at_ms`,
		caller.ID, caller.InstanceID, caller.AgentName, caller.DeviceName, caller.PublicKey,
		caller.PublicKeyFingerprint, caller.KeyAlgorithm, caller.CreatedAtMS, nowMS)
	if err != nil {
		return RepoKeyCaller{}, fmt.Errorf("upsert repo key caller: %w", err)
	}
	return caller, nil
}

// IsRepoKeyRecipient reports whether a caller was already accepted for a repository.
func (r *Repository) IsRepoKeyRecipient(ctx context.Context, instanceID, repoID, agentName, deviceName string) (bool, error) {
	var count int
	err := r.SQL().QueryRowContext(ctx, `
		SELECT COUNT(1) FROM repo_key_recipients
		WHERE instance_id = ? AND repo_id = ? AND agent_name = ? AND device_name = ?`,
		strings.TrimSpace(instanceID), strings.TrimSpace(repoID), strings.TrimSpace(agentName),
		strings.TrimSpace(deviceName)).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("read repo key recipient: %w", err)
	}
	return count > 0, nil
}

// AddRepoKeyRecipient records the callers a repository will auto-approve next time.
func (r *Repository) AddRepoKeyRecipient(ctx context.Context, instanceID, repoID, agentName, deviceName, approvedBy string, nowMS int64) error {
	_, err := r.SQL().ExecContext(ctx, `
		INSERT INTO repo_key_recipients (instance_id, repo_id, agent_name, device_name, approved_at_ms, approved_by)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(instance_id, repo_id, agent_name, device_name) DO NOTHING`,
		strings.TrimSpace(instanceID), strings.TrimSpace(repoID), strings.TrimSpace(agentName),
		strings.TrimSpace(deviceName), nowMS, approvedBy)
	if err != nil {
		return fmt.Errorf("add repo key recipient: %w", err)
	}
	return nil
}

// CreateRepoKeyApplication stores one application. When an identical application is
// already waiting it returns that row instead of inserting a second one (AC-28).
func (r *Repository) CreateRepoKeyApplication(ctx context.Context, application RepoKeyApplication, nowMS int64, audit AuditEvent) (RepoKeyApplication, bool, error) {
	if strings.TrimSpace(application.RepoID) == "" || strings.TrimSpace(application.AgentName) == "" ||
		strings.TrimSpace(application.Purpose) == "" || strings.TrimSpace(application.PublicKey) == "" {
		return RepoKeyApplication{}, false, ErrRepoKeyInvalid
	}
	application.ID = uuid.NewString()
	application.Status = "pending"
	application.CreatedAtMS = nowMS
	application.UpdatedAtMS = nowMS
	application.DecidedAtMS = nil
	application.FirstDeliveredAtMS = nil

	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return RepoKeyApplication{}, false, fmt.Errorf("begin create repo key application: %w", err)
	}
	defer tx.Rollback()

	existing, err := scanRepoKeyApplication(tx.QueryRowContext(ctx, `
		SELECT `+repoKeyApplicationColumns+` FROM repo_key_applications
		WHERE instance_id = ? AND repo_id = ? AND agent_name = ? AND status = 'pending'`,
		application.InstanceID, application.RepoID, application.AgentName))
	if err == nil {
		// A pending application is reused, not duplicated (AC-28). If the caller's
		// public key or stated purpose changed since it was filed, the stored snapshot
		// is refreshed: delivery must go to the key the operator is shown, and a
		// changed key still needs the human decision it is waiting for (AC-34).
		if existing.PublicKeyFingerprint == application.PublicKeyFingerprint && existing.Purpose == application.Purpose {
			return existing, false, nil
		}
		if err = writeRepoKeyAuditTx(ctx, tx, audit, nowMS, bindingKeyTarget(application.RepoID, application.PublicKeyFingerprint), map[string]any{
			"repo": application.RepoID, "agent": application.AgentName, "device": application.DeviceName,
			"kind": application.Kind, "source": application.Source,
			"public_key_fingerprint":          application.PublicKeyFingerprint,
			"previous_public_key_fingerprint": existing.PublicKeyFingerprint,
			"application_updated":             true,
		}); err != nil {
			return RepoKeyApplication{}, false, err
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE repo_key_applications SET purpose = ?, public_key = ?, public_key_fingerprint = ?,
				key_algorithm = ?, updated_at_ms = ? WHERE id = ?`,
			application.Purpose, application.PublicKey, application.PublicKeyFingerprint,
			application.KeyAlgorithm, nowMS, existing.ID); err != nil {
			return RepoKeyApplication{}, false, fmt.Errorf("refresh pending repo key application: %w", err)
		}
		if err = tx.Commit(); err != nil {
			return RepoKeyApplication{}, false, fmt.Errorf("commit refresh repo key application: %w", err)
		}
		existing.Purpose = application.Purpose
		existing.PublicKey = application.PublicKey
		existing.PublicKeyFingerprint = application.PublicKeyFingerprint
		existing.KeyAlgorithm = application.KeyAlgorithm
		existing.UpdatedAtMS = nowMS
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RepoKeyApplication{}, false, fmt.Errorf("read pending repo key application: %w", err)
	}

	if err = writeRepoKeyAuditTx(ctx, tx, audit, nowMS, bindingKeyTarget(application.RepoID, application.PublicKeyFingerprint), map[string]any{
		"repo": application.RepoID, "agent": application.AgentName, "device": application.DeviceName,
		"kind": application.Kind, "source": application.Source,
		"public_key_fingerprint": application.PublicKeyFingerprint,
	}); err != nil {
		return RepoKeyApplication{}, false, err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO repo_key_applications (id, instance_id, repo_id, repo_has_remote, agent_name, device_name,
			purpose, kind, status, public_key, public_key_fingerprint, key_algorithm, source,
			authorization_token_hash, token_expires_at_ms, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?, ?, ?, ?, ?, ?)`,
		application.ID, application.InstanceID, application.RepoID, boolToInt(application.RepoHasRemote),
		application.AgentName, application.DeviceName, application.Purpose, application.Kind,
		application.PublicKey, application.PublicKeyFingerprint, application.KeyAlgorithm, application.Source,
		application.AuthorizationTokenHash, application.TokenExpiresAtMS, nowMS, nowMS); err != nil {
		if isUniqueViolation(err) {
			return RepoKeyApplication{}, false, ErrRepoKeyApplicationPending
		}
		return RepoKeyApplication{}, false, fmt.Errorf("create repo key application: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return RepoKeyApplication{}, false, fmt.Errorf("commit create repo key application: %w", err)
	}
	return application, true, nil
}

// GetRepoKeyApplication reads one application by id.
func (r *Repository) GetRepoKeyApplication(ctx context.Context, id string) (RepoKeyApplication, error) {
	application, err := scanRepoKeyApplication(r.SQL().QueryRowContext(ctx, `
		SELECT `+repoKeyApplicationColumns+` FROM repo_key_applications WHERE id = ?`, strings.TrimSpace(id)))
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyApplication{}, ErrRepoKeyApplicationNotFound
	}
	if err != nil {
		return RepoKeyApplication{}, fmt.Errorf("read repo key application: %w", err)
	}
	return application, nil
}

// GetRepoKeyApplicationByToken resolves an authorization link. The token itself is
// never stored; only its fingerprint is compared.
func (r *Repository) GetRepoKeyApplicationByToken(ctx context.Context, token string) (RepoKeyApplication, error) {
	application, err := scanRepoKeyApplication(r.SQL().QueryRowContext(ctx, `
		SELECT `+repoKeyApplicationColumns+` FROM repo_key_applications WHERE authorization_token_hash = ?`,
		repokey.TokenFingerprint(token)))
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyApplication{}, ErrRepoKeyApplicationNotFound
	}
	if err != nil {
		return RepoKeyApplication{}, fmt.Errorf("read repo key application: %w", err)
	}
	return application, nil
}

// ApplicationDecision is what an approval records. The caller has already issued or
// resolved the key; the repository stores the decision atomically with the binding
// fields, so a half-decided application cannot exist.
type ApplicationDecision struct {
	ApplicationID  string
	Approve        bool
	DecidedBy      string
	RejectReason   string
	Kind           string
	BindingID      string
	KeyFingerprint string
	KeyMask        string
}

// DecideRepoKeyApplication applies the operator's decision exactly once.
func (r *Repository) DecideRepoKeyApplication(ctx context.Context, decision ApplicationDecision, nowMS int64, audit AuditEvent) (RepoKeyApplication, error) {
	applicationID := strings.TrimSpace(decision.ApplicationID)
	if applicationID == "" {
		return RepoKeyApplication{}, ErrRepoKeyInvalid
	}
	if !decision.Approve && strings.TrimSpace(decision.RejectReason) == "" {
		return RepoKeyApplication{}, ErrRepoKeyInvalid
	}
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return RepoKeyApplication{}, fmt.Errorf("begin decide repo key application: %w", err)
	}
	defer tx.Rollback()

	application, err := scanRepoKeyApplication(tx.QueryRowContext(ctx, `
		SELECT `+repoKeyApplicationColumns+` FROM repo_key_applications WHERE id = ?`, applicationID))
	if errors.Is(err, sql.ErrNoRows) {
		return RepoKeyApplication{}, ErrRepoKeyApplicationNotFound
	}
	if err != nil {
		return RepoKeyApplication{}, fmt.Errorf("read repo key application: %w", err)
	}
	if application.Status != "pending" {
		// The other entry point already decided it. Returning the stored row is what
		// keeps "one action, two places" from becoming a second approval (AC-8).
		return application, nil
	}
	target := bindingKeyTarget(application.RepoID, application.PublicKeyFingerprint)
	details := map[string]any{"repo": application.RepoID, "agent": application.AgentName, "kind": application.Kind}
	if !decision.Approve {
		details["reject_reason"] = decision.RejectReason
	} else {
		details["key_mask"] = decision.KeyMask
		details["binding_id"] = decision.BindingID
	}
	if err = writeRepoKeyAuditTx(ctx, tx, audit, nowMS, target, details); err != nil {
		return RepoKeyApplication{}, err
	}
	if decision.Approve {
		kind := decision.Kind
		if kind == "" {
			kind = application.Kind
		}
		if _, err = tx.ExecContext(ctx, `
			UPDATE repo_key_applications SET status = 'approved', kind = ?, binding_id = ?,
				key_fingerprint = ?, key_mask = ?, decided_by = ?, decided_at_ms = ?, updated_at_ms = ?
			WHERE id = ?`,
			kind, decision.BindingID, decision.KeyFingerprint, decision.KeyMask,
			decision.DecidedBy, nowMS, nowMS, applicationID); err != nil {
			return RepoKeyApplication{}, fmt.Errorf("approve repo key application: %w", err)
		}
	} else {
		if _, err = tx.ExecContext(ctx, `
			UPDATE repo_key_applications SET status = 'rejected', decided_by = ?, decided_at_ms = ?,
				reject_reason = ?, updated_at_ms = ? WHERE id = ?`,
			decision.DecidedBy, nowMS, decision.RejectReason, nowMS, applicationID); err != nil {
			return RepoKeyApplication{}, fmt.Errorf("reject repo key application: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return RepoKeyApplication{}, fmt.Errorf("commit decide repo key application: %w", err)
	}
	return r.GetRepoKeyApplication(ctx, applicationID)
}

// ListRepoKeyApplications returns applications in one status, newest first. An
// empty status lists every application.
func (r *Repository) ListRepoKeyApplications(ctx context.Context, instanceID, status string, limit int) ([]RepoKeyApplication, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT ` + repoKeyApplicationColumns + ` FROM repo_key_applications WHERE instance_id = ?`
	args := []any{strings.TrimSpace(instanceID)}
	if strings.TrimSpace(status) != "" {
		query += ` AND status = ?`
		args = append(args, strings.TrimSpace(status))
	}
	query += ` ORDER BY created_at_ms DESC LIMIT ?`
	args = append(args, limit)
	rows, err := r.SQL().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list repo key applications: %w", err)
	}
	defer rows.Close()
	applications := []RepoKeyApplication{}
	for rows.Next() {
		application, scanErr := scanRepoKeyApplication(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan repo key application: %w", scanErr)
		}
		applications = append(applications, application)
	}
	return applications, rows.Err()
}

// ListRepoKeyDeliveries returns every hand-out of one key, newest first.
func (r *Repository) ListRepoKeyDeliveries(ctx context.Context, instanceID, bindingID string) ([]RepoKeyDelivery, error) {
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT id, instance_id, application_id, binding_id, key_fingerprint, key_mask, agent_name,
		       device_name, public_key_fingerprint, kind, operator, delivered_at_ms
		FROM repo_key_deliveries WHERE instance_id = ? AND binding_id = ?
		ORDER BY delivered_at_ms DESC`, strings.TrimSpace(instanceID), strings.TrimSpace(bindingID))
	if err != nil {
		return nil, fmt.Errorf("list repo key deliveries: %w", err)
	}
	defer rows.Close()
	deliveries := []RepoKeyDelivery{}
	for rows.Next() {
		var delivery RepoKeyDelivery
		if err := rows.Scan(&delivery.ID, &delivery.InstanceID, &delivery.ApplicationID, &delivery.BindingID,
			&delivery.KeyFingerprint, &delivery.KeyMask, &delivery.AgentName, &delivery.DeviceName,
			&delivery.PublicKeyFingerprint, &delivery.Kind, &delivery.Operator, &delivery.DeliveredAtMS); err != nil {
			return nil, fmt.Errorf("scan repo key delivery: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

// RecordRepoKeyDelivery appends one hand-out and, on the first, stamps the
// application so it leaves the delivery queue (AC-33).
func (r *Repository) RecordRepoKeyDelivery(ctx context.Context, delivery RepoKeyDelivery, audit AuditEvent) (RepoKeyDelivery, error) {
	if strings.TrimSpace(delivery.ApplicationID) == "" || strings.TrimSpace(delivery.BindingID) == "" {
		return RepoKeyDelivery{}, ErrRepoKeyInvalid
	}
	delivery.ID = uuid.NewString()
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return RepoKeyDelivery{}, fmt.Errorf("begin record repo key delivery: %w", err)
	}
	defer tx.Rollback()
	if err = writeRepoKeyAuditTx(ctx, tx, audit, delivery.DeliveredAtMS, bindingKeyTarget("", delivery.KeyFingerprint), map[string]any{
		"agent": delivery.AgentName, "device": delivery.DeviceName, "operator": delivery.Operator,
		"kind": delivery.Kind, "key_mask": delivery.KeyMask,
		"public_key_fingerprint": delivery.PublicKeyFingerprint,
	}); err != nil {
		return RepoKeyDelivery{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO repo_key_deliveries (id, instance_id, application_id, binding_id, key_fingerprint, key_mask,
			agent_name, device_name, public_key_fingerprint, kind, operator, delivered_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		delivery.ID, delivery.InstanceID, delivery.ApplicationID, delivery.BindingID, delivery.KeyFingerprint,
		delivery.KeyMask, delivery.AgentName, delivery.DeviceName, delivery.PublicKeyFingerprint,
		delivery.Kind, delivery.Operator, delivery.DeliveredAtMS); err != nil {
		return RepoKeyDelivery{}, fmt.Errorf("record repo key delivery: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE repo_key_applications SET first_delivered_at_ms = COALESCE(first_delivered_at_ms, ?), updated_at_ms = ?
		WHERE id = ?`, delivery.DeliveredAtMS, delivery.DeliveredAtMS, delivery.ApplicationID); err != nil {
		return RepoKeyDelivery{}, fmt.Errorf("stamp repo key delivery: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return RepoKeyDelivery{}, fmt.Errorf("commit record repo key delivery: %w", err)
	}
	return delivery, nil
}

// ListRepoKeyStops returns the stop history of one binding.
func (r *Repository) ListRepoKeyStops(ctx context.Context, instanceID, bindingID string) ([]RepoKeyStop, error) {
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT id, instance_id, binding_id, category, reason, stopped_by, stopped_at_ms,
		       reenabled_by, reenabled_at_ms, reenable_reason
		FROM repo_key_stops WHERE instance_id = ? AND binding_id = ?
		ORDER BY stopped_at_ms DESC`, strings.TrimSpace(instanceID), strings.TrimSpace(bindingID))
	if err != nil {
		return nil, fmt.Errorf("list repo key stops: %w", err)
	}
	defer rows.Close()
	stops := []RepoKeyStop{}
	for rows.Next() {
		var stop RepoKeyStop
		var reenabledAt sql.NullInt64
		if err := rows.Scan(&stop.ID, &stop.InstanceID, &stop.BindingID, &stop.Category, &stop.Reason,
			&stop.StoppedBy, &stop.StoppedAtMS, &stop.ReenabledBy, &reenabledAt, &stop.ReenableReason); err != nil {
			return nil, fmt.Errorf("scan repo key stop: %w", err)
		}
		if reenabledAt.Valid {
			value := reenabledAt.Int64
			stop.ReenabledAtMS = &value
		}
		stops = append(stops, stop)
	}
	return stops, rows.Err()
}

// ListRepoKeyApplicationsByBinding returns the approval history of one key.
func (r *Repository) ListRepoKeyApplicationsByBinding(ctx context.Context, instanceID, bindingID string) ([]RepoKeyApplication, error) {
	rows, err := r.SQL().QueryContext(ctx, `
		SELECT `+repoKeyApplicationColumns+` FROM repo_key_applications
		WHERE instance_id = ? AND binding_id = ? ORDER BY created_at_ms DESC`,
		strings.TrimSpace(instanceID), strings.TrimSpace(bindingID))
	if err != nil {
		return nil, fmt.Errorf("list repo key applications: %w", err)
	}
	defer rows.Close()
	applications := []RepoKeyApplication{}
	for rows.Next() {
		application, scanErr := scanRepoKeyApplication(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan repo key application: %w", scanErr)
		}
		applications = append(applications, application)
	}
	return applications, rows.Err()
}

// CountRepoKeyApplications returns how many applications are in a status.
func (r *Repository) CountRepoKeyApplications(ctx context.Context, instanceID, status string) (int, error) {
	var count int
	err := r.SQL().QueryRowContext(ctx, `
		SELECT COUNT(1) FROM repo_key_applications WHERE instance_id = ? AND status = ?`,
		strings.TrimSpace(instanceID), strings.TrimSpace(status)).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count repo key applications: %w", err)
	}
	return count, nil
}

func writeRepoKeyAuditTx(ctx context.Context, tx *sql.Tx, audit AuditEvent, nowMS int64, targetID string, details map[string]any) error {
	audit.TargetType = "repo_api_key"
	audit.TargetID = targetID
	audit.Result = "success"
	audit.OccurredAtMS = nowMS
	audit.Details = mergeAuditDetails(audit.Details, details)
	if _, err := insertAuditEvent(ctx, tx, audit); err != nil {
		return fmt.Errorf("%w: %w", ErrRepoKeyAuditFailed, err)
	}
	return nil
}

// bindingKeyTarget is the audit target an operator can search: the repository and
// the key's display mask (never its value).
func bindingKeyTarget(repoID, keyRef string) string {
	repoID = strings.TrimSpace(repoID)
	keyRef = strings.TrimSpace(keyRef)
	if repoID == "" {
		return keyRef
	}
	if keyRef == "" {
		return repoID
	}
	return repoID + "|" + keyRef
}

func validRepoKeyStopCategory(category string) bool {
	switch category {
	case "leaked", "rotate", "misuse", "other":
		return true
	}
	return false
}
