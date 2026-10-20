export type AutomationState = 'idle' | 'monitoring' | 'disabled' | 'waiting' | 'error' | string;

export type AutomationSource =
  | 'default_off'
  | 'web'
  | 'env_enabled'
  | 'env_disabled'
  | 'demo'
  | 'unavailable'
  | 'error'
  | string;

export type AutomationMonitorReason =
  | 'not_adopted'
  | 'switch_off'
  | 'no_controllable_credential'
  | 'no_weekly_evidence'
  | 'not_exhausted'
  | 'missing_reset'
  | 'expired_reset'
  | 'stale_evidence'
  | 'backoff'
  | 'recovery_scheduled'
  | 'externally_disabled'
  | 'externally_re_enabled'
  | 'unverified_account_weekly_capability'
  | 'evidence_identity_mismatch'
  | string;

export interface AccountAutomationAccountDTO {
  auth_index: string;
  provider?: string;
  name?: string;
  account_label?: string;
  is_adopted: boolean;
  is_enabled: boolean;
  state: AutomationState;
  monitor_reason?: AutomationMonitorReason;
  disabled_by_automation: boolean;
  recovery_at_ms?: number | null;
  adopted_at_ms?: number | null;
  adopt_batch_id?: string;
  next_attempt_at_ms?: number | null;
  attempt_count: number;
  last_error?: string;
  last_disable_at_ms?: number | null;
  last_enable_at_ms?: number | null;
  intent_version: number;
  has_weekly_evidence: boolean;
  weekly_used_percent?: number | null;
  weekly_reset_at_ms?: number | null;
  weekly_observed_at_ms?: number | null;
}

export interface AccountAutomationSettingsDTO {
  web_enabled: boolean;
  enabled: boolean;
  source: AutomationSource;
  disabled_reason?: string;
  master_from_env: boolean;
  accounts: number;
}

export interface AccountAutomationCandidateDTO {
  auth_index: string;
  provider?: string;
  name?: string;
  account_label?: string;
  disabled: boolean;
  controllable: boolean;
  adopted: boolean;
  adoptable: boolean;
  adoption_blocked_reason?: AutomationMonitorReason;
  auto_disable_supported: boolean;
}

export interface AccountAutomationListResponse {
  settings: AccountAutomationSettingsDTO;
  accounts: AccountAutomationAccountDTO[];
  candidates: AccountAutomationCandidateDTO[];
  candidates_degraded?: boolean;
  candidates_error?: string;
}

export interface AccountAutomationAdoptPayload {
  auth_indexes: string[];
  enable?: boolean;
  note?: string;
}

export interface AccountAutomationAdoptResponse {
  adopted: number;
}

export interface AccountAutomationManualResetPayload {
  reset_at_ms: number;
}
