export interface FeishuSettingsDTO {
  enabled: boolean;
  app_id?: string;
  has_secret: boolean;
  chat_id?: string;
  summary_times: string[];
  timezone?: string;
  quiet_start: string;
  quiet_end: string;
  tight_remaining_percent: number;
  channel_unavailable_after_ms: number;
  monitor_down_after_ms: number;
  source: string;
  degraded: boolean;
  reason?: string;
  forced_off: boolean;
  updated_at_ms?: number | null;
}

export interface FeishuSettingsWriteDTO {
  enabled?: boolean;
  app_id?: string;
  app_secret?: string;
  chat_id?: string;
  summary_times?: string[];
  timezone?: string;
  quiet_start?: string;
  quiet_end?: string;
  tight_remaining_percent?: number;
  channel_unavailable_after_ms?: number;
  monitor_down_after_ms?: number;
}

export interface FeishuStatusDTO {
  enabled: boolean;
  ready: boolean;
  degraded: boolean;
  reason?: string;
  source: string;
  forced_off: boolean;
  connected: boolean;
  open_items: number;
  pending_sends: number;
  queued_sends: number;
  last_summary_at?: string;
  last_error?: string;
}

export interface FeishuTestResponse {
  sent: boolean;
  reason?: 'no_findings';
  error?: string;
}
