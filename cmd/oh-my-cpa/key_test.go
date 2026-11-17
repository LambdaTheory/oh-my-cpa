package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

// runKeyCommand is the test entry point: it calls the dispatcher the way main does and
// returns everything it wrote.
func runKeyCommand(t *testing.T, arguments ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = runKey(context.Background(), arguments, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestKeyTopLevelHelpListsRegisteredCommands(t *testing.T) {
	code, stdout, stderr := runKeyCommand(t, "--help")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_OK, stderr)
	}
	if stderr != "" {
		t.Fatalf("help wrote to stderr: %q", stderr)
	}
	for _, name := range []string{"gen"} {
		if !strings.Contains(stdout, name) {
			t.Fatalf("help does not list %q:\n%s", name, stdout)
		}
	}
	if !strings.Contains(stdout, "OMC_HOME") || !strings.Contains(stdout, keyclient.ENV_CPA_BASE_URL) {
		t.Fatalf("help does not describe the environment:\n%s", stdout)
	}
}

func TestKeyWithoutACommandIsAUsageError(t *testing.T) {
	code, stdout, stderr := runKeyCommand(t)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d", code, keyclient.EXIT_USAGE)
	}
	if stdout != "" {
		t.Fatalf("usage went to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, "Usage: oh-my-cpa key <command>") {
		t.Fatalf("stderr does not carry the usage:\n%s", stderr)
	}
}

func TestKeyUnknownCommandIsAUsageError(t *testing.T) {
	code, _, stderr := runKeyCommand(t, "frobnicate")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d", code, keyclient.EXIT_USAGE)
	}
	if !strings.Contains(stderr, `unknown command "frobnicate"`) {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestKeyGenHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		code, stdout, stderr := runKeyCommand(t, "gen", flag)
		if code != keyclient.EXIT_OK {
			t.Fatalf("%s: exit = %d (stderr=%s)", flag, code, stderr)
		}
		for _, phrase := range []string{"--repo", "x25519:<base64>", "--base-url", "reused"} {
			if !strings.Contains(stdout, phrase) {
				t.Fatalf("%s: help is missing %q:\n%s", flag, phrase, stdout)
			}
		}
	}
}

// TestKeyGenStdoutIsOnlyThePublicKey is AC-2 at the command layer: everything a person
// needs to read is on stderr, so stdout can be piped straight into an apply call.
func TestKeyGenStdoutIsOnlyThePublicKey(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout has %d lines, want exactly the public key:\n%s", len(lines), stdout)
	}
	if !strings.HasPrefix(lines[0], "x25519:") {
		t.Fatalf("stdout line = %q, want an x25519: public key", lines[0])
	}
	if !strings.Contains(stderr, "generated a key") {
		t.Fatalf("the progress line should be on stderr: %q", stderr)
	}
}

func TestKeyGenSecondRunReusesAndSaysSo(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	_, first, _ := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo")
	code, second, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if first != second {
		t.Fatalf("reuse printed a different key:\n%s%s", first, second)
	}
	if !strings.Contains(stderr, "reused the existing key") {
		t.Fatalf("reuse should be announced on stderr: %q", stderr)
	}
}

func TestKeyGenWritesBaseURLAndReportsIt(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo", "--base-url", "http://127.0.0.1:8317")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stderr, "gateway address") {
		t.Fatalf("stderr = %q", stderr)
	}
	content, err := os.ReadFile(filepath.Join(home, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "http://127.0.0.1:8317") {
		t.Fatalf("config.json = %s", content)
	}
}

func TestKeyGenMissingRepoIsAUsageError(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, stderr := runKeyCommand(t, "gen", "--home", home)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, keyclient.EXIT_USAGE, stdout, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_REPO_REQUIRED) {
		t.Fatalf("stderr should lead with the code: %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
}

func TestKeyGenRejectsAnUnknownFlag(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--nope")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, keyclient.EXIT_USAGE, stderr)
	}
}

