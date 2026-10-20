package automation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
	"github.com/oh-my-cpa/oh-my-cpa/internal/security"
)

// ErrAccountNotFound is returned when a mutation names a credential that is not on
// the adopted roster.
var ErrAccountNotFound = errors.New("automation_account_not_found")

// ErrUnverifiedWeeklyCapability is returned when an operator action would act on a
// provider whose account-level weekly window OMC has not verified. An unverified
// provider cannot be handed a recovery time to bypass the admission rule.
var ErrUnverifiedWeeklyCapability = errors.New("unverified_account_weekly_capability")

// maxStoredErrorRunes bounds a persisted failure message. The message is a
// gateway's own text, so it is redacted and bounded before it is stored.
const maxStoredErrorRunes = 400

// Credential is one live auth file the automation roster is keyed by.
type Credential struct {
	AuthIndex    string
	Name         string
	Provider     string
	AccountLabel string
	Disabled     bool
	// Controllable is true when OMC can write the credential's disabled state.
	Controllable bool
}

// AuditFunc records an automation transition on the audit trail.
type AuditFunc func(ctx context.Context, action, result, targetID string, details map[string]any)

// Service performs account-level auto-management. It reads the roster and the
// evidence from the repository, asks the pure Decide function what to do, and
// performs the CPA side effect. Every per-account mutation - from the background
// pass and from an HTTP handler alike - runs under one shared keyed lock, so a
// cancel that has returned cannot be followed by the loop enabling the account.
type Service struct {
	Repo   *repository.Repository
	Logger *slog.Logger
	Policy PolicyConfig

	// MasterEnabled reports whether automatic control is on for the stored web
	// switch. The environment master switch is resolved here, never in the
	// repository, so an env-forced off cannot be overwritten by a web write.
	MasterEnabled func(webEnabled bool) bool
	// Source names where the effective switch came from.
	Source func(webEnabled bool) string
	// Credentials lists the gateway's live auth files by auth index.
	Credentials func(ctx context.Context) ([]Credential, error)
	// SetDisabled changes one credential's disabled state through CPA. It returns
	// only once CPA has confirmed the change.
	SetDisabled func(ctx context.Context, name, authIndex string, disabled bool) error
	// Audit is best-effort: a missing callback is a deployment that does not audit
	// background automation, not a failure.
	Audit AuditFunc
	// Now is injectable so tests drive a virtual clock instead of sleeping.
	Now func() time.Time

	locksMu sync.Mutex
	locks   map[string]*sync.Mutex
}

