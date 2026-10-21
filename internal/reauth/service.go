// Package reauth coordinates credential recovery without executing OAuth or reading mail.
package reauth

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

const (
	CODE_TTL            = 5 * time.Minute
	WAIT_TTL            = 10 * time.Minute
	SETTLE_DELAY        = 2 * time.Minute
	MAX_VERIFY_ATTEMPTS = 3
	TICK_INTERVAL       = time.Second
)

var (
	ErrConflict = errors.New("reauth_state_conflict")
	ErrDisabled = errors.New("reauth_disabled")
	codePattern = regexp.MustCompile(`^[0-9]{6}$`)
)

type Options struct {
	Logger  *slog.Logger
	Repo    *repository.Repository
	Now     func() time.Time
	Enabled func() bool
	Notify  func(context.Context, repository.ReauthJob, string) error
	Refresh func(context.Context, string) (string, error)
}
type codeValue struct {
	value   string
	expires time.Time
}
type Service struct {
	mu     transitionLock
	tickMu sync.Mutex
	opts   Options
	codes  map[string]codeValue
}

func New(opts Options) *Service {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{opts: opts, codes: map[string]codeValue{}, mu: make(transitionLock, 1)}
}

// Database maintenance can hold a transition longer than a callback budget.
// Code admission must honor cancellation while waiting for that transition too.
type transitionLock chan struct{}

func (m transitionLock) Lock()   { m <- struct{}{} }
func (m transitionLock) Unlock() { <-m }
func (m transitionLock) lockContext(ctx context.Context) error {
	select {
	case m <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *Service) Enabled() bool {
	return s != nil && s.opts.Repo != nil && s.opts.Enabled != nil && s.opts.Enabled()
}

func (s *Service) Observe(ctx context.Context, authIndex, provider, accountType, label, state string) error {
	metadata, isOAuth := CredentialProvider("", provider, accountType)
	if !s.Enabled() || !isOAuth || authIndex == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.opts.Now().UnixMilli()
	if state == "healthy" {
		return s.opts.Repo.CloseReauthCycle(ctx, authIndex, now)
	}
	if state != "auth_invalid" {
		return nil
	}
	label = security.RedactText(strings.TrimSpace(label))
	if label == "" {
		label = metadata.Name + " account"
	}
	if len(label) > 256 {
		label = label[:256]
	}
	return s.opts.Repo.CreateReauthJob(ctx, repository.ReauthJob{ID: uuid.NewString(), AuthIndex: authIndex, Provider: metadata.ID, Flow: metadata.Flow, CodeKind: metadata.CodeKind, AccountLabel: label, State: "pending", CreatedAtMS: now, UpdatedAtMS: now})
}
func (s *Service) Next(ctx context.Context, provider string) (repository.ReauthJob, error) {
	if !s.Enabled() {
		return repository.ReauthJob{}, ErrDisabled
	}
	return s.opts.Repo.NextReauthJob(ctx, provider)
}
func (s *Service) Get(ctx context.Context, id string) (repository.ReauthJob, error) {
	if !s.Enabled() {
		return repository.ReauthJob{}, ErrDisabled
	}
	return s.opts.Repo.GetReauthJob(ctx, id)
}

// Dismiss is an operator-confirmed correction, never automatic recovery. It
// takes the tick lock so an in-flight verifier or notification cannot revive it.
func (s *Service) Dismiss(ctx context.Context, id string) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.opts.Repo.GetReauthJob(ctx, id)
	if err != nil {
		return err
	}
	if j.State == "succeeded" || j.CycleClosedAtMS != 0 {
		return ErrConflict
	}
	if err := s.audit(ctx, j, "dismiss_misclassified"); err != nil {
		return err
	}
	if err := s.opts.Repo.DismissReauthJob(ctx, j, s.opts.Now().UnixMilli()); err != nil {
		return err
	}
	delete(s.codes, id)
	return nil
}
func (s *Service) audit(ctx context.Context, j repository.ReauthJob, action string) error {
	_, err := s.opts.Repo.RecordAuditEvent(ctx, repository.AuditEvent{OccurredAtMS: s.opts.Now().UnixMilli(), Action: "reauth." + action, TargetType: "reauth_job", TargetID: j.ID, Result: "admitted"})
	return err
}
func (s *Service) save(ctx context.Context, j repository.ReauthJob, previous string) error {
	j.UpdatedAtMS = s.opts.Now().UnixMilli()
	return s.opts.Repo.SaveReauthJob(ctx, j, previous)
}
func SafeFailure(reason string) bool {
	switch reason {
	case "password_required", "sso_required", "code_unavailable", "token_exchange_failed", "runner_failed":
		return true
	}
	return false
}
func (s *Service) Report(ctx context.Context, id, status, reason string, start OAuthStart) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.opts.Repo.GetReauthJob(ctx, id)
	if err != nil {
		return err
	}
	previous := j.State
	if status != "oauth_started" && start != (OAuthStart{}) {
		return ErrConflict
	}
	if (j.State == "oauth_started" || j.State == "waiting_browser" || j.State == "waiting_callback" || j.State == "waiting_device" || j.State == "waiting_code" || j.State == "code_received" || j.State == "oauth_submitted") && s.opts.Now().UnixMilli() >= j.NextAttemptAtMS {
		return ErrConflict
	}
	switch status {
	case "oauth_started":
		metadata, ok := ProviderFor(j.Provider)
		if !ok || !validOAuthStart(metadata, start) {
			return ErrConflict
		}
		if j.State == "oauth_started" || j.State == "waiting_code" || j.State == "waiting_browser" || j.State == "waiting_callback" || j.State == "waiting_device" {
			if j.UserCode != start.UserCode || j.VerificationURL != start.VerificationURL {
				return ErrConflict
			}
			return nil
		}
		next, err := s.opts.Repo.NextReauthJob(ctx, j.Provider)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		if j.State != "pending" || next.ID != id {
			return ErrConflict
		}
		j.State = "oauth_started"
		j.NextAttemptAtMS = s.opts.Now().Add(WAIT_TTL).UnixMilli()
		if j.CodeKind == "device" {
			j.UserCode, j.VerificationURL = start.UserCode, start.VerificationURL
			j.ExpiresAtMS = s.opts.Now().Add(time.Duration(start.ExpiresIn) * time.Second).UnixMilli()
			j.NextAttemptAtMS = j.ExpiresAtMS
		}
	case "code_submitted":
		if j.State == "oauth_submitted" {
			return nil
		}
		if j.CodeKind != "otp" || j.State != "code_received" || !j.CodeConsumed {
			return ErrConflict
		}
		j.State = "oauth_submitted"
		j.NextAttemptAtMS = s.opts.Now().Add(WAIT_TTL).UnixMilli()
	case "succeeded":
		if j.State == "verifying" || j.State == "succeeded" {
			return nil
		}
		if j.State != "oauth_submitted" && j.State != "oauth_started" && j.State != "waiting_code" && j.State != "waiting_browser" && j.State != "waiting_callback" && j.State != "waiting_device" {
			return ErrConflict
		}
		j.State = "verifying"
		j.NextAttemptAtMS = s.opts.Now().Add(SETTLE_DELAY).UnixMilli()
	case "failed":
		if !SafeFailure(reason) {
			return ErrConflict
		}
		if j.State == "failed" {
			return nil
		}
		if j.State == "pending" || j.State == "succeeded" || j.State == "expired" || j.State == "verifying" {
			return ErrConflict
		}
		j.State = "failed"
		j.LastError = reason
		j.Notification = queueOutcome(j.Notification, "failed")
		j.NextAttemptAtMS = 0
	default:
		return ErrConflict
	}
	if err := s.audit(ctx, j, "status."+status); err != nil {
		return err
	}
	if err := s.save(ctx, j, previous); err != nil {
		return err
	}
	if status == "failed" {
		delete(s.codes, id)
	}
	return nil
}

