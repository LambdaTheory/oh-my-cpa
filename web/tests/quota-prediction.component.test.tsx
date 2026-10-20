import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import {
  QuotaPrediction,
  QuotaPredictionPanel,
  QUOTA_PREDICTION_REFRESH_OPTIONS,
  QUOTA_PREDICTION_REFETCH_INTERVAL_MS,
} from '../src/components/dashboard/QuotaPrediction';
import { api } from '../src/api/client';
import type { DashboardPredictionsResponse, QuotaPredictionItem } from '../src/types/quotaPrediction';

vi.mock('../src/api/client', () => ({
  api: {
    getDashboardPredictions: vi.fn(),
    postFeishuPredictionTest: vi.fn(),
  },
}));

const item = (overrides: Partial<QuotaPredictionItem> = {}): QuotaPredictionItem => ({
  credential_label: 'operator@example.com',
  provider: 'codex',
  window_kind: 'weekly',
  auth_index: 'auth-1',
  used_percent: 65,
  reset_at_ms: Date.parse('2026-10-21T08:00:00Z'),
  observed_at_ms: Date.parse('2026-10-20T08:00:00Z'),
  data_age_seconds: 300,
  burn_rate_per_hour: 4.2,
  projected_percent_at_reset: 110,
  projected_tokens_at_reset: 125000,
  projected_cost_nanos_at_reset: 125000000,
  exhaustion_eta_ms: Date.parse('2026-10-21T06:00:00Z'),
  verdict: 'will_exhaust',
  evidence: 'projection',
  reasons: ['no_price'],
  ...overrides,
});

const response = (providers: DashboardPredictionsResponse['providers']): DashboardPredictionsResponse => ({
  generated_at_ms: Date.parse('2026-10-20T08:00:00Z'),
  duration_ms: 7,
  omitted: 0,
  providers,
});

function surface(ui: React.ReactElement) {
  return (
    <I18nProvider>
      <AntdApp>{ui}</AntdApp>
    </I18nProvider>
  );
}

function renderOwner() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      {surface(<QuotaPrediction />)}
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  window.localStorage.setItem('omc-lang', 'en');
});

describe('dashboard quota prediction', () => {
  it('groups providers in dashboard order and renders full row evidence', () => {
    render(surface(<QuotaPredictionPanel data={response([
      { provider: 'codex', items: [item()] },
      { provider: 'antigravity', items: [item({ provider: 'antigravity', credential_label: 'gravity' })] },
    ])} />));
    const groups = screen.getAllByRole('region');
    expect(groups.map((group) => group.getAttribute('aria-label'))).toEqual(['Antigravity', 'Codex']);
    const codex = groups[1];
    expect(within(codex).getByText('operator@example.com')).toBeTruthy();
    expect(within(codex).getByText('Will exhaust')).toBeTruthy();
    expect(within(codex).getByText('65.0%')).toBeTruthy();
    expect(within(codex).getByText('110.0%')).toBeTruthy();
    expect(within(codex).getByText(/125,000 tokens/)).toBeTruthy();
    expect(screen.getByText('Computed in 7 ms')).toBeTruthy();
  });

  it('keeps the required foreground-only five-minute refresh configuration', () => {
    expect(QUOTA_PREDICTION_REFETCH_INTERVAL_MS).toBe(5 * 60 * 1000);
    expect(QUOTA_PREDICTION_REFRESH_OPTIONS).toEqual({
      refetchInterval: 5 * 60 * 1000,
      refetchIntervalInBackground: false,
      refetchOnWindowFocus: true,
    });
  });

  it('offers manual refresh and distinguishes empty, unknown, and stale data', () => {
    const refresh = vi.fn();
    const { rerender } = render(surface(<QuotaPredictionPanel data={response([])} onRefresh={refresh} />));
    expect(screen.getByText('No quota predictions yet')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Refresh quota prediction' }));
    expect(refresh).toHaveBeenCalledTimes(1);

    rerender(surface(<QuotaPredictionPanel data={response([{ provider: 'claude', items: [item({
      provider: 'claude', verdict: 'unknown', used_percent: null, projected_percent_at_reset: null,
      projected_tokens_at_reset: null, projected_cost_nanos_at_reset: null,
      data_age_seconds: 90000, reasons: ['stale_reading', 'fewer_than_two_observations'],
    })] }])} />));
    expect(screen.getByText('Unknown')).toBeTruthy();
    expect(screen.getByText('Stale data')).toBeTruthy();
    expect(screen.getByText('Quota reading is stale')).toBeTruthy();
  });

  it('confirms the Feishu action before calling it', async () => {
    const send = vi.fn();
    render(surface(<QuotaPredictionPanel data={response([])} onSend={send} />));
    fireEvent.click(screen.getByRole('button', { name: 'Send prediction summary to Feishu' }));
    expect(send).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole('button', { name: 'Send summary' }));
    expect(send).toHaveBeenCalledTimes(1);
  });

  it('shows only an informational toast when the manual send has no findings', async () => {
    vi.mocked(api.getDashboardPredictions).mockResolvedValue(response([]));
    vi.mocked(api.postFeishuPredictionTest).mockResolvedValue({ sent: false, reason: 'no_findings' });
    renderOwner();
    fireEvent.click(await screen.findByRole('button', { name: 'Send prediction summary to Feishu' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send summary' }));
    await waitFor(() => expect(api.postFeishuPredictionTest).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('No prediction findings to send')).toBeTruthy();
  });
});
