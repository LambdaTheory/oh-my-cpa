import type { PropsWithChildren } from 'react';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Outlet } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppRoutes } from '../src/App';
import { I18nProvider } from '../src/i18n';
import { CredentialQuotaBody } from '../src/pages/quota/CredentialQuotaBody';
import { LendingAdviceEntryLink } from '../src/pages/quota/LendingAdviceEntryLink';
import { buildNavGroups } from '../src/components/common/navigation';
import { navEntryLabel, resolveNavLocation } from '../src/types/sidebarNav';
import {
  QUOTA_LENDING_NAV_LABEL_KEY,
  QUOTA_LENDING_PATH,
  hasPreviousHistoryEntry,
} from '../src/types/quotaLendingMount';
import type { LendingCard, LendingDetail, LendingOverview, LendingPool, QuotaItem } from '../src/types/quota';

vi.mock('../src/components/common/AuthGate', () => ({
  AuthGate: ({ children }: PropsWithChildren) => <>{children}</>,
}));
vi.mock('../src/utils/TimeZoneProvider', () => ({
  TimeZoneProvider: ({ children }: PropsWithChildren) => <>{children}</>,
  useTimeZone: () => 'UTC',
  useTimeZoneSetting: () => ['UTC', () => {}],
}));
vi.mock('../src/components/common/AppLayout', () => ({ AppLayout: () => <Outlet /> }));
vi.mock('../src/components/common/ShellLoading', () => ({ ShellLoading: () => null }));
vi.mock('../src/pages/quota/QuotaLendingPage', () => ({
  QuotaLendingPage: () => <div data-testid="quota-lending-page" />,
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

function pool(cards: LendingCard[], overrides: Partial<LendingPool> = {}): LendingPool {
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
    ...overrides,
  };
}

function overview(pools: LendingPool[]): LendingOverview {
  return { generated_at_ms: NOW, pools, unpooled_count: 0, unpooled: [] };
}

const advisedPool = () => pool([
  card({ auth_index: 'a', name: 'codex-primary', remaining_percent: 88, reset_at_ms: NOW + 4 * DAY }),
  card({ auth_index: 'b', name: 'codex-secondary', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
]);

const advised = () => overview([advisedPool()]);

const quotaItem = (): QuotaItem => ({
  auth_index: 'pool-secondary',
  name: 'pool-secondary',
  type: 'codex',
  provider: 'codex',
  disabled: false,
  status: 'healthy',
  observed_at_ms: NOW,
  windows: [],
  recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
  capabilities: { refresh_supported: true, clear_cooldown_supported: true, reset_credit_supported: false },
  quota_exceeded: false,
});

const detail = (): LendingDetail => ({
  generated_at_ms: NOW,
  pool: pool([
    card({ auth_index: 'pool-primary', name: 'codex-primary', role: 'prefer', remaining_percent: 88 }),
    card({ auth_index: 'pool-secondary', name: 'codex-secondary', role: 'avoid', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
  ]),
  unpooled_reason_codes: [],
});

function renderEntry(lending?: LendingOverview | null) {
  return render(
    <MemoryRouter>
      <I18nProvider><LendingAdviceEntryLink lending={lending} nowMS={NOW} /></I18nProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => window.localStorage.setItem('omc-lang', 'en'));

describe('quota lending mount module', () => {
  it('keeps the path, the menu label key and the history guard in one module', () => {
    expect(QUOTA_LENDING_PATH).toBe('/quota-lending');
    expect(QUOTA_LENDING_NAV_LABEL_KEY).toBe('nav.quota_lending');
    expect(hasPreviousHistoryEntry({ idx: 1 })).toBe(true);
    expect(hasPreviousHistoryEntry({ idx: 0 })).toBe(false);
    expect(hasPreviousHistoryEntry(undefined)).toBe(false);
  });

  it('resolves the lending page to its own entry under Capabilities', () => {
    window.__OMCPA_CONFIG__ = { basePath: '/omc', demo: false };
    const groups = buildNavGroups([]);
    const { entry, group } = resolveNavLocation(groups, QUOTA_LENDING_PATH);
    expect(entry?.key).toBe(QUOTA_LENDING_PATH);
    expect(entry?.labelKey).toBe(QUOTA_LENDING_NAV_LABEL_KEY);
    expect(group?.key).toBe('capabilities');
    expect(group?.labelKey).toBe('nav.group.capabilities');
    // The breadcrumb and the tab title read the same entry name.
    expect(navEntryLabel(entry!, (key) => (key === 'nav.quota_lending' ? 'Quota lending' : key))).toBe('Quota lending');
    expect(resolveNavLocation(groups, QUOTA_LENDING_PATH + '/').entry?.key).toBe(QUOTA_LENDING_PATH);
  });

  it('mounts the page from the route constant', async () => {
    window.__OMCPA_CONFIG__ = { basePath: '/omc', demo: false };
    window.history.replaceState({}, '', `/omc${QUOTA_LENDING_PATH}`);
    render(<AppRoutes />);
    expect(await screen.findByTestId('quota-lending-page')).toBeTruthy();
  });

  it('sends the three entry states to the mount path', () => {
    const advisedView = renderEntry(advised());
    const advisedLink = screen.getByRole('link');
    expect(advisedLink.getAttribute('href')).toBe(QUOTA_LENDING_PATH);
    expect(advisedLink.getAttribute('data-lending-entry')).toBe('advised');
    expect(advisedLink.textContent).toContain('Quota lending · 1 suggestion');
    expect(advisedLink.textContent).not.toContain('suggestions');
    expect(advisedLink.querySelector('span[aria-hidden="true"]')).toBeTruthy();
    advisedView.unmount();

    const balanced = renderEntry(overview([pool([
      card({ auth_index: 'a', name: 'codex-primary', remaining_percent: 21.8, reset_at_ms: NOW + 2 * DAY }),
      card({ auth_index: 'b', name: 'codex-secondary', remaining_percent: 40, reset_at_ms: NOW + 4 * DAY }),
    ], { state: 'balanced' })]));
    const balancedLink = screen.getByRole('link');
    expect(balancedLink.getAttribute('href')).toBe(QUOTA_LENDING_PATH);
    expect(balancedLink.getAttribute('data-lending-entry')).toBe('balanced');
    expect(balancedLink.querySelector('span[aria-hidden="true"]')).toBeNull();
    balanced.unmount();

    const plain = renderEntry(overview([pool([
      card({ auth_index: 'a', name: 'codex-primary' }),
      card({ auth_index: 'b', name: 'codex-secondary', role: 'limited', is_comparable: false }),
    ], { state: 'limited' })]));
    const plainLink = screen.getByRole('link');
    expect(plainLink.getAttribute('href')).toBe(QUOTA_LENDING_PATH);
    expect(plainLink.getAttribute('data-lending-entry')).toBe('plain');
    plain.unmount();

    const absent = renderEntry(null);
    expect(screen.queryByRole('link')).toBeNull();
    absent.unmount();
    renderEntry(undefined);
    expect(screen.queryByRole('link')).toBeNull();
  });

  it('pluralizes the entry count above one', () => {
    const twoPools = renderEntry(overview([
      advisedPool(),
      pool([
        card({ auth_index: 'c', name: 'codex-plus-a', remaining_percent: 90 }),
        card({ auth_index: 'd', name: 'codex-plus-b', remaining_percent: 30, reset_at_ms: NOW + 2 * DAY }),
      ], { id: 'codex:plus', plan_type: 'plus', plan_label: 'Plus' }),
    ]));
    expect(screen.getByRole('link').textContent).toContain('Quota lending · 2 suggestions');
    twoPools.unmount();
  });

  it('sends the credential detail link to the mount path and hides it without lending', () => {
    const withLending = render(
      <MemoryRouter>
        <I18nProvider>
          <CredentialQuotaBody item={quotaItem()} nowMS={NOW} isDemo={false} lending={detail()} />
        </I18nProvider>
      </MemoryRouter>,
    );
    const link = document.querySelector('[data-lending-detail-link="true"]') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe(QUOTA_LENDING_PATH);
    expect(link.textContent).toBe('View the whole pool on the lending page');
    withLending.unmount();

    render(
      <MemoryRouter>
        <I18nProvider>
          <CredentialQuotaBody item={quotaItem()} nowMS={NOW} isDemo={false} lending={null} />
        </I18nProvider>
      </MemoryRouter>,
    );
    expect(document.querySelector('[data-lending-detail-link]')).toBeNull();
    expect(document.querySelector('[data-lending-view]')).toBeNull();
  });
});
