export type QuotaPredictionVerdict = 'will_exhaust' | 'under_used' | 'on_track' | 'unknown';

export interface QuotaPredictionItem {
  credential_label: string;
  provider: string;
  window_kind: string;
  auth_index?: string;
  used_percent: number | null;
  reset_at_ms: number | null;
  observed_at_ms: number;
  data_age_seconds: number | null;
  burn_rate_per_hour: number | null;
  projected_percent_at_reset: number | null;
  projected_tokens_at_reset: number | null;
  projected_cost_nanos_at_reset: number | null;
  exhaustion_eta_ms: number | null;
  verdict: QuotaPredictionVerdict;
  evidence: string;
  reasons: string[];
}

export interface QuotaPredictionProvider {
  provider: string;
  items: QuotaPredictionItem[];
}

export interface DashboardPredictionsResponse {
  generated_at_ms: number;
  duration_ms: number;
  omitted: number;
  providers: QuotaPredictionProvider[];
}
