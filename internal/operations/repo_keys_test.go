package operations

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/capability"
	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/crypto"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

var repoKeyTestCounter atomic.Uint64

// fakeGateway is CPA's side of the flow: a mutable client key list behind the two
// management endpoints the repository key code touches.
type fakeGateway struct {
	mu   sync.Mutex
	keys []string
}

func (g *fakeGateway) handler() http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch {
		// The gate every management request passes: a gateway that does not answer 8 here
		// is treated as older or misconfigured before any of the routes below is reached.
		case strings.HasSuffix(request.URL.Path, "/config/config-version") && request.Method == http.MethodGet:
			_, _ = writer.Write([]byte("8"))
		case strings.HasSuffix(request.URL.Path, "/config.yaml") && request.Method == http.MethodGet:
			_, _ = writer.Write([]byte("config-version: 8\naccess:\n  api-keys: []\n"))
		case strings.HasSuffix(request.URL.Path, "/config") && request.Method == http.MethodGet:
			_ = json.NewEncoder(writer).Encode(map[string]any{"access": map[string]any{"api-keys": g.keys}})
		case strings.HasSuffix(request.URL.Path, "/config") && request.Method == http.MethodPatch:
			var patch map[string]any
			if err := json.NewDecoder(request.Body).Decode(&patch); err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if access, ok := patch["access"].(map[string]any); ok {
				if raw, ok := access["api-keys"].([]any); ok {
					keys := make([]string, 0, len(raw))
					for _, item := range raw {
						if key, ok := item.(string); ok {
							keys = append(keys, key)
						}
					}
					g.keys = keys
				}
			}
			_, _ = writer.Write([]byte(`{"status":"ok"}`))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	})
}

func (g *fakeGateway) clientKeys() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.keys...)
}

