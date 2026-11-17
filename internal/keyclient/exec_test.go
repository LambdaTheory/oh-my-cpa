package keyclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

const (
	// helperModeEnv turns the test binary into the child `key exec` starts. The value
	// selects what the child does, so one test binary covers every runtime.
	helperModeEnv       = "OMC_KEY_EXEC_HELPER"
	helperModeEnvReport = "report"
	helperModeExit      = "exit"
	helperExitCode      = 7

	// helperMarkerEnv names a file the child creates when it starts. A test that expects
	// the child never to run sets it and asserts the file is absent, which is a direct
	// observation rather than an inference from empty output.
	helperMarkerEnv = "OMC_KEY_EXEC_MARKER"
)

// codexOverrides holds the provider flags this child was started with. Codex's overrides
// arrive as ordinary arguments, so the helper has to take them out before the testing
// package parses its own flags; keeping them lets the child report what it received.
var codexOverrides []string

// TestMain lets the helper binary accept the flags Codex would receive. The stripping is
// confined to child mode, so an ordinary `go test` run is unaffected.
func TestMain(m *testing.M) {
	if os.Getenv(helperModeEnv) != "" {
		os.Args, codexOverrides = stripCodexOverrides(os.Args)
	}
	os.Exit(m.Run())
}

// stripCodexOverrides removes `-c <assignment>` pairs from the child's arguments and
// returns them, so the testing package's flag parser sees only its own flags.
func stripCodexOverrides(arguments []string) ([]string, []string) {
	kept := make([]string, 0, len(arguments))
	overrides := make([]string, 0, 8)
	for index := 0; index < len(arguments); index++ {
		if arguments[index] == "-c" && index+1 < len(arguments) {
			overrides = append(overrides, arguments[index+1])
			index++
			continue
		}
		kept = append(kept, arguments[index])
	}
	return kept, overrides
}

// TestHelperProcess is the child side of the process tests. A test binary runs one entry
// point, so an environment variable selects the behaviour instead of a flag.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv(helperModeEnv) {
	case helperModeEnvReport:
		recordHelperStart()
		report, err := json.Marshal(observeChild())
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, string(report))
		os.Exit(0)
	case helperModeExit:
		recordHelperStart()
		os.Exit(helperExitCode)
	default:
		t.Skip("child-process entry point; the parent test starts it")
	}
}

// helperVariable is one variable as the child saw it: whether it exists, how long it is,
// and a digest. A digest proves the exact value without a value ever reaching an output
// stream, which is the property `key exec` promises and this test must not break by
// printing the thing it checks.
type helperVariable struct {
	Present bool   `json:"present"`
	Length  int    `json:"length"`
	Sha256  string `json:"sha256"`
}

// helperOpenCode is the part of the merged configuration the child could read, reported as
// structure plus a digest so the provider names and address stay readable.
type helperOpenCode struct {
	ProviderNames []string `json:"provider_names"`
	BaseURL       string   `json:"base_url"`
	APIKeySha256  string   `json:"api_key_sha256"`
	APIKeyPresent bool     `json:"api_key_present"`
	Theme         string   `json:"theme"`
}

// childReport is what the helper prints back to the parent.
type childReport struct {
	Variables map[string]helperVariable `json:"variables"`
	Argv      []string                  `json:"argv_sha256"`
	Overrides []string                  `json:"override_sha256"`
	Directory string                    `json:"directory"`
	OpenCode  *helperOpenCode           `json:"opencode,omitempty"`
}

// observedVariables are the names the helper reports. A fixed list keeps unrelated
// environment values out of the report.
var observedVariables = []string{
	ENV_ANTHROPIC_BASE_URL,
	ENV_ANTHROPIC_AUTH_TOKEN,
	ENV_ANTHROPIC_API_KEY,
	ENV_CODEX_KEY,
	ENV_OPENCODE_CONTENT,
}

