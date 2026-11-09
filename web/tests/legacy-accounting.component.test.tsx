import type { ReactNode } from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { I18nProvider, makeT } from '../src/i18n';
import { tokenAccountingMessage } from '../src/components/usage/TokenAccountingNote';
import { RequestRow } from '../src/components/usage/RequestRow';
import { UsageEventDrawer } from '../src/components/usage/UsageEventDrawer';
import { CostBreakdownView } from '../src/components/pricing/CostBreakdownView';
import { PricingEditorProvider } from '../src/components/pricing/PricingEditorContext';
import { TokenDisplayProvider } from '../src/types/tokenDisplayContext';
import type { RequestCostBreakdown } from '../src/types/pricing';
import type { TokenAccounting } from '../src/types/tokenAccounting';
import type { UsageEvent } from '../src/types/usageEvents';

/**
 * F1 keeps the original totals of an old-format event as non-null `legacy`
 * evidence, and the pricing path bills from those totals. The console must say
 * so: it is not the unresolved case that hides the price action, and the status
 * is never printed as a dictionary key.
 */

const LEGACY_ACCOUNTING: TokenAccounting = {
  version: 0,
  status: 'legacy',
  raw_tokens: {
    input_tokens: 20, output_tokens: 100, reasoning_tokens: 20, cached_tokens: 0,
    cache_read_tokens: 0, cache_read_tokens_present: false, cache_creation_tokens: 0,
    total_tokens: 120, total_tokens_present: true,
  },
};

const EVENT: UsageEvent = {
  id: 9001,
  event_key: 'legacy-event',
  request_id: 'legacy-request',
  timestamp_ms: 1_800_000_000_000,
  provider: 'openai',
  model: 'legacy-model',
  failed: false,
  generate: true,
  latency_ms: 2000,
  ttft_ms: 1000,
  tokens: { input: 20, output: 100, reasoning: 20, cached: 0, cache_read: 0, cache_creation: 0, total: 120 },
  has_request_log: false,
  cost_usd: null,
  pricing_status: 'unpriced',
  accounting_status: 'legacy',
  token_accounting: LEGACY_ACCOUNTING,
};

const UNPRICED_BREAKDOWN: RequestCostBreakdown = {
  status: 'unpriced',
  accounting_status: 'legacy',
  token_accounting: LEGACY_ACCOUNTING,
  recomputed_matches: false,
};

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false, media: query, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  }));
  window.localStorage.setItem('omc-lang', 'en');
});

function mount(children: ReactNode, detail?: { event: UsageEvent; related_errors: unknown[]; cost_breakdown?: RequestCostBreakdown }) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity, staleTime: Infinity } } });
  client.setQueryData(['preferences'], { omc_models_view: 'model', omc_request_model_icons: false });
  if (detail) client.setQueryData(['usage-event', detail.event.id], detail);
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{}', { headers: { 'Content-Type': 'application/json' } })));
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <AntdApp>
          <TokenDisplayProvider>
            <PricingEditorProvider>{children}</PricingEditorProvider>
          </TokenDisplayProvider>
        </AntdApp>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

it.each(['zh', 'en'] as const)('describes non-empty legacy evidence without printing the status key in %s', lang => {
  const t = makeT(lang);
  const message = tokenAccountingMessage(t, LEGACY_ACCOUNTING);
  expect(message).not.toContain('events.');
  expect(message).not.toMatch(lang === 'zh' ? /未计入费用估算/ : /excluded from cost estimates/);
  expect(message).toContain('20');
  expect(message).toContain('100');
  expect(message).toMatch(lang === 'zh' ? /旧版计量/ : /Legacy accounting/);
});

it('offers the price action for an unpriced request whose evidence is legacy', () => {
  const { container } = mount(
    <RequestRow event={EVENT} credentials={new Map()} onOpen={() => undefined} onToggleSelect={() => undefined} />,
  );
  expect(screen.getByTestId('request-set-price')).toBeTruthy();
  const costMissing = container.querySelector('.req-cost-none');
  expect(costMissing?.getAttribute('data-request-tooltip')).toBe(makeT('en')('events.cost_unpriced'));
  expect(document.body.textContent).not.toContain('events.accounting_status_legacy');
});

it('offers the price action and keeps the legacy explanation for an unpriced legacy breakdown', () => {
  mount(<CostBreakdownView breakdown={UNPRICED_BREAKDOWN} model="legacy-model" costUsd={null} />);
  expect(screen.getByTestId('cost-set-price')).toBeTruthy();
  expect(screen.getByText(/Legacy accounting/)).toBeTruthy();
  expect(document.body.textContent).not.toContain('events.accounting_status_legacy');
  expect(document.body.textContent).not.toMatch(/excluded from cost estimates/);
});

it('explains a priced legacy request with its preserved counts instead of an unresolved status', () => {
  mount(
    <CostBreakdownView
      breakdown={{ status: 'priced', stored_nanos: 4200, token_accounting: LEGACY_ACCOUNTING, recomputed_matches: true }}
      model="legacy-model"
      costUsd={0.0000042}
    />,
  );
  expect(screen.getByText(/Legacy accounting/)).toBeTruthy();
  expect(document.body.textContent).toContain('20');
  expect(document.body.textContent).toContain('100');
  expect(document.body.textContent).not.toContain('events.accounting_status_legacy');
  expect(document.body.textContent).not.toMatch(/excluded from cost estimates/);
});

it('shows legacy evidence and the price action in the request drawer', async () => {
  mount(
    <UsageEventDrawer eventId={EVENT.id} credentials={new Map()} onClose={() => undefined} />,
    { event: EVENT, related_errors: [], cost_breakdown: UNPRICED_BREAKDOWN },
  );
  const tab = await screen.findByRole('tab', { name: 'Performance & usage' });
  fireEvent.click(tab);
  await waitFor(() => expect(screen.getAllByText(/Legacy accounting/).length).toBeGreaterThan(0));
  expect(screen.getByTestId('cost-set-price')).toBeTruthy();
  expect(document.body.textContent).not.toContain('events.accounting_status_legacy');
  expect(document.body.textContent).not.toMatch(/excluded from cost estimates/);
});
