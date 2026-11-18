import React from 'react';
import { App as AntdApp } from 'antd';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { QuotaPredictionPanel } from '../src/components/dashboard/QuotaPrediction';
import type { DashboardPredictionsResponse, DashboardPredictionItem } from '../src/types/quotaPrediction';

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
  expected_used_percent: 50,
  reset_at_ms: Date.parse('2026-10-21T08:00:00Z'),
  observed_at_ms: generatedAtMS,
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

const response = (providers: DashboardPredictionsResponse['providers']): DashboardPredictionsResponse => ({
  generated_at_ms: generatedAtMS,
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

function openGroup(name: string): HTMLElement {
  const group = screen.getByRole('region', { name });
  const details = group.querySelector(':scope > details') as HTMLDetailsElement;
  if (!details.open) fireEvent.click(within(details).getAllByText(name)[0].closest('summary') as HTMLElement);
  return group;
}

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
});

describe('quota prediction collapsed row classes', () => {
  it('shows a rolling window as a current reading without ETA, expectation or risk conclusion', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'antigravity', items: [item({
      provider: 'antigravity',
      credential_label: 'gravity@example.com',
      is_rolling_window: true,
      lasts_until_reset: null,
      expected_used_percent: null,
      exhaustion_eta_ms: null,
      used_percent: 40,
      reasons: ['rolling_window'],
      unavailable_reason: 'rolling_window',
    })] }])} />));
    openGroup('Antigravity');
    const row = screen.getByText('gravity@example.com').closest('li') as HTMLElement;
    expect(row.getAttribute('data-kind')).toBe('rolling');
    expect(row.getAttribute('data-lasts')).toBeNull();
    expect(within(row).getByText('40.0%')).toBeTruthy();
    expect(within(row).getByText('Rolling window')).toBeTruthy();
    expect(within(row).getByText(/Rolling window \(no fixed period\)/)).toBeTruthy();
    expect(within(row).getByText('A rolling window has no fixed period; only the current usage and next reset are shown.')).toBeTruthy();
    expect(within(row).queryByText('Expected')).toBeNull();
    expect(within(row).queryByText('Deviation')).toBeNull();
    expect(within(row).queryByText('Exhausts')).toBeNull();
    expect(within(row).queryByText('Cannot be estimated')).toBeNull();
    const summary = screen.getByTestId('prediction-summary').textContent;
    expect(summary).toBe('0 windows cannot last until reset · 0 undecidable');
    expect(within(screen.getByRole('region', { name: 'Antigravity' })).getByText('No risk verdict')).toBeTruthy();
  });

  it('folds zero-traffic zero-use windows into one expandable panel line and drops idle-only providers', () => {
    render(surface(<QuotaPredictionPanel data={response([
      { provider: 'codex', items: [
        item({ credential_label: 'risk@example.com', lasts_until_reset: false }),
        item({ auth_index: 'idle-one', credential_label: 'idle-one@example.com', window_id: 'daily', used_percent: 0, usage_requests: 0, lasts_until_reset: true, exhaustion_eta_ms: null }),
        item({ auth_index: 'idle-two', credential_label: 'idle-two@example.com', window_id: 'weekly', used_percent: 0, usage_requests: 0, lasts_until_reset: true, exhaustion_eta_ms: null }),
      ] },
      { provider: 'claude', items: [
        item({ provider: 'claude', auth_index: 'only-idle', credential_label: 'only-idle@example.com', used_percent: 0, usage_requests: 0, lasts_until_reset: true, exhaustion_eta_ms: null }),
      ] },
    ])} />));
    openGroup('Codex');
    expect(screen.queryByRole('region', { name: 'Claude' })).toBeNull();
    expect(document.querySelectorAll('li[data-kind="idle"]').length).toBe(0);
    const idle = screen.getByTestId('prediction-idle') as HTMLDetailsElement;
    expect(idle.open).toBe(false);
    expect(within(idle).getByText('Idle windows hidden: 3 (zero traffic and zero usage this cycle)')).toBeTruthy();
    fireEvent.click(within(idle).getByText('Idle windows hidden: 3 (zero traffic and zero usage this cycle)'));
    expect(idle.open).toBe(true);
    expect(within(idle).getByText('only-idle@example.com')).toBeTruthy();
    expect(within(idle).getByText('idle-one@example.com')).toBeTruthy();
    expect(within(idle).getByText('idle-two@example.com')).toBeTruthy();
    expect(screen.getByTestId('prediction-summary').textContent).toBe('1 windows cannot last until reset · 0 undecidable');
  });

  it('keeps a zero reading with unread or non-zero traffic in the risk list', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [
      item({ credential_label: 'unread@example.com', used_percent: 0, usage_requests: undefined, lasts_until_reset: null }),
      item({ credential_label: 'traffic@example.com', window_id: 'daily', used_percent: 0, usage_requests: 4, lasts_until_reset: null }),
    ] }])} />));
    openGroup('Codex');
    expect(screen.getByText('unread@example.com')).toBeTruthy();
    expect(screen.getByText('traffic@example.com')).toBeTruthy();
    expect(screen.queryByTestId('prediction-idle')).toBeNull();
    expect(screen.getByTestId('prediction-summary').textContent).toBe('0 windows cannot last until reset · 2 undecidable');
  });

  it('folds stale readings into an expandable expired section and keeps them out of the counts', () => {
    render(surface(<QuotaPredictionPanel data={response([{ provider: 'codex', items: [
      item({ credential_label: 'risk@example.com', lasts_until_reset: false }),
      item({
        credential_label: 'stale@example.com',
        window_id: 'stale',
        used_percent: 65,
        expected_used_percent: null,
        lasts_until_reset: null,
        exhaustion_eta_ms: null,
        reading_stale: true,
        unavailable_reason: 'reading_failed',
        reasons: ['stale_reading'],
      }),
    ] }])} />));
    const group = openGroup('Codex');
    const expired = screen.getByText('Expired / unavailable (1)').closest('details') as HTMLDetailsElement;
    expect(expired.open).toBe(false);
    const mainList = group.querySelector(':scope > details > ul') as HTMLElement;
    expect(within(mainList).queryByText('stale@example.com')).toBeNull();
    expect(within(group).getByText('stale@example.com')).toBeTruthy();
    fireEvent.click(screen.getByText('Expired / unavailable (1)'));
    expect(expired.open).toBe(true);
    const staleRow = within(expired).getByText('stale@example.com').closest('li') as HTMLElement;
    expect(staleRow.getAttribute('data-kind')).toBe('stale');
    expect(screen.getByTestId('prediction-summary').textContent).toBe('1 windows cannot last until reset · 0 undecidable');
    expect(screen.getByText('Not pushed: lasts to reset 0, unknown 0, stale 1')).toBeTruthy();
  });
});
