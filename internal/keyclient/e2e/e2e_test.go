//go:build e2e

package e2e

// The cross-process harness for the repository-key delivery loop.
//
// It starts the real Oh My CPA binary against a loopback stand-in for the
// gateway, drives the three MCP capabilities that hand a key out (apply, status,
// ciphertext), decides the application over the console's HTTP surface, opens
// the envelope with this harness's own implementation, and proves the recovered
// key is one the gateway accepts.
//
// Everything the `key` command will eventually own is marked in this file: the
// keypair's storage, `key accept`'s envelope handling, and `key exec`'s
// per-runtime environment. Those are stand-ins by design - the follow-up slice
// replaces them and leaves the rest of the loop intact.

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	e2eRepo   = "LambdaTheory/oh-my-cpa"
	e2eAgent  = "e2e-codex"
	e2eDevice = "e2e-device"
)

// masterKey is generated per run rather than committed: the deployment cipher's
// key is a credential, and a literal that looks like one is refused by the
// repository's secret scan whatever it is really for.
func masterKey(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate the deployment key: %v", err)
	}
	return hex.EncodeToString(raw)
}

// buildOnce keeps one `go build` for the whole package run: the binary is a
// fixture, not the subject, and rebuilding it per test would dominate the run.
var (
	buildOnce   sync.Once
	builtBinary string
	buildErr    error
)

func omcBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "omc-key-e2e-bin-")
		if err != nil {
			buildErr = err
			return
		}
		path := filepath.Join(dir, "oh-my-cpa")
		// The console's embedded dist holds only .gitkeep in a source checkout,
		// which compiles, so this needs no frontend build. The service is driven
		// through its API only.
		command := exec.Command("go", "build", "-trimpath", "-o", path, "./cmd/oh-my-cpa")
		command.Dir = moduleRoot()
		if output, err := command.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("go build: %w: %s", err, tail(string(output)))
			return
		}
		builtBinary = path
	})
	if buildErr != nil {
		t.Fatalf("build the Oh My CPA binary: %v", buildErr)
	}
	return builtBinary
}

// moduleRoot walks up from the test's working directory (this package) to the
// checkout, which is where the build has to run.
func moduleRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}

// harness is one running stack: the binary, its temporary homes, and the
// gateway it was pointed at.
type harness struct {
	binary  string
	gateway *fakeCPA

	consoleURL string
	listenAddr string
	home       string
	evidence   string
	logPath    string
	masterKey  string

	process *exec.Cmd
	exited  chan struct{}
	client  *http.Client
}

func startHarness(t *testing.T) *harness {
	t.Helper()
	// Build first: the module cache and build cache belong to the checkout, and
	// nothing below should be able to affect where they live.
	binary := omcBinary(t)

	home := t.TempDir()
	evidence := os.Getenv("OMC_E2E_EVIDENCE_DIR")
	if evidence == "" {
		evidence = filepath.Join(os.TempDir(), "omc-key-e2e-evidence")
	}
	if err := os.MkdirAll(evidence, 0o700); err != nil {
		t.Fatalf("create the evidence directory: %v", err)
	}

	h := &harness{
		binary:     binary,
		gateway:    startFakeCPA(t),
		home:       home,
		evidence:   evidence,
		listenAddr: freeAddress(t),
		logPath:    filepath.Join(evidence, "omc.log"),
		masterKey:  masterKey(t),
	}
	h.consoleURL = "http://" + h.listenAddr + "/omc"
	h.start(t)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	h.client = &http.Client{Jar: jar, Timeout: 20 * time.Second}
	h.waitHealthy(t)
	// The console's management routes want a session; only the capability routes
	// take the bearer. Signing in once keeps every later step a plain request.
	h.login(t)
	return h
}

