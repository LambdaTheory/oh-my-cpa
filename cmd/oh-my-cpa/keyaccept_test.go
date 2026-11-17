package main

import (
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

// withStdin gives one command run a fixed stdin, restoring the real one afterwards, so a
// test never touches the process's own input.
func withStdin(t *testing.T, input string) {
	t.Helper()
	previous := keyStdin
	keyStdin = strings.NewReader(input)
	t.Cleanup(func() { keyStdin = previous })
}

// acceptFixture generates a repository key through the real command, reads its public
// key, and returns the MCP result that opens with it.
func acceptFixture(t *testing.T, home, repo, applicationID, plaintext string) string {
	t.Helper()
	code, publicKey, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", repo)
	if code != keyclient.EXIT_OK {
		t.Fatalf("gen failed: %s", stderr)
	}
	publicBytes, err := repokey.ParsePublicKey(strings.TrimSpace(publicKey))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := repokey.Encrypt(publicBytes, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	return `{"status":"success","data":{"application_id":"` + applicationID +
		`","kind":"issue","key_mask":"sk-cpa-ab***wxyz","algorithm":"` + repokey.Algorithm +
		`","ciphertext":` + quoteJSON(sealed) + `}}`
}

// quoteJSON turns a JSON document into a JSON string, the way a text content field carries
// it.
func quoteJSON(document string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(document) + `"`
}

func TestKeyAcceptHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		code, stdout, stderr := runKeyCommand(t, "accept", flag)
		if code != keyclient.EXIT_OK {
			t.Fatalf("%s: exit = %d (stderr=%s)", flag, code, stderr)
		}
		for _, phrase := range []string{"--repo", "--application-id", "--json", "stdin", "repo_key_ciphertext"} {
			if !strings.Contains(stdout, phrase) {
				t.Fatalf("%s: help is missing %q:\n%s", flag, phrase, stdout)
			}
		}
	}
}

func TestKeyAcceptStoresTheEnvelopeAndPrintsOneLine(t *testing.T) {
	home := t.TempDir() + "/home"
	input := acceptFixture(t, home, "org/repo", "app-1", "sk-cpa-secret-value")
	withStdin(t, input)
	code, stdout, stderr := runKeyCommand(t, "accept", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout has %d lines, want one:\n%s", len(lines), stdout)
	}
	for _, phrase := range []string{"accepted", "org/repo", "sk-cpa-ab***wxyz", "envelopes/"} {
		if !strings.Contains(lines[0], phrase) {
			t.Fatalf("the confirmation line is missing %q: %q", phrase, lines[0])
		}
	}
	// Neither the ciphertext nor the plaintext may appear on either stream.
	for _, stream := range []string{stdout, stderr} {
		for _, forbidden := range []string{"sk-cpa-secret-value", "epk"} {
			if strings.Contains(stream, forbidden) {
				t.Fatalf("accept wrote %q to a stream: %q", forbidden, stream)
			}
		}
	}
}

func TestKeyAcceptJSONIsMachineReadable(t *testing.T) {
	home := t.TempDir() + "/home"
	withStdin(t, acceptFixture(t, home, "org/repo", "app-1", "sk-cpa-secret-value"))
	code, stdout, stderr := runKeyCommand(t, "accept", "--home", home, "--repo", "org/repo", "--json")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	for _, phrase := range []string{`"application_id": "app-1"`, `"approval_status": "approved"`, `"envelope_file"`} {
		if !strings.Contains(stdout, phrase) {
			t.Fatalf("--json is missing %q:\n%s", phrase, stdout)
		}
	}
	if strings.Contains(stdout, "sk-cpa-secret-value") {
		t.Fatalf("--json carries the plaintext:\n%s", stdout)
	}
}

func TestKeyAcceptRefusesAKeyMismatch(t *testing.T) {
	home := t.TempDir() + "/home"
	// A different repository's key seals the envelope, and it is offered to org/repo.
	foreign := acceptFixture(t, home, "org/other", "app-2", "sk-cpa-other")
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo"); code != keyclient.EXIT_OK {
		t.Fatalf("gen: %s", stderr)
	}
	withStdin(t, foreign)
	code, stdout, stderr := runKeyCommand(t, "accept", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_FAILED {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_FAILED, stderr)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
	if !strings.Contains(stderr, keyclient.CODE_ENVELOPE_MISMATCH) {
		t.Fatalf("stderr should lead with the code: %q", stderr)
	}
}

func TestKeyAcceptWithoutStdinIsAnEnvelopeError(t *testing.T) {
	home := t.TempDir() + "/home"
	if code, _, stderr := runKeyCommand(t, "gen", "--home", home, "--repo", "org/repo"); code != keyclient.EXIT_OK {
		t.Fatalf("gen: %s", stderr)
	}
	withStdin(t, "")
	code, _, stderr := runKeyCommand(t, "accept", "--home", home, "--repo", "org/repo")
	if code != keyclient.EXIT_FAILED || !strings.Contains(stderr, keyclient.CODE_ENVELOPE_INVALID) {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

func TestKeyAcceptRequiresARepo(t *testing.T) {
	home := t.TempDir() + "/home"
	withStdin(t, `{"ciphertext":"{}"}`)
	code, stdout, stderr := runKeyCommand(t, "accept", "--home", home)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d", code, keyclient.EXIT_USAGE)
	}
	if !strings.Contains(stderr, keyclient.CODE_REPO_REQUIRED) || stdout != "" {
		t.Fatalf("stdout = %q, stderr = %q", stdout, stderr)
	}
}

func TestKeyAcceptRejectsAStrayArgument(t *testing.T) {
	home := t.TempDir() + "/home"
	withStdin(t, `{"ciphertext":"{}"}`)
	code, _, stderr := runKeyCommand(t, "accept", "--home", home, "--repo", "org/repo", "extra")
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, stderr = %q", code, stderr)
	}
}

