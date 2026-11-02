package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Model groups: an operator's ordered list of model names kept for one purpose.
//
// The group is identified by an opaque id and named by the operator. The name is
// what a reader resolves - later the read-only agent surface asks for a group by
// name - so it is unique case-insensitively, and a rename is a rename rather than a
// second group. Members live in their own table because one model may sit in several
// groups with an independent position in each; nothing here joins a model to a
// provider, a credential or a quota reading, which is why a name that CPA no longer
// serves can stay in a group and be reported later rather than silently dropped.
//
// `is_preset` marks the five groups a deployment starts with. A preset group is
// ordinary in every other respect - it renames and deletes like any other - and the
// flag exists only so restore can tell which of the original five is missing, which
// a name cannot answer once somebody has renamed one.

const (
	// ModelGroupNameLimit is the ceiling a group name is held to. It is a rune count,
	// not a byte count: the five preset names are Chinese, and counting bytes would
	// give a Chinese name a third of the room an ASCII one gets.
	ModelGroupNameLimit = 40
	// ModelGroupModelLimit bounds one member name. It matches the model field the
	// announcement table already stores, so the same identifier is admissible in both.
	ModelGroupModelLimit = 256
)

var (
	ErrModelGroupNotFound     = errors.New("model_group_not_found")
	ErrModelGroupName         = errors.New("model_group_invalid_name")
	ErrModelGroupNameConflict = errors.New("model_group_name_conflict")
	ErrModelGroupMember       = errors.New("model_group_invalid_member")
	ErrModelGroupOrder        = errors.New("model_group_invalid_order")
	// ErrModelGroupAuditFailed means the trail could not be written, so the change was
	// rolled back with it. It is distinct from a rejected input: the request was valid
	// and the same one will succeed once the audit store answers.
	ErrModelGroupAuditFailed = errors.New("model_group_audit_failed")
)

// ModelGroupMember is one model in one group, at the position the operator gave it.
type ModelGroupMember struct {
	Model     string `json:"model"`
	Position  int    `json:"position"`
	AddedAtMS int64  `json:"added_at_ms"`
}

// ModelGroup is a group with its members in the operator's order.
type ModelGroup struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	IsPreset    bool               `json:"is_preset"`
	CreatedAtMS int64              `json:"created_at_ms"`
	UpdatedAtMS int64              `json:"updated_at_ms"`
	Members     []ModelGroupMember `json:"members"`
}

// Every write below takes the audit event it must record and writes it inside the same
// transaction, before the change itself. That order is the point: the trail is the first
// write, so a store that refuses it aborts the transaction and no group, name or member
// moves. Writing the change first and the record afterwards - which is what these
// functions did before - answers 500 while the change is already durable, and the client
// cannot tell that from a write that never happened.

// ModelGroupAddResult reports what one add-member request did, so a caller can say
// which models were already present instead of guessing why the count did not move.
type ModelGroupAddResult struct {
	Group   ModelGroup `json:"group"`
	Added   []string   `json:"added"`
	Skipped []string   `json:"skipped"`
}

// ModelGroupRestoreResult reports the restore, including which preset groups were
// already there so the console can say nothing was missing rather than "done".
type ModelGroupRestoreResult struct {
	Created []ModelGroup `json:"created"`
	Skipped []string     `json:"skipped"`
}

// presetModelGroups are the five groups a deployment starts with, in the order the
// console lists them. The ids are fixed because restore recognises a renamed preset
// by identity; a generated id would make a rename look like a deletion.
var presetModelGroups = []struct {
	ID   string
	Name string
}{
	{"preset-labor", "力工组"},
	{"preset-frontend", "前端组"},
	{"preset-orchestrator", "编排组"},
	{"preset-review", "审查组"},
	{"preset-followup", "跟进组"},
}

// presetModelGroupRank orders the presets as declared. Sorting them by name would list
// them in UTF-8 byte order, which for these Chinese names is an order nobody chose.
var presetModelGroupRank = func() map[string]int {
	ranks := make(map[string]int, len(presetModelGroups))
	for index, preset := range presetModelGroups {
		ranks[preset.ID] = index
	}
	return ranks
}()

