package repository

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The restore path must write its audit row before it creates any group, and must write
// nothing at all when no preset is missing. Neither claim is observable from the final
// state - a restore that inserted first and recorded second commits the same rows as one
// that recorded first - so these tests enforce the ordering at the storage layer, with a
// trigger that fails an insert which happens before the audit row exists in the same
// transaction. An uncommitted row is visible to a trigger in its own transaction, which
// is what makes the check exact rather than a race.

// requireAuditFirst installs a trigger that aborts any preset creation until a
// restore audit row is present in the current transaction.
func requireAuditFirst(t *testing.T, repo *Repository) {
	t.Helper()
	if _, err := repo.SQL().ExecContext(context.Background(), `
		CREATE TRIGGER require_restore_audit_first BEFORE INSERT ON model_groups
		WHEN NEW.is_preset = 1
		BEGIN
			SELECT CASE WHEN (
				SELECT COUNT(1) FROM audit_events
				WHERE action = 'model_group.restore_defaults'
			) = 0 THEN RAISE(ABORT, 'a preset was inserted before its audit row') END;
		END`); err != nil {
		t.Fatalf("install audit-ordering trigger: %v", err)
	}
}

func restoreAuditRows(t *testing.T, repo *Repository) []AuditEvent {
	t.Helper()
	events, err := repo.ListAuditEvents(context.Background(), 500)
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	restores := make([]AuditEvent, 0, 4)
	for _, event := range events {
		if event.Action == "model_group.restore_defaults" {
			restores = append(restores, event)
		}
	}
	return restores
}

// TestRestoreWritesItsAuditBeforeThePresetsItCreates covers the ordering with a partial
// restore - the case a final-state check cannot distinguish from the reverse order.
func TestRestoreWritesItsAuditBeforeThePresetsItCreates(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	// Two of the five go missing, the other three stay, so the restore is neither a
	// no-op nor a full rebuild.
	for _, name := range []string{"编排组", "审查组"} {
		group := findGroupByName(t, repo, ctx, name)
		if _, err := repo.DeleteModelGroup(ctx, group.ID, 1000, testAudit("model_group.delete")); err != nil {
			t.Fatal(err)
		}
	}
	requireAuditFirst(t, repo)

	restored, err := repo.RestoreDefaultModelGroups(ctx, 5000, testAudit("model_group.restore_defaults"))
	if err != nil {
		t.Fatalf("restore under the ordering trigger: %v", err)
	}
	if len(restored.Created) != 2 {
		t.Fatalf("restore created %d groups, want 2", len(restored.Created))
	}
	created := make([]string, 0, len(restored.Created))
	for _, group := range restored.Created {
		created = append(created, group.Name)
	}
	if got := strings.Join(created, ","); got != "编排组,审查组" {
		t.Fatalf("restored %s, want 编排组,审查组", got)
	}
	if got := strings.Join(restored.Skipped, ","); got != "力工组,前端组,跟进组" {
		t.Fatalf("skipped %s, want the three that were still there", got)
	}

	// The audit row names exactly the groups that were created, not all five.
	rows := restoreAuditRows(t, repo)
	if len(rows) != 1 {
		t.Fatalf("restore recorded %d audit rows, want 1", len(rows))
	}
	var names []string
	if encoded, err := json.Marshal(rows[0].Details["created"]); err == nil {
		_ = json.Unmarshal(encoded, &names)
	}
	if got := strings.Join(names, ","); got != "编排组,审查组" {
		t.Fatalf("audit row names %q, want only the two created groups", got)
	}
}

