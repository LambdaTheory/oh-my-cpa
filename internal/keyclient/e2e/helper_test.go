//go:build e2e

package e2e

// Child processes the harness re-executes from this test binary, so the harness
// needs no Node or second toolchain:
//
//   - the masking helper stands in for a runtime's child process. It reports
//     which injected variables are present and in what shape, never a value,
//     and uses the value it was handed against the fixture gateway;
//   - the envelope opener decrypts a delivery envelope with the caller's private
//     key using its own implementation of the construction, so a bug shared with
//     internal/repokey cannot make both sides agree.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

const (
	helperModeEnv     = "OMC_E2E_HELPER"
	helperModeMask    = "mask"
	helperModeDecrypt = "decrypt"

	// markerEnv names a file the helper creates when it starts. A run that expects the
	// child never to start sets it and asserts the file is absent.
	markerEnv = "OMC_E2E_MARKER"
)

// codexOverrides holds the `-c` assignments `key exec --runtime codex` inserts. They
// arrive as ordinary arguments, so the child takes them out before the testing package
// parses its own flags; keeping them lets the report say what the runtime was handed.
var codexOverrides []string

// TestMain lets the helper binary accept the flags Codex would receive. The stripping is
// confined to child mode, so an ordinary `go test` run is unaffected.
func TestMain(m *testing.M) {
	if os.Getenv(helperModeEnv) != "" {
		os.Args, codexOverrides = stripCodexOverrides(os.Args)
	}
	os.Exit(m.Run())
}

// stripCodexOverrides removes `-c <assignment>` pairs from the child's arguments.
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

// injectedNames lists the variables each runtime's injection is expected to
// set. Anything else the helper finds set is reported too, so an accidental
// extra is visible rather than hidden.
var injectedNames = map[string][]string{
	"claude":   {"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN"},
	"codex":    {"OMC_REPO_KEY"},
	"opencode": {"OPENCODE_CONFIG_CONTENT"},
}

// TestE2EChildProcess is the single entry point for both helpers, because a Go
// test binary runs one test per invocation without extra flags.
func TestE2EChildProcess(t *testing.T) {
	switch os.Getenv(helperModeEnv) {
	case helperModeMask:
		runMaskHelper(t)
		os.Exit(0)
	case helperModeDecrypt:
		runDecryptHelper(t)
		os.Exit(0)
	default:
		t.Skip("child-process entry point; the harness runs it")
	}
}

// maskingReport is what the helper prints. Presence and shape only: the point of
// the helper is that no value reaches an output stream.
type maskingReport struct {
	Runtime   string            `json:"runtime"`
	Present   map[string]bool   `json:"present"`
	EnvCounts map[string]int    `json:"env_counts"`
	Masked    []string          `json:"masked"`
	Inference *maskingInference `json:"inference,omitempty"`
	Version   string            `json:"version"`

	// These are the non-value facts the CLI slice has to prove: the child's own
	// arguments do not carry the key, the process that started it does not carry the
	// key either, and the helper actually ran.
	ArgvCarriesKey   bool `json:"argv_carries_key"`
	ParentChecked    bool `json:"parent_checked"`
	ParentCarriesKey bool `json:"parent_carries_key"`
	MarkerWritten    bool `json:"marker_written"`

	// Codex receives provider overrides in argv and a key in the environment; OpenCode
	// receives one merged document. Reporting the shape lets the caller assert both.
	Overrides         []string `json:"overrides,omitempty"`
	OpenCodeProviders []string `json:"opencode_providers,omitempty"`
	OpenCodeModels    []string `json:"opencode_models,omitempty"`
	OpenCodeTheme     string   `json:"opencode_theme,omitempty"`
}

type maskingInference struct {
	Status  int    `json:"status"`
	Content string `json:"content"`
	Base    string `json:"base"`
}

