package keyclient

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
)

// The variables each runtime reads, and the ones that must not survive into the child.
//
// Filtering is not tidying: a leftover ANTHROPIC_API_KEY or an OPENCODE_CONFIG_CONTENT
// naming another provider would be read alongside, or instead of, the repository key,
// and the child would then run with a credential this command did not choose. Every
// name below is therefore removed from the inherited environment before the runtime's
// own values are appended.
const (
	ENV_ANTHROPIC_API_KEY    = "ANTHROPIC_API_KEY"
	ENV_ANTHROPIC_AUTH_TOKEN = "ANTHROPIC_AUTH_TOKEN"
	ENV_ANTHROPIC_BASE_URL   = "ANTHROPIC_BASE_URL"

	// ENV_CODEX_KEY is the variable Codex's generated provider entry points at with
	// env_key. The key never enters argv, where other local processes could read it.
	ENV_CODEX_KEY = "OMC_REPO_KEY"

	// ENV_OPENCODE_CONTENT is OpenCode's process-scoped configuration override: the one
	// entry point that does not put the key in a file.
	ENV_OPENCODE_CONTENT = "OPENCODE_CONFIG_CONTENT"

	// ENV_OPENCODE_CONFIG is the user's own configuration path override, honoured the
	// way OpenCode honours it.
	ENV_OPENCODE_CONFIG = "OPENCODE_CONFIG"

	// MAX_OPENCODE_CONFIG_BYTES bounds the merged configuration. A single environment
	// value has a platform limit (32 KiB for the Windows environment block); refusing
	// well below it with a readable error beats a truncated JSON document that OpenCode
	// would reject with a syntax error.
	MAX_OPENCODE_CONFIG_BYTES = 24 << 10

	// OPENCODE_PROVIDER_ID is the provider entry this command defines, in both
	// `exec` (environment) and `install` (file) form. It is the same identifier so a
	// person sees one name for what they installed and what they run.
	OPENCODE_PROVIDER_ID = "oh-my-cpa"

	// CODEX_PROVIDER_ID is the Codex model_provider this command selects for one run.
	CODEX_PROVIDER_ID = "oh-my-cpa"

	// PROVIDER_DISPLAY_NAME is what a runtime shows for the provider this command defines.
	PROVIDER_DISPLAY_NAME = "Oh My CPA"

	// OPENCODE_PROVIDER_PACKAGE is the package OpenCode loads to talk to an
	// OpenAI-compatible endpoint. It must be the npm form: the bundled executable is
	// installed through npm and resolves provider packages by name.
	OPENCODE_PROVIDER_PACKAGE = "@ai-sdk/openai-compatible"
)

// Injection is one runtime's prepared child process: the complete environment it will
// receive, the complete argument list, and the warnings to print before it starts.
type Injection struct {
	// Env is the whole child environment, already filtered and extended. It is not a
	// delta, so a caller assigning it to exec.Cmd.Env cannot accidentally keep an
	// inherited variable the runtime must not see.
	Env []string
	// Argv is the whole child argument list, with any runtime-required flags inserted.
	Argv []string
	// Warnings are one-line messages for stderr. They never carry a value.
	Warnings []string
}

// InjectOptions is everything one runtime's mapping needs. The file system is not
// touched here: OpenCodeDocument is the user's configuration file already read by the
// caller, which keeps this mapping a pure function of its inputs.
type InjectOptions struct {
	// Key is the plaintext gateway key. It is placed only in the child's environment.
	Key string
	// Root is the normalized gateway root, without a trailing slash or /v1.
	Root string
	// Argv is the command the operator asked to run, with its arguments.
	Argv []string
	// Env is the environment this command inherited.
	Env []string

	// OpenCodeDocument is the content of the user's OpenCode configuration file; nil
	// or empty means there is none. It is merged under the inherited
	// OPENCODE_CONFIG_CONTENT, which takes priority.
	OpenCodeDocument []byte
	// OpenCodeConfigPath names that file in error messages.
	OpenCodeConfigPath string
}

