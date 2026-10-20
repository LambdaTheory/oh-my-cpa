package automation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Set(value time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = value
}

type writerCall struct {
	name      string
	authIndex string
	disabled  bool
}

type fakeWriter struct {
	mu    sync.Mutex
	calls []writerCall
	fail  error
}

func (w *fakeWriter) write(ctx context.Context, name, authIndex string, disabled bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, writerCall{name: name, authIndex: authIndex, disabled: disabled})
	return w.fail
}

func (w *fakeWriter) snapshot() []writerCall {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]writerCall{}, w.calls...)
}

func (w *fakeWriter) setFailure(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.fail = err
}

func automationRepo(t *testing.T) *repository.Repository {
	t.Helper()
	db, err := repository.Open(context.Background(),
		fmt.Sprintf("file:automation_%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return repository.New(db)
}

func insertWeekly(t *testing.T, repo *repository.Repository, authIndex, provider string, used float64, observedAtMS, resetAtMS int64) {
	t.Helper()
	var reset any
	if resetAtMS > 0 {
		reset = resetAtMS
	}
	if _, err := repo.SQL().ExecContext(context.Background(), `
		INSERT INTO quota_observations (
			auth_index, provider, window_kind, scope, model, window_minutes, used_percent,
			reset_at_ms, status, observed_at_ms, event_key, created_at_ms, source
		) VALUES (?, ?, 'weekly', 'standard', '', 10080, ?, ?, '', ?, ?, ?, 'active')`,
		authIndex, provider, used, reset, observedAtMS, fmt.Sprintf("evt-%d-%d", observedAtMS, resetAtMS), observedAtMS); err != nil {
		t.Fatal(err)
	}
}

func newTestService(repo *repository.Repository, clock *testClock, writer *fakeWriter, credentials *[]Credential) *Service {
	return &Service{
		Repo:          repo,
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Policy:        DefaultPolicyConfig(),
		MasterEnabled: func(bool) bool { return true },
		Source:        func(bool) string { return "web" },
		Credentials: func(context.Context) ([]Credential, error) {
			return *credentials, nil
		},
		SetDisabled: writer.write,
		Now:         clock.Now,
	}
}

// TestServiceAdoptsUnverifiedCredentialsAsMonitorOnly proves roster admission and
// automatic action admission are separate. A controllable provider with no verified
// weekly capability joins the roster in monitoring, while an uncontrollable credential
// is omitted even when a caller submits its auth index directly.
func TestServiceAdoptsUnverifiedCredentialsAsMonitorOnly(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{
		{AuthIndex: "idx-unverified", Name: "openrouter-1.json", Provider: "openrouter", Controllable: true},
		{AuthIndex: "idx-uncontrollable", Provider: "openrouter", Controllable: false},
	}
	service := newTestService(repo, clock, writer, &credentials)

	adopted, err := service.Adopt(ctx, []string{"idx-unverified", "idx-uncontrollable"}, true, "monitoring batch")
	if err != nil || adopted != 1 {
		t.Fatalf("adopt = %d, %v", adopted, err)
	}
	account, found, err := repo.GetAutomationAccount(ctx, "idx-unverified")
	if err != nil || !found {
		t.Fatalf("read unverified account: found=%v err=%v", found, err)
	}
	if account.State != StateMonitoring || account.MonitorReason != ReasonUnverifiedCapability || !account.IsEnabled {
		t.Fatalf("unverified adoption must start monitor-only: %+v", account)
	}
	if _, found, err := repo.GetAutomationAccount(ctx, "idx-uncontrollable"); err != nil || found {
		t.Fatalf("uncontrollable credential must not be adopted: found=%v err=%v", found, err)
	}

	resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-unverified", "openrouter", 100, clock.Now().UnixMilli(), resetAt)
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("monitor-only adoption must never disable: %+v", calls)
	}
}

// TestServiceDisablesThenEnablesAtTheReset exercises the whole persisted plan: a
// disable confirmed by CPA, then an on-time enable at the recorded reset with no
// pre-probe, then a completed plan.
func TestServiceDisablesThenEnablesAtTheReset(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "codex-1.json", Provider: "codex", Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	adopted, err := service.Adopt(ctx, []string{"idx-1"}, true, "first batch")
	if err != nil || adopted != 1 {
		t.Fatalf("adopt = %d, %v", adopted, err)
	}
	resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), resetAt)

	service.RunOnce(ctx)
	calls := writer.snapshot()
	if len(calls) != 1 || !calls[0].disabled || calls[0].name != "codex-1.json" {
		t.Fatalf("disable calls = %+v", calls)
	}
	account, found, err := repo.GetAutomationAccount(ctx, "idx-1")
	if err != nil || !found {
		t.Fatalf("read account: %v %v", found, err)
	}
	if !account.IsDisabledByAutomation() || account.State != StateDisabled {
		t.Fatalf("account after disable = %+v", account)
	}
	if account.CycleResetAtMS == nil || *account.CycleResetAtMS != resetAt {
		t.Fatalf("cycle reset = %+v", account.CycleResetAtMS)
	}

	// CPA now reports the confirmed write. A second pass with the same reading
	// must keep waiting rather than disable again.
	credentials[0].Disabled = true
	service.RunOnce(ctx)
	if len(writer.snapshot()) != 1 {
		t.Fatal("a repeated pass must not re-close the credential")
	}

	// The credential is now disabled in CPA; at the reset it must be enabled once.
	clock.Set(time.UnixMilli(resetAt))
	service.RunOnce(ctx)
	calls = writer.snapshot()
	if len(calls) != 2 || calls[1].disabled {
		t.Fatalf("enable calls = %+v", calls)
	}
	account, _, _ = repo.GetAutomationAccount(ctx, "idx-1")
	if account.IsDisabledByAutomation() || account.State != StateIdle {
		t.Fatalf("account after enable = %+v", account)
	}
	if account.LastError != "" || account.NextAttemptAtMS != nil {
		t.Fatalf("a completed enable must clear the retry state: %+v", account)
	}
	if account.CycleResetAtMS != nil {
		t.Fatalf("a completed enable must consume the cycle recovery time: %+v", account)
	}
	if recovery, ok := RecoveryDue(account, clock.Now()); ok {
		t.Fatalf("a completed enable must not expose pending recovery: %+v", recovery)
	}
}

