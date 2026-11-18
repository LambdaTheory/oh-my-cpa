//go:build e2e

package e2e

// The CLI half of the repository-key loop: every local step is the real
// `oh-my-cpa key` command, run as a separate process against the same console and
// gateway the earlier harness uses.
//
// One flow runs end to end: gen -> apply -> approve -> ciphertext -> accept ->
// exec (three runtimes) -> install -> purge -> exec after purge. Every captured
// stream is scanned for the plaintext key, and each step leaves its exit code and
// assertion results in OMC_E2E_EVIDENCE_DIR/key-cli-loop.json.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

const cliStateFilename = "state.json"

// cliRun is one CLI process and the streams it wrote. The plaintext key is never
// stored in evidence; these captures exist so the final scan can look for it.
type cliRun struct {
	Step   string
	Code   int
	Stdout string
	Stderr string
}

type cliLoop struct {
	t       *testing.T
	h       *harness
	record  *evidenceRecord
	path    string
	outputs []cliRun
	bodies  []cliBody
}

// cliBody is one HTTP step's response, kept only so the final scan can look for
// the plaintext key outside the CLI streams too.
type cliBody struct {
	Step string
	Body []byte
}

func newCLILoop(t *testing.T, h *harness, path string, record *evidenceRecord) *cliLoop {
	t.Helper()
	return &cliLoop{t: t, h: h, record: record, path: path}
}

// step appends one recorded step and rewrites the evidence file. Writing after each
// step means a failure later still leaves the steps that passed.
func (l *cliLoop) step(name string, detail map[string]any) {
	l.record.Steps = append(l.record.Steps, evidenceStep{Name: name, Detail: detail})
	l.write()
}

func (l *cliLoop) write() {
	encoded, err := json.MarshalIndent(l.record, "", "  ")
	if err != nil {
		l.t.Fatalf("encode the CLI evidence: %v", err)
	}
	if err := os.WriteFile(l.path, append(encoded, '\n'), 0o600); err != nil {
		l.t.Fatalf("write the CLI evidence: %v", err)
	}
}

// run starts the real binary with exactly this environment. A nil environment would
// inherit the operator's shell, which could change what a run proves.
func (l *cliLoop) run(step string, env []string, stdin []byte, args ...string) cliRun {
	l.t.Helper()
	command := exec.Command(l.h.binary, args...)
	command.Env = env
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		var exitError *exec.ExitError
		if !errors.As(err, &exitError) {
			l.t.Fatalf("run %s: %v", step, err)
		}
		code = exitError.ExitCode()
	}
	run := cliRun{Step: step, Code: code, Stdout: stdout.String(), Stderr: stderr.String()}
	l.outputs = append(l.outputs, run)
	return run
}

func (l *cliLoop) capture(step string, body []byte) {
	l.bodies = append(l.bodies, cliBody{Step: step, Body: append([]byte(nil), body...)})
}

// require fails with the names of the assertions that did not hold. The diagnostic is
// the caller's, already redacted, so a failure cannot print the key.
func (l *cliLoop) require(step string, checks map[string]bool, diagnostic string) {
	l.t.Helper()
	failed := make([]string, 0, len(checks))
	for name, ok := range checks {
		if !ok {
			failed = append(failed, name)
		}
	}
	if len(failed) == 0 {
		return
	}
	sort.Strings(failed)
	if diagnostic != "" {
		l.t.Fatalf("step %s failed: %s\n%s", step, strings.Join(failed, ", "), diagnostic)
	}
	l.t.Fatalf("step %s failed: %s", step, strings.Join(failed, ", "))
}