// observeChild builds the report from this process's own environment and arguments.
func observeChild() childReport {
	report := childReport{
		Variables: map[string]helperVariable{},
		Directory: workingDirectory(),
	}
	for _, name := range observedVariables {
		report.Variables[name] = describeValue(os.Getenv(name))
	}
	for _, argument := range os.Args[1:] {
		report.Argv = append(report.Argv, digestOf(argument))
	}
	for _, override := range codexOverrides {
		report.Overrides = append(report.Overrides, digestOf(override))
	}
	if content := os.Getenv(ENV_OPENCODE_CONTENT); content != "" {
		report.OpenCode = observeOpenCode(content)
	}
	return report
}

// observeOpenCode reads the merged configuration the way OpenCode would: the provider
// table, the address and the key, the last as a digest.
func observeOpenCode(content string) *helperOpenCode {
	observation := &helperOpenCode{}
	var document map[string]any
	if err := json.Unmarshal([]byte(content), &document); err != nil {
		observation.ProviderNames = []string{"<not json>"}
		return observation
	}
	if theme, ok := document["theme"].(string); ok {
		observation.Theme = theme
	}
	providers, ok := document["provider"].(map[string]any)
	if !ok {
		return observation
	}
	for name := range providers {
		observation.ProviderNames = append(observation.ProviderNames, name)
	}
	sort.Strings(observation.ProviderNames)
	ours, ok := providers[OPENCODE_PROVIDER_ID].(map[string]any)
	if !ok {
		return observation
	}
	options, ok := ours["options"].(map[string]any)
	if !ok {
		return observation
	}
	if baseURL, ok := options["baseURL"].(string); ok {
		observation.BaseURL = baseURL
	}
	if apiKey, ok := options["apiKey"].(string); ok {
		observation.APIKeyPresent = true
		observation.APIKeySha256 = digestOf(apiKey)
	}
	return observation
}

// describeValue reports a value without carrying it.
func describeValue(value string) helperVariable {
	return helperVariable{Present: value != "", Length: len(value), Sha256: digestOf(value)}
}

