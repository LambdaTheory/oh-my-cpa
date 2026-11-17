package keyclient

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// purgeAt is a fixed instant, so a test that compares file lists never depends on the
// clock.
var purgeAt = time.Date(2026, 11, 17, 11, 0, 0, 0, time.UTC)

// seedInstalledRepo is the common setup: a repository with key, envelope and one
// installed runtime, all under the temp home.
func seedInstalledRepo(t *testing.T, home Home, repo, gatewayKey string, runtime Runtime, configPath string) installedRepo {
	t.Helper()
	seeded := seedRepo(t, home, repo, gatewayKey)
	if err := os.MkdirAll(filepath.Dir(configPath), DIR_MODE); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.WriteFile(configPath, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
			t.Fatal(err)
		}
	}
	installForTest(t, home, seeded, runtime, configPath, true)
	return seeded
}

// TestPurgeRemovesEverythingOfOneRepository is AC-6's first claim and C-31's isolation
// requirement: this repository's files go, the other's stay byte-identical, and the
// record goes last.
func TestPurgeRemovesEverythingOfOneRepository(t *testing.T) {
	home := newTestHome(t)
	target := seedRepo(t, home, "org/target", TEST_GATEWAY_KEY)
	other := seedRepo(t, home, "org/other", "sk-cpa-other-2222222222222222222222222")
	configPath := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(configPath, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, target, RuntimeClaude, configPath, false)

	targetKey, _ := home.KeyPath(target.Record.Repo)
	targetEnvelope, _ := home.EnvelopePath(target.Record.Repo)
	otherKey, _ := home.KeyPath(other.Record.Repo)
	otherEnvelope, _ := home.EnvelopePath(other.Record.Repo)
	otherKeyBefore, err := os.ReadFile(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	otherEnvelopeBefore, err := os.ReadFile(otherEnvelope)
	if err != nil {
		t.Fatal(err)
	}

	result, err := Purge(PurgeOptions{Repo: target.Record.Repo, Home: home, Now: purgeAt})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	for _, path := range []string{targetKey, targetEnvelope} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("%s survived purge (err=%v)", path, statErr)
		}
	}
	if !containsPath(result.Removed, targetKey) || !containsPath(result.Removed, targetEnvelope) {
		t.Fatalf("the report does not name the removed files: %v", result.Removed)
	}
	if _, statErr := os.Stat(otherKey); statErr != nil {
		t.Fatalf("the other repository's key is gone: %v", statErr)
	}
	otherKeyAfter, _ := os.ReadFile(otherKey)
	if !bytes.Equal(otherKeyBefore, otherKeyAfter) {
		t.Fatal("the other repository's key changed")
	}
	otherEnvelopeAfter, _ := os.ReadFile(otherEnvelope)
	if !bytes.Equal(otherEnvelopeBefore, otherEnvelopeAfter) {
		t.Fatal("the other repository's envelope changed")
	}

	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok := store.Repo(target.Record.Repo); ok {
		t.Fatal("the state record survived")
	}
	if _, ok := store.Repo(other.Record.Repo); !ok {
		t.Fatal("the other repository's record was removed")
	}
	if _, ok := store.Runtime(RuntimeClaude); ok {
		t.Fatal("the runtime slot survived")
	}
	// The installed value is gone from the file, and the unrelated member is not.
	decoded := readJSONFile(t, configPath)
	if decoded["model"] != "sonnet" {
		t.Fatal("purge changed an unrelated member")
	}
	if _, hasEnv := decoded["env"]; hasEnv {
		t.Fatalf("the emptied env object survived: %v", decoded["env"])
	}
}

