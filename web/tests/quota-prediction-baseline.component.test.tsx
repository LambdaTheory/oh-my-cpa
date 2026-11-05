import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { QuotaPredictionPanel } from '../src/components/dashboard/QuotaPrediction';
import type { DashboardPredictionsResponse, DashboardPredictionItem } from '../src/types/quotaPrediction';
import { assessPrediction, predictionDeviation, summarizeUnpushed } from '../src/components/dashboard/quotaPredictionLogic';

vi.mock('../src/api/client', async (importOriginal) => ({
  ...await importOriginal<typeof import('../src/api/client')>(),
  api: {
    getDashboardPredictions: vi.fn(),
    postFeishuPredictionTest: vi.fn(),
  },
}));

const generatedAtMS = Date.parse('2026-10-20T08:00:00Z');

const item = (overrides: Partial<DashboardPredictionItem> = {}): DashboardPredictionItem => ({
  credential_label: 'operator@example.com',
  provider: 'codex',
  window_kind: 'weekly',
  window_id: 'weekly',
  window_scope: 'standard',
  auth_index: 'auth-1',
  used_percent: 65,
  expected_used_percent: 40,
  reset_at_ms: Date.parse('2026-10-25T08:00:00Z'),
  observed_at_ms: generatedAtMS,
  data_age_seconds: 300,
  burn_rate_per_hour: 4.2,
  projected_percent_at_reset: 90,
  projected_tokens_at_reset: null,
  projected_cost_nanos_at_reset: null,
  exhaustion_eta_ms: null,
  verdict: 'on_track',
  evidence: 'projection',
  reasons: [],
  ...overrides,
});

const response = (providers: DashboardPredictionsResponse['providers']): DashboardPredictionsResponse => ({
  generated_at_ms: generatedAtMS,
  duration_ms: 7,
  omitted: 0,
  providers,
});

function surface(ui: React.ReactElement) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return (
    <QueryClientProvider client={client}>
      <I18nProvider>
        <AntdApp>{ui}</AntdApp>
      </I18nProvider>
    </QueryClientProvider>
  );
}

describe('quota prediction time-linear baseline', () => {
  it('uses the stated lasts-until-reset answer as the only risk threshold', () => {
    // Plenty of usage left, but the forecast says it will not last.
    expect(assessPrediction(item({ used_percent: 20, verdict: 'will_exhaust', lasts_until_reset: false }), generatedAtMS).untilReset).toBe('insufficient');
    // Low headroom is no longer a threshold on its own when the backend states
    // that the window lasts until the reset.
    expect(assessPrediction(item({ used_percent: 95, verdict: 'on_track', lasts_until_reset: true }), generatedAtMS).untilReset).toBe('enough');
    // Unknown stays unknown rather than becoming a warning.
    expect(assessPrediction(item({ used_percent: 95, verdict: 'unknown', reasons: ['fewer_than_three_buckets'], lasts_until_reset: null }), generatedAtMS).untilReset).toBe('unknown');
  });

  it('shows the actual and expected usage together', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item()] }])} />));
    expect(screen.getByText(/65\.0%.*40\.0%/)).toBeTruthy();
  });

  // F1: a window the backend says lasts until reset must not also carry the
  // "about to exhaust" advice, even when barely any headroom is left.
  it('never contradicts a lasts-until-reset answer with urgent advice', () => {
    const low = item({ used_percent: 95, verdict: 'on_track', lasts_until_reset: true, exhaustion_eta_ms: generatedAtMS + 3_600_000 });
    const assessment = assessPrediction(low, generatedAtMS);
    expect(assessment.untilReset).toBe('enough');
    expect(assessment.isUrgent).toBe(false);
    expect(assessment.today).toBe('enough');
    window.localStorage.setItem('omc-lang', 'en');
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [low] }])} />));
    expect(screen.getByText('No action needed.')).toBeTruthy();
    expect(screen.queryByText(/Exhaustion within/)).toBeNull();
  });

  // F5: the row states the gap as faster or slower percentage points.
  it('writes the deviation as faster or slower points', () => {
    expect(predictionDeviation(item({ used_percent: 65, expected_used_percent: 40 }))).toEqual({ direction: 'ahead', points: 25 });
    expect(predictionDeviation(item({ used_percent: 30, expected_used_percent: 40 }))).toEqual({ direction: 'behind', points: 10 });
    expect(predictionDeviation(item({ expected_used_percent: null }))).toBeNull();
    window.localStorage.setItem('omc-lang', 'en');
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item()] }])} />));
    expect(screen.getByText(/Ahead by 25\.0 points/)).toBeTruthy();
  });

  // F5: the footer names what the push leaves out, in disjoint buckets.
  it('summarizes the rows the push leaves out', () => {
    const stale = item({ window_id: 'stale', used_percent: null, reasons: ['stale_reading'] });
    const unknown = item({ window_id: 'unknown', verdict: 'unknown', used_percent: 50, reasons: ['fewer_than_three_buckets'] });
    const enough = item({ window_id: 'enough', verdict: 'on_track', lasts_until_reset: true });
    expect(summarizeUnpushed([stale, unknown, enough], generatedAtMS)).toEqual({ lasts: 1, unknown: 1, stale: 1 });
    window.localStorage.setItem('omc-lang', 'en');
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [stale, unknown, enough] }])} />));
    expect(screen.getByText('Not pushed: lasts to reset 1, unknown 1, stale 1')).toBeTruthy();
  });
});