// digestOf is the sha256 of a string, hex-encoded. An absent value has no digest, so a
// caller can tell "empty" from "set to the empty string" by the presence flag instead.
func digestOf(value string) string {
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

// recordHelperStart writes the marker the parent test looks for. A failed write exits
// non-zero so a test cannot pass on a child that half started.
func recordHelperStart() {
	marker := os.Getenv(helperMarkerEnv)
	if marker == "" {
		return
	}
	if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// workingDirectory reports the child's working directory without failing the process.
func workingDirectory() string {
	directory, err := os.Getwd()
	if err != nil {
		return ""
	}
	return directory
}

// execFixture is a home with a repository key and an envelope that opens to sampleKey.
type execFixture struct {
	Home Home
	Repo string
}

// newExecFixture writes a key pair and seals sampleKey to it, which is exactly the state
// a completed `gen` + `accept` leaves behind minus the network.
func newExecFixture(t *testing.T) execFixture {
	t.Helper()
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	generated, err := Generate(GenOptions{Home: home, Repo: "org/repo", Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := repokey.ParsePublicKey(generated.Pairs.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := repokey.Encrypt(publicKey, sampleKey)
	if err != nil {
		t.Fatal(err)
	}
	envelopePath, err := home.EnvelopePath("org/repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envelopePath, []byte(envelope), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	return execFixture{Home: home, Repo: "org/repo"}
}

// helperCommand is the argv that makes this test binary act as the child. The mode is
// read from the environment, so this argv is the same for every mode.
func helperCommand() []string {
	return []string{os.Args[0], "-test.run=TestHelperProcess"}
}

// helperEnvironment is the parent environment plus the switch the helper reads. The
// helper's own mode variable is inherited, which is harmless: it never reaches the
// variables under test.
func helperEnvironment(mode string) []string {
	return append(os.Environ(), helperModeEnv+"="+mode)
}

// runExec starts RunChild with a captured byte buffer for both streams.
func runExec(t *testing.T, options ExecOptions) (status int, stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	options.Stdout = &out
	options.Stderr = &errOut
	status, err = RunChild(context.Background(), options)
	return status, out.String(), errOut.String(), err
}

// decodeReport parses the helper's single JSON line.
func decodeReport(t *testing.T, stdout string) childReport {
	t.Helper()
	var report childReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &report); err != nil {
		t.Fatalf("the child did not print one JSON report: %v\noutput=%q", err, stdout)
	}
	return report
}

// assertVariableIs checks that the child saw name set to exactly want, comparing digests
// so the assertion never prints the value it is checking.
func assertVariableIs(t *testing.T, report childReport, name, want string) {
	t.Helper()
	observed, present := report.Variables[name]
	if !present || !observed.Present {
		t.Fatalf("the child does not see %s", name)
	}
	if observed.Sha256 != digestOf(want) {
		t.Fatalf("%s reached the child with the wrong value (%d bytes)", name, observed.Length)
	}
}

// containsDigest reports whether any of the child's arguments hashes to value.
func containsDigest(digests []string, value string) bool {
	want := digestOf(value)
	for _, digest := range digests {
		if digest == want {
			return true
		}
	}
	return false
}

// snapshotTree records every path under root with its size, so a test can prove a command
// wrote nothing.
func snapshotTree(t *testing.T, root string) map[string]int64 {
	t.Helper()
	files := map[string]int64{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		files[path] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestExecClaudeChildSeesTheKeyAndNothingElse is the core AC-4 assertion: the child sees
// the two variables, the competing one is gone, and neither stream carries the key.
func TestExecClaudeChildSeesTheKeyAndNothingElse(t *testing.T) {
	fixture := newExecFixture(t)
	environment := helperEnvironment(helperModeEnvReport)
	environment = append(environment,
		"ANTHROPIC_API_KEY=user-own-key",
		"ANTHROPIC_AUTH_TOKEN=user-own-token",
	)
	status, stdout, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     environment,
		Home:    fixture.Home,
	})
	if err != nil {
		t.Fatalf("RunChild: %v (stderr=%s)", err, stderr)
	}
	if status != EXIT_OK {
		t.Fatalf("status = %d (stderr=%s)", status, stderr)
	}
	report := decodeReport(t, stdout)
	assertVariableIs(t, report, ENV_ANTHROPIC_AUTH_TOKEN, sampleKey)
	assertVariableIs(t, report, ENV_ANTHROPIC_BASE_URL, "http://127.0.0.1:8317")
	if report.Variables[ENV_ANTHROPIC_API_KEY].Present {
		t.Fatalf("the child still sees %s", ENV_ANTHROPIC_API_KEY)
	}
	if strings.Contains(stdout, sampleKey) {
		t.Fatalf("the key reached the child's stdout: %q", stdout)
	}
	if strings.Contains(stderr, sampleKey) {
		t.Fatalf("the key reached stderr: %q", stderr)
	}
	if !strings.Contains(stderr, ENV_ANTHROPIC_API_KEY) {
		t.Fatalf("the removal should be announced, without a value: %q", stderr)
	}
}

func TestExecCodexChildSeesTheKeyAndTheProviderFlags(t *testing.T) {
	fixture := newExecFixture(t)
	// The helper is this binary, but the runtime requires the command to be codex. The
	// command name check is on the basename, so a copy named codex stands in for it and
	// its arguments can still ask the test binary to report.
	codexCommand := copyAsCodex(t)
	status, stdout, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeCodex,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    []string{codexCommand, "-test.run=TestHelperProcess"},
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if err != nil {
		t.Fatalf("RunChild: %v (stderr=%s)", err, stderr)
	}
	if status != EXIT_OK {
		t.Fatalf("status = %d (stderr=%s)", status, stderr)
	}
	report := decodeReport(t, stdout)
	assertVariableIs(t, report, ENV_CODEX_KEY, sampleKey)
	for _, required := range []string{
		`model_provider="oh-my-cpa"`,
		`model_providers.oh-my-cpa.base_url="http://127.0.0.1:8317/v1"`,
		`model_providers.oh-my-cpa.env_key="OMC_REPO_KEY"`,
		`model_providers.oh-my-cpa.wire_api="responses"`,
	} {
		if !containsDigest(report.Overrides, required) {
			t.Fatalf("the child did not receive the override %q (saw %d overrides)", required, len(report.Overrides))
		}
	}
	if digestOf("-test.run=TestHelperProcess") != report.Argv[len(report.Argv)-1] {
		t.Fatalf("the child's own argument was changed or reordered: %q", report.Argv)
	}
	if strings.Contains(stdout, sampleKey) || strings.Contains(stderr, sampleKey) {
		t.Fatalf("the key reached an output stream")
	}
}

// copyAsCodex copies the test binary to a file named codex so the runtime's command check
// accepts it, while the binary still honours -test.run.
func copyAsCodex(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	name := "codex"
	if filepath.Ext(executable) == ".exe" {
		name = "codex.exe"
	}
	copied := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(copied, content, 0o700); err != nil {
		t.Fatal(err)
	}
	return copied
}

func TestExecOpenCodeChildSeesTheMergedConfiguration(t *testing.T) {
	fixture := newExecFixture(t)
	opencodeConfig := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(opencodeConfig, []byte(`{"provider":{"other":{"npm":"@ai-sdk/openai"}},"theme":"dark"}`), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	environment := helperEnvironment(helperModeEnvReport)
	environment = append(environment, ENV_OPENCODE_CONFIG+"="+opencodeConfig)
	status, stdout, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeOpenCode,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     environment,
		Home:    fixture.Home,
	})
	if err != nil {
		t.Fatalf("RunChild: %v (stderr=%s)", err, stderr)
	}
	if status != EXIT_OK {
		t.Fatalf("status = %d (stderr=%s)", status, stderr)
	}
	report := decodeReport(t, stdout)
	if report.OpenCode == nil {
		t.Fatalf("the child did not receive %s", ENV_OPENCODE_CONTENT)
	}
	if report.OpenCode.Theme != "dark" {
		t.Fatalf("the user's file configuration was dropped: %+v", report.OpenCode)
	}
	if !slices.Contains(report.OpenCode.ProviderNames, "other") {
		t.Fatalf("the user's provider was dropped: %v", report.OpenCode.ProviderNames)
	}
	if !slices.Contains(report.OpenCode.ProviderNames, OPENCODE_PROVIDER_ID) {
		t.Fatalf("our provider is missing: %v", report.OpenCode.ProviderNames)
	}
	if report.OpenCode.BaseURL != "http://127.0.0.1:8317/v1" {
		t.Fatalf("baseURL = %q", report.OpenCode.BaseURL)
	}
	if !report.OpenCode.APIKeyPresent || report.OpenCode.APIKeySha256 != digestOf(sampleKey) {
		t.Fatalf("the key was not carried to the child intact")
	}
	if strings.Contains(stdout, sampleKey) || strings.Contains(stderr, sampleKey) {
		t.Fatalf("the key reached an output stream")
	}
}

// TestExecPassesTheChildsExitCode is the pass-through requirement: a failing child is the
// command's result, not a failure of the command that started it.
func TestExecPassesTheChildsExitCode(t *testing.T) {
	fixture := newExecFixture(t)
	status, _, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeExit),
		Home:    fixture.Home,
	})
	if err != nil {
		t.Fatalf("RunChild returned an error for a started child: %v (stderr=%s)", err, stderr)
	}
	if status != helperExitCode {
		t.Fatalf("status = %d, want the child's %d", status, helperExitCode)
	}
}

func TestExecRefusesAMissingCommand(t *testing.T) {
	fixture := newExecFixture(t)
	status, _, _, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_COMMAND_REQUIRED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_COMMAND_REQUIRED, err)
	}
	if status != EXIT_USAGE {
		t.Fatalf("status = %d, want %d", status, EXIT_USAGE)
	}
}