// TestRestoreWithNothingMissingWritesNothing proves the no-op path is a pure read.
//
// A trigger cannot make this claim: a statement that inserts zero rows fires no
// BEFORE INSERT, so the `INSERT ... SELECT ... WHERE NOT EXISTS` a previous revision used
// slipped past every row-level check while still being a write. `query_only` refuses the
// statement itself, which is what makes "a restore with nothing missing writes nothing"
// observable rather than merely true of the effect.
func TestRestoreWithNothingMissingWritesNothing(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	before := restoreAuditRows(t, repo)
	if _, err := repo.SQL().ExecContext(ctx, `PRAGMA query_only = ON`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := repo.SQL().ExecContext(context.Background(), `PRAGMA query_only = OFF`); err != nil {
			t.Errorf("restore query_only: %v", err)
		}
	})

	restored, err := repo.RestoreDefaultModelGroups(ctx, 5000, testAudit("model_group.restore_defaults"))
	if err != nil {
		t.Fatalf("a restore with no preset missing must issue no write: %v", err)
	}
	if len(restored.Created) != 0 {
		t.Fatalf("restore created %v with all five present", restored.Created)
	}
	if len(restored.Skipped) != 5 {
		t.Fatalf("restore skipped %v, want all five", restored.Skipped)
	}
	if after := restoreAuditRows(t, repo); len(after) != len(before) {
		t.Fatalf("a no-op restore recorded an audit row: %d before, %d after", len(before), len(after))
	}
}

// TestRestoreKeepsARenamedPresetAndItsMembers is the identity rule at repository level,
// re-asserted here because the read-first rewrite is what decides "missing" now: a
// renamed preset is still found by its id and must not be created a second time.
func TestRestoreKeepsARenamedPresetAndItsMembers(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	labor := findGroupByName(t, repo, ctx, "力工组")
	if _, err := repo.AddModelGroupMembers(ctx, labor.ID, []string{"kept"}, 1000, testAudit("model_group.members.add")); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RenameModelGroup(ctx, labor.ID, "改名后的力工组", 1100, testAudit("model_group.rename")); err != nil {
		t.Fatal(err)
	}
	// Two others go missing so the restore is a real partial one.
	for _, name := range []string{"前端组", "跟进组"} {
		group := findGroupByName(t, repo, ctx, name)
		if _, err := repo.DeleteModelGroup(ctx, group.ID, 1200, testAudit("model_group.delete")); err != nil {
			t.Fatal(err)
		}
	}
	requireAuditFirst(t, repo)

	restored, err := repo.RestoreDefaultModelGroups(ctx, 5000, testAudit("model_group.restore_defaults"))
	if err != nil {
		t.Fatal(err)
	}
	if len(restored.Created) != 2 {
		t.Fatalf("restore created %d groups, want 2 (the renamed preset is not missing)", len(restored.Created))
	}
	kept, err := repo.GetModelGroup(ctx, labor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Name != "改名后的力工组" {
		t.Fatalf("restore overwrote the renamed preset: %q", kept.Name)
	}
	assertMembers(t, kept, []string{"kept"})
	var renamed int
	if err := repo.SQL().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM model_groups WHERE name = '力工组'`).Scan(&renamed); err != nil {
		t.Fatal(err)
	}
	if renamed != 0 {
		t.Fatalf("restore created a second 力工组 beside the renamed one")
	}
}

// TestModelGroupAuditKeepsCallerMetadataWithBusinessDetails covers the second review
// point: the write's own details are merged into the event rather than replacing the
// request metadata the handler attached.
func TestModelGroupAuditKeepsCallerMetadataWithBusinessDetails(t *testing.T) {
	repo, ctx := modelGroupRepo(t)
	event := AuditEvent{
		Action:  "model_group.create",
		Details: map[string]any{"client_request_id": "caller-abc-123"},
	}
	group, err := repo.CreateModelGroup(ctx, "带关联ID的组", 5000, event)
	if err != nil {
		t.Fatal(err)
	}

	events, err := repo.ListAuditEvents(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	var found *AuditEvent
	for index := range events {
		if events[index].Action == "model_group.create" && events[index].TargetID == group.ID {
			found = &events[index]
			break
		}
	}
	if found == nil {
		t.Fatal("the create recorded no audit row")
	}
	if found.Details["client_request_id"] != "caller-abc-123" {
		t.Fatalf("audit details lost the caller's correlation id: %+v", found.Details)
	}
	if found.Details["name"] != "带关联ID的组" {
		t.Fatalf("audit details lost the business detail: %+v", found.Details)
	}
}