func newRepoKeyService(t *testing.T) (*Service, *fakeGateway) {
	t.Helper()
	gateway := &fakeGateway{keys: []string{"sk-cpa-legacy"}}
	server := httptest.NewServer(gateway.handler())
	t.Cleanup(server.Close)
	client, err := management.NewClient(server.URL, "management-key-value", 5*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("file:memdb_repokeysvc_%d?mode=memory&cache=shared", repoKeyTestCounter.Add(1))
	sealer, err := crypto.New("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	database, err := repository.Open(context.Background(), name, repository.WithCipher(sealer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.SQL.Exec(`
		INSERT INTO cpa_instances (id, name, base_url, usage_addr,
			management_key_ciphertext, management_key_nonce, status, created_at, updated_at)
		VALUES ('default', 'Default', 'http://127.0.0.1:8317', '127.0.0.1:8317',
			x'00', x'00', 'unknown', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	return &Service{
		Repo:   repository.New(database),
		Client: func(context.Context) (*management.Client, error) { return client, nil },
	}, gateway
}

func newCallerKeyPair(t *testing.T) (*ecdh.PrivateKey, string) {
	t.Helper()
	private, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private, "x25519:" + base64.StdEncoding.EncodeToString(private.PublicKey().Bytes())
}

func callerContext(agent, device string) context.Context {
	return capability.WithCaller(context.Background(), capability.Caller{Agent: agent, Device: device})
}

// The whole closed loop: ask, apply, approve, fetch, decrypt. Nothing in the intermediate
// answers carries the key (AC-3, AC-5, AC-24).
func TestRepoKeyApplicationLifecycle(t *testing.T) {
	service, gateway := newRepoKeyService(t)
	ctx := callerContext("claude-hl", "laptop-a")
	privateKey, publicKey := newCallerKeyPair(t)

	query, err := service.QueryRepoKey(ctx, RepoKeyQueryInput{Repo: "https://github.com/LambdaTheory/oh-my-cpa.git"})
	if err != nil {
		t.Fatal(err)
	}
	if query.State != "none" || query.NextKind != "issue" || query.Repo != "LambdaTheory/oh-my-cpa" || !query.RepoHasRemote {
		t.Fatalf("query on a fresh repo = %+v", query)
	}

	applied, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{
		Repo: "LambdaTheory/oh-my-cpa", Purpose: "开发时调模型", PublicKey: publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Status != "pending" || applied.Kind != "issue" || applied.ApplicationID == "" || applied.ApprovalToken == "" {
		t.Fatalf("application = %+v", applied)
	}
	if strings.Contains(fmt.Sprintf("%+v", applied), "sk-cpa-") {
		t.Fatalf("the application answer carries a key: %+v", applied)
	}

	// A second application from the same agent waits on the first one (AC-28).
	again, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{
		Repo: "LambdaTheory/oh-my-cpa", Purpose: "开发时调模型", PublicKey: publicKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again.ApplicationID != applied.ApplicationID {
		t.Fatalf("a second application queued separately: %s vs %s", again.ApplicationID, applied.ApplicationID)
	}
	if len(gateway.clientKeys()) != 1 {
		t.Fatalf("an application alone must not create a key: %v", gateway.clientKeys())
	}

	approved, err := service.ApproveRepoKeyApplication(context.Background(), applied.ApplicationID, "operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != "approved" || approved.Kind != "issue" || approved.KeyMask == "" || approved.Ciphertext == "" {
		t.Fatalf("approved application = %+v", approved)
	}
	keys := gateway.clientKeys()
	if len(keys) != 2 {
		t.Fatalf("approval did not issue exactly one key: %v", keys)
	}

	// Idempotent: the second click (another tab, the one-time link) decides nothing again.
	secondApproval, err := service.ApproveRepoKeyApplication(context.Background(), applied.ApplicationID, "other@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if secondApproval.DecidedBy != "operator@example.com" || len(gateway.clientKeys()) != 2 {
		t.Fatalf("a second approval changed the outcome: %+v", secondApproval)
	}

	delivery, err := service.FetchRepoKeyCiphertext(ctx, applied.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	if delivery.Kind != "issue" || delivery.Algorithm != repokey.Algorithm {
		t.Fatalf("delivery = %+v", delivery)
	}
	plaintext, err := repokey.Decrypt(privateKey.Bytes(), delivery.Ciphertext)
	if err != nil {
		t.Fatalf("the caller cannot open the envelope: %v", err)
	}
	if !containsString(gateway.clientKeys(), plaintext) {
		t.Fatalf("the envelope did not carry the issued key")
	}
	if strings.Contains(delivery.Ciphertext, plaintext) {
		t.Fatal("the ciphertext contains the plaintext")
	}

	// A second fetch returns the same envelope and records a second delivery (AC-33).
	repeat, err := service.FetchRepoKeyCiphertext(ctx, applied.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	if repeat.Ciphertext != delivery.Ciphertext {
		t.Fatal("re-fetch produced a different envelope")
	}
	deliveries, err := service.Repo.ListRepoKeyDeliveries(context.Background(), "default", approved.BindingID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 2 {
		t.Fatalf("delivery records = %d, want 2", len(deliveries))
	}
	awaiting, err := service.Repo.ListRepoKeyAwaitingDelivery(context.Background(), "default", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(awaiting) != 0 {
		t.Fatalf("a delivered application still waits for delivery: %+v", awaiting)
	}
}

// The same repository hands the same key to a second caller after one approval, and to an
// already-accepted caller without asking again (AC-6, 免审).
func TestRepoKeyReuseAndAutoApproval(t *testing.T) {
	service, gateway := newRepoKeyService(t)
	firstCtx := callerContext("claude-hl", "laptop-a")
	firstPrivate, firstPublic := newCallerKeyPair(t)
	first, err := service.ApplyRepoKey(firstCtx, RepoKeyApplyInput{Repo: "org/repo", Purpose: "build", PublicKey: firstPublic})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.ApproveRepoKeyApplication(context.Background(), first.ApplicationID, "operator@example.com"); err != nil {
		t.Fatal(err)
	}
	issuedKeys := gateway.clientKeys()

	// A different agent on a different device: reuse, but a person decides first.
	secondCtx := callerContext("codex", "desktop-b")
	secondPrivate, secondPublic := newCallerKeyPair(t)
	query, err := service.QueryRepoKey(secondCtx, RepoKeyQueryInput{Repo: "org/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if query.State != "active" || query.NextKind != "reuse" || query.AutoApprove {
		t.Fatalf("query for a new caller = %+v", query)
	}
	second, err := service.ApplyRepoKey(secondCtx, RepoKeyApplyInput{Repo: "org/repo", Purpose: "tests", PublicKey: secondPublic})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "pending" || second.Kind != "reuse" {
		t.Fatalf("a new caller skipped approval: %+v", second)
	}
	approvedSecond, err := service.ApproveRepoKeyApplication(context.Background(), second.ApplicationID, "operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if approvedSecond.Kind != "reuse" {
		t.Fatalf("approval = %+v, want reuse", approvedSecond)
	}
	if len(gateway.clientKeys()) != len(issuedKeys) {
		t.Fatalf("reuse issued a new key: %v", gateway.clientKeys())
	}
	delivery, err := service.FetchRepoKeyCiphertext(secondCtx, second.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	secondPlaintext, err := repokey.Decrypt(secondPrivate.Bytes(), delivery.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	firstDelivery, err := service.FetchRepoKeyCiphertext(firstCtx, first.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	firstPlaintext, err := repokey.Decrypt(firstPrivate.Bytes(), firstDelivery.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if secondPlaintext != firstPlaintext {
		t.Fatal("two callers of one repository received different keys")
	}

	// The accepted caller asks again: no human, the envelope comes straight back (免审).
	again, err := service.ApplyRepoKey(secondCtx, RepoKeyApplyInput{Repo: "org/repo", Purpose: "tests", PublicKey: secondPublic})
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != "approved" || again.Kind != "reuse" || again.ApplicationID == "" {
		t.Fatalf("an accepted caller was made to wait: %+v", again)
	}
	auto, err := service.FetchRepoKeyCiphertext(secondCtx, again.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	if autoPlaintext, err := repokey.Decrypt(secondPrivate.Bytes(), auto.Ciphertext); err != nil || autoPlaintext != firstPlaintext {
		t.Fatalf("auto-approved delivery failed: %v", err)
	}
	if len(gateway.clientKeys()) != len(issuedKeys) {
		t.Fatal("auto-approval issued a new key")
	}

	// Changing the public key sends the same caller back through a person (AC-34).
	_, changedPublic := newCallerKeyPair(t)
	changed, err := service.ApplyRepoKey(secondCtx, RepoKeyApplyInput{Repo: "org/repo", Purpose: "tests", PublicKey: changedPublic})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Status != "pending" {
		t.Fatalf("a changed public key was auto-approved: %+v", changed)
	}
}

// Stopping removes the key from the gateway and keeps every record; a re-application needs a
// new key, and the old one never comes back (AC-10, AC-12).
func TestRepoKeyStopAndReissue(t *testing.T) {
	service, gateway := newRepoKeyService(t)
	ctx := callerContext("claude-hl", "laptop-a")
	privateKey, publicKey := newCallerKeyPair(t)
	applied, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: "org/stopped", Purpose: "build", PublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.ApproveRepoKeyApplication(context.Background(), applied.ApplicationID, "operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := service.FetchRepoKeyCiphertext(ctx, applied.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	originalKey, err := repokey.Decrypt(privateKey.Bytes(), delivery.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}

	stopped, err := service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator@example.com", "rotate", "quarterly rotation")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != "stopped" || stopped.StopCategory != "rotate" {
		t.Fatalf("stopped binding = %+v", stopped)
	}
	if containsString(gateway.clientKeys(), originalKey) {
		t.Fatal("the stopped key is still in the gateway")
	}

	// Records survive the stop.
	stillThere, err := service.Repo.GetRepoKeyBindingByID(context.Background(), approved.BindingID)
	if err != nil || stillThere.KeyMask != approved.KeyMask {
		t.Fatalf("stopped binding was lost: %+v %v", stillThere, err)
	}
	deliveries, err := service.Repo.ListRepoKeyDeliveries(context.Background(), "default", approved.BindingID)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("delivery records after stop = %d %v", len(deliveries), err)
	}

	query, err := service.QueryRepoKey(ctx, RepoKeyQueryInput{Repo: "org/stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if query.State != "stopped" || query.NextKind != "reissue" || query.StopCategory != "rotate" {
		t.Fatalf("query after stop = %+v", query)
	}

	// Re-application is queued regardless of the recipient record, and issues a new key.
	reapplied, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: "org/stopped", Purpose: "build", PublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	if reapplied.Kind != "reissue" || reapplied.Status != "pending" {
		t.Fatalf("re-application = %+v", reapplied)
	}
	newApproval, err := service.ApproveRepoKeyApplication(context.Background(), reapplied.ApplicationID, "operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if newApproval.BindingID == approved.BindingID {
		t.Fatal("a re-issue reused the stopped binding")
	}
	newDelivery, err := service.FetchRepoKeyCiphertext(ctx, reapplied.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	newKey, err := repokey.Decrypt(privateKey.Bytes(), newDelivery.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if newKey == originalKey {
		t.Fatal("the re-issued key is the stopped one")
	}
	if containsString(gateway.clientKeys(), originalKey) {
		t.Fatal("the stopped key came back into the gateway")
	}
	if !containsString(gateway.clientKeys(), newKey) {
		t.Fatal("the new key is not in the gateway")
	}
}

// A rejected application carries the reason back; a leaked key is never re-enabled (AC-7,
// AC-31).
func TestRepoKeyRejectAndReenableRules(t *testing.T) {
	service, gateway := newRepoKeyService(t)
	ctx := callerContext("claude-hl", "laptop-a")
	privateKey, publicKey := newCallerKeyPair(t)
	applied, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: "org/rules", Purpose: "build", PublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RejectRepoKeyApplication(context.Background(), applied.ApplicationID, "operator@example.com", ""); err == nil {
		t.Fatal("a rejection without a reason was accepted")
	}
	rejected, err := service.RejectRepoKeyApplication(context.Background(), applied.ApplicationID, "operator@example.com", "用途写清楚")
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Status != "rejected" || rejected.RejectReason != "用途写清楚" {
		t.Fatalf("rejected application = %+v", rejected)
	}
	status, err := service.RepoKeyStatus(context.Background(), applied.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Status != "rejected" || status.RejectReason != "用途写清楚" || status.CiphertextReady {
		t.Fatalf("status = %+v", status)
	}
	if _, err := service.FetchRepoKeyCiphertext(ctx, applied.ApplicationID, ""); err == nil {
		t.Fatal("a rejected application delivered a ciphertext")
	}

	second, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: "org/rules", Purpose: "build", PublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.ApproveRepoKeyApplication(context.Background(), second.ApplicationID, "operator@example.com")
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := service.FetchRepoKeyCiphertext(ctx, second.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	key, err := repokey.Decrypt(privateKey.Bytes(), delivery.Ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator@example.com", "misuse", "runaway loop"); err != nil {
		t.Fatal(err)
	}

	// A re-enable needs the key's own value: OMC keeps no plaintext, so the operator must
	// paste it, and anything else is refused.
	if _, err := service.ReenableRepoKeyBinding(context.Background(), approved.BindingID, "operator@example.com", "mistake", "sk-cpa-wrong"); err == nil {
		t.Fatal("a mismatched key was accepted for re-enable")
	}
	if _, err := service.ReenableRepoKeyBinding(context.Background(), approved.BindingID, "operator@example.com", "", key); err == nil {
		t.Fatal("a re-enable without a reason was accepted")
	}
	reenabled, err := service.ReenableRepoKeyBinding(context.Background(), approved.BindingID, "operator@example.com", "误停用", key)
	if err != nil {
		t.Fatal(err)
	}
	if reenabled.Status != "active" || !containsString(gateway.clientKeys(), key) {
		t.Fatalf("re-enable did not restore the key: %+v", reenabled)
	}

	// Leaked keys are permanently stopped.
	secondStop, err := service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator@example.com", "leaked", "pasted into a ticket")
	if err != nil {
		t.Fatal(err)
	}
	if secondStop.StopCategory != "leaked" {
		t.Fatalf("stop category = %q", secondStop.StopCategory)
	}
	if _, err := service.ReenableRepoKeyBinding(context.Background(), approved.BindingID, "operator@example.com", "should not work", key); err == nil {
		t.Fatal("a leaked key was re-enabled")
	}
	if containsString(gateway.clientKeys(), key) {
		t.Fatal("the leaked key is still in the gateway")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