// TestExecMissingKeyNeverStartsTheChild covers the AC-6 half that purge produces: no key,
// exit 4, a readable code, and a child that did not run.
func TestExecMissingKeyNeverStartsTheChild(t *testing.T) {
	fixture := newExecFixture(t)
	marker := filepath.Join(t.TempDir(), "child-ran")
	if err := os.Remove(keyPathFor(t, fixture.Home, fixture.Repo)); err != nil {
		t.Fatal(err)
	}
	environment := append(helperEnvironment(helperModeEnvReport), helperMarkerEnv+"="+marker)
	status, stdout, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     environment,
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_KEY_NOT_FOUND {
		t.Fatalf("code = %q, want %q (err=%v stderr=%s)", CodeOf(err), CODE_KEY_NOT_FOUND, err, stderr)
	}
	if status != EXIT_NOT_FOUND {
		t.Fatalf("status = %d, want %d", status, EXIT_NOT_FOUND)
	}
	if stdout != "" {
		t.Fatalf("a refusal wrote to stdout: %q", stdout)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the child ran without a key")
	}
	if !strings.Contains(MessageOf(err), "key gen") {
		t.Fatalf("the message should say what to run next: %q", MessageOf(err))
	}
}

func TestExecMissingEnvelopeNeverStartsTheChild(t *testing.T) {
	fixture := newExecFixture(t)
	marker := filepath.Join(t.TempDir(), "child-ran")
	if err := os.Remove(envelopePathFor(t, fixture.Home, fixture.Repo)); err != nil {
		t.Fatal(err)
	}
	environment := append(helperEnvironment(helperModeEnvReport), helperMarkerEnv+"="+marker)
	status, _, _, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     environment,
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_ENVELOPE_MISSING {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_ENVELOPE_MISSING, err)
	}
	if status != EXIT_NOT_FOUND {
		t.Fatalf("status = %d, want %d", status, EXIT_NOT_FOUND)
	}
	if !strings.Contains(MessageOf(err), "key accept") {
		t.Fatalf("the message should say what to run next: %q", MessageOf(err))
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the child ran without an envelope")
	}
}

