import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../src/api/client';
import { I18nProvider } from '../src/i18n';
import { QuotaLendingPage } from '../src/pages/quota/QuotaLendingPage';
import { QUOTA_LENDING_BACK_LABEL_KEY, QUOTA_LENDING_PATH } from '../src/types/quotaLendingMount';
import type { LendingCard, LendingOverview, LendingPool, QuotaOverviewResponse } from '../src/types/quota';

vi.mock('../src/api/client', () => ({
  api: { getQuotaOverview: vi.fn() },
}));

const NOW = Date.parse('2026-11-23T12:00:00Z');
const DAY = 86_400_000;

function card(overrides: Partial<LendingCard> & Pick<LendingCard, 'auth_index' | 'name'>): LendingCard {
  return {
    display_name: overrides.name,
    role: 'suggested',
    reason_codes: [],
    is_comparable: true,
    remaining_percent: 50,
    reset_at_ms: NOW + 4 * DAY,
    reset_accuracy: 'exact',
    window_id: 'weekly',
    window_kind: 'weekly',
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    priority: 0,
    priority_known: true,
    priority_is_default: true,
    can_edit_priority: true,
    suggested_priority: 0,
    needs_change: false,
    ...overrides,
  };
}

function pool(cards: LendingCard[]): LendingPool {
  const suggested = cards.filter((entry) => entry.role === 'suggested');
  return {
    id: 'codex:pro',
    provider: 'codex',
    plan_type: 'pro',
    plan_label: 'Pro 200',
    window_kind: 'weekly',
    state: 'advised',
    valid_until_ms: NOW + 2 * DAY,
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    is_reference: false,
    is_balanced: false,
    needs_change_count: suggested.filter((entry) => entry.needs_change === true).length,
    hold_count: cards.filter((entry) => entry.role === 'hold').length,
    reason_codes: [],
    cards,
    outside_cards: [],
  };
}

const lending = (): LendingOverview => ({
  generated_at_ms: NOW,
  pools: [pool([
    card({ auth_index: 'a', name: 'synthetic-primary', tier: 1, remaining_percent: 88 }),
    card({
      auth_index: 'b', name: 'synthetic-secondary', tier: 2, remaining_percent: 40,
      reset_at_ms: NOW + 2 * DAY, suggested_priority: -10, needs_change: true,
    }),
  ])],
  unpooled_count: 0,
  unpooled: [],
});

function overview(lendingValue: LendingOverview | null): QuotaOverviewResponse {
  return {
    summary: {
      total_credentials: 0,
      healthy_count: 0,
      warning_count: 0,
      exhausted_count: 0,
      cooldown_count: 0,
      attention_count: 0,
    },
    quotas: [],
    total: 0,
    lending: lendingValue,
  };
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/omc/quota-lending']}>
        <I18nProvider><QuotaLendingPage /></I18nProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent: () => false,
  }));
  window.localStorage.setItem('omc-lang', 'en');
  vi.mocked(api.getQuotaOverview).mockResolvedValue(overview(lending()));
});

describe('quota lending page shell', () => {
  it('shows the back control only when the browser history has an entry behind it', async () => {
    expect(QUOTA_LENDING_BACK_LABEL_KEY).toBe('common.back');
    expect(QUOTA_LENDING_PATH).toBe('/quota-lending');
    window.history.replaceState({ idx: 1 }, '', '/omc/quota-lending');
    const withHistory = renderPage();
    expect(await screen.findByText('codex · Pro 200')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Go back' })).toBeTruthy();
    expect(screen.getByText(/Reading from .* · higher numbers are used first/)).toBeTruthy();
    withHistory.unmount();

    window.history.replaceState({ idx: 0 }, '', '/omc/quota-lending');
    renderPage();
    expect(await screen.findByText('codex · Pro 200')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Go back' })).toBeNull();
  });

  it('renders the pool rows and the read-only boundary from the overview', async () => {
    window.history.replaceState({ idx: 0 }, '', '/omc/quota-lending');
    const { container } = renderPage();
    expect(await screen.findByText('Change 1')).toBeTruthy();
    expect(container.querySelectorAll('[data-lending-card]')).toHaveLength(2);
    expect(container.querySelector('[data-lending-view="page"]')).toBeTruthy();
    const head = container.querySelector('.terminal-page-head') as HTMLElement;
    const title = head.querySelector('.terminal-title') as HTMLElement;
    const tag = head.querySelector('[data-lending-read-only="true"]') as HTMLElement;
    const subtitle = head.querySelector('.terminal-subtitle') as HTMLElement;
    expect(tag).toBeTruthy();
    expect(head.contains(tag)).toBe(true);
    expect(tag.parentElement).toBe(subtitle.parentElement);
    expect(container.querySelector('[data-lending-view="page"]')?.contains(tag)).toBe(false);
    expect(title.nextElementSibling).toBe(subtitle);
  });

  it('degrades to the empty state when the gateway returns lending: null', async () => {
    vi.mocked(api.getQuotaOverview).mockResolvedValue(overview(null));
    window.history.replaceState({ idx: 0 }, '', '/omc/quota-lending');
    const { container } = renderPage();
    expect(await screen.findByText('Lending advice could not be read')).toBeTruthy();
    expect(screen.queryByText('No lendable pool yet')).toBeNull();
    expect(container.querySelector('[data-lending-view="page"]')).toBeTruthy();
    expect(container.querySelector('[data-lending-absent="true"]')).toBeTruthy();
    expect(container.querySelector('[data-lending-unpooled-section]')).toBeNull();
  });
});
