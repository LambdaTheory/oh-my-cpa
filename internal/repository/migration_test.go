package repository

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
	"github.com/oh-my-cpa/oh-my-cpa/migrations"
)

func TestMigration004SanitizesHistoricalData(t *testing.T) {
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	tempDir, err := os.MkdirTemp("", "oh-my-cpa-migration-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test_upgrade.db")
	rawDB, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}

	// Apply migrations 001, 002, 003 manually to simulate a v0.3 database
	for _, versionFile := range []string{"001_initial.sql", "002_usage_ingest.sql", "003_ui_preferences.sql"} {
		content, readErr := migrations.Files.ReadFile(versionFile)
		if readErr != nil {
			t.Fatalf("read migration %s: %v", versionFile, readErr)
		}
		if _, execErr := rawDB.Exec(string(content)); execErr != nil {
			t.Fatalf("exec migration %s: %v", versionFile, execErr)
		}
		ver := 1
		if strings.HasPrefix(versionFile, "002") {
			ver = 2
		} else if strings.HasPrefix(versionFile, "003") {
			ver = 3
		}
		if _, execErr := rawDB.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 1000)`, ver); execErr != nil {
			t.Fatalf("record migration %d: %v", ver, execErr)
		}
	}

	// Insert cpa_instance
	if _, err := rawDB.Exec(`
		INSERT INTO cpa_instances (id, name, base_url, usage_addr, management_key_ciphertext, management_key_nonce, created_at, updated_at)
		VALUES ('default', 'Default', 'http://127.0.0.1:8317', '127.0.0.1:8317', x'00', x'00', 100, 100)`); err != nil {
		t.Fatal(err)
	}

	// Seed historical data containing sensitive secrets
	sensitiveSecrets := []string{
		"sk-hist-res-account",
		"sk-hist-res-extra-account",
		"sk-hist-inbox-key",
		"sk-hist-inbox-err-token",
		"sk-hist-err-body-secret",
		"sk-hist-err-auth-status",
		"sk-hist-err-quota-reason",
		"sk-hist-usage-group-key",
		"sk-hist-usage-source",
	}

	// 1. discovered_resources:
	// a. with account & extra.account
	if _, err := rawDB.Exec(`
		INSERT INTO discovered_resources (id, instance_id, resource_key, cpa_resource_type, cpa_driver, protocol_driver, protocol_display, details_json, last_seen_at, created_at, updated_at)
		VALUES ('res-1', 'default', 'k1', 'auth-file', 'openai', 'openai', 'OpenAI', ?, 100, 100, 100)`,
		`{"account":"sk-hist-res-account","extra":{"account":"sk-hist-res-extra-account","account_present":"true","account_type":"api_key","other_junk":"strip_me"}}`); err != nil {
		t.Fatal(err)
	}
	// b. with empty details_json
	if _, err := rawDB.Exec(`
		INSERT INTO discovered_resources (id, instance_id, resource_key, cpa_resource_type, cpa_driver, protocol_driver, protocol_display, details_json, last_seen_at, created_at, updated_at)
		VALUES ('res-2', 'default', 'k2', 'codex-api-key', 'codex', 'openai', 'OpenAI', '{}', 100, 100, 100)`); err != nil {
		t.Fatal(err)
	}
	// c. with malformed json
	if _, err := rawDB.Exec(`
		INSERT INTO discovered_resources (id, instance_id, resource_key, cpa_resource_type, cpa_driver, protocol_driver, protocol_display, details_json, last_seen_at, created_at, updated_at)
		VALUES ('res-3', 'default', 'k3', 'codex-api-key', 'codex', 'openai', 'OpenAI', 'invalid-json-text', 100, 100, 100)`); err != nil {
		t.Fatal(err)
	}

	// 2. usage_inboxes:
	if _, err := rawDB.Exec(`
		INSERT INTO usage_inboxes (id, instance_id, source_mode, message_hash, raw_message, status, attempt_count, last_error, popped_at)
		VALUES (1, 'default', 'http_pull', 'hash1', ?, 'pending', 0, ?, 1000)`,
		`{"request_id":"req-1","api_key":"sk-hist-inbox-key","model":"m","tokens":{"total_tokens":5}}`,
		`failed Authorization: Bearer sk-hist-inbox-err-token`); err != nil {
		t.Fatal(err)
	}

	// 3. error_events:
	if _, err := rawDB.Exec(`
		INSERT INTO error_events (id, instance_id, event_key, request_id, provider, model, auth_id, auth_index, status_code, code, body, retryable, auth_status, quota_reason, timestamp_ms, created_at_ms)
		VALUES (1, 'default', 'err-1', 'req-1', 'openai', 'm', 'a1', 'idx1', 400, 'ERR', ?, 0, ?, ?, 1000, 1000)`,
		`rejected with api_key="sk-hist-err-body-secret"`,
		`invalid status: sk-hist-err-auth-status`,
		`quota exhausted: sk-hist-err-quota-reason`); err != nil {
		t.Fatal(err)
	}

	// 4. usage_events:
	if _, err := rawDB.Exec(`
		INSERT INTO usage_events (id, instance_id, event_key, api_group_key, provider, endpoint, auth_type, request_id, timestamp_ms, source, auth_index, failed, generate, latency_ms, total_tokens, created_at_ms)
		VALUES (1, 'default', 'ev-1', 'sk-hist-usage-group-key', 'openai', 'https://user:pass@example.test/v1?token=secret#frag', 'apikey', 'req-1', 1000, 'sk-hist-usage-source', 'idx1', 0, 1, 10, 5, 1000)`); err != nil {
		t.Fatal(err)
	}

	_ = rawDB.Close()

	// Now open the database through Open with WithMigrationBackup and WithCipher
	backupDir := filepath.Join(tempDir, "backups")
	db, err := Open(context.Background(), dbPath, WithMigrationBackup(cipher, backupDir, 3))
	if err != nil {
		t.Fatalf("Open and migrate failed: %v", err)
	}
	defer db.Close()

	// Verify backup was created before migration 004
	backupFiles, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("read backup dir: %v", err)
	}
	encCount := 0
	for _, f := range backupFiles {
		if strings.HasSuffix(f.Name(), ".db.enc") {
			encCount++
		}
	}
	if encCount == 0 {
		t.Fatal("expected encrypted migration backup to be created, found none")
	}

	// Verify that NONE of the sensitive secrets exist anywhere in the migrated database
	for _, secret := range sensitiveSecrets {
		// Check discovered_resources
		var count int
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM discovered_resources WHERE details_json LIKE ?`, "%"+secret+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Fatalf("secret %q leaked into discovered_resources.details_json", secret)
		}

		// Check usage_inboxes raw_message and last_error
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM usage_inboxes WHERE raw_message LIKE ? OR last_error LIKE ?`, "%"+secret+"%", "%"+secret+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Fatalf("secret %q leaked into usage_inboxes", secret)
		}

		// Check error_events body, auth_status, quota_reason
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM error_events WHERE body LIKE ? OR auth_status LIKE ? OR quota_reason LIKE ?`, "%"+secret+"%", "%"+secret+"%", "%"+secret+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Fatalf("secret %q leaked into error_events", secret)
		}

		// Check usage_events api_group_key and source
		if err := db.SQL.QueryRow(`SELECT COUNT(1) FROM usage_events WHERE api_group_key LIKE ? OR source LIKE ?`, "%"+secret+"%", "%"+secret+"%").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			t.Fatalf("secret %q leaked into usage_events", secret)
		}
	}

	// Verify specific sanitized values
	var groupKey, groupLabel, source string
	if err := db.SQL.QueryRow(`SELECT api_group_key, api_group_label, source FROM usage_events WHERE id = 1`).Scan(&groupKey, &groupLabel, &source); err != nil {
		t.Fatal(err)
	}
	if groupLabel != "api_key" {
		t.Fatalf("api_group_label = %q, want api_key", groupLabel)
	}
	if !strings.HasPrefix(groupKey, "hmac:") && groupKey != security.RedactedValue {
		t.Fatalf("api_group_key = %q, want hmac prefix or redacted", groupKey)
	}
	if !strings.HasPrefix(source, "hmac:") && source != security.RedactedValue {
		t.Fatalf("source = %q, want hmac prefix or redacted", source)
	}

	// Verify usage inbox ciphertext was populated and can be decrypted
	var ciphertext, nonce []byte
	var rawMsg string
	if err := db.SQL.QueryRow(`SELECT raw_message_ciphertext, raw_message_nonce, raw_message FROM usage_inboxes WHERE id = 1`).Scan(&ciphertext, &nonce, &rawMsg); err != nil {
		t.Fatal(err)
	}
	if len(ciphertext) == 0 || len(nonce) == 0 {
		t.Fatal("expected raw_message_ciphertext and nonce to be populated")
	}
	decrypted, err := cipher.Decrypt(ciphertext, nonce)
	if err != nil {
		t.Fatalf("failed to decrypt inbox ciphertext: %v", err)
	}
	if !strings.Contains(string(decrypted), "sk-hist-inbox-key") {
		t.Fatalf("decrypted ciphertext does not contain original payload: %s", string(decrypted))
	}
	if strings.Contains(rawMsg, "sk-hist-inbox-key") {
		t.Fatalf("raw_message projection contains secret: %s", rawMsg)
	}
	if !strings.Contains(rawMsg, security.RedactedValue) {
		t.Fatalf("raw_message projection missing redacted marker: %s", rawMsg)
	}

	// Re-opening the database must succeed idempotently
	db2, err := Open(context.Background(), dbPath, WithMigrationBackup(cipher, backupDir, 3))
	if err != nil {
		t.Fatalf("idempotent reopen failed: %v", err)
	}
	_ = db2.Close()
}