// TestPurgeLeavesOtherEnvEntriesAndProviders is C-28/C-32 on the purge side: a shared
// file keeps everything this tool did not write.
func TestPurgeLeavesOtherEnvEntriesAndProviders(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)

	claudeConfig := filepath.Join(t.TempDir(), "settings.json")
	claudeOriginal := `{
  "env": {"OTHER": "kept"},
  "model": "sonnet"
}
`
	if err := os.WriteFile(claudeConfig, []byte(claudeOriginal), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeClaude, claudeConfig, false)

	openCodeConfig := filepath.Join(t.TempDir(), "opencode.json")
	openCodeOriginal := `{"provider":{"cpa":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://other/v1"}}}}`
	if err := os.WriteFile(openCodeConfig, []byte(openCodeOriginal), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeOpenCode, openCodeConfig, false)

	codexConfig := filepath.Join(t.TempDir(), "config.toml")
	codexOriginal := "model = \"gpt-5\"\n\n[model_providers.other]\nname = \"Other\"\nbase_url = \"https://other.example/v1\"\n"
	if err := os.WriteFile(codexConfig, []byte(codexOriginal), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeCodex, codexConfig, false)

	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}

	claudeAfter, err := os.ReadFile(claudeConfig)
	if err != nil {
		t.Fatal(err)
	}
	claudeDecoded := readJSONFile(t, claudeConfig)
	env, ok := claudeDecoded["env"].(map[string]any)
	if !ok || env["OTHER"] != "kept" {
		t.Fatalf("claude's other env entry did not survive:\n%s", claudeAfter)
	}
	if strings.Contains(string(claudeAfter), TEST_GATEWAY_KEY) {
		t.Fatal("claude's file still holds the key")
	}
	if claudeDecoded["model"] != "sonnet" {
		t.Fatal("claude's model was dropped")
	}

	openCodeDecoded := readJSONFile(t, openCodeConfig)
	providers, ok := openCodeDecoded["provider"].(map[string]any)
	if !ok {
		t.Fatalf("opencode's provider object is gone:\n%v", openCodeDecoded)
	}
	if _, hasOurs := providers[OPENCODE_PROVIDER_ID]; hasOurs {
		t.Fatal("the oh-my-cpa provider survived purge")
	}
	if _, hasOther := providers["cpa"]; !hasOther {
		t.Fatal("the other provider was removed")
	}

	codexAfter, err := os.ReadFile(codexConfig)
	if err != nil {
		t.Fatal(err)
	}
	if string(codexAfter) != codexOriginal {
		t.Fatalf("codex's config did not return to its previous state:\n---got---\n%s\n---want---\n%s", codexAfter, codexOriginal)
	}
}

// TestPurgeRestoresThePreviousModelProvider is the specific undo C-32 asks for: the
// top-level line comes back with what was there, not merely disappearing.
func TestPurgeRestoresThePreviousModelProvider(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config, []byte("model = \"gpt-5\"\nmodel_provider = \"openai\"\n"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeCodex, config, false)
	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `model_provider = "openai"`) {
		t.Fatalf("the previous provider was not restored:\n%s", content)
	}
	if strings.Contains(string(content), CODEX_PROVIDER_ID) {
		t.Fatalf("this tool's provider is still mentioned:\n%s", content)
	}
}

// TestPurgeRemovesTheProviderLineWhenThereWasNone closes the other half of that rule.
func TestPurgeRemovesTheProviderLineWhenThereWasNone(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "config.toml")
	original := "model = \"gpt-5\"\n\n[model_providers.other]\nname = \"Other\"\n"
	if err := os.WriteFile(config, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeCodex, config, false)
	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), CODEX_PROVIDER_ID) {
		t.Fatalf("the provider line survived:\n%s", content)
	}
	if string(content) != original {
		t.Fatalf("the file did not return to its original shape:\n%s", content)
	}
}

// TestPurgeSingleRuntimeKeepsTheKey is the --runtime contract: only the named runtime's
// value goes, and the material stays because the repository is still installed
// elsewhere.
func TestPurgeSingleRuntimeKeepsTheKey(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	claudeConfig := filepath.Join(t.TempDir(), "settings.json")
	openCodeConfig := filepath.Join(t.TempDir(), "opencode.json")
	for _, path := range []string{claudeConfig, openCodeConfig} {
		if err := os.WriteFile(path, []byte(`{}`), FILE_MODE); err != nil {
			t.Fatal(err)
		}
	}
	installForTest(t, home, repo, RuntimeClaude, claudeConfig, false)
	installForTest(t, home, repo, RuntimeOpenCode, openCodeConfig, false)

	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Runtime: RuntimeClaude, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}
	if hasKey, _ := HasPrivateKey(home, repo.Record.Repo); !hasKey {
		t.Fatal("a --runtime run removed the private key")
	}
	if hasEnvelope, _ := HasEnvelope(home, repo.Record.Repo); !hasEnvelope {
		t.Fatal("a --runtime run removed the envelope")
	}
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok := store.Repo(repo.Record.Repo); !ok {
		t.Fatal("a --runtime run removed the record")
	}
	if _, ok := store.Runtime(RuntimeClaude); ok {
		t.Fatal("the named runtime's slot survived")
	}
	if _, ok := store.Runtime(RuntimeOpenCode); !ok {
		t.Fatal("the other runtime's slot was removed")
	}
	claudeDecoded := readJSONFile(t, claudeConfig)
	if _, hasEnv := claudeDecoded["env"]; hasEnv {
		t.Fatalf("the named runtime still holds a value: %v", claudeDecoded)
	}
	openCodeDecoded := readJSONFile(t, openCodeConfig)
	providers, _ := openCodeDecoded["provider"].(map[string]any)
	if _, ok := providers[OPENCODE_PROVIDER_ID]; !ok {
		t.Fatal("the unnamed runtime lost its value")
	}
}