// BaseURL is the endpoint one runtime is pointed at. Claude Code takes the gateway root
// and appends /v1/messages itself; the OpenAI-shaped runtimes expect the /v1 prefix to
// be part of the address they are given.
func (r Runtime) BaseURL(root string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(root), "/")
	if trimmed == "" {
		return "", Errorf(CODE_BASE_URL_REQUIRED, "no gateway address; pass --base-url, set %s, or save one with `oh-my-cpa key gen --base-url <url>`", ENV_CPA_BASE_URL)
	}
	switch r {
	case RuntimeClaude:
		return trimmed, nil
	case RuntimeCodex, RuntimeOpenCode:
		return trimmed + "/v1", nil
	}
	return "", Errorf(CODE_RUNTIME_UNKNOWN, "unknown runtime %q; use claude, codex or opencode", string(r))
}

// Inject maps a runtime's requirements onto a child process.
//
// Each branch builds the environment from scratch (inherited minus the names that would
// compete with the repository key, plus this runtime's own), then builds the argument
// list. Nothing here logs, formats or returns the key to a caller that prints.
func (r Runtime) Inject(options InjectOptions) (Injection, error) {
	root := strings.TrimSpace(options.Root)
	baseURL, err := r.BaseURL(root)
	if err != nil {
		return Injection{}, err
	}
	switch r {
	case RuntimeClaude:
		return injectClaude(options, baseURL)
	case RuntimeCodex:
		return injectCodex(options, baseURL)
	case RuntimeOpenCode:
		return injectOpenCode(options, baseURL)
	}
	return Injection{}, Errorf(CODE_RUNTIME_UNKNOWN, "unknown runtime %q; use claude, codex or opencode", string(r))
}

// injectClaude sets the two variables Claude Code's Anthropic client reads and removes
// the ones that could win over them.
//
// ANTHROPIC_API_KEY is removed with a warning because Anthropic's clients have preferred
// it to ANTHROPIC_AUTH_TOKEN historically, and a user who has it exported would
// otherwise send their own key to this gateway without noticing.
func injectClaude(options InjectOptions, baseURL string) (Injection, error) {
	if strings.TrimSpace(options.Key) == "" {
		return Injection{}, Errorf(CODE_DECRYPT_FAILED, "the envelope opened to an empty key")
	}
	warnings := []string{}
	if envValue(options.Env, ENV_ANTHROPIC_API_KEY) != "" {
		warnings = append(warnings, "removed "+ENV_ANTHROPIC_API_KEY+" for this child: it would take precedence over the repository key")
	}
	env := filterEnv(options.Env, ENV_ANTHROPIC_API_KEY, ENV_ANTHROPIC_AUTH_TOKEN, ENV_ANTHROPIC_BASE_URL)
	env = append(env,
		ENV_ANTHROPIC_BASE_URL+"="+baseURL,
		ENV_ANTHROPIC_AUTH_TOKEN+"="+options.Key,
	)
	return Injection{Env: env, Argv: copyArgv(options.Argv), Warnings: warnings}, nil
}