func TestKeyCLIFullLoop(t *testing.T) {
	h := startHarness(t)

	root := t.TempDir()
	cliHome := filepath.Join(root, "omc-home")
	userHome := filepath.Join(root, "user-home")
	claudeDir := filepath.Join(userHome, ".claude")
	opencodeDir := filepath.Join(userHome, ".config", "opencode")
	for _, directory := range []string{claudeDir, opencodeDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create %s: %v", directory, err)
		}
	}

	// Pre-existing user configuration belongs to somebody else. Install has to keep it,
	// and purge has to leave it behind when it removes this repository's values.
	claudeSettings := filepath.Join(claudeDir, "settings.json")
	originalSettings := []byte("{\"env\":{\"OTHER\":\"1\"}}")
	if err := os.WriteFile(claudeSettings, originalSettings, 0o600); err != nil {
		t.Fatalf("seed the Claude settings: %v", err)
	}
	openCodeConfig := filepath.Join(opencodeDir, "opencode.json")
	if err := os.WriteFile(openCodeConfig, []byte(`{"theme":"dark","provider":{"other":{"npm":"@ai-sdk/openai"}}}`), 0o600); err != nil {
		t.Fatalf("seed the OpenCode configuration: %v", err)
	}

	evidencePath := filepath.Join(h.evidence, "key-cli-loop.json")
	record := &evidenceRecord{
		Repo:       e2eRepo,
		Conclusion: "failed",
		Services: map[string]any{
			"console":   h.consoleURL,
			"gateway":   h.gateway.baseURL(),
			"log":       h.logPath,
			"binary":    h.binary,
			"cli_home":  cliHome,
			"user_home": userHome,
		},
	}
	loop := newCLILoop(t, h, evidencePath, record)
	loop.step("start", map[string]any{"agent": e2eAgent, "device": e2eDevice})

	env := cliEnvironment(userHome)
	repoID := strings.ToLower(e2eRepo)
	secret := ""
	approvedMask := ""

	// 1. Generate the caller's half with the CLI. stdout is the public key and nothing
	// else, so it can be submitted as-is.
	gen := loop.run("gen", env, nil, "key", "gen", "--repo", e2eRepo, "--home", cliHome, "--base-url", h.gateway.baseURL())
	publicKey := strings.TrimSpace(gen.Stdout)
	genChecks := map[string]bool{
		"exit_zero":                 gen.Code == 0,
		"stdout_is_one_public_key":  strings.HasPrefix(publicKey, "x25519:") && !strings.ContainsAny(publicKey, "\r\n"),
		"stderr_names_the_key_file": strings.Contains(gen.Stderr, "generated a key"),
	}
	loop.step("gen", map[string]any{"exit_code": gen.Code, "checks": genChecks, "public_key_prefix": "x25519:"})
	loop.require("gen", genChecks, tail(redact(gen.Stderr, secret)))

	// 2. Apply with the generated public key, through the capability adapter and with
	// the caller identity the delivery path records.
	applyStatus, applyEnvelope, applyRaw := h.capability(t, "repo_key_apply", map[string]any{
		"repo": e2eRepo, "purpose": "e2e: the key CLI loop", "public_key": publicKey,
	})
	loop.capture("apply", applyRaw)
	var applied applyData
	applyDecodeErr := json.Unmarshal(applyEnvelope.Data, &applied)
	applyChecks := map[string]bool{
		"http_200":        applyStatus == http.StatusOK,
		"capability_ok":   applyEnvelope.Status == "success",
		"data_decodes":    applyDecodeErr == nil,
		"status_pending":  applied.Status == "pending",
		"kind_issue":      applied.Kind == "issue",
		"has_application": applied.ApplicationID != "",
		"has_approval":    applied.ApprovalToken != "",
		"agent_recorded":  applied.Agent == e2eAgent,
		"device_recorded": applied.Device == e2eDevice,
	}
	loop.step("apply", map[string]any{
		"http_status": applyStatus, "checks": applyChecks,
		"application_id": applied.ApplicationID, "approval_link_path": "/repo-key-apply/{token}",
	})
	loop.require("apply", applyChecks, tail(redact(string(applyRaw), secret)))

	// 3. A person decides over the console's HTTP surface; no browser is involved.
	approveStatus, approveBody := h.management(t, http.MethodPost,
		"/v1/management/repo-keys/applications/"+applied.ApplicationID+"/approve",
		map[string]any{"reason": "e2e: the key CLI loop"})
	loop.capture("approve", approveBody)
	var approved struct {
		Application struct {
			Status  string `json:"status"`
			KeyMask string `json:"key_mask"`
		} `json:"application"`
	}
	approveDecodeErr := json.Unmarshal(approveBody, &approved)
	gatewayKeys := h.gateway.keys()
	if len(gatewayKeys) == 1 {
		secret = gatewayKeys[0]
	}
	approvedMask = approved.Application.KeyMask
	approveChecks := map[string]bool{
		"http_200":          approveStatus == http.StatusOK,
		"body_decodes":      approveDecodeErr == nil,
		"status_approved":   approved.Application.Status == "approved",
		"key_mask_present":  approvedMask != "",
		"gateway_key_count": len(gatewayKeys) == 1,
		"gateway_key_found": secret != "",
		"body_no_key":       !strings.Contains(string(approveBody), secret),
	}
	loop.step("approve", map[string]any{
		"http_status": approveStatus, "checks": approveChecks,
		"key_mask": approvedMask, "gateway_keys": len(gatewayKeys),
	})
	loop.require("approve", approveChecks, tail(redact(string(approveBody), secret)))

	// 4. Fetch the envelope, then hand that result to the CLI on stdin.
	cipherStatus, cipherEnvelope, cipherRaw := h.capability(t, "repo_key_ciphertext", map[string]any{
		"application_id": applied.ApplicationID,
	})
	loop.capture("ciphertext", cipherRaw)
	var delivery ciphertextData
	cipherDecodeErr := json.Unmarshal(cipherEnvelope.Data, &delivery)
	cipherChecks := map[string]bool{
		"http_200":            cipherStatus == http.StatusOK,
		"capability_ok":       cipherEnvelope.Status == "success",
		"data_decodes":        cipherDecodeErr == nil,
		"algorithm":           delivery.Algorithm == envelopeAlgorithm,
		"ciphertext_present":  delivery.Ciphertext != "",
		"mask_matches":        delivery.KeyMask == approvedMask,
		"ciphertext_not_key":  !strings.Contains(delivery.Ciphertext, secret),
		"application_matches": delivery.ApplicationID == applied.ApplicationID,
		"body_no_key":         !strings.Contains(string(cipherRaw), secret),
	}
	loop.step("ciphertext", map[string]any{
		"http_status": cipherStatus, "checks": cipherChecks,
		"envelope_sha256": sha256Hex(delivery.Ciphertext),
	})
	loop.require("ciphertext", cipherChecks, tail(redact(string(cipherRaw), secret)))

	// 5. `key accept` reads the capability result on stdin, proves the local private key
	// opens it, and stores only the envelope.
	accept := loop.run("accept", env, cipherEnvelope.Data, "key", "accept", "--repo", e2eRepo, "--home", cliHome)
	state := readCLIState(t, filepath.Join(cliHome, cliStateFilename))
	stored, hasRecord := state.Repos[repoID]
	keyPath := filepath.Join(cliHome, filepath.FromSlash(stored.PrivateKeyFile))
	envelopePath := filepath.Join(cliHome, filepath.FromSlash(stored.EnvelopeFile))
	acceptChecks := map[string]bool{
		"exit_zero":             accept.Code == 0,
		"state_record_present":  hasRecord,
		"private_key_present":   regularFileExists(keyPath),
		"envelope_present":      regularFileExists(envelopePath),
		"approval_recorded":     stored.ApprovalStatus == "approved",
		"mask_recorded":         stored.KeyMask == approvedMask,
		"stdout_names_key_mask": strings.Contains(accept.Stdout, approvedMask),
		"stdout_no_key":         !strings.Contains(accept.Stdout, secret),
		"stderr_no_key":         !strings.Contains(accept.Stderr, secret),
	}
	loop.step("accept", map[string]any{"exit_code": accept.Code, "checks": acceptChecks})
	loop.require("accept", acceptChecks, tail(redact(accept.Stderr, secret)))

	// 6. Each runtime's child process sees its own injection and, where the variables
	// allow, reaches the gateway with the key it was given.
	helper := testExecutable(t)
	claudeMarker := filepath.Join(t.TempDir(), "marker-claude")
	claudeEnv := append(cliEnvCopy(env),
		"OMC_E2E_HELPER="+helperModeMask,
		"OMC_E2E_RUNTIME=claude",
		"OMC_E2E_VERSION=e2e",
		markerEnv+"="+claudeMarker,
		"ANTHROPIC_API_KEY=e2e-decoy-that-must-not-survive",
	)
	claudeExec := loop.run("exec-claude", claudeEnv, nil,
		"key", "exec", "--runtime", "claude", "--repo", e2eRepo, "--home", cliHome,
		"--", helper, "-test.run=^TestE2EChildProcess$")
	claudeReport := decodeMaskReport(claudeExec.Stdout)
	claudeChecks := map[string]bool{
		"exit_zero":          claudeExec.Code == 0,
		"helper_ran":         claudeReport.MarkerWritten && regularFileExists(claudeMarker),
		"runtime_is_claude":  claudeReport.Runtime == "claude",
		"base_url_present":   claudeReport.Present["ANTHROPIC_BASE_URL"],
		"auth_token_present": claudeReport.Present["ANTHROPIC_AUTH_TOKEN"],
		"one_auth_token":     claudeReport.EnvCounts["ANTHROPIC_AUTH_TOKEN"] == 1,
		"api_key_removed":    !claudeReport.Present["ANTHROPIC_API_KEY"] && claudeReport.EnvCounts["ANTHROPIC_API_KEY"] == 0,
		"gateway_200":        claudeReport.Inference != nil && claudeReport.Inference.Status == http.StatusOK,
		"gateway_content":    claudeReport.Inference != nil && claudeReport.Inference.Content == "E2E_FAKE_GATEWAY_OK",
		"argv_no_key":        !claudeReport.ArgvCarriesKey,
		"parent_no_key":      !claudeReport.ParentCarriesKey,
		"stdout_no_key":      !strings.Contains(claudeExec.Stdout, secret),
		"stderr_no_key":      !strings.Contains(claudeExec.Stderr, secret),
	}
	if runtime.GOOS == "linux" {
		claudeChecks["parent_env_checked"] = claudeReport.ParentChecked
	}
	loop.step("exec-claude", map[string]any{
		"exit_code": claudeExec.Code, "checks": claudeChecks,
		"masked": claudeReport.Masked, "parent_checked": claudeReport.ParentChecked,
	})
	loop.require("exec-claude", claudeChecks, tail(redact(claudeExec.Stderr, secret)))

	// Codex only accepts a command named codex. A copy of this binary under that name
	// keeps the helper entry point while satisfying the runtime's check.
	codexCommand := copyTestBinaryAs(t, "codex")
	codexMarker := filepath.Join(t.TempDir(), "marker-codex")
	codexEnv := append(cliEnvCopy(env),
		"OMC_E2E_HELPER="+helperModeMask,
		"OMC_E2E_RUNTIME=codex",
		"OMC_E2E_VERSION=e2e",
		markerEnv+"="+codexMarker,
		"OMC_REPO_KEY=e2e-decoy-that-must-not-survive",
	)
	codexExec := loop.run("exec-codex", codexEnv, nil,
		"key", "exec", "--runtime", "codex", "--repo", e2eRepo, "--home", cliHome,
		"--", codexCommand, "-test.run=^TestE2EChildProcess$")
	codexReport := decodeMaskReport(codexExec.Stdout)
	codexBaseURL := h.gateway.baseURL() + "/v1"
	expectedOverrides := []string{
		`model_provider="oh-my-cpa"`,
		`model_providers.oh-my-cpa.name="Oh My CPA"`,
		`model_providers.oh-my-cpa.base_url="` + codexBaseURL + `"`,
		`model_providers.oh-my-cpa.env_key="OMC_REPO_KEY"`,
		`model_providers.oh-my-cpa.wire_api="responses"`,
	}
	codexChecks := map[string]bool{
		"exit_zero":         codexExec.Code == 0,
		"helper_ran":        codexReport.MarkerWritten && regularFileExists(codexMarker),
		"runtime_is_codex":  codexReport.Runtime == "codex",
		"key_present":       codexReport.Present["OMC_REPO_KEY"],
		"one_key":           codexReport.EnvCounts["OMC_REPO_KEY"] == 1,
		"overrides_present": len(codexReport.Overrides) == len(expectedOverrides),
		"gateway_200":       codexReport.Inference != nil && codexReport.Inference.Status == http.StatusOK,
		"gateway_content":   codexReport.Inference != nil && codexReport.Inference.Content == "E2E_FAKE_GATEWAY_OK",
		"argv_no_key":       !codexReport.ArgvCarriesKey,
		"parent_no_key":     !codexReport.ParentCarriesKey,
		"stdout_no_key":     !strings.Contains(codexExec.Stdout, secret),
		"stderr_no_key":     !strings.Contains(codexExec.Stderr, secret),
	}
	for index, expected := range expectedOverrides {
		codexChecks["override_"+string(rune('a'+index))] = index < len(codexReport.Overrides) && codexReport.Overrides[index] == expected
	}
	if runtime.GOOS == "linux" {
		codexChecks["parent_env_checked"] = codexReport.ParentChecked
	}
	loop.step("exec-codex", map[string]any{
		"exit_code": codexExec.Code, "checks": codexChecks,
		"overrides": codexReport.Overrides, "parent_checked": codexReport.ParentChecked,
	})
	loop.require("exec-codex", codexChecks, tail(redact(codexExec.Stderr, secret)))

	opencodeMarker := filepath.Join(t.TempDir(), "marker-opencode")
	opencodeEnv := append(cliEnvCopy(env),
		"OMC_E2E_HELPER="+helperModeMask,
		"OMC_E2E_RUNTIME=opencode",
		"OMC_E2E_VERSION=e2e",
		markerEnv+"="+opencodeMarker,
	)
	opencodeExec := loop.run("exec-opencode", opencodeEnv, nil,
		"key", "exec", "--runtime", "opencode", "--repo", e2eRepo, "--home", cliHome,
		"--", helper, "-test.run=^TestE2EChildProcess$")
	opencodeReport := decodeMaskReport(opencodeExec.Stdout)
	opencodeChecks := map[string]bool{
		"exit_zero":           opencodeExec.Code == 0,
		"helper_ran":          opencodeReport.MarkerWritten && regularFileExists(opencodeMarker),
		"runtime_is_opencode": opencodeReport.Runtime == "opencode",
		"content_present":     opencodeReport.Present["OPENCODE_CONFIG_CONTENT"],
		"one_content":         opencodeReport.EnvCounts["OPENCODE_CONFIG_CONTENT"] == 1,
		"provider_written":    containsString(opencodeReport.OpenCodeProviders, "oh-my-cpa"),
		"provider_other_kept": containsString(opencodeReport.OpenCodeProviders, "other"),
		"theme_kept":          opencodeReport.OpenCodeTheme == "dark",
		"base_url_v1":         opencodeReport.Inference != nil && strings.HasSuffix(opencodeReport.Inference.Base, "/v1"),
		"gateway_200":         opencodeReport.Inference != nil && opencodeReport.Inference.Status == http.StatusOK,
		"gateway_content":     opencodeReport.Inference != nil && opencodeReport.Inference.Content == "E2E_FAKE_GATEWAY_OK",
		"argv_no_key":         !opencodeReport.ArgvCarriesKey,
		"parent_no_key":       !opencodeReport.ParentCarriesKey,
		"stdout_no_key":       !strings.Contains(opencodeExec.Stdout, secret),
		"stderr_no_key":       !strings.Contains(opencodeExec.Stderr, secret),
	}
	if runtime.GOOS == "linux" {
		opencodeChecks["parent_env_checked"] = opencodeReport.ParentChecked
	}
	loop.step("exec-opencode", map[string]any{
		"exit_code": opencodeExec.Code, "checks": opencodeChecks,
		"providers": opencodeReport.OpenCodeProviders, "parent_checked": opencodeReport.ParentChecked,
	})
	loop.require("exec-opencode", opencodeChecks, tail(redact(opencodeExec.Stderr, secret)))

	// 7. `key install` writes the plaintext into the runtime's own user-level file and
	// backs the previous file up first. HOME and CLAUDE_CONFIG_DIR point into the
	// temporary user home, so the real ~/.claude is never touched.
	installEnv := append(cliEnvCopy(env), "CLAUDE_CONFIG_DIR="+claudeDir)
	install := loop.run("install", installEnv, nil,
		"key", "install", "--runtime", "claude", "--repo", e2eRepo, "--home", cliHome)
	installedRaw, installedErr := os.ReadFile(claudeSettings)
	var installedSettings struct {
		Env map[string]string `json:"env"`
	}
	installedDecodeErr := json.Unmarshal(installedRaw, &installedSettings)
	backupPath := findBackup(t, filepath.Join(cliHome, "backups"), originalSettings)
	backupRaw := []byte(nil)
	if backupPath != "" {
		backupRaw, _ = os.ReadFile(backupPath)
	}
	installChecks := map[string]bool{
		"exit_zero":               install.Code == 0,
		"settings_readable":       installedErr == nil,
		"settings_decode":         installedDecodeErr == nil,
		"token_is_gateway_key":    installedSettings.Env["ANTHROPIC_AUTH_TOKEN"] == secret,
		"base_url_is_gateway":     installedSettings.Env["ANTHROPIC_BASE_URL"] == h.gateway.baseURL(),
		"other_value_kept":        installedSettings.Env["OTHER"] == "1",
		"backup_present":          backupPath != "",
		"backup_is_original_file": bytes.Equal(backupRaw, originalSettings),
		"warning_says_plaintext":  strings.Contains(install.Stderr, "plaintext"),
		"stdout_no_key":           !strings.Contains(install.Stdout, secret),
		"stderr_no_key":           !strings.Contains(install.Stderr, secret),
	}
	loop.step("install", map[string]any{
		"exit_code": install.Code, "checks": installChecks,
		"config_path": claudeSettings, "backup_present": backupPath != "",
	})
	loop.require("install", installChecks, tail(redact(install.Stderr, secret)))

	// 8. Purge removes the private key, the envelope, the state record and the
	// configured values, and leaves the user's other settings alone.
	purge := loop.run("purge", installEnv, nil, "key", "purge", "--repo", e2eRepo, "--home", cliHome)
	stateAfter := readCLIState(t, filepath.Join(cliHome, cliStateFilename))
	_, recordStillThere := stateAfter.Repos[repoID]
	settingsAfterRaw, settingsAfterErr := os.ReadFile(claudeSettings)
	var settingsAfter struct {
		Env map[string]string `json:"env"`
	}
	settingsAfterDecodeErr := json.Unmarshal(settingsAfterRaw, &settingsAfter)
	_, tokenStillSet := settingsAfter.Env["ANTHROPIC_AUTH_TOKEN"]
	_, baseURLStillSet := settingsAfter.Env["ANTHROPIC_BASE_URL"]
	purgeChecks := map[string]bool{
		"exit_zero":               purge.Code == 0,
		"private_key_gone":        !regularFileExists(keyPath),
		"envelope_gone":           !regularFileExists(envelopePath),
		"state_record_gone":       !recordStillThere,
		"runtime_slots_cleared":   len(stateAfter.Runtimes) == 0,
		"settings_still_readable": settingsAfterErr == nil && settingsAfterDecodeErr == nil,
		"config_token_gone":       !tokenStillSet,
		"config_base_url_gone":    !baseURLStillSet,
		"config_other_kept":       settingsAfter.Env["OTHER"] == "1",
		"stdout_no_key":           !strings.Contains(purge.Stdout, secret),
		"stderr_no_key":           !strings.Contains(purge.Stderr, secret),
	}
	loop.step("purge", map[string]any{"exit_code": purge.Code, "checks": purgeChecks})
	loop.require("purge", purgeChecks, tail(redact(purge.Stderr, secret)))

	// 9. The same exec that worked before now fails before the child starts: exit 4, a
	// readable key_not_found, and no marker file.
	failedMarker := filepath.Join(t.TempDir(), "marker-after-purge")
	failedEnv := append(cliEnvCopy(env),
		"OMC_E2E_HELPER="+helperModeMask,
		"OMC_E2E_RUNTIME=claude",
		"OMC_E2E_VERSION=e2e",
		markerEnv+"="+failedMarker,
	)
	failed := loop.run("exec-after-purge", failedEnv, nil,
		"key", "exec", "--runtime", "claude", "--repo", e2eRepo, "--home", cliHome,
		"--", helper, "-test.run=^TestE2EChildProcess$")
	failedChecks := map[string]bool{
		"exit_code_4":                failed.Code == 4,
		"stderr_names_key_not_found": strings.Contains(failed.Stderr, "key_not_found"),
		"stderr_is_readable":         len(strings.TrimSpace(failed.Stderr)) > 0,
		"no_panic":                   !strings.Contains(failed.Stdout, "panic:") && !strings.Contains(failed.Stderr, "panic:"),
		"helper_did_not_run":         !regularFileExists(failedMarker),
		"stdout_no_key":              !strings.Contains(failed.Stdout, secret),
		"stderr_no_key":              !strings.Contains(failed.Stderr, secret),
	}
	loop.step("exec-after-purge", map[string]any{"exit_code": failed.Code, "checks": failedChecks})
	loop.require("exec-after-purge", failedChecks, tail(redact(failed.Stderr, secret)))

	// Nothing this run captured may name the key: every CLI stream and the service log.
	leaked := []string{}
	for _, output := range loop.outputs {
		if strings.Contains(output.Stdout, secret) || strings.Contains(output.Stderr, secret) {
			leaked = append(leaked, output.Step)
		}
	}
	for _, body := range loop.bodies {
		if strings.Contains(string(body.Body), secret) {
			leaked = append(leaked, body.Step+"-body")
		}
	}
	if strings.Contains(h.log(), secret) {
		leaked = append(leaked, "service-log")
	}
	noPlaintextChecks := map[string]bool{"no_plaintext_in_streams": len(leaked) == 0}
	loop.step("no-plaintext", map[string]any{"checks": noPlaintextChecks, "leaked": leaked})
	loop.require("no-plaintext", noPlaintextChecks, strings.Join(leaked, ", "))

	record.Conclusion = "pass"
	record.Steps = append(record.Steps, evidenceStep{Name: "complete", Detail: map[string]any{
		"evidence": evidencePath, "key_mask": approvedMask, "steps": len(record.Steps),
	}})
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("encode the final CLI evidence: %v", err)
	}
	if secret == "" || bytes.Contains(encoded, []byte(secret)) {
		t.Fatal("the evidence would contain the plaintext key")
	}
	if err := os.WriteFile(evidencePath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("write the final CLI evidence: %v", err)
	}
	t.Logf("key CLI loop passed: gen -> apply -> approve -> ciphertext -> accept -> exec x3 -> install -> purge -> exec refused (key mask %s); evidence at %s",
		approvedMask, evidencePath)
}

