package keyclient

import (
	"encoding/json"
	"strings"
	"testing"
)

// sampleKey is a stand-in for a decrypted gateway key. Tests assert it reaches the child
// environment and no other channel, so it must be distinctive and never a real shape.
const sampleKey = "sk-cpa-test-0000-1111-2222-3333"

// environmentOf turns a slice of "NAME=value" into a map for assertions.
func environmentOf(t *testing.T, env []string) map[string]string {
	t.Helper()
	values := map[string]string{}
	for _, entry := range env {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			t.Fatalf("environment entry %q has no =", entry)
		}
		values[name] = value
	}
	return values
}

func TestRuntimeBaseURL(t *testing.T) {
	cases := []struct {
		runtime Runtime
		root    string
		want    string
	}{
		{RuntimeClaude, "http://127.0.0.1:8317", "http://127.0.0.1:8317"},
		{RuntimeClaude, "https://cpa.example.com/omc", "https://cpa.example.com/omc"},
		{RuntimeCodex, "http://127.0.0.1:8317", "http://127.0.0.1:8317/v1"},
		{RuntimeOpenCode, "https://cpa.example.com/omc/", "https://cpa.example.com/omc/v1"},
	}
	for _, testCase := range cases {
		got, err := testCase.runtime.BaseURL(testCase.root)
		if err != nil {
			t.Fatalf("%s.BaseURL(%q): %v", testCase.runtime, testCase.root, err)
		}
		if got != testCase.want {
			t.Fatalf("%s.BaseURL(%q) = %q, want %q", testCase.runtime, testCase.root, got, testCase.want)
		}
	}
}

func TestRuntimeBaseURLRefusesAnEmptyRoot(t *testing.T) {
	if _, err := RuntimeClaude.BaseURL("  "); CodeOf(err) != CODE_BASE_URL_REQUIRED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_BASE_URL_REQUIRED, err)
	}
}

// TestClaudeInjectionSetsAndRemoves is the table row for Claude Code: the two variables
// are set from the repository key and the root, and the three that could compete with
// them are gone, including ones the user had exported.
func TestClaudeInjectionSetsAndRemoves(t *testing.T) {
	inherited := []string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=user-own-key",
		"ANTHROPIC_AUTH_TOKEN=user-own-token",
		"ANTHROPIC_BASE_URL=http://elsewhere:9999",
	}
	injection, err := RuntimeClaude.Inject(InjectOptions{
		Key:  sampleKey,
		Root: "http://127.0.0.1:8317",
		Argv: []string{"claude", "-p", "hello"},
		Env:  inherited,
	})
	if err != nil {
		t.Fatal(err)
	}
	values := environmentOf(t, injection.Env)
	if values[ENV_ANTHROPIC_API_KEY] != "" {
		t.Fatalf("%s survived: %q", ENV_ANTHROPIC_API_KEY, values[ENV_ANTHROPIC_API_KEY])
	}
	if _, present := values[ENV_ANTHROPIC_API_KEY]; present {
		t.Fatalf("%s is still in the child environment", ENV_ANTHROPIC_API_KEY)
	}
	if values[ENV_ANTHROPIC_AUTH_TOKEN] != sampleKey {
		t.Fatalf("%s = %q", ENV_ANTHROPIC_AUTH_TOKEN, values[ENV_ANTHROPIC_AUTH_TOKEN])
	}
	if values[ENV_ANTHROPIC_BASE_URL] != "http://127.0.0.1:8317" {
		t.Fatalf("%s = %q", ENV_ANTHROPIC_BASE_URL, values[ENV_ANTHROPIC_BASE_URL])
	}
	if values["PATH"] != "/usr/bin" {
		t.Fatalf("PATH was not preserved: %q", values["PATH"])
	}
	if strings.Join(injection.Argv, " ") != "claude -p hello" {
		t.Fatalf("argv = %q, want it unchanged", injection.Argv)
	}
	if len(injection.Warnings) != 1 || !strings.Contains(injection.Warnings[0], ENV_ANTHROPIC_API_KEY) {
		t.Fatalf("warnings = %q, want one naming %s", injection.Warnings, ENV_ANTHROPIC_API_KEY)
	}
	for _, warning := range injection.Warnings {
		if strings.Contains(warning, "user-own-key") {
			t.Fatalf("a warning quoted the value: %q", warning)
		}
	}
}