// injectCodex selects the generated provider for this one run and hands Codex the key
// through an environment variable it reads by name.
//
// Codex has no verified way to select a provider from the environment alone, so the
// provider is passed as `-c` overrides, which sit above the user's own config.toml for
// this invocation and change no file. The key is referenced as env_key so it stays out
// of argv and out of `ps`.
//
// Only the codex executable itself is accepted: a wrapper script would receive these
// flags and might forward them to a different program, and a failure to apply them
// would be invisible.
func injectCodex(options InjectOptions, baseURL string) (Injection, error) {
	if len(options.Argv) == 0 {
		return Injection{}, Errorf(CODE_COMMAND_REQUIRED, "no command; put the command after `--`")
	}
	if !isCodexCommand(options.Argv[0]) {
		return Injection{}, Errorf(CODE_CODEX_COMMAND, "%q is not codex; `key exec --runtime codex` only runs codex itself, because a wrapper script would not reliably receive the provider flags. Run `oh-my-cpa key exec --runtime codex -- codex ...`, or write the key into the Codex configuration with `oh-my-cpa key install --runtime codex`", options.Argv[0])
	}
	if strings.TrimSpace(options.Key) == "" {
		return Injection{}, Errorf(CODE_DECRYPT_FAILED, "the envelope opened to an empty key")
	}
	env := filterEnv(options.Env, ENV_CODEX_KEY)
	env = append(env, ENV_CODEX_KEY+"="+options.Key)

	// -c must precede any subcommand: Codex reads global options only before it.
	overrides := []string{
		"-c", `model_provider="` + CODEX_PROVIDER_ID + `"`,
		"-c", `model_providers.` + CODEX_PROVIDER_ID + `.name="` + PROVIDER_DISPLAY_NAME + `"`,
		"-c", `model_providers.` + CODEX_PROVIDER_ID + `.base_url="` + baseURL + `"`,
		"-c", `model_providers.` + CODEX_PROVIDER_ID + `.env_key="` + ENV_CODEX_KEY + `"`,
		"-c", `model_providers.` + CODEX_PROVIDER_ID + `.wire_api="responses"`,
	}
	argv := make([]string, 0, len(options.Argv)+len(overrides))
	argv = append(argv, options.Argv[0])
	argv = append(argv, overrides...)
	argv = append(argv, options.Argv[1:]...)
	return Injection{Env: env, Argv: argv, Warnings: []string{}}, nil
}

// injectOpenCode sends the user's configuration plus one provider entry as
// OPENCODE_CONFIG_CONTENT, the only process-scoped entry point that writes no file.
//
// The file content is sent in full because whether OpenCode merges this variable with
// the file is not settled; sending the composition we mean removes the question. The
// v1 singular `provider` key is what the installed versions read.
func injectOpenCode(options InjectOptions, baseURL string) (Injection, error) {
	if strings.TrimSpace(options.Key) == "" {
		return Injection{}, Errorf(CODE_DECRYPT_FAILED, "the envelope opened to an empty key")
	}
	content, err := mergeOpenCodeConfig(options, baseURL)
	if err != nil {
		return Injection{}, err
	}
	env := filterEnv(options.Env, ENV_OPENCODE_CONTENT)
	env = append(env, ENV_OPENCODE_CONTENT+"="+content)
	return Injection{Env: env, Argv: copyArgv(options.Argv), Warnings: []string{}}, nil
}

// mergeOpenCodeConfig composes the document OpenCode should see: the user's file, then
// any inherited OPENCODE_CONFIG_CONTENT over it (the environment is the more specific
// instruction), then this command's provider entry.
func mergeOpenCodeConfig(options InjectOptions, baseURL string) (string, error) {
	document := map[string]any{}
	if len(bytes.TrimSpace(options.OpenCodeDocument)) > 0 {
		if err := json.Unmarshal(options.OpenCodeDocument, &document); err != nil {
			return "", Errorf(CODE_CONFIG_NOT_JSON, "%s is not plain JSON; OpenCode also accepts comments, but this command cannot merge them without rewriting them. Remove the comments or use `oh-my-cpa key install --runtime opencode`", configPathLabel(options.OpenCodeConfigPath))
		}
	}
	if inherited := envValue(options.Env, ENV_OPENCODE_CONTENT); strings.TrimSpace(inherited) != "" {
		var overlay map[string]any
		if err := json.Unmarshal([]byte(inherited), &overlay); err != nil {
			return "", Errorf(CODE_CONFIG_NOT_JSON, "the inherited %s is not a JSON object; unset it and run the command again", ENV_OPENCODE_CONTENT)
		}
		document = mergeJSONObjects(document, overlay)
	}

	providers := map[string]any{}
	if existing, present := document["provider"]; present {
		object, ok := existing.(map[string]any)
		if !ok {
			return "", Errorf(CODE_CONFIG_NOT_JSON, "%s has a \"provider\" value that is not an object; this command cannot merge its provider entry into it", configPathLabel(options.OpenCodeConfigPath))
		}
		providers = object
	}
	providers[OPENCODE_PROVIDER_ID] = map[string]any{
		"npm":  OPENCODE_PROVIDER_PACKAGE,
		"name": PROVIDER_DISPLAY_NAME,
		"options": map[string]any{
			"baseURL": baseURL,
			"apiKey":  options.Key,
		},
	}
	document["provider"] = providers

	encoded, err := json.Marshal(document)
	if err != nil {
		return "", Wrapf(err, CODE_CONFIG_NOT_JSON, "cannot encode the merged OpenCode configuration: %v", err)
	}
	if len(encoded) > MAX_OPENCODE_CONFIG_BYTES {
		return "", Errorf(CODE_CONFIG_TOO_LARGE, "the merged OpenCode configuration is %d bytes, over the %d-byte limit for one environment value; use `oh-my-cpa key install --runtime opencode` instead, which writes the provider into %s", len(encoded), MAX_OPENCODE_CONFIG_BYTES, configPathLabel(options.OpenCodeConfigPath))
	}
	return string(encoded), nil
}

