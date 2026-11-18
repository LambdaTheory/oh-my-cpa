export type QuotaPredictionVerdict = 'will_exhaust' | 'under_used' | 'on_track' | 'unknown';

/**
 * The four-category reason a row's "lasts until reset" conclusion is unknown.
 * `no_cycle` is a sub-case of the window category for windows that publish
 * neither a reset instant nor a period, such as a dollar balance.
 */
export type QuotaPredictionUnavailableReason =
  | 'reading_failed'
  | 'history_insufficient'
  | 'window_boundary_unknown'
  | 'usage_abnormal'
  | 'no_cycle'
  | 'rolling_window';

export interface QuotaPredictionItem {
  credential_label: string;
  provider: string;
  window_kind: string;
  window_id: string;
  window_scope: string;
  window_model?: string;
  auth_index?: string;
  used_percent: number | null;
  expected_used_percent: number | null;
  reset_at_ms: number | null;
  observed_at_ms: number;
  data_age_seconds: number | null;
  /** true / false, or null when the evidence cannot decide. */
  lasts_until_reset: boolean | null;
  exhaustion_eta_ms: number | null;
  /**
   * Deprecated with the old verdict vocabulary; retained for one release so
   * older callers keep working. The console no longer renders these.
   */
  burn_rate_per_hour: number | null;
  projected_percent_at_reset: number | null;
  projected_tokens_at_reset: number | null;
  projected_cost_nanos_at_reset: number | null;
  verdict: QuotaPredictionVerdict;
  evidence: string;
  reasons: string[];
  sample_count?: number;
  coverage_seconds?: number | null;
  max_gap_seconds?: number | null;
}

export interface QuotaPredictionProvider {
  provider: string;
  items: DashboardPredictionItem[];
}

/** Console-only context. It is never part of the external prediction contract. */
export interface DashboardPredictionItem extends QuotaPredictionItem {
  unavailable_reason?: QuotaPredictionUnavailableReason | '';
  reading_stale?: boolean;
  /**
   * Console-only: the window's reset moves with the observation, so it has no
   * fixed cycle to fit a trend against. The panel shows the current share and
   * the next reset instead of an ETA or a lasts-until-reset conclusion.
   */
  is_rolling_window?: boolean;
  /**
   * Console-only: requests recorded for the cycle that was looked up. Absent
   * means usage was not read; `0` is a successful read that found no traffic.
   */
  usage_requests?: number;
  window_start_ms?: number | null;
  remaining_percent?: number | null;
  uniform_projected_percent_at_reset?: number | null;
  remaining_window_hours?: number | null;
  window_error_hours?: number | null;
  remaining_tokens?: number | null;
  capacity_error_percent?: number | null;
  capacity_unavailable?: string;
}

export interface DashboardPredictionsResponse {
  generated_at_ms: number;
  duration_ms: number;
  omitted: number;
  providers: QuotaPredictionProvider[];
}
