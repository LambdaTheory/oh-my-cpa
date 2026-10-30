package repository

import (
	"context"
	"testing"
)

// TestMigration059UpgradesAnExistingDatabaseCoveringTheModelGroups is the acceptance
// claim for the upgrade path: an already-populated deployment advances to 059 and gains
// the five preset groups, without losing anything it already held. The generic upgrade
// test proves the whole chain applies; this one is about the state the new tables start
// in on a database that has been running, which is the case a fresh-database test
// cannot cover.
func TestMigration059UpgradesAnExistingDatabaseCoveringTheModelGroups(t *testing.T) {
	ctx := context.Background()
	db := openDatabaseAtMigration(t, 58)
	var beforeMax int
	if err := db.SQL.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&beforeMax); err != nil {
		t.Fatal(err)
	}
	if beforeMax != 58 {
		t.Fatalf("pre-upgrade schema version = %d, want 58", beforeMax)
	}
	var exists int
	if err := db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = 'model_groups'`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists != 0 {
		t.Fatal("the model group table exists before its migration")
	}
	// A row in an existing table must survive the upgrade untouched.
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO reset_announcements
		(id, provider, source, scope, confidence, original_text, registered_at_ms, created_at_ms)
		VALUES ('kept', 'claude', 'manual', 'all', 'confirmed', 'kept across 059', 1000, 1000)`); err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate 58 through 59: %v", err)
	}

	repo := New(db)
	groups, err := repo.ListModelGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := PresetModelGroupNames()
	if len(groups) != len(want) {
		t.Fatalf("after upgrade there are %d groups, want %d presets", len(groups), len(want))
	}
	for index, name := range want {
		if groups[index].Name != name || !groups[index].IsPreset || len(groups[index].Members) != 0 {
			t.Fatalf("upgraded preset %d = %+v", index, groups[index])
		}
	}
	var preserved int
	if err := db.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reset_announcements WHERE id = 'kept'`).Scan(&preserved); err != nil {
		t.Fatal(err)
	}
	if preserved != 1 {
		t.Fatalf("a pre-existing row did not survive the upgrade (count = %d)", preserved)
	}

	// Re-running the migrator must not seed a second copy of anything.
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("idempotent re-migrate: %v", err)
	}
	groups, err = repo.ListModelGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != len(want) {
		t.Fatalf("re-migrating duplicated the presets (%d groups)", len(groups))
	}
}