func TestClaudeInjectionWithoutACompetingKeyIsSilent(t *testing.T) {
	injection, err := RuntimeClaude.Inject(InjectOptions{
		Key:  sampleKey,
		Root: "http://127.0.0.1:8317",
		Argv: []string{"claude"},
		Env:  []string{"PATH=/usr/bin"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(injection.Warnings) != 0 {
		t.Fatalf("warnings = %q, want none", injection.Warnings)
	}
}

// TestCodexInjectionUsesEnvForKey is the table row for Codex: the key is in OMC_REPO_KEY
// and nowhere in argv, and every -c override lands before the user's own arguments.
func TestCodexInjectionUsesEnvForKey(t *testing.T) {
	injection, err := RuntimeCodex.Inject(InjectOptions{
		Key:  sampleKey,
		Root: "http://127.0.0.1:8317",
		Argv: []string{"codex", "exec", "list files"},
		Env:  []string{"PATH=/usr/bin", "OMC_REPO_KEY=some-old-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	values := environmentOf(t, injection.Env)
	if values[ENV_CODEX_KEY] != sampleKey {
		t.Fatalf("%s = %q", ENV_CODEX_KEY, values[ENV_CODEX_KEY])
	}
	for _, argument := range injection.Argv {
		if strings.Contains(argument, sampleKey) {
			t.Fatalf("the key reached argv: %q", injection.Argv)
		}
	}
	if injection.Argv[0] != "codex" {
		t.Fatalf("argv[0] = %q", injection.Argv[0])
	}
	joined := strings.Join(injection.Argv, " ")
	for _, required := range []string{
		`-c model_provider="oh-my-cpa"`,
		`-c model_providers.oh-my-cpa.name="Oh My CPA"`,
		`-c model_providers.oh-my-cpa.base_url="http://127.0.0.1:8317/v1"`,
		`-c model_providers.oh-my-cpa.env_key="OMC_REPO_KEY"`,
		`-c model_providers.oh-my-cpa.wire_api="responses"`,
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("argv is missing %q:\n%s", required, joined)
		}
	}
	// The overrides are global options, so they must all precede "exec"; Codex reads
	// them only before the subcommand.
	execIndex := indexOf(injection.Argv, "exec")
	for index := 1; index < len(injection.Argv) && index < execIndex; index++ {
		if injection.Argv[index] != "-c" && !strings.HasPrefix(injection.Argv[index], "model_provider") {
			t.Fatalf("unexpected argument before the subcommand: %q", injection.Argv[index])
		}
	}
	if execIndex < 0 {
		t.Fatalf("the subcommand is missing from argv: %q", injection.Argv)
	}
	if last := injection.Argv[len(injection.Argv)-1]; last != "list files" {
		t.Fatalf("user arguments were dropped or reordered: %q", injection.Argv)
	}
}

func TestCodexInjectionAcceptsTheExecutableWithExtension(t *testing.T) {
	for _, command := range []string{"codex", "codex.exe", "CODEX", "C:/tools/Codex.EXE"} {
		if _, err := RuntimeCodex.Inject(InjectOptions{
			Key:  sampleKey,
			Root: "http://127.0.0.1:8317",
			Argv: []string{command},
			Env:  []string{},
		}); err != nil {
			t.Fatalf("%q: %v", command, err)
		}
	}
}

// TestCodexInjectionRefusesWrappers is the plan's codex_command_required: a shell wrapper
// or a differently named binary is refused rather than run with flags it may not pass on.
func TestCodexInjectionRefusesWrappers(t *testing.T) {
	for _, command := range []string{"./codex-wrapper.sh", "sh", "codex-cli", "npx"} {
		_, err := RuntimeCodex.Inject(InjectOptions{
			Key:  sampleKey,
			Root: "http://127.0.0.1:8317",
			Argv: []string{command, "exec"},
			Env:  []string{},
		})
		if CodeOf(err) != CODE_CODEX_COMMAND {
			t.Fatalf("%q: code = %q, want %q (err=%v)", command, CodeOf(err), CODE_CODEX_COMMAND, err)
		}
		if ExitCode(err) != EXIT_USAGE {
			t.Fatalf("%q: exit = %d, want %d", command, ExitCode(err), EXIT_USAGE)
		}
	}
}

// TestOpenCodeInjectionMergesTheUsersConfiguration is the table row for OpenCode: one
// JSON document carrying the user's own providers and ours, with the inherited content
// taking priority over the file.
func TestOpenCodeInjectionMergesTheUsersConfiguration(t *testing.T) {
	document := []byte(`{
		"$schema": "https://opencode.ai/config.json",
		"provider": {
			"other": {"npm": "@ai-sdk/openai", "options": {"baseURL": "http://other/v1"}}
		},
		"model": "other/model"
	}`)
	inheritedContent := `{"theme":"dark","provider":{"inherited":{"npm":"@ai-sdk/openai"}}}`
	injection, err := RuntimeOpenCode.Inject(InjectOptions{
		Key:                sampleKey,
		Root:               "http://127.0.0.1:8317",
		Argv:               []string{"opencode"},
		Env:                []string{"PATH=/usr/bin", ENV_OPENCODE_CONTENT + "=" + inheritedContent},
		OpenCodeDocument:   document,
		OpenCodeConfigPath: "/home/user/.config/opencode/opencode.json",
		Models:             []string{"gpt-6.2-sol", "claude-sonnet-5-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	values := environmentOf(t, injection.Env)
	// A second copy of the variable would leave the child's effective configuration
	// dependent on which duplicate the platform returns last, so the inherited one must
	// be gone, not merely overridden.
	if occurrences := countEnvName(injection.Env, ENV_OPENCODE_CONTENT); occurrences != 1 {
		t.Fatalf("%s appears %d times in the child environment, want once", ENV_OPENCODE_CONTENT, occurrences)
	}
	merged := values[ENV_OPENCODE_CONTENT]
	if strings.Contains(merged, inheritedContent) && merged == inheritedContent {
		t.Fatalf("%s was not merged with anything", ENV_OPENCODE_CONTENT)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(merged), &parsed); err != nil {
		t.Fatalf("the injected configuration is not JSON: %v", err)
	}
	if parsed["theme"] != "dark" {
		t.Fatalf("the inherited configuration was dropped: %v", parsed)
	}
	if parsed["$schema"] != "https://opencode.ai/config.json" {
		t.Fatalf("the file's unknown top-level key was dropped: %v", parsed)
	}
	if parsed["model"] != "other/model" {
		t.Fatalf("the user's model choice was changed: %v", parsed)
	}
	providers, ok := parsed["provider"].(map[string]any)
	if !ok {
		t.Fatalf("provider is not an object: %T", parsed["provider"])
	}
	for _, name := range []string{"other", "inherited", OPENCODE_PROVIDER_ID} {
		if _, present := providers[name]; !present {
			t.Fatalf("provider %q is missing: %v", name, providers)
		}
	}
	ours, ok := providers[OPENCODE_PROVIDER_ID].(map[string]any)
	if !ok {
		t.Fatalf("our provider is not an object: %T", providers[OPENCODE_PROVIDER_ID])
	}
	if ours["npm"] != OPENCODE_PROVIDER_PACKAGE {
		t.Fatalf("npm = %v, want %q", ours["npm"], OPENCODE_PROVIDER_PACKAGE)
	}
	options, ok := ours["options"].(map[string]any)
	if !ok {
		t.Fatalf("options is not an object: %T", ours["options"])
	}
	if options["apiKey"] != sampleKey {
		t.Fatalf("apiKey was not set from the decrypted key")
	}
	if options["baseURL"] != "http://127.0.0.1:8317/v1" {
		t.Fatalf("baseURL = %v", options["baseURL"])
	}
	models, ok := ours["models"].(map[string]any)
	if !ok {
		t.Fatalf("models is not an object: %T", ours["models"])
	}
	if len(models) != 2 {
		t.Fatalf("models = %v, want exactly the two ids", models)
	}
	for _, id := range []string{"gpt-6.2-sol", "claude-sonnet-5-5"} {
		if _, present := models[id]; !present {
			t.Fatalf("model %q is missing: %v", id, models)
		}
	}
}

// TestOpenCodeInjectionRequiresModels is the gap the acceptance run found: OpenCode
// refuses a custom provider that declares no models (ProviderModelNotFoundError), so a
// provider entry without them is refused here instead of started and failing later.
func TestOpenCodeInjectionRequiresModels(t *testing.T) {
	_, err := RuntimeOpenCode.Inject(InjectOptions{
		Key:  sampleKey,
		Root: "http://127.0.0.1:8317",
		Argv: []string{"opencode"},
		Env:  []string{},
	})
	if CodeOf(err) != CODE_MODELS_REQUIRED {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_MODELS_REQUIRED, err)
	}
	if ExitCode(err) != EXIT_CONFIG {
		t.Fatalf("exit = %d, want %d", ExitCode(err), EXIT_CONFIG)
	}
	if !strings.Contains(MessageOf(err), "--models") {
		t.Fatalf("the message does not name the way forward: %q", MessageOf(err))
	}
}

func TestOpenCodeInjectionWithoutAFileIsOnlyOurLayer(t *testing.T) {
	injection, err := RuntimeOpenCode.Inject(InjectOptions{
		Key:    sampleKey,
		Root:   "http://127.0.0.1:8317",
		Argv:   []string{"opencode"},
		Env:    []string{},
		Models: []string{"gpt-6.2-sol"},
	})
	if err != nil {
		t.Fatal(err)
	}
	values := environmentOf(t, injection.Env)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(values[ENV_OPENCODE_CONTENT]), &parsed); err != nil {
		t.Fatal(err)
	}
	providers := parsed["provider"].(map[string]any)
	if len(providers) != 1 {
		t.Fatalf("providers = %v, want only ours", providers)
	}
	if _, present := providers[OPENCODE_PROVIDER_ID]; !present {
		t.Fatalf("our provider is missing: %v", providers)
	}
}

// TestOpenCodeInjectionRefusesNonJSON keeps a comment-bearing configuration intact: it is
// refused with a readable code rather than rewritten without the comments.
func TestOpenCodeInjectionRefusesNonJSON(t *testing.T) {
	_, err := RuntimeOpenCode.Inject(InjectOptions{
		Key:                sampleKey,
		Root:               "http://127.0.0.1:8317",
		Argv:               []string{"opencode"},
		Env:                []string{},
		OpenCodeDocument:   []byte("{\n  // a comment\n  \"theme\": \"dark\"\n}\n"),
		OpenCodeConfigPath: "/home/user/.config/opencode/opencode.json",
		Models:             []string{"gpt-6.2-sol"},
	})
	if CodeOf(err) != CODE_CONFIG_NOT_JSON {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_NOT_JSON, err)
	}
	if !strings.Contains(MessageOf(err), "/home/user/.config/opencode/opencode.json") {
		t.Fatalf("the message does not name the file: %q", MessageOf(err))
	}
}

func TestOpenCodeInjectionRefusesAnInheritedNonJSONContent(t *testing.T) {
	_, err := RuntimeOpenCode.Inject(InjectOptions{
		Key:    sampleKey,
		Root:   "http://127.0.0.1:8317",
		Argv:   []string{"opencode"},
		Env:    []string{ENV_OPENCODE_CONTENT + "=not json"},
		Models: []string{"gpt-6.2-sol"},
	})
	if CodeOf(err) != CODE_CONFIG_NOT_JSON {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_NOT_JSON, err)
	}
}

// TestOpenCodeInjectionRefusesAnOversizedMergedDocument is the 24 KiB ceiling: past it the
// command suggests install instead of handing OpenCode a value the platform may truncate.
func TestOpenCodeInjectionRefusesAnOversizedMergedDocument(t *testing.T) {
	large := `{"theme":"dark","padding":"` + strings.Repeat("x", MAX_OPENCODE_CONFIG_BYTES) + `"}`
	_, err := RuntimeOpenCode.Inject(InjectOptions{
		Key:                sampleKey,
		Root:               "http://127.0.0.1:8317",
		Argv:               []string{"opencode"},
		Env:                []string{},
		OpenCodeDocument:   []byte(large),
		OpenCodeConfigPath: "/home/user/.config/opencode/opencode.json",
		Models:             []string{"gpt-6.2-sol"},
	})
	if CodeOf(err) != CODE_CONFIG_TOO_LARGE {
		t.Fatalf("code = %q, want %q (err=%v)", CodeOf(err), CODE_CONFIG_TOO_LARGE, err)
	}
	if ExitCode(err) != EXIT_CONFIG {
		t.Fatalf("exit = %d, want %d", ExitCode(err), EXIT_CONFIG)
	}
	if !strings.Contains(MessageOf(err), "key install") {
		t.Fatalf("the message does not point at install: %q", MessageOf(err))
	}
}

func TestOpenCodeConfigPath(t *testing.T) {
	if got := OpenCodeConfigPath([]string{}, ""); got != "" {
		t.Fatalf("no home resolved to %q, want empty", got)
	}
	if got := OpenCodeConfigPath([]string{"HOME=/home/user"}, "/home/user"); !strings.HasSuffix(got, ".config/opencode/opencode.json") && !strings.HasSuffix(got, `.config\opencode\opencode.json`) {
		t.Fatalf("default path = %q", got)
	}
	if got := OpenCodeConfigPath([]string{ENV_OPENCODE_CONFIG + "=/custom/opencode.json"}, "/home/user"); got != "/custom/opencode.json" {
		t.Fatalf("override path = %q", got)
	}
}

// TestInjectionNeverPutsTheKeyInArgv asserts the cross-runtime invariant once, so a new
// runtime added without thinking about argv fails here.
func TestInjectionNeverPutsTheKeyInArgv(t *testing.T) {
	commands := map[Runtime]string{RuntimeClaude: "claude", RuntimeCodex: "codex", RuntimeOpenCode: "opencode"}
	for _, runtimeName := range Runtimes() {
		injection, err := runtimeName.Inject(InjectOptions{
			Key:    sampleKey,
			Root:   "http://127.0.0.1:8317",
			Argv:   []string{commands[runtimeName]},
			Env:    []string{},
			Models: []string{"gpt-6.2-sol"},
		})
		if err != nil {
			t.Fatalf("%s: %v", runtimeName, err)
		}
		for _, argument := range injection.Argv {
			if strings.Contains(argument, sampleKey) {
				t.Fatalf("%s put the key in argv: %q", runtimeName, injection.Argv)
			}
		}
	}
}

// indexOf returns the first position of value, or -1.
func indexOf(values []string, value string) int {
	for index, candidate := range values {
		if candidate == value {
			return index
		}
	}
	return -1
}

// countEnvName counts how many entries define name. More than one means the child's
// effective value depends on how the platform resolves duplicates.
func countEnvName(env []string, name string) int {
	count := 0
	for _, entry := range env {
		entryName, _, found := strings.Cut(entry, "=")
		if found && entryName == name {
			count++
		}
	}
	return count
}