// TestExecEnvelopeForAnotherKeyIsRefused proves the failure is decrypt_failed and that the
// message carries no ciphertext or key material.
func TestExecEnvelopeForAnotherKeyIsRefused(t *testing.T) {
	fixture := newExecFixture(t)
	otherPair, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	otherPublic, err := repokey.ParsePublicKey(otherPair.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	otherEnvelope, err := repokey.Encrypt(otherPublic, "sk-cpa-another-repo")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envelopePathFor(t, fixture.Home, fixture.Repo), []byte(otherEnvelope), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	status, _, _, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_DECRYPT_FAILED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_DECRYPT_FAILED, err)
	}
	if status != EXIT_FAILED {
		t.Fatalf("status = %d, want %d", status, EXIT_FAILED)
	}
	if strings.Contains(MessageOf(err), "sk-cpa-another-repo") {
		t.Fatalf("the message carries plaintext: %q", MessageOf(err))
	}
	if strings.Contains(MessageOf(err), otherEnvelope) {
		t.Fatalf("the message carries the envelope: %q", MessageOf(err))
	}
}

func TestExecMalformedEnvelopeIsRefused(t *testing.T) {
	fixture := newExecFixture(t)
	if err := os.WriteFile(envelopePathFor(t, fixture.Home, fixture.Repo), []byte("{not json"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_ENVELOPE_INVALID {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_ENVELOPE_INVALID, err)
	}
}

// TestExecRequiresABaseURL covers base_url_required: with none of the flag, the
// environment and config.json set, no address is invented.
func TestExecRequiresABaseURL(t *testing.T) {
	fixture := newExecFixture(t)
	environment := []string{helperModeEnv + "=" + helperModeEnvReport}
	status, _, _, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		Argv:    helperCommand(),
		Env:     environment,
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_BASE_URL_REQUIRED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_BASE_URL_REQUIRED, err)
	}
	if status != EXIT_CONFIG {
		t.Fatalf("status = %d, want %d", status, EXIT_CONFIG)
	}
}

