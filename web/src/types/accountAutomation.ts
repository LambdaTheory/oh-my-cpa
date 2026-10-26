export type AutomationState = 'idle' | 'monitoring' | 'disabled' | 'waiting' | 'error' | string;

export type AutomationRecoveryStatus = 'scheduled' | 'overdue' | string;

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
  | 'credential_not_found'
  | 'credential_missing_name'
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
  burn_credit: boolean;
  removed: boolean;
  state: AutomationState;
  monitor_reason?: AutomationMonitorReason;
  auto_disable_supported: boolean;
  disabled_by_automation: boolean;
  recovery_at_ms?: number | null;
  recovery_status?: AutomationRecoveryStatus;
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

export interface AccountAutomationInventoryDTO {
  available: boolean;
  error?: string;
  /** Total current credentials, disabled and unmanaged included. Unknown when unavailable. */
  credentials?: number | null;
  /** Current credentials with a uniquely matched active plan. */
  managed?: number | null;
  /** Current credentials with no plan. Adopting one creates a fresh lifecycle. */
  unmanaged?: number | null;
  /** Current credentials CPA itself reports disabled. */
  disabled?: number | null;
  /** Active plans whose credential is absent, awaiting the second complete confirmation.
   *  Omitted when the inventory could not be read: an outage has no truthful figure. */
  pending_absence?: number | null;
}

/** One current gateway credential joined with the plan that matched it, if any. */
export interface AccountAutomationCredentialDTO {
  auth_index: string;
  provider?: string;
  name?: string;
  account_label?: string;
  disabled: boolean;
  controllable: boolean;
  managed: boolean;
  adoptable: boolean;
  adoption_blocked_reason?: AutomationMonitorReason;
  auto_disable_supported: boolean;
  plan?: AccountAutomationAccountDTO | null;
}

export interface AccountAutomationListResponse {
  settings: AccountAutomationSettingsDTO;
  inventory: AccountAutomationInventoryDTO;
  /** The gateway's current credentials. This is the console's main table. */
  credentials: AccountAutomationCredentialDTO[];
  /** The active managed roster with each plan; last-known values when inventory is unavailable. */
  accounts: AccountAutomationAccountDTO[];
  accounts_last_known: boolean;
  candidates: AccountAutomationCandidateDTO[];
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

export type AccountAutomationPatch =
  | { enabled: boolean; burn_credit?: boolean; expected_intent_version?: number }
  | { enabled?: boolean; burn_credit: boolean; expected_intent_version?: number };

export interface AccountAutomationUnadoptPayload {
  auth_indexes: string[];
}

export interface AccountAutomationUnadoptResponse {
  removed: number;
}
