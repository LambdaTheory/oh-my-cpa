// Package automation owns account-level auto-management: it disables a credential
// only on explicit new account-level weekly (7d) exhaustion evidence, and enables
// it directly at the recorded absolute reset time.
//
// The package holds two things: a pure decision function that a virtual clock can
// drive without sleeping, and a service that performs the CPA side effects and
// persists the outcome. The decision function never performs I/O, so every state
// transition can be asserted directly.
package automation

import (
	"fmt"
	"math"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

// State vocabulary persisted in account_automation_accounts.state. It mirrors the
// repository constants; the decision function names the state it computed, and
// the service writes it.
const (
	StateIdle       = repository.AutomationStateIdle
	StateMonitoring = repository.AutomationStateMonitoring
	StateDisabled   = repository.AutomationStateDisabled
	StateWaiting    = repository.AutomationStateWaiting
	StateError      = repository.AutomationStateError
)

// Monitor reasons. They are stored on the account so the console can say why an
// account is not under automatic control instead of showing a bare "off".
const (
	ReasonNotAdopted          = "not_adopted"
	ReasonSwitchOff           = "switch_off"
	ReasonNoCredential        = "no_controllable_credential"
	ReasonNoWeeklyEvidence    = "no_weekly_evidence"
	ReasonNotExhausted        = "not_exhausted"
	ReasonMissingReset        = "missing_reset"
	ReasonExpiredReset        = "expired_reset"
	ReasonStaleEvidence       = "stale_evidence"
	ReasonBackoff             = "backoff"
	ReasonRecoveryScheduled   = "recovery_scheduled"
	ReasonExternallyDisabled  = "externally_disabled"
	ReasonExternallyReEnabled = "externally_re_enabled"
	// ReasonUnverifiedCapability names a provider whose account-level weekly window
	// OMC has not verified. Such a provider is not acted on - neither disabled from a
	// weekly reading nor enabled from a saved plan or a hand-filled recovery time -
	// however much its reading looks like a weekly 100.
	ReasonUnverifiedCapability = "unverified_account_weekly_capability"
	// ReasonIdentityMismatch names a disagreement between the roster account, the
	// weekly reading and the live gateway credential (auth index or provider). A plan
	// for one identity is never closed against, or enabled from, another.
	ReasonIdentityMismatch = "evidence_identity_mismatch"
)

// DecisionKind is the action a pass decided for one account.
type DecisionKind string

const (
	// DecisionNone is a no-op: nothing changed and nothing needs writing.
	DecisionNone DecisionKind = "none"
	// DecisionMonitor records a descriptive state and reason.
	DecisionMonitor DecisionKind = "monitor"
	// DecisionDisable disables the credential through CPA.
	DecisionDisable DecisionKind = "disable"
	// DecisionEnable enables the credential through CPA.
	DecisionEnable DecisionKind = "enable"
)

// CredentialState is what the process knows about one credential's live gateway
// state.
type CredentialState struct {
	// Exists is true when a credential with this auth index is present in CPA.
	Exists bool
	// Disabled is CPA's current disabled flag for it.
	Disabled bool
	// Controllable is true when OMC can write its disabled state (an auth file
	// with a name).
	Controllable bool
	// Provider is the live credential's provider. It must agree with the roster's
	// provider for any plan to act; an empty value names no recognized provider and
	// is never treated as one (in particular, never as Claude).
	Provider string
}

// PolicyConfig are the thresholds the decision function reads. They are resolved
// from the process configuration, so a deployment can tune the evidence age.
type PolicyConfig struct {
	// ExhaustedUsedPercent is the used share that counts as exhausted. It is 100:
	// the requirement is an explicit "used 100% / remaining 0" reading.
	ExhaustedUsedPercent float64
	// MaxEvidenceAge bounds how old the justifying reading may be. A reading with
	// a reset still in the future describes a cycle that has not ended, but an
	// unbounded age would let a reading survive a long scheduler outage.
	MaxEvidenceAge time.Duration
	// BackoffBase and BackoffMax shape the retry schedule after a failed enable.
	BackoffBase time.Duration
	BackoffMax  time.Duration
}

// DefaultPolicyConfig is the documented policy: exhausted at 100%, evidence kept
// for a day, retries doubling from five minutes to six hours.
func DefaultPolicyConfig() PolicyConfig {
	return PolicyConfig{
		ExhaustedUsedPercent: 100,
		MaxEvidenceAge:       24 * time.Hour,
		BackoffBase:          5 * time.Minute,
		BackoffMax:           6 * time.Hour,
	}
}

// Decision is one account's evaluated plan.
type Decision struct {
	Kind                 DecisionKind
	State                string
	Reason               string
	ResetAtMS            int64
	HasReset             bool
	EvidenceObservedAtMS int64
	CycleKey             string
}

// CycleKey identifies a weekly cycle by its reset instant. The key is what stops
// a stale reading in an already-closed cycle from re-disabling the credential.
func CycleKey(window repository.AutomationWeeklyWindow) string {
	if window.ResetAtMS == nil || *window.ResetAtMS <= 0 {
		return ""
	}
	return fmt.Sprintf("%s:%s:%d", window.AuthIndex, window.Provider, *window.ResetAtMS)
}

// Decide evaluates one managed account. It is pure: the same inputs always yield
// the same decision, which is what makes the state machine testable with a
// virtual clock.
func Decide(account repository.AutomationAccount, evidence *repository.AutomationWeeklyWindow, credential CredentialState, now time.Time, cfg PolicyConfig) Decision {
	if !account.IsAdopted {
		return Decision{Kind: DecisionNone, State: StateIdle, Reason: ReasonNotAdopted}
	}
	if !credential.Exists || !credential.Controllable {
		return Decision{Kind: DecisionMonitor, State: StateMonitoring, Reason: ReasonNoCredential}
	}
	if !account.IsEnabled {
		// The account switch is off. Any pending enable or retry is cancelled; a
		// credential OMC already disabled is left as it is, because switching off
		// is not an instruction to re-enable.
		return Decision{Kind: DecisionNone, State: StateIdle, Reason: ReasonSwitchOff}
	}
	if credential.Disabled {
		if reason, ok := admitRecoveryIdentity(account, credential.Provider); !ok {
			return Decision{Kind: DecisionMonitor, State: StateMonitoring, Reason: reason}
		}
		return decideRecovery(account, now)
	}
	// The credential is live. If OMC had disabled it, the operator (or CPA) turned
	// it back on outside this feature: drop the plan without inferring intent.
	if account.IsDisabledByAutomation() {
		return Decision{Kind: DecisionMonitor, State: StateIdle, Reason: ReasonExternallyReEnabled}
	}
	return decideDisable(account, evidence, credential, now, cfg)
}

// decideRecovery plans the direct enable at the recorded absolute reset time. No
// pre-enable probe is performed: the enable is the action, and only CPA's success
// completes it.
func decideRecovery(account repository.AutomationAccount, now time.Time) Decision {
	dueMS, hasDue := RecoveryDue(account)
	if !hasDue {
		// Nothing says when this credential will be worth using again. Enabling
		// now would be a guess, so it is monitored and the missing reset shown.
		return Decision{Kind: DecisionMonitor, State: StateMonitoring, Reason: ReasonMissingReset}
	}
	if pending := account.NextAttemptAtMS; pending != nil && now.UnixMilli() < *pending {
		return Decision{Kind: DecisionNone, State: StateWaiting, Reason: ReasonBackoff, ResetAtMS: dueMS, HasReset: true}
	}
	if now.UnixMilli() < dueMS {
		if account.LastDisableAtMS == nil {
			return Decision{Kind: DecisionMonitor, State: StateMonitoring, Reason: ReasonExternallyDisabled, ResetAtMS: dueMS, HasReset: true}
		}
		return Decision{Kind: DecisionMonitor, State: StateWaiting, Reason: ReasonRecoveryScheduled, ResetAtMS: dueMS, HasReset: true}
	}
	return Decision{Kind: DecisionEnable, State: StateWaiting, ResetAtMS: dueMS, HasReset: true}
}

// decideDisable is the only path that may disable a credential. It first admits the
// reading through the shared identity/capability rule, then requires a standard weekly
// window at exactly 100% with a reset still in the future, newer than the evidence
// watermark, and in a cycle that has not already been closed.
func decideDisable(account repository.AutomationAccount, evidence *repository.AutomationWeeklyWindow, credential CredentialState, now time.Time, cfg PolicyConfig) Decision {
	monitor := func(reason string) Decision {
		return Decision{Kind: DecisionMonitor, State: StateMonitoring, Reason: reason}
	}
	if pending := account.NextAttemptAtMS; pending != nil && now.UnixMilli() < *pending {
		return Decision{Kind: DecisionNone, State: StateError, Reason: ReasonBackoff}
	}
	if evidence == nil || evidence.ObservedAtMS <= 0 {
		return monitor(ReasonNoWeeklyEvidence)
	}
	if math.IsNaN(evidence.UsedPercent) || math.IsInf(evidence.UsedPercent, 0) {
		return monitor(ReasonNoWeeklyEvidence)
	}
	if cfg.MaxEvidenceAge <= 0 {
		cfg = DefaultPolicyConfig()
	}
	if now.UnixMilli()-evidence.ObservedAtMS > cfg.MaxEvidenceAge.Milliseconds() || evidence.ObservedAtMS > now.UnixMilli() {
		return monitor(ReasonStaleEvidence)
	}
	// The identity chain and the verified capability are checked before the reading
	// itself: an unknown provider with a weekly 100, or a reading whose provider no
	// longer matches the account or the live credential, is monitored and never acted
	// on. The threshold is a real 100: a value above the scale is malformed, so it is
	// rejected outright rather than clamped into a disable.
	if reason, ok := admitWeeklyEvidence(account, *evidence, credential.Provider); !ok {
		return monitor(reason)
	}
	if evidence.UsedPercent < cfg.ExhaustedUsedPercent || evidence.UsedPercent > 100 {
		return monitor(ReasonNotExhausted)
	}
	if evidence.ResetAtMS == nil || *evidence.ResetAtMS <= 0 {
		// No reset is not a reason to disable: the credential would never come
		// back. The operator may hand-fill a one-time recovery time.
		return monitor(ReasonMissingReset)
	}
	if *evidence.ResetAtMS <= now.UnixMilli() {
		// An already-passed reset does not create a new date; the reading describes
		// a cycle that has ended.
		return monitor(ReasonExpiredReset)
	}
	if account.DisableObservedAtMS != nil && evidence.ObservedAtMS <= *account.DisableObservedAtMS {
		return monitor(ReasonStaleEvidence)
	}
	key := CycleKey(*evidence)
	if key == "" {
		return monitor(ReasonMissingReset)
	}
	if account.CycleKey != "" && key == account.CycleKey {
		// The same cycle, already closed once: without new evidence it must not
		// re-close the credential.
		return monitor(ReasonStaleEvidence)
	}
	return Decision{
		Kind:                 DecisionDisable,
		State:                StateDisabled,
		ResetAtMS:            *evidence.ResetAtMS,
		HasReset:             true,
		EvidenceObservedAtMS: evidence.ObservedAtMS,
		CycleKey:             key,
	}
}

// AdoptionRecovery derives the one-time recovery plan to persist when an operator
// explicitly adopts a credential that is already disabled upstream - the documented
// first takeover. It applies the same admission and evidence rule as decideDisable (the
// account, the reading and the live credential must share one identity, the provider
// must have a verified account-level weekly capability, and the reading must be a fresh
// standard weekly 100% with a reset still in the future) but performs no CPA write and
// records no disable: the credential was disabled outside OMC, so this only schedules
// the direct enable at the reset.
//
// A partial, stale, already-expired, reset-less or identity-mismatched reading yields no
// plan; the account is then monitored as missing_reset and the operator may hand-fill a
// one-time recovery time. A seven-day reading is accepted whatever spelling the verified
// provider published, because the caller's query accepts both the normalized "weekly" and
// Claude's "seven_day" kind.
func AdoptionRecovery(account repository.AutomationAccount, evidence *repository.AutomationWeeklyWindow, credential CredentialState, now time.Time, cfg PolicyConfig) (resetAtMS int64, cycleKey string, observedAtMS int64, ok bool) {
	if evidence == nil || evidence.ObservedAtMS <= 0 {
		return 0, "", 0, false
	}
	if math.IsNaN(evidence.UsedPercent) || math.IsInf(evidence.UsedPercent, 0) {
		return 0, "", 0, false
	}
	if cfg.MaxEvidenceAge <= 0 {
		cfg = DefaultPolicyConfig()
	}
	if now.UnixMilli()-evidence.ObservedAtMS > cfg.MaxEvidenceAge.Milliseconds() || evidence.ObservedAtMS > now.UnixMilli() {
		return 0, "", 0, false
	}
	if _, admitted := admitWeeklyEvidence(account, *evidence, credential.Provider); !admitted {
		return 0, "", 0, false
	}
	if evidence.UsedPercent < cfg.ExhaustedUsedPercent || evidence.UsedPercent > 100 {
		return 0, "", 0, false
	}
	if evidence.ResetAtMS == nil || *evidence.ResetAtMS <= now.UnixMilli() {
		return 0, "", 0, false
	}
	key := CycleKey(*evidence)
	if key == "" {
		return 0, "", 0, false
	}
	return *evidence.ResetAtMS, key, evidence.ObservedAtMS, true
}

// RecoveryDue returns the absolute instant the account should be enabled, and
// whether one is known. A live operator override (manual_reset_at_ms, not yet
// consumed) takes precedence over the cycle reset recorded at disable time.
func RecoveryDue(account repository.AutomationAccount) (int64, bool) {
	if account.ManualResetAtMS != nil && !account.ManualResetConsumed && *account.ManualResetAtMS > 0 {
		return *account.ManualResetAtMS, true
	}
	if account.CycleResetAtMS != nil && *account.CycleResetAtMS > 0 {
		return *account.CycleResetAtMS, true
	}
	return 0, false
}

// NextBackoff is the delay after the attempt-th consecutive failure. It doubles
// from BackoffBase and is capped at BackoffMax.
func NextBackoff(attempt int, cfg PolicyConfig) time.Duration {
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = DefaultPolicyConfig().BackoffBase
	}
	maximum := cfg.BackoffMax
	if maximum < cfg.BackoffBase {
		maximum = cfg.BackoffBase
	}
	delay := cfg.BackoffBase
	for i := 0; i < attempt && delay < maximum; i++ {
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	return delay
}
