package keyclient

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

// TEST_GATEWAY_KEY is a stand-in for a gateway key: shaped like one, obviously not one.
const TEST_GATEWAY_KEY = "sk-cpa-test-0123456789abcdef0123456789abcdef"

// installedRepo is a repository with a real key pair and a real envelope, ready to
// install.

type installedRepo struct {
	Record RepoState
	Key    string
}

// seedRepo creates a repository's key and seals TEST_GATEWAY_KEY into its envelope with
// repokey itself, so the tests exercise the same decryption the product uses.
func seedRepo(t *testing.T, home Home, repo string, gatewayKey string) installedRepo {
	t.Helper()
	generated, err := Generate(GenOptions{Home: home, Repo: repo, Now: time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := repokey.ParsePublicKey(generated.Pairs.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := repokey.Encrypt(publicKey, gatewayKey)
	if err != nil {
		t.Fatal(err)
	}
	envelopePath, err := home.EnvelopePath(generated.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envelopePath, []byte(envelope), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	return installedRepo{Record: generated.Record, Key: gatewayKey}
}

// installForTest runs one install against a temp config path.
func installForTest(t *testing.T, home Home, repo installedRepo, runtime Runtime, configPath string, replace bool) InstallResult {
	t.Helper()
	key, err := InstallKeyMaterial(home, repo.Record.Repo)
	if err != nil {
		t.Fatalf("read key material: %v", err)
	}
	result, err := Install(InstallOptions{
		Home:       home,
		Runtime:    runtime,
		Repo:       repo.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: configPath,
		Replace:    replace,
		Now:        time.Date(2026, 11, 17, 9, 15, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("install %s: %v", runtime, err)
	}
	return result
}

// readJSONFile decodes a file into a generic map for structural assertions.
func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(content, &decoded); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, content)
	}
	return decoded
}

// TestInstallClaudeWritesTheTwoEnvMembers is AC-5 for the first runtime: the two
// members the plan names are written, and every other member of the settings file — an
// unrelated env entry, permissions, model — is untouched.
func TestInstallClaudeWritesTheTwoEnvMembers(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	settings := filepath.Join(t.TempDir(), "settings.json")
	original := `{
  "permissions": {"allow": ["Bash(ls:*)"]},
  "env": {"OTHER": "kept"},
  "model": "sonnet"
}
`
	if err := os.WriteFile(settings, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}

	result := installForTest(t, home, repo, RuntimeClaude, settings, false)
	if result.ConfigPath != settings {
		t.Fatalf("config path = %q", result.ConfigPath)
	}

	decoded := readJSONFile(t, settings)
	env, ok := decoded["env"].(map[string]any)
	if !ok {
		t.Fatalf("env is not an object: %v", decoded["env"])
	}
	if env[CLAUDE_ENV_KEY] != TEST_GATEWAY_KEY {
		t.Fatalf("env.%s = %v", CLAUDE_ENV_KEY, env[CLAUDE_ENV_KEY])
	}
	if env[CLAUDE_ENV_BASE_URL] != "http://127.0.0.1:8317" {
		t.Fatalf("env.%s = %v", CLAUDE_ENV_BASE_URL, env[CLAUDE_ENV_BASE_URL])
	}
	if env["OTHER"] != "kept" {
		t.Fatal("install dropped an env member it does not own")
	}
	if decoded["model"] != "sonnet" {
		t.Fatal("install dropped the model member")
	}
	permissions, ok := decoded["permissions"].(map[string]any)
	if !ok || permissions["allow"] == nil {
		t.Fatal("install dropped the permissions member")
	}

	// Member order is preserved: an install must not reorder somebody's file.
	content, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(content), "{\n  \"permissions\"") {
		t.Fatalf("the first member moved:\n%s", content)
	}
}

// TestInstallClaudeRefusesAnExistingAPIKey covers the documented refusal: an
// ANTHROPIC_API_KEY can take precedence over the token, so install stops rather than
// leaving a file where the key looks installed but is not used.
func TestInstallClaudeRefusesAnExistingAPIKey(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	settings := filepath.Join(t.TempDir(), "settings.json")
	original := `{"env": {"ANTHROPIC_API_KEY": "sk-ant-something"}}`
	if err := os.WriteFile(settings, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}

	key, err := InstallKeyMaterial(home, repo.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeClaude,
		Repo:       repo.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: settings,
		Now:        time.Now(),
	})
	if CodeOf(err) != CODE_CONFIG_CONFLICT {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_CONFLICT, err)
	}
	if !strings.Contains(MessageOf(err), CLAUDE_ENV_API_KEY) {
		t.Fatalf("the message does not name the conflicting member: %q", MessageOf(err))
	}
	content, readErr := os.ReadFile(settings)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != original {
		t.Fatalf("a refused install changed the file:\n%s", content)
	}
	if entries, _ := os.ReadDir(home.BackupsDir()); len(entries) != 0 {
		t.Fatalf("a refused install left %d backups", len(entries))
	}
}