// serviceEnv is the binary's whole environment, built from nothing: a variable
// set in the operator's shell must not be able to change what this run proves.
func (h *harness) serviceEnv(t *testing.T) []string {
	t.Helper()
	envFile := filepath.Join(h.home, "empty.env")
	if err := os.WriteFile(envFile, nil, 0o600); err != nil {
		t.Fatalf("create the empty environment file: %v", err)
	}
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + h.home,
		"TMPDIR=" + h.home,
		"TZ=Asia/Shanghai",
		"OMCPA_DATA_DIR=" + filepath.Join(h.home, "data"),
		"OMCPA_MASTER_KEY=" + h.masterKey,
		"OMCPA_CPA_BASE_URL=" + h.gateway.baseURL(),
		"OMCPA_CPA_MANAGEMENT_KEY=" + fakeCPAManagementKey,
		"OMCPA_LISTEN_ADDR=" + h.listenAddr,
		"OMCPA_BASE_PATH=/omc",
		"OMCPA_ENV_FILE=" + envFile,
		"OMCPA_VERSION=e2e",
		// Only the management reads the key flow needs stay on. Usage ingestion
		// would pop the gateway's queue; the schedulers would poll it.
		"OMCPA_USAGE_INGEST_MODE=off",
		"OMCPA_QUOTA_SCHEDULER_ENABLED=false",
		"OMCPA_ACCOUNT_AUTOMATION_ENABLED=false",
		"OMCPA_OLLAMA_COLLECTOR_ENABLED=false",
		"OMCPA_FEISHU_FORCE_DISABLED=true",
		"OMCPA_UPDATE_CHECK_ENABLED=false",
	}
}

func (h *harness) start(t *testing.T) {
	t.Helper()
	logFile, err := os.Create(h.logPath)
	if err != nil {
		t.Fatalf("create the service log: %v", err)
	}
	command := exec.Command(h.binary)
	command.Env = h.serviceEnv(t)
	command.Dir = h.home
	command.Stdout = logFile
	command.Stderr = logFile
	t.Cleanup(func() { logFile.Close() })
	if err := command.Start(); err != nil {
		t.Fatalf("start Oh My CPA: %v", err)
	}
	h.process = command
	h.exited = make(chan struct{})
	go func() {
		_ = command.Wait()
		close(h.exited)
	}()
	t.Cleanup(func() {
		_ = command.Process.Kill()
		<-h.exited
	})
}

// waitHealthy polls until the console answers. It reports the service's own log
// when the process stops early rather than waiting out the deadline.
func (h *harness) waitHealthy(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	last := ""
	ok, reason := waitFor("the console to answer /api/healthz", 90*time.Second, func() bool {
		response, err := client.Get(h.consoleURL + "/api/healthz")
		if err != nil {
			last = err.Error()
			return false
		}
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode != http.StatusOK {
			last = fmt.Sprintf("%d %s", response.StatusCode, tail(string(body)))
			return false
		}
		return true
	}, h.stopped, func() string { return "last answer: " + last + "\n" + tail(h.log()) })
	if !ok {
		t.Fatal(reason)
	}
}

func (h *harness) log() string {
	raw, err := os.ReadFile(h.logPath)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (h *harness) stopped() bool {
	select {
	case <-h.exited:
		return true
	default:
		return false
	}
}

// login takes a console session. The management routes are session-only; the
// capability routes are the ones that accept the bearer.
func (h *harness) login(t *testing.T) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"password": fakeCPAManagementKey})
	if err != nil {
		t.Fatalf("encode the login: %v", err)
	}
	response, err := h.client.Post(h.consoleURL+"/api/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login answered %d: %s", response.StatusCode, tail(string(payload)))
	}
}

// capability calls one MCP capability through the console's HTTP adapter with
// the caller identity the delivery path records.
func (h *harness) capability(t *testing.T, name string, arguments any) (int, capabilityEnvelope, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		t.Fatalf("encode %s arguments: %v", name, err)
	}
	request, err := http.NewRequest(http.MethodPost, h.consoleURL+"/api/v1/capabilities/invoke", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build the %s request: %v", name, err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+fakeCPAManagementKey)
	request.Header.Set("X-Omc-Agent", e2eAgent)
	request.Header.Set("X-Omc-Device", e2eDevice)
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	var envelope capabilityEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("%s answered %d with something that is not a capability result: %s", name, response.StatusCode, tail(string(payload)))
	}
	return response.StatusCode, envelope, payload
}

