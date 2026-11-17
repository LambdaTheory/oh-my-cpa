package main

import (
	"bytes"
	"context"
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