func runMaskHelper(t *testing.T) {
	runtimeName := os.Getenv("OMC_E2E_RUNTIME")
	report := maskingReport{
		Runtime:   runtimeName,
		Present:   map[string]bool{},
		EnvCounts: map[string]int{},
		Masked:    []string{},
		Version:   os.Getenv("OMC_E2E_VERSION"),
	}

	if marker := strings.TrimSpace(os.Getenv(markerEnv)); marker != "" {
		if err := os.WriteFile(marker, []byte(runtimeName), 0o600); err != nil {
			t.Fatalf("write the child marker: %v", err)
		}
		report.MarkerWritten = true
	}

	names := []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OMC_REPO_KEY", "OPENCODE_CONFIG_CONTENT"}
	for _, name := range names {
		value, exists := os.LookupEnv(name)
		report.Present[name] = exists
		for _, entry := range os.Environ() {
			entryName, _, found := strings.Cut(entry, "=")
			if found && sameEnvName(entryName, name) {
				report.EnvCounts[name]++
			}
		}
		if exists && value != "" {
			report.Masked = append(report.Masked, name+"="+maskValue(value))
		}
	}

	secret := ""
	switch runtimeName {
	case "claude":
		secret = os.Getenv("ANTHROPIC_AUTH_TOKEN")
		status, content := callAnthropicShape(os.Getenv("ANTHROPIC_BASE_URL"), secret)
		report.Inference = &maskingInference{Status: status, Content: content, Base: os.Getenv("ANTHROPIC_BASE_URL")}
	case "codex":
		secret = os.Getenv("OMC_REPO_KEY")
		report.Overrides = append([]string(nil), codexOverrides...)
		gateway := codexGatewayFromOverrides(codexOverrides)
		if gateway == "" {
			// The direct-helper path in the earlier harness passes the gateway
			// separately; `key exec` is expected to put it in the override.
			gateway = os.Getenv("OMC_E2E_GATEWAY")
		}
		status, content := callOpenAIShape(gateway, secret)
		report.Inference = &maskingInference{Status: status, Content: content, Base: gateway}
	case "opencode":
		observation, err := readOpenCodeObservation(os.Getenv("OPENCODE_CONFIG_CONTENT"))
		if err != nil {
			t.Fatalf("read the injected opencode configuration: %v", err)
		}
		secret = observation.APIKey
		report.OpenCodeProviders = observation.Providers
		report.OpenCodeModels = observation.Models
		report.OpenCodeTheme = observation.Theme
		status, content := callOpenAIShape(observation.BaseURL, secret)
		report.Inference = &maskingInference{Status: status, Content: content, Base: observation.BaseURL}
	default:
		t.Fatalf("unknown runtime %q", runtimeName)
	}

	report.ArgvCarriesKey = argumentsCarry(os.Args, secret) || argumentsCarry(codexOverrides, secret)
	report.ParentChecked, report.ParentCarriesKey = parentEnvironmentCarries(secret)

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("encode report: %v", err)
	}
	fmt.Fprintln(os.Stdout, string(encoded))
}