// management calls a console route and returns its status and body.
func (h *harness) management(t *testing.T, method, path string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode the %s body: %v", path, err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, h.consoleURL+"/api"+path, reader)
	if err != nil {
		t.Fatalf("build %s: %v", path, err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer response.Body.Close()
	payload, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, payload
}

// capabilityEnvelope is the executor's result. Only the fields this harness
// reads appear here.
type capabilityEnvelope struct {
	Status string          `json:"status"`
	Code   string          `json:"code"`
	Detail string          `json:"detail"`
	Data   json.RawMessage `json:"data"`
}

// The delivery shapes, mirrored from the wire so a renamed field is a real
// break rather than something the server's own types would absorb.
type applyData struct {
	ApplicationID string `json:"application_id"`
	Status        string `json:"status"`
	Kind          string `json:"kind"`
	Repo          string `json:"repo"`
	RepoDisplay   string `json:"repo_display"`
	Agent         string `json:"agent"`
	Device        string `json:"device"`
	KeyMask       string `json:"key_mask"`
	ApprovalToken string `json:"approval_token"`
	ApprovalURL   string `json:"approval_url"`
}

type statusData struct {
	ApplicationID   string `json:"application_id"`
	Status          string `json:"status"`
	Kind            string `json:"kind"`
	Repo            string `json:"repo"`
	KeyMask         string `json:"key_mask"`
	CiphertextReady bool   `json:"ciphertext_ready"`
	RejectReason    string `json:"reject_reason"`
}

type ciphertextData struct {
	ApplicationID string `json:"application_id"`
	Algorithm     string `json:"algorithm"`
	KeyMask       string `json:"key_mask"`
	Ciphertext    string `json:"ciphertext"`
}

// caller is one client keypair. Stand-in for `oh-my-cpa key gen`: the pair lives
// in memory because the CLI's home layout, state file and private-key format are
// the follow-up slice's decisions.
type caller struct {
	private *ecdh.PrivateKey
	public  string
}

func newCaller(t *testing.T) caller {
	t.Helper()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate a caller key: %v", err)
	}
	return caller{private: private, public: "x25519:" + base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())}
}

// evidenceRecord is what a run leaves behind: what happened, in order, with no
// plaintext key in any field.
type evidenceRecord struct {
	Repo       string         `json:"repo"`
	Steps      []evidenceStep `json:"steps"`
	Conclusion string         `json:"conclusion"`
	Services   map[string]any `json:"services"`
}

type evidenceStep struct {
	Name   string         `json:"name"`
	Detail map[string]any `json:"detail"`
}

func (h *harness) record(t *testing.T, record *evidenceRecord, name string, detail map[string]any) {
	t.Helper()
	if detail == nil {
		detail = map[string]any{}
	}
	record.Steps = append(record.Steps, evidenceStep{Name: name, Detail: detail})
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Fatalf("encode the evidence: %v", err)
	}
	if err := os.WriteFile(h.evidencePath(), append(encoded, '\n'), 0o600); err != nil {
		t.Fatalf("write the evidence: %v", err)
	}
}

func (h *harness) evidencePath() string { return filepath.Join(h.evidence, "key-loop.json") }