func TestExecReadsTheBaseURLFromTheEnvironmentThenTheConfig(t *testing.T) {
	fixture := newExecFixture(t)
	if err := fixture.Home.WriteConfig("http://from-config:8317", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name        string
		environment []string
		want        string
	}{
		{"config", helperEnvironment(helperModeEnvReport), "http://from-config:8317"},
		{"environment wins over config", append(helperEnvironment(helperModeEnvReport), ENV_CPA_BASE_URL+"=http://from-env:8317"), "http://from-env:8317"},
	} {
		status, stdout, stderr, err := runExec(t, ExecOptions{
			Runtime: RuntimeClaude,
			Repo:    fixture.Repo,
			Argv:    helperCommand(),
			Env:     testCase.environment,
			Home:    fixture.Home,
		})
		if err != nil {
			t.Fatalf("%s: %v (stderr=%s)", testCase.name, err, stderr)
		}
		if status != EXIT_OK {
			t.Fatalf("%s: status = %d", testCase.name, status)
		}
		assertVariableIs(t, decodeReport(t, stdout), ENV_ANTHROPIC_BASE_URL, testCase.want)
	}
}

// TestExecWritesNothing is the "no plaintext on disk" half of AC-4: the home is byte-for-byte
// the same after a successful run, and no temporary file appears beside it.
func TestExecWritesNothing(t *testing.T) {
	fixture := newExecFixture(t)
	before := snapshotTree(t, fixture.Home.Root)
	status, _, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if err != nil {
		t.Fatalf("RunChild: %v (stderr=%s)", err, stderr)
	}
	if status != EXIT_OK {
		t.Fatalf("status = %d (stderr=%s)", status, stderr)
	}
	after := snapshotTree(t, fixture.Home.Root)
	if len(before) != len(after) {
		t.Fatalf("the home gained or lost files: before=%v after=%v", sortedKeys(before), sortedKeys(after))
	}
	for path, size := range before {
		if after[path] != size {
			t.Fatalf("%s changed size: %d -> %d", path, size, after[path])
		}
	}
}

// TestExecRunsInTheCallersDirectory keeps relative paths working for the child, which the
// plan requires: the command runs where the operator ran it.
func TestExecRunsInTheCallersDirectory(t *testing.T) {
	fixture := newExecFixture(t)
	directory := t.TempDir()
	status, stdout, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		Dir:     directory,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if err != nil {
		t.Fatalf("RunChild: %v (stderr=%s)", err, stderr)
	}
	if status != EXIT_OK {
		t.Fatalf("status = %d", status)
	}
	report := decodeReport(t, stdout)
	if filepath.Clean(report.Directory) != filepath.Clean(directory) {
		t.Fatalf("the child ran in %q, want %q", report.Directory, directory)
	}
}

func TestExecUnknownRuntimeIsAUsageError(t *testing.T) {
	fixture := newExecFixture(t)
	_, _, _, err := runExec(t, ExecOptions{
		Runtime: "gpt",
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_RUNTIME_UNKNOWN {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_RUNTIME_UNKNOWN, err)
	}
}

// TestExecCannotStartTheCommand reports exec_failed, and the message carries no key.
func TestExecCannotStartTheCommand(t *testing.T) {
	fixture := newExecFixture(t)
	status, _, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    []string{filepath.Join(t.TempDir(), "does-not-exist")},
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_EXEC_FAILED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_EXEC_FAILED, err)
	}
	if status != EXIT_FAILED {
		t.Fatalf("status = %d, want %d", status, EXIT_FAILED)
	}
	if strings.Contains(stderr, sampleKey) || strings.Contains(MessageOf(err), sampleKey) {
		t.Fatal("a failure message carried the key")
	}
}

// TestZeroBytes proves the erasure helper does what the comment claims, so nobody reads
// more safety into it than it provides.
func TestZeroBytes(t *testing.T) {
	buffer := []byte("a-private-key-material")
	zeroBytes(buffer)
	for index, value := range buffer {
		if value != 0 {
			t.Fatalf("byte %d was not cleared: %d", index, value)
		}
	}
}