// AcceptCode only sees messages that already passed the transport identity whitelist.
func (s *Service) AcceptCode(ctx context.Context, messageID, text string) error {
	if !s.Enabled() || messageID == "" || !codePattern.MatchString(text) {
		return nil
	}
	if err := s.mu.lockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	j, err := s.opts.Repo.NextReauthJob(ctx, "")
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if j.CodeKind != "otp" || j.State != "waiting_code" || s.opts.Now().UnixMilli() >= j.NextAttemptAtMS {
		return nil
	}
	used, err := s.opts.Repo.ReauthMessageUsed(ctx, messageID)
	if err != nil {
		return err
	}
	if used {
		return nil
	}
	if err := s.audit(ctx, j, "code.accept"); err != nil {
		return err
	}
	j.State = "code_received"
	j.MessageID = messageID
	j.Notification = "processing"
	j.NextAttemptAtMS = s.opts.Now().Add(CODE_TTL).UnixMilli()
	if err := s.save(ctx, j, "waiting_code"); err != nil {
		return err
	}
	s.codes[j.ID] = codeValue{value: text, expires: s.opts.Now().Add(CODE_TTL)}
	return nil
}
func (s *Service) TakeCode(ctx context.Context, id string) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.opts.Repo.GetReauthJob(ctx, id)
	if err != nil {
		return "", err
	}
	code, ok := s.codes[id]
	if j.CodeKind != "otp" || j.State != "code_received" || j.CodeConsumed {
		return "", nil
	}
	if !ok || !s.opts.Now().Before(code.expires) {
		delete(s.codes, id)
		j.State = "expired"
		j.Notification = ""
		j.NextAttemptAtMS = 0
		j.LastError = "code_expired"
		return "", s.save(ctx, j, "code_received")
	}
	if err := s.audit(ctx, j, "code.consume"); err != nil {
		return "", err
	}
	j.CodeConsumed = true
	j.NextAttemptAtMS = s.opts.Now().Add(WAIT_TTL).UnixMilli()
	if err := s.save(ctx, j, "code_received"); err != nil {
		return "", err
	}
	delete(s.codes, id)
	return code.value, nil
}

