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
	"strings"
	"testing"
	"time"
)

const (
	helperModeEnv     = "OMC_E2E_HELPER"
	helperModeMask    = "mask"
	helperModeDecrypt = "decrypt"
)

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
	Masked    []string          `json:"masked"`
	Inference *maskingInference `json:"inference,omitempty"`
	Version   string            `json:"version"`
}

type maskingInference struct {
	Status  int    `json:"status"`
	Content string `json:"content"`
	Base    string `json:"base"`
}

func runMaskHelper(t *testing.T) {
	runtimeName := os.Getenv("OMC_E2E_RUNTIME")
	report := maskingReport{
		Runtime: runtimeName,
		Present: map[string]bool{},
		Masked:  []string{},
		Version: os.Getenv("OMC_E2E_VERSION"),
	}
	names := []string{"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "OMC_REPO_KEY", "OPENCODE_CONFIG_CONTENT"}
	for _, name := range names {
		value, exists := os.LookupEnv(name)
		report.Present[name] = exists
		if exists && value != "" {
			report.Masked = append(report.Masked, name+"="+maskValue(value))
		}
	}

	switch runtimeName {
	case "claude":
		status, content := callAnthropicShape(os.Getenv("ANTHROPIC_BASE_URL"), os.Getenv("ANTHROPIC_AUTH_TOKEN"))
		report.Inference = &maskingInference{Status: status, Content: content, Base: os.Getenv("ANTHROPIC_BASE_URL")}
	case "codex":
		status, content := callOpenAIShape(os.Getenv("OMC_E2E_GATEWAY"), os.Getenv("OMC_REPO_KEY"))
		report.Inference = &maskingInference{Status: status, Content: content, Base: os.Getenv("OMC_E2E_GATEWAY")}
	case "opencode":
		baseURL, apiKey, err := openCodeProvider(os.Getenv("OPENCODE_CONFIG_CONTENT"))
		if err != nil {
			t.Fatalf("read the injected opencode configuration: %v", err)
		}
		status, content := callOpenAIShape(baseURL, apiKey)
		report.Inference = &maskingInference{Status: status, Content: content, Base: baseURL}
	default:
		t.Fatalf("unknown runtime %q", runtimeName)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("encode report: %v", err)
	}
	fmt.Fprintln(os.Stdout, string(encoded))
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

// openCodeProvider reads back the entry Oh My CPA is specified to write: the
// provider's baseURL and apiKey, beside whatever else the document holds.
func openCodeProvider(raw string) (baseURL, apiKey string, err error) {
	var document struct {
		Provider map[string]struct {
			Options struct {
				BaseURL string `json:"baseURL"`
				APIKey  string `json:"apiKey"`
			} `json:"options"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		return "", "", err
	}
	entry, ok := document.Provider["oh-my-cpa"]
	if !ok {
		return "", "", fmt.Errorf("no oh-my-cpa provider in the injected configuration")
	}
	if entry.Options.BaseURL == "" || entry.Options.APIKey == "" {
		return "", "", fmt.Errorf("the oh-my-cpa provider has no baseURL or apiKey")
	}
	return entry.Options.BaseURL, entry.Options.APIKey, nil
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