// TestInstallClaudeRejectsJSONC is the refusal for a file this tool cannot model. It is
// the JSONC case from the plan: comments are exactly what a decode/re-encode would
// silently delete.
func TestInstallClaudeRejectsJSONC(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	settings := filepath.Join(t.TempDir(), "settings.json")
	original := "{\n  // a comment\n  \"model\": \"sonnet\"\n}\n"
	if err := os.WriteFile(settings, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	key, err := InstallKeyMaterial(home, repo.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeClaude,
		Repo:       repo.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: settings,
		Now:        time.Now(),
	})
	if CodeOf(err) != CODE_CONFIG_NOT_JSON {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_NOT_JSON, err)
	}
	content, readErr := os.ReadFile(settings)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != original {
		t.Fatalf("a refused install rewrote the file:\n%s", content)
	}
}

// TestInstallIsIdempotent is the plan's repeat-install rule: a second run over the same
// key writes nothing, takes no second backup, and leaves one copy of each member.
func TestInstallIsIdempotent(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	settings := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(settings, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	first := installForTest(t, home, repo, RuntimeClaude, settings, false)
	afterFirst, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if first.BackupPath == "" {
		t.Fatal("the first install took no backup")
	}

	second := installForTest(t, home, repo, RuntimeClaude, settings, false)
	afterSecond, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Unchanged {
		t.Fatal("the second install reported a change")
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Fatalf("the second install changed the file:\n%s\n---\n%s", afterFirst, afterSecond)
	}
	entries, err := os.ReadDir(home.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("backups = %d, want the one the first install took", len(entries))
	}
	// The key still appears exactly once in each place.
	if strings.Count(string(afterSecond), TEST_GATEWAY_KEY) != 1 {
		t.Fatalf("the key appears %d times:\n%s", strings.Count(string(afterSecond), TEST_GATEWAY_KEY), afterSecond)
	}
}

// TestInstallBackupHoldsThePreviousFileExactly is the recovery guarantee: the backup is
// a byte-for-byte copy of what was there, so restoring it is lossless.
func TestInstallBackupHoldsThePreviousFileExactly(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	configDir := t.TempDir()
	config := filepath.Join(configDir, "opencode.json")
	original := []byte("{\n  \"provider\": {\"other\": {\"npm\": \"x\"}},\n  \"model\": \"keep-me\"\n}\n")
	if err := os.WriteFile(config, original, FILE_MODE); err != nil {
		t.Fatal(err)
	}
	result := installForTest(t, home, repo, RuntimeOpenCode, config, false)
	if !strings.HasPrefix(filepath.Base(result.BackupPath), "20261117T091500Z-") {
		t.Fatalf("backup name = %q, want a UTC timestamp prefix", filepath.Base(result.BackupPath))
	}
	if filepath.Dir(result.BackupPath) != home.BackupsDir() {
		t.Fatalf("backup is in %q, want %q", filepath.Dir(result.BackupPath), home.BackupsDir())
	}
	backed, err := os.ReadFile(result.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(backed, original) {
		t.Fatalf("the backup is not the previous file:\n%s", backed)
	}
	if bytes.Contains(backed, []byte(TEST_GATEWAY_KEY)) {
		t.Fatal("the backup of a file without the key contains the key")
	}
}

// TestInstallOpenCodeKeepsOtherProvidersAndWritesV1Shape covers the JSON structure the
// plan fixes: v1 singular `provider`, one new entry under it, no `models` map.
func TestInstallOpenCodeKeepsOtherProvidersAndWritesV1Shape(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "opencode.json")
	original := `{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "cpa": {"npm": "@ai-sdk/openai-compatible", "options": {"baseURL": "http://other/v1"}}
  },
  "theme": "dark"
}
`
	if err := os.WriteFile(config, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeOpenCode, config, false)

	decoded := readJSONFile(t, config)
	if _, hasV2 := decoded["providers"]; hasV2 {
		t.Fatal("install wrote the v2 plural key")
	}
	providers, ok := decoded["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider is not an object: %v", decoded["provider"])
	}
	other, ok := providers["cpa"].(map[string]any)
	if !ok {
		t.Fatal("install dropped the other provider")
	}
	otherOptions, ok := other["options"].(map[string]any)
	if !ok || otherOptions["baseURL"] != "http://other/v1" {
		t.Fatal("install changed the other provider's options")
	}
	installed, ok := providers[OPENCODE_PROVIDER_ID].(map[string]any)
	if !ok {
		t.Fatal("the oh-my-cpa provider is missing")
	}
	if installed["npm"] != OPENCODE_NPM {
		t.Fatalf("npm = %v", installed["npm"])
	}
	if installed["name"] != "Oh My CPA (org/repo)" {
		t.Fatalf("name = %v", installed["name"])
	}
	if _, hasModels := installed["models"]; hasModels {
		t.Fatal("install wrote a models map")
	}
	options, ok := installed["options"].(map[string]any)
	if !ok {
		t.Fatal("options is missing")
	}
	if options["apiKey"] != TEST_GATEWAY_KEY {
		t.Fatalf("options.apiKey = %v", options["apiKey"])
	}
	if options["baseURL"] != "http://127.0.0.1:8317/v1" {
		t.Fatalf("options.baseURL = %v", options["baseURL"])
	}
	if decoded["theme"] != "dark" {
		t.Fatal("install dropped an unrelated top-level member")
	}
}

