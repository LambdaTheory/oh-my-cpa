package operations

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/cpa/management"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func approveRegressionRepoKey(t *testing.T, service *Service, repo, agent, device string) repository.RepoKeyApplication {
	t.Helper()
	_, publicKey := newCallerKeyPair(t)
	applied, err := service.ApplyRepoKey(callerContext(agent, device), RepoKeyApplyInput{Repo: repo, Purpose: "synthetic regression", PublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	approved, err := service.ApproveRepoKeyApplication(context.Background(), applied.ApplicationID, "operator", "reviewed")
	if err != nil {
		t.Fatal(err)
	}
	return approved
}

func interceptRegressionGateway(t *testing.T, service *Service, gateway *fakeGateway, before func(*http.Request)) {
	t.Helper()
	handler := gateway.handler()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		before(request)
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	client, err := management.NewClient(server.URL, "synthetic-management-key", 5*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	service.Client = func(context.Context) (*management.Client, error) { return client, nil }
}

func TestRepoKeyRegressionUnapprovedPublicKeyCannotAutoApprove(t *testing.T) {
	service, _ := newRepoKeyService(t)
	approved := approveRegressionRepoKey(t, service, "org/public-key-change", "agent", "device")
	ctx := callerContext("agent", "device")
	_, replacement := newCallerKeyPair(t)
	changed, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: approved.RepoID, Purpose: "replace public key", PublicKey: replacement})
	if err != nil || changed.Status != "pending" {
		t.Fatalf("changed application status=%s err=%v", changed.Status, err)
	}
	registered, err := service.Repo.GetRepoKeyCaller(ctx, "default", "agent", "device")
	if err != nil {
		t.Fatal(err)
	}
	if registered.PublicKeyFingerprint != approved.PublicKeyFingerprint {
		t.Error("an unapproved application replaced the approved caller key")
	}
	repeated, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: approved.RepoID, Purpose: "replace public key"})
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Status != "pending" || repeated.ApplicationID != changed.ApplicationID {
		t.Fatalf("unapproved replacement escaped the queue: status=%s same_id=%t", repeated.Status, repeated.ApplicationID == changed.ApplicationID)
	}
	stored, err := service.Repo.GetRepoKeyApplication(ctx, repeated.ApplicationID)
	if err != nil || stored.PublicKey != replacement {
		t.Fatal("a repeated application did not preserve its pending public key")
	}
	if _, err := service.FetchRepoKeyCiphertext(ctx, repeated.ApplicationID, ""); err == nil {
		t.Fatal("an unapproved replacement received ciphertext")
	}
	if _, err := service.ApproveRepoKeyApplication(ctx, repeated.ApplicationID, "operator", "changed key reviewed"); err != nil {
		t.Fatal(err)
	}
	registered, err = service.Repo.GetRepoKeyCaller(ctx, "default", "agent", "device")
	if err != nil || registered.PublicKey != replacement {
		t.Fatal("successful approval did not promote the replacement")
	}
}