// TestServiceCancelWinsOverTheBackgroundEnable is the race the shared lock and the
// intent version exist for: once a cancel has returned, a pass that read the plan
// earlier must not turn the credential back on.
func TestServiceCancelWinsOverTheBackgroundEnable(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "codex-1.json", Provider: "codex", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
	if _, err := repo.SetAutomationManualReset(ctx, "idx-1", resetAt, 1, clock.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelAccount(ctx, "idx-1"); err != nil {
		t.Fatal(err)
	}
	// The reset is due, but the switch is off: nothing may be enabled.
	clock.Set(time.UnixMilli(resetAt))
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a cancelled plan must not enable: %+v", calls)
	}
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.IsEnabled {
		t.Fatalf("cancel must leave the switch off: %+v", account)
	}
}

// TestServicePersistsEnableBackoff proves a failed enable backs off on disk and is
// not retried before the persisted instant.
func TestServicePersistsEnableBackoff(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "codex-1.json", Provider: "codex", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	resetAt := clock.Now().UnixMilli()
	if _, err := repo.SetAutomationManualReset(ctx, "idx-1", resetAt+time.Minute.Milliseconds(), 1, clock.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	clock.Set(time.UnixMilli(resetAt + time.Minute.Milliseconds()))

	writer.setFailure(errors.New("upstream said no"))
	service.RunOnce(ctx)
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.NextAttemptAtMS == nil || account.AttemptCount != 1 || account.LastError == "" {
		t.Fatalf("failed enable must persist a backoff: %+v", account)
	}
	nextAttempt := *account.NextAttemptAtMS

	// Before the backoff instant: no new attempt.
	writer.setFailure(nil)
	callsBefore := len(writer.snapshot())
	service.RunOnce(ctx)
	if len(writer.snapshot()) != callsBefore {
		t.Fatal("a pass inside the backoff must not retry")
	}

	// At the backoff instant: the enable is attempted and completes.
	clock.Set(time.UnixMilli(nextAttempt))
	service.RunOnce(ctx)
	account, _, _ = repo.GetAutomationAccount(ctx, "idx-1")
	if account.NextAttemptAtMS != nil || account.AttemptCount != 0 {
		t.Fatalf("a completed retry must clear the backoff: %+v", account)
	}
}

// TestServiceReconcilesAnExternalReEnable proves an externally re-enabled account
// is not re-closed against the stale reading that disabled it.
func TestServiceReconcilesAnExternalReEnable(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "codex-1.json", Provider: "codex", Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), resetAt)
	service.RunOnce(ctx)
	if len(writer.snapshot()) != 1 {
		t.Fatal("expected the first disable")
	}

	// The operator re-enables the credential outside OMC.
	credentials[0].Disabled = false
	service.RunOnce(ctx)
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.IsDisabledByAutomation() {
		t.Fatalf("external re-enable must end the disable episode: %+v", account)
	}
	// The stale reading is still the newest one; it must not re-disable.
	service.RunOnce(ctx)
	if len(writer.snapshot()) != 1 {
		t.Fatal("a reconciled account must not be re-closed by the stale reading")
	}
}