// TestInstallCodexWritesAManagedBlockAndProvider covers the TOML side: the block, the
// top-level line, and the previous provider remembered for purge.
func TestInstallCodexWritesAManagedBlockAndProvider(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "config.toml")
	original := `# my codex settings
model = "gpt-5"
model_provider = "openai"

[model_providers.other]
name = "Other"
base_url = "https://other.example/v1"
`
	if err := os.WriteFile(config, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	result := installForTest(t, home, repo, RuntimeCodex, config, false)
	if result.PreviousModelProvider == nil || *result.PreviousModelProvider != "openai" {
		t.Fatalf("previous provider = %v", result.PreviousModelProvider)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, want := range []string{
		CODEX_BLOCK_START,
		CODEX_BLOCK_END,
		"[model_providers." + CODEX_PROVIDER_ID + "]",
		`wire_api = "responses"`,
		`experimental_bearer_token = "` + TEST_GATEWAY_KEY + `"`,
		`base_url = "http://127.0.0.1:8317/v1"`,
		`name = "Oh My CPA (org/repo)"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config.toml is missing %q:\n%s", want, text)
		}
	}
	// The untouched parts survive exactly, including the comment.
	if !strings.HasPrefix(text, "# my codex settings\nmodel = \"gpt-5\"\nmodel_provider = \"oh-my-cpa\"\n") {
		t.Fatalf("the top-level keys were not preserved in place:\n%s", text)
	}
	if !strings.Contains(text, "[model_providers.other]\nname = \"Other\"\nbase_url = \"https://other.example/v1\"") {
		t.Fatalf("the other provider table changed:\n%s", text)
	}
	if strings.Count(text, "model_provider = ") != 1 {
		t.Fatalf("model_provider appears %d times:\n%s", strings.Count(text, "model_provider = "), text)
	}
	if strings.Index(text, "model_provider = ") > strings.Index(text, "[model_providers."+CODEX_PROVIDER_ID) {
		t.Fatal("model_provider is after a table header, so it is not top-level")
	}
}

// TestInstallCodexRefusesAnUnmanagedProviderTable is the collision the plan names: a
// hand-written table with this tool's own name would make any append produce invalid
// TOML, so install stops instead.
func TestInstallCodexRefusesAnUnmanagedProviderTable(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "config.toml")
	original := "model = \"gpt-5\"\n\n[model_providers.oh-my-cpa]\nname = \"by hand\"\n"
	if err := os.WriteFile(config, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	key, err := InstallKeyMaterial(home, repo.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeCodex,
		Repo:       repo.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: config,
		Now:        time.Now(),
	})
	if CodeOf(err) != CODE_CONFIG_CONFLICT {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_CONFLICT, err)
	}
	content, readErr := os.ReadFile(config)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != original {
		t.Fatalf("a refused install rewrote the file:\n%s", content)
	}
}

// TestInstallCodexRefusesAHalfBlock is the interrupted-write case: a start marker with no
// end marker would otherwise let the scan delete everything from the marker to EOF.
func TestInstallCodexRefusesAHalfBlock(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "config.toml")
	original := CODEX_BLOCK_START + "\n[model_providers.oh-my-cpa]\nname = \"half\"\n"
	if err := os.WriteFile(config, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	key, err := InstallKeyMaterial(home, repo.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeCodex,
		Repo:       repo.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: config,
		Now:        time.Now(),
	})
	if CodeOf(err) != CODE_CONFIG_CONFLICT {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_CONFLICT, err)
	}
}

// TestInstallCodexRefusesMultiLineStrings covers the boundary the scanner will not
// guess: inside a multi-line string a line starting with "[" is text, not a table.
func TestInstallCodexRefusesMultiLineStrings(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "config.toml")
	original := "instructions = \"\"\"\n[model_providers.oh-my-cpa]\n\"\"\"\n"
	if err := os.WriteFile(config, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	key, err := InstallKeyMaterial(home, repo.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeCodex,
		Repo:       repo.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: config,
		Now:        time.Now(),
	})
	if CodeOf(err) != CODE_CONFIG_CONFLICT {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_CONFLICT, err)
	}
	content, readErr := os.ReadFile(config)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != original {
		t.Fatalf("the file changed:\n%s", content)
	}
}

// TestInstallRequiresReplaceForAForeignSlot is the ownership rule. A slot holding
// another repository's value is not overwritten by accident, and --replace is what says
// "yes, this one".
func TestInstallRequiresReplaceForAForeignSlot(t *testing.T) {
	home := newTestHome(t)
	first := seedRepo(t, home, "org/first", "sk-cpa-first-000000000000000000000000")
	second := seedRepo(t, home, "org/second", "sk-cpa-second-11111111111111111111111")
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, first, RuntimeClaude, config, false)

	key, err := InstallKeyMaterial(home, second.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeClaude,
		Repo:       second.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: config,
		Now:        time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC),
	})
	if CodeOf(err) != CODE_OWNED_BY_ANOTHER {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_OWNED_BY_ANOTHER, err)
	}
	if !strings.Contains(MessageOf(err), "org/first") {
		t.Fatalf("the message does not name the owner: %q", MessageOf(err))
	}
	decoded := readJSONFile(t, config)
	env, _ := decoded["env"].(map[string]any)
	if env[CLAUDE_ENV_KEY] != first.Key {
		t.Fatal("a refused install changed the slot")
	}

	// With --replace the slot changes and the old value lives only in the new backup.
	installForTest(t, home, second, RuntimeClaude, config, true)
	decoded = readJSONFile(t, config)
	env, _ = decoded["env"].(map[string]any)
	if env[CLAUDE_ENV_KEY] != second.Key {
		t.Fatalf("--replace did not take the slot: %v", env[CLAUDE_ENV_KEY])
	}
	entries, err := os.ReadDir(home.BackupsDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("backups = %d, want one per install", len(entries))
	}
	found := false
	for _, entry := range entries {
		content, readErr := os.ReadFile(filepath.Join(home.BackupsDir(), entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if bytes.Contains(content, []byte(first.Key)) {
			found = true
		}
	}
	if !found {
		t.Fatal("the replaced repository's value is not in any backup")
	}
}

// TestInstallRefusesToReplaceAValueItDidNotWrite covers a slot this tool never owned:
// state has no record, the file has a value, and --replace is the only way through.
func TestInstallRefusesToReplaceAValueItDidNotWrite(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	original := `{"env":{"ANTHROPIC_AUTH_TOKEN":"written-by-hand"}}`
	if err := os.WriteFile(config, []byte(original), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	key, err := InstallKeyMaterial(home, repo.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeClaude,
		Repo:       repo.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: config,
		Now:        time.Now(),
	})
	if CodeOf(err) != CODE_CONFIG_CONFLICT {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_CONFLICT, err)
	}
	content, readErr := os.ReadFile(config)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != original {
		t.Fatalf("a refused install changed the file:\n%s", content)
	}
}

// TestInstallNeedsKeyAndEnvelope is the "nothing is written without material" rule.
func TestInstallNeedsKeyAndEnvelope(t *testing.T) {
	home := newTestHome(t)
	config := filepath.Join(t.TempDir(), "settings.json")

	if _, err := InstallKeyMaterial(home, "org/missing"); CodeOf(err) != CODE_KEY_NOT_FOUND {
		t.Fatalf("code = %q, want %q", CodeOf(err), CODE_KEY_NOT_FOUND)
	}
	generated, err := Generate(GenOptions{Home: home, Repo: "org/noenvelope", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = InstallKeyMaterial(home, generated.Record.Repo)
	if CodeOf(err) != CODE_ENVELOPE_MISSING {
		t.Fatalf("code = %q, want %q", CodeOf(err), CODE_ENVELOPE_MISSING)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeClaude,
		Repo:       generated.Record,
		Key:        "sk-cpa-whatever-00000000000000000000",
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: config,
		Now:        time.Now(),
	})
	if err != nil {
		t.Fatalf("install with a supplied key failed: %v", err)
	}
	if _, statErr := os.Stat(config); statErr != nil {
		t.Fatalf("the config was not created: %v", statErr)
	}
}

// TestInstallRecordsTheSlot is the state contract: the record names the file, the
// fingerprint of the value and whether the backup already held the key.
func TestInstallRecordsTheSlot(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"`+TEST_GATEWAY_KEY+`"}}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	installForTest(t, home, repo, RuntimeClaude, config, false)

	store, err := OpenStore(home)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	slot, ok := store.Runtime(RuntimeClaude)
	if !ok {
		t.Fatal("no runtime record was written")
	}
	if slot.Repo != "org/repo" {
		t.Fatalf("slot repo = %q", slot.Repo)
	}
	if slot.ConfigPath != config {
		t.Fatalf("slot config path = %q, want %q", slot.ConfigPath, config)
	}
	if slot.ValueFingerprint != KeyValueFingerprint(TEST_GATEWAY_KEY) {
		t.Fatalf("slot fingerprint = %q", slot.ValueFingerprint)
	}
	if !slot.BackupContainsKey {
		t.Fatal("the backup already held this key, so backup_contains_key should be true")
	}
	if slot.BackupPath == "" {
		t.Fatal("the slot has no backup path")
	}
}

// TestRuntimeConfigPathHonoursTheRuntimeVariables is the difference between writing the
// file the runtime reads and writing one it ignores.
func TestRuntimeConfigPathHonoursTheRuntimeVariables(t *testing.T) {
	t.Setenv(ENV_CLAUDE_CONFIG_DIR, filepath.Join(t.TempDir(), "claude"))
	t.Setenv(ENV_CODEX_HOME, filepath.Join(t.TempDir(), "codex"))
	t.Setenv(ENV_OPENCODE_CONFIG, filepath.Join(t.TempDir(), "opencode.json"))

	cases := []struct {
		runtime Runtime
		want    string
	}{
		{RuntimeClaude, filepath.Join(os.Getenv(ENV_CLAUDE_CONFIG_DIR), CLAUDE_SETTINGS_FILE)},
		{RuntimeCodex, filepath.Join(os.Getenv(ENV_CODEX_HOME), CODEX_CONFIG_FILE)},
		{RuntimeOpenCode, os.Getenv(ENV_OPENCODE_CONFIG)},
	}
	for _, testCase := range cases {
		got, err := RuntimeConfigPath(testCase.runtime, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != testCase.want {
			t.Fatalf("%s: path = %q, want %q", testCase.runtime, got, testCase.want)
		}
	}
	explicit := filepath.Join(t.TempDir(), "explicit.json")
	got, err := RuntimeConfigPath(RuntimeClaude, explicit)
	if err != nil {
		t.Fatal(err)
	}
	if got != explicit {
		t.Fatalf("--config was ignored: %q", got)
	}
}

// TestRuntimeConfigPathDefaultsToTheUserLevelFile is the layering rule that keeps a key
// out of a repository: the default is under the user's home, never under the working
// directory.
func TestRuntimeConfigPathDefaultsToTheUserLevelFile(t *testing.T) {
	t.Setenv(ENV_CLAUDE_CONFIG_DIR, "")
	t.Setenv(ENV_CODEX_HOME, "")
	t.Setenv(ENV_OPENCODE_CONFIG, "")
	userHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	wants := map[Runtime]string{
		RuntimeClaude:   filepath.Join(userHome, CLAUDE_CONFIG_DIRNAME, CLAUDE_SETTINGS_FILE),
		RuntimeCodex:    filepath.Join(userHome, CODEX_CONFIG_DIRNAME, CODEX_CONFIG_FILE),
		RuntimeOpenCode: filepath.Join(userHome, OPENCODE_CONFIG_DIR, OPENCODE_CONFIG_FILE),
	}
	for runtime, want := range wants {
		got, err := RuntimeConfigPath(runtime, "")
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s: path = %q, want %q", runtime, got, want)
		}
	}
}

// TestInstallRefusesASymlinkedConfig keeps a dotfiles-managed settings file out of
// reach: writing through the link would put the plaintext key into whatever repository
// the link points at.
func TestInstallRefusesASymlinkedConfig(t *testing.T) {
	home := newTestHome(t)
	repo := seedRepo(t, home, "org/repo", TEST_GATEWAY_KEY)
	real := filepath.Join(t.TempDir(), "real.json")
	if err := os.WriteFile(real, []byte(`{"model":"sonnet"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "settings.json")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("this platform does not allow creating a symlink without extra rights: %v", err)
	}
	key, err := InstallKeyMaterial(home, repo.Record.Repo)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Install(InstallOptions{
		Home:       home,
		Runtime:    RuntimeClaude,
		Repo:       repo.Record,
		Key:        key,
		BaseURL:    "http://127.0.0.1:8317",
		ConfigPath: link,
		Now:        time.Now(),
	})
	if CodeOf(err) != CODE_CONFIG_CONFLICT {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_CONFLICT, err)
	}
	content, readErr := os.ReadFile(real)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if bytes.Contains(content, []byte(TEST_GATEWAY_KEY)) {
		t.Fatal("install wrote the key through the link")
	}
}

