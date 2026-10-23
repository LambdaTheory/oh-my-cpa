package automation

import (
	"math"
	"testing"
	"time"

	"github.com/oh-my-cpa/oh-my-cpa/internal/repository"
)

func ptrInt64(value int64) *int64 { return &value }

func TestPausedRecoveryAndCredit(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	account := adoptedAccount()
	account.BurnCredit = true
	evidence := weekly(100, now.UnixMilli(), now.Add(time.Hour).UnixMilli())
	if decision := Decide(account, &evidence, liveCredential(), now, DefaultPolicyConfig()); decision.Kind == DecisionDisable || decision.State != StateMonitoring {
		t.Fatalf("Credit must keep monitoring: %+v", decision)
	}
	account.State = StateWaiting
	account.CycleResetAtMS = ptrInt64(now.UnixMilli())
	account.LastDisableAtMS = ptrInt64(now.Add(-time.Hour).UnixMilli())
	account.IsEnabled = false
	account.MonitorReason = ReasonSwitchOff
	if _, ok := RecoveryDue(account, now); !ok {
		t.Fatal("paused plan must remain visible")
	}
	credential := liveCredential()
	credential.Disabled = true
	if decision := Decide(account, nil, credential, now, DefaultPolicyConfig()); decision.Kind != DecisionNone || decision.Reason != ReasonSwitchOff {
		t.Fatalf("pause must prevent writes: %+v", decision)
	}
	account.IsEnabled = true
	if decision := Decide(account, nil, credential, now, DefaultPolicyConfig()); decision.Kind != DecisionEnable {
		t.Fatalf("Credit must preserve due recovery after resume: %+v", decision)
	}
	account.ManualResetAtMS = ptrInt64(now.Add(time.Hour).UnixMilli())
	if due, ok := RecoveryDue(account, now); !ok || due.AtMS != *account.ManualResetAtMS {
		t.Fatalf("manual precedence: %+v/%v", due, ok)
	}
}

func TestUnavailableCredentialReasons(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	for _, tc := range []struct {
		credential CredentialState
		reason     string
	}{
		{CredentialState{}, ReasonCredentialNotFound},
		{CredentialState{Exists: true}, ReasonCredentialMissingName},
	} {
		decision := Decide(adoptedAccount(), nil, tc.credential, now, DefaultPolicyConfig())
		if decision.Kind != DecisionMonitor || decision.Reason != tc.reason {
			t.Fatalf("unavailable credential: %+v", decision)
		}
	}
}

func adoptedAccount() repository.AutomationAccount {
	return repository.AutomationAccount{
		AuthIndex: "idx-1",
		Provider:  "codex",
		Name:      "codex-1.json",
		IsAdopted: true,
		IsEnabled: true,
		State:     StateIdle,
	}
}

func weekly(used float64, observedAt, resetAt int64) repository.AutomationWeeklyWindow {
	window := repository.AutomationWeeklyWindow{
		AuthIndex:     "idx-1",
		Provider:      "codex",
		WindowKind:    "weekly",
		WindowMinutes: 10080,
		ObservedAtMS:  observedAt,
		UsedPercent:   used,
		Source:        "active",
	}
	if resetAt > 0 {
		window.ResetAtMS = ptrInt64(resetAt)
	}
	return window
}

func liveCredential() CredentialState {
	return CredentialState{Exists: true, Controllable: true, Provider: "codex"}
}

func claudeAccount() repository.AutomationAccount {
	account := adoptedAccount()
	account.Provider = "claude"
	account.Name = "claude-1.json"
	return account
}

func claudeWeekly(used float64, observedAt, resetAt int64) repository.AutomationWeeklyWindow {
	window := weekly(used, observedAt, resetAt)
	window.Provider = "claude"
	window.WindowKind = "seven_day"
	return window
}

func claudeCredential() CredentialState {
	return CredentialState{Exists: true, Controllable: true, Provider: "claude"}
}