func TestRepoKeyDeliveryLoop(t *testing.T) {
	h := startHarness(t)
	record := &evidenceRecord{
		Repo:       e2eRepo,
		Conclusion: "failed",
		Services: map[string]any{
			"console": h.consoleURL,
			"gateway": h.gateway.baseURL(),
			"log":     h.logPath,
		},
	}
	c := newCaller(t)
	h.record(t, record, "start", map[string]any{"agent": e2eAgent, "device": e2eDevice, "public_key_len": len(c.public)})

	// 1. Apply. The answer carries the application and the one-time link a person
	// decides on; the first application is always a person's decision.
	status, envelope, raw := h.capability(t, "repo_key_apply", map[string]any{
		"repo": e2eRepo, "purpose": "e2e: fetch this repository's client key", "public_key": c.public,
	})
	if status != http.StatusOK || envelope.Status != "success" {
		t.Fatalf("repo_key_apply failed: %d %s", status, tail(string(raw)))
	}
	var applied applyData
	if err := json.Unmarshal(envelope.Data, &applied); err != nil {
		t.Fatalf("decode the apply result: %v", err)
	}
	if applied.ApplicationID == "" || applied.ApprovalToken == "" {
		t.Fatalf("apply returned no application or no approval link: %s", tail(string(envelope.Data)))
	}
	if applied.Status != "pending" || applied.Kind != "issue" {
		t.Fatalf("the first application is %s/%s, want pending/issue", applied.Status, applied.Kind)
	}
	if applied.Agent != e2eAgent || applied.Device != e2eDevice {
		t.Fatalf("the application records %s@%s, want %s@%s", applied.Agent, applied.Device, e2eAgent, e2eDevice)
	}
	// The bound identity is folded; the spelling the caller submitted is kept for
	// display, and both have to survive the round trip.
	if applied.Repo != strings.ToLower(e2eRepo) || applied.RepoDisplay != e2eRepo {
		t.Fatalf("the application is for %q displayed as %q, want %q / %q",
			applied.Repo, applied.RepoDisplay, strings.ToLower(e2eRepo), e2eRepo)
	}
	if !strings.HasPrefix(applied.ApprovalURL, h.consoleURL) || !strings.HasSuffix(applied.ApprovalURL, "/repo-key-apply/"+applied.ApprovalToken) {
		t.Fatalf("the approval link %q does not point at this console's decision page", applied.ApprovalURL)
	}
	h.record(t, record, "apply", map[string]any{
		"application_id": applied.ApplicationID, "status": applied.Status, "kind": applied.Kind,
		// The link is one-time and opens a decision, not a key; recording the path
		// shape is what a reader needs to follow the run.
		"approval_link_path": "/repo-key-apply/{token}",
	})

	// 2. Failure path: the envelope before anyone decided, and the console's own
	// refusal beside it. The console route is where the readable sentence lives.
	consoleStatus, body := h.management(t, http.MethodPost, "/v1/management/repo-keys/applications/"+applied.ApplicationID+"/ciphertext", nil)
	if consoleStatus != http.StatusBadRequest || !strings.Contains(string(body), "application_not_approved") {
		t.Fatalf("an undecided application's ciphertext answered %d: %s", consoleStatus, tail(string(body)))
	}
	status, envelope, raw = h.capability(t, "repo_key_ciphertext", map[string]any{"application_id": applied.ApplicationID})
	if envelope.Status != "error" || envelope.Code == "" {
		t.Fatalf("the capability handed out a ciphertext before approval: %d %s", status, tail(string(raw)))
	}
	h.record(t, record, "undecided-ciphertext", map[string]any{
		"console_status": consoleStatus, "console_error": strings.TrimSpace(string(body)),
		"capability_status": envelope.Status, "capability_code": envelope.Code,
	})

	// 3. The applicant's own view before a decision.
	pending := h.status(t, applied.ApplicationID)
	if pending.Status != "pending" || pending.CiphertextReady {
		t.Fatalf("status before a decision is %s ready=%v", pending.Status, pending.CiphertextReady)
	}
	h.record(t, record, "status-pending", map[string]any{"status": pending.Status, "ciphertext_ready": pending.CiphertextReady})

	// 4. Approve over the console's HTTP surface, not a browser click.
	status, body = h.management(t, http.MethodPost, "/v1/management/repo-keys/applications/"+applied.ApplicationID+"/approve", map[string]any{"reason": "e2e harness approval"})
	if status != http.StatusOK {
		t.Fatalf("approve answered %d: %s", status, tail(string(body)))
	}
	var approved struct {
		Application struct {
			Status  string `json:"status"`
			KeyMask string `json:"key_mask"`
		} `json:"application"`
	}
	if err := json.Unmarshal(body, &approved); err != nil {
		t.Fatalf("decode the approval: %v", err)
	}
	if approved.Application.Status != "approved" || approved.Application.KeyMask == "" {
		t.Fatalf("the approval left the application %s with mask %q", approved.Application.Status, approved.Application.KeyMask)
	}
	keys := h.gateway.keys()
	if len(keys) != 1 {
		t.Fatalf("the gateway holds %d client keys after one approval, want 1", len(keys))
	}
	h.record(t, record, "approve", map[string]any{
		"status": approved.Application.Status, "key_mask": approved.Application.KeyMask, "gateway_keys": len(keys),
	})

	// 5. The applicant's view after the decision, then the envelope.
	decided := h.status(t, applied.ApplicationID)
	if decided.Status != "approved" || !decided.CiphertextReady {
		t.Fatalf("status after approval is %s ready=%v", decided.Status, decided.CiphertextReady)
	}
	if decided.KeyMask != approved.Application.KeyMask {
		t.Fatalf("status mask %q disagrees with the approval's %q", decided.KeyMask, approved.Application.KeyMask)
	}

	status, envelope, raw = h.capability(t, "repo_key_ciphertext", map[string]any{"application_id": applied.ApplicationID})
	if status != http.StatusOK || envelope.Status != "success" {
		t.Fatalf("repo_key_ciphertext failed: %d %s", status, tail(string(raw)))
	}
	var delivery ciphertextData
	if err := json.Unmarshal(envelope.Data, &delivery); err != nil {
		t.Fatalf("decode the delivery: %v", err)
	}
	if delivery.Algorithm != envelopeAlgorithm || delivery.Ciphertext == "" {
		t.Fatalf("the delivery is %q with %d bytes of ciphertext", delivery.Algorithm, len(delivery.Ciphertext))
	}
	if delivery.KeyMask != decided.KeyMask {
		t.Fatalf("delivery mask %q disagrees with status mask %q", delivery.KeyMask, decided.KeyMask)
	}
	gatewayKey := keys[0]
	if strings.Contains(delivery.Ciphertext, gatewayKey) {
		t.Fatal("the delivery carries the key in the clear")
	}

	// 6. Open the envelope. Stand-in for `key accept`, with the harness's own
	// implementation of the construction.
	secret := h.openDelivery(t, c, delivery.Ciphertext)
	if secret != gatewayKey {
		t.Fatalf("the envelope opened to a key the gateway does not hold (mask %s)", decided.KeyMask)
	}
	h.record(t, record, "deliver", map[string]any{
		"key_mask": decided.KeyMask, "envelope_sha256": sha256Hex(delivery.Ciphertext),
		"opens_to_gateway_key": secret == gatewayKey,
	})

	// 7. A child process's view of each runtime's injection, proved without
	// printing the key. These three constructions are the stand-in for
	// `oh-my-cpa key exec`.
	h.maskRuntime(t, "claude", claudeInjection(h.gateway.baseURL(), secret), secret, record)
	h.maskRuntime(t, "codex", codexInjection(h.gateway.baseURL(), secret), secret, record)
	h.maskRuntime(t, "opencode", openCodeInjection(h.gateway.baseURL(), secret), secret, record)

	// 8. A second caller with a different public key must be decided by a person
	// even though the repository already has a key (a changed key never skips a
	// decision). Its rejection is the second failure path.
	changed := newCaller(t)
	status, envelope, raw = h.capability(t, "repo_key_apply", map[string]any{
		"repo": e2eRepo, "purpose": "e2e: a changed public key", "public_key": changed.public,
	})
	if status != http.StatusOK || envelope.Status != "success" {
		t.Fatalf("the changed-key application failed: %d %s", status, tail(string(raw)))
	}
	var changedApplied applyData
	if err := json.Unmarshal(envelope.Data, &changedApplied); err != nil {
		t.Fatalf("decode the changed-key application: %v", err)
	}
	if changedApplied.Status != "pending" || changedApplied.ApplicationID == applied.ApplicationID {
		t.Fatalf("a changed public key came back %s (%s), want a new pending application",
			changedApplied.Status, changedApplied.ApplicationID)
	}

	status, body = h.management(t, http.MethodPost, "/v1/management/repo-keys/applications/"+changedApplied.ApplicationID+"/reject", map[string]any{"reason": ""})
	if status != http.StatusBadRequest {
		t.Fatalf("a rejection without a reason answered %d: %s", status, tail(string(body)))
	}
	status, body = h.management(t, http.MethodPost, "/v1/management/repo-keys/applications/"+changedApplied.ApplicationID+"/reject", map[string]any{"reason": "e2e: this caller is not the one asking"})
	if status != http.StatusOK {
		t.Fatalf("reject answered %d: %s", status, tail(string(body)))
	}
	rejected := h.status(t, changedApplied.ApplicationID)
	if rejected.Status != "rejected" || rejected.CiphertextReady || rejected.RejectReason == "" {
		t.Fatalf("a rejected application reads %s ready=%v reason=%q", rejected.Status, rejected.CiphertextReady, rejected.RejectReason)
	}
	consoleStatus, body = h.management(t, http.MethodPost, "/v1/management/repo-keys/applications/"+changedApplied.ApplicationID+"/ciphertext", nil)
	if consoleStatus != http.StatusBadRequest || !strings.Contains(string(body), "application_not_approved") {
		t.Fatalf("a rejected application's ciphertext answered %d: %s", consoleStatus, tail(string(body)))
	}
	if len(h.gateway.keys()) != 1 {
		t.Fatalf("the rejection changed the gateway's key list (%d keys)", len(h.gateway.keys()))
	}
	h.record(t, record, "reject", map[string]any{
		"status": rejected.Status, "reject_reason": rejected.RejectReason, "ciphertext_status": consoleStatus,
		"gateway_keys": len(h.gateway.keys()),
	})

	// 9. Nothing this run wrote may name the key.
	mustNotContain(t, []byte(h.log()), secret)
	written, err := os.ReadFile(h.evidencePath())
	if err != nil {
		t.Fatalf("read the evidence: %v", err)
	}
	mustNotContain(t, written, secret)

	record.Conclusion = "pass"
	h.record(t, record, "complete", map[string]any{"evidence": h.evidencePath()})
	t.Logf("key loop passed: apply -> approve -> ciphertext -> local open -> gateway accept (key mask %s); evidence at %s",
		decided.KeyMask, h.evidencePath())
}

