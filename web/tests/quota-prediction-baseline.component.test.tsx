import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { QuotaPredictionPanel } from '../src/components/dashboard/QuotaPrediction';
import type { DashboardPredictionsResponse, DashboardPredictionItem } from '../src/types/quotaPrediction';
import { assessPrediction } from '../src/components/dashboard/quotaPredictionLogic';

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
});