func unknownProviderAccount() repository.AutomationAccount {
	account := adoptedAccount()
	account.Provider = "openrouter"
	account.Name = "openrouter-1.json"
	return account
}

func unknownProviderWeekly(used float64, observedAt, resetAt int64) repository.AutomationWeeklyWindow {
	window := weekly(used, observedAt, resetAt)
	window.Provider = "openrouter"
	return window
}

func unknownProviderCredential() CredentialState {
	return CredentialState{Exists: true, Controllable: true, Provider: "openrouter"}
}

// approxWeekly is the old ±1h near-miss: 10140 minutes is close to seven days but is
// not the exact period the capability registry names.
func approxWeekly(used float64, observedAt, resetAt int64) repository.AutomationWeeklyWindow {
	window := weekly(used, observedAt, resetAt)
	window.WindowMinutes = 10140
	return window
}

// TestDecideNeverDisablesWithoutExplicitWeeklyEvidence is the contract that keeps
// the 5h window, a model-scoped window, a partial reading and a missing reset from
// ever closing the credential.
func TestDecideNeverDisablesWithoutExplicitWeeklyEvidence(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	cfg := DefaultPolicyConfig()
	futureReset := now.Add(48 * time.Hour).UnixMilli()
	fresh := now.Add(-time.Minute).UnixMilli()

	cases := []struct {
		name     string
		account  repository.AutomationAccount
		evidence *repository.AutomationWeeklyWindow
		cred     CredentialState
		wantKind DecisionKind
		want     string
	}{
		{
			name:     "not adopted is untouched",
			account:  repository.AutomationAccount{AuthIndex: "idx-1"},
			evidence: windowPtr(weekly(100, fresh, futureReset)),
			cred:     liveCredential(),
			wantKind: DecisionNone,
			want:     ReasonNotAdopted,
		},
		{
			name:     "no controllable credential is monitor only",
			account:  adoptedAccount(),
			evidence: windowPtr(weekly(100, fresh, futureReset)),
			cred:     CredentialState{},
			wantKind: DecisionMonitor,
			want:     ReasonCredentialNotFound,
		},
		{
			name:     "switch off does nothing",
			account:  func() repository.AutomationAccount { a := adoptedAccount(); a.IsEnabled = false; return a }(),
			evidence: windowPtr(weekly(100, fresh, futureReset)),
			cred:     liveCredential(),
			wantKind: DecisionNone,
			want:     ReasonSwitchOff,
		},
		{
			name:     "no weekly reading",
			account:  adoptedAccount(),
			evidence: nil,
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonNoWeeklyEvidence,
		},
		{
			name:     "a partial weekly reading is not exhaustion",
			account:  adoptedAccount(),
			evidence: windowPtr(weekly(99, fresh, futureReset)),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonNotExhausted,
		},
		{
			name:     "a NaN reading is not exhaustion",
			account:  adoptedAccount(),
			evidence: windowPtr(weekly(math.NaN(), fresh, futureReset)),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonNoWeeklyEvidence,
		},
		{
			name:     "a missing reset never disables",
			account:  adoptedAccount(),
			evidence: windowPtr(weekly(100, fresh, 0)),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonMissingReset,
		},
		{
			name:     "an already passed reset never disables",
			account:  adoptedAccount(),
			evidence: windowPtr(weekly(100, fresh, now.Add(-time.Hour).UnixMilli())),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonExpiredReset,
		},
		{
			name:     "an old reading is stale",
			account:  adoptedAccount(),
			evidence: windowPtr(weekly(100, now.Add(-48*time.Hour).UnixMilli(), futureReset)),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonStaleEvidence,
		},
		{
			name: "a reading at or below the watermark is stale",
			account: func() repository.AutomationAccount {
				a := adoptedAccount()
				a.DisableObservedAtMS = ptrInt64(fresh)
				return a
			}(),
			evidence: windowPtr(weekly(100, fresh, futureReset)),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonStaleEvidence,
		},
		{
			name: "the same closed cycle never re-closes",
			account: func() repository.AutomationAccount {
				a := adoptedAccount()
				a.CycleKey = CycleKey(weekly(100, fresh, futureReset))
				return a
			}(),
			evidence: windowPtr(weekly(100, fresh, futureReset)),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonStaleEvidence,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			decision := Decide(testCase.account, testCase.evidence, testCase.cred, now, cfg)
			if decision.Kind != testCase.wantKind {
				t.Fatalf("kind = %q, want %q (%+v)", decision.Kind, testCase.wantKind, decision)
			}
			if testCase.want != "" && decision.Reason != testCase.want {
				t.Fatalf("reason = %q, want %q", decision.Reason, testCase.want)
			}
		})
	}
}

