import { describe, expect, it } from 'vitest';
import type { DashboardPredictionItem } from '../src/types/quotaPrediction';
import {
  isIdleWindow,
  partitionPredictions,
  predictionConclusion,
  predictionRowKind,
  summarizePredictions,
  summarizeUnpushed,
} from '../src/components/dashboard/quotaPredictionLogic';

const item = (overrides: Partial<DashboardPredictionItem> = {}): DashboardPredictionItem => ({
  credential_label: 'operator@example.com',
  provider: 'codex',
  window_kind: 'weekly',
  window_id: 'weekly',
  window_scope: 'standard',
  auth_index: 'auth-1',
  used_percent: 65,
  expected_used_percent: 50,
  reset_at_ms: Date.parse('2026-10-21T08:00:00Z'),
  observed_at_ms: Date.parse('2026-10-20T08:00:00Z'),
  data_age_seconds: 300,
  lasts_until_reset: false,
  exhaustion_eta_ms: Date.parse('2026-10-21T06:00:00Z'),
  burn_rate_per_hour: 4.2,
  projected_percent_at_reset: 110,
  projected_tokens_at_reset: 125000,
  projected_cost_nanos_at_reset: 125000000,
  verdict: 'will_exhaust',
  evidence: 'projection',
  reasons: ['no_price'],
  ...overrides,
});

describe('quota prediction row classes', () => {
  it('treats a zero reading as idle only when the backend read zero cycle traffic', () => {
    expect(isIdleWindow(item({ used_percent: 0, usage_requests: 0 }))).toBe(true);
    // Missing usage is not a read of zero; the percentage alone cannot decide.
    expect(isIdleWindow(item({ used_percent: 0, usage_requests: undefined }))).toBe(false);
    expect(isIdleWindow(item({ used_percent: 0, usage_requests: 3 }))).toBe(false);
    expect(isIdleWindow(item({ used_percent: 5, usage_requests: 0 }))).toBe(false);
    // A stale zero reading is folded as stale, not as idle.
    expect(isIdleWindow(item({ used_percent: 0, usage_requests: 0, reading_stale: true }))).toBe(false);
  });

  it('orders stale before idle before rolling before a risk row', () => {
    expect(predictionRowKind(item({ reading_stale: true, is_rolling_window: true }))).toBe('stale');
    expect(predictionRowKind(item({ used_percent: 0, usage_requests: 0, is_rolling_window: true }))).toBe('idle');
    expect(predictionRowKind(item({ used_percent: 40, is_rolling_window: true }))).toBe('rolling');
    expect(predictionRowKind(item())).toBe('risk');
  });

  it('partitions every row and leaves the source order intact', () => {
    const rolling = item({ auth_index: 'rolling', lasts_until_reset: null, is_rolling_window: true });
    const idle = item({ auth_index: 'idle', used_percent: 0, usage_requests: 0, lasts_until_reset: true });
    const stale = item({ auth_index: 'stale', reading_stale: true, lasts_until_reset: null });
    const risk = item({ auth_index: 'risk' });
    const partitioned = partitionPredictions([rolling, idle, stale, risk]);
    expect(partitioned.rolling.map((row) => row.auth_index)).toEqual(['rolling']);
    expect(partitioned.idle.map((row) => row.auth_index)).toEqual(['idle']);
    expect(partitioned.stale.map((row) => row.auth_index)).toEqual(['stale']);
    expect(partitioned.risk.map((row) => row.auth_index)).toEqual(['risk']);
  });

  it('keeps collapsed and current-reading rows out of the cannot/unknown summary', () => {
    const cannot = item({ auth_index: 'cannot', lasts_until_reset: false });
    const unknown = item({ auth_index: 'unknown', lasts_until_reset: null });
    const rolling = item({ auth_index: 'rolling', lasts_until_reset: null, is_rolling_window: true });
    const idle = item({ auth_index: 'idle', used_percent: 0, usage_requests: 0, lasts_until_reset: true });
    const stale = item({ auth_index: 'stale', reading_stale: true, lasts_until_reset: null });
    expect(summarizePredictions([cannot, unknown, rolling, idle, stale])).toEqual({ cannot: 1, unknown: 1 });
  });

  it('keeps the push footer disjoint and ignores non-risk rows', () => {
    const lasts = item({ auth_index: 'lasts', lasts_until_reset: true });
    const unknown = item({ auth_index: 'unknown', lasts_until_reset: null });
    const rolling = item({ auth_index: 'rolling', lasts_until_reset: null, is_rolling_window: true });
    const idle = item({ auth_index: 'idle', used_percent: 0, usage_requests: 0, lasts_until_reset: true });
    const stale = item({ auth_index: 'stale', reading_stale: true, lasts_until_reset: null });
    expect(summarizeUnpushed([lasts, unknown, rolling, idle, stale])).toEqual({ lasts: 1, unknown: 1, stale: 1 });
  });

  it('states a rolling window as a reading, not an undecidable risk', () => {
    expect(predictionConclusion(item({ lasts_until_reset: null, is_rolling_window: true }), 0))
      .toEqual({ key: 'dash.prediction.line_rolling' });
    // The current reading still wins when it proves exhaustion.
    expect(predictionConclusion(item({ used_percent: 100, lasts_until_reset: false, is_rolling_window: true }), 0))
      .toEqual({ key: 'dash.prediction.line_exhausted' });
    // A stale row stays expired even if the window is rolling.
    expect(predictionConclusion(item({ reading_stale: true, lasts_until_reset: null, is_rolling_window: true }), 0))
      .toEqual({ key: 'dash.prediction.line_expired' });
  });
});