func TestRepoKeyRegressionDedupeKeepsDeviceAndKeyTogether(t *testing.T) {
	service, _ := newRepoKeyService(t)
	_, firstKey := newCallerKeyPair(t)
	secondPrivate, secondKey := newCallerKeyPair(t)
	first, err := service.ApplyRepoKey(callerContext("agent", "device-one"), RepoKeyApplyInput{Repo: "org/device-pair", Purpose: "first device", PublicKey: firstKey})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ApplyRepoKey(callerContext("agent", "device-two"), RepoKeyApplyInput{Repo: "org/device-pair", Purpose: "second device", PublicKey: secondKey})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := service.Repo.GetRepoKeyApplication(context.Background(), second.ApplicationID)
	if err != nil {
		t.Fatal(err)
	}
	if second.ApplicationID != first.ApplicationID || stored.DeviceName != "device-two" || stored.PublicKey != secondKey {
		t.Fatalf("deduplication mixed caller snapshots: same_id=%t device=%q second_key=%t", second.ApplicationID == first.ApplicationID, stored.DeviceName, stored.PublicKey == secondKey)
	}
	if _, err := service.ApproveRepoKeyApplication(context.Background(), second.ApplicationID, "operator", ""); err != nil {
		t.Fatal(err)
	}
	delivery, err := service.FetchRepoKeyCiphertext(callerContext("agent", "device-two"), second.ApplicationID, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repokey.Decrypt(secondPrivate.Bytes(), delivery.Ciphertext); err != nil {
		t.Fatal("the approved device cannot decrypt its delivery")
	}
	acceptedFirst, _ := service.Repo.IsRepoKeyRecipient(context.Background(), "default", "org/device-pair", "agent", "device-one")
	acceptedSecond, _ := service.Repo.IsRepoKeyRecipient(context.Background(), "default", "org/device-pair", "agent", "device-two")
	if acceptedFirst || !acceptedSecond {
		t.Fatal("approval registered the wrong device")
	}
}

func TestRepoKeyRegressionRejectDuringGatewayWrite(t *testing.T) {
	service, gateway := newRepoKeyService(t)
	ctx := callerContext("agent", "device")
	_, publicKey := newCallerKeyPair(t)
	application, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: "org/decision-race", Purpose: "synthetic race", PublicKey: publicKey})
	if err != nil {
		t.Fatal(err)
	}
	originalKeys := gateway.clientKeys()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	interceptRegressionGateway(t, service, gateway, func(request *http.Request) {
		if request.Method == http.MethodPatch && strings.HasSuffix(request.URL.Path, "/config") {
			once.Do(func() { close(entered); <-release })
		}
	})
	type outcome struct {
		application repository.RepoKeyApplication
		err         error
	}
	result := make(chan outcome, 1)
	go func() {
		decided, err := service.ApproveRepoKeyApplication(ctx, application.ApplicationID, "approver", "")
		result <- outcome{decided, err}
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("approval never reached the controlled gateway write")
	}
	rejected, rejectErr := service.RejectRepoKeyApplication(ctx, application.ApplicationID, "rejector", "not authorized")
	close(release)
	approved := <-result
	if rejectErr != nil || rejected.Status != "rejected" || approved.err != nil || approved.application.Status != "rejected" {
		t.Fatalf("the winning rejection was not retained: reject=%s/%v approval=%s/%v", rejected.Status, rejectErr, approved.application.Status, approved.err)
	}
	if !reflect.DeepEqual(gateway.clientKeys(), originalKeys) {
		t.Error("a rejected approval left a usable key in the gateway")
	}
	recipient, err := service.Repo.IsRepoKeyRecipient(ctx, "default", "org/decision-race", "agent", "device")
	if err != nil || recipient {
		t.Fatal("a rejected caller was registered for automatic access")
	}
	repeated, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: "org/decision-race", Purpose: "retry after rejection"})
	if err != nil || repeated.Status != "pending" {
		t.Fatalf("rejected caller bypassed review: status=%s err=%v", repeated.Status, err)
	}
}

func TestRepoKeyRegressionInvalidStopHasNoSideEffects(t *testing.T) {
	for _, input := range []struct{ category, reason string }{{"invalid", "reason"}, {"rotate", ""}, {"leaked", "   "}} {
		t.Run(input.category+"/"+input.reason, func(t *testing.T) {
			service, gateway := newRepoKeyService(t)
			approved := approveRegressionRepoKey(t, service, "org/invalid-stop", "agent", "device")
			originalKeys := gateway.clientKeys()
			_, err := service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator", input.category, input.reason)
			if err == nil {
				t.Fatal("invalid stop was accepted")
			}
			if !reflect.DeepEqual(gateway.clientKeys(), originalKeys) {
				t.Fatal("an invalid stop changed the gateway's usable keys")
			}
			binding, err := service.Repo.GetRepoKeyBindingByID(context.Background(), approved.BindingID)
			if err != nil || binding.Status != "active" {
				t.Fatal("an invalid stop changed the binding")
			}
		})
	}
}

