package repository

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repokey"
)

var repoKeyDSNCounter atomic.Uint64

// repoKeyRepo opens a private in-memory database, because the package's shared DSN
// would leak rows between tests.
func repoKeyRepo(t *testing.T) (*Repository, context.Context) {
	t.Helper()
	name := fmt.Sprintf("file:memdb_repokey_%d?mode=memory&cache=shared", repoKeyDSNCounter.Add(1))
	db, err := Open(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := New(db)
	if _, err := db.SQL.Exec(`
		INSERT INTO cpa_instances (id, name, base_url, usage_addr,
			management_key_ciphertext, management_key_nonce, status, created_at, updated_at)
		VALUES ('default', 'Default', 'http://127.0.0.1:8317', '127.0.0.1:8317',
			x'00', x'00', 'unknown', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	return repo, context.Background()
}

func newRepoKeyBinding(repo *Repository, ctx context.Context, t *testing.T, repoID, fingerprint, mask string, nowMS int64) RepoKeyBinding {
	t.Helper()
	binding, err := repo.CreateRepoKeyBinding(ctx, RepoKeyBinding{
		InstanceID: "default", RepoID: repoID, RepoHasRemote: true,
		KeyFingerprint: fingerprint, KeyMask: mask, CreatedBy: "operator@example.com",
		// The deployment-cipher copy a re-enable unseals; the repository only requires its
		// presence (AC-31), so the tests carry a placeholder.
		KeySealed: "sealed:" + fingerprint,
	}, nowMS, testAudit("repo_key.issue"))
	if err != nil {
		t.Fatalf("create binding: %v", err)
	}
	return binding
}

// newRepoKeyReenableRequest files the request that asks to put a stopped key back in
// service. It names the binding instead of carrying a public key (AC-31).
func newRepoKeyReenableRequest(t *testing.T, repo *Repository, ctx context.Context, binding RepoKeyBinding, token string, nowMS int64) RepoKeyApplication {
	t.Helper()
	application, created, err := repo.CreateRepoKeyApplication(ctx, RepoKeyApplication{
		InstanceID: binding.InstanceID, RepoID: binding.RepoID, RepoHasRemote: true,
		AgentName: "console", DeviceName: "console", Purpose: "put the stopped key back in service",
		Kind: "reuse", RequestKind: "reenable", TargetBindingID: binding.ID,
		KeyFingerprint: binding.KeyFingerprint, KeyMask: binding.KeyMask, KeyAlgorithm: "x25519",
		Source: "console", AuthorizationTokenHash: repokey.TokenFingerprint(token), TokenExpiresAtMS: nowMS + 60_000,
	}, nowMS, testAudit("repo_key.apply"))
	if err != nil || !created {
		t.Fatalf("reenable request: %v created=%v", err, created)
	}
	return application
}

func countAudit(t *testing.T, repo *Repository, ctx context.Context, action string) int {
	t.Helper()
	var count int
	if err := repo.SQL().QueryRowContext(ctx,
		`SELECT COUNT(1) FROM audit_events WHERE target_type = 'repo_api_key' AND action = ?`, action).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// A repository holds at most one active key; a second one is refused rather than
// silently coexisting (AC-6).
func TestRepoKeyOneActiveBindingPerRepo(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	first := newRepoKeyBinding(repo, ctx, t, "LambdaTheory/oh-my-cpa", "fp-one", "sk-cpa-****1111", 1000)

	active, err := repo.GetActiveRepoKeyBinding(ctx, "default", "LambdaTheory/oh-my-cpa")
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != first.ID {
		t.Fatalf("active binding = %s, want %s", active.ID, first.ID)
	}

	_, err = repo.CreateRepoKeyBinding(ctx, RepoKeyBinding{
		InstanceID: "default", RepoID: "LambdaTheory/oh-my-cpa", RepoHasRemote: true,
		KeyFingerprint: "fp-two", KeyMask: "sk-cpa-****2222",
	}, 2000, testAudit("repo_key.issue"))
	if !errors.Is(err, ErrRepoKeyConflict) {
		t.Fatalf("second active binding error = %v, want ErrRepoKeyConflict", err)
	}

	// A refused write leaves no audit trail claiming an issue happened.
	if count := countAudit(t, repo, ctx, "repo_key.issue"); count != 1 {
		t.Fatalf("issue audit events = %d, want 1", count)
	}
}

// The three outcomes the agent asks about: active, stopped, none (AC-3).
func TestRepoKeyRepoStatus(t *testing.T) {
	repo, ctx := repoKeyRepo(t)

	status, err := repo.GetRepoKeyRepoStatus(ctx, "default", "org/fresh")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "none" || status.Binding != nil {
		t.Fatalf("fresh repo status = %+v", status)
	}

	binding := newRepoKeyBinding(repo, ctx, t, "org/used", "fp-status", "sk-cpa-****aaaa", 1000)
	status, err = repo.GetRepoKeyRepoStatus(ctx, "default", "org/used")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "active" || status.Binding == nil || status.Binding.ID != binding.ID {
		t.Fatalf("active repo status = %+v", status)
	}

	if _, err := repo.StopRepoKeyBinding(ctx, binding.ID, "operator@example.com", "rotate", "quarterly rotation", 2000,
		testAudit("repo_key.stop")); err != nil {
		t.Fatal(err)
	}
	status, err = repo.GetRepoKeyRepoStatus(ctx, "default", "org/used")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "stopped" || status.Binding == nil || status.Binding.StopCategory != "rotate" {
		t.Fatalf("stopped repo status = %+v", status)
	}
}

// Stopping retains the binding, its history and the operator's reason; re-enabling
// clears the current state but keeps the stop row (AC-10, AC-31).
func TestRepoKeyStopAndReenableRetainsHistory(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	binding := newRepoKeyBinding(repo, ctx, t, "org/app", "fp-stop", "sk-cpa-****bbbb", 1000)

	stopped, err := repo.StopRepoKeyBinding(ctx, binding.ID, "operator@example.com", "misuse", "runaway loop", 2000,
		testAudit("repo_key.stop"))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != "stopped" || stopped.StoppedAtMS == nil || *stopped.StoppedAtMS != 2000 {
		t.Fatalf("stopped binding = %+v", stopped)
	}
	if _, err := repo.GetActiveRepoKeyBinding(ctx, "default", "org/app"); !errors.Is(err, ErrRepoKeyNotFound) {
		t.Fatalf("stopped key still answers as active: %v", err)
	}
	if _, err := repo.StopRepoKeyBinding(ctx, binding.ID, "operator@example.com", "other", "again", 2500,
		testAudit("repo_key.stop")); !errors.Is(err, ErrRepoKeyConflict) {
		t.Fatalf("stopping twice error = %v, want ErrRepoKeyConflict", err)
	}

	// Re-enabling is a request a person approves, not a switch the console throws (AC-31).
	request := newRepoKeyReenableRequest(t, repo, ctx, binding, "token-reenable", 2500)
	reenabled, err := repo.ApproveRepoKeyReenable(ctx, request.ID, "operator@example.com", "cause fixed", 3000,
		testAudit("repo_key.reenable"))
	if err != nil {
		t.Fatal(err)
	}
	if reenabled.Status != "approved" || reenabled.DecidedAtMS == nil || reenabled.DecisionReason != "cause fixed" {
		t.Fatalf("approved reenable request = %+v", reenabled)
	}
	// The key came back without waiting in the delivery queue: a re-enable hands nothing
	// to a caller (AC-33).
	if reenabled.FirstDeliveredAtMS == nil || *reenabled.FirstDeliveredAtMS != 3000 {
		t.Fatalf("reenable request still waits for delivery: %+v", reenabled)
	}
	restored, err := repo.GetActiveRepoKeyBinding(ctx, "default", "org/app")
	if err != nil {
		t.Fatal(err)
	}
	if restored.ID != binding.ID || restored.StoppedAtMS != nil {
		t.Fatalf("reenabled binding = %+v", restored)
	}
	if restored.KeyFingerprint != binding.KeyFingerprint {
		t.Fatalf("reenabled binding = %q, want the same key %q", restored.KeyFingerprint, binding.KeyFingerprint)
	}

	stops, err := repo.ListRepoKeyStops(ctx, "default", binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stops) != 1 {
		t.Fatalf("stop rows = %d, want 1", len(stops))
	}
	if stops[0].Category != "misuse" || stops[0].Reason != "runaway loop" ||
		stops[0].ReenabledBy != "operator@example.com" || stops[0].ReenabledAtMS == nil ||
		stops[0].ReenableReason != "cause fixed" {
		t.Fatalf("stop history = %+v", stops[0])
	}
}

// A leaked key is never put back into service (AC-31). The stop drops the sealed copy on
// purpose, so there is nothing left that could restore it.
func TestRepoKeyLeakedKeyCannotBeReenabled(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	binding := newRepoKeyBinding(repo, ctx, t, "org/leaky", "fp-leak", "sk-cpa-****cccc", 1000)
	stopped, err := repo.StopRepoKeyBinding(ctx, binding.ID, "operator@example.com", "leaked", "pasted into a ticket", 2000,
		testAudit("repo_key.stop"))
	if err != nil {
		t.Fatal(err)
	}
	if stopped.KeySealed != "" {
		t.Fatalf("a leaked key kept its sealed copy: %+v", stopped)
	}
	request := newRepoKeyReenableRequest(t, repo, ctx, binding, "token-leak", 2500)
	_, err = repo.ApproveRepoKeyReenable(ctx, request.ID, "operator@example.com", "we think it is fine", 3000,
		testAudit("repo_key.reenable"))
	if !errors.Is(err, ErrRepoKeyBindingNotReenable) {
		t.Fatalf("reenable leaked error = %v, want ErrRepoKeyBindingNotReenable", err)
	}
	if _, err := repo.GetActiveRepoKeyBinding(ctx, "default", "org/leaky"); !errors.Is(err, ErrRepoKeyNotFound) {
		t.Fatalf("a leaked key answered as active: %v", err)
	}
	if count := countAudit(t, repo, ctx, "repo_key.reenable"); count != 0 {
		t.Fatalf("a refused re-enable still wrote %d audit events", count)
	}
}

// The same caller asking twice waits once; a changed public key refreshes the
// snapshot instead of forking a second application (AC-28, AC-34).
func TestRepoKeyApplicationDedupeAndPublicKeyRefresh(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	base := RepoKeyApplication{
		InstanceID: "default", RepoID: "org/dedupe", RepoHasRemote: true,
		AgentName: "claude", DeviceName: "laptop", Purpose: "run evaluations",
		Kind: "issue", PublicKey: "x25519:first", PublicKeyFingerprint: "x25519:sha256:first",
		KeyAlgorithm: "x25519", Source: "agent", AuthorizationTokenHash: repokey.TokenFingerprint("token-1"), TokenExpiresAtMS: 9000,
	}
	first, created, err := repo.CreateRepoKeyApplication(ctx, base, 1000, testAudit("repo_key.apply"))
	if err != nil || !created {
		t.Fatalf("first application: %v created=%v", err, created)
	}

	second, created, err := repo.CreateRepoKeyApplication(ctx, base, 1100, testAudit("repo_key.apply"))
	if err != nil {
		t.Fatal(err)
	}
	if created || second.ID != first.ID {
		t.Fatalf("duplicate applied as %s created=%v, want reuse of %s", second.ID, created, first.ID)
	}
	if count := countAudit(t, repo, ctx, "repo_key.apply"); count != 1 {
		t.Fatalf("apply audit events = %d, want 1 (an unchanged re-ask is not a new request)", count)
	}

	changed := base
	changed.PublicKey = "x25519:second"
	changed.PublicKeyFingerprint = "x25519:sha256:second"
	third, created, err := repo.CreateRepoKeyApplication(ctx, changed, 1200, testAudit("repo_key.apply"))
	if err != nil {
		t.Fatal(err)
	}
	if created || third.ID != first.ID {
		t.Fatalf("changed key forked a new application: %s created=%v", third.ID, created)
	}
	if third.PublicKeyFingerprint != "x25519:sha256:second" || third.UpdatedAtMS != 1200 {
		t.Fatalf("pending application did not take the new public key: %+v", third)
	}
	if count := countAudit(t, repo, ctx, "repo_key.apply"); count != 2 {
		t.Fatalf("apply audit events = %d, want 2 after the key changed", count)
	}

	pending, err := repo.ListRepoKeyApplications(ctx, "default", "pending", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending applications = %d, want 1", len(pending))
	}
}

// A caller asking again is handed a fresh link. The returned token must be the one the
// waiting row accepts, so the row rotates with it and the earlier link stops working
// (AC-8, review F1).
func TestRepoKeyApplicationDedupeRotatesApprovalToken(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	base := RepoKeyApplication{
		InstanceID: "default", RepoID: "org/rotate", RepoHasRemote: true,
		AgentName: "claude", DeviceName: "laptop", Purpose: "run evaluations",
		Kind: "issue", PublicKey: "x25519:first", PublicKeyFingerprint: "x25519:sha256:first",
		KeyAlgorithm: "x25519", Source: "agent", AuthorizationTokenHash: repokey.TokenFingerprint("token-1"), TokenExpiresAtMS: 9000,
	}
	first, created, err := repo.CreateRepoKeyApplication(ctx, base, 1000, testAudit("repo_key.apply"))
	if err != nil || !created {
		t.Fatalf("first application: %v created=%v", err, created)
	}

	again := base
	again.AuthorizationTokenHash = repokey.TokenFingerprint("token-2")
	again.TokenExpiresAtMS = 12000
	reused, created, err := repo.CreateRepoKeyApplication(ctx, again, 1100, testAudit("repo_key.apply"))
	if err != nil {
		t.Fatal(err)
	}
	if created || reused.ID != first.ID {
		t.Fatalf("duplicate applied as %s created=%v, want reuse of %s", reused.ID, created, first.ID)
	}
	// The link the caller was just given opens the waiting application...
	if found, err := repo.GetRepoKeyApplicationByToken(ctx, "token-2"); err != nil || found.ID != first.ID {
		t.Fatalf("the returned token does not open the application: %v", err)
	}
	// ...and the one handed out before it no longer does.
	if _, err := repo.GetRepoKeyApplicationByToken(ctx, "token-1"); !errors.Is(err, ErrRepoKeyApplicationNotFound) {
		t.Fatalf("the superseded token still opens the application: %v", err)
	}
}
// A decision is applied once. The second caller (the one-time link, or the console
// queue) reads the stored outcome instead of re-deciding (AC-8, AC-25).
func TestRepoKeyDecisionIsAppliedOnce(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	application, _, err := repo.CreateRepoKeyApplication(ctx, RepoKeyApplication{
		InstanceID: "default", RepoID: "org/decide", RepoHasRemote: true,
		AgentName: "codex", DeviceName: "desktop", Purpose: "index the repo",
		Kind: "issue", PublicKey: "x25519:pk", PublicKeyFingerprint: "x25519:sha256:pk",
		KeyAlgorithm: "x25519", Source: "agent", AuthorizationTokenHash: repokey.TokenFingerprint("token-decide"), TokenExpiresAtMS: 9000,
	}, 1000, testAudit("repo_key.apply"))
	if err != nil {
		t.Fatal(err)
	}
	binding := newRepoKeyBinding(repo, ctx, t, "org/decide", "fp-decide", "sk-cpa-****dddd", 2000)

	approved, err := repo.DecideRepoKeyApplication(ctx, ApplicationDecision{
		ApplicationID: application.ID, Approve: true, DecidedBy: "operator@example.com",
		Kind: "issue", BindingID: binding.ID, KeyFingerprint: binding.KeyFingerprint, KeyMask: binding.KeyMask,
	}, 2100, testAudit("repo_key.approve"))
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != "approved" || approved.BindingID != binding.ID || approved.DecidedAtMS == nil {
		t.Fatalf("approved application = %+v", approved)
	}

	// A rejected decision arriving later must not overwrite the approval.
	again, err := repo.DecideRepoKeyApplication(ctx, ApplicationDecision{
		ApplicationID: application.ID, Approve: false, DecidedBy: "someone@example.com", RejectReason: "too late",
	}, 2200, testAudit("repo_key.reject"))
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != "approved" || again.RejectReason != "" {
		t.Fatalf("second decision changed the outcome: %+v", again)
	}
	if count := countAudit(t, repo, ctx, "repo_key.reject"); count != 0 {
		t.Fatalf("a no-op decision still wrote %d audit events", count)
	}

	// Rejection needs a reason(TM AC-7) and is the only way an application leaves the
	// queue undelivered.
	rejected, _, err := repo.CreateRepoKeyApplication(ctx, RepoKeyApplication{
		InstanceID: "default", RepoID: "org/decide", RepoHasRemote: true,
		AgentName: "opencode", DeviceName: "desktop", Purpose: "try things",
		Kind: "reuse", PublicKey: "x25519:pk2", PublicKeyFingerprint: "x25519:sha256:pk2",
		KeyAlgorithm: "x25519", Source: "agent", AuthorizationTokenHash: repokey.TokenFingerprint("token-reject"), TokenExpiresAtMS: 9000,
	}, 3000, testAudit("repo_key.apply"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DecideRepoKeyApplication(ctx, ApplicationDecision{
		ApplicationID: rejected.ID, Approve: false, DecidedBy: "operator@example.com",
	}, 3100, testAudit("repo_key.reject")); !errors.Is(err, ErrRepoKeyInvalid) {
		t.Fatalf("reasonless rejection error = %v, want ErrRepoKeyInvalid", err)
	}
}

// Each hand-out is recorded, and the first one leaves the delivery queue (AC-20, AC-33).
func TestRepoKeyDeliveryRecordsAndLeavesQueue(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	application, _, err := repo.CreateRepoKeyApplication(ctx, RepoKeyApplication{
		InstanceID: "default", RepoID: "org/deliver", RepoHasRemote: true,
		AgentName: "claude", DeviceName: "laptop", Purpose: "ship it",
		Kind: "issue", PublicKey: "x25519:pk", PublicKeyFingerprint: "x25519:sha256:pk",
		KeyAlgorithm: "x25519", Source: "agent", AuthorizationTokenHash: repokey.TokenFingerprint("token-deliver"), TokenExpiresAtMS: 9000,
	}, 1000, testAudit("repo_key.apply"))
	if err != nil {
		t.Fatal(err)
	}
	binding := newRepoKeyBinding(repo, ctx, t, "org/deliver", "fp-deliver", "sk-cpa-****eeee", 2000)
	if _, err := repo.DecideRepoKeyApplication(ctx, ApplicationDecision{
		ApplicationID: application.ID, Approve: true, DecidedBy: "operator@example.com",
		Kind: "issue", BindingID: binding.ID, KeyFingerprint: binding.KeyFingerprint, KeyMask: binding.KeyMask,
	}, 2100, testAudit("repo_key.approve")); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.RecordRepoKeyDelivery(ctx, RepoKeyDelivery{
		InstanceID: "default", ApplicationID: application.ID, BindingID: binding.ID,
		KeyFingerprint: binding.KeyFingerprint, KeyMask: binding.KeyMask,
		AgentName: "claude", DeviceName: "laptop", PublicKeyFingerprint: "x25519:sha256:pk",
		Kind: "issue", Operator: "approval", DeliveredAtMS: 2200,
	}, testAudit("repo_key.deliver")); err != nil {
		t.Fatal(err)
	}

	stored, err := repo.GetRepoKeyApplication(ctx, application.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.FirstDeliveredAtMS == nil || *stored.FirstDeliveredAtMS != 2200 {
		t.Fatalf("application was not stamped as delivered: %+v", stored)
	}

	deliveries, err := repo.ListRepoKeyDeliveries(ctx, "default", binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(deliveries) != 1 || deliveries[0].Operator != "approval" || deliveries[0].AgentName != "claude" {
		t.Fatalf("delivery records = %+v", deliveries)
	}
}

// A repository may be stopped and then issued a fresh key; the old stopped row stays
// attributed to the repository (AC-12).
func TestRepoKeyReissueAfterStopLeavesOldBinding(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	old := newRepoKeyBinding(repo, ctx, t, "org/reissue", "fp-old", "sk-cpa-****0000", 1000)
	if _, err := repo.StopRepoKeyBinding(ctx, old.ID, "operator@example.com", "rotate", "rotating", 2000,
		testAudit("repo_key.stop")); err != nil {
		t.Fatal(err)
	}
	fresh := newRepoKeyBinding(repo, ctx, t, "org/reissue", "fp-new", "sk-cpa-****ffff", 3000)

	active, err := repo.GetActiveRepoKeyBinding(ctx, "default", "org/reissue")
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != fresh.ID {
		t.Fatalf("active binding = %s, want the reissued one", active.ID)
	}
	status, err := repo.GetRepoKeyRepoStatus(ctx, "default", "org/reissue")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != "active" || len(status.Stops) != 1 || status.Stops[0].ID != old.ID {
		t.Fatalf("status after reissue = %+v", status)
	}
	bindings, err := repo.ListRepoKeyBindings(ctx, "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 2 {
		t.Fatalf("bindings = %d, want both the stopped and the active key", len(bindings))
	}
}

// The audit write is the gate: if it cannot be written, the change does not happen
// (AGENTS Tier 1 fail-closed audit).
func TestRepoKeyWriteFailsWhenAuditCannotBeWritten(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	_, err := repo.CreateRepoKeyBinding(ctx, RepoKeyBinding{
		InstanceID: "default", RepoID: "org/no-audit", RepoHasRemote: true,
		KeyFingerprint: "fp-no-audit", KeyMask: "sk-cpa-****9999",
	}, 1000, AuditEvent{}) // no action: the audit row is not admissible
	if !errors.Is(err, ErrRepoKeyAuditFailed) {
		t.Fatalf("error = %v, want ErrRepoKeyAuditFailed", err)
	}
	if _, err := repo.GetActiveRepoKeyBinding(ctx, "default", "org/no-audit"); !errors.Is(err, ErrRepoKeyNotFound) {
		t.Fatalf("binding survived a failed audit write: %v", err)
	}
}

// A caller's public key is registered per agent+device and can be replaced there;
// replacing it is not a new caller (AC-34).
func TestRepoKeyCallerUpsert(t *testing.T) {
	repo, ctx := repoKeyRepo(t)
	caller, err := repo.UpsertRepoKeyCaller(ctx, RepoKeyCaller{
		InstanceID: "default", AgentName: "claude", DeviceName: "laptop",
		PublicKey: "x25519:one", PublicKeyFingerprint: "x25519:sha256:one", KeyAlgorithm: "x25519",
	}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	replaced, err := repo.UpsertRepoKeyCaller(ctx, RepoKeyCaller{
		InstanceID: "default", AgentName: "claude", DeviceName: "laptop",
		PublicKey: "x25519:two", PublicKeyFingerprint: "x25519:sha256:two", KeyAlgorithm: "x25519",
	}, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if replaced.ID != caller.ID || replaced.CreatedAtMS != 1000 || replaced.UpdatedAtMS != 2000 {
		t.Fatalf("replacement = %+v, want same row %s updated", replaced, caller.ID)
	}
	found, err := repo.FindRepoKeyCallerByFingerprint(ctx, "default", "x25519:sha256:two", "")
	if err != nil {
		t.Fatal(err)
	}
	if found.DeviceName != "laptop" {
		t.Fatalf("fingerprint lookup = %+v", found)
	}
	if _, err := repo.FindRepoKeyCallerByFingerprint(ctx, "default", "x25519:sha256:one", ""); !errors.Is(err, ErrRepoKeyNotFound) {
		t.Fatalf("the replaced key still resolves: %v", err)
	}
}