// TestServicePlansRecoveryForADisabledAdoptedAccount also covers rows written by the
// original takeover implementation, which stored the reset on an idle row. That row
// shape must still enable at the reset without ever recording a disable.
func TestServicePlansRecoveryForADisabledAdoptedAccount(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "claude-1.json", Provider: "claude", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "claude", 100, clock.Now().UnixMilli(), resetAt)

	adopted, err := service.Adopt(ctx, []string{"idx-1"}, true, "takeover")
	if err != nil || adopted != 1 {
		t.Fatalf("adopt = %d, %v", adopted, err)
	}
	account, found, err := repo.GetAutomationAccount(ctx, "idx-1")
	if err != nil || !found {
		t.Fatalf("read account: %v %v", found, err)
	}
	if account.CycleResetAtMS == nil || *account.CycleResetAtMS != resetAt || account.CycleKey == "" || account.DisableObservedAtMS == nil {
		t.Fatalf("a takeover must persist the recovery plan and its watermark: %+v", account)
	}
	if account.LastDisableAtMS != nil || account.IsDisabledByAutomation() {
		t.Fatalf("a takeover must not record a disable: %+v", account)
	}
	if account.ManualResetAtMS != nil || account.ManualResetConsumed {
		t.Fatalf("a takeover must not invent a manual reset: %+v", account)
	}
	if _, err := repo.SQL().ExecContext(ctx, `
		UPDATE account_automation_accounts
		SET state = ?, monitor_reason = ''
		WHERE auth_index = ?`, StateIdle, "idx-1"); err != nil {
		t.Fatal(err)
	}
	account, _, _ = repo.GetAutomationAccount(ctx, "idx-1")
	if account.LastDisableAtMS != nil || account.LastEnableAtMS != nil {
		t.Fatalf("legacy takeover fixture must have no completed action: %+v", account)
	}

	// Before the reset the account is monitored; nothing is written to CPA.
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("nothing must be written before the reset: %+v", calls)
	}

	// At the reset the credential is enabled directly, with no disable ever sent.
	clock.Set(time.UnixMilli(resetAt))
	service.RunOnce(ctx)
	calls := writer.snapshot()
	if len(calls) != 1 || calls[0].disabled || calls[0].authIndex != "idx-1" || calls[0].name != "claude-1.json" {
		t.Fatalf("recovery calls = %+v", calls)
	}
	account, _, _ = repo.GetAutomationAccount(ctx, "idx-1")
	if !account.IsEnabled || account.State != StateIdle || account.LastEnableAtMS == nil {
		t.Fatalf("account after recovery enable = %+v", account)
	}
	if account.IsDisabledByAutomation() || account.LastDisableAtMS != nil {
		t.Fatalf("a takeover enable must not be reported as an automation disable: %+v", account)
	}
}

// TestServiceAdoptionWithoutAQualifyingResetWaitsForTheOperator proves the reset-less
// half of the takeover: no reading means no planned recovery, the account is shown as
// missing_reset, and the operator's hand-filled future time then schedules exactly one
// direct enable.
func TestServiceAdoptionWithoutAQualifyingResetWaitsForTheOperator(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "codex-1.json", Provider: "codex", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), 0)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, ""); err != nil {
		t.Fatal(err)
	}
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.CycleResetAtMS != nil || account.ManualResetAtMS != nil {
		t.Fatalf("a reset-less reading must not plan a recovery: %+v", account)
	}
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a missing reset must not act: %+v", calls)
	}
	account, _, _ = repo.GetAutomationAccount(ctx, "idx-1")
	if account.MonitorReason != ReasonMissingReset {
		t.Fatalf("state = %+v, want canonical missing_reset", account)
	}

	// The operator hand-fills a future recovery time; only its instant enables.
	manual := clock.Now().Add(2 * time.Hour).UnixMilli()
	if _, err := service.SetManualReset(ctx, "idx-1", manual); err != nil {
		t.Fatal(err)
	}
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a future manual reset must wait: %+v", calls)
	}
	clock.Set(time.UnixMilli(manual))
	service.RunOnce(ctx)
	calls := writer.snapshot()
	if len(calls) != 1 || calls[0].disabled {
		t.Fatalf("manual recovery calls = %+v", calls)
	}
	account, _, _ = repo.GetAutomationAccount(ctx, "idx-1")
	if account.IsDisabledByAutomation() || account.LastEnableAtMS == nil {
		t.Fatalf("a manual recovery must enable without claiming a disable: %+v", account)
	}
}