// Status is the resolved master switch for the console and capability reads.
type Status struct {
	WebEnabled     bool   `json:"web_enabled"`
	Enabled        bool   `json:"enabled"`
	Source         string `json:"source"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logger() *slog.Logger {
	if s != nil && s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

func (s *Service) policy() PolicyConfig {
	cfg := DefaultPolicyConfig()
	if s != nil && s.Policy != (PolicyConfig{}) {
		cfg = s.Policy
	}
	if cfg.ExhaustedUsedPercent <= 0 {
		cfg.ExhaustedUsedPercent = 100
	}
	return cfg
}

func (s *Service) masterEnabled(webEnabled bool) bool {
	if s.MasterEnabled == nil {
		return false
	}
	return s.MasterEnabled(webEnabled)
}

func (s *Service) source(webEnabled bool) string {
	if s.Source == nil {
		return "default_off"
	}
	return s.Source(webEnabled)
}

func (s *Service) audit(ctx context.Context, action, result, targetID string, details map[string]any) {
	if s == nil || s.Audit == nil {
		return
	}
	s.Audit(ctx, action, result, targetID, details)
}

// lockAccount returns the release function for one credential's shared lock.
func (s *Service) lockAccount(authIndex string) func() {
	s.locksMu.Lock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	lock, ok := s.locks[authIndex]
	if !ok {
		lock = &sync.Mutex{}
		s.locks[authIndex] = lock
	}
	s.locksMu.Unlock()
	lock.Lock()
	return lock.Unlock
}

// WithAccount runs fn under the credential's lock against a freshly read row.
// The HTTP write paths and the background pass both go through it, which is what
// makes the intent_version guard meaningful.
func (s *Service) WithAccount(ctx context.Context, authIndex string, fn func(ctx context.Context, account repository.AutomationAccount) error) error {
	if s == nil || s.Repo == nil {
		return errors.New("automation service is not available")
	}
	authIndex = strings.TrimSpace(authIndex)
	if authIndex == "" {
		return ErrAccountNotFound
	}
	release := s.lockAccount(authIndex)
	defer release()
	account, found, err := s.Repo.GetAutomationAccount(ctx, authIndex)
	if err != nil {
		return err
	}
	if !found {
		return ErrAccountNotFound
	}
	return fn(ctx, account)
}

// Status reads the stored web switch and resolves the effective master switch.
func (s *Service) Status(ctx context.Context) (Status, error) {
	if s == nil || s.Repo == nil {
		return Status{}, errors.New("automation service is not available")
	}
	settings, err := s.Repo.GetAutomationSettings(ctx)
	if err != nil {
		return Status{}, err
	}
	enabled := s.masterEnabled(settings.WebEnabled)
	status := Status{
		WebEnabled: settings.WebEnabled,
		Enabled:    enabled,
		Source:     s.source(settings.WebEnabled),
	}
	if !enabled {
		status.DisabledReason = status.Source
	}
	return status, nil
}

// SetWebEnabled writes the operator's web preference. It is inert while an
// environment value forces the master switch.
func (s *Service) SetWebEnabled(ctx context.Context, enabled bool) error {
	if s == nil || s.Repo == nil {
		return errors.New("automation service is not available")
	}
	if err := s.Repo.SetAutomationWebEnabled(ctx, enabled, s.now().UnixMilli()); err != nil {
		return err
	}
	s.audit(ctx, "account_automation.settings", "success", "settings", map[string]any{"web_enabled": enabled})
	return nil
}

// ListAccounts returns the adopted roster with each account's plan.
func (s *Service) ListAccounts(ctx context.Context) ([]repository.AutomationAccount, error) {
	if s == nil || s.Repo == nil {
		return nil, errors.New("automation service is not available")
	}
	return s.Repo.ListAutomationAccounts(ctx)
}

// Adopt records the one-time batch adoption of the named credentials and returns
// how many were adopted. A name that is not a live credential is skipped rather
// than invented.
func (s *Service) Adopt(ctx context.Context, authIndexes []string, enable bool, note string) (int, error) {
	if s == nil || s.Repo == nil {
		return 0, errors.New("automation service is not available")
	}
	if s.Credentials == nil {
		return 0, errors.New("automation service is not available")
	}
	credentials, err := s.Credentials(ctx)
	if err != nil {
		return 0, err
	}
	byIndex := make(map[string]Credential, len(credentials))
	for _, credential := range credentials {
		byIndex[credential.AuthIndex] = credential
	}
	selected := make([]repository.AutomationAccount, 0, len(authIndexes))
	adoptedIndexes := make([]string, 0, len(authIndexes))
	seen := map[string]bool{}
	for _, authIndex := range authIndexes {
		authIndex = strings.TrimSpace(authIndex)
		if authIndex == "" || seen[authIndex] {
			continue
		}
		credential, ok := byIndex[authIndex]
		if !ok || !credential.Controllable {
			continue
		}
		seen[authIndex] = true
		account := repository.AutomationAccount{
			AuthIndex:    authIndex,
			Provider:     credential.Provider,
			Name:         credential.Name,
			AccountLabel: credential.AccountLabel,
			State:        StateIdle,
		}
		if enable && !ProviderHasVerifiedWeeklyCapability(credential.Provider) {
			account.State = StateMonitoring
			account.MonitorReason = ReasonUnverifiedCapability
		}
		selected = append(selected, account)
		adoptedIndexes = append(adoptedIndexes, authIndex)
	}
	if len(selected) == 0 {
		return 0, nil
	}
	if enable {
		// An explicit takeover with the switch on plans the direct enable for accounts
		// that are already disabled upstream: the same weekly evidence a disable would
		// need, recorded as a recovery plan but never as a disable.
		s.planAdoptionRecovery(ctx, selected, byIndex)
	}
	selectedJSON, _ := json.Marshal(adoptedIndexes)
	nowMS := s.now().UnixMilli()
	batch := repository.AutomationBatch{
		ID:            uuid.NewString(),
		CreatedAtMS:   nowMS,
		SelectedCount: len(selected),
		SelectedJSON:  string(selectedJSON),
		Note:          strings.TrimSpace(note),
	}
	if err := s.Repo.AdoptAutomationAccounts(ctx, batch, selected, enable, nowMS); err != nil {
		return 0, err
	}
	s.audit(ctx, "account_automation.adopt", "success", batch.ID, map[string]any{
		"selected_count": len(selected),
		"enabled":        enable,
	})
	return len(selected), nil
}

// planAdoptionRecovery fills in the one-time recovery plan for the accounts that are
// being adopted while already disabled upstream. It reads the same standard weekly
// evidence the background pass uses and persists the cycle reset and the evidence
// watermark, so the account is enabled directly at the reset without ever writing a
// disable, and never reports a disable OMC did not perform. Accounts with no qualifying
// reading are left without a plan: they stay missing_reset and the operator may hand-fill
// a recovery time.
func (s *Service) planAdoptionRecovery(ctx context.Context, selected []repository.AutomationAccount, credentials map[string]Credential) {
	indexes := make([]string, 0, len(selected))
	for _, account := range selected {
		indexes = append(indexes, account.AuthIndex)
	}
	evidence, err := s.Repo.LatestStandardWeeklyWindows(ctx, indexes)
	if err != nil {
		// Evidence is required to plan a recovery; a failed read degrades to adoption
		// without a plan rather than inventing a reset.
		s.logger().Warn("account automation: adoption evidence read failed", "error", err)
		return
	}
	now := s.now()
	for i := range selected {
		credential, ok := credentials[selected[i].AuthIndex]
		if !ok || !credential.Disabled {
			continue
		}
		window, ok := evidence[selected[i].AuthIndex]
		if !ok {
			continue
		}
		resetAtMS, cycleKey, observedAtMS, ok := AdoptionRecovery(selected[i], &window, CredentialState{
			Exists:       true,
			Disabled:     credential.Disabled,
			Controllable: credential.Controllable,
			Provider:     credential.Provider,
		}, now, s.policy())
		if !ok {
			continue
		}
		selected[i].CycleResetAtMS = &resetAtMS
		selected[i].CycleKey = cycleKey
		selected[i].DisableObservedAtMS = &observedAtMS
		selected[i].State = StateWaiting
		selected[i].MonitorReason = ReasonRecoveryScheduled
	}
}

// SetAccountSwitch turns one account's auto-management on or off. Turning it off
// clears a pending enable or retry.
func (s *Service) SetAccountSwitch(ctx context.Context, authIndex string, enabled bool) (repository.AutomationAccount, error) {
	var updated repository.AutomationAccount
	err := s.WithAccount(ctx, authIndex, func(ctx context.Context, account repository.AutomationAccount) error {
		next, err := s.Repo.SetAutomationSwitch(ctx, account.AuthIndex, enabled, account.IntentVersion, s.now().UnixMilli())
		if err != nil {
			return err
		}
		updated = next
		return nil
	})
	if err != nil {
		return repository.AutomationAccount{}, err
	}
	s.audit(ctx, "account_automation.switch", "success", updated.AuthIndex, map[string]any{"enabled": enabled})
	return updated, nil
}

// CancelAccount cancels a pending enable or retry by turning the account switch
// off. It never re-enables a credential OMC disabled.
func (s *Service) CancelAccount(ctx context.Context, authIndex string) (repository.AutomationAccount, error) {
	updated, err := s.SetAccountSwitch(ctx, authIndex, false)
	if err != nil {
		return repository.AutomationAccount{}, err
	}
	s.audit(ctx, "account_automation.cancel", "success", updated.AuthIndex, nil)
	return updated, nil
}

// SetManualReset records the operator's one-time absolute recovery time. It is
// the hand-filled supplement for an account whose upstream published no reset: a
// recovery instant, never exhaustion evidence and never a capability claim. It is
// accepted only for a provider whose account-level weekly window OMC has verified,
// so an unknown provider cannot be handed a future time to bypass the admission
// rule. Because a recovery time only takes effect while the credential is disabled
// upstream, it can never disable a live account.
func (s *Service) SetManualReset(ctx context.Context, authIndex string, resetAtMS int64) (repository.AutomationAccount, error) {
	if resetAtMS <= 0 {
		return repository.AutomationAccount{}, errors.New("invalid_parameters")
	}
	var updated repository.AutomationAccount
	err := s.WithAccount(ctx, authIndex, func(ctx context.Context, account repository.AutomationAccount) error {
		if !ProviderHasVerifiedWeeklyCapability(account.Provider) {
			return ErrUnverifiedWeeklyCapability
		}
		next, err := s.Repo.SetAutomationManualReset(ctx, account.AuthIndex, resetAtMS, account.IntentVersion, s.now().UnixMilli())
		if err != nil {
			return err
		}
		updated = next
		return nil
	})
	if err != nil {
		return repository.AutomationAccount{}, err
	}
	s.audit(ctx, "account_automation.manual_reset", "success", updated.AuthIndex, map[string]any{"reset_at_ms": resetAtMS})
	return updated, nil
}

// RunOnce evaluates every adopted account once. It is not concurrent with itself
// and performs at most one side effect per account per pass.
func (s *Service) RunOnce(ctx context.Context) {
	if s == nil || s.Repo == nil || s.Credentials == nil || s.SetDisabled == nil {
		return
	}
	settings, err := s.Repo.GetAutomationSettings(ctx)
	if err != nil {
		s.logger().Warn("account automation: settings read failed", "error", err)
		return
	}
	if !s.masterEnabled(settings.WebEnabled) {
		return
	}
	accounts, err := s.Repo.ListAutomationAccounts(ctx)
	if err != nil {
		s.logger().Warn("account automation: roster read failed", "error", err)
		return
	}
	if len(accounts) == 0 {
		return
	}
	credentials, err := s.Credentials(ctx)
	if err != nil {
		s.logger().Warn("account automation: credential list failed", "error", err)
		return
	}
	byIndex := make(map[string]Credential, len(credentials))
	indexes := make([]string, 0, len(credentials))
	for _, credential := range credentials {
		byIndex[credential.AuthIndex] = credential
		indexes = append(indexes, credential.AuthIndex)
	}
	weekly, err := s.Repo.LatestStandardWeeklyWindows(ctx, indexes)
	if err != nil {
		// Evidence is required to disable, so a failed read degrades to "monitor
		// only" rather than to an action taken on nothing.
		s.logger().Warn("account automation: weekly evidence read failed", "error", err)
	}
	now := s.now()
	for _, account := range accounts {
		if !account.IsAdopted {
			continue
		}
		s.runAccount(ctx, account, byIndex[account.AuthIndex], weekly[account.AuthIndex], now)
	}
}

func (s *Service) runAccount(ctx context.Context, account repository.AutomationAccount, credential Credential, evidence repository.AutomationWeeklyWindow, now time.Time) {
	release := s.lockAccount(account.AuthIndex)
	defer release()
	// Re-read under the lock: an operator may have changed the intent since the
	// roster was listed, and the version guard below must compare like with like.
	fresh, found, err := s.Repo.GetAutomationAccount(ctx, account.AuthIndex)
	if err != nil || !found {
		return
	}
	var evidencePtr *repository.AutomationWeeklyWindow
	if evidence.AuthIndex != "" {
		evidencePtr = &evidence
	}
	decision := Decide(fresh, evidencePtr, CredentialState{
		Exists:       credential.AuthIndex != "",
		Disabled:     credential.Disabled,
		Controllable: credential.Controllable,
		Provider:     credential.Provider,
	}, now, s.policy())
	switch decision.Kind {
	case DecisionMonitor:
		s.applyMonitor(ctx, fresh, decision, now)
	case DecisionDisable:
		if evidencePtr == nil {
			return
		}
		s.applyDisable(ctx, fresh, *evidencePtr, decision, now)
	case DecisionEnable:
		s.applyEnable(ctx, fresh, decision, now)
	}
}

func (s *Service) applyMonitor(ctx context.Context, account repository.AutomationAccount, decision Decision, now time.Time) {
	if account.State == decision.State && account.MonitorReason == decision.Reason {
		return
	}
	if decision.Reason == ReasonExternallyReEnabled {
		// The credential is live again though OMC had disabled it. Clearing the
		// plan also ends the disable episode, so the closed cycle cannot re-close.
		if _, err := s.Repo.RecordAutomationReset(ctx, account.AuthIndex, account.IntentVersion, now.UnixMilli(), StateIdle, decision.Reason); err != nil {
			s.logWrite("reset", account.AuthIndex, err)
		}
		return
	}
	if _, err := s.Repo.RecordAutomationMonitoring(ctx, account.AuthIndex, decision.State, decision.Reason, account.IntentVersion, now.UnixMilli()); err != nil {
		s.logWrite("monitoring", account.AuthIndex, err)
	}
}

func (s *Service) applyDisable(ctx context.Context, account repository.AutomationAccount, evidence repository.AutomationWeeklyWindow, decision Decision, now time.Time) {
	if err := s.SetDisabled(ctx, account.Name, account.AuthIndex, true); err != nil {
		s.recordFailure(ctx, account, now, err)
		return
	}
	if _, err := s.Repo.RecordAutomationDisable(ctx, account.AuthIndex, account.IntentVersion, evidence, decision.CycleKey, now.UnixMilli()); err != nil {
		s.logWrite("disable", account.AuthIndex, err)
		return
	}
	s.audit(ctx, "account_automation.disable", "success", account.AuthIndex, map[string]any{
		"cycle_key":      decision.CycleKey,
		"reset_at_ms":    decision.ResetAtMS,
		"observed_at_ms": decision.EvidenceObservedAtMS,
		"used_percent":   evidence.UsedPercent,
	})
}

func (s *Service) applyEnable(ctx context.Context, account repository.AutomationAccount, decision Decision, now time.Time) {
	if err := s.SetDisabled(ctx, account.Name, account.AuthIndex, false); err != nil {
		s.recordFailure(ctx, account, now, err)
		return
	}
	viaManual := account.ManualResetAtMS != nil && !account.ManualResetConsumed &&
		decision.HasReset && *account.ManualResetAtMS == decision.ResetAtMS
	if _, err := s.Repo.RecordAutomationEnable(ctx, account.AuthIndex, account.IntentVersion, now.UnixMilli(), viaManual); err != nil {
		// The gateway change has landed; a failed bookkeeping write must not invite a
		// retry that would enable again, so it is logged rather than raised.
		s.logWrite("enable", account.AuthIndex, err)
		return
	}
	s.audit(ctx, "account_automation.enable", "success", account.AuthIndex, map[string]any{
		"reset_at_ms": decision.ResetAtMS,
		"via_manual":  viaManual,
	})
}

func (s *Service) recordFailure(ctx context.Context, account repository.AutomationAccount, now time.Time, failure error) {
	message := strings.TrimSpace(security.RedactText(failure.Error()))
	if len(message) > maxStoredErrorRunes {
		message = message[:maxStoredErrorRunes]
	}
	nextAttemptMS := now.Add(NextBackoff(account.AttemptCount, s.policy())).UnixMilli()
	if _, err := s.Repo.RecordAutomationFailure(ctx, account.AuthIndex, account.IntentVersion, now.UnixMilli(), message, nextAttemptMS); err != nil {
		s.logWrite("failure", account.AuthIndex, err)
	}
	s.audit(ctx, "account_automation.failure", "failure", account.AuthIndex, map[string]any{
		"error":           message,
		"next_attempt_ms": nextAttemptMS,
	})
}

func (s *Service) logWrite(operation, authIndex string, err error) {
	if errors.Is(err, repository.ErrAutomationConflict) {
		// A concurrent operator intent won; the next pass re-reads and decides
		// again, so this is not an error worth warning about.
		s.logger().Debug("account automation: write skipped after intent change", "operation", operation, "auth_index", authIndex)
		return
	}
	s.logger().Warn("account automation: write failed", "operation", operation, "auth_index", authIndex, "error", err)
}
