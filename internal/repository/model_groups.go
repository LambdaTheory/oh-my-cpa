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

func (r *Repository) CreateModelGroup(ctx context.Context, name string, nowMS int64) (ModelGroup, error) {
	clean, err := validateModelGroupName(name)
	if err != nil {
		return ModelGroup{}, err
	}
	group := ModelGroup{ID: uuid.NewString(), Name: clean, CreatedAtMS: nowMS, UpdatedAtMS: nowMS, Members: []ModelGroupMember{}}
	if _, err = r.SQL().ExecContext(ctx, `
		INSERT INTO model_groups (id, name, is_preset, created_at_ms, updated_at_ms)
		VALUES (?, ?, 0, ?, ?)`, group.ID, group.Name, nowMS, nowMS); err != nil {
		if isUniqueViolation(err) {
			return ModelGroup{}, ErrModelGroupNameConflict
		}
		return ModelGroup{}, fmt.Errorf("create model group: %w", err)
	}
	return group, nil
}

func (r *Repository) RenameModelGroup(ctx context.Context, id, name string, nowMS int64) (ModelGroup, error) {
	clean, err := validateModelGroupName(name)
	if err != nil {
		return ModelGroup{}, err
	}
	result, err := r.SQL().ExecContext(ctx, `
		UPDATE model_groups SET name = ?, updated_at_ms = ? WHERE id = ?`, clean, nowMS, strings.TrimSpace(id))
	if err != nil {
		if isUniqueViolation(err) {
			return ModelGroup{}, ErrModelGroupNameConflict
		}
		return ModelGroup{}, fmt.Errorf("rename model group: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ModelGroup{}, ErrModelGroupNotFound
	}
	return r.GetModelGroup(ctx, id)
}

// DeleteModelGroup removes a group and, with it, its member rows. The models
// themselves are CPA's, not ours: nothing outside this table is touched, so a model
// that was in this group stays wherever else it is listed.
func (r *Repository) DeleteModelGroup(ctx context.Context, id string) (bool, error) {
	result, err := r.SQL().ExecContext(ctx, `DELETE FROM model_groups WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, fmt.Errorf("delete model group: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted model group: %w", err)
	}
	return affected > 0, nil
}

// AddModelGroupMembers appends models after the current last member. A model already
// in the group is reported as skipped rather than refused: the operator asked for the
// group to contain it, and it does. Duplicates within one request are handled the same
// way, so the same name twice in one call adds it once.
func (r *Repository) AddModelGroupMembers(ctx context.Context, id string, models []string, nowMS int64) (ModelGroupAddResult, error) {
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
		position++
		if _, err = tx.ExecContext(ctx, `
			INSERT INTO model_group_members (group_id, model, position, added_at_ms)
			VALUES (?, ?, ?, ?)`, strings.TrimSpace(id), model, position, nowMS); err != nil {
			return ModelGroupAddResult{}, fmt.Errorf("add model group member: %w", err)
		}
		result.Added = append(result.Added, model)
	}
	if len(result.Added) > 0 {
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
func (r *Repository) RemoveModelGroupMember(ctx context.Context, id, model string, nowMS int64) (ModelGroup, error) {
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
	result, err := tx.ExecContext(ctx, `
		DELETE FROM model_group_members WHERE group_id = ? AND model = ?`, groupID, clean)
	if err != nil {
		return ModelGroup{}, fmt.Errorf("remove model group member: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ModelGroup{}, fmt.Errorf("count removed model group member: %w", err)
	}
	if affected == 0 {
		return ModelGroup{}, ErrModelGroupMember
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
func (r *Repository) ReorderModelGroupMembers(ctx context.Context, id string, models []string, nowMS int64) (ModelGroup, error) {
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
func (r *Repository) RestoreDefaultModelGroups(ctx context.Context, nowMS int64) (ModelGroupRestoreResult, error) {
	result := ModelGroupRestoreResult{Created: []ModelGroup{}, Skipped: []string{}}
	for _, preset := range presetModelGroups {
		group, created, err := r.restorePresetModelGroup(ctx, preset.ID, preset.Name, nowMS)
		if err != nil {
			return ModelGroupRestoreResult{}, err
		}
		if created {
			result.Created = append(result.Created, group)
			continue
		}
		result.Skipped = append(result.Skipped, preset.Name)
	}
	return result, nil
}

// restorePresetModelGroup inserts one preset if neither its id nor its name is taken.
// The insert is the decision point: the unique name index decides the race, so two
// restores running at once cannot both create the same group.
func (r *Repository) restorePresetModelGroup(ctx context.Context, id, name string, nowMS int64) (ModelGroup, bool, error) {
	result, err := r.SQL().ExecContext(ctx, `
		INSERT INTO model_groups (id, name, is_preset, created_at_ms, updated_at_ms)
		SELECT ?, ?, 1, ?, ?
		WHERE NOT EXISTS (SELECT 1 FROM model_groups WHERE id = ?)
		  AND NOT EXISTS (SELECT 1 FROM model_groups WHERE name = ? COLLATE NOCASE)`,
		id, name, nowMS, nowMS, id, name)
	if err != nil {
		if isUniqueViolation(err) {
			return ModelGroup{}, false, nil
		}
		return ModelGroup{}, false, fmt.Errorf("restore model group %s: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return ModelGroup{}, false, fmt.Errorf("count restored model group: %w", err)
	}
	if affected == 0 {
		return ModelGroup{}, false, nil
	}
	group, err := r.GetModelGroup(ctx, id)
	if err != nil {
		return ModelGroup{}, false, err
	}
	return group, true, nil
}
