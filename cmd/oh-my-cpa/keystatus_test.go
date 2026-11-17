package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
)

func TestKeyStatusHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		code, stdout, stderr := runKeyCommand(t, "status", flag)
		if code != keyclient.EXIT_OK {
			t.Fatalf("%s: exit = %d (stderr=%s)", flag, code, stderr)
		}
		for _, phrase := range []string{"--remote", "--application-id", "--json", keyclient.ENV_SERVER_URL, keyclient.ENV_MANAGEMENT_KEY} {
			if !strings.Contains(stdout, phrase) {
				t.Fatalf("%s: help is missing %q:\n%s", flag, phrase, stdout)
			}
		}
	}
}

func TestKeyHelpListsAcceptAndStatus(t *testing.T) {
	code, stdout, _ := runKeyCommand(t, "--help")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d", code)
	}
	for _, name := range []string{"accept", "status", "gen"} {
		if !strings.Contains(stdout, "  "+name) {
			t.Fatalf("the command list does not include %q:\n%s", name, stdout)
		}
	}
}

// TestKeyStatusWithoutAHomeSaysWhatToDo: status is the first command a person runs, so an
// empty machine is a successful answer with a next step, not an error.
func TestKeyStatusWithoutAHomeSaysWhatToDo(t *testing.T) {
	home := t.TempDir() + "/home"
	code, stdout, stderr := runKeyCommand(t, "status", "--home", home)
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "holds no keys yet") || !strings.Contains(stdout, "key gen") {
		t.Fatalf("stdout does not name the next command:\n%s", stdout)
	}
}

func TestKeyStatusListsTheThreeStates(t *testing.T) {
	home := t.TempDir() + "/home"
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/key-only"); code != keyclient.EXIT_OK {
		t.Fatalf("gen key-only: %s", stderr)
	}
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/full"); code != keyclient.EXIT_OK {
		t.Fatalf("gen full: %s", stderr)
	}
	withStdin(t, acceptFixture(t, home, "org/full", "app-full", "sk-cpa-plaintext"))
	if code, _, stderr := runKeyCommand(t, "accept", "--home", home, "--repo", "org/full"); code != keyclient.EXIT_OK {
		t.Fatalf("accept: %s", stderr)
	}
	code, stdout, stderr := runKeyCommand(t, "status", "--home", home)
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "org/full") || !strings.Contains(stdout, "org/key-only") {
		t.Fatalf("the report is missing a repository:\n%s", stdout)
	}
	if !strings.Contains(stdout, "envelope:    missing") {
		t.Fatalf("the key-only row should show a missing envelope:\n%s", stdout)
	}
	if !strings.Contains(stdout, "approved (accept,") {
		t.Fatalf("the full row should show the accepted approval:\n%s", stdout)
	}
	if strings.Contains(stdout, "sk-cpa-plaintext") {
		t.Fatalf("status printed the plaintext:\n%s", stdout)
	}
}

func TestKeyStatusJSONIsParseable(t *testing.T) {
	home := t.TempDir() + "/home"
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo"); code != keyclient.EXIT_OK {
		t.Fatalf("gen: %s", stderr)
	}
	code, stdout, stderr := runKeyCommand(t, "status", "--home", home, "--json")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	var decoded struct {
		Home          string `json:"home"`
		BaseURLSource string `json:"base_url_source"`
		Repos         []struct {
			Repo              string `json:"repo"`
			PrivateKeyPresent bool   `json:"private_key_present"`
			EnvelopePresent   bool   `json:"envelope_present"`
		} `json:"repos"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("status --json is not JSON: %v\n%s", err, stdout)
	}
	if len(decoded.Repos) != 1 || decoded.Repos[0].Repo != "org/repo" || !decoded.Repos[0].PrivateKeyPresent || decoded.Repos[0].EnvelopePresent {
		t.Fatalf("decoded = %+v", decoded)
	}
	if decoded.BaseURLSource != "absent" {
		t.Fatalf("base URL source = %q, want absent", decoded.BaseURLSource)
	}
}

func TestKeyStatusApplicationIDRecordsTheApplication(t *testing.T) {
	home := t.TempDir() + "/home"
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo"); code != keyclient.EXIT_OK {
		t.Fatalf("gen: %s", stderr)
	}
	code, stdout, stderr := runKeyCommand(t, "status", "--home", home, "--repo", "org/repo", "--application-id", "app-42")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "app-42") || !strings.Contains(stdout, "unknown (local,") {
		t.Fatalf("the association is not shown:\n%s", stdout)
	}
	// A second run may name the id without repeating --repo.
	code, stdout, stderr = runKeyCommand(t, "status", "--home", home, "--application-id", "app-42")
	if code != keyclient.EXIT_OK {
		t.Fatalf("second run: exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "app-42") {
		t.Fatalf("the recorded association was not found:\n%s", stdout)
	}
	// An id with no repository and no record cannot be placed.
	code, _, stderr = runKeyCommand(t, "status", "--home", home, "--application-id", "app-unknown")
	if code != keyclient.EXIT_USAGE || !strings.Contains(stderr, keyclient.CODE_REPO_REQUIRED) {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestKeyStatusRemoteNeedsConfiguration(t *testing.T) {
	home := t.TempDir() + "/home"
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo"); code != keyclient.EXIT_OK {
		t.Fatalf("gen: %s", stderr)
	}
	t.Setenv(keyclient.ENV_SERVER_URL, "")
	t.Setenv(keyclient.ENV_MANAGEMENT_KEY, "")
	code, stdout, stderr := runKeyCommand(t, "status", "--home", home, "--remote")
	if code != keyclient.EXIT_CONFIG {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_CONFIG, stderr)
	}
	if stdout != "" {
		t.Fatalf("a failed --remote still printed a report: %q", stdout)
	}
	if !strings.Contains(stderr, keyclient.CODE_REMOTE_NOT_CONFIGURED) {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestKeyStatusRemoteFailureIsReported(t *testing.T) {
	home := t.TempDir() + "/home"
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo"); code != keyclient.EXIT_OK {
		t.Fatalf("gen: %s", stderr)
	}
	// A loopback address the bridge accepts, with nothing listening: the transport fails,
	// which is the case a person hits when the console is down.
	t.Setenv(keyclient.ENV_SERVER_URL, "http://127.0.0.1:1")
	t.Setenv(keyclient.ENV_MANAGEMENT_KEY, "test-management-key")
	code, stdout, stderr := runKeyCommand(t, "status", "--home", home, "--remote")
	if code != keyclient.EXIT_FAILED {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_FAILED, stderr)
	}
	// The local report is still shown, and the failure is named after it.
	if !strings.Contains(stdout, "org/repo") {
		t.Fatalf("the local report was not shown:\n%s", stdout)
	}
	if !strings.Contains(stderr, keyclient.CODE_REMOTE_UNAVAILABLE) || !strings.Contains(stderr, "remote_error:") {
		t.Fatalf("stderr = %q", stderr)
	}
}
