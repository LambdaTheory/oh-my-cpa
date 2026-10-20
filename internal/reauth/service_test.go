package reauth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

type fixture struct {
	service   *Service
	repo      *repository.Repository
	now       time.Time
	cards     []string
	refreshes int
	result    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := repository.Open(context.Background(), fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{repo: repository.New(db), now: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), result: "healthy"}
	f.service = New(Options{Repo: f.repo, Now: func() time.Time { return f.now }, Enabled: func() bool { return true }, Notify: func(_ context.Context, j repository.ReauthJob, kind string) error {
		f.cards = append(f.cards, kind)
		return nil
	}, Refresh: func(context.Context, string) (string, error) { f.refreshes++; return f.result, nil }})
	return f
}
func (f *fixture) start(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	if err := f.service.Observe(ctx, "synthetic-account", "claude", "Claude test account", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	j, err := f.service.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Report(ctx, j.ID, "oauth_started", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	return j.ID
}
func (f *fixture) submit(t *testing.T, id string) {
	t.Helper()
	ctx := context.Background()
	if err := f.service.AcceptCode(ctx, "synthetic-message", "123456"); err != nil {
		t.Fatal(err)
	}
	code, err := f.service.TakeCode(ctx, id)
	if err != nil || code != "123456" {
		t.Fatalf("code delivery failed: %v", err)
	}
	if err := f.service.Report(ctx, id, "code_submitted", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Report(ctx, id, "succeeded", ""); err != nil {
		t.Fatal(err)
	}
}
func TestInvalidCycleAndSerialAdmission(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.start(t)
	for _, state := range []string{"auth_invalid", "transport", "unknown"} {
		if err := f.service.Observe(ctx, "synthetic-account", "claude", "test", state); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.Observe(ctx, "other", "codex", "test", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = f.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&count)
	if count != 1 {
		t.Fatalf("jobs=%d", count)
	}
	if err := f.service.Observe(ctx, "second", "claude", "second", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	var second string
	_ = f.repo.SQL().QueryRow(`SELECT id FROM reauth_jobs WHERE auth_index='second'`).Scan(&second)
	if err := f.service.Report(ctx, second, "oauth_started", ""); err != ErrConflict {
		t.Fatalf("parallel start: %v", err)
	}
	if err := f.service.Report(ctx, id, "failed", "token_exchange_failed"); err != nil {
		t.Fatal(err)
	}
	_ = f.service.Observe(ctx, "synthetic-account", "claude", "test", "auth_invalid")
	_ = f.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs WHERE auth_index='synthetic-account'`).Scan(&count)
	if count != 1 {
		t.Fatal("terminal failure recreated same episode")
	}
	_ = f.service.Observe(ctx, "synthetic-account", "claude", "test", "healthy")
	_ = f.service.Observe(ctx, "synthetic-account", "claude", "test", "auth_invalid")
	_ = f.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs WHERE auth_index='synthetic-account'`).Scan(&count)
	if count != 2 {
		t.Fatal("new episode did not create job")
	}
}
func TestCodeOnceExpiryAndRestart(t *testing.T) {
	for _, mode := range []string{"consume", "expire", "restart"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			id := f.start(t)
			for _, invalid := range []string{"12345", "1234567", " 123456", "A1 123456", "abcdef"} {
				if err := f.service.AcceptCode(ctx, invalid, invalid); err != nil {
					t.Fatal(err)
				}
			}
			j, _ := f.service.Get(ctx, id)
			if j.State != "waiting_code" {
				t.Fatal("invalid input accepted")
			}
			_ = f.service.AcceptCode(ctx, "msg-one", "123456")
			_ = f.service.AcceptCode(ctx, "msg-two", "654321")
			switch mode {
			case "expire":
				f.now = f.now.Add(CODE_TTL)
			case "restart":
				f.service = New(f.service.opts)
			}
			code, err := f.service.TakeCode(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "consume" && code != "123456" {
				t.Fatal("first code not retained")
			}
			if mode != "consume" && code != "" {
				t.Fatal("expired/lost code disclosed")
			}
			code, err = f.service.TakeCode(ctx, id)
			if err != nil || code != "" {
				t.Fatal("code replayed")
			}
			var stored string
			_ = f.repo.SQL().QueryRow(`SELECT last_error || notification || message_id FROM reauth_jobs WHERE id=?`, id).Scan(&stored)
			if strings.Contains(stored, "123456") || strings.Contains(stored, "654321") {
				t.Fatal("code persisted")
			}
		})
	}
}
func TestVerificationSettlementAndBackoff(t *testing.T) {
	for _, result := range []string{"healthy", "auth_invalid"} {
		t.Run(result, func(t *testing.T) {
			f := newFixture(t)
			f.result = result
			ctx := context.Background()
			id := f.start(t)
			f.submit(t, id)
			if err := f.service.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if f.refreshes != 0 {
				t.Fatal("settle delay bypassed")
			}
			for attempt := 1; attempt <= MAX_VERIFY_ATTEMPTS; attempt++ {
				j, _ := f.service.Get(ctx, id)
				f.now = time.UnixMilli(j.NextAttemptAtMS)
				if err := f.service.Tick(ctx); err != nil {
					t.Fatal(err)
				}
				j, _ = f.service.Get(ctx, id)
				if result == "healthy" {
					if j.State != "succeeded" {
						t.Fatalf("state=%s", j.State)
					}
					break
				}
				if attempt < MAX_VERIFY_ATTEMPTS && j.State != "verifying" {
					t.Fatalf("early failure=%s", j.State)
				}
			}
			if err := f.service.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			j, _ := f.service.Get(ctx, id)
			expected := "succeeded"
			expectedRefreshes := 1
			if result != "healthy" {
				expected = "failed"
				expectedRefreshes = 4
			}
			if j.State != expected || f.refreshes != expectedRefreshes {
				t.Fatalf("state=%s refreshes=%d", j.State, f.refreshes)
			}
			if strings.Join(f.cards, ",") != "request,processing,"+expected {
				t.Fatalf("cards=%v", f.cards)
			}
		})
	}
}
func TestAuditFailureRefusesCodeAndStatus(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.start(t)
	_, err := f.repo.SQL().Exec(`CREATE TRIGGER deny_reauth_audit BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL, 'audit unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.AcceptCode(ctx, "msg", "123456"); err == nil {
		t.Fatal("code accepted without audit")
	}
	if err := f.service.Report(ctx, id, "failed", "runner_failed"); err == nil {
		t.Fatal("status accepted without audit")
	}
	j, _ := f.service.Get(ctx, id)
	if j.State != "waiting_code" || len(f.service.codes) != 0 {
		t.Fatal("audit failure changed state")
	}
}

func TestReauthWaitExpiryAndMessageReplayAcrossJobs(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.start(t)
	_ = f.service.AcceptCode(ctx, "durable-message", "123456")
	if err := f.service.Report(ctx, id, "failed", "runner_failed"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	_ = f.service.Observe(ctx, "synthetic-account", "claude", "test", "healthy")
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	second := f.start(t)
	if err := f.service.AcceptCode(ctx, "durable-message", "123456"); err != nil {
		t.Fatal(err)
	}
	j, _ := f.service.Get(ctx, second)
	if j.State != "waiting_code" {
		t.Fatal("old message replay accepted")
	}
	f.now = f.now.Add(WAIT_TTL)
	if err := f.service.AcceptCode(ctx, "late-message", "654321"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ = f.service.Get(ctx, second)
	if j.State != "expired" {
		t.Fatal("wait did not expire")
	}
	if strings.Join(f.cards, ",") != "request,processing,failed,request" {
		t.Fatalf("unexpected timeout reminder: %v", f.cards)
	}
}

func TestReauthSlowSendDoesNotBlockCodeAcceptance(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.start(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	f.service.opts.Notify = func(context.Context, repository.ReauthJob, string) error { close(entered); <-release; return nil }
	j, _ := f.service.Get(ctx, id)
	j.Notification = "request"
	if err := f.repo.SaveReauthJob(ctx, j, j.State); err != nil {
		t.Fatal(err)
	}
	go func() { done <- f.service.Tick(ctx) }()
	<-entered
	if err := f.service.AcceptCode(ctx, "synthetic-message", "123456"); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	j, _ = f.service.Get(ctx, id)
	if j.State != "code_received" || j.Notification != "processing" {
		t.Fatal("send overwrote code transition")
	}
}

func TestReauthCodeConsumptionRequiresAudit(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.start(t)
	if err := f.service.AcceptCode(ctx, "synthetic-message", "123456"); err != nil {
		t.Fatal(err)
	}
	_, err := f.repo.SQL().Exec(`CREATE TRIGGER deny_reauth_consume BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL, 'audit unavailable'); END`)
	if err != nil {
		t.Fatal(err)
	}
	code, err := f.service.TakeCode(ctx, id)
	if err == nil || code != "" {
		t.Fatal("code disclosed without audit")
	}
	j, _ := f.service.Get(ctx, id)
	if j.CodeConsumed {
		t.Fatal("failed audit consumed code")
	}
}

func TestCodeAcceptanceHonorsCallbackCancellationWhileTransitionBusy(t *testing.T) {
	f := newFixture(t)
	f.start(t)
	f.service.mu.Lock()
	defer f.service.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.service.AcceptCode(ctx, "synthetic-message", "123456"); err != context.Canceled {
		t.Fatalf("callback cancellation=%v", err)
	}
}