// sameEnvName compares variable names the way the platform does.
func sameEnvName(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// argumentsCarry reports whether any argument contains the key.
func argumentsCarry(arguments []string, secret string) bool {
	if secret == "" {
		return false
	}
	for _, argument := range arguments {
		if strings.Contains(argument, secret) {
			return true
		}
	}
	return false
}

// parentEnvironmentCarries reads the starting environment of the process that started
// this helper. On Linux that is the `key exec` process; /proc is the only portable way
// to observe it from the child. Elsewhere the check is reported as unavailable rather
// than guessed at.
func parentEnvironmentCarries(secret string) (checked, carries bool) {
	if secret == "" || runtime.GOOS != "linux" {
		return false, false
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", os.Getppid()))
	if err != nil {
		return false, false
	}
	return true, bytes.Contains(raw, []byte(secret))
}

// codexGatewayFromOverrides reads the provider base URL `key exec` inserted, so the
// helper reaches the same gateway the runtime was configured for.
func codexGatewayFromOverrides(overrides []string) string {
	const prefix = `model_providers.oh-my-cpa.base_url="`
	for _, override := range overrides {
		if strings.HasPrefix(override, prefix) && strings.HasSuffix(override, `"`) {
			return strings.TrimSuffix(strings.TrimPrefix(override, prefix), `"`)
		}
	}
	return ""
}

// maskValue names the variable's length and first two characters. Enough for a
// human to see that the right thing was set, not enough to use it.
func maskValue(value string) string {
	if len(value) <= 8 {
		return fmt.Sprintf("••••••••(%d chars)", len(value))
	}
	return fmt.Sprintf("%s••••••••(%d chars)", value[:2], len(value))
}

// callAnthropicShape uses the runtime's own two variables, so a successful call
// is proof that the injection is the only thing that put a usable key there.
func callAnthropicShape(baseURL, token string) (int, string) {
	body := map[string]any{"model": "claude-e2e", "max_tokens": 16, "messages": []any{map[string]any{"role": "user", "content": "ping"}}}
	var response struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	status, err := postJSON(strings.TrimRight(baseURL, "/")+"/v1/messages", token, body, &response)
	if err != nil {
		return status, "request failed: " + err.Error()
	}
	if len(response.Content) == 0 {
		return status, ""
	}
	return status, response.Content[0].Text
}

func callOpenAIShape(baseURL, key string) (int, string) {
	body := map[string]any{"model": "gpt-e2e", "messages": []any{map[string]any{"role": "user", "content": "ping"}}}
	var response struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	status, err := postJSON(strings.TrimRight(baseURL, "/")+"/chat/completions", key, body, &response)
	if err != nil {
		return status, "request failed: " + err.Error()
	}
	if len(response.Choices) == 0 {
		return status, ""
	}
	return status, response.Choices[0].Message.Content
}

func postJSON(endpoint, bearer string, body, output any) (int, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	if err != nil {
		return response.StatusCode, err
	}
	if output != nil {
		_ = json.Unmarshal(raw, output)
	}
	return response.StatusCode, nil
}

// openCodeObservation is what the child can see in the merged OpenCode document: the
// entry Oh My CPA wrote, and enough of the rest to prove it was merged rather than
// replaced.
type openCodeObservation struct {
	BaseURL   string
	APIKey    string
	Providers []string
	Models    []string
	Theme     string
}

// readOpenCodeObservation reads back the entry Oh My CPA is specified to write, beside
// whatever else the document holds.
func readOpenCodeObservation(raw string) (openCodeObservation, error) {
	var document struct {
		Theme    string `json:"theme"`
		Provider map[string]struct {
			Models  map[string]any `json:"models"`
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return openCodeObservation{}, err
	}
	entry, ok := document.Provider["oh-my-cpa"]
	if !ok {
		return openCodeObservation{}, fmt.Errorf("no oh-my-cpa provider in the injected configuration")
	}
	if entry.Options.BaseURL == "" || entry.Options.APIKey == "" {
		return openCodeObservation{}, fmt.Errorf("the oh-my-cpa provider has no baseURL or apiKey")
	}
	providers := make([]string, 0, len(document.Provider))
	for name := range document.Provider {
		providers = append(providers, name)
	}
	sort.Strings(providers)
	models := make([]string, 0, len(entry.Models))
	for id := range entry.Models {
		models = append(models, id)
	}
	sort.Strings(models)
	return openCodeObservation{
		BaseURL:   entry.Options.BaseURL,
		APIKey:    entry.Options.APIKey,
		Providers: providers,
		Models:    models,
		Theme:     document.Theme,
	}, nil
}

// envelope is the delivery format the server writes and this helper opens.
type envelope struct {
	Version            int    `json:"v"`
	Algorithm          string `json:"alg"`
	EphemeralPublicKey string `json:"epk"`
	Nonce              string `json:"nonce"`
	Ciphertext         string `json:"ciphertext"`
}

const envelopeAlgorithm = "x25519-hkdf-sha256-aes256gcm"

// runDecryptHelper opens one envelope from standard input and prints the
// plaintext. It is the stand-in for `key accept` and never writes the key down.
func runDecryptHelper(t *testing.T) {
	raw, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		t.Fatalf("read the envelope: %v", err)
	}
	private, err := ecdh.X25519().NewPrivateKey(decodePrivateKey(os.Getenv("OMC_E2E_PRIVATE_KEY")))
	if err != nil {
		t.Fatalf("load the caller's private key: %v", err)
	}
	plaintext, err := openEnvelope(private, strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("open the envelope: %v", err)
	}
	fmt.Fprintln(os.Stdout, plaintext)
}

func decodePrivateKey(value string) []byte {
	value = strings.TrimSpace(value)
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil
	}
	return raw
}

// openEnvelope is this harness's own implementation of the delivery
// construction: X25519, HKDF-SHA256 over the shared secret with this flow's
// label, then AES-256-GCM.
func openEnvelope(private *ecdh.PrivateKey, encoded string) (string, error) {
	var box envelope
	if err := json.Unmarshal([]byte(encoded), &box); err != nil {
		return "", fmt.Errorf("decode the envelope: %w", err)
	}
	if box.Version != 1 || box.Algorithm != envelopeAlgorithm {
		return "", fmt.Errorf("unsupported envelope v=%d alg=%q", box.Version, box.Algorithm)
	}
	ephemeralRaw, err := base64.StdEncoding.DecodeString(box.EphemeralPublicKey)
	if err != nil {
		return "", fmt.Errorf("decode the ephemeral key: %w", err)
	}
	ephemeral, err := ecdh.X25519().NewPublicKey(ephemeralRaw)
	if err != nil {
		return "", fmt.Errorf("load the ephemeral key: %w", err)
	}
	shared, err := private.ECDH(ephemeral)
	if err != nil {
		return "", fmt.Errorf("derive the shared secret: %w", err)
	}
	key, err := hkdf.Key(sha256.New, shared, nil, "omc-repo-key-delivery-v1", 32)
	if err != nil {
		return "", fmt.Errorf("derive the envelope key: %w", err)
	}
	nonce, err := base64.StdEncoding.DecodeString(box.Nonce)
	if err != nil {
		return "", fmt.Errorf("decode the nonce: %w", err)
	}
	sealed, err := base64.StdEncoding.DecodeString(box.Ciphertext)
	if err != nil {
		return "", fmt.Errorf("decode the ciphertext: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("the envelope did not open with this private key")
	}
	return string(plaintext), nil
}
