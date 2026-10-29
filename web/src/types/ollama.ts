import type { NormalizedQuota } from './quota';

export interface OllamaAccountDTO {
  id: string;
  label?: string;
  enabled: boolean;
  has_cookie: boolean;
  has_api_key: boolean;
  status?: string;
  last_success_at_ms?: number | null;
  last_observed_at_ms?: number | null;
  newest_error?: string;
  newest_error_at_ms?: number | null;
  next_attempt_at_ms?: number | null;
  attempt_count: number;
  created_at_ms: number;
  updated_at_ms: number;
  refreshing?: boolean;
  retry_at_ms?: number | null;
}

export interface OllamaAccountWritePayload {
  label?: string;
  cookie?: string;
  api_key?: string;
  enabled?: boolean;
}

export interface OllamaAccountListResponse {
  accounts: OllamaAccountDTO[];
}

export interface OllamaQuotaDTO {
  account_id: string;
  label?: string;
  enabled: boolean;
  status?: string;
  observed_at_ms?: number | null;
  quota?: NormalizedQuota | null;
}

export interface OllamaQuotaResponse {
  accounts: OllamaQuotaDTO[];
}

export interface OllamaRefreshResponse {
  account: OllamaAccountDTO;
  quota?: NormalizedQuota | null;
  refreshed: boolean;
  retry_at_ms?: number | null;
}