// PresetModelGroupNames lists the five preset names, for the console's copy and for
// tests that assert restore brought exactly these back.
func PresetModelGroupNames() []string {
	names := make([]string, 0, len(presetModelGroups))
	for _, preset := range presetModelGroups {
		names = append(names, preset.Name)
	}
	return names
}

func validateModelGroupName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" || utf8.RuneCountInString(trimmed) > ModelGroupNameLimit ||
		!utf8.ValidString(trimmed) || strings.ContainsRune(trimmed, 0) {
		return "", ErrModelGroupName
	}
	return trimmed, nil
}

func validateModelGroupModel(model string) (string, error) {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" || len(trimmed) > ModelGroupModelLimit ||
		!utf8.ValidString(trimmed) || strings.ContainsRune(trimmed, 0) {
		return "", ErrModelGroupMember
	}
	return trimmed, nil
}

// isUniqueViolation reports whether a write failed because it would have duplicated a
// unique key. The name and the member position both carry one, and both need to be
// told apart from a real failure so the caller can answer 409 rather than 500.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint failed") || strings.Contains(message, "constraint failed: unique")
}

func (r *Repository) ListModelGroups(ctx context.Context) ([]ModelGroup, error) {
	groups, err := r.readModelGroups(ctx, "")
	if err != nil {
		return nil, err
	}
	return groups, nil
}

func (r *Repository) GetModelGroup(ctx context.Context, id string) (ModelGroup, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ModelGroup{}, ErrModelGroupNotFound
	}
	groups, err := r.readModelGroups(ctx, id)
	if err != nil {
		return ModelGroup{}, err
	}
	if len(groups) == 0 {
		return ModelGroup{}, ErrModelGroupNotFound
	}
	return groups[0], nil
}

// readModelGroups reads every group (or the one named by id) with its members in one
// read transaction, so a group never comes back with a member list from a different
// moment than its own timestamps.
func (r *Repository) readModelGroups(ctx context.Context, id string) ([]ModelGroup, error) {
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin model group read: %w", err)
	}
	defer tx.Rollback()

	groupQuery := `SELECT id, name, is_preset, created_at_ms, updated_at_ms FROM model_groups`
	args := []any{}
	if id != "" {
		groupQuery += ` WHERE id = ?`
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, groupQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("list model groups: %w", err)
	}
	groups := []ModelGroup{}
	for rows.Next() {
		var group ModelGroup
		var isPreset int
		if err = rows.Scan(&group.ID, &group.Name, &isPreset, &group.CreatedAtMS, &group.UpdatedAtMS); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan model group: %w", err)
		}
		group.IsPreset = isPreset == 1
		group.Members = []ModelGroupMember{}
		groups = append(groups, group)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("read model groups: %w", err)
	}
	if err = rows.Close(); err != nil {
		return nil, fmt.Errorf("close model group read: %w", err)
	}
	if len(groups) == 0 {
		return groups, nil
	}

	memberRows, err := tx.QueryContext(ctx, `
		SELECT group_id, model, position, added_at_ms
		FROM model_group_members ORDER BY group_id, position`)
	if err != nil {
		return nil, fmt.Errorf("list model group members: %w", err)
	}
	defer memberRows.Close()
	byID := make(map[string]*ModelGroup, len(groups))
	for index := range groups {
		byID[groups[index].ID] = &groups[index]
	}
	for memberRows.Next() {
		var groupID string
		var member ModelGroupMember
		if err = memberRows.Scan(&groupID, &member.Model, &member.Position, &member.AddedAtMS); err != nil {
			return nil, fmt.Errorf("scan model group member: %w", err)
		}
		if group, found := byID[groupID]; found {
			group.Members = append(group.Members, member)
		}
	}
	if err = memberRows.Err(); err != nil {
		return nil, fmt.Errorf("read model group members: %w", err)
	}
	if err = memberRows.Close(); err != nil {
		return nil, fmt.Errorf("close model group member read: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("finish model group read: %w", err)
	}

	// Preset groups first in their declared order, then the operator's own by name. The
	// console draws this order and nothing depends on it, but a stable order is what
	// keeps the page from reshuffling between two reads of unchanged data.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].IsPreset != groups[j].IsPreset {
			return groups[i].IsPreset
		}
		if groups[i].IsPreset {
			return presetModelGroupRank[groups[i].ID] < presetModelGroupRank[groups[j].ID]
		}
		return groups[i].Name < groups[j].Name
	})
	return groups, nil
}

