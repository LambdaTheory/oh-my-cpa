import { render, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { I18nProvider } from '../src/i18n';
import { CompactQuotaView } from '../src/pages/quota/CompactQuotaView';
import { CredentialQuotaBody } from '../src/pages/quota/CredentialQuotaBody';
import type { QuotaItem, QuotaWindow } from '../src/types/quota';

const NOW_MS = Date.parse('2026-11-12T08:00:00Z');
const HOUR_MS = 3_600_000;

function windowFixture(
  overrides: Partial<QuotaWindow> & Pick<QuotaWindow, 'id' | 'label'>,
): QuotaWindow {
  return { scope: 'standard', reset_accuracy: 'exact', ...overrides };
}

function pluginQuota(authIndex: string, provider: string, overrides: Partial<QuotaItem> = {}): QuotaItem {
  return {
    auth_index: authIndex,
    name: authIndex,
    type: provider,
    provider,
    disabled: false,
    status: 'healthy',
    observed_at_ms: NOW_MS,
    windows: [],
    recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
    capabilities: { refresh_supported: true, clear_cooldown_supported: false, reset_credit_supported: false },
    quota_exceeded: false,
    ...overrides,
  };
}

// An Ollama credential entered as api_key has no quota windows at all: the balance is the whole
// reading. The `ollama_balance_source` signal is how the view currently distinguishes a verified
// balance from an inferred one; this fixture carries it so these assertions are about rendering
// the plugin's balance text and the missing-window state, not about that ownership gate.
const OLLAMA_API_KEY = pluginQuota('cpa-opaque-ollama', 'ollama', {
  status: 'unpublished',
  plan: { plan_type: 'unknown', plan_label: '', tier: 'unknown', credits: { balance: '12.34 USD' } },
  raw_signals: { ollama_balance_source: 'settings_balance' },
});

const CMC = pluginQuota('cpa-opaque-cmc', 'openai-compatible-command code', {
  plan: {
    plan_type: 'unknown',
    plan_label: 'Pro',
    tier: 'unknown',
    credits: { balance: '321 credits', stale: true },
  },
  windows: [
    windowFixture({
      id: 'cmc-5h', label: 'Command Code · 5-hour session', kind: 'five_hour', period_hours: 5,
      remaining_percent: 80, reset_at_ms: NOW_MS + 3 * HOUR_MS,
    }),
    windowFixture({
      id: 'cmc-7d', label: 'Command Code · Weekly', kind: 'weekly', period_hours: 168,
      remaining_percent: 55, reset_at_ms: NOW_MS + 100 * HOUR_MS,
    }),
  ],
});

const OPC = pluginQuota('cpa-opaque-opc', 'openai-compatible-opencode', {
  windows: [
    windowFixture({
      id: 'opc-rolling', label: 'Rolling', remaining_percent: 0, reset_at_ms: NOW_MS + HOUR_MS,
      stale: true, note: 'upstream reports this window rate limited',
    }),
    windowFixture({ id: 'opc-weekly', label: 'Weekly', remaining_percent: 42, reset_at_ms: NOW_MS + 72 * HOUR_MS }),
    windowFixture({ id: 'opc-monthly', label: 'Monthly', remaining_percent: 21, reset_at_ms: NOW_MS + 480 * HOUR_MS }),
  ],
});

function renderRow(item: QuotaItem) {
  const view = render(<I18nProvider><CompactQuotaView item={item} nowMS={NOW_MS} /></I18nProvider>);
  const row = view.container.querySelector('[data-quota-density="compact"]') as HTMLElement;
  return { ...view, row };
}

function renderBody(item: QuotaItem) {
  const view = render(
    <MemoryRouter>
      <I18nProvider>
        <CredentialQuotaBody item={item} nowMS={NOW_MS} isDemo={false} />
      </I18nProvider>
    </MemoryRouter>,
  );
  const body = view.container.querySelector('[data-quota-density="expanded"]') as HTMLElement;
  return { ...view, body };
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  window.localStorage.setItem('omc-lang', 'en');
});