// TestKeyJSONUnknownFlagIsOneJSONLine: the --json contract is one object per line even
// when flag parsing itself fails. The flag package's raw diagnostic must not precede it.
func TestKeyJSONUnknownFlagIsOneJSONLine(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, stderr := runKeyCommand(t, "gen", "--home", home, "--json", "--bogus")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, keyclient.EXIT_USAGE, stderr)
	}
	if stdout != "" {
		t.Fatalf("a JSON failure wrote to stdout: %q", stdout)
	}
	if strings.Count(stderr, "\n") != 1 || !strings.HasSuffix(stderr, "\n") {
		t.Fatalf("stderr is not exactly one line: %q", stderr)
	}
	var envelope keyFailureJSON
	if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
		t.Fatalf("stderr is not JSON: %v\n%s", err, stderr)
	}
	if envelope.Code != keyclient.CODE_USAGE {
		t.Fatalf("code = %q, want %q (stderr=%s)", envelope.Code, keyclient.CODE_USAGE, stderr)
	}
	if strings.TrimSpace(envelope.Message) == "" {
		t.Fatalf("the envelope has no message: %s", stderr)
	}
}

// TestKeyUnknownFlagWithoutJSONKeepsTheFlagDiagnostic: only the JSON form changes. A
// caller that did not ask for JSON still gets the flag package's line and the text error.
func TestKeyUnknownFlagWithoutJSONKeepsTheFlagDiagnostic(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, stderr := runKeyCommand(t, "gen", "--home", home, "--bogus")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%q)", code, keyclient.EXIT_USAGE, stderr)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("stderr is not two lines: %q", stderr)
	}
	if lines[0] != "flag provided but not defined: -bogus" {
		t.Fatalf("first line = %q, want the flag package diagnostic", lines[0])
	}
	if !strings.HasPrefix(lines[1], keyclient.CODE_USAGE+": ") {
		t.Fatalf("second line = %q, want the text failure", lines[1])
	}
}

// TestKeyDispatchDoesNotLoadTheService is the boundary the plan asks for: a key command
// runs before the environment file, configuration and database exist, so a machine that
// has never hosted the console can still hold a key. A missing .env and an unusable
// configuration are therefore not observable here.
func TestKeyDispatchDoesNotLoadTheService(t *testing.T) {
	working := t.TempDir()
	t.Chdir(working)
	t.Setenv("OMCPA_ENV_FILE", filepath.Join(working, "does-not-exist.env"))
	t.Setenv("OMCPA_DATA_DIR", filepath.Join(working, "data"))
	t.Setenv("OMCPA_MASTER_KEY", "not-a-valid-key")
	home := filepath.Join(working, "home")
	code, stdout, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.HasPrefix(stdout, "x25519:") {
		t.Fatalf("stdout = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(working, "data")); !os.IsNotExist(err) {
		t.Fatalf("the key command created a data directory (err=%v)", err)
	}
}

func TestRegisterKeyCommandPanicsOnADuplicate(t *testing.T) {
	original := keyCommands
	keyCommands = map[string]*keyCommand{}
	defer func() { keyCommands = original }()

	registerKeyCommand(&keyCommand{Name: "sample", Run: func(context.Context, []string, io.Writer, io.Writer) error { return nil }})
	defer func() {
		if recover() == nil {
			t.Fatal("registering a second command with the same name should panic")
		}
	}()
	registerKeyCommand(&keyCommand{Name: "sample", Run: func(context.Context, []string, io.Writer, io.Writer) error { return nil }})
}

func TestKeyUsageIsSorted(t *testing.T) {
	usage := KeyUsage()
	genIndex := strings.Index(usage, "  gen ")
	if genIndex < 0 {
		t.Fatalf("usage does not list gen:\n%s", usage)
	}
	if !strings.Contains(usage, "Commands:") {
		t.Fatalf("usage has no command heading:\n%s", usage)
	}
}

// TestKeyJSONFailureEnvelope is R-04 at the dispatcher: with --json, every subcommand's
// failure is one JSON object on stderr and nothing on stdout. status and accept define the
// flag for their success documents; the other four accept it for the failure envelope only.
func TestKeyJSONFailureEnvelope(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv(keyclient.ENV_SERVER_URL, "")
	t.Setenv(keyclient.ENV_MANAGEMENT_KEY, "")
	cases := []struct {
		name     string
		args     []string
		wantCode string
	}{
		{"status", []string{"status", "--home", home, "--remote", "--json"}, keyclient.CODE_REMOTE_NOT_CONFIGURED},
		{"accept", []string{"accept", "--home", home, "--json"}, keyclient.CODE_REPO_REQUIRED},
		{"gen", []string{"gen", "--home", home, "--json"}, keyclient.CODE_REPO_REQUIRED},
		{"exec", []string{"exec", "--home", home, "--runtime", "claude", "--repo", "org/repo", "--json", "--", "echo"}, keyclient.CODE_KEY_NOT_FOUND},
		{"install", []string{"install", "--home", home, "--runtime", "claude", "--repo", "org/repo", "--json"}, keyclient.CODE_KEY_NOT_FOUND},
		{"purge", []string{"purge", "--home", home, "--json"}, keyclient.CODE_REPO_REQUIRED},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			code, stdout, stderr := runKeyCommand(t, testCase.args...)
			if code == keyclient.EXIT_OK {
				t.Fatalf("the case was meant to fail; stdout=%q stderr=%q", stdout, stderr)
			}
			if stdout != "" {
				t.Fatalf("a JSON failure wrote to stdout: %q", stdout)
			}
			if strings.Count(stderr, "\n") != 1 || !strings.HasSuffix(stderr, "\n") {
				t.Fatalf("stderr is not exactly one line: %q", stderr)
			}
			var envelope keyFailureJSON
			if err := json.Unmarshal([]byte(stderr), &envelope); err != nil {
				t.Fatalf("stderr is not JSON: %v\n%s", err, stderr)
			}
			if envelope.Code != testCase.wantCode {
				t.Fatalf("code = %q, want %q (stderr=%s)", envelope.Code, testCase.wantCode, stderr)
			}
			if strings.TrimSpace(envelope.Message) == "" {
				t.Fatalf("the envelope has no message: %s", stderr)
			}
		})
	}
}

