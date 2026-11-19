package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/keyclient"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

const execSampleKey = "sk-cpa-exec-test-4444-5555-6666"

// childStart holds the arguments this binary was started with when it acts as `key exec`'s
// child. Codex's provider overrides are ordinary arguments, and the test binary would
// reject them; TestMain keeps them aside before the testing package parses its own flags.
var childStart struct {
	arguments []string
	overrides []string
}

// TestMain intercepts the child invocation so the provider flags never reach the testing
// package's parser. An ordinary `go test ./cmd/oh-my-cpa` run is unaffected.
func TestMain(m *testing.M) {
	if os.Getenv("OMC_KEY_EXEC_HELPER") != "" {
		childStart.arguments, childStart.overrides = splitProviderOverrides(os.Args[1:])
		// A child runs exactly the helper test. Anything broader would start the tests
		// that spawn children, which is an unbounded tree rather than a test run.
		os.Args = []string{os.Args[0], "-test.run=^TestKeyExecHelperProcess$"}
	}
	os.Exit(m.Run())
}

// splitProviderOverrides separates the `-c <assignment>` pairs from the rest of the
// arguments, which is exactly the split Codex itself makes.
func splitProviderOverrides(arguments []string) (rest []string, overrides []string) {
	for index := 0; index < len(arguments); index++ {
		if arguments[index] == "-c" && index+1 < len(arguments) {
			overrides = append(overrides, arguments[index+1])
			index++
			continue
		}
		rest = append(rest, arguments[index])
	}
	return rest, overrides
}

// newExecCommandHome builds a home with a key and an envelope the exec command can open,
// plus a codex-named copy of this binary so the codex runtime's command check passes.
func newExecCommandHome(t *testing.T) (home string, codexCommand string) {
	t.Helper()
	home = filepath.Join(t.TempDir(), "home")
	resolved, err := keyclient.ResolveHome(home)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := keyclient.Generate(keyclient.GenOptions{Home: resolved, Repo: "org/repo"})
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := repokey.ParsePublicKey(generated.Pairs.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := repokey.Encrypt(publicKey, execSampleKey)
	if err != nil {
		t.Fatal(err)
	}
	envelopePath, err := resolved.EnvelopePath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envelopePath, []byte(envelope), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, copyBinaryAs(t, "codex")
}

// copyBinaryAs puts a copy of the test binary under another name, so a runtime that
// requires a specific command can still be exercised with the helper entry point.
func copyBinaryAs(t *testing.T, name string) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(executable) == ".exe" {
		name += ".exe"
	}
	copied := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(copied, content, 0o700); err != nil {
		t.Fatal(err)
	}
	return copied
}

func TestKeyExecHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		code, stdout, stderr := runKeyCommand(t, "exec", flag)
		if code != keyclient.EXIT_OK {
			t.Fatalf("%s: exit = %d (stderr=%s)", flag, code, stderr)
		}
		for _, phrase := range []string{"--runtime", "--models", "ANTHROPIC_AUTH_TOKEN", "OMC_REPO_KEY", "OPENCODE_CONFIG_CONTENT", "after --"} {
			if !strings.Contains(stdout, phrase) {
				t.Fatalf("%s: help is missing %q:\n%s", flag, phrase, stdout)
			}
		}
	}
}

func TestKeyExecTopLevelHelpListsExec(t *testing.T) {
	code, stdout, stderr := runKeyCommand(t, "--help")
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "  exec ") {
		t.Fatalf("the top-level help does not list exec:\n%s", stdout)
	}
}

