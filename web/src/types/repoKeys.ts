/**
 * Repository-owned client keys, as the console reads them.
 *
 * These are OMC's own records: a key belongs to a repository, an agent on a machine applies for
 * it over MCP, and a person approves the application once. No type here carries a key value: the
 * list and the detail show masks, and the plaintext exists only in the gateway until an operator
 * deliberately reveals it.
 */

export type RepoKeyStatus = 'active' | 'stopped';

export interface RepoKeyBinding {
  id: string;
  repo: string;
  repo_has_remote: boolean;
  key_mask: string;
  status: RepoKeyStatus;
  created_at_ms: number;
  created_by: string;
  stopped_at_ms?: number;
  stopped_by?: string;
  stop_category?: string;
  stop_reason?: string;
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
  public_key_fingerprint: string;
  key_mask?: string;
  decided_by?: string;
  decided_at_ms?: number;
  reject_reason?: string;
  ciphertext_ready: boolean;
  first_delivered_at_ms?: number;
  created_at_ms: number;
}

export interface RepoKeyDelivery {
  id: string;
  application_id: string;
  agent: string;
  device: string;
  kind: string;
  operator: string;
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

export interface RepoKeyUsage {
  key_fingerprint: string;
  requests: number;
  failed: number;
  total_tokens: number;
  last_used_ms: number;
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