func TestMigration045UpgradesForkSchemaThrough054(t *testing.T) {
	ctx := context.Background()
	database := openDatabaseAtMigration(t, 45)

	var beforeMax int
	if err := database.SQL.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_migrations`).Scan(&beforeMax); err != nil {
		t.Fatal(err)
	}
	if beforeMax != 45 {
		t.Fatalf("pre-upgrade schema version = %d, want 45", beforeMax)
	}
	var beforeCount int
	if err := database.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&beforeCount); err != nil {
		t.Fatal(err)
	}
	if beforeCount != 40 {
		t.Fatalf("pre-upgrade migration count = %d, want 40", beforeCount)
	}

	forkTables := []string{
		"quota_observations",
		"account_automation_settings",
		"account_automation_accounts",
		"account_automation_batches",
		"ollama_accounts",
		"feishu_settings",
		"feishu_secrets",
		"feishu_pending_items",
		"feishu_notifications",
		"feishu_channel_tracks",
		"feishu_state",
		"feishu_account_signals",
	}
	for _, table := range forkTables {
		var count int
		if err := database.SQL.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("check pre-upgrade table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("pre-upgrade table %s count = %d, want 1", table, count)
		}
	}
	if _, err := database.SQL.ExecContext(ctx, `
		INSERT INTO quota_observations
			(auth_index, provider, window_kind, window_minutes, used_percent, observed_at_ms, event_key, created_at_ms)
		VALUES ('synthetic-auth', 'codex', 'weekly', 10080, 25, 1000, 'synthetic-event', 1000)`); err != nil {
		t.Fatalf("seed pre-upgrade fork row: %v", err)
	}

	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate schema 45 through 53: %v", err)
	}

	upstreamTables := []string{
		"reauth_jobs",
		"credential_warmup_policies",
		"credential_warmup_state",
		"credential_warmup_runs",
		"fonts",
		"font_faces",
		"channel_model_prices",
		"reset_announcements",
		"quota_refresh_attempts",
	}
	for _, table := range append(forkTables, upstreamTables...) {
		var count int
		if err := database.SQL.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("check table %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}
	var preservedCount int
	if err := database.SQL.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM quota_observations WHERE event_key = 'synthetic-event'`).Scan(&preservedCount); err != nil {
		t.Fatal(err)
	}
	if preservedCount != 1 {
		t.Fatalf("pre-upgrade fork row count = %d after migration, want 1", preservedCount)
	}

	for table, column := range map[string]string{
		"quota_snapshots":        "warmup_json",
		"model_price_versions":   "channel",
		"quota_refresh_attempts": "reason_summary",
	} {
		var count int
		if err := database.SQL.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&count); err != nil {
			t.Fatalf("check %s.%s: %v", table, column, err)
		}
		if count != 1 {
			t.Fatalf("column %s.%s count = %d, want 1", table, column, count)
		}
	}

	rows, err := database.SQL.QueryContext(ctx, `
		SELECT version
		FROM schema_migrations
		ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	expectedVersions := make([]int, 0, 49)
	for version := 1; version <= 9; version++ {
		expectedVersions = append(expectedVersions, version)
	}
	for version := 15; version <= 54; version++ {
		expectedVersions = append(expectedVersions, version)
	}
	for _, want := range expectedVersions {
		if !rows.Next() {
			t.Fatalf("schema_migrations ended before version %d", want)
		}
		var got int
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("schema_migrations version = %d, want %d", got, want)
		}
	}
	if rows.Next() {
		var extra int
		if err := rows.Scan(&extra); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("unexpected migration version %d", extra)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestBackupRestoreSmokeAndFreeSpaceCheck(t *testing.T) {
	cipher, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	tempDir, err := os.MkdirTemp("", "oh-my-cpa-backup-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.db")
	backupDir := filepath.Join(tempDir, "backups")

	// 1. Test insufficient free space fails closed
	noSpacePolicy := BackupConfig{
		Cipher:       cipher,
		Directory:    backupDir,
		Retention:    3,
		MinFreeBytes: 10 * 1024 * 1024,
		FreeSpace: func(path string) (uint64, error) {
			return 100, nil // only 100 bytes available
		},
	}

	// Initialize file DB with migration 001 first
	initDB, err := Open(context.Background(), dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = initDB.Close()

	// Re-open with mock insufficient space and simulate pending migration
	// (we can test createMigrationBackup directly)
	db, err := Open(context.Background(), dbPath, WithBackupConfig(noSpacePolicy))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = db.createMigrationBackup(context.Background(), 999)
	if err == nil || !strings.Contains(err.Error(), "insufficient free space") {
		t.Fatalf("expected insufficient free space error, got: %v", err)
	}

	// 2. Test successful backup and restore smoke test
	sufficientPolicy := BackupConfig{
		Cipher:    cipher,
		Directory: backupDir,
		Retention: 2,
		FreeSpace: func(path string) (uint64, error) {
			return 1024 * 1024 * 1024, nil
		},
	}
	db.backup = sufficientPolicy
	if err := db.createMigrationBackup(context.Background(), 100); err != nil {
		t.Fatalf("createMigrationBackup failed: %v", err)
	}

	// Check backup file exists and has correct permissions
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	var backupPath string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".db.enc") {
			backupPath = filepath.Join(backupDir, e.Name())
			break
		}
	}
	if backupPath == "" {
		t.Fatal("no .db.enc file found")
	}

	// Check restore smoke passes
	if err := RestoreBackupSmoke(context.Background(), backupPath, cipher); err != nil {
		t.Fatalf("RestoreBackupSmoke failed: %v", err)
	}

	// Check digest mismatch rejection
	shaFile := backupPath + ".sha256"
	if err := os.WriteFile(shaFile, []byte("0000000000000000000000000000000000000000000000000000000000000000  file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err = RestoreBackupSmoke(context.Background(), backupPath, cipher)
	if err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("expected digest mismatch error, got: %v", err)
	}
	if err := os.Remove(shaFile); err != nil {
		t.Fatal(err)
	}
	err = RestoreBackupSmoke(context.Background(), backupPath, cipher)
	if err == nil || !strings.Contains(err.Error(), "read backup digest") {
		t.Fatalf("expected missing digest error, got: %v", err)
	}

	// 3. Test backup retention: create multiple backups and verify oldest pruned
	for i := 101; i <= 104; i++ {
		if err := db.createMigrationBackup(context.Background(), i); err != nil {
			t.Fatalf("create backup %d: %v", i, err)
		}
	}
	allEntries, _ := os.ReadDir(backupDir)
	encFiles := 0
	for _, e := range allEntries {
		if strings.HasSuffix(e.Name(), ".db.enc") {
			encFiles++
		}
	}
	if encFiles > 2 {
		t.Fatalf("expected at most 2 retained backups, got %d", encFiles)
	}
}

func TestMigration053ProviderFlowsShareContactSlot(t *testing.T) {
	ctx := context.Background()
	db := openDatabaseAtMigration(t, 52)
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO reset_announcements
		(id, provider, source, scope, confidence, original_text, registered_at_ms, created_at_ms)
		VALUES ('synthetic-announcement', 'claude', 'manual', 'all', 'confirmed', 'Synthetic reset notice', 1000, 1000)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var preservedCount int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*) FROM reset_announcements
		WHERE id = 'synthetic-announcement' AND original_text = 'Synthetic reset notice'`).Scan(&preservedCount); err != nil {
		t.Fatal(err)
	}
	if preservedCount != 1 {
		t.Fatalf("announcement count after upgrade = %d, want 1", preservedCount)
	}
	var count, distinctCount, maxVersion int
	if err := db.SQL.QueryRowContext(ctx, `SELECT COUNT(*), COUNT(DISTINCT version), MAX(version) FROM schema_migrations`).Scan(&count, &distinctCount, &maxVersion); err != nil {
		t.Fatal(err)
	}
	if count != 49 || distinctCount != 49 || maxVersion != 54 {
		t.Fatalf("migration ledger count/distinct/max = %d/%d/%d, want 49/49/54", count, distinctCount, maxVersion)
	}
	for i, p := range []string{"claude", "codex", "antigravity", "devin", "kimi", "kimi-ai", "xai", "meta"} {
		if _, err := db.SQL.ExecContext(ctx, "INSERT INTO reauth_jobs (id,auth_index,provider,account_label,flow,code_kind,state,created_at_ms,updated_at_ms) VALUES (?,?,?,?,?,?,'pending',?,?)", p, p, p, p, "device_code", "device", i, i); err != nil {
			t.Fatalf("provider %s: %v", p, err)
		}
	}
	for _, state := range []string{"oauth_started", "waiting_code", "waiting_browser", "waiting_callback", "waiting_device", "code_received", "oauth_submitted", "verifying"} {
		if _, err := db.SQL.ExecContext(ctx, "UPDATE reauth_jobs SET state=? WHERE id='claude'", state); err != nil {
			t.Fatal(err)
		}
		if _, err := db.SQL.ExecContext(ctx, "UPDATE reauth_jobs SET state='waiting_device' WHERE id='kimi'"); err == nil {
			t.Fatalf("contact index admitted concurrent %s and device", state)
		}
	}
	if _, err := db.SQL.ExecContext(ctx, "UPDATE reauth_jobs SET state='succeeded' WHERE id='claude'"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SQL.ExecContext(ctx, "UPDATE reauth_jobs SET state='waiting_device' WHERE id='kimi'"); err != nil {
		t.Fatal(err)
	}
}

func TestMigration054PreservesAutomationRoster(t *testing.T) {
	ctx := context.Background()
	db := openDatabaseAtMigration(t, 53)
	if _, err := db.SQL.ExecContext(ctx, `INSERT INTO account_automation_accounts (auth_index, is_adopted, cycle_reset_at_ms, cycle_key) VALUES ('synthetic', 1, 2000, 'synthetic-cycle')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var credit int
	var removed *int64
	var reset int64
	var cycle string
	if err := db.SQL.QueryRowContext(ctx, `SELECT burn_credit, removed_at_ms, cycle_reset_at_ms, cycle_key FROM account_automation_accounts WHERE auth_index = 'synthetic'`).Scan(&credit, &removed, &reset, &cycle); err != nil {
		t.Fatal(err)
	}
	if credit != 0 || removed != nil || reset != 2000 || cycle != "synthetic-cycle" {
		t.Fatalf("migration changed roster: credit=%d removed=%v reset=%d cycle=%s", credit, removed, reset, cycle)
	}
}