// cliEnvironment is the CLI's whole environment. It is built from a short allowlist so a
// variable in the operator's shell cannot change what a run proves; in particular no
// OMCPA_* value survives into `key exec`.
func cliEnvironment(userHome string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + userHome,
		"USERPROFILE=" + userHome,
		"TMPDIR=" + userHome,
		"TMP=" + userHome,
		"TEMP=" + userHome,
		"TZ=Asia/Shanghai",
	}
	for _, name := range []string{"SYSTEMROOT", "COMSPEC", "LANG"} {
		if value := os.Getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	return env
}

func cliEnvCopy(env []string) []string {
	return append([]string(nil), env...)
}

func testExecutable(t *testing.T) string {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate this test binary: %v", err)
	}
	return executable
}

// copyTestBinaryAs puts a copy of this test binary under another name, so a runtime that
// requires a specific command can still be exercised with the helper entry point.
func copyTestBinaryAs(t *testing.T, name string) string {
	t.Helper()
	executable := testExecutable(t)
	content, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("read this test binary: %v", err)
	}
	if filepath.Ext(executable) == ".exe" {
		name += ".exe"
	}
	copied := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(copied, content, 0o755); err != nil {
		t.Fatalf("copy the test binary as %s: %v", name, err)
	}
	return copied
}