// TestServiceIgnoresUnverifiedProviderWeeklyExhaustion proves an unknown provider that
// publishes a weekly 100 is not disabled: the capability registry, not the shape of the
// reading, decides whether a provider's account-level weekly window may be acted on.
func TestServiceIgnoresUnverifiedProviderWeeklyExhaustion(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "openrouter-1.json", Provider: "openrouter", Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, "takeover"); err != nil {
		t.Fatal(err)
	}
	resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "openrouter", 100, clock.Now().UnixMilli(), resetAt)

	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("an unverified provider must not be disabled: %+v", calls)
	}
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.MonitorReason != ReasonUnverifiedCapability {
		t.Fatalf("reason = %q, want %q", account.MonitorReason, ReasonUnverifiedCapability)
	}
	if account.IsDisabledByAutomation() {
		t.Fatalf("no disable may be recorded for an unverified provider: %+v", account)
	}
}

// TestServiceManualResetRefusesUnverifiedProvider proves the hand-filled recovery time is
// a supplement, not a capability credential: an unknown provider cannot be handed a future
// time to bypass the admission rule, nothing is enabled, and no live account is disabled.
func TestServiceManualResetRefusesUnverifiedProvider(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "openrouter-1.json", Provider: "openrouter", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, "takeover"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetManualReset(ctx, "idx-1", clock.Now().Add(2*time.Hour).UnixMilli()); !errors.Is(err, ErrUnverifiedWeeklyCapability) {
		t.Fatalf("manual reset on an unverified provider = %v, want refused", err)
	}
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.ManualResetAtMS != nil {
		t.Fatalf("a refused manual reset must not be stored: %+v", account)
	}
	clock.Set(clock.Now().Add(3 * time.Hour))
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a refused manual reset must not enable: %+v", calls)
	}
}

// TestServiceAdoptionRecoveryRequiresMatchingProvider pins the first-takeover identity rule:
// an adopted-while-disabled account whose weekly reading does not name the provider the live
// credential does gets no recovery plan, so it cannot be enabled at a reset it does not own.
func TestServiceAdoptionRecoveryRequiresMatchingProvider(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "claude-1.json", Provider: "claude", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
	// The reading belongs to a different provider than the credential being adopted.
	insertWeekly(t, repo, "idx-1", "codex", 100, clock.Now().UnixMilli(), resetAt)

	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, "takeover"); err != nil {
		t.Fatal(err)
	}
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.CycleResetAtMS != nil || account.DisableObservedAtMS != nil {
		t.Fatalf("a mismatched reading must not plan a recovery: %+v", account)
	}
	clock.Set(time.UnixMilli(resetAt))
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a mismatched reading must not enable: %+v", calls)
	}
}

// TestServiceDoesNotExecuteARecoveryPlanForAChangedProvider pins the identity change rule:
// when the credential under the same auth index is now a different provider, the plan saved
// for the old provider is not executed.
func TestServiceDoesNotExecuteARecoveryPlanForAChangedProvider(t *testing.T) {
	ctx := context.Background()
	repo := automationRepo(t)
	clock := &testClock{now: time.UnixMilli(1_000_000_000_000)}
	writer := &fakeWriter{}
	credentials := []Credential{{AuthIndex: "idx-1", Name: "claude-1.json", Provider: "claude", Disabled: true, Controllable: true}}
	service := newTestService(repo, clock, writer, &credentials)

	resetAt := clock.Now().Add(48 * time.Hour).UnixMilli()
	insertWeekly(t, repo, "idx-1", "claude", 100, clock.Now().UnixMilli(), resetAt)
	if _, err := service.Adopt(ctx, []string{"idx-1"}, true, "takeover"); err != nil {
		t.Fatal(err)
	}
	account, _, _ := repo.GetAutomationAccount(ctx, "idx-1")
	if account.CycleResetAtMS == nil {
		t.Fatalf("the takeover should have planned a recovery: %+v", account)
	}

	// The credential under the same auth index is now a different provider.
	credentials[0].Provider = "codex"
	credentials[0].Name = "codex-1.json"
	clock.Set(time.UnixMilli(resetAt))
	service.RunOnce(ctx)
	if calls := writer.snapshot(); len(calls) != 0 {
		t.Fatalf("a changed provider must not execute the old plan: %+v", calls)
	}
	account, _, _ = repo.GetAutomationAccount(ctx, "idx-1")
	if account.MonitorReason != ReasonIdentityMismatch {
		t.Fatalf("reason = %q, want %q", account.MonitorReason, ReasonIdentityMismatch)
	}
}
