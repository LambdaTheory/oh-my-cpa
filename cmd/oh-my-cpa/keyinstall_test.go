package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

// commandTestGatewayKey is a stand-in gateway key for the command-layer tests.
const commandTestGatewayKey = "sk-cpa-command-test-0123456789abcdef012345"

// prepareKeyedRepo makes a home with a key and a real envelope, so a command test can run
// the whole path instead of stubbing it.
func prepareKeyedRepo(t *testing.T, home, repo, gatewayKey string) {
	t.Helper()
	code, stdout, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", repo)
	if code != keyclient.EXIT_OK {
		t.Fatalf("gen: exit = %d (stderr=%s)", code, stderr)
	}
	publicKey, err := repokey.ParsePublicKey(strings.TrimSpace(stdout))
	if err != nil {
		t.Fatalf("parse the public key: %v", err)
	}
	envelope, err := repokey.Encrypt(publicKey, gatewayKey)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := keyclient.ResolveHome(home)
	if err != nil {
		t.Fatal(err)
	}
	repoID, _, err := keyclient.DeriveRepoID(repo)
	if err != nil {
		t.Fatal(err)
	}
	envelopePath, err := resolved.EnvelopePath(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envelopePath, []byte(envelope), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKeyInstallHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		code, stdout, stderr := runKeyCommand(t, "install", flag)
		if code != keyclient.EXIT_OK {
			t.Fatalf("%s: exit = %d (stderr=%s)", flag, code, stderr)
		}
		for _, phrase := range []string{"--runtime", "--replace", "--config", "--models", "settings.json", "config.toml", "opencode.json"} {
			if !strings.Contains(stdout, phrase) {
				t.Fatalf("%s: help is missing %q:\n%s", flag, phrase, stdout)
			}
		}
	}
}

// TestKeyInstallWarnsAndWrites is AC-5 at the command layer: the warning reaches stderr,
// stdout says what was written where, and the file really holds it.
func TestKeyInstallWarnsAndWrites(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "claude", "--config", config, "--base-url", "http://127.0.0.1:8317")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	for _, phrase := range []string{"warning:", "plaintext", config, "restart", "key purge --repo org/repo"} {
		if !strings.Contains(stderr, phrase) {
			t.Fatalf("the warning is missing %q:\n%s", phrase, stderr)
		}
	}
	if !strings.Contains(stdout, "installed claude") || !strings.Contains(stdout, config) {
		t.Fatalf("stdout = %q", stdout)
	}
	// The output names no internal shorthand a reader outside the session would not know.
	for _, unwanted := range []string{"S4a", "T5-S8", "AC-5"} {
		if strings.Contains(stderr, unwanted) || strings.Contains(stdout, unwanted) {
			t.Fatalf("the output leaks %q", unwanted)
		}
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), commandTestGatewayKey) {
		t.Fatalf("the key was not written:\n%s", content)
	}
}

func TestKeyInstallRequiresRuntime(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	code, stdout, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_RUNTIME_UNKNOWN) {
		t.Fatalf("stderr should lead with the code: %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
}

func TestKeyInstallRejectsAnUnknownRuntime(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, _, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo", "--runtime", "vim")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_RUNTIME_UNKNOWN) {
		t.Fatalf("stderr = %q", stderr)
	}
}

// TestKeyInstallWithoutAKeyIsNotFound is the exit code the plan fixes: 4, with the command
// that would create the key.
func TestKeyInstallWithoutAKeyIsNotFound(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	config := filepath.Join(t.TempDir(), "settings.json")
	code, _, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "claude", "--config", config, "--base-url", "http://127.0.0.1:8317")
	if code != keyclient.EXIT_NOT_FOUND {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_NOT_FOUND, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_KEY_NOT_FOUND) || !strings.Contains(stderr, "key gen") {
		t.Fatalf("stderr = %q", stderr)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("a failed install created the configuration file")
	}
}

func TestKeyInstallWithoutAnEnvelopeIsNotFound(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo"); code != keyclient.EXIT_OK {
		t.Fatalf("gen: exit = %d (stderr=%s)", code, stderr)
	}
	config := filepath.Join(t.TempDir(), "settings.json")
	code, _, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "claude", "--config", config, "--base-url", "http://127.0.0.1:8317")
	if code != keyclient.EXIT_NOT_FOUND {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_NOT_FOUND, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_ENVELOPE_MISSING) {
		t.Fatalf("stderr = %q", stderr)
	}
}

