/**
 * Repository-owned client keys, as the console reads them.
 *
 * These are OMC's own records: a key belongs to a repository, an agent on a machine applies for
 * it over MCP, and a person approves the application once. No type here carries a key value: the
 * list and the detail show masks, and the plaintext exists only in the gateway until an operator
 * deliberately reveals it.
 */

export type RepoKeyStatus = 'active' | 'stopped';

/** The windows the detail's usage block can show (AC-19). */
export type RepoKeyUsageWindow = '24h' | '7d' | '30d';

export interface RepoKeyModelUsage {
  model: string;
  requests: number;
  failed: number;
  tokens: number;
  cost_nanos: number;
}

/** What a stopped key was asked after it left service: refused requests, and no cost (AC-13). */
export interface RepoKeyStoppedUsage {
  since_ms: number;
  rejected: number;
  cost_nanos: number;
}

export interface RepoKeyUsage {
  window: string;
  from_ms: number;
  to_ms: number;
  requests: number;
  failed: number;
  total_tokens: number;
  cost_nanos: number;
  last_used_ms: number;
  models: RepoKeyModelUsage[];
  stopped_after?: RepoKeyStoppedUsage;
}

export interface RepoKeyBinding {
  id: string;
  repo: string;
  repo_has_remote: boolean;
  key_mask: string;
  /** The gateway's usage identity, used to mark the gateway list's bound rows (AC-14). */
  key_fingerprint?: string;
  /** The operator's name for the key, shared with the gateway's own alias list (AC-15). */
  alias?: string;
  status: RepoKeyStatus;
  created_at_ms: number;
  created_by: string;
  stopped_at_ms?: number;
  stopped_by?: string;
  stop_category?: string;
  stop_reason?: string;
  /** The newest hand-out, shown as 最近领取人 (AC-15). */
  last_recipient?: string;
  last_delivered_at_ms?: number;
  usage_7d?: RepoKeyUsage;
}

export type RepoKeyApplicationStatus = 'pending' | 'approved' | 'rejected';
export type RepoKeyKind = 'issue' | 'reuse' | 'reissue';

export interface RepoKeyApplication {
  id: string;
  repo: string;
  repo_has_remote: boolean;
  agent: string;
  device: string;
  purpose: string;
  kind: RepoKeyKind;
  status: RepoKeyApplicationStatus;
  source: 'agent' | 'console';
  /** The applicant's identity: a changed fingerprint sends the request back to a person (AC-34). */
  public_key_fingerprint: string;
  key_fingerprint?: string;
  key_mask?: string;
  decided_by?: string;
  decided_at_ms?: number;
  reject_reason?: string;
  /** What the approver wrote when approving (AC-21). */
  decision_reason?: string;
  ciphertext_ready: boolean;
  first_delivered_at_ms?: number;
  created_at_ms: number;
  /** 'reenable' names a stopped key instead of carrying a public key (AC-31). */
  request_kind?: string;
  target_binding_id?: string;
  /** The one-time approval link, known only in the answer to the caller that filed it (AC-8). */
  approval_link?: string;
}

export interface RepoKeyDelivery {
  id: string;
  application_id: string;
  agent: string;
  device: string;
  kind: string;
  operator: string;
  key_mask: string;
  /** The public key the envelope was sealed to (AC-20). */
  public_key_fingerprint: string;
  delivered_at_ms: number;
}

export interface RepoKeyStop {
  id: string;
  category: string;
  reason: string;
  stopped_by: string;
  stopped_at_ms: number;
  reenabled_by?: string;
  reenabled_at_ms?: number;
  reenable_reason?: string;
}

export interface RepoKeyListResponse {
  keys: RepoKeyBinding[];
  pending_applications: number;
  awaiting_delivery: number;
}

export interface RepoKeyBindingDetail {
  key: RepoKeyBinding;
  stops: RepoKeyStop[];
  deliveries: RepoKeyDelivery[];
  applications: RepoKeyApplication[];
  usage: RepoKeyUsage;
}

/** A repository's state now, as an approver needs it (AC-4). */
export interface RepoKeyRepoView {
  repo: string;
  repo_has_remote: boolean;
  state: 'active' | 'stopped' | 'none';
  key_mask?: string;
  created_by?: string;
  created_at_ms?: number;
  last_recipient?: string;
  last_delivered_at_ms?: number;
  usage: RepoKeyUsage;
  stops: RepoKeyStop[];
}

/**
 * Everything the approval page shows: the request, the repository as it stands now, and what
 * approving would do. The queue and the one-time link show the same thing (AC-8).
 */
export interface RepoKeyReview {
  application: RepoKeyApplication;
  repo: RepoKeyRepoView;
  previous_public_key_fingerprint?: string;
  public_key_changed: boolean;
  /** What approving would do: reenable, reuse, reissue or issue. */
  impact: 'reenable' | 'reuse' | 'reissue' | 'issue';
  approval_expired: boolean;
}

/** The audit platform link's health (AC-32). */
export interface RepoKeyAuditPushStatus {
  enabled: boolean;
  pending: number;
  quarantined: number;
  earliest_ms: number;
  last_success_ms: number;
}