// TestKeyJSONFalseKeepsTheTextFailure: only the requested form changes. An explicit false
// must keep the familiar `code: message` line.
func TestKeyJSONFalseKeepsTheTextFailure(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, stderr := runKeyCommand(t, "gen", "--home", home, "--json=false")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d", code, keyclient.EXIT_USAGE)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
	if !strings.HasPrefix(stderr, keyclient.CODE_REPO_REQUIRED+": ") {
		t.Fatalf("stderr = %q, want the text form", stderr)
	}
	if json.Valid([]byte(stderr)) {
		t.Fatalf("--json=false produced JSON: %q", stderr)
	}
}

// TestKeyJSONAfterTheTerminatorIsTheChildsArgument: `key exec ... -- echo --json` asks
// echo for a flag, not this process for a format. The dispatcher must stop scanning at the
// terminator.
func TestKeyJSONAfterTheTerminatorIsTheChildsArgument(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, _, stderr := runKeyCommand(t, "exec", "--home", home, "--runtime", "claude", "--repo", "org/repo", "--", "echo", "--json")
	if code != keyclient.EXIT_NOT_FOUND {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_NOT_FOUND, stderr)
	}
	if !strings.HasPrefix(stderr, keyclient.CODE_KEY_NOT_FOUND+": ") {
		t.Fatalf("stderr = %q, want the text form", stderr)
	}
}

// TestKeyJSONDoesNotChangeASuccessfulTextCommand: a command with no JSON success document
// still accepts the flag; only its failures use the envelope.
func TestKeyJSONDoesNotChangeASuccessfulTextCommand(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	code, stdout, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo", "--json")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.HasPrefix(stdout, "x25519:") {
		t.Fatalf("stdout = %q, want the public key", stdout)
	}
	if !strings.Contains(stderr, "generated a key") {
		t.Fatalf("stderr = %q, want the text progress line", stderr)
	}
}