func TestRepoKeyRegressionStopAuditFailureRestoresGateway(t *testing.T) {
	service, gateway := newRepoKeyService(t)
	approved := approveRegressionRepoKey(t, service, "org/stop-audit", "agent", "device")
	originalKeys := gateway.clientKeys()
	if _, err := service.Repo.SQL().Exec(`CREATE TRIGGER fail_repo_stop_audit BEFORE INSERT ON audit_events WHEN NEW.action = 'repo_key.stop' BEGIN SELECT RAISE(ABORT, 'synthetic audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator", "rotate", "synthetic stop")
	if !errors.Is(err, repository.ErrRepoKeyAuditFailed) {
		t.Fatalf("stop did not surface audit failure: %v", err)
	}
	if !reflect.DeepEqual(gateway.clientKeys(), originalKeys) {
		t.Fatal("a failed stop left the active binding unusable")
	}
}

func TestRepoKeyRegressionLegacyCreateCannotRestoreOwnedKey(t *testing.T) {
	for _, category := range []string{"leaked", "rotate"} {
		t.Run(category, func(t *testing.T) {
			service, gateway := newRepoKeyService(t)
			approved := approveRegressionRepoKey(t, service, "org/legacy-bypass", "agent", "device")
			secret, err := service.repoKeySecret(context.Background(), approved.KeyFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator", category, "synthetic stop"); err != nil {
				t.Fatal(err)
			}
			if _, err := service.CreateKey(context.Background(), secret, ""); err == nil {
				t.Error("the legacy entry point restored a repository-owned key")
			}
			if containsString(gateway.clientKeys(), secret) {
				t.Fatal("a stopped key became usable without approval")
			}
			if _, err := service.CreateKey(context.Background(), "sk-synthetic-unbound-legacy", ""); err != nil {
				t.Fatalf("unbound legacy creation regressed: %v", err)
			}
		})
	}
}

func TestRepoKeyRegressionApprovalRetainsReissueKind(t *testing.T) {
	service, _ := newRepoKeyService(t)
	approved := approveRegressionRepoKey(t, service, "org/reissue-history", "agent", "device")
	if _, err := service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator", "leaked", "synthetic stop"); err != nil {
		t.Fatal(err)
	}
	applied, err := service.ApplyRepoKey(callerContext("agent", "device"), RepoKeyApplyInput{Repo: approved.RepoID, Purpose: "replace leaked key"})
	if err != nil || applied.Kind != "reissue" {
		t.Fatalf("replacement application kind=%s err=%v", applied.Kind, err)
	}
	decided, err := service.ApproveRepoKeyApplication(context.Background(), applied.ApplicationID, "operator", "")
	if err != nil || decided.Kind != "reissue" {
		t.Fatalf("replacement approval lost its type: kind=%s err=%v", decided.Kind, err)
	}
	if decided.KeyFingerprint == approved.KeyFingerprint {
		t.Fatal("reissue reused the stopped key")
	}
}

func TestRepoKeyRegressionReenableNeverWritesTwoActiveKeys(t *testing.T) {
	service, gateway := newRepoKeyService(t)
	first := approveRegressionRepoKey(t, service, "org/reenable-conflict", "agent", "device")
	if _, err := service.StopRepoKeyBinding(context.Background(), first.BindingID, "operator", "rotate", "synthetic stop"); err != nil {
		t.Fatal(err)
	}
	reenable, err := service.RequestRepoKeyReenable(context.Background(), first.BindingID, "operator", "restore after pause")
	if err != nil {
		t.Fatal(err)
	}
	approveRegressionRepoKey(t, service, first.RepoID, "replacement-agent", "device")
	originalKeys := gateway.clientKeys()
	var mu sync.Mutex
	var writes []int
	interceptRegressionGateway(t, service, gateway, func(request *http.Request) {
		if request.Method != http.MethodPatch || !strings.HasSuffix(request.URL.Path, "/config") {
			return
		}
		var patch map[string]json.RawMessage
		// Observe the real outgoing patch without changing what the gateway receives.
		encoded, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			return
		}
		request.Body = io.NopCloser(strings.NewReader(string(encoded)))
		if err := json.Unmarshal(encoded, &patch); err != nil {
			t.Error(err)
			return
		}
		var access struct {
			Keys []string `json:"api-keys"`
		}
		if err := json.Unmarshal(patch["access"], &access); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		writes = append(writes, len(access.Keys))
		mu.Unlock()
	})
	if _, err := service.ApproveRepoKeyApplication(context.Background(), reenable.ID, "operator", ""); err == nil {
		t.Fatal("re-enable succeeded while another key was active")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 0 {
		t.Fatalf("conflicting re-enable wrote CPA before refusing: key counts=%v (baseline=%d)", writes, len(originalKeys))
	}
	if !reflect.DeepEqual(gateway.clientKeys(), originalKeys) {
		t.Fatal("conflicting re-enable changed the usable key set")
	}
}

func TestRepoKeyRegressionReenableRequestUsesWriteGate(t *testing.T) {
	service, _ := newRepoKeyService(t)
	approved := approveRegressionRepoKey(t, service, "org/reenable-snapshot", "agent", "device")
	if _, err := service.StopRepoKeyBinding(context.Background(), approved.BindingID, "operator", "rotate", "synthetic pause"); err != nil {
		t.Fatal(err)
	}
	service.Acquire = func(context.Context) error { return errors.New("synthetic write in progress") }
	service.Release = func() { t.Error("a refused write released an unowned permit") }
	if _, err := service.RequestRepoKeyReenable(context.Background(), approved.BindingID, "operator", "synthetic resume"); err == nil || err.Error() != "write_busy" {
		t.Fatalf("re-enable request mutated a decision snapshot outside the write gate: %v", err)
	}
	count, err := service.Repo.CountRepoKeyApplications(context.Background(), "default", "pending")
	if err != nil || count != 0 {
		t.Fatal("refused re-enable admission still changed the approval queue")
	}
}

func TestRepoKeyRegressionApprovalFailureIsAtomic(t *testing.T) {
	for _, failure := range []string{"audit", "recipient"} {
		t.Run(failure, func(t *testing.T) {
			service, gateway := newRepoKeyService(t)
			ctx := callerContext("agent", "device")
			_, publicKey := newCallerKeyPair(t)
			applied, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: "org/atomic-approval", Purpose: "synthetic use", PublicKey: publicKey})
			if err != nil {
				t.Fatal(err)
			}
			originalKeys := gateway.clientKeys()
			trigger := `CREATE TRIGGER fail_approval BEFORE INSERT ON audit_events WHEN NEW.action = 'repo_key.approve' BEGIN SELECT RAISE(ABORT, 'synthetic audit failure'); END`
			if failure == "recipient" {
				trigger = `CREATE TRIGGER fail_recipient BEFORE INSERT ON repo_key_recipients BEGIN SELECT RAISE(ABORT, 'synthetic recipient failure'); END`
			}
			if _, err := service.Repo.SQL().Exec(trigger); err != nil {
				t.Fatal(err)
			}
			if _, err := service.ApproveRepoKeyApplication(ctx, applied.ApplicationID, "operator", ""); err == nil {
				t.Fatal("approval ignored a failed durable write")
			}
			stored, err := service.Repo.GetRepoKeyApplication(ctx, applied.ApplicationID)
			if err != nil || stored.Status != "pending" || stored.Ciphertext != "" {
				t.Fatal("a failed approval committed its decision or delivery")
			}
			bindings, err := service.Repo.ListRepoKeyBindings(ctx, "default")
			if err != nil || len(bindings) != 0 || !reflect.DeepEqual(gateway.clientKeys(), originalKeys) {
				t.Fatal("a failed approval left a new binding or usable gateway key")
			}
			if _, err := service.Repo.GetRepoKeyCaller(ctx, "default", "agent", "device"); !errors.Is(err, repository.ErrRepoKeyNotFound) {
				t.Fatal("a failed approval registered its caller key")
			}
		})
	}
}

func TestRepoKeyRegressionOldPendingRegistrationIsNotTrusted(t *testing.T) {
	service, _ := newRepoKeyService(t)
	approved := approveRegressionRepoKey(t, service, "org/old-registration", "agent", "device")
	ctx := callerContext("agent", "device")
	_, replacement := newCallerKeyPair(t)
	raw, err := repokey.ParsePublicKey(replacement)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Repo.UpsertRepoKeyCaller(ctx, repository.RepoKeyCaller{
		InstanceID: "default", AgentName: "agent", DeviceName: "device", PublicKey: replacement,
		PublicKeyFingerprint: repokey.PublicKeyFingerprint(raw), KeyAlgorithm: "x25519",
	}, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	query, err := service.QueryRepoKey(ctx, RepoKeyQueryInput{Repo: approved.RepoID})
	if err != nil || query.AutoApprove {
		t.Fatal("a registration written by the old pending path was trusted")
	}
	applied, err := service.ApplyRepoKey(ctx, RepoKeyApplyInput{Repo: approved.RepoID, Purpose: "repeat after upgrade"})
	if err != nil || applied.Status != "pending" {
		t.Fatalf("unapproved stored fingerprint skipped review: status=%s err=%v", applied.Status, err)
	}
}