// TestPurgeDryRunChangesNothing is the plan's explicit promise: a dry run reads, decides
// and prints, and no file moves.
func TestPurgeDryRunChangesNothing(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeClaude, config, false)

	before := snapshot(t, home)
	configBefore, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}

	result, err := Purge(PurgeOptions{Repo: repo.Record.Repo, DryRun: true, Home: home, Now: purgeAt})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(result.Removed) == 0 {
		t.Fatal("the dry run reported nothing to remove")
	}
	if !containsPath(result.Removed, config) {
		t.Fatalf("the dry run did not list the config value: %v", result.Removed)
	}

	after := snapshot(t, home)
	if diff := diffSnapshots(before, after); diff != "" {
		t.Fatalf("the dry run changed the home:\n%s", diff)
	}
	configAfter, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configBefore, configAfter) {
		t.Fatal("the dry run changed the configuration file")
	}
}

// TestPurgeDryRunReportsWithoutAHome is the read-only promise: asking about a machine with
// no home must not create one.
func TestPurgeDryRunReportsWithoutAHome(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "absent")}
	result, err := Purge(PurgeOptions{Repo: "org/repo", DryRun: true, Home: home, Now: purgeAt})
	if err != nil {
		t.Fatalf("dry run against a missing home: %v", err)
	}
	if len(result.Removed) != 0 || len(result.Pending) != 0 {
		t.Fatalf("result = %+v, want nothing", result)
	}
	if _, statErr := os.Stat(home.Root); !os.IsNotExist(statErr) {
		t.Fatalf("the dry run created the home (err=%v)", statErr)
	}
}

// TestPurgeRefusesAValueItDoesNotOwn is the install_not_owned path: a value changed after
// the install is reported and kept, and the run says so rather than finishing silently.
func TestPurgeRefusesAValueItDoesNotOwn(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeClaude, config, false)

	// Somebody switches the file to a different key after the install.
	replaced := `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8317","ANTHROPIC_AUTH_TOKEN":"sk-cpa-somebody-else-entirely-000"}}`
	if err := os.WriteFile(config, []byte(replaced), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	_, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt})
	if CodeOf(err) != CODE_NOT_OWNED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_NOT_OWNED, err)
	}
	content, readErr := os.ReadFile(config)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != replaced {
		t.Fatalf("a refused purge changed the file:\n%s", content)
	}
}

// TestPurgeKeepsEverythingWhenARuntimeCannotBeRead is the "state last, nothing lost on
// failure" rule: a run that stops at the first runtime removes neither the record nor the
// key, so the operator can fix the file and run it again.
func TestPurgeKeepsEverythingWhenARuntimeCannotBeRead(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeClaude, config, false)
	// A directory where the configuration file should be is a file this tool cannot
	// read, whatever account it runs as.
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(config, DIR_MODE); err != nil {
		t.Fatal(err)
	}

	result, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt})
	if err == nil {
		t.Fatalf("purge succeeded although the configuration could not be read; result=%+v", result)
	}
	if CodeOf(err) != CODE_CONFIG_NOT_JSON {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_NOT_JSON, err)
	}
	if hasKey, _ := HasPrivateKey(home, repo.Record.Repo); !hasKey {
		t.Fatal("the key was removed although the install is still in place")
	}
	store, openErr := OpenStore(home)
	if openErr != nil {
		t.Fatal(openErr)
	}
	defer store.Close()
	if _, ok := store.Repo(repo.Record.Repo); !ok {
		t.Fatal("the record was deleted although the value is still installed")
	}
	if _, ok := store.Runtime(RuntimeClaude); !ok {
		t.Fatal("the runtime slot was deleted although the value is still installed")
	}
}

