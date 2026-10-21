export type ResetAdviceVerdict = 'use_before_expiry' | 'keep_for_emergency' | 'no_action' | 'unknown';

export interface ResetAdvice {
  verdict: ResetAdviceVerdict;
  evidence: 'fact' | 'projection' | 'unknown';
  reasons: string[];
  remaining_resets: number | null;
  earliest_expiry_ms: number | null;
  used_percent: number | null;
  reset_observed_at_ms: number;
  quota_observed_at_ms: number;
  local_evidence: {
    source: 'local_observation';
    verdict: ResetAdviceVerdict;
    evidence: 'fact' | 'projection' | 'unknown';
    reasons: string[];
    quota_reset_at_ms: number | null;
  };
  announcements: {
    id: string;
    source: 'manual' | 'status_rss' | 'x_api';
    scope: 'all' | 'model' | 'credential';
    model?: string;
    confidence: 'confirmed' | 'probable' | 'unverified';
    expected_reset_at_ms: number | null;
    registered_at_ms: number;
    influences_advice: boolean;
    has_conflict: boolean;
  }[];
}