func TestDecideDisableRequiresFreshNewCycleEvidence(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	reset := now.Add(48 * time.Hour).UnixMilli()
	decision := Decide(adoptedAccount(), windowPtr(weekly(100, now.Add(-time.Minute).UnixMilli(), reset)), liveCredential(), now, DefaultPolicyConfig())
	if decision.Kind != DecisionDisable {
		t.Fatalf("kind = %q, want disable (%+v)", decision.Kind, decision)
	}
	if decision.ResetAtMS != reset {
		t.Fatalf("reset = %d, want %d", decision.ResetAtMS, reset)
	}
	if decision.CycleKey == "" {
		t.Fatal("cycle key must be set so the cycle cannot re-close")
	}
}

// TestDecideEnablesAtTheRecordedResetAndBacksOff covers the enable side: a direct
// enable at the cycle reset, a one-time manual reset, the missing-reset monitor,
// and the persisted backoff that suppresses an early retry.
func TestDecideEnablesAtTheRecordedReset(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	base := adoptedAccount()
	base.State = StateDisabled
	disabled := CredentialState{Exists: true, Disabled: true, Controllable: true, Provider: "codex"}

	// Before the reset: waiting, no action.
	account := base
	account.CycleResetAtMS = ptrInt64(now.Add(time.Hour).UnixMilli())
	account.LastDisableAtMS = ptrInt64(now.Add(-time.Hour).UnixMilli())
	decision := Decide(account, nil, disabled, now, DefaultPolicyConfig())
	if decision.Kind != DecisionMonitor || decision.Reason != ReasonRecoveryScheduled {
		t.Fatalf("before reset = %+v, want recovery_scheduled", decision)
	}

	// At the reset: enable, directly, with no pre-probe.
	account.CycleResetAtMS = ptrInt64(now.UnixMilli())
	decision = Decide(account, nil, disabled, now, DefaultPolicyConfig())
	if decision.Kind != DecisionEnable || decision.ResetAtMS != now.UnixMilli() {
		t.Fatalf("at reset = %+v, want enable", decision)
	}

	// Missing reset: monitor, never guess an instant.
	account.CycleResetAtMS = nil
	account.LastDisableAtMS = ptrInt64(now.Add(-time.Hour).UnixMilli())
	decision = Decide(account, nil, disabled, now, DefaultPolicyConfig())
	if decision.Kind != DecisionMonitor || decision.Reason != ReasonMissingReset {
		t.Fatalf("missing reset = %+v, want missing_reset", decision)
	}

	// A one-time manual reset takes over and enables at its instant.
	manual := now.Add(2 * time.Hour).UnixMilli()
	account.ManualResetAtMS = ptrInt64(manual)
	decision = Decide(account, nil, disabled, now, DefaultPolicyConfig())
	if decision.Kind != DecisionMonitor || decision.Reason != ReasonRecoveryScheduled || decision.ResetAtMS != manual {
		t.Fatalf("manual reset waiting = %+v", decision)
	}
	account.ManualResetAtMS = ptrInt64(now.UnixMilli())
	decision = Decide(account, nil, disabled, now, DefaultPolicyConfig())
	if decision.Kind != DecisionEnable || decision.ResetAtMS != now.UnixMilli() {
		t.Fatalf("manual reset due = %+v, want enable at manual instant", decision)
	}

	// A pending backoff suppresses the attempt even when the reset is due.
	account.ManualResetAtMS = nil
	account.CycleResetAtMS = ptrInt64(now.UnixMilli())
	account.NextAttemptAtMS = ptrInt64(now.Add(time.Minute).UnixMilli())
	decision = Decide(account, nil, disabled, now, DefaultPolicyConfig())
	if decision.Kind != DecisionNone || decision.Reason != ReasonBackoff {
		t.Fatalf("backoff = %+v, want none/backoff", decision)
	}
}