// keyPathFor and envelopePathFor resolve a repository's files, failing the test rather
// than the command when the identity does not map to a filename.
func keyPathFor(t *testing.T, home Home, repoID string) string {
	t.Helper()
	path, err := home.KeyPath(repoID)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func envelopePathFor(t *testing.T, home Home, repoID string) string {
	t.Helper()
	path, err := home.EnvelopePath(repoID)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func sortedKeys(values map[string]int64) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// TestExecRefusesAnUnreadablePrivateKey keeps key_unreadable distinct from key_not_found:
// a key file of the wrong length is a damaged key, not a missing one.
func TestExecRefusesAnUnreadablePrivateKey(t *testing.T) {
	fixture := newExecFixture(t)
	path, err := fixture.Home.KeyPath(fixture.Repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("short"), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Repo:    fixture.Repo,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if CodeOf(err) != CODE_KEY_UNREADABLE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_KEY_UNREADABLE, err)
	}
}

// TestExecInfersTheRepositoryFromTheDirectory exercises the --repo omission path with a
// real checkout: a directory with a git origin resolves to that repository.
func TestExecInfersTheRepositoryFromTheDirectory(t *testing.T) {
	fixture := newExecFixture(t)
	directory := t.TempDir()
	runGitForTest(t, directory, "init")
	runGitForTest(t, directory, "remote", "add", "origin", "git@github.com:Org/Repo.git")
	status, stdout, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Dir:     directory,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    fixture.Home,
	})
	if err != nil {
		t.Fatalf("RunChild: %v (stderr=%s)", err, stderr)
	}
	if status != EXIT_OK {
		t.Fatalf("status = %d (stderr=%s)", status, stderr)
	}
	assertVariableIs(t, decodeReport(t, stdout), ENV_ANTHROPIC_AUTH_TOKEN, sampleKey)
}

// TestExecRequiresAGitBinaryOrAName is the fallback: without a remote the directory name is
// the identity, which is what a local checkout without an origin uses.
func TestExecFallsBackToTheDirectoryName(t *testing.T) {
	home := Home{Root: filepath.Join(t.TempDir(), "home")}
	if err := home.Ensure(); err != nil {
		t.Fatal(err)
	}
	pair, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "local-checkout")
	if err := os.MkdirAll(directory, DIR_MODE); err != nil {
		t.Fatal(err)
	}
	repoID, display, err := InferRepo(directory)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WritePrivateKey(home, repoID, pair.PrivateKey); err != nil {
		t.Fatal(err)
	}
	publicKey, err := repokey.ParsePublicKey(pair.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := repokey.Encrypt(publicKey, sampleKey)
	if err != nil {
		t.Fatal(err)
	}
	envelopePath, err := home.EnvelopePath(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(envelopePath, []byte(envelope), FILE_MODE); err != nil {
		t.Fatal(err)
	}
	status, stdout, stderr, err := runExec(t, ExecOptions{
		Runtime: RuntimeClaude,
		Dir:     directory,
		BaseURL: "http://127.0.0.1:8317",
		Argv:    helperCommand(),
		Env:     helperEnvironment(helperModeEnvReport),
		Home:    home,
	})
	if err != nil {
		t.Fatalf("RunChild: %v (stderr=%s)", err, stderr)
	}
	if status != EXIT_OK {
		t.Fatalf("status = %d (stderr=%s)", status, stderr)
	}
	assertVariableIs(t, decodeReport(t, stdout), ENV_ANTHROPIC_AUTH_TOKEN, sampleKey)
	if repoID == "" || display == "" {
		t.Fatalf("the directory identity did not resolve: repoID=%q display=%q", repoID, display)
	}
}

// runGitForTest runs a git command in directory, failing the test on error. It exists so
// the inference test needs no shell.
func runGitForTest(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	output, ok := runGit(directory, arguments...)
	if !ok {
		t.Fatalf("git %v in %s failed: %s", arguments, directory, output)
	}
}