// status asks for one application's state and decodes it.
func (h *harness) status(t *testing.T, applicationID string) statusData {
	t.Helper()
	status, envelope, raw := h.capability(t, "repo_key_status", map[string]any{"application_id": applicationID})
	if status != http.StatusOK || envelope.Status != "success" {
		t.Fatalf("repo_key_status for %s failed: %d %s", applicationID, status, tail(string(raw)))
	}
	var result statusData
	if err := json.Unmarshal(envelope.Data, &result); err != nil {
		t.Fatalf("decode the status result: %v", err)
	}
	return result
}

// openDelivery decrypts one envelope in the same child-process opener the
// masking helpers use, so the harness has exactly one decryption implementation
// on this side of the boundary and its own test process never prints a report
// that could carry the plaintext.
func (h *harness) openDelivery(t *testing.T, c caller, ciphertext string) string {
	t.Helper()
	argv := []string{os.Args[0], "-test.run=^TestE2EChildProcess$"}
	env := append(os.Environ(), helperModeEnv+"="+helperModeDecrypt, "OMC_E2E_PRIVATE_KEY="+base64.StdEncoding.EncodeToString(c.private.Bytes()))
	command := exec.Command(argv[0], argv[1:]...)
	command.Env = env
	command.Stdin = strings.NewReader(ciphertext)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("the envelope opener failed: %v\n%s", err, tail(stderr.String()))
	}
	plaintext := strings.TrimSpace(stdout.String())
	if plaintext == "" {
		t.Fatal("the envelope opener printed nothing")
	}
	return plaintext
}