// TestRecoveryDueProjectsOnlyActivePlans separates the execution timestamp from
// its read projection. Future plans are scheduled, failed overdue plans stay
// recognizable, and a timestamp left on a completed row is neither displayed nor
// reused if the credential is disabled again outside automation.
func TestRecoveryDueProjectsOnlyActivePlans(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	disabledAt := now.Add(-2 * time.Hour).UnixMilli()
	future := now.Add(time.Hour).UnixMilli()
	past := now.Add(-time.Hour).UnixMilli()

	scheduled := adoptedAccount()
	scheduled.State = StateWaiting
	scheduled.CycleResetAtMS = ptrInt64(future)
	scheduled.LastDisableAtMS = ptrInt64(disabledAt)
	if recovery, ok := RecoveryDue(scheduled, now); !ok || recovery.AtMS != future || recovery.Status != RecoveryScheduled {
		t.Fatalf("future recovery = %+v/%v", recovery, ok)
	}

	overdue := scheduled
	overdue.State = StateError
	overdue.CycleResetAtMS = ptrInt64(past)
	if recovery, ok := RecoveryDue(overdue, now); !ok || recovery.AtMS != past || recovery.Status != RecoveryOverdue {
		t.Fatalf("overdue recovery = %+v/%v", recovery, ok)
	}

	legacyTakeover := adoptedAccount()
	legacyTakeover.State = StateIdle
	legacyTakeover.CycleResetAtMS = ptrInt64(past)
	legacyTakeover.DisableObservedAtMS = ptrInt64(now.Add(-2 * time.Hour).UnixMilli())
	legacyTakeover.CycleKey = "idx-1:codex:legacy"
	if recovery, ok := RecoveryDue(legacyTakeover, now); !ok || recovery.AtMS != past || recovery.Status != RecoveryOverdue {
		t.Fatalf("legacy takeover recovery = %+v/%v", recovery, ok)
	}
	decision := Decide(legacyTakeover, nil, CredentialState{
		Exists: true, Disabled: true, Controllable: true, Provider: "codex",
	}, now, DefaultPolicyConfig())
	if decision.Kind != DecisionEnable {
		t.Fatalf("legacy takeover must still enable after reset: %+v", decision)
	}
	orphanedIdle := legacyTakeover
	orphanedIdle.CycleKey = ""
	if recovery, ok := RecoveryDue(orphanedIdle, now); ok {
		t.Fatalf("an idle timestamp without the takeover watermark must remain history: %+v", recovery)
	}

	recovered := overdue
	recovered.State = StateIdle
	recovered.LastEnableAtMS = ptrInt64(now.Add(-30 * time.Minute).UnixMilli())
	if recovery, ok := RecoveryDue(recovered, now); ok {
		t.Fatalf("completed recovery must not project an old time: %+v", recovery)
	}
	decision = Decide(recovered, nil, CredentialState{Exists: true, Disabled: true, Controllable: true, Provider: "codex"}, now, DefaultPolicyConfig())
	if decision.Kind != DecisionMonitor || decision.Reason != ReasonMissingReset {
		t.Fatalf("an old completed reset must not enable an externally disabled credential: %+v", decision)
	}

	failedDisable := recovered
	failedDisable.State = StateError
	failedDisable.MonitorReason = ReasonNotExhausted
	if recovery, ok := RecoveryDue(failedDisable, now); ok {
		t.Fatalf("an unrelated failed disable must not reactivate old recovery history: %+v", recovery)
	}
}