// TestPurgeIsIdempotent is C-33's repeat requirement: a second purge is not a failure.
func TestPurgeIsIdempotent(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeClaude, config, false)
	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}
	result, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt})
	if err != nil {
		t.Fatalf("the repeat purge failed: %v", err)
	}
	if len(result.Pending) != 0 {
		t.Fatalf("the repeat purge is pending: %v", result.Pending)
	}
}

// TestPurgeRefusesAnInvalidRepoBeforeTouchingFiles is C-35's path-safety claim.
func TestPurgeRefusesAnInvalidRepoBeforeTouchingFiles(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	before := snapshot(t, home)
	for _, value := range []string{"", "   ", "..", "."} {
		if _, err := Purge(PurgeOptions{Repo: value, Home: home, Now: purgeAt}); err == nil {
			t.Fatalf("%q was accepted", value)
		}
	}
	// A traversal-looking value normalizes to a local directory identity rather than an
	// escape: every path is Home.Root plus a hashed filename, which the slug cannot leave.
	// A dry run proves where the resolved paths land without deleting anything.
	if _, err := Purge(PurgeOptions{Repo: "../../../etc/passwd", DryRun: true, Home: home, Now: purgeAt}); err != nil {
		t.Logf("the traversal-shaped identity was refused: %v", err)
	}
	traversalID, _, err := DeriveRepoID("../../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	keyPath, err := home.KeyPath(traversalID)
	if err != nil {
		t.Fatal(err)
	}
	if relative, relErr := filepath.Rel(home.Root, keyPath); relErr != nil || strings.HasPrefix(relative, "..") {
		t.Fatalf("the key path for %q escapes the home: %q", traversalID, keyPath)
	}
	after := snapshot(t, home)
	if diff := diffSnapshots(before, after); diff != "" {
		t.Fatalf("a refused purge changed the home:\n%s", diff)
	}
	if hasKey, _ := HasPrivateKey(home, repo.Record.Repo); !hasKey {
		t.Fatal("a refused purge removed the key")
	}
}

// TestPurgeRemovesOnlyBackupsThatHoldTheKey is C-32's backup rule: this repository's
// backup goes, and a backup of somebody else's file stays with its contents intact.
func TestPurgeRemovesOnlyBackupsThatHoldTheKey(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	result := installForTest(t, home, repo, RuntimeClaude, config, false)
	if result.BackupPath == "" {
		t.Fatal("no backup was taken")
	}
	backupBefore, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	// A backup of an unrelated file, in the same directory as every real backup.
	foreign := filepath.Join(home.BackupsDir(), "20260101T000000Z-unrelated-settings.json")
	foreignContent := []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"sk-cpa-foreign-333333333333333333"}}`)
	if err := os.WriteFile(foreign, foreignContent, FILE_MODE); err != nil {
		t.Fatal(err)
	}
	// A backup that does hold this repository's key, as a later install would leave it.
	mine := filepath.Join(home.BackupsDir(), "20261118T000000Z-later-settings.json")
	if err := os.WriteFile(mine, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"`+TEST_GATEWAY_KEY+`"}}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}

	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}
	// The install's own backup held the file as it was before the install, which did not
	// contain this key: it is somebody's restore point and stays.
	if keptBackup, statErr := os.ReadFile(result.BackupPath); statErr != nil {
		t.Fatalf("the install's backup was removed: %v", statErr)
	} else if !bytes.Equal(keptBackup, backupBefore) {
		t.Fatal("the install's backup changed")
	}
	if _, statErr := os.Stat(mine); !os.IsNotExist(statErr) {
		t.Fatalf("%s holds the key and should be gone (err=%v)", mine, statErr)
	}
	kept, err := os.ReadFile(foreign)
	if err != nil {
		t.Fatalf("the unrelated backup was removed: %v", err)
	}
	if !bytes.Equal(kept, foreignContent) {
		t.Fatal("the unrelated backup changed")
	}
}

// TestPurgeWithoutTheKeyKeepsUnprovableBackups is the documented degradation: without the
// private key a backup's content cannot be checked, so only the recorded backup goes and
// the rest stay with a reason.
func TestPurgeWithoutTheKeyKeepsUnprovableBackups(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	result := installForTest(t, home, repo, RuntimeClaude, config, false)
	unknown := filepath.Join(home.BackupsDir(), "20261119T000000Z-mystery-settings.json")
	if err := os.WriteFile(unknown, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"`+TEST_GATEWAY_KEY+`"}}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	// The key is lost before the purge, which is the case the degradation exists for.
	keyPath, err := home.KeyPath(repo.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(keyPath); err != nil {
		t.Fatal(err)
	}

	purgeResult, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if !purgeResult.BackupScanUnavailable {
		t.Fatal("the result does not report that the backup scan was unavailable")
	}
	if _, statErr := os.Stat(result.BackupPath); !os.IsNotExist(statErr) {
		t.Fatalf("the recorded backup of this repository should be gone (err=%v)", statErr)
	}
	if _, statErr := os.Stat(unknown); statErr != nil {
		t.Fatalf("the unprovable backup was removed anyway: %v", statErr)
	}
	keptNamed := false
	for _, note := range purgeResult.Kept {
		if strings.Contains(note, unknown) {
			keptNamed = true
		}
	}
	if !keptNamed {
		t.Fatalf("the kept backup is not named in the report: %v", purgeResult.Kept)
	}
}

// TestPurgeRemovesTheRecordButKeepsThePendingOne covers what state may and may not lose.
func TestPurgeRemovesTheRecordButKeepsThePendingOne(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, ok := store.Repo(repo.Record.Repo); ok {
		t.Fatal("the record survived a complete purge")
	}
	if len(store.Repos()) != 0 {
		t.Fatalf("other records appeared: %v", store.Repos())
	}
}

// containsPath reports whether the list names the path.
func containsPath(list []string, path string) bool {
	for _, entry := range list {
		if entry == path || strings.HasPrefix(entry, path+" ") {
			return true
		}
	}
	return false
}

// snapshot records every path under root with its size, so a test can prove a run changed
// nothing at all.
func snapshot(t *testing.T, home Home) map[string]int64 {
	t.Helper()
	files := map[string]int64{}
	err := filepath.Walk(home.Root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if info.IsDir() {
			files[path+"/"] = 0
			return nil
		}
		files[path] = info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files
}

// diffSnapshots renders the difference between two snapshots, or "" when they match.
func diffSnapshots(before, after map[string]int64) string {
	var builder strings.Builder
	for path, size := range before {
		if afterSize, ok := after[path]; !ok {
			builder.WriteString("removed: " + path + "\n")
		} else if afterSize != size {
			builder.WriteString("resized: " + path + "\n")
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			builder.WriteString("added: " + path + "\n")
		}
	}
	return builder.String()
}

// TestPurgeRemovesTheProviderLineWhenTheBlockWasDeletedByHand is the half-finished-cleanup
// case: the block went, the line stayed, and the file still selects a provider that no
// longer exists.
func TestPurgeRemovesTheProviderLineWhenTheBlockWasDeletedByHand(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config, []byte("model = \"gpt-5\"\n"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeCodex, config, false)
	// Delete the managed block by hand, as somebody tidying the file might.
	document, err := parseTOML(mustReadFile(t, config))
	if err != nil {
		t.Fatal(err)
	}
	start, end, found, err := document.blockMarkerPositions()
	if err != nil || !found {
		t.Fatalf("the block was not found: %v", err)
	}
	document.removeBlock(start, end)
	if err := os.WriteFile(config, document.encode(), FILE_MODE); err != nil {
		t.Fatal(err)
	}

	if _, err := Purge(PurgeOptions{Repo: repo.Record.Repo, Home: home, Now: purgeAt}); err != nil {
		t.Fatal(err)
	}
	content := mustReadFile(t, config)
	if strings.Contains(string(content), CODEX_PROVIDER_ID) {
		t.Fatalf("the provider line survived after the block was gone:\n%s", content)
	}
	if strings.Contains(string(content), CODEX_MODEL_PROVIDER) {
		t.Fatalf("a model_provider line survived:\n%s", content)
	}
}

// mustReadFile reads a test fixture or fails the test.
func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
