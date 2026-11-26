import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { QuotaLendingPage } from '../src/pages/quota/QuotaLendingPage';

const NOW = Date.parse('2026-11-23T12:00:00Z');
const DAY = 86_400_000;

/**
 * Payloads an older gateway may return: a card without the friendly name, or a pool without the
 * outside list. They are deliberately built as plain JSON rather than typed fixtures because the
 * point is the missing field (PRD §7).
 */
function cardBody(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    auth_index: 'card-a',
    name: 'codex-demo-a.json',
    role: 'suggested',
    tier: 1,
    remaining_percent: 80,
    reset_at_ms: NOW + DAY,
    reset_accuracy: 'exact',
    priority: 0,
    priority_known: true,
    priority_is_default: true,
    can_edit_priority: true,
    suggested_priority: 0,
    needs_change: false,
    reason_codes: [],
    is_comparable: true,
    ...overrides,
  };
}

function poolBody(cards: Record<string, unknown>[], outsideCards?: Record<string, unknown>[]): Record<string, unknown> {
  return {
    id: 'codex:pro',
    provider: 'codex',
    plan_type: 'pro',
    plan_label: 'Pro 200',
    window_kind: 'weekly',
    state: 'advised',
    is_reference: false,
    is_balanced: false,
    needs_change_count: 0,
    hold_count: 0,
    reason_codes: [],
    cards,
    ...(outsideCards ? { outside_cards: outsideCards } : {}),
  };
}

function overviewBody(pools: Record<string, unknown>[]): Record<string, unknown> {
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
    lending: { generated_at_ms: NOW, pools, unpooled_count: 0, unpooled: [] },
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
  window.localStorage.setItem('omc-lang', 'en');
  window.history.replaceState({ idx: 0 }, '', '/omc/quota-lending');
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
});

function stubOverview(body: Record<string, unknown>) {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  })));
}

describe('quota lending page with a degraded payload', () => {
  it('shows the file name on every surface when a card has no display_name', async () => {
    stubOverview(overviewBody([poolBody([cardBody({ suggested_priority: -10, needs_change: true })], [])]));
    const { container } = renderPage();

    expect(await screen.findByText('codex-demo-a.json')).toBeTruthy();
    const row = container.querySelector('[data-lending-card="card-a"]') as HTMLElement;
    expect(row).toBeTruthy();
    expect(within(row).getByText('codex-demo-a.json')).toBeTruthy();
    expect(container.querySelector('[data-lending-sentence]')?.textContent).toContain('codex-demo-a.json');
  });

  it('renders the page without an outside reminder when a pool omits outside_cards', async () => {
    stubOverview(overviewBody([poolBody([cardBody({ display_name: 'Alpha' })])]));
    const { container } = renderPage();

    expect(await screen.findByText('Alpha')).toBeTruthy();
    expect(container.querySelector('[data-lending-pool="codex:pro"]')).toBeTruthy();
    expect(container.querySelector('[data-lending-card="card-a"]')).toBeTruthy();
    expect(container.querySelector('[data-lending-outside-toggle="true"]')).toBeNull();
  });
});