// CreateModelGroup inserts a group and its audit row in one transaction.
func (r *Repository) CreateModelGroup(ctx context.Context, name string, nowMS int64, audit AuditEvent) (ModelGroup, error) {
	clean, err := validateModelGroupName(name)
	if err != nil {
		return ModelGroup{}, err
	}
	group := ModelGroup{ID: uuid.NewString(), Name: clean, CreatedAtMS: nowMS, UpdatedAtMS: nowMS, Members: []ModelGroupMember{}}

	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return ModelGroup{}, fmt.Errorf("begin create model group: %w", err)
	}
	defer tx.Rollback()

	if err = writeModelGroupAuditTx(ctx, tx, audit, nowMS, group.ID, map[string]any{"name": group.Name}); err != nil {
		return ModelGroup{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		INSERT INTO model_groups (id, name, is_preset, created_at_ms, updated_at_ms)
		VALUES (?, ?, 0, ?, ?)`, group.ID, group.Name, nowMS, nowMS); err != nil {
		if isUniqueViolation(err) {
			return ModelGroup{}, ErrModelGroupNameConflict
		}
		return ModelGroup{}, fmt.Errorf("create model group: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return ModelGroup{}, fmt.Errorf("commit create model group: %w", err)
	}
	return group, nil
}

// RenameModelGroup renames a group and records it in one transaction. The stored name is
// read inside that transaction, so the audit row names the name that was actually
// replaced rather than one a concurrent rename already moved on from.
func (r *Repository) RenameModelGroup(ctx context.Context, id, name string, nowMS int64, audit AuditEvent) (ModelGroup, error) {
	clean, err := validateModelGroupName(name)
	if err != nil {
		return ModelGroup{}, err
	}
	groupID := strings.TrimSpace(id)

	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return ModelGroup{}, fmt.Errorf("begin rename model group: %w", err)
	}
	defer tx.Rollback()

	var previous string
	err = tx.QueryRowContext(ctx, `SELECT name FROM model_groups WHERE id = ?`, groupID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return ModelGroup{}, ErrModelGroupNotFound
	}
	if err != nil {
		return ModelGroup{}, fmt.Errorf("read model group: %w", err)
	}
	if err = writeModelGroupAuditTx(ctx, tx, audit, nowMS, groupID, map[string]any{"from": previous, "to": clean}); err != nil {
		return ModelGroup{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		UPDATE model_groups SET name = ?, updated_at_ms = ? WHERE id = ?`, clean, nowMS, groupID); err != nil {
		if isUniqueViolation(err) {
			return ModelGroup{}, ErrModelGroupNameConflict
		}
		return ModelGroup{}, fmt.Errorf("rename model group: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return ModelGroup{}, fmt.Errorf("commit rename model group: %w", err)
	}
	return r.GetModelGroup(ctx, groupID)
}

// DeleteModelGroup removes a group and, with it, its member rows, recording the removal
// in the same transaction. The models themselves are CPA's, not ours: nothing outside
// this table is touched, so a model that was in this group stays wherever else it is
// listed.
func (r *Repository) DeleteModelGroup(ctx context.Context, id string, nowMS int64, audit AuditEvent) (bool, error) {
	groupID := strings.TrimSpace(id)

	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin delete model group: %w", err)
	}
	defer tx.Rollback()

	var name string
	err = tx.QueryRowContext(ctx, `SELECT name FROM model_groups WHERE id = ?`, groupID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read model group: %w", err)
	}
	var members int
	if err = tx.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM model_group_members WHERE group_id = ?`, groupID).Scan(&members); err != nil {
		return false, fmt.Errorf("count model group members: %w", err)
	}
	if err = writeModelGroupAuditTx(ctx, tx, audit, nowMS, groupID, map[string]any{"name": name, "members": members}); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM model_groups WHERE id = ?`, groupID); err != nil {
		return false, fmt.Errorf("delete model group: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return false, fmt.Errorf("commit delete model group: %w", err)
	}
	return true, nil
}

