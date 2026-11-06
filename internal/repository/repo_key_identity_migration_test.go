package repository

import (
	"context"
	"errors"
	"testing"
)

func seedRepoKeyIdentityBeforeUpgrade(t *testing.T, db *DB, id, repoID string) {
	t.Helper()
	if _, err := db.SQL.Exec(`INSERT INTO repo_key_bindings
		(id, instance_id, repo_id, repo_has_remote, key_fingerprint, key_mask, status, created_at_ms)
		VALUES (?, 'default', ?, 1, ?, 'synthetic-mask', 'active', 1000)`, id, repoID, "synthetic-fp-"+id); err != nil {
		t.Fatal(err)
	}
}

func TestRepoKeyIdentityMigrationPreservesOldRegistration(t *testing.T) {
	ctx := context.Background()
	db := openDatabaseAtMigration(t, 62)
	seedRepoKeyIdentityBeforeUpgrade(t, db, "existing", "Legacy-Org/Existing-Repo")
	seedRepoKeyIdentityBeforeUpgrade(t, db, "other-host", "gitlab.com/Team/Project")
	if _, err := db.SQL.Exec(`INSERT INTO repo_key_applications
		(id, instance_id, repo_id, repo_has_remote, agent_name, device_name, purpose, kind, status,
		 public_key, public_key_fingerprint, key_algorithm, source, authorization_token_hash,
		 token_expires_at_ms, created_at_ms, updated_at_ms, binding_id, decided_by, decided_at_ms)
		VALUES ('existing-app', 'default', 'Legacy-Org/Existing-Repo', 1, 'agent', 'device', 'synthetic use',
		 'issue', 'approved', 'synthetic-public-key', 'synthetic-public-fp', 'x25519', 'agent',
		 'synthetic-token-hash', 2000, 1000, 1100, 'existing', 'operator', 1100)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.Exec(`INSERT INTO repo_key_recipients
		(instance_id, repo_id, agent_name, device_name, approved_at_ms, approved_by)
		VALUES ('default', 'Legacy-Org/Existing-Repo', 'agent', 'device', 1100, 'operator')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	repo := New(db)
	binding, err := repo.GetActiveRepoKeyBinding(ctx, "default", "legacy-org/existing-repo")
	if err != nil || binding.ID != "existing" || binding.KeyFingerprint != "synthetic-fp-existing" {
		t.Fatal("canonical lookup lost the old key registration")
	}
	application, err := repo.GetRepoKeyApplication(ctx, "existing-app")
	if err != nil || application.RepoID != "legacy-org/existing-repo" || application.RepoDisplay != "Legacy-Org/Existing-Repo" || application.BindingID != binding.ID {
		t.Fatal("upgrade lost the application display spelling or binding association")
	}
	recipient, err := repo.IsRepoKeyRecipient(ctx, "default", "legacy-org/existing-repo", "agent", "device")
	if err != nil || !recipient {
		t.Fatal("upgrade lost the approved recipient")
	}
	other, err := repo.GetActiveRepoKeyBinding(ctx, "default", "gitlab.com/Team/Project")
	if err != nil || other.ID != "other-host" {
		t.Fatal("upgrade folded a non-GitHub identity")
	}
	_, err = repo.CreateRepoKeyBinding(ctx, RepoKeyBinding{InstanceID: "default", RepoID: "LEGACY-ORG/EXISTING-REPO", RepoHasRemote: true, KeyFingerprint: "synthetic-duplicate-fp", KeyMask: "synthetic-mask"}, 1200, testAudit("repo_key.reissue"))
	if !errors.Is(err, ErrRepoKeyConflict) {
		t.Fatalf("the storage boundary admitted a case-variant active key: %v", err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("re-running the upgrade changed its outcome: %v", err)
	}
}

func TestRepoKeyIdentityMigrationPreservesSinglePathOtherHost(t *testing.T) {
	ctx := context.Background()
	db := openDatabaseAtMigration(t, 62)
	for index, identity := range []string{"git.example.com/Repo", "git.example.com/repo", "localhost/Repo", "localhost/repo"} {
		id := []string{"host-upper", "host-lower", "local-upper", "local-lower"}[index]
		seedRepoKeyIdentityBeforeUpgrade(t, db, id, identity)
		if _, err := db.SQL.Exec(`INSERT INTO repo_key_applications
			(id, instance_id, repo_id, repo_has_remote, agent_name, device_name, purpose, kind, status,
			 public_key, public_key_fingerprint, key_algorithm, source, authorization_token_hash,
			 token_expires_at_ms, created_at_ms, updated_at_ms)
			VALUES (?, 'default', ?, 1, 'agent', 'device', 'synthetic use', 'issue', 'pending',
			 'synthetic-public-key', 'synthetic-public-fp', 'x25519', 'agent', ?, 2000, 1000, 1000)`, id, identity, id+"-token"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL.Exec(`INSERT INTO repo_key_recipients
			(instance_id, repo_id, agent_name, device_name, approved_at_ms, approved_by)
			VALUES ('default', ?, 'agent', 'device', 1100, 'operator')`, identity); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("case-sensitive host paths collided during migration: %v", err)
	}
	repo := New(db)
	for index, identity := range []string{"git.example.com/Repo", "git.example.com/repo", "localhost/Repo", "localhost/repo"} {
		id := []string{"host-upper", "host-lower", "local-upper", "local-lower"}[index]
		binding, err := repo.GetActiveRepoKeyBinding(ctx, "default", identity)
		if err != nil || binding.ID != id {
			t.Fatalf("migration lost the exact host/path identity %q", identity)
		}
		application, err := repo.GetRepoKeyApplication(ctx, id)
		if err != nil || application.RepoID != identity || application.RepoDisplay != identity {
			t.Fatalf("migration folded a host/path application %q", identity)
		}
		recipient, err := repo.IsRepoKeyRecipient(ctx, "default", identity, "agent", "device")
		if err != nil || !recipient {
			t.Fatalf("migration folded a host/path recipient %q", identity)
		}
	}
}

func TestRepoKeyIdentityMigrationRefusesAmbiguousActiveKeysWithoutDataLoss(t *testing.T) {
	db := openDatabaseAtMigration(t, 62)
	seedRepoKeyIdentityBeforeUpgrade(t, db, "one", "Legacy-Org/Existing-Repo")
	seedRepoKeyIdentityBeforeUpgrade(t, db, "two", "legacy-org/existing-repo")
	if err := db.Migrate(context.Background()); err == nil {
		t.Fatal("upgrade silently chose between two usable keys of the same repository")
	}
	var count, version int
	if err := db.SQL.QueryRow(`SELECT COUNT(*) FROM repo_key_bindings WHERE status = 'active'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.SQL.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	var spelling string
	if err := db.SQL.QueryRow(`SELECT repo_id FROM repo_key_bindings WHERE id = 'one'`).Scan(&spelling); err != nil {
		t.Fatal(err)
	}
	if count != 2 || version != 62 || spelling != "Legacy-Org/Existing-Repo" {
		t.Fatal("a refused upgrade changed or discarded existing registrations")
	}
}
