package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

// installClaudeForPurge installs the keyed repo into a temp settings file and returns it.
func installClaudeForPurge(t *testing.T, home string) string {
	t.Helper()
	config := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(config, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runKeyCommand(t, "install", "--home", home, "--repo", "org/repo",
		"--runtime", "claude", "--config", config, "--base-url", "http://127.0.0.1:8317")
	if code != keyclient.EXIT_OK {
		t.Fatalf("install: exit = %d (stderr=%s)", code, stderr)
	}
	return config
}

func TestKeyPurgeHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		code, stdout, stderr := runKeyCommand(t, "purge", flag)
		if code != keyclient.EXIT_OK {
			t.Fatalf("%s: exit = %d (stderr=%s)", flag, code, stderr)
		}
		for _, phrase := range []string{"--repo", "--runtime", "--dry-run", "console"} {
			if !strings.Contains(stdout, phrase) {
				t.Fatalf("%s: help is missing %q:\n%s", flag, phrase, stdout)
			}
		}
	}
}

// TestKeyPurgeClearsLocalMaterial is AC-6 at the command layer, including the sentence the
// plan requires: this cleaned this machine, not the gateway.
func TestKeyPurgeClearsLocalMaterial(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := installClaudeForPurge(t, home)

	code, stdout, stderr := runKeyCommand(t, "purge", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "local material cleared") || !strings.Contains(stdout, "still valid") || !strings.Contains(stdout, "console") {
		t.Fatalf("stdout does not say the gateway key is still active:\n%s", stdout)
	}
	// Every removed item is named, one per line.
	for _, want := range []string{"settings.json", "keys/", "envelopes/", "state.json"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("the report does not mention %q:\n%s", want, stdout)
		}
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), commandTestGatewayKey) {
		t.Fatalf("the configuration still holds the key:\n%s", content)
	}
	resolved, err := keyclient.ResolveHome(home)
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(resolved.StatePath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(state), "org/repo") {
		t.Fatalf("the state record survived:\n%s", state)
	}
}

func TestKeyPurgeRequiresRepo(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, stderr := runKeyCommand(t, "purge", "--home", home)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_REPO_REQUIRED) {
		t.Fatalf("stderr = %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
}

// TestKeyPurgeDryRunChangesNothing says what it would do, in the future tense, and does
// none of it.
func TestKeyPurgeDryRunChangesNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := installClaudeForPurge(t, home)
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runKeyCommand(t, "purge", "--home", home, "--repo", "org/repo", "--dry-run")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "would remove") {
		t.Fatalf("the dry run does not use the future tense:\n%s", stdout)
	}
	if strings.Contains(stdout, "local material cleared") {
		t.Fatalf("the dry run claims it cleared something:\n%s", stdout)
	}
	after, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the dry run changed the configuration file")
	}
	resolved, err := keyclient.ResolveHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if hasKey, _ := keyclient.HasPrivateKey(resolved, "org/repo"); !hasKey {
		t.Fatal("the dry run removed the key")
	}
}

// TestKeyPurgeSingleRuntimeKeepsTheKey is the narrow form: one runtime's value goes, the
// key stays, and the output says why.
func TestKeyPurgeSingleRuntimeKeepsTheKey(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := installClaudeForPurge(t, home)

	code, stdout, stderr := runKeyCommand(t, "purge", "--home", home, "--repo", "org/repo", "--runtime", "claude")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "keeps the key and envelope") {
		t.Fatalf("the output does not explain what it kept:\n%s", stderr)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), commandTestGatewayKey) {
		t.Fatalf("the named runtime's value survived:\n%s", content)
	}
	resolved, err := keyclient.ResolveHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if hasKey, _ := keyclient.HasPrivateKey(resolved, "org/repo"); !hasKey {
		t.Fatal("the private key was removed")
	}
	if hasEnvelope, _ := keyclient.HasEnvelope(resolved, "org/repo"); !hasEnvelope {
		t.Fatal("the envelope was removed")
	}
	// The closing line must not claim the local material is gone when the key is still
	// here; that sentence belongs to a full purge.
	if strings.Contains(stdout, "local material cleared") {
		t.Fatalf("a --runtime run claims the local material was cleared:\n%s", stdout)
	}
	if !strings.Contains(stdout, "removed claude's installed value") || !strings.Contains(stdout, "private key and envelope are still on this machine") {
		t.Fatalf("stdout does not say what a --runtime run left behind:\n%s", stdout)
	}
}

// TestKeyPurgeRefusesABorrowedValue is the install_not_owned exit: 1, and the value stays.
func TestKeyPurgeRefusesABorrowedValue(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	config := installClaudeForPurge(t, home)
	replaced := `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:8317","ANTHROPIC_AUTH_TOKEN":"somebody-elses"}}`
	if err := os.WriteFile(config, []byte(replaced), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runKeyCommand(t, "purge", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_FAILED {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_FAILED, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_NOT_OWNED) {
		t.Fatalf("stderr = %q", stderr)
	}
	content, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != replaced {
		t.Fatalf("a refused purge changed the file:\n%s", content)
	}
}

// TestKeyPurgeFailureDoesNotClaimItWasClean keeps the report honest: a run that stopped
// with an error has an empty result, and printing "already clean" beside that error would
// contradict it.
func TestKeyPurgeFailureDoesNotClaimItWasClean(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	code, stdout, stderr := runKeyCommand(t, "purge", "--home", home, "--repo", "org/repo", "--runtime", "codex")
	if code != keyclient.EXIT_NOT_FOUND {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_NOT_FOUND, stderr)
	}
	if strings.Contains(stdout, "already clean") {
		t.Fatalf("a failed run claimed the repository was clean:\n%s", stdout)
	}
	if !strings.Contains(stderr, keyclient.CODE_NOT_INSTALLED) {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestKeyPurgeRepeatIsNotAFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	installClaudeForPurge(t, home)
	if code, _, stderr := runKeyCommand(t, "purge", "--home", home, "--repo", "org/repo"); code != keyclient.EXIT_OK {
		t.Fatalf("first purge: exit = %d (stderr=%s)", code, stderr)
	}
	code, stdout, stderr := runKeyCommand(t, "purge", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_OK {
		t.Fatalf("second purge: exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "already clean") {
		t.Fatalf("stdout = %q", stdout)
	}
}

// TestKeyPurgeAcceptsTheSpellingAnInstallUsed keeps the two commands agreeing about which
// record a name refers to.
func TestKeyPurgeAcceptsTheSpellingAnInstallUsed(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	prepareKeyedRepo(t, home, "org/repo", commandTestGatewayKey)
	installClaudeForPurge(t, home)
	code, _, stderr := runKeyCommand(t, "purge", "--home", home, "--repo", "git@github.com:org/repo.git")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	resolved, err := keyclient.ResolveHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if hasKey, _ := keyclient.HasPrivateKey(resolved, "org/repo"); hasKey {
		t.Fatal("the key survived a purge named with the clone URL")
	}
}
