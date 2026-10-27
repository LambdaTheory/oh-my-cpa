import type {
  AccountAutomationAccountDTO,
  AccountAutomationCredentialDTO,
  AccountAutomationListResponse,
} from '../../types/accountAutomation';

/**
 * The console's account-automation projection.
 *
 * The main table is built from the gateway's current credentials, not from stored roster
 * memberships: a plan whose credential is gone is history, not an account. Every row is one
 * credential, so two credentials sharing a display label stay two rows. Counts describe the
 * same current rows, and an unavailable inventory reports unknown counts rather than zero.
 */

export type AutomationFacet = 'all' | 'managed' | 'unmanaged' | 'paused' | 'disabled' | 'monitor_only' | 'error';
export type AutomationDisplayState =
  | 'unmanaged'
  | 'monitoring'
  | 'disabled'
  | 'paused'
  | 'credit'
  | 'monitor_only'
  | 'error';

/** The plan fields the row renders, absent for an unmanaged credential. */
const NO_PLAN: AccountAutomationAccountDTO = {
  auth_index: '',
  is_adopted: false,
  is_enabled: false,
  burn_credit: false,
  removed: false,
  state: 'idle',
  auto_disable_supported: false,
  disabled_by_automation: false,
  attempt_count: 0,
  intent_version: 0,
  has_weekly_evidence: false,
};

export function projectAutomationCredential(credential: AccountAutomationCredentialDTO) {
  const plan = credential.plan ?? null;
  const reason = plan?.monitor_reason?.toLowerCase();
  const isManaged = credential.managed && plan !== null;
  const capabilityVerified = credential.auto_disable_supported && (plan?.auto_disable_supported ?? true);
  const isMonitorOnly = isManaged && (!capabilityVerified ||
    ['credential_not_found', 'credential_missing_name', 'no_controllable_credential'].includes(reason ?? ''));
  const hasRecovery = Boolean(plan?.recovery_at_ms &&
    (plan.recovery_status === 'scheduled' || plan.recovery_status === 'overdue'));
  const hasBackoff = Boolean(plan?.next_attempt_at_ms);
  const hasError = isManaged &&
    (!['idle', 'monitoring', 'disabled', 'waiting'].includes(plan?.state ?? '') || hasBackoff || reason === 'evidence_identity_mismatch');
  // An unmanaged credential is not paused: there is no plan to pause. Pause, capability and
  // error states precede the execution state; Credit never hides recovery or failure.
  const state: AutomationDisplayState = !isManaged ? 'unmanaged'
    : !plan?.is_enabled ? 'paused'
    : isMonitorOnly ? 'monitor_only'
    : hasError ? 'error'
    : plan.disabled_by_automation ? 'disabled'
    : plan.burn_credit && !hasRecovery ? 'credit' : 'monitoring';
  return {
    credential,
    account: plan,
    plan: plan ?? NO_PLAN,
    isManaged,
    state,
    reason,
    isMonitorOnly,
    hasRecovery,
    hasBackoff,
    hasError,
    /** Gateway truth, never the last automation action. */
    isGatewayDisabled: credential.disabled,
  };
}

export type AutomationRow = ReturnType<typeof projectAutomationCredential>;

export function matchesAutomationFacet(row: AutomationRow, facet: AutomationFacet) {
  switch (facet) {
    case 'all': return true;
    case 'managed': return row.isManaged;
    case 'unmanaged': return !row.isManaged;
    case 'paused': return row.isManaged && row.state === 'paused';
    case 'disabled': return row.isGatewayDisabled || row.hasRecovery;
    case 'monitor_only': return row.isMonitorOnly;
    case 'error': return row.hasError;
  }
}

export function projectAutomationRoster(data?: AccountAutomationListResponse) {
  const inventory = data?.inventory;
  const available = Boolean(data && inventory?.available);
  const rows = available ? (data?.credentials ?? []).map(projectAutomationCredential) : [];
  return {
    rows,
    available,
    // The active roster excludes tombstones already; a stored plan is last-known history when
    // the inventory is unavailable, whatever the current adoption flag says.
    lastKnownAccounts: !available ? (data?.accounts ?? []).filter(account => !account.removed) : [],
    stats: {
      // Every count is unknown during an outage: deriving zero from "no rows" would be the
      // exact confusion (outage versus empty gateway) this projection exists to prevent.
      credentials: available ? (inventory?.credentials ?? 0) : null,
      managed: available ? rows.filter(row => row.isManaged).length : null,
      unmanaged: available ? rows.filter(row => !row.isManaged).length : null,
      gatewayDisabled: available ? rows.filter(row => row.isGatewayDisabled).length : null,
      paused: available ? rows.filter(row => row.state === 'paused').length : null,
      recovery: available ? rows.filter(row => row.hasRecovery).length : null,
      pendingAbsence: available ? (inventory?.pending_absence ?? 0) : null,
    },
  };
}

const REASON_KEYS: Record<string, string> = {
  credential_not_found: 'automation.reason_credential_not_found',
  credential_missing_name: 'automation.reason_credential_missing_name',
  no_controllable_credential: 'automation.reason_no_credential',
  no_weekly_evidence: 'automation.no_weekly_summary',
  not_exhausted: 'automation.reason_not_exhausted',
  missing_reset: 'automation.reason_missing_reset',
  expired_reset: 'automation.reason_expired_reset',
  stale_evidence: 'automation.reason_stale_evidence',
  backoff: 'automation.reason_backoff',
  recovery_scheduled: 'automation.reason_recovery_scheduled',
  externally_disabled: 'automation.reason_externally_disabled',
  externally_re_enabled: 'automation.reason_externally_re_enabled',
  unverified_account_weekly_capability: 'automation.reason_capability_short',
  evidence_identity_mismatch: 'automation.reason_evidence_identity_mismatch',
  switch_off: 'automation.reason_switch_off',
  not_adopted: 'automation.reason_not_adopted',
};

export function automationReasonKey(reason?: string) {
  return REASON_KEYS[reason?.toLowerCase() ?? ''] ?? 'automation.reason_unknown';
}

export function automationRowReasonKey(row: AutomationRow) {
  if (row.state === 'unmanaged') return row.credential.adoptable
    ? 'automation.reason_unmanaged' : 'automation.reason_unmanaged_blocked';
  if (row.state === 'paused') return row.hasRecovery ? 'automation.reason_paused_recovery' : 'automation.reason_switch_off';
  if (row.isMonitorOnly) return automationReasonKey(
    ['credential_not_found', 'credential_missing_name', 'no_controllable_credential'].includes(row.reason ?? '')
      ? row.reason : 'unverified_account_weekly_capability');
  if (row.hasError) {
    if (row.reason === 'evidence_identity_mismatch') return automationReasonKey(row.reason);
    return row.hasBackoff ? 'automation.reason_retry_count' : 'automation.reason_action_failed';
  }
  if (row.hasRecovery) return row.account?.recovery_status === 'overdue' ? 'automation.recovery_overdue' : 'automation.recovery_at';
  if (row.state === 'credit') return 'automation.reason_credit';
  if (row.state === 'disabled') return 'automation.reason_exhausted';
  return automationReasonKey(row.reason);
}
