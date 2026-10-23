import type { AccountAutomationAccountDTO, AccountAutomationListResponse } from '../../types/accountAutomation';

export type AutomationFacet = 'all' | 'controlled' | 'paused' | 'disabled' | 'monitor_only' | 'error';
export type AutomationDisplayState = 'monitoring' | 'disabled' | 'paused' | 'credit' | 'monitor_only' | 'error';

export function projectAutomationAccount(account: AccountAutomationAccountDTO) {
  const reason = account.monitor_reason?.toLowerCase();
  const isMonitorOnly = !account.auto_disable_supported ||
    ['credential_not_found', 'credential_missing_name', 'no_controllable_credential'].includes(reason ?? '');
  const hasRecovery = Boolean(account.recovery_at_ms &&
    (account.recovery_status === 'scheduled' || account.recovery_status === 'overdue'));
  const hasBackoff = Boolean(account.next_attempt_at_ms);
  const hasError = !['idle', 'monitoring', 'disabled', 'waiting'].includes(account.state) || hasBackoff || reason === 'evidence_identity_mismatch';
  // Pause and capability gates precede execution state; Credit never hides recovery or failure.
  const state: AutomationDisplayState = !account.is_enabled ? 'paused'
    : isMonitorOnly ? 'monitor_only'
    : hasError ? 'error'
    : account.disabled_by_automation ? 'disabled'
    : account.burn_credit && !hasRecovery ? 'credit' : 'monitoring';
  return { account, state, reason, isMonitorOnly, hasRecovery, hasBackoff, hasError };
}

export type AutomationRow = ReturnType<typeof projectAutomationAccount>;

export function matchesAutomationFacet(row: AutomationRow, facet: AutomationFacet) {
  switch (facet) {
    case 'all': return true;
    case 'controlled': return row.account.is_enabled && !row.isMonitorOnly;
    case 'paused': return !row.account.is_enabled;
    case 'disabled': return row.account.disabled_by_automation || row.hasRecovery;
    case 'monitor_only': return row.isMonitorOnly;
    case 'error': return row.hasError;
  }
}

export function projectAutomationRoster(data?: AccountAutomationListResponse) {
  const rows = (data?.accounts ?? []).filter(account => account.is_adopted && !account.removed).map(projectAutomationAccount);
  return {
    rows,
    stats: {
      adopted: rows.length,
      credentials: !data || data.candidates_degraded ? null : data.candidates.length,
      monitorOnly: rows.filter(row => row.isMonitorOnly).length,
      disabled: rows.filter(row => row.account.disabled_by_automation).length,
      waiting: rows.filter(row => row.hasRecovery).length,
      backoff: rows.filter(row => row.hasBackoff).length,
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
  if (row.state === 'paused') return row.hasRecovery ? 'automation.reason_paused_recovery' : 'automation.reason_switch_off';
  if (row.isMonitorOnly) return automationReasonKey(
    ['credential_not_found', 'credential_missing_name', 'no_controllable_credential'].includes(row.reason ?? '')
      ? row.reason : 'unverified_account_weekly_capability');
  if (row.hasError) return row.hasBackoff ? 'automation.reason_retry_count' : 'automation.reason_action_failed';
  if (row.hasRecovery) return row.account.recovery_status === 'overdue' ? 'automation.recovery_overdue' : 'automation.recovery_at';
  if (row.state === 'credit') return 'automation.reason_credit';
  if (row.state === 'disabled') return 'automation.reason_exhausted';
  return automationReasonKey(row.reason);
}