// runtimeInjection is one runtime's environment construction: what to set and
// what conflicting variable to drop. It is the stand-in for `key exec`'s
// per-runtime branch.
type runtimeInjection struct {
	env    []string
	remove []string
}

// claudeInjection sets the two variables Claude Code reads and drops the API key
// variable that would otherwise take precedence over them.
func claudeInjection(gateway, secret string) runtimeInjection {
	return runtimeInjection{
		env: []string{
			"ANTHROPIC_BASE_URL=" + strings.TrimRight(gateway, "/"),
			"ANTHROPIC_AUTH_TOKEN=" + secret,
		},
		remove: []string{"ANTHROPIC_API_KEY"},
	}
}

// codexInjection puts the key in the environment only: a token in argv is
// readable by every process on the machine.
func codexInjection(gateway, secret string) runtimeInjection {
	return runtimeInjection{env: []string{"OMC_REPO_KEY=" + secret, "OMC_E2E_GATEWAY=" + strings.TrimRight(gateway, "/") + "/v1"}}
}

// openCodeInjection merges the key into a document that already holds another
// provider, which is what the operator's configuration looks like.
func openCodeInjection(gateway, secret string) runtimeInjection {
	document := map[string]any{
		"provider": map[string]any{
			"other": map[string]any{"options": map[string]any{"apiKey": "not-ours"}},
			"oh-my-cpa": map[string]any{
				"npm":  "@ai-sdk/openai-compatible",
				"name": "Oh My CPA (e2e)",
				"options": map[string]any{
					"baseURL": strings.TrimRight(gateway, "/") + "/v1",
					"apiKey":  secret,
				},
			},
		},
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		panic(err)
	}
	return runtimeInjection{env: []string{"OPENCODE_CONFIG_CONTENT=" + string(encoded)}}
}