// writeModelGroupAuditTx admits and writes the group's audit row as the transaction's
// first write. A store that refuses it fails the whole write, which is what keeps "no
// trace, no change" true instead of merely intended. The error carries
// ErrModelGroupAuditFailed so the HTTP layer can answer a stable code instead of a bare
// 500 that a caller would read as a rejected name.
//
// The business details are merged into whatever the caller already put on the event
// rather than replacing it. The handler attaches the caller's own correlation id there,
// and dropping it would quietly lose the only field that ties this row to the request a
// client is asking about. Business keys win a collision: they describe the change, which
// is what the row is for, and the metadata keys are names the handler owns and does not
// reuse for business data.
func writeModelGroupAuditTx(ctx context.Context, tx *sql.Tx, audit AuditEvent, nowMS int64, targetID string, details map[string]any) error {
	audit.TargetType = "model_group"
	audit.TargetID = targetID
	audit.Result = "success"
	audit.OccurredAtMS = nowMS
	audit.Details = mergeAuditDetails(audit.Details, details)
	if _, err := insertAuditEvent(ctx, tx, audit); err != nil {
		return fmt.Errorf("%w: %w", ErrModelGroupAuditFailed, err)
	}
	return nil
}

// mergeAuditDetails combines the request metadata a handler attached with the business
// details a write produces. It returns nil when both are empty so an event with no
// details still stores the empty object insertAuditEvent writes by default.
func mergeAuditDetails(metadata, business map[string]any) map[string]any {
	if len(metadata) == 0 && len(business) == 0 {
		return nil
	}
	merged := make(map[string]any, len(metadata)+len(business))
	for key, value := range metadata {
		merged[key] = value
	}
	for key, value := range business {
		merged[key] = value
	}
	return merged
}