// TestDecideReconcilesAnExternalReEnable proves OMC never re-closes a credential
// another actor turned back on, and never claims it disabled an account it did not.
func TestDecideReconcilesAnExternalReEnable(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	account := adoptedAccount()
	account.LastDisableAtMS = ptrInt64(now.Add(-time.Hour).UnixMilli())
	// The credential is live again though OMC disabled it.
	decision := Decide(account, windowPtr(weekly(100, now.Add(-time.Minute).UnixMilli(), now.Add(time.Hour).UnixMilli())), liveCredential(), now, DefaultPolicyConfig())
	if decision.Kind != DecisionMonitor || decision.Reason != ReasonExternallyReEnabled {
		t.Fatalf("external re-enable = %+v, want reconcile", decision)
	}

	// An adopted but never-OMC-disabled account is not reported as disabled by OMC.
	external := adoptedAccount()
	external.State = StateMonitoring
	if external.IsDisabledByAutomation() {
		t.Fatal("an account with no last_disable must not be reported as disabled by automation")
	}
}

// TestDecideRefusesUnverifiedProvidersAndIdentityMismatch is the security contract for
// the shared capability/identity admission: an unknown provider that publishes a weekly
// 100 is not disabled, a reading or a live credential that disagrees with the account's
// provider is not acted on, an empty provider is never treated as Claude, and the old
// 168h ± 1h near-miss is not the same window. A verified Claude or Codex reading still
// closes.
func TestDecideRefusesUnverifiedProvidersAndIdentityMismatch(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	reset := now.Add(48 * time.Hour).UnixMilli()
	fresh := now.Add(-time.Minute).UnixMilli()
	cfg := DefaultPolicyConfig()
	emptyProvider := adoptedAccount()
	emptyProvider.Provider = ""
	authMismatch := weekly(100, fresh, reset)
	authMismatch.AuthIndex = "idx-2"

	cases := []struct {
		name     string
		account  repository.AutomationAccount
		evidence repository.AutomationWeeklyWindow
		cred     CredentialState
		wantKind DecisionKind
		want     string
	}{
		{
			name:     "an unknown provider that publishes a weekly 100 is not disabled",
			account:  unknownProviderAccount(),
			evidence: unknownProviderWeekly(100, fresh, reset),
			cred:     unknownProviderCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonUnverifiedCapability,
		},
		{
			name:     "an empty provider is never treated as a known one",
			account:  emptyProvider,
			evidence: weekly(100, fresh, reset),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonUnverifiedCapability,
		},
		{
			name:     "a reading for another auth index is not acted on",
			account:  adoptedAccount(),
			evidence: authMismatch,
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonIdentityMismatch,
		},
		{
			name:     "a reading whose provider disagrees with the account is not acted on",
			account:  adoptedAccount(),
			evidence: claudeWeekly(100, fresh, reset),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonIdentityMismatch,
		},
		{
			name:     "a live credential whose provider changed is not acted on",
			account:  adoptedAccount(),
			evidence: weekly(100, fresh, reset),
			cred:     claudeCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonIdentityMismatch,
		},
		{
			name:     "a 168h near-miss is not the exact seven-day window",
			account:  adoptedAccount(),
			evidence: approxWeekly(100, fresh, reset),
			cred:     liveCredential(),
			wantKind: DecisionMonitor,
			want:     ReasonUnverifiedCapability,
		},
		{
			name:     "a verified Codex reading still closes",
			account:  adoptedAccount(),
			evidence: weekly(100, fresh, reset),
			cred:     liveCredential(),
			wantKind: DecisionDisable,
			want:     "",
		},
		{
			name:     "a verified Claude reading still closes",
			account:  claudeAccount(),
			evidence: claudeWeekly(100, fresh, reset),
			cred:     claudeCredential(),
			wantKind: DecisionDisable,
			want:     "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			decision := Decide(testCase.account, windowPtr(testCase.evidence), testCase.cred, now, cfg)
			if decision.Kind != testCase.wantKind {
				t.Fatalf("kind = %q, want %q (%+v)", decision.Kind, testCase.wantKind, decision)
			}
			if testCase.want != "" && decision.Reason != testCase.want {
				t.Fatalf("reason = %q, want %q", decision.Reason, testCase.want)
			}
		})
	}
}