// TestInstallVerificationRepairsAFileThatLostContent is the rollback guarantee. The
// check runs on the file as it is after the write, so a file that lost a member is
// refused, and the restore puts the bytes back exactly.
func TestInstallVerificationRepairsAFileThatLostContent(t *testing.T) {
	original := []byte("{\n  \"permissions\": {\"allow\": []},\n  \"model\": \"sonnet\"\n}\n")
	// What a write that dropped the permissions member would have left behind.
	dropped := []byte("{\n  \"model\": \"sonnet\",\n  \"env\": {\"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:8317\", \"ANTHROPIC_AUTH_TOKEN\": \"" + TEST_GATEWAY_KEY + "\"}\n}\n")

	configDir := t.TempDir()
	config := filepath.Join(configDir, "settings.json")
	if err := os.WriteFile(config, dropped, FILE_MODE); err != nil {
		t.Fatal(err)
	}
	record := RepoState{Repo: "org/repo"}
	err := verifyRuntimeEdit(RuntimeClaude, original, config, record, "http://127.0.0.1:8317", TEST_GATEWAY_KEY)
	if err == nil {
		t.Fatal("verification accepted a file that lost a member")
	}
	if !strings.Contains(err.Error(), "permissions") {
		t.Fatalf("the failure does not name the lost member: %v", err)
	}

	backup := filepath.Join(configDir, "backup.json")
	if err := os.WriteFile(backup, original, FILE_MODE); err != nil {
		t.Fatal(err)
	}
	if err := restoreConfig(config, original, true, backup); err != nil {
		t.Fatal(err)
	}
	restored, readErr := os.ReadFile(config)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !bytes.Equal(restored, original) {
		t.Fatalf("the restore is not the previous file:\n%s", restored)
	}
}

// TestInstallVerificationReportsAnotherRepositorysValue proves the check is on this
// repository's key, not merely on "some value is present".
func TestInstallVerificationReportsAnotherRepositorysValue(t *testing.T) {
	content := []byte("{\"env\":{\"ANTHROPIC_BASE_URL\":\"http://127.0.0.1:8317\",\"ANTHROPIC_AUTH_TOKEN\":\"someone-elses\"}}")
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, content, FILE_MODE); err != nil {
		t.Fatal(err)
	}
	err := verifyRuntimeEdit(RuntimeClaude, nil, config, RepoState{Repo: "org/repo"}, "http://127.0.0.1:8317", TEST_GATEWAY_KEY)
	if err == nil {
		t.Fatal("verification accepted a file that holds another value")
	}
}