// mergeJSONObjects deep-merges overlay into base, with overlay winning. Nested objects
// are merged so a caller's override of one key keeps the rest of that object; every
// other value replaces wholesale, which is what a JSON reader expects.
func mergeJSONObjects(base, overlay map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(overlay))
	for name, value := range base {
		merged[name] = value
	}
	for name, value := range overlay {
		existing, exists := merged[name]
		existingObject, existingIsObject := existing.(map[string]any)
		valueObject, valueIsObject := value.(map[string]any)
		if exists && existingIsObject && valueIsObject {
			merged[name] = mergeJSONObjects(existingObject, valueObject)
			continue
		}
		merged[name] = value
	}
	return merged
}

// isCodexCommand reports whether the command is the codex executable itself. A .cmd or
// a script with another name is refused: it is a wrapper, and the provider flags may not
// reach the program that would use them.
func isCodexCommand(command string) bool {
	base := strings.ToLower(filepath.Base(strings.TrimSpace(command)))
	return base == "codex" || base == "codex.exe"
}

// filterEnv returns env without the named variables. Names are compared
// case-insensitively on Windows, where the environment is, and exactly elsewhere.
func filterEnv(env []string, names ...string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, blocked := range names {
			if envNameEqual(name, blocked) {
				drop = true
				break
			}
		}
		if !drop {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}

// envValue returns the value of name in env, matching the same names envNameEqual does.
// A later entry wins, which is also how the child's own lookup will resolve a duplicate.
func envValue(env []string, name string) string {
	for index := len(env) - 1; index >= 0; index-- {
		entryName, value, found := strings.Cut(env[index], "=")
		if found && envNameEqual(entryName, name) {
			return value
		}
	}
	return ""
}

// envNameEqual compares environment variable names the way the platform does.
func envNameEqual(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// copyArgv returns a copy so a mapping never mutates the caller's slice.
func copyArgv(argv []string) []string {
	copied := make([]string, len(argv))
	copy(copied, argv)
	return copied
}

// configPathLabel names a configuration file in a message, using a neutral label when
// the caller did not have one.
func configPathLabel(path string) string {
	if strings.TrimSpace(path) == "" {
		return "the OpenCode configuration"
	}
	return path
}

// OpenCodeConfigPath resolves the file OpenCode would read: $OPENCODE_CONFIG, else
// ~/.config/opencode/opencode.json. It never creates anything.
func OpenCodeConfigPath(env []string, userHomeDir string) string {
	if configured := strings.TrimSpace(envValue(env, ENV_OPENCODE_CONFIG)); configured != "" {
		return configured
	}
	if strings.TrimSpace(userHomeDir) == "" {
		return ""
	}
	return filepath.Join(userHomeDir, ".config", "opencode", "opencode.json")
}
