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
import type { DashboardPredictionsResponse, DashboardPredictionItem } from '../src/types/quotaPrediction';
import { assessPrediction, classifyPredictionFailure, summarizePredictions } from '../src/components/dashboard/quotaPredictionLogic';

vi.mock('../src/api/client', async (importOriginal) => ({
  ...await importOriginal<typeof import('../src/api/client')>(),
  api: {
    getDashboardPredictions: vi.fn(),
    postFeishuPredictionTest: vi.fn(),
  },
}));

const item = (overrides: Partial<DashboardPredictionItem> = {}): DashboardPredictionItem => ({
  credential_label: 'operator@example.com',
  provider: 'codex',
  window_kind: 'weekly',
  window_id: 'weekly',
  window_scope: 'standard',
  auth_index: 'auth-1',
  used_percent: 65,
  remaining_tokens: 125000,
  capacity_error_percent: 1.2,
  remaining_window_hours: 58.8,
  window_error_hours: 0.84,
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

function expandProviders() {
  for (const summary of document.querySelectorAll('summary')) fireEvent.click(summary);
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
    expect([...document.querySelectorAll('details')].every((details) => !details.open)).toBe(true);
    expandProviders();
    expect([...document.querySelectorAll('details')].every((details) => details.open)).toBe(true);
    const codex = groups[1];
    expect(within(codex).getByText('operator@example.com')).toBeTruthy();
    expect(within(codex).getByText('35.0%')).toBeTruthy();
    expect(within(codex).getByText('About 22.0 hours')).toBeTruthy();
    expect(screen.getByText('2 providers insufficient')).toBeTruthy();
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
    expandProviders();
    expect(screen.getByText('Reading failed')).toBeTruthy();
    expect(screen.queryByText('Quota reading is stale')).toBeNull();
  });

  it('confirms the Feishu action before calling it', async () => {
    const send = vi.fn();
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item()] }])} onSend={send} />));
    fireEvent.click(screen.getByRole('button', { name: 'Send prediction summary to Feishu' }));
    expect(send).not.toHaveBeenCalled();
    fireEvent.click(await screen.findByRole('button', { name: 'Send summary' }));
    expect(send).toHaveBeenCalledTimes(1);
  });

  it('shows only an informational toast when the manual send has no findings', async () => {
    vi.mocked(api.getDashboardPredictions).mockResolvedValue(response([{ provider: 'codex', items: [item()] }]));
    vi.mocked(api.postFeishuPredictionTest).mockResolvedValue({ sent: false, reason: 'no_findings' });
    renderOwner();
    await screen.findByText('Codex');
    expandProviders();
    await screen.findByText('operator@example.com');
    fireEvent.click(screen.getByRole('button', { name: 'Send prediction summary to Feishu' }));
    fireEvent.click(await screen.findByRole('button', { name: 'Send summary' }));
    await waitFor(() => expect(api.postFeishuPredictionTest).toHaveBeenCalledTimes(1));
    expect(await screen.findByText('No prediction findings to send')).toBeTruthy();
  });

  it('distinguishes model windows for the same credential without duplicate keys', () => {
    const errors = vi.spyOn(console, 'error');
    try {
      render(surface(<QuotaPredictionPanel data={response([{ provider: 'claude', items: [
        item({ window_kind: 'model_scoped', window_id: 'seven_day_opus', window_scope: 'model', window_model: 'claude-opus' }),
        item({ window_kind: 'model_scoped', window_id: 'seven_day_sonnet', window_scope: 'model', window_model: 'claude-sonnet' }),
      ] }])} />));
      expandProviders();
      expect(screen.getByText('Model window · claude-opus')).toBeTruthy();
      expect(screen.getByText('Model window · claude-sonnet')).toBeTruthy();
      expect(errors.mock.calls.some((args) => args.join(' ').includes('same key'))).toBe(false);
    } finally {
      errors.mockRestore();
    }
  });

  it.each([['fallback-auth', 'fallback-auth'], [undefined, 'Unknown credential']])('names the combined reading with the resolved identity %s', (authIndex, label) => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({ credential_label: '', auth_index: authIndex })] }])} />));
    expandProviders();
    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.getByText('Remaining quota')).toBeTruthy();
    expect(screen.getByText('Time remaining at current pace')).toBeTruthy();
  });

  it('keeps one refresh in flight through rapid clicks and renders its final result', async () => {
    let complete!: (data: DashboardPredictionsResponse) => void;
    vi.mocked(api.getDashboardPredictions)
      .mockResolvedValueOnce(response([{ provider: 'codex', items: [item()] }]))
      .mockImplementationOnce(() => new Promise((resolve) => { complete = resolve; }));
    renderOwner();
    await screen.findByText('Codex');
    expandProviders();
    await screen.findByText('operator@example.com');
    const refresh = screen.getByRole('button', { name: 'Refresh quota prediction' });
    fireEvent.click(refresh);
    fireEvent.click(refresh);
    fireEvent.click(refresh);
    await waitFor(() => expect((refresh as HTMLButtonElement).disabled).toBe(true));
    expect(api.getDashboardPredictions).toHaveBeenCalledTimes(2);
    complete(response([{ provider: 'codex', items: [item({ used_percent: 72 })] }]));
    await screen.findByText('28.0%');
    await waitFor(() => expect((refresh as HTMLButtonElement).disabled).toBe(false));
    expect(api.getDashboardPredictions).toHaveBeenCalledTimes(2);
  });

  it.each([{ isLoading: true }, { isError: true }, { data: response([]) }])('disables send without usable data: %j', (props) => {
    render(surface(<QuotaPredictionPanel onSend={vi.fn()} {...props} />));
    expect((screen.getByRole('button', { name: 'Send prediction summary to Feishu' }) as HTMLButtonElement).disabled).toBe(true);
  });

  it.each(['pending', 'rejected'])('reports send failure without success for %s delivery', async (mode) => {
    vi.mocked(api.getDashboardPredictions).mockResolvedValue(response([{ provider: 'codex', items: [item()] }]));
    let complete!: () => void;
    vi.mocked(api.postFeishuPredictionTest).mockImplementationOnce(() => new Promise((resolve, reject) => {
      complete = () => mode === 'pending' ? resolve({ sent: false, error: 'Delivery pending' }) : reject(new Error('Transport failed'));
    }));
    renderOwner();
    await screen.findByText('Codex');
    expandProviders();
    await screen.findByText('operator@example.com');
    const trigger = screen.getByRole('button', { name: 'Send prediction summary to Feishu' });
    fireEvent.click(trigger);
    const confirm = await screen.findByRole('button', { name: 'Send summary' });
    fireEvent.click(confirm);
    await waitFor(() => expect((trigger as HTMLButtonElement).disabled).toBe(true));
    fireEvent.click(trigger);
    fireEvent.click(confirm);
    expect(api.postFeishuPredictionTest).toHaveBeenCalledTimes(1);
    complete();
    expect(await screen.findByText('Could not send prediction summary')).toBeTruthy();
    expect(screen.queryByText('Prediction summary sent to Feishu')).toBeNull();
  });

  it('uses 24-hour urgency before reset risk and never certifies partial providers', () => {
    const generatedAtMS = response([]).generated_at_ms;
    const late = item({ reset_at_ms: generatedAtMS + 72 * 3_600_000, exhaustion_eta_ms: generatedAtMS + 48 * 3_600_000 });
    expect(summarizePredictions([late], generatedAtMS)).toEqual({ today: 'enough', untilReset: 'insufficient' });
    expect(summarizePredictions([item({ exhaustion_eta_ms: generatedAtMS + 24 * 3_600_000 })], generatedAtMS).today).toBe('insufficient');
    const healthy = item({ verdict: 'on_track', projected_percent_at_reset: 80 });
    expect(summarizePredictions([healthy], generatedAtMS, true).today).toBe('unknown');
    expect(summarizePredictions([healthy, item({ verdict: 'unknown' })], generatedAtMS).today).toBe('unknown');
    expect(summarizePredictions([healthy, item()], generatedAtMS).today).toBe('insufficient');
  });

  it('marks ten-percent headroom independently from history and counts providers once', () => {
    const generatedAtMS = response([]).generated_at_ms;
    const low = item({ used_percent: 90, verdict: 'unknown', reasons: ['fewer_than_three_buckets'], exhaustion_eta_ms: null });
    expect(assessPrediction(low, generatedAtMS).untilReset).toBe('insufficient');
    expect(assessPrediction({ ...low, used_percent: 89.99 }, generatedAtMS).untilReset).toBe('unknown');
    expect(assessPrediction({ ...low, reasons: ['reading_error'] }, generatedAtMS).untilReset).toBe('unknown');
    expect(assessPrediction({ ...low, reset_at_ms: generatedAtMS, reasons: ['boundary_unknown'] }, generatedAtMS).untilReset).toBe('unknown');
    render(surface(<QuotaPredictionPanel data={response([
      { provider: 'codex', items: [low, { ...low, auth_index: 'second', window_id: 'five_hour' }] },
      { provider: 'step', items: [item({ provider: 'step', verdict: 'on_track', exhaustion_eta_ms: null })] },
      { provider: 'ollama', items: [item({ provider: 'ollama', verdict: 'unknown', reset_at_ms: null, reasons: ['boundary_unknown'] })] },
    ])} />));
    expect(screen.getByText('1 providers insufficient')).toBeTruthy();
    expect(screen.getByText('Step')).toBeTruthy();
    expect(screen.getByText('Ollama')).toBeTruthy();
    expandProviders();
    expect(screen.getAllByText(/At most 10% remains/)).toHaveLength(2);
    expect(screen.getByText('Unknown cycle')).toBeTruthy();
  });

  it.each([
    ['reading_error', 'reading_failed'],
    ['fewer_than_three_buckets', 'history_insufficient'],
    ['boundary_unknown', 'cycle_unknown'],
    ['mid_cycle_reset', 'usage_abnormal'],
    ['invalid_used_percent', 'usage_abnormal'],
  ])('groups %s into one actionable failure class', (reason, category) => {
    expect(classifyPredictionFailure(item({ verdict: 'unknown', reasons: [reason] }), response([]).generated_at_ms)).toBe(category);
  });

  it('bounds endurance to the current cycle and keeps omitted summaries unknown', () => {
    const generatedAtMS = response([]).generated_at_ms;
    const healthy = item({ verdict: 'on_track', reset_at_ms: generatedAtMS + 3_600_000, exhaustion_eta_ms: generatedAtMS + 10 * 3_600_000 });
    expect(assessPrediction(healthy, generatedAtMS).enduranceMS).toBeNull();
    render(surface(<QuotaPredictionPanel data={{ ...response([{ provider: 'codex', items: [healthy] }]), omitted: 1 }} />));
    expect(screen.queryByText('No known shortage')).toBeNull();
    expandProviders();
    expect(screen.getByText('Through the next reset')).toBeTruthy();
    expect(screen.queryByText('About 10.0 hours')).toBeNull();
  });

  it('keeps a valid flat forecast readable when low balance independently triggers shortage', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({ used_percent: 95, verdict: 'on_track', exhaustion_eta_ms: null, reasons: ['no_positive_trend'] })] }])} />));
    expandProviders();
    expect(screen.getByText('Through the next reset')).toBeTruthy();
    expect(screen.getByText('1 providers insufficient')).toBeTruthy();
  });

  it('describes an elapsed exhaustion ETA without a zero-hour countdown', () => {
    const generatedAtMS = response([]).generated_at_ms;
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({ exhaustion_eta_ms: generatedAtMS - 60_000 })] }])} />));
    expandProviders();
    expect(screen.getByText('Exhausted')).toBeTruthy();
    expect(screen.getByText(/Estimated exhaustion time has passed/)).toBeTruthy();
    expect(screen.queryByText(/within 0 hours|About 0.0 hours/)).toBeNull();
  });

  it('renders missing readings explicitly, with an action for every forecast', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({
      verdict: 'unknown', used_percent: null, reset_at_ms: null, data_age_seconds: null,
      projected_percent_at_reset: null, projected_tokens_at_reset: null, projected_cost_nanos_at_reset: null,
      exhaustion_eta_ms: null, reasons: ['reading_error'],
    })] }])} />));
    expandProviders();
    expect(screen.getAllByText('Data missing').length).toBeGreaterThanOrEqual(3);
    expect(screen.queryAllByRole('meter')).toHaveLength(0);
    expect(screen.queryByText('0.0%')).toBeNull();
    expect(screen.getByText('Reading failed')).toBeTruthy();
    expect(screen.getByText('Suggestion: check quota readings and history before deciding.')).toBeTruthy();
  });

  it('labels inferred allowance with error and groups a just-reset window as history', () => {
    const data = response([{ provider: 'codex', items: [{ ...item({ verdict: 'unknown', reasons: ['fewer_than_three_buckets'] }),
      window_start_ms: Date.parse('2026-10-20T07:59:00Z'), remaining_percent: 35, uniform_projected_percent_at_reset: null, remaining_tokens: null,
    }] }]);
    const { rerender } = render(surface(<QuotaPredictionPanel data={data} />));
    expandProviders();
    expect(screen.getByText('Insufficient history')).toBeTruthy();
    expect(screen.getByText(/58.8 quota-hours · error ±0.84/)).toBeTruthy();
    expect(screen.queryByText('Projected at reset')).toBeNull();
    expect(screen.getByText('35.0%')).toBeTruthy();
    rerender(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({ verdict: 'on_track' })] }])} />));
    expect(screen.getByText('No action needed.')).toBeTruthy();
    rerender(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item()] }])} />));
    expect(screen.getByText(/Exhaustion within 22 hours/)).toBeTruthy();
  });
});