describe('plugin credential quota rendering', () => {
  it('renders an Ollama api_key reading as its USD balance with the missing-window state', () => {
    const { row } = renderRow(OLLAMA_API_KEY);
    expect(within(row).getByText('Credit balance: 12.34 USD')).toBeTruthy();
    expect(within(row).getByText('Upstream answered without a quota window')).toBeTruthy();
    expect(row.querySelectorAll('[data-quota-compact-window]')).toHaveLength(0);
    // Nothing was cached: a first, successful reading carries no mark.
    expect(row.querySelector('[data-plugin-quota-stale]')).toBeNull();
  });

  it('renders Command Code plan, credits balance and its 5-hour and weekly windows', () => {
    const { row } = renderRow(CMC);
    expect(within(row).getByText('Pro')).toBeTruthy();
    expect(within(row).getByText('Credit balance: 321 credits')).toBeTruthy();
    expect(within(row).getByText('5-hour limit')).toBeTruthy();
    expect(within(row).getByText('Weekly limit')).toBeTruthy();
    expect(within(row).getAllByRole('meter').map((meter) => meter.getAttribute('aria-valuenow')))
      .toEqual(['80', '55']);
  });

  it('renders OpenCode Go rolling and weekly windows without inventing a plan or balance', () => {
    const { row } = renderRow(OPC);
    expect(row.querySelector('[data-credit-balance]')).toBeNull();
    expect(within(row).queryByText('Plan')).toBeNull();
    const labels = Array.from(row.querySelectorAll('[data-quota-compact-window]'))
      .map((element) => element.getAttribute('data-quota-window-label'));
    expect(labels).toEqual(['Rolling', 'Weekly']);
    expect(within(row).getAllByRole('meter').map((meter) => meter.getAttribute('aria-valuenow')))
      .toEqual(['0', '42']);
  });

  it('lists every window in the drawer, including the one the row leaves out', () => {
    const { body } = renderBody(OPC);
    expect(body.textContent).toContain('Rolling');
    expect(body.textContent).toContain('Weekly');
    expect(body.textContent).toContain('Monthly');
    expect(within(body).getAllByRole('meter')).toHaveLength(3);
    // No summary and no subscription: neither a plan nor a balance is fabricated.
    expect(body.querySelector('[data-credit-balance]')).toBeNull();
    expect(within(body).queryByText('Plan')).toBeNull();
  });
});

describe('plugin stale readings', () => {
  it('marks a stale credits balance on the row and names the plugin reason in the tooltip', () => {
    const { row } = renderRow(CMC);
    const badge = row.querySelector('[data-plugin-quota-stale]') as HTMLElement;
    expect(badge).not.toBeNull();
    expect(badge.textContent).toBe('Cached reading');
    expect(badge.getAttribute('title')).toBe(
      'The plugin returned its last successful reading, not the result of this refresh',
    );
  });

  it('marks a stale reading in the drawer status row as well', () => {
    const { body } = renderBody(CMC);
    const badge = body.querySelector('[data-plugin-quota-stale]') as HTMLElement;
    expect(badge).not.toBeNull();
    expect(badge.textContent).toBe('Cached reading');
    expect(badge.getAttribute('title')).toContain('last successful reading');
    // The cached balance itself still renders.
    expect(within(body).getByText('321 credits')).toBeTruthy();
  });

  it('marks the stale window in the drawer and prints its note instead of swallowing it', () => {
    const { body } = renderBody(OPC);
    const staleWindows = body.querySelectorAll('[data-quota-window-stale]');
    expect(staleWindows).toHaveLength(1);
    expect(staleWindows[0].textContent).toBe('Cached reading');
    expect(within(body).getByText('upstream reports this window rate limited')).toBeTruthy();
    expect(body.querySelectorAll('[data-quota-window-note]')).toHaveLength(1);
  });

  it('keeps the plugin reason in the row tooltip when only a window is stale', () => {
    const { row } = renderRow(OPC);
    const badge = row.querySelector('[data-plugin-quota-stale]') as HTMLElement;
    expect(badge).not.toBeNull();
    expect(badge.getAttribute('title')).toContain('upstream reports this window rate limited');
  });
});