// TestKeyExecPassesEverythingAfterTheTerminator is the argument-shape requirement: nothing
// between `--` is interpreted, so a child can receive its own flags.
func TestKeyExecPassesEverythingAfterTheTerminator(t *testing.T) {
	home, codexCommand := newExecCommandHome(t)
	t.Setenv("OMC_KEY_EXEC_HELPER", "argv")
	code, stdout, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--base-url", "http://127.0.0.1:8317",
		"--runtime", "codex",
		"--", codexCommand, "exec", "--model", "gpt-5", "some prompt",
	)
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	var report struct {
		Arguments []string `json:"arguments"`
		Overrides []string `json:"overrides"`
	}
	// The helper prints one JSON line; the test binary then prints its own summary.
	firstLine, _, _ := strings.Cut(stdout, "\n")
	if err := json.Unmarshal([]byte(firstLine), &report); err != nil {
		t.Fatalf("the child did not report: %v\nstdout=%q", err, stdout)
	}
	if want := []string{"exec", "--model", "gpt-5", "some prompt"}; !slices.Equal(report.Arguments, want) {
		t.Fatalf("the child's own arguments were altered: got %q, want %q", report.Arguments, want)
	}
	if len(report.Overrides) != 5 {
		t.Fatalf("the child received %d provider overrides, want 5: %q", len(report.Overrides), report.Overrides)
	}
	for _, argument := range append(report.Arguments, report.Overrides...) {
		if strings.Contains(argument, execSampleKey) {
			t.Fatalf("the key reached the command line: %q", argument)
		}
	}
}

// TestKeyExecPassesTheChildExitCode is the pass-through at the command layer: the
// dispatcher must return the child's status, not a generic failure.
func TestKeyExecPassesTheChildExitCode(t *testing.T) {
	home, _ := newExecCommandHome(t)
	t.Setenv("OMC_KEY_EXEC_HELPER", "exit")
	code, _, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--base-url", "http://127.0.0.1:8317",
		"--runtime", "claude",
		"--", os.Args[0], "-test.run=TestKeyExecHelperProcess",
	)
	if code != 7 {
		t.Fatalf("exit = %d, want the child's 7 (stderr=%s)", code, stderr)
	}
}

// TestKeyExecHelperProcess is the child the command tests start. It has to live in this
// package because the command layer runs its child from its own test binary.
func TestKeyExecHelperProcess(t *testing.T) {
	switch os.Getenv("OMC_KEY_EXEC_HELPER") {
	case "exit":
		os.Exit(7)
	case "argv":
		report, err := json.Marshal(map[string]any{
			"arguments": childStart.arguments,
			"overrides": childStart.overrides,
		})
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout.Write(report)
		os.Stdout.WriteString("\n")
	default:
		t.Skip("child-process entry point")
	}
}

func TestKeyExecMissingRuntimeIsAUsageError(t *testing.T) {
	home, _ := newExecCommandHome(t)
	code, stdout, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--", "true",
	)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_RUNTIME_UNKNOWN) {
		t.Fatalf("stderr = %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
}

func TestKeyExecUnknownRuntimeIsAUsageError(t *testing.T) {
	home, _ := newExecCommandHome(t)
	code, _, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--runtime", "gpt", "--", "true",
	)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_RUNTIME_UNKNOWN) {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestKeyExecMissingCommandIsAUsageError(t *testing.T) {
	home, _ := newExecCommandHome(t)
	code, _, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--runtime", "claude",
	)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_COMMAND_REQUIRED) {
		t.Fatalf("stderr = %q", stderr)
	}
}

// TestKeyExecRejectsACommandBeforeTheTerminator keeps the two argument regions distinct: a
// bare word where only flags belong is a mistake worth reporting, not something to run.
func TestKeyExecRejectsACommandBeforeTheTerminator(t *testing.T) {
	home, _ := newExecCommandHome(t)
	code, _, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--runtime", "claude", "claude",
	)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, "after --") {
		t.Fatalf("the message should point at the terminator: %q", stderr)
	}
}