// TestKeyInstallConflictIsExitFive keeps the conflict class distinct from a failure: a
// slot this tool does not own is a decision, not a broken machine.
func TestKeyInstallConflictIsExitFive(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"hand-written"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "claude", "--config", config, "--base-url", "http://127.0.0.1:8317")
	if code != keyclient.EXIT_CONFLICT {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_CONFLICT, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_CONFIG_CONFLICT) || !strings.Contains(stderr, "--replace") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestKeyInstallJSONCIsAnOperationalFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "settings.json")
	original := "{\n  // comment\n  \"model\": \"sonnet\"\n}\n"
	if err := os.WriteFile(config, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "claude", "--config", config, "--base-url", "http://127.0.0.1:8317")
	if code != keyclient.EXIT_FAILED {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_FAILED, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_CONFIG_NOT_JSON) {
		t.Fatalf("stderr = %q", stderr)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Fatalf("the file changed:\n%s", content)
	}
}

// TestKeyInstallRemembersTheBaseURL keeps one address for the whole flow: the flag is
// stored, so `key exec` and a later install agree without repeating it.
func TestKeyInstallRemembersTheBaseURL(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "settings.json")
	code, _, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "claude", "--config", config, "--base-url", "http://127.0.0.1:9000")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	stored, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "http://127.0.0.1:9000") {
		t.Fatalf("config.json = %s", stored)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"ANTHROPIC_BASE_URL": "http://127.0.0.1:9000"`) {
		t.Fatalf("the gateway root is not what was written:\n%s", content)
	}
}

func TestKeyInstallSecondRunSaysNothingChanged(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "settings.json")
	arguments := []string{"install", "--home", home, "--repo", "org/repo", "--runtime", "codex",
		"--config", config, "--base-url", "http://127.0.0.1:8317"}
	if code, _, stderr := runKeyCommand(t, arguments...); code != keyclient.EXIT_OK {
		t.Fatalf("first install: exit = %d (stderr=%s)", code, stderr)
	}
	code, stdout, stderr := runKeyCommand(t, arguments...)
	if code != keyclient.EXIT_OK {
		t.Fatalf("second install: exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "nothing changed") {
		t.Fatalf("stdout = %q", stdout)
	}
	if strings.Contains(stderr, "warning:") {
		t.Fatalf("a run that wrote nothing still warned:\n%s", stderr)
	}
}

func TestKeyInstallReportsTheReplacedCodexProvider(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(config, []byte("model_provider = \"openai\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "codex", "--config", config, "--base-url", "http://127.0.0.1:8317")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "openai") || !strings.Contains(stdout, "purge") {
		t.Fatalf("stdout does not say what it replaced and how to undo it:\n%s", stdout)
	}
}

// closedGatewayAddress returns the address of a server that has just been shut down, so
// a request to it fails the way an unreachable gateway does instead of waiting.
func closedGatewayAddress(t *testing.T) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {}))
	address := server.URL
	server.Close()
	return address
}

// TestKeyInstallOpenCodeWithoutModelsReportsTheGatewayFailure is the refusal the task
// asks for: no config is written when the model list cannot be read, and the message
// names both the failure and --models.
func TestKeyInstallOpenCodeWithoutModelsReportsTheGatewayFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "opencode.json")
	code, stdout, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "opencode", "--config", config, "--base-url", closedGatewayAddress(t))
	if code != keyclient.EXIT_FAILED {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_FAILED, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_MODELS_UNAVAILABLE) {
		t.Fatalf("stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "--models") {
		t.Fatalf("the failure does not name the way forward: %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("a failed install created the configuration file")
	}
}

// TestKeyInstallOpenCodeUsesTheExplicitModelList is the offline path: --models skips
// the gateway request that the address above cannot answer.
func TestKeyInstallOpenCodeUsesTheExplicitModelList(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "opencode.json")
	code, stdout, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "opencode", "--config", config, "--base-url", closedGatewayAddress(t),
		"--models", " gpt-6.2-sol, claude-sonnet-5-5 ")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "installed opencode") {
		t.Fatalf("stdout = %q", stdout)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{`"gpt-6.2-sol": {}`, `"claude-sonnet-5-5": {}`} {
		if !strings.Contains(string(content), id) {
			t.Fatalf("the written file is missing %s:\n%s", id, content)
		}
	}
}

// TestKeyInstallModelsOnlyAppliesToOpenCode keeps --models from silently doing nothing on
// a runtime that has no model table to fill.
func TestKeyInstallModelsOnlyAppliesToOpenCode(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := filepath.Join(t.TempDir(), "settings.json")
	code, _, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "claude", "--config", config, "--base-url", "http://127.0.0.1:8317",
		"--models", "gpt-6.2-sol")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, "--models applies only to --runtime opencode") {
		t.Fatalf("stderr = %q", stderr)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("a refused install created the configuration file")
	}
}
