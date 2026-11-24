import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../src/api/client';
import { I18nProvider } from '../src/i18n';
import { QuotaLendingPage } from '../src/pages/quota/QuotaLendingPage';
import { QUOTA_LENDING_BACK_LABEL_KEY } from '../src/pages/quota/lendingMount';
import type { LendingCard, LendingOverview, LendingPool, QuotaOverviewResponse } from '../src/types/quota';

vi.mock('../src/api/client', () => ({
  api: { getQuotaOverview: vi.fn() },
}));

const NOW = Date.parse('2026-11-23T12:00:00Z');
const DAY = 86_400_000;

function card(overrides: Partial<LendingCard> & Pick<LendingCard, 'auth_index' | 'name'>): LendingCard {
  return {
    role: 'normal',
    reason_codes: [],
    is_comparable: true,
    remaining_percent: 50,
    reset_at_ms: NOW + 4 * DAY,
    reset_accuracy: 'exact',
    window_id: 'weekly',
    window_kind: 'weekly',
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    days_to_reset: 4,
    daily_available_percent: 12.5,
    ...overrides,
  };
}

function pool(cards: LendingCard[]): LendingPool {
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
    reason_codes: [],
    cards,
  };
}

const lending = (): LendingOverview => ({
  generated_at_ms: NOW,
  pools: [pool([
    card({ auth_index: 'a', name: 'codex-primary', remaining_percent: 88 }),
    card({ auth_index: 'b', name: 'codex-secondary', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
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
    window.history.replaceState({ idx: 1 }, '', '/omc/quota-lending');
    const withHistory = renderPage();
    expect(await screen.findByText('codex · Pro 200 · Weekly limit')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Go back' })).toBeTruthy();
    expect(screen.getByText(/Recomputed from the .* reading/)).toBeTruthy();
    withHistory.unmount();

    window.history.replaceState({ idx: 0 }, '', '/omc/quota-lending');
    renderPage();
    expect(await screen.findByText('codex · Pro 200 · Weekly limit')).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Go back' })).toBeNull();
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