// AddModelGroupMembers appends models after the current last member. A model already
// in the group is reported as skipped rather than refused: the operator asked for the
// group to contain it, and it does. Duplicates within one request are handled the same
// way, so the same name twice in one call adds it once.
func (r *Repository) AddModelGroupMembers(ctx context.Context, id string, models []string, nowMS int64, audit AuditEvent) (ModelGroupAddResult, error) {
	clean := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		trimmed, err := validateModelGroupModel(model)
		if err != nil {
			return ModelGroupAddResult{}, err
		}
		if seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		clean = append(clean, trimmed)
	}
	if len(clean) == 0 {
		return ModelGroupAddResult{}, ErrModelGroupMember
	}

	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return ModelGroupAddResult{}, fmt.Errorf("begin add model group members: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM model_groups WHERE id = ?`, strings.TrimSpace(id)).Scan(&exists); err != nil {
		return ModelGroupAddResult{}, fmt.Errorf("check model group: %w", err)
	}
	if exists == 0 {
		return ModelGroupAddResult{}, ErrModelGroupNotFound
	}
	var position int
	if err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(position), 0) FROM model_group_members WHERE group_id = ?`,
		strings.TrimSpace(id)).Scan(&position); err != nil {
		return ModelGroupAddResult{}, fmt.Errorf("read model group tail: %w", err)
	}

	result := ModelGroupAddResult{Added: []string{}, Skipped: []string{}}
	// What would change is decided by reads, before any write, so the audit row can be
	// the transaction's first write and still name exactly the models it adds.
	for _, model := range clean {
		var present int
		if err = tx.QueryRowContext(ctx, `
			SELECT COUNT(1) FROM model_group_members WHERE group_id = ? AND model = ?`,
			strings.TrimSpace(id), model).Scan(&present); err != nil {
			return ModelGroupAddResult{}, fmt.Errorf("check model group member: %w", err)
		}
		if present > 0 {
			result.Skipped = append(result.Skipped, model)
			continue
		}
		result.Added = append(result.Added, model)
	}
	// A request that changes nothing records nothing: it is the same refusal-free
	// no-op the skipped model is, and an audit row for it would claim a write happened.
	if len(result.Added) > 0 {
		if err = writeModelGroupAuditTx(ctx, tx, audit, nowMS, strings.TrimSpace(id), map[string]any{"added": result.Added}); err != nil {
			return ModelGroupAddResult{}, err
		}
		for _, model := range result.Added {
			position++
			if _, err = tx.ExecContext(ctx, `
				INSERT INTO model_group_members (group_id, model, position, added_at_ms)
				VALUES (?, ?, ?, ?)`, strings.TrimSpace(id), model, position, nowMS); err != nil {
				return ModelGroupAddResult{}, fmt.Errorf("add model group member: %w", err)
			}
		}
		if _, err = tx.ExecContext(ctx, `UPDATE model_groups SET updated_at_ms = ? WHERE id = ?`, nowMS, strings.TrimSpace(id)); err != nil {
			return ModelGroupAddResult{}, fmt.Errorf("touch model group: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return ModelGroupAddResult{}, fmt.Errorf("commit add model group members: %w", err)
	}
	group, err := r.GetModelGroup(ctx, id)
	if err != nil {
		return ModelGroupAddResult{}, err
	}
	result.Group = group
	return result, nil
}

// RemoveModelGroupMember removes one model and closes the gap it leaves, so the
// remaining members read 1..n rather than keeping a hole where the model was.
func (r *Repository) RemoveModelGroupMember(ctx context.Context, id, model string, nowMS int64, audit AuditEvent) (ModelGroup, error) {
	clean, err := validateModelGroupModel(model)
	if err != nil {
		return ModelGroup{}, err
	}
	groupID := strings.TrimSpace(id)

	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return ModelGroup{}, fmt.Errorf("begin remove model group member: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM model_groups WHERE id = ?`, groupID).Scan(&exists); err != nil {
		return ModelGroup{}, fmt.Errorf("check model group: %w", err)
	}
	if exists == 0 {
		return ModelGroup{}, ErrModelGroupNotFound
	}
	// The member is confirmed present before the audit row, so a request naming a model
	// the group does not hold is refused without leaving a record of a removal.
	var present int
	if err = tx.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM model_group_members WHERE group_id = ? AND model = ?`,
		groupID, clean).Scan(&present); err != nil {
		return ModelGroup{}, fmt.Errorf("check model group member: %w", err)
	}
	if present == 0 {
		return ModelGroup{}, ErrModelGroupMember
	}
	if err = writeModelGroupAuditTx(ctx, tx, audit, nowMS, groupID, map[string]any{"removed": clean}); err != nil {
		return ModelGroup{}, err
	}
	if _, err = tx.ExecContext(ctx, `
		DELETE FROM model_group_members WHERE group_id = ? AND model = ?`, groupID, clean); err != nil {
		return ModelGroup{}, fmt.Errorf("remove model group member: %w", err)
	}
	if err = closeModelGroupGapTx(ctx, tx, groupID); err != nil {
		return ModelGroup{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE model_groups SET updated_at_ms = ? WHERE id = ?`, nowMS, groupID); err != nil {
		return ModelGroup{}, fmt.Errorf("touch model group: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return ModelGroup{}, fmt.Errorf("commit remove model group member: %w", err)
	}
	return r.GetModelGroup(ctx, id)
}