func TestNextBackoffDoublesAndCaps(t *testing.T) {
	cfg := PolicyConfig{BackoffBase: time.Minute, BackoffMax: 8 * time.Minute}
	want := []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 8 * time.Minute}
	for attempt, expected := range want {
		if got := NextBackoff(attempt, cfg); got != expected {
			t.Fatalf("attempt %d = %s, want %s", attempt, got, expected)
		}
	}
}

func windowPtr(value repository.AutomationWeeklyWindow) *repository.AutomationWeeklyWindow {
	return &value
}

// TestAdoptionRecoveryRequiresFreshExhaustedFutureReset pins the takeover rule: only
// a fresh standard weekly reading at 100% with a reset still in the future, for an
// identity that matches the account and the live credential and for a provider whose
// weekly window is verified, becomes a recovery plan. An adopted disabled account is
// therefore never scheduled to enable on a partial, stale, already-expired, reset-less
// or identity-mismatched reading.
func TestAdoptionRecoveryRequiresFreshExhaustedFutureReset(t *testing.T) {
	now := time.UnixMilli(1_000_000_000_000)
	cfg := DefaultPolicyConfig()
	futureReset := now.Add(48 * time.Hour).UnixMilli()
	fresh := now.Add(-time.Minute).UnixMilli()
	codex := adoptedAccount()
	codexCred := liveCredential()

	cases := []struct {
		name     string
		account  repository.AutomationAccount
		evidence *repository.AutomationWeeklyWindow
		cred     CredentialState
		want     bool
	}{
		{"a fresh Codex weekly reading at 100% with a future reset plans a recovery", codex, windowPtr(weekly(100, fresh, futureReset)), codexCred, true},
		{"a fresh Claude seven-day reading at 100% with a future reset plans a recovery", claudeAccount(), windowPtr(claudeWeekly(100, fresh, futureReset)), claudeCredential(), true},
		{"a partial reading does not", codex, windowPtr(weekly(99, fresh, futureReset)), codexCred, false},
		{"a reading over the scale does not", codex, windowPtr(weekly(101, fresh, futureReset)), codexCred, false},
		{"an approximate 168h reading does not", codex, windowPtr(approxWeekly(100, fresh, futureReset)), codexCred, false},
		{"a missing reset does not", codex, windowPtr(weekly(100, fresh, 0)), codexCred, false},
		{"an already-expired reset does not", codex, windowPtr(weekly(100, fresh, now.Add(-time.Hour).UnixMilli())), codexCred, false},
		{"an old reading does not", codex, windowPtr(weekly(100, now.Add(-48*time.Hour).UnixMilli(), futureReset)), codexCred, false},
		{"a NaN reading does not", codex, windowPtr(weekly(math.NaN(), fresh, futureReset)), codexCred, false},
		{"a provider the registry has not verified does not", unknownProviderAccount(), windowPtr(unknownProviderWeekly(100, fresh, futureReset)), unknownProviderCredential(), false},
		{"a credential whose provider no longer matches does not", codex, windowPtr(weekly(100, fresh, futureReset)), claudeCredential(), false},
		{"no evidence does not", codex, nil, codexCred, false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			resetAt, cycleKey, observedAt, ok := AdoptionRecovery(testCase.account, testCase.evidence, testCase.cred, now, cfg)
			if ok != testCase.want {
				t.Fatalf("ok = %v, want %v", ok, testCase.want)
			}
			if ok && (resetAt != futureReset || cycleKey == "" || observedAt != fresh) {
				t.Fatalf("plan = %d/%q/%d", resetAt, cycleKey, observedAt)
			}
		})
	}
}