// maskRuntime runs the helper as one runtime's child process and asserts what it
// saw, including that it reached the gateway with the key it was given rather
// than a copy the harness passed separately.
func (h *harness) maskRuntime(t *testing.T, runtimeName string, injection runtimeInjection, secret string, record *evidenceRecord) {
	t.Helper()
	argv := []string{os.Args[0], "-test.run=^TestE2EChildProcess$"}
	for _, argument := range argv {
		if strings.Contains(argument, secret) {
			t.Fatalf("the %s child's argv carries the key", runtimeName)
		}
	}

	// A decoy for every variable the injection has to remove: without one, a
	// removal that silently stopped happening would look exactly like success.
	// The decoys go in before the filter, so the filter is what removes them.
	base := os.Environ()
	for _, name := range injection.remove {
		base = append(base, name+"=e2e-decoy-that-must-not-survive")
	}
	env := filterEnv(base, injection.remove)
	env = append(env,
		helperModeEnv+"="+helperModeMask,
		"HOME="+h.home,
		"TMPDIR="+h.home,
		"OMC_E2E_RUNTIME="+runtimeName,
		"OMC_E2E_VERSION=e2e",
	)
	env = append(env, injection.env...)

	command := exec.Command(argv[0], argv[1:]...)
	command.Env = env
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("the %s helper failed: %v\n%s", runtimeName, err, tail(stderr.String()))
	}
	mustNotContain(t, stdout.Bytes(), secret)
	mustNotContain(t, stderr.Bytes(), secret)

	var report maskingReport
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &report); err != nil {
		t.Fatalf("the %s helper printed no report: %v %s", runtimeName, err, tail(stdout.String()))
	}
	if report.Runtime != runtimeName {
		t.Fatalf("the helper reported runtime %q, want %q", report.Runtime, runtimeName)
	}
	for _, name := range injectedNames[runtimeName] {
		if !report.Present[name] {
			t.Fatalf("%s was not set for the %s child", name, runtimeName)
		}
	}
	for _, name := range injection.remove {
		if report.Present[name] {
			t.Fatalf("%s survived into the %s child and would take precedence over the injected key", name, runtimeName)
		}
	}
	if report.Inference == nil || report.Inference.Status != http.StatusOK {
		t.Fatalf("the %s child could not use the key it was given: %+v", runtimeName, report.Inference)
	}
	if report.Inference.Content != "E2E_FAKE_GATEWAY_OK" {
		t.Fatalf("the %s child reached the gateway but read %q", runtimeName, report.Inference.Content)
	}
	h.record(t, record, "mask-"+runtimeName, map[string]any{
		"present": report.Present, "masked": report.Masked, "gateway_status": report.Inference.Status,
	})
}

// filterEnv drops the named variables, which is how a runtime's conflicting
// settings are removed for its child.
func filterEnv(base, remove []string) []string {
	env := []string{}
	for _, entry := range base {
		name, _, _ := strings.Cut(entry, "=")
		drop := false
		for _, unwanted := range remove {
			if strings.EqualFold(name, unwanted) {
				drop = true
				break
			}
		}
		if !drop {
			env = append(env, entry)
		}
	}
	return env
}

// mustNotContain fails when a capture names the key.
func mustNotContain(t *testing.T, capture []byte, secret string) {
	t.Helper()
	if secret == "" {
		t.Fatal("the harness has no key to check the capture against")
	}
	if bytes.Contains(capture, []byte(secret)) {
		t.Fatal("a capture from this run contains the plaintext key")
	}
}

// freeAddress reserves a loopback port by binding and releasing it. The service
// is then polled for health, so a port taken in between fails the start instead
// of being read as a passing run.
func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a loopback port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release the loopback port: %v", err)
	}
	return address
}

func sha256Hex(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// tail keeps a failure readable when a body is a whole page.
func tail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2000 {
		return value[len(value)-2000:]
	}
	return value
}
