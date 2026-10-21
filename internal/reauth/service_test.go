package reauth

import (
	"context"
	"errors"
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
	if err := f.service.Observe(ctx, "synthetic-account", "claude", "oauth", "Claude test account", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	j, err := f.service.Next(ctx, "claude")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Report(ctx, j.ID, "oauth_started", "", OAuthStart{}); err != nil {
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
	if err := f.service.Report(ctx, id, "code_submitted", "", OAuthStart{}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Report(ctx, id, "succeeded", "", OAuthStart{}); err != nil {
		t.Fatal(err)
	}
}
func TestObserveRequiresOAuthAccountType(t *testing.T) {
	for _, accountType := range []string{"api_key", " API_KEY ", "service_account", "unknown", "oauth", " OAUTH ", ""} {
		t.Run("kind_"+accountType, func(t *testing.T) {
			f := newFixture(t)
			if err := f.service.Observe(context.Background(), "0123456789abcdef", "codex", accountType, "Synthetic account", "auth_invalid"); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := f.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if accountType == "" || strings.EqualFold(strings.TrimSpace(accountType), "oauth") {
				want = 1
			}
			if count != want {
				t.Fatalf("jobs=%d want=%d", count, want)
			}
		})
	}
}

func TestInvalidCycleAndSerialAdmission(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.start(t)
	for _, state := range []string{"auth_invalid", "transport", "unknown"} {
		if err := f.service.Observe(ctx, "synthetic-account", "claude", "oauth", "test", state); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.Observe(ctx, "other", "vertex", "oauth", "test", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	var count int
	_ = f.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs`).Scan(&count)
	if count != 1 {
		t.Fatalf("jobs=%d", count)
	}
	if err := f.service.Observe(ctx, "second", "claude", "oauth", "second", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	var second string
	_ = f.repo.SQL().QueryRow(`SELECT id FROM reauth_jobs WHERE auth_index='second'`).Scan(&second)
	if err := f.service.Report(ctx, second, "oauth_started", "", OAuthStart{}); err != ErrConflict {
		t.Fatalf("parallel start: %v", err)
	}
	if err := f.service.Report(ctx, id, "failed", "token_exchange_failed", OAuthStart{}); err != nil {
		t.Fatal(err)
	}
	_ = f.service.Observe(ctx, "synthetic-account", "claude", "oauth", "test", "auth_invalid")
	_ = f.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs WHERE auth_index='synthetic-account'`).Scan(&count)
	if count != 1 {
		t.Fatal("terminal failure recreated same episode")
	}
	_ = f.service.Observe(ctx, "synthetic-account", "claude", "oauth", "test", "healthy")
	_ = f.service.Observe(ctx, "synthetic-account", "claude", "oauth", "test", "auth_invalid")
	_ = f.repo.SQL().QueryRow(`SELECT COUNT(*) FROM reauth_jobs WHERE auth_index='synthetic-account'`).Scan(&count)
	if count != 2 {
		t.Fatal("new episode did not create job")
	}
}
func TestHealthyObservationClosesActiveEpisode(t *testing.T) {
	for _, state := range []string{"waiting_code", "code_received", "oauth_submitted"} {
		for _, terminal := range []string{"expired", "failed"} {
			t.Run(state+"/"+terminal, func(t *testing.T) {
				f := newFixture(t)
				ctx := context.Background()
				id := f.start(t)
				if state != "waiting_code" {
					if err := f.service.AcceptCode(ctx, "synthetic-message", "123456"); err != nil {
						t.Fatal(err)
					}
				}
				if state == "oauth_submitted" {
					if _, err := f.service.TakeCode(ctx, id); err != nil {
						t.Fatal(err)
					}
					if err := f.service.Report(ctx, id, "code_submitted", "", OAuthStart{}); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.service.Observe(ctx, "synthetic-account", "claude", "oauth", "test", "healthy"); err != nil {
					t.Fatal(err)
				}
				j, err := f.service.Next(ctx, "claude")
				if err != nil || j.ID != id || j.State != state {
					t.Fatalf("healthy reading released active contact: job=%+v err=%v", j, err)
				}
				if terminal == "expired" {
					f.now = time.UnixMilli(j.NextAttemptAtMS)
					if err := f.service.Tick(ctx); err != nil {
						t.Fatal(err)
					}
				} else if err := f.service.Report(ctx, id, "failed", "runner_failed", OAuthStart{}); err != nil {
					t.Fatal(err)
				}
				if err := f.service.Observe(ctx, "synthetic-account", "claude", "oauth", "test", "auth_invalid"); err != nil {
					t.Fatal(err)
				}
				j, err = f.service.Next(ctx, "claude")
				if err != nil || j.ID == id || j.State != "pending" {
					t.Fatalf("new invalid episode suppressed: job=%+v err=%v", j, err)
				}
			})
		}
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
func TestNotificationFailureDoesNotBlockVerification(t *testing.T) {
	for _, result := range []string{"healthy", "auth_invalid"} {
		t.Run(result, func(t *testing.T) {
			f := newFixture(t)
			f.result = result
			ctx := context.Background()
			id := f.start(t)
			f.submit(t, id)
			sendErr := errors.New("synthetic sender unavailable")
			sends := 0
			f.service.opts.Notify = func(context.Context, repository.ReauthJob, string) error {
				sends++
				return sendErr
			}
			if err := f.service.Tick(ctx); !errors.Is(err, sendErr) {
				t.Fatalf("notification failure not reported: %v", err)
			}
			if f.refreshes != 0 {
				t.Fatal("settle delay bypassed")
			}
			attempts := 1
			if result == "auth_invalid" {
				attempts = MAX_VERIFY_ATTEMPTS
			}
			for attempt := 1; attempt <= attempts; attempt++ {
				j, err := f.service.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				f.now = time.UnixMilli(j.NextAttemptAtMS)
				if err := f.service.Tick(ctx); !errors.Is(err, sendErr) {
					t.Fatalf("notification failure not reported: %v", err)
				}
				if f.refreshes != attempt {
					t.Fatalf("notification blocked due verification: refreshes=%d want=%d", f.refreshes, attempt)
				}
			}
			j, err := f.service.Get(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			expected := "succeeded"
			if result == "auth_invalid" {
				expected = "failed"
			}
			if j.State != expected || j.Notification != "processing,"+expected || sends != attempts+1 {
				t.Fatalf("state=%s notification=%s sends=%d", j.State, j.Notification, sends)
			}
			// Deliver the acknowledgement, then fail the outcome receipt itself.
			f.service.opts.Notify = func(_ context.Context, _ repository.ReauthJob, kind string) error {
				if kind == "processing" {
					return nil
				}
				return sendErr
			}
			if err := f.service.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			if result == "auth_invalid" && f.refreshes != MAX_VERIFY_ATTEMPTS+1 {
				t.Fatal("queued failure receipt blocked final refresh")
			}
			if err := f.service.Tick(ctx); !errors.Is(err, sendErr) {
				t.Fatalf("outcome receipt was not retried: %v", err)
			}
			if err := f.service.Observe(ctx, "second", "claude", "oauth", "second", "auth_invalid"); err != nil {
				t.Fatal(err)
			}
			next, err := f.service.Next(ctx, "claude")
			if err != nil || next.AuthIndex != "second" {
				t.Fatalf("contact slot retained: job=%+v err=%v", next, err)
			}
			if err := f.service.Report(ctx, next.ID, "oauth_started", "", OAuthStart{}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFailedReceiptDoesNotBlockFinalRefresh(t *testing.T) {
	f := newFixture(t)
	f.result = "auth_invalid"
	ctx := context.Background()
	id := f.start(t)
	f.submit(t, id)
	for attempt := 0; attempt < MAX_VERIFY_ATTEMPTS; attempt++ {
		j, err := f.service.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		f.now = time.UnixMilli(j.NextAttemptAtMS)
		if err := f.service.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	sendErr := errors.New("synthetic sender unavailable")
	f.service.opts.Notify = func(_ context.Context, _ repository.ReauthJob, kind string) error {
		if kind != "failed" {
			t.Fatalf("receipt=%s", kind)
		}
		return sendErr
	}
	if err := f.service.Tick(ctx); !errors.Is(err, sendErr) {
		t.Fatalf("notification failure not reported: %v", err)
	}
	j, err := f.service.Get(ctx, id)
	if err != nil || j.State != "failed" || j.NextAttemptAtMS != 0 || f.refreshes != MAX_VERIFY_ATTEMPTS+1 {
		t.Fatalf("failed receipt blocked final refresh: job=%+v refreshes=%d err=%v", j, f.refreshes, err)
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
	if err := f.service.Report(ctx, id, "failed", "runner_failed", OAuthStart{}); err == nil {
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
	if err := f.service.Report(ctx, id, "failed", "runner_failed", OAuthStart{}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	_ = f.service.Observe(ctx, "synthetic-account", "claude", "oauth", "test", "healthy")
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

func TestProviderFlowsAndGlobalContactSlot(t *testing.T) {
	for _, c := range []struct{ provider, flow, kind, waiting string }{
		{"anthropic", "redirect_otp", "otp", "waiting_code"},
		{"codex", "redirect_otp", "otp", "waiting_code"},
		{"antigravity", "redirect_browser", "none", "waiting_browser"},
		{"devin", "redirect_manual_callback", "none", "waiting_callback"},
		{"kimi", "device_code", "device", "waiting_device"},
		{"kimi-ai", "device_code", "device", "waiting_device"},
		{"xai", "device_code", "device", "waiting_device"},
		{"meta", "device_code", "device", "waiting_device"},
	} {
		t.Run(c.provider, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			if err := f.service.Observe(ctx, "account", c.provider, "oauth", "", "auth_invalid"); err != nil {
				t.Fatal(err)
			}
			p, _ := ProviderFor(c.provider)
			j, err := f.service.Next(ctx, p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if j.Flow != c.flow || j.CodeKind != c.kind || j.Provider != p.ID || j.AccountLabel != p.Name+" account" {
				t.Fatalf("metadata=%+v", j)
			}
			start := OAuthStart{}
			if c.kind == "device" {
				start = OAuthStart{Flow: "device", UserCode: "ABCD-1234", VerificationURL: "https://verify.example.test/device", ExpiresIn: 90}
			}
			if err := f.service.Report(ctx, j.ID, "oauth_started", "", start); err != nil {
				t.Fatal(err)
			}
			deadline, _ := f.service.Get(ctx, j.ID)
			if err := f.service.Report(ctx, j.ID, "oauth_started", "", start); err != nil {
				t.Fatal(err)
			}
			if err := f.service.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			j, _ = f.service.Get(ctx, j.ID)
			if j.State != c.waiting || j.NextAttemptAtMS != deadline.NextAttemptAtMS {
				t.Fatalf("waiting=%+v", j)
			}
			if c.kind == "device" && (j.UserCode != start.UserCode || j.VerificationURL != start.VerificationURL || j.ExpiresAtMS != f.now.Add(90*time.Second).UnixMilli()) {
				t.Fatalf("device metadata=%+v", j)
			}
			other := "codex"
			if p.ID == other {
				other = "antigravity"
			}
			if err := f.service.Observe(ctx, "other", other, "oauth", "", "auth_invalid"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.Next(ctx, other); err == nil {
				t.Fatal("other provider acquired active slot")
			}
			var otherID string
			if err := f.repo.SQL().QueryRow("SELECT id FROM reauth_jobs WHERE auth_index='other'").Scan(&otherID); err != nil {
				t.Fatal(err)
			}
			if err := f.service.Report(ctx, otherID, "oauth_started", "", OAuthStart{}); err != ErrConflict {
				t.Fatalf("parallel admission=%v", err)
			}
			if err := f.service.AcceptCode(ctx, "message", "123456"); err != nil {
				t.Fatal(err)
			}
			code, err := f.service.TakeCode(ctx, j.ID)
			if err != nil {
				t.Fatal(err)
			}
			if c.kind == "otp" {
				if code != "123456" {
					t.Fatal("OTP unavailable")
				}
				if err := f.service.Report(ctx, j.ID, "code_submitted", "", OAuthStart{}); err != nil {
					t.Fatal(err)
				}
			} else {
				if code != "" {
					t.Fatal("non-OTP code disclosed")
				}
				if err := f.service.Report(ctx, j.ID, "code_submitted", "", OAuthStart{}); err != ErrConflict {
					t.Fatal("non-OTP accepted code status")
				}
			}
			if err := f.service.Report(ctx, j.ID, "succeeded", "", OAuthStart{}); err != nil {
				t.Fatal(err)
			}
			f.now = f.now.Add(SETTLE_DELAY)
			if err := f.service.Tick(ctx); err != nil {
				t.Fatal(err)
			}
			j, _ = f.service.Get(ctx, j.ID)
			if j.State != "succeeded" {
				t.Fatalf("result=%+v", j)
			}
			if _, err := f.service.Next(ctx, other); err != nil {
				t.Fatalf("settled slot not released: %v", err)
			}
		})
	}
}

func TestDeviceStartValidationAndExpiry(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.service.Observe(ctx, "device", "xai", "oauth", "", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	j, err := f.service.Next(ctx, "xai")
	if err != nil {
		t.Fatal(err)
	}
	valid := OAuthStart{Flow: "device", UserCode: "ABCD-1234", VerificationURL: "https://accounts.x.ai/device", ExpiresIn: 60}
	for _, change := range []func(*OAuthStart){
		func(s *OAuthStart) { s.Flow = "redirect" }, func(s *OAuthStart) { s.UserCode = "" },
		func(s *OAuthStart) { s.ExpiresIn = 0 }, func(s *OAuthStart) { s.ExpiresIn = 86401 },
		func(s *OAuthStart) { s.VerificationURL = "javascript:alert(1)" },
		func(s *OAuthStart) { s.VerificationURL = "https://accounts.x.ai/device?token=secret" },
		func(s *OAuthStart) { s.VerificationURL = "https://user:password@accounts.x.ai/device" },
	} {
		bad := valid
		change(&bad)
		if err := f.service.Report(ctx, j.ID, "oauth_started", "", bad); err != ErrConflict {
			t.Fatalf("bad device start accepted: %v", err)
		}
	}
	if err := f.service.Report(ctx, j.ID, "oauth_started", "", valid); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(time.Minute)
	if err := f.service.Report(ctx, j.ID, "succeeded", "", OAuthStart{}); err != ErrConflict {
		t.Fatal("expired grant accepted")
	}
	f.service = New(f.service.opts)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ = f.service.Get(ctx, j.ID)
	if j.State != "expired" {
		t.Fatalf("expired=%+v", j)
	}
}

func TestOTPFlowCanCompleteWithoutEmailChallenge(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.start(t)
	if err := f.service.Report(ctx, id, "succeeded", "", OAuthStart{}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(SETTLE_DELAY)
	if err := f.service.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	j, _ := f.service.Get(ctx, id)
	if j.State != "succeeded" {
		t.Fatal(j.State)
	}
}

func TestBrowserNotificationFailureStillVerifies(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.service.Observe(ctx, "browser", "antigravity", "oauth", "", "auth_invalid"); err != nil {
		t.Fatal(err)
	}
	j, err := f.service.Next(ctx, "antigravity")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Report(ctx, j.ID, "oauth_started", "", OAuthStart{}); err != nil {
		t.Fatal(err)
	}
	sendErr := errors.New("synthetic delivery failure")
	f.service.opts.Notify = func(context.Context, repository.ReauthJob, string) error { return sendErr }
	if err := f.service.Tick(ctx); !errors.Is(err, sendErr) {
		t.Fatal(err)
	}
	if err := f.service.Report(ctx, j.ID, "succeeded", "", OAuthStart{}); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(SETTLE_DELAY)
	if err := f.service.Tick(ctx); !errors.Is(err, sendErr) {
		t.Fatal(err)
	}
	j, _ = f.service.Get(ctx, j.ID)
	if j.State != "succeeded" || f.refreshes != 1 {
		t.Fatalf("delivery blocked verification: %+v", j)
	}
}