// TestKeyExecMissingKeyIsNotFound covers the exit code a script branches on.
func TestKeyExecMissingKeyIsNotFound(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	resolved, err := keyclient.ResolveHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := resolved.Ensure(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--base-url", "http://127.0.0.1:8317",
		"--runtime", "claude", "--", "true",
	)
	if code != keyclient.EXIT_NOT_FOUND {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_NOT_FOUND, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_KEY_NOT_FOUND) {
		t.Fatalf("stderr = %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("a failure wrote to stdout: %q", stdout)
	}
}

func TestKeyExecMissingBaseURLIsAConfigurationError(t *testing.T) {
	home, _ := newExecCommandHome(t)
	t.Setenv(keyclient.ENV_CPA_BASE_URL, "")
	code, _, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--runtime", "claude", "--", "true",
	)
	if code != keyclient.EXIT_CONFIG {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_CONFIG, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_BASE_URL_REQUIRED) {
		t.Fatalf("stderr = %q", stderr)
	}
}

// TestKeyExecCodexRefusesAWrapper is the runtime restriction surfaced at the command layer,
// with a message that names the two ways forward.
func TestKeyExecCodexRefusesAWrapper(t *testing.T) {
	home, _ := newExecCommandHome(t)
	code, _, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--base-url", "http://127.0.0.1:8317",
		"--runtime", "codex", "--", "codex-wrapper", "exec",
	)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_CODEX_COMMAND) {
		t.Fatalf("stderr = %q", stderr)
	}
	if !strings.Contains(stderr, "key install") {
		t.Fatalf("the message should offer install: %q", stderr)
	}
}

// TestKeyExecOpenCodeWithModelsSkipsTheGateway is the offline path: the explicit list is
// used as given, so an unreachable gateway does not stop the child.
func TestKeyExecOpenCodeWithModelsSkipsTheGateway(t *testing.T) {
	home, _ := newExecCommandHome(t)
	t.Setenv("OMC_KEY_EXEC_HELPER", "argv")
	code, stdout, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--base-url", closedGatewayAddress(t),
		"--runtime", "opencode", "--models", "gpt-6.2-sol",
		"--", os.Args[0], "-test.run=TestKeyExecHelperProcess",
	)
	if code != keyclient.EXIT_OK {
		t.Fatalf("exit = %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, `"arguments"`) {
		t.Fatalf("the child did not run: %q", stdout)
	}
}

// TestKeyExecOpenCodeWithoutModelsFailsBeforeTheChild is the refusal: a gateway that
// cannot name the models stops the command before anything is started.
func TestKeyExecOpenCodeWithoutModelsFailsBeforeTheChild(t *testing.T) {
	home, _ := newExecCommandHome(t)
	t.Setenv("OMC_KEY_EXEC_HELPER", "argv")
	code, stdout, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--base-url", closedGatewayAddress(t),
		"--runtime", "opencode",
		"--", os.Args[0], "-test.run=TestKeyExecHelperProcess",
	)
	if code != keyclient.EXIT_FAILED {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_FAILED, stderr)
	}
	if !strings.Contains(stderr, keyclient.CODE_MODELS_UNAVAILABLE) {
		t.Fatalf("stderr = %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("the child ran or the command wrote to stdout: %q", stdout)
	}
}

// TestKeyExecModelsOnlyAppliesToOpenCode keeps --models from silently doing nothing on a
// runtime that has no model table to fill.
func TestKeyExecModelsOnlyAppliesToOpenCode(t *testing.T) {
	home, _ := newExecCommandHome(t)
	code, _, stderr := runKeyCommand(t,
		"exec", "--home", home, "--repo", "org/repo", "--base-url", "http://127.0.0.1:8317",
		"--runtime", "claude", "--models", "gpt-6.2-sol", "--", "true",
	)
	if code != keyclient.EXIT_USAGE {
		t.Fatalf("exit = %d, want %d (stderr=%s)", code, keyclient.EXIT_USAGE, stderr)
	}
	if !strings.Contains(stderr, "--models applies only to --runtime opencode") {
		t.Fatalf("stderr = %q", stderr)
	}
}