// closeModelGroupGapTx renumbers one group's members to 1..n in their current order.
//
// The new order is read first and applied from an explicit list. Renumbering in place
// with one UPDATE does not work twice over: (group_id, position) is unique, so a row
// cannot be moved onto a slot another row still holds, and SQLite updates row by row,
// so even a correlated re-count is evaluated against a table that is half rewritten and
// collides depending on which order the rows happen to come back in. Reading the order
// as data and writing the positions from it has neither problem.
func closeModelGroupGapTx(ctx context.Context, tx *sql.Tx, groupID string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT model FROM model_group_members WHERE group_id = ? ORDER BY position`, groupID)
	if err != nil {
		return fmt.Errorf("read model group order: %w", err)
	}
	ordered := []string{}
	for rows.Next() {
		var model string
		if err = rows.Scan(&model); err != nil {
			rows.Close()
			return fmt.Errorf("scan model group order: %w", err)
		}
		ordered = append(ordered, model)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("read model group order: %w", err)
	}
	if err = rows.Close(); err != nil {
		return fmt.Errorf("close model group order read: %w", err)
	}
	return writeModelGroupPositionsTx(ctx, tx, groupID, ordered)
}

// writeModelGroupPositionsTx puts groupID's members into the given order as 1..n.
//
// Positions are first pushed past every real one and then set, so no assignment can land
// on a slot another member has not vacated yet: the (group_id, position) uniqueness is
// momentarily held by values no caller reads, inside the caller's transaction.
func writeModelGroupPositionsTx(ctx context.Context, tx *sql.Tx, groupID string, ordered []string) error {
	if _, err := tx.ExecContext(ctx, `
		UPDATE model_group_members SET position = position + 1000000 WHERE group_id = ?`, groupID); err != nil {
		return fmt.Errorf("offset model group positions: %w", err)
	}
	for offset, model := range ordered {
		if _, err := tx.ExecContext(ctx, `
			UPDATE model_group_members SET position = ? WHERE group_id = ? AND model = ?`,
			offset+1, groupID, model); err != nil {
			return fmt.Errorf("renumber model group position: %w", err)
		}
	}
	return nil
}

// ReorderModelGroupMembers applies the operator's order wholesale. models must name
// exactly the group's current members, each once: a partial list would silently drop
// the rest, and a name the group does not hold would silently add it, so both are
// refused instead of guessed at.
func (r *Repository) ReorderModelGroupMembers(ctx context.Context, id string, models []string, nowMS int64, audit AuditEvent) (ModelGroup, error) {
	groupID := strings.TrimSpace(id)

	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return ModelGroup{}, fmt.Errorf("begin reorder model group members: %w", err)
	}
	defer tx.Rollback()

	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(1) FROM model_groups WHERE id = ?`, groupID).Scan(&exists); err != nil {
		return ModelGroup{}, fmt.Errorf("check model group: %w", err)
	}
	if exists == 0 {
		return ModelGroup{}, ErrModelGroupNotFound
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT model FROM model_group_members WHERE group_id = ?`, groupID)
	if err != nil {
		return ModelGroup{}, fmt.Errorf("read model group members: %w", err)
	}
	current := map[string]bool{}
	for rows.Next() {
		var model string
		if err = rows.Scan(&model); err != nil {
			rows.Close()
			return ModelGroup{}, fmt.Errorf("scan model group member: %w", err)
		}
		current[model] = true
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ModelGroup{}, fmt.Errorf("read model group members: %w", err)
	}
	if err = rows.Close(); err != nil {
		return ModelGroup{}, fmt.Errorf("close model group member read: %w", err)
	}

	if len(models) != len(current) {
		return ModelGroup{}, ErrModelGroupOrder
	}
	ordered := make([]string, 0, len(models))
	seen := make(map[string]bool, len(models))
	for _, model := range models {
		clean, err := validateModelGroupModel(model)
		if err != nil {
			return ModelGroup{}, err
		}
		if !current[clean] || seen[clean] {
			return ModelGroup{}, ErrModelGroupOrder
		}
		seen[clean] = true
		ordered = append(ordered, clean)
	}

	if err = writeModelGroupAuditTx(ctx, tx, audit, nowMS, groupID, map[string]any{"order": ordered}); err != nil {
		return ModelGroup{}, err
	}
	if err = writeModelGroupPositionsTx(ctx, tx, groupID, ordered); err != nil {
		return ModelGroup{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE model_groups SET updated_at_ms = ? WHERE id = ?`, nowMS, groupID); err != nil {
		return ModelGroup{}, fmt.Errorf("touch model group: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return ModelGroup{}, fmt.Errorf("commit reorder model group members: %w", err)
	}
	return r.GetModelGroup(ctx, id)
}