// cliRepoState is the part of state.json this test reads. Field names are the contract;
// the file is parsed rather than a helper recomputing the paths.
type cliRepoState struct {
	Repo           string `json:"repo"`
	RepoDisplay    string `json:"repo_display"`
	PrivateKeyFile string `json:"private_key_file"`
	EnvelopeFile   string `json:"envelope_file"`
	ApprovalStatus string `json:"approval_status"`
	KeyMask        string `json:"key_mask"`
}

type cliState struct {
	Version  int                        `json:"version"`
	Repos    map[string]cliRepoState    `json:"repos"`
	Runtimes map[string]json.RawMessage `json:"runtimes"`
}

func readCLIState(t *testing.T, path string) cliState {
	t.Helper()
	state := cliState{Repos: map[string]cliRepoState{}, Runtimes: map[string]json.RawMessage{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state
		}
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if state.Repos == nil {
		state.Repos = map[string]cliRepoState{}
	}
	if state.Runtimes == nil {
		state.Runtimes = map[string]json.RawMessage{}
	}
	return state
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// findBackup returns the first backup whose bytes equal want. A backup of the original
// file is what makes the install reversible.
func findBackup(t *testing.T, directory string, want []byte) string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read %s: %v", directory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		content, err := os.ReadFile(path)
		if err == nil && bytes.Equal(content, want) {
			return path
		}
	}
	return ""
}

// decodeMaskReport reads the helper's single JSON line; an empty or malformed report
// makes the step's checks fail rather than stopping before the evidence is written.
func decodeMaskReport(stdout string) maskingReport {
	trimmed := strings.TrimSpace(stdout)
	if trimmed == "" {
		return maskingReport{}
	}
	first, _, _ := strings.Cut(trimmed, "\n")
	var report maskingReport
	if err := json.Unmarshal([]byte(first), &report); err != nil {
		return maskingReport{}
	}
	return report
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// redact keeps a failure diagnostic from printing the key.
func redact(value, secret string) string {
	if secret == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "<redacted>")
}
