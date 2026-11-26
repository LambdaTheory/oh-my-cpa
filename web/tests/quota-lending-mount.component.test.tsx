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
  OAUTH_MANAGEMENT_PATH,
  OAUTH_OPEN_CREDENTIAL_PARAM,
  QUOTA_LENDING_NAV_LABEL_KEY,
  QUOTA_LENDING_PATH,
  hasPreviousHistoryEntry,
  oauthPrioritySettingsPath,
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

function pool(cards: LendingCard[], overrides: Partial<LendingPool> = {}): LendingPool {
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
    ...overrides,
  };
}

function overview(pools: LendingPool[]): LendingOverview {
  return { generated_at_ms: NOW, pools, unpooled_count: 0, unpooled: [] };
}

/** A pool with exactly one card to change. */
const oneChangePool = (id = 'codex:pro') => pool([
  card({ auth_index: 'a', name: 'synthetic-primary', tier: 1, priority: 0, suggested_priority: 0, needs_change: false }),
  card({
    auth_index: 'b', name: 'synthetic-secondary', tier: 2, priority: 0, suggested_priority: -10, needs_change: true,
  }),
], { id });

/** A pool whose numbers already match. */
const keepPool = () => pool([
  card({ auth_index: 'a', name: 'synthetic-primary', tier: 1, priority: 10, priority_is_default: false, suggested_priority: 10 }),
  card({ auth_index: 'b', name: 'synthetic-secondary', tier: 2, priority: 0, suggested_priority: 0 }),
]);

/** A pool that produced no ranking at all. */
const noAdvicePool = () => pool([
  card({
    auth_index: 'a', name: 'synthetic-primary', role: 'hold', tier: undefined,
    priority: undefined, priority_known: false, priority_is_default: false,
    suggested_priority: undefined, needs_change: undefined,
    primary_reason: 'reading_failed', reason_codes: ['reading_failed'],
  }),
], { state: 'no_advice', needs_change_count: 0, hold_count: 1 });

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
    card({ auth_index: 'pool-primary', name: 'synthetic-primary', tier: 1 }),
    card({
      auth_index: 'pool-secondary', name: 'synthetic-secondary', tier: 2,
      suggested_priority: -10, needs_change: true,
    }),
  ]),
  unpooled_reason_codes: [],
});

function renderEntry(lending?: LendingOverview | null) {
  return render(
    <MemoryRouter>
      <I18nProvider><LendingAdviceEntryLink lending={lending} /></I18nProvider>
    </MemoryRouter>,
  );
}

beforeEach(() => window.localStorage.setItem('omc-lang', 'en'));

describe('quota lending mount module', () => {
  it('keeps the path, the menu label, the settings address and the history guard in one module', () => {
    expect(QUOTA_LENDING_PATH).toBe('/quota-lending');
    expect(QUOTA_LENDING_NAV_LABEL_KEY).toBe('nav.quota_lending');
    expect(OAUTH_MANAGEMENT_PATH).toBe('/oauth-management');
    expect(OAUTH_OPEN_CREDENTIAL_PARAM).toBe('open');
    expect(oauthPrioritySettingsPath('auth-2')).toBe('/oauth-management?open=auth-2');
    expect(oauthPrioritySettingsPath('a/b c')).toBe('/oauth-management?open=a%2Fb%20c');
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

  it('speaks the three entry states from the backend counts', () => {
    const change = renderEntry(overview([oneChangePool()]));
    const changeLink = screen.getByRole('link');
    expect(changeLink.getAttribute('href')).toBe(QUOTA_LENDING_PATH);
    expect(changeLink.getAttribute('data-lending-entry')).toBe('change');
    expect(changeLink.getAttribute('data-lending-entry-count')).toBe('1');
    expect(changeLink.textContent).toContain('Quota lending · 1 to change');
    expect(changeLink.querySelector('span[aria-hidden="true"]')).toBeTruthy();
    change.unmount();

    const keep = renderEntry(overview([keepPool()]));
    const keepLink = screen.getByRole('link');
    expect(keepLink.getAttribute('data-lending-entry')).toBe('keep');
    expect(keepLink.getAttribute('data-lending-entry-count')).toBe('0');
    expect(keepLink.textContent).toContain('Quota lending · no change');
    expect(keepLink.querySelector('span[aria-hidden="true"]')).toBeNull();
    keep.unmount();

    const plain = renderEntry(overview([noAdvicePool()]));
    const plainLink = screen.getByRole('link');
    expect(plainLink.getAttribute('data-lending-entry')).toBe('plain');
    expect(plainLink.textContent).toContain('Quota lending');
    expect(plainLink.textContent).not.toContain('to change');
    plain.unmount();

    const absent = renderEntry(null);
    expect(screen.queryByRole('link')).toBeNull();
    absent.unmount();
    renderEntry(undefined);
    expect(screen.queryByRole('link')).toBeNull();
  });

  it('sums the change counts of every pool and pluralizes above one', () => {
    const twoPools = renderEntry(overview([
      oneChangePool('codex:pro'),
      oneChangePool('codex:plus'),
    ]));
    expect(screen.getByRole('link').textContent).toContain('Quota lending · 2 to change');
    expect(screen.getByRole('link').getAttribute('data-lending-entry-count')).toBe('2');
    twoPools.unmount();
  });

  it('sends the credential detail link to the page and hides it without lending', () => {
    const withLending = render(
      <MemoryRouter>
        <I18nProvider>
          <CredentialQuotaBody item={quotaItem()} nowMS={NOW} isDemo={false} lending={detail()} />
        </I18nProvider>
      </MemoryRouter>,
    );
    const link = document.querySelector('[data-lending-detail-link="true"]') as HTMLAnchorElement;
    expect(link.getAttribute('href')).toBe(QUOTA_LENDING_PATH);
    expect(link.textContent).toBe('View the whole group on the lending page');
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