// RestoreDefaultModelGroups recreates a preset group that no longer exists, as an
// empty group. A preset that is still there is left exactly as it is - members, order
// and name included - whether or not it was renamed, because its identity is its id
// rather than its current name. A group the operator created under a preset's name is
// also left alone: the name is taken, and inventing a second "力工组" would make the
// name ambiguous for every later read.
//
// The whole restore is one transaction, and the audit row is its first write. What is
// missing is decided by reads before anything is written, the same way every other
// group write decides what it will do first: a restore with nothing missing is then a
// read-only transaction that writes nothing at all, not five inserts that happen to
// match no rows.
func (r *Repository) RestoreDefaultModelGroups(ctx context.Context, nowMS int64, audit AuditEvent) (ModelGroupRestoreResult, error) {
	tx, err := r.SQL().BeginTx(ctx, nil)
	if err != nil {
		return ModelGroupRestoreResult{}, fmt.Errorf("begin restore model groups: %w", err)
	}
	defer tx.Rollback()

	result := ModelGroupRestoreResult{Created: []ModelGroup{}, Skipped: []string{}}
	missing := []struct{ id, name string }{}
	for _, preset := range presetModelGroups {
		taken, err := presetModelGroupExistsTx(ctx, tx, preset.ID, preset.Name)
		if err != nil {
			return ModelGroupRestoreResult{}, err
		}
		if taken {
			result.Skipped = append(result.Skipped, preset.Name)
			continue
		}
		missing = append(missing, struct{ id, name string }{preset.ID, preset.Name})
	}
	// A restore with nothing missing is a no-op and records nothing, for the same reason
	// an add that skipped every model does.
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for _, preset := range missing {
			names = append(names, preset.name)
		}
		if err = writeModelGroupAuditTx(ctx, tx, audit, nowMS, "defaults", map[string]any{"created": names}); err != nil {
			return ModelGroupRestoreResult{}, err
		}
		for _, preset := range missing {
			if err = insertPresetModelGroupTx(ctx, tx, preset.id, preset.name, nowMS); err != nil {
				return ModelGroupRestoreResult{}, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return ModelGroupRestoreResult{}, fmt.Errorf("commit restore model groups: %w", err)
	}
	for _, preset := range missing {
		group, err := r.GetModelGroup(ctx, preset.id)
		if err != nil {
			return ModelGroupRestoreResult{}, err
		}
		result.Created = append(result.Created, group)
	}
	return result, nil
}

// presetModelGroupExistsTx reports whether a preset is already represented, by either of
// the two conditions restore must honour: the id still exists (so a renamed preset is
// left as it is rather than duplicated under its original name), or the name is taken by
// an operator's own group (so a second group with that name is never invented).
func presetModelGroupExistsTx(ctx context.Context, tx *sql.Tx, id, name string) (bool, error) {
	var exists int
	if err := tx.QueryRowContext(ctx, `
		SELECT COUNT(1) FROM model_groups
		WHERE id = ? OR name = ? COLLATE NOCASE`,
		id, name).Scan(&exists); err != nil {
		return false, fmt.Errorf("check preset model group %s: %w", id, err)
	}
	return exists > 0, nil
}

// insertPresetModelGroupTx creates one preset the caller has already found missing. The
// check-then-insert race is closed by the unique name index, not by this function: a
// concurrent creator makes the insert fail with a uniqueness violation, which rolls the
// whole restore back rather than leaving a duplicate or an audit row describing a group
// this transaction did not create. The caller sees a refusal and a retry reports the
// preset as skipped. One connection is held per database and restores are operator
// actions, so the losing side is theoretical; the constraint is what makes "at most one
// group per preset" a property of the schema rather than of the caller.
func insertPresetModelGroupTx(ctx context.Context, tx *sql.Tx, id, name string, nowMS int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO model_groups (id, name, is_preset, created_at_ms, updated_at_ms)
		VALUES (?, ?, 1, ?, ?)`, id, name, nowMS, nowMS); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("restore model group %s: another writer created it first", id)
		}
		return fmt.Errorf("restore model group %s: %w", id, err)
	}
	return nil
}