func (s *Service) Run(ctx context.Context) error {
	defer func() { s.mu.Lock(); clear(s.codes); s.mu.Unlock() }()
	ticker := time.NewTicker(TICK_INTERVAL)
	defer ticker.Stop()
	hasError := false
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := s.Tick(ctx)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil && !hasError {
				s.opts.Logger.WarnContext(ctx, "reauth background work failed", "reason", "service_unavailable")
			}
			if err == nil && hasError {
				s.opts.Logger.InfoContext(ctx, "reauth background work recovered")
			}
			hasError = err != nil
		}
	}
}

// Tick keeps network work outside the transition lock so code callbacks stay short.
func (s *Service) Tick(ctx context.Context) error {
	if !s.Enabled() {
		s.mu.Lock()
		clear(s.codes)
		s.mu.Unlock()
		return nil
	}
	s.tickMu.Lock()
	defer s.tickMu.Unlock()
	jobs, err := s.opts.Repo.ListReauthWork(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, j := range jobs {
		if err := s.step(ctx, j.ID); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
func (s *Service) step(ctx context.Context, id string) (stepErr error) {
	s.mu.Lock()
	j, err := s.opts.Repo.GetReauthJob(ctx, id)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	previous := j.State
	now := s.opts.Now()
	switch j.State {
	case "oauth_started":
		if now.UnixMilli() >= j.NextAttemptAtMS {
			j.State, j.LastError, j.Notification, j.NextAttemptAtMS = "expired", "code_expired", "", 0
			break
		}
		switch j.Flow {
		case "redirect_otp":
			j.State = "waiting_code"
		case "redirect_browser":
			j.State = "waiting_browser"
		case "redirect_manual_callback":
			j.State = "waiting_callback"
		case "device_code":
			j.State = "waiting_device"
		}
		j.Notification = "request"
	case "waiting_code", "waiting_browser", "waiting_callback", "waiting_device", "code_received", "oauth_submitted":
		_, hasCode := s.codes[id]
		if now.UnixMilli() >= j.NextAttemptAtMS || (j.State == "code_received" && !j.CodeConsumed && !hasCode) {
			j.State = "expired"
			j.LastError = "code_expired"
			j.Notification = ""
			j.NextAttemptAtMS = 0
			delete(s.codes, id)
		}
	}
	if j.State != previous {
		err = s.save(ctx, j, previous)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	// Delivery remains retryable without holding verification or the contact slot hostage.
	notifyErr := s.notify(ctx, j)
	defer func() { stepErr = errors.Join(notifyErr, stepErr) }()
	if (j.State != "verifying" && j.State != "failed") || j.NextAttemptAtMS == 0 || now.UnixMilli() < j.NextAttemptAtMS {
		return nil
	}
	if s.opts.Refresh == nil {
		return ErrDisabled
	}
	if err := s.audit(ctx, j, "verify"); err != nil {
		return err
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	state, refreshErr := s.opts.Refresh(refreshCtx, j.AuthIndex)
	cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, err := s.opts.Repo.GetReauthJob(ctx, id)
	if err != nil {
		return err
	}
	if latest.State != j.State {
		return nil
	}
	if j.State == "failed" {
		latest.NextAttemptAtMS = 0
		return s.save(ctx, latest, latest.State)
	}
	latest.Attempts++
	if refreshErr == nil && state == "healthy" {
		latest.State = "succeeded"
		latest.Notification = queueOutcome(latest.Notification, "succeeded")
		latest.NextAttemptAtMS = 0
	} else if latest.Attempts >= MAX_VERIFY_ATTEMPTS {
		latest.State = "failed"
		latest.Notification = queueOutcome(latest.Notification, "failed")
		latest.LastError = "verification_unavailable"
		if refreshErr == nil && state == "auth_invalid" {
			latest.LastError = "auth_invalid"
		}
		latest.NextAttemptAtMS = now.UnixMilli()
	} else {
		latest.NextAttemptAtMS = now.Add(SETTLE_DELAY * time.Duration(1<<(latest.Attempts-1))).UnixMilli()
	}
	if err := s.save(ctx, latest, j.State); err != nil {
		return err
	}
	if latest.State == "succeeded" {
		return s.opts.Repo.CloseReauthCycle(ctx, latest.AuthIndex, now.UnixMilli())
	}
	return nil
}

func (s *Service) notify(ctx context.Context, j repository.ReauthJob) error {
	if j.Notification == "" {
		return nil
	}
	notification, _, _ := strings.Cut(j.Notification, ",")
	if s.opts.Notify == nil {
		return ErrDisabled
	}
	if err := s.audit(ctx, j, "notify."+notification); err != nil {
		return err
	}
	sendCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	err := s.opts.Notify(sendCtx, j, notification)
	cancel()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	latest, err := s.opts.Repo.GetReauthJob(ctx, j.ID)
	if err != nil {
		return err
	}
	first, remaining, _ := strings.Cut(latest.Notification, ",")
	if first != notification {
		return nil
	}
	latest.Notification = remaining
	return s.save(ctx, latest, latest.State)
}

// A fast runner must not replace the acknowledgement before the contact sees it.
func queueOutcome(pending, outcome string) string {
	if pending == "processing" {
		return pending + "," + outcome
	}
	return outcome
}
