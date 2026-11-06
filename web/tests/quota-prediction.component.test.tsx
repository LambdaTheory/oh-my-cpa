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
import {
  formatPredictionDeviation,
  formatPredictionETA,
  predictionConclusion,
  predictionDeviation,
  predictionLasts,
  sortPredictions,
  summarizePredictions,
} from '../src/components/dashboard/quotaPredictionLogic';
import { useT } from '../src/i18n';

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
  sample_count: 12,
  coverage_seconds: 7200,
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

function instant(value: number): string {
  const date = new Date(value);
  const pad = (number: number) => String(number).padStart(2, '0');
  return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  window.localStorage.setItem('omc-lang', 'en');
});

describe('dashboard quota prediction', () => {
  it('groups providers in dashboard order and states the four fields per row', () => {
    render(surface(<QuotaPredictionPanel data={response([
      { provider: 'codex', items: [item()] },
      { provider: 'antigravity', items: [item({ provider: 'antigravity', credential_label: 'gravity', lasts_until_reset: true, exhaustion_eta_ms: null })] },
    ])} />));
    const groups = screen.getAllByRole('region');
    expect(groups.map((group) => group.getAttribute('aria-label'))).toEqual(['Antigravity', 'Codex']);
    expect([...document.querySelectorAll('details')].every((details) => !details.open)).toBe(true);
    expandProviders();
    expect([...document.querySelectorAll('details')].every((details) => details.open)).toBe(true);
    const codex = groups[1];
    expect(within(codex).getByText('operator@example.com')).toBeTruthy();
    expect(within(codex).getByText('Actual')).toBeTruthy();
    expect(within(codex).getByText('65.0%')).toBeTruthy();
    expect(within(codex).getByText('Expected')).toBeTruthy();
    expect(within(codex).getByText('50.0%')).toBeTruthy();
    expect(within(codex).getByText('15.0 pts over')).toBeTruthy();
    expect(within(codex).getAllByText('Cannot last').length).toBeGreaterThan(0);
    expect(within(codex).getByText(/Projected to run out at \d\d-\d\d \d\d:\d\d, before the reset/)).toBeTruthy();
    expect(screen.getByText('1 windows cannot last until reset · 0 undecidable')).toBeTruthy();
    expect(screen.getByText('Computed in 7 ms')).toBeTruthy();
  });

  it('counts credentials in the group header, not quota windows', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'claude', items: [
      item({ provider: 'claude', auth_index: 'auth-1', credential_label: 'work', window_kind: 'five_hour', window_id: 'five_hour' }),
      item({ provider: 'claude', auth_index: 'auth-1', credential_label: 'work', window_kind: 'weekly', window_id: 'weekly' }),
      item({ provider: 'claude', auth_index: 'auth-2', credential_label: 'personal', window_kind: 'weekly', window_id: 'weekly' }),
    ] }])} />));
    // Two credentials behind three windows: the header counts credentials and
    // the rows underneath count windows.
    expect(screen.getByText('2 credentials · 3 cannot last')).toBeTruthy();
  });

  it('never renders inferred tokens, cost, or quota-hour estimates', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item()] }])} />));
    expandProviders();
    expect(screen.queryByText(/125,000 tokens/)).toBeNull();
    expect(screen.queryByText(/quota-hours/)).toBeNull();
    expect(screen.queryByText(/Projected tokens/)).toBeNull();
    expect(screen.queryByText(/Time remaining at current pace/)).toBeNull();
    expect(screen.queryByText(/24 hours/)).toBeNull();
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
      provider: 'claude',
      lasts_until_reset: null,
      used_percent: 40,
      expected_used_percent: 38,
      exhaustion_eta_ms: null,
      unavailable_reason: 'history_insufficient',
      reasons: ['fewer_than_two_observations'],
    })] }])} />));
    expandProviders();
    expect(screen.getAllByText('Undecidable').length).toBeGreaterThan(0);
    expect(screen.getAllByText(/Not enough observations to estimate a trend/).length).toBeGreaterThan(0);
    expect(screen.getByText('Cannot be estimated')).toBeTruthy();
    expect(screen.queryByText('Data missing')).toBeNull();

    rerender(surface(<QuotaPredictionPanel data={response([{ provider: 'claude', items: [item({
      provider: 'claude',
      lasts_until_reset: null,
      reading_stale: true,
      unavailable_reason: 'reading_failed',
      exhaustion_eta_ms: null,
      reasons: ['stale_reading'],
    })] }])} />));
    expandProviders();
    expect(screen.getByText('The reading is stale, so it is not estimated.')).toBeTruthy();
    expect(screen.getAllByText('Stale').length).toBeGreaterThan(0);
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

  it.each([['fallback-auth', 'fallback-auth'], [undefined, 'Unknown credential']])('names the row with the resolved identity %s', (authIndex, label) => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({ credential_label: '', auth_index: authIndex })] }])} />));
    expandProviders();
    expect(screen.getByText(label)).toBeTruthy();
    expect(screen.getByText('Actual')).toBeTruthy();
    expect(screen.getByText('Expected')).toBeTruthy();
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
    await screen.findByText('72.0%');
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

  it('derives the three-state conclusion only from lasts_until_reset', () => {
    expect(predictionLasts(item({ lasts_until_reset: true }))).toBe('yes');
    expect(predictionLasts(item({ lasts_until_reset: false }))).toBe('no');
    expect(predictionLasts(item({ lasts_until_reset: null }))).toBe('unknown');
    // The deprecated verdict must never override the three-state field.
    expect(predictionLasts(item({ lasts_until_reset: true, verdict: 'will_exhaust' }))).toBe('yes');
    expect(predictionLasts(item({ lasts_until_reset: false, verdict: 'on_track' }))).toBe('no');
  });

  it('computes the actual-minus-expected deviation and renders missing values as dashes', () => {
    expect(predictionDeviation(item({ used_percent: 65, expected_used_percent: 50 }))).toBe(15);
    expect(predictionDeviation(item({ used_percent: 30, expected_used_percent: 50 }))).toBe(-20);
    expect(predictionDeviation(item({ used_percent: null, expected_used_percent: 50 }))).toBeNull();
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({
      used_percent: 30, expected_used_percent: 50, lasts_until_reset: true, exhaustion_eta_ms: null,
    })] }])} />));
    expandProviders();
    expect(screen.getByText('20.0 pts under')).toBeTruthy();
  });

  it('sorts by exhaustion risk and keeps undecidable rows last', () => {
    const generatedAtMS = response([]).generated_at_ms;
    const exhausted = item({ auth_index: 'a', used_percent: 100, lasts_until_reset: false, exhaustion_eta_ms: generatedAtMS });
    const past = item({ auth_index: 'b', used_percent: 90, lasts_until_reset: false, exhaustion_eta_ms: generatedAtMS - 60_000 });
    const cannot = item({ auth_index: 'c', used_percent: 80, lasts_until_reset: false, exhaustion_eta_ms: generatedAtMS + 3_600_000 });
    const can = item({ auth_index: 'd', used_percent: 20, lasts_until_reset: true, exhaustion_eta_ms: null });
    const unknown = item({ auth_index: 'e', used_percent: 20, lasts_until_reset: null, exhaustion_eta_ms: null });
    const sorted = sortPredictions([unknown, can, cannot, past, exhausted], generatedAtMS);
    expect(sorted.map((row) => row.auth_index)).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(summarizePredictions([exhausted, cannot, can, unknown])).toEqual({ cannot: 2, unknown: 1 });
  });

  it('renders exhausted windows with the reading instant and the waiting copy', () => {
    const reading = Date.parse('2026-10-20T08:00:00Z');
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({
      used_percent: 100, expected_used_percent: 70, lasts_until_reset: false, exhaustion_eta_ms: reading,
    })] }])} />));
    expandProviders();
    expect(screen.getByText('Quota used up; waiting for the reset.')).toBeTruthy();
    expect(screen.getByText(instant(reading))).toBeTruthy();
  });

  it('renders no-cycle amounts as cannot estimate without a zero percent', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'ollama', items: [item({
      provider: 'ollama',
      used_percent: 3.2,
      expected_used_percent: null,
      reset_at_ms: null,
      lasts_until_reset: null,
      exhaustion_eta_ms: null,
      unavailable_reason: 'no_cycle',
      reasons: ['boundary_unknown'],
    })] }])} />));
    expandProviders();
    expect(screen.getAllByText('Cannot estimate: no cycle.').length).toBeGreaterThan(0);
    expect(screen.getByText('Cannot be calculated')).toBeTruthy();
    expect(screen.queryByText('0.0%')).toBeNull();
  });

  it('renders missing readings explicitly, with an action for every unknown row', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({
      lasts_until_reset: null, used_percent: null, expected_used_percent: null, reset_at_ms: null, data_age_seconds: null,
      exhaustion_eta_ms: null, unavailable_reason: 'reading_failed', reasons: ['reading_error'], sample_count: 0, coverage_seconds: null,
    })] }])} />));
    expandProviders();
    expect(screen.getAllByText('Cannot be calculated').length).toBeGreaterThanOrEqual(2);
    expect(screen.getAllByText('—').length).toBeGreaterThanOrEqual(1);
    expect(screen.queryByText('0.0%')).toBeNull();
    expect(screen.getByText('Cannot be estimated')).toBeTruthy();
    expect(screen.getAllByText(/The latest quota read failed/).length).toBeGreaterThan(0);
    expect(screen.queryByText('Data missing')).toBeNull();
  });

  it('groups every unknown category into the four actionable classes plus no-cycle', () => {
    const cases: Array<[DashboardPredictionItem['unavailable_reason'], string]> = [
      ['reading_failed', 'The latest quota read failed; check credential auth and upstream status, then retry.'],
      ['history_insufficient', 'Not enough observations to estimate a trend; wait for the next collection. This is normal right after a reset or after onboarding.'],
      ['window_boundary_unknown', 'The window boundaries are unknown, so progress cannot be calculated; check whether upstream returns a reset time. Quotas without a fixed period belong here.'],
      ['usage_abnormal', 'The reading or trend is abnormal, so no conclusion is given; check for out-of-range values or a mid-window reset.'],
      ['no_cycle', 'Cannot estimate: no cycle.'],
    ];
    for (const [reason, copy] of cases) {
      expect(predictionConclusion(item({ lasts_until_reset: null, exhaustion_eta_ms: null, unavailable_reason: reason }), 0).key)
        .toBe(`dash.prediction.unknown.${reason}`);
      const { unmount } = render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({
        lasts_until_reset: null, used_percent: 30, expected_used_percent: 25, exhaustion_eta_ms: null, unavailable_reason: reason,
      })] }])} />));
      expandProviders();
      expect(screen.getAllByText(copy).length).toBeGreaterThan(0);
      unmount();
    }
  });

  it('states a flat trend as will-not-exhaust and a stale reading as expired', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [item({
      used_percent: 20, expected_used_percent: 25, lasts_until_reset: true, exhaustion_eta_ms: null,
    })] }])} />));
    expandProviders();
    expect(screen.getByText('At the current pace it lasts until the reset.')).toBeTruthy();
    expect(screen.getByText('Will not exhaust')).toBeTruthy();
    expect(screen.getAllByText('Lasts until reset').length).toBeGreaterThan(0);
  });

  it('formats the ETA and deviation copy for every tri-state branch', () => {
    let t!: ReturnType<typeof useT>;
    function Capture() {
      t = useT();
      return null;
    }
    render(<I18nProvider><Capture /></I18nProvider>);
    const eta = Date.parse('2026-10-20T09:30:00Z');
    expect(formatPredictionETA(item({ exhaustion_eta_ms: eta }), t)).toBe(instant(eta));
    expect(formatPredictionETA(item({ lasts_until_reset: true, exhaustion_eta_ms: null }), t)).toBe('Will not exhaust');
    expect(formatPredictionETA(item({ lasts_until_reset: null, exhaustion_eta_ms: null }), t)).toBe('Cannot be estimated');
    expect(formatPredictionDeviation(item({ used_percent: 65, expected_used_percent: 50 }), t)).toBe('15.0 pts over');
    expect(formatPredictionDeviation(item({ used_percent: null, expected_used_percent: 50 }), t)).toBe('—');
  });

  it('counts only rows present, keeping omitted totals out of the shortage count', () => {
    render(surface(<QuotaPredictionPanel data={{ ...response([
      { provider: 'codex', items: [item({ lasts_until_reset: false }), item({ auth_index: 'ok', lasts_until_reset: true })] },
    ]), omitted: 1 }} />));
    expandProviders();
    expect(screen.getByText(/1 windows cannot last until reset · 0 undecidable/)).toBeTruthy();
    expect(screen.getByText(/1 more not shown/)).toBeTruthy();
  });
});
