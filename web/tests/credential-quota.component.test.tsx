import React from 'react';
import { render, screen, fireEvent, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { I18nProvider } from '../src/i18n';
import {
  CredentialQuotaPanel,
  type CredentialQuotaPanelProps,
} from '../src/components/dashboard/CredentialQuota';
import { buildCredentialQuotaBoard, credentialQuotaRefreshTargets } from '../src/components/dashboard/credentialQuotaLogic';
import { CredentialQuotaBody } from '../src/pages/quota/CredentialQuotaBody';
import { CompactQuotaView } from '../src/pages/quota/CompactQuotaView';
import { OAuthCredentialRecord } from '../src/pages/oauthManagement/OAuthCredentialRecord';
import { buildOAuthWorkspaceProjection, countWorkspaceQuotaStatuses } from '../src/pages/oauthManagement/oauthWorkspaceLogic';
import { quotaFreshnessMS, resolveQuotaStatus } from '../src/utils/quotaStatus';
import type { ManagementAuthFile } from '../src/types/managementAuthFile';
import type { QuotaItem, QuotaWindow } from '../src/types/quota';

const NOW_MS = Date.parse('2026-10-10T08:00:00Z');
const HOUR_MS = 3_600_000;

function file(name: string, overrides: Partial<ManagementAuthFile> = {}): ManagementAuthFile {
  return { name, auth_index: name, type: 'codex', disabled: false, unavailable: false, runtime_only: false, success: 0, failed: 0, ...overrides };
}

function quotaWindow(kind: 'five_hour' | 'weekly', remaining: number, resetInHours: number): QuotaWindow {
  return { id: kind, label: kind, kind, scope: 'standard', remaining_percent: remaining, reset_at_ms: NOW_MS + resetInHours * HOUR_MS, reset_accuracy: 'exact' };
}

function quota(authIndex: string, windows: QuotaWindow[], overrides: Partial<QuotaItem> = {}): QuotaItem {
  return {
    auth_index: authIndex,
    name: authIndex,
    type: 'codex',
    provider: 'codex',
    disabled: false,
    status: 'healthy',
    observed_at_ms: NOW_MS,
    windows,
    recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
    capabilities: { refresh_supported: true, clear_cooldown_supported: true, reset_credit_supported: false },
    quota_exceeded: false,
    ...overrides,
  };
}

const FILES: ManagementAuthFile[] = [
  file('ample', { email: 'ample@example.com' }),
  file('low'),
  file('cooling'),
  file('expired'),
  file('unread'),
  file('off', { disabled: true }),
  file('configured-key', { runtime_only: true }),
];

const QUOTAS: QuotaItem[] = [
  quota('ample', [quotaWindow('five_hour', 92, 3), quotaWindow('weekly', 80, 100)]),
  quota('low', [quotaWindow('weekly', 12, 30), quotaWindow('five_hour', 64, 2)]),
  quota('cooling', [quotaWindow('five_hour', 40, 2)], { status: 'cooldown', active_cooldown: { is_active: true, recover_at_ms: NOW_MS + 25 * 60_000 } }),
  quota('expired', [], { status: 'error', recommendation: { status: 'needs_reauth', priority: 'critical', action: 'reauth', reason: 'token revoked' } }),
];

function QuotaSurface(props: Partial<CredentialQuotaPanelProps>) {
  return <React.StrictMode><MemoryRouter basename="/omc" initialEntries={['/omc/dashboard']}>
    <I18nProvider><CredentialQuotaPanel nowMS={NOW_MS} {...props} /></I18nProvider>
  </MemoryRouter></React.StrictMode>;
}

const panelState = () => screen.getByTestId('dashboard-credential-quota').getAttribute('data-quota-panel-state');

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  window.localStorage.setItem('omc-lang', 'en');
});

describe('credential quota board', () => {
  it('renders payment cooldown and account reauthentication independently after reload', () => {
    const payment = quota('synthetic', [], {
      provider: 'antigravity', status: 'cooldown', error: 'HTTP 403',
      active_cooldown: { is_active: true, reason: 'payment_required', recover_at_ms: NOW_MS + HOUR_MS },
      recommendation: { status: 'cooldown', priority: 'high', action: 'clear_cooldown', reason: 'payment_required' },
      last_refresh: { outcome: 'failed', attempted_at_ms: NOW_MS, http_status: 403, reason: 'http' },
    });
    const entry = file('synthetic', { type: 'antigravity', status: 'active' });
    expect(buildCredentialQuotaBoard([entry], [payment], NOW_MS).rows[0].state).toBe('cooldown');
    const { rerender } = render(<I18nProvider><CredentialQuotaBody item={payment} nowMS={NOW_MS} isDemo={false} /></I18nProvider>);
    expect(screen.queryByText('Reauthenticate')).toBeNull();
    const rejected = { ...payment, status: 'error' as const, recommendation: { status: 'needs_reauth' as const, priority: 'critical' as const, action: 'reauth' as const, reason: 'authentication failed' } };
    expect(buildCredentialQuotaBoard([entry], [rejected], NOW_MS).rows[0].state).toBe('reauth');
    rerender(<I18nProvider><CredentialQuotaBody item={rejected} nowMS={NOW_MS} isDemo={false} /></I18nProvider>);
    expect(screen.getByText('Reauthenticate')).toBeTruthy();
  });

  it.each([{ unavailable: true }, { status: 'error' }])('excludes CPA file problems from normal counts: %j', (problem) => {
    const entry = file('unavailable', problem);
    const item = quota(entry.name, [quotaWindow('five_hour', 80, 3)]);
    const record = buildOAuthWorkspaceProjection([entry], [item], [], [], [], NOW_MS).records[0];
    expect(countWorkspaceQuotaStatuses([record], NOW_MS)).toEqual({ error: 1 });
    render(<I18nProvider><OAuthCredentialRecord file={entry} displayProvider="codex" identityKey="unavailable" selected={false} busy={false} canTargetFile={false} onSelect={vi.fn()} onToggle={vi.fn()} onDownload={vi.fn()} onDelete={vi.fn()} onEdit={vi.fn()} onShowModels={vi.fn()} onShowDetails={vi.fn()} /></I18nProvider>);
    expect(screen.getByText('Problems')).toBeTruthy();
  });

  it('keeps serving credentials and their retained windows when scheduling leaves readings stale', () => {
    const items = [
      quota('stale', [quotaWindow('five_hour', 80, 3)], { observed_at_ms: NOW_MS - 12 * HOUR_MS }),
      quota('failed', [quotaWindow('five_hour', 70, 3)], { last_refresh: { outcome: 'failed', attempted_at_ms: NOW_MS, reason: 'timeout' } }),
      quota('limited', [quotaWindow('five_hour', 60, 3)], { last_refresh: { outcome: 'rate_limited', attempted_at_ms: NOW_MS } }),
    ];
    const files = items.map((item) => file(item.auth_index));
    expect(buildCredentialQuotaBoard(files, items, NOW_MS)).toMatchObject({ servingCount: 3, blockedCount: 0 });
    const { container } = render(<QuotaSurface files={files} quotas={items} />);
    expect(screen.getAllByRole('meter').map((meter) => meter.getAttribute('aria-valuenow'))).toEqual(['60', '70', '80']);
    expect(screen.getByText('Data stale')).toBeTruthy();
    expect(screen.getByText(/Refresh failed.*Request timed out/)).toBeTruthy();
    expect(screen.getByText('Rate limited')).toBeTruthy();
    expect(container.querySelectorAll('[data-quota-reading-trusted="false"]')).toHaveLength(3);
    expect(buildCredentialQuotaBoard(files.map((entry) => ({ ...entry, unavailable: true })), items, NOW_MS).servingCount).toBe(0);
  });

  it('shows a credential-specific non-HTTP failure in its row and tooltip', async () => {
    const item = quota('timeout', [], { last_refresh: { outcome: 'failed', attempted_at_ms: NOW_MS, reason: 'timeout' } });
    render(<I18nProvider><CompactQuotaView item={item} nowMS={NOW_MS} /></I18nProvider>);
    const tag = screen.getByText('Refresh failed · Request timed out');
    fireEvent.mouseOver(tag);
    expect((await screen.findByRole('tooltip')).textContent).toBe('Request timed out');
  });
  it('shares latest-attempt and freshness status between rows, counts and dashboard', () => {
    const oldMS = NOW_MS - 12 * HOUR_MS;
    const items = [
      quota('normal', [quotaWindow('five_hour', 80, 3)]),
      quota('failed', [quotaWindow('five_hour', 80, 3)], { observed_at_ms: oldMS, last_success_at_ms: oldMS, last_refresh: { outcome: 'failed', attempted_at_ms: NOW_MS, http_status: 483 } }),
      quota('limited', [], { last_refresh: { outcome: 'rate_limited', attempted_at_ms: NOW_MS, retry_at_ms: NOW_MS + 120_000 } }),
      quota('stale', [quotaWindow('weekly', 80, 100)], { observed_at_ms: oldMS }),
      quota('disabled', [], { disabled: true }),
    ];
    const files = items.map((item) => file(item.auth_index, { disabled: item.disabled }));
    const records = buildOAuthWorkspaceProjection(files, items, [], [], [], NOW_MS).records;
    const counts = countWorkspaceQuotaStatuses(records, NOW_MS);
    expect(counts).toEqual({ healthy: 1, refresh_failed: 1, rate_limited: 1, stale: 1, disabled: 1 });
    expect(Object.values(counts).reduce((sum, n) => sum + n, 0)).toBe(files.length);
    expect(buildCredentialQuotaBoard(files, items, NOW_MS).rows.map((row) => [row.fileName, row.state, row.readingStatus])).toEqual([
      ['failed', 'healthy', 'refresh_failed'],
      ['normal', 'healthy', undefined],
      ['stale', 'healthy', 'stale'],
      ['limited', 'unread', 'rate_limited'],
    ]);
    const { container } = render(<I18nProvider>{items.map((item) => <CompactQuotaView key={item.auth_index} item={item} nowMS={NOW_MS} />)}</I18nProvider>);
    const failed = within(container.querySelector('[data-quota-body="failed"]') as HTMLElement);
    expect(failed.getByText('Refresh failed · HTTP 483')).toBeTruthy();
    expect(failed.queryByText('Normal')).toBeNull();
    expect(failed.getByText(/HTTP 483.*Last successful reading/)).toBeTruthy();
    expect(screen.getByText('Rate limited')).toBeTruthy();
    expect(screen.getByText('Data stale')).toBeTruthy();
    expect(screen.getByText(/Retry at/)).toBeTruthy();
    const boundary = { ...items[0], observed_at_ms: NOW_MS - quotaFreshnessMS(items[0]) };
    expect(resolveQuotaStatus(boundary, NOW_MS)).toBe('healthy');
    expect(resolveQuotaStatus(boundary, NOW_MS + 1)).toBe('stale');
    expect(quotaFreshnessMS({ ...items[0], provider: 'ollama' })).toBe(30 * 60_000);
  });
  it('orders credentials by what needs a decision first and only counts the disabled', () => {
    const board = buildCredentialQuotaBoard(FILES, QUOTAS, NOW_MS);
    expect(board.rows.map((row) => [row.fileName, row.state])).toEqual([
      ['expired', 'reauth'],
      ['cooling', 'cooldown'],
      ['low', 'low'],
      ['ample', 'healthy'],
      ['unread', 'unread'],
    ]);
    expect(board).toMatchObject({ servingCount: 3, blockedCount: 2, disabledCount: 1 });
    const low = board.rows.find((row) => row.fileName === 'low')!;
    expect(low.windows.map((entry) => entry.kind)).toEqual(['five_hour', 'weekly']);
    expect(low.bindingWindow?.kind).toBe('weekly');
    expect(low.remainingPercent).toBe(12);
  });

  it('recovers an exhausted credential only when every spent window has reset', () => {
    const board = buildCredentialQuotaBoard(
      [file('spent')],
      [quota('spent', [quotaWindow('five_hour', 0, 2), quotaWindow('weekly', 0, 40)], { status: 'exhausted' })],
      NOW_MS,
    );
    expect(board.rows[0]).toMatchObject({ state: 'exhausted', recoverAtMS: NOW_MS + 40 * HOUR_MS });
  });

  it.each([
    { readingStatus: 'stale', overrides: { observed_at_ms: NOW_MS - 12 * HOUR_MS }, label: 'Data stale' },
    { readingStatus: 'refresh_failed', overrides: { last_refresh: { outcome: 'failed', attempted_at_ms: NOW_MS, reason: 'timeout' } }, label: 'Refresh failed · Request timed out' },
    { readingStatus: 'rate_limited', overrides: { last_refresh: { outcome: 'rate_limited', attempted_at_ms: NOW_MS, retry_at_ms: NOW_MS + 120_000 } }, label: 'Rate limited' },
  ] satisfies { readingStatus: string; overrides: Partial<QuotaItem>; label: string }[])('keeps exhausted recovery and reading trust separate: $readingStatus', ({ readingStatus, overrides, label }) => {
    const files = [file('spent')];
    const items = [quota('spent', [quotaWindow('five_hour', 0, 2), quotaWindow('weekly', 0, 40)], { status: 'exhausted', ...overrides })];
    const board = buildCredentialQuotaBoard(files, items, NOW_MS);
    expect(board).toMatchObject({ servingCount: 0, blockedCount: 1 });
    expect(board.rows[0]).toMatchObject({ state: 'exhausted', recoverAtMS: NOW_MS + 40 * HOUR_MS, readingStatus });
    const { container } = render(<QuotaSurface files={files} quotas={items} />);
    const row = screen.getByRole('listitem');
    expect(row.getAttribute('data-quota-state')).toBe('exhausted');
    expect(row.textContent).toContain('40h left');
    expect(within(row).getByText(label)).toBeTruthy();
    expect(container.querySelector('[data-quota-reading-trusted="false"]')).toBeTruthy();
  });

  it.each([
    { outcome: 'failed', label: 'Refresh failed · Request timed out' },
    { outcome: 'rate_limited', label: 'Rate limited' },
  ] as const)('shows reading trust in the dashboard before a successful reading: $outcome', ({ outcome, label }) => {
    const item = quota('unread', [], { last_refresh: { outcome, attempted_at_ms: NOW_MS, reason: 'timeout' } });
    render(<QuotaSurface files={[file('unread')]} quotas={[item]} />);
    const row = screen.getByRole('listitem');
    expect(row.getAttribute('data-quota-state')).toBe('unread');
    expect(within(row).getByText(label)).toBeTruthy();
  });

  it('asks a live refresh only of enabled credentials a provider can be read for', () => {
    const quotas = [
      ...QUOTAS,
      quota('off', []),
      quota('unread', [], { capabilities: { refresh_supported: false, clear_cooldown_supported: false, reset_credit_supported: false } }),
    ];
    expect(credentialQuotaRefreshTargets(FILES, quotas)).toEqual(['ample', 'low', 'cooling', 'expired']);
    expect(credentialQuotaRefreshTargets([file('twin'), file('twin')], [quota('twin', [])])).toEqual([]);
  });
});

describe('credential quota presentation', () => {
  it('announces first loading without claiming an empty fleet', () => {
    render(<QuotaSurface isLoading />);
    expect(panelState()).toBe('loading');
    expect(screen.getByRole('status').getAttribute('aria-busy')).toBe('true');
    expect(screen.queryByText('No credential connected yet.')).toBeNull();
  });

  it('distinguishes an unread credential list from a deployment that holds none', () => {
    const retry = vi.fn();
    const { rerender } = render(<QuotaSurface isError error={new Error('read failed')} onRetry={retry} />);
    expect(panelState()).toBe('unknown');
    expect(screen.getByRole('alert').textContent).toContain('Failed to load credential quota');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledTimes(1);

    rerender(<QuotaSurface files={[]} quotas={[]} />);
    expect(panelState()).toBe('empty');
    expect(screen.getByText('No credential connected yet.')).toBeTruthy();
    expect(screen.getByRole('link').getAttribute('href')).toBe('/omc/oauth-management?action=connect');
  });

  it('draws each credential with its windows or the reason it cannot serve', () => {
    render(<QuotaSurface files={FILES} quotas={QUOTAS} />);
    expect(panelState()).toBe('ready');
    const rows = screen.getAllByRole('listitem');
    expect(rows).toHaveLength(5);

    expect(within(rows[0]).getByText('Sign-in required')).toBeTruthy();
    expect(within(rows[1]).getByText('Cooling down')).toBeTruthy();
    expect(rows[1].textContent).toContain('25m left');
    expect(within(rows[1]).queryByRole('meter')).toBeNull();

    const meters = within(rows[2]).getAllByRole('meter');
    expect(meters.map((meter) => meter.getAttribute('aria-valuenow'))).toEqual(['64', '12']);
    expect(rows[2].textContent).toContain('30h left');
    expect(within(rows[3]).getByText('ample@example.com')).toBeTruthy();
    expect(within(rows[4]).getByText('No quota reading')).toBeTruthy();

    // No row is interactive, and the footer is the panel's one way out.
    expect(screen.queryByRole('button')).toBeNull();
    const links = screen.getAllByRole('link');
    expect(links).toHaveLength(1);
    expect(links[0].getAttribute('href')).toBe('/omc/oauth-management');
    expect(links[0].closest('li')).toBeNull();
  });

  it('lists a large fleet in full, most urgent first, for the panel to scroll', () => {
    const files = Array.from({ length: 40 }, (_, index) => file(`credential-${index}`));
    const quotas = files.map((entry, index) => quota(entry.name, [quotaWindow('weekly', 100 - index, 24)]));
    render(<QuotaSurface files={files} quotas={quotas} />);
    const rows = screen.getAllByRole('listitem');
    expect(rows).toHaveLength(40);
    expect(within(rows[0]).getByText('credential-39')).toBeTruthy();
    expect(within(rows[39]).getByText('credential-0')).toBeTruthy();
  });

  it('keeps the rows through a failed refresh and reports it additively', () => {
    const retry = vi.fn();
    const { rerender } = render(<QuotaSurface files={FILES} quotas={QUOTAS} isError onRetry={retry} />);
    expect(panelState()).toBe('ready');
    expect(screen.getAllByRole('listitem')).toHaveLength(5);
    expect(screen.getByRole('alert').classList.contains('is-warning')).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledTimes(1);

    rerender(<QuotaSurface files={FILES} quotas={QUOTAS} onRetry={retry} />);
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('offers one control, which asks for fresh readings', () => {
    const refresh = vi.fn();
    const { rerender } = render(<QuotaSurface files={FILES} quotas={QUOTAS} onRefreshQuota={refresh} />);
    const button = screen.getByRole('button', { name: 'Refresh quota' });
    expect(screen.getAllByRole('button')).toHaveLength(1);
    expect(button.getAttribute('aria-busy')).toBe('false');
    fireEvent.click(button);
    expect(refresh).toHaveBeenCalledTimes(1);

    rerender(<QuotaSurface files={FILES} quotas={QUOTAS} onRefreshQuota={refresh} isRefreshingQuota />);
    expect(screen.getByRole('button', { name: 'Refresh quota' }).getAttribute('aria-busy')).toBe('true');
  });

  it('still lists credentials when no quota reading is available', () => {
    render(<QuotaSurface files={[file('solo')]} />);
    expect(panelState()).toBe('ready');
    expect(screen.getByText('No quota reading')).toBeTruthy();
  });
});

describe('claude reset grants display', () => {
  const grantItem = (overrides: Partial<QuotaItem> = {}): QuotaItem =>
    quota('claude-grants', [], {
      provider: 'claude',
      reset_grants: [
        { id: 'grant-weekly', label: 'Weekly Reset', resets_total: 2, resets_left: 1, ends_at_ms: NOW_MS + 3 * 24 * 60_000, clears: ['five_hour'] },
        { id: 'grant-spent', label: 'Spent', resets_total: 1, resets_left: 0 },
      ],
      ...overrides,
    });

  it('lists grants with remaining resets and hides spent ones, without any claim action', () => {
    render(
      <MemoryRouter>
        <I18nProvider>
          <CredentialQuotaBody item={grantItem()} nowMS={NOW_MS} isDemo={false} />
        </I18nProvider>
      </MemoryRouter>,
    );
    expect(screen.getByText('Weekly Reset')).toBeTruthy();
    expect(screen.getByText(/1 of 2 left/)).toBeTruthy();
    // Upstream scope ids resolve to localized window labels, not raw ids.
    expect(screen.getByText(/5-hour limit/)).toBeTruthy();
    expect(screen.queryByText('five_hour')).toBeNull();
    expect(screen.queryByText('Spent')).toBeNull();
    // Display-only: no button offers to spend the entitlement.
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('omits the grants section when nothing remains', () => {
    render(
      <MemoryRouter>
        <I18nProvider>
          <CredentialQuotaBody
            item={grantItem({ reset_grants: [{ id: 'grant-spent', resets_total: 1, resets_left: 0 }] })}
            nowMS={NOW_MS}
            isDemo={false}
          />
        </I18nProvider>
      </MemoryRouter>,
    );
    expect(screen.queryByText(/Claude reset grants/)).toBeNull();
  });
});

describe('list row reset badge', () => {
  const DAY_MS = 24 * HOUR_MS;
  const renderRow = (item: QuotaItem) => render(
    <I18nProvider><CompactQuotaView item={item} nowMS={NOW_MS} /></I18nProvider>,
  );
  const codexRow = quota('codex-credits', [], {
    reset_credits: { available_count: 2, applicable_available_count: 2 },
  });
  const claudeRow = (grants: QuotaItem['reset_grants']) => quota('claude-grants', [], { provider: 'claude', type: 'claude', reset_grants: grants });
  const LIVE_GRANTS: QuotaItem['reset_grants'] = [
    { id: 'later', resets_total: 3, resets_left: 2, ends_at_ms: NOW_MS + 5 * DAY_MS },
    { id: 'sooner', resets_total: 1, resets_left: 1, ends_at_ms: NOW_MS + 2 * DAY_MS + 3 * HOUR_MS },
    // Spent and ending first: it must neither count nor set the earliest expiry.
    { id: 'spent', resets_total: 1, resets_left: 0, ends_at_ms: NOW_MS + HOUR_MS },
  ];

  it('shows banked Codex credits as the available count', () => {
    renderRow(codexRow);
    expect(screen.getByText(/Available 2/)).toBeTruthy();
    expect(document.querySelector('[data-reset-grants]')).toBeNull();
  });

  it('sums unspent Claude grants and names the soonest expiry of a live grant', () => {
    renderRow(claudeRow(LIVE_GRANTS));
    const badge = document.querySelector('[data-reset-grants]') as HTMLElement;
    expect(badge.textContent).toContain('Available 3');
    expect(badge.textContent).toContain('Earliest: 2d 3h left');
    expect(badge.getAttribute('title')).toContain('Claude resets currently available');
  });

  it('shows no badge when every grant is spent or none was issued', () => {
    const { unmount } = renderRow(claudeRow([{ id: 'spent', resets_total: 1, resets_left: 0, ends_at_ms: NOW_MS + DAY_MS }]));
    expect(document.querySelector('[data-reset-grants]')).toBeNull();
    expect(screen.queryByText(/Available/)).toBeNull();
    unmount();
    renderRow(quota('plain', []));
    expect(screen.queryByText(/Available/)).toBeNull();
  });

  it('reports an available reset for Codex and Claude alike, but offers redemption only for Codex', () => {
    const files = [file('codex-credits'), file('claude-grants', { type: 'claude' }), file('claude-spent', { type: 'claude' }), file('plain')];
    const records = buildOAuthWorkspaceProjection(files, [
      { ...codexRow, capabilities: { refresh_supported: true, clear_cooldown_supported: true, reset_credit_supported: true } },
      claudeRow(LIVE_GRANTS),
      { ...claudeRow([{ id: 'spent', resets_total: 1, resets_left: 0 }]), auth_index: 'claude-spent' },
      quota('plain', []),
    ], []).records;
    expect(records.map((record) => [record.fileName, record.hasAvailableReset, record.canRedeemCredit])).toEqual([
      ['codex-credits', true, true],
      ['claude-grants', true, false],
      ['claude-spent', false, false],
      ['plain', false, false],
    ]);
  });
});

describe('paid usage, balances and usage breakdown', () => {
  const renderBody = (item: QuotaItem) => render(
    <MemoryRouter>
      <I18nProvider>
        <CredentialQuotaBody item={item} nowMS={NOW_MS} isDemo={false} />
      </I18nProvider>
    </MemoryRouter>,
  );

  it('names a known credit pool, shows dollars left and states that extra usage is off', () => {
    renderBody(quota('claude-balances', [], {
      provider: 'claude',
      plan: {
        plan_type: 'max', plan_label: 'Claude Max', tier: 'elite',
        extra_usage: { is_enabled: false, monthly_limit_cents: 0, used_credits_cents: 0, user_disabled: true },
        balances: [
          { id: 'iguana_necktie', limit_usd: 50, used_usd: 15, remaining_usd: 35, reset_at_ms: NOW_MS + 24 * HOUR_MS },
          { id: 'future_pool', limit_usd: 10, used_usd: 0, remaining_usd: 10 },
        ],
      },
    }));
    expect(screen.getByText('Cloud session credits')).toBeTruthy();
    expect(screen.queryByText('iguana_necktie')).toBeNull();
    // A pool the console does not know keeps its upstream key rather than vanishing.
    expect(screen.getByText('future_pool')).toBeTruthy();
    expect(screen.getByText(/\$35\.00 left/)).toBeTruthy();
    expect(screen.getByText('Off')).toBeTruthy();
    expect(screen.getByText('Turned off by the user')).toBeTruthy();
  });

  it('shows enabled extra usage in its own currency and lists product shares as a breakdown', () => {
    renderBody(quota('xai-breakdown', [], {
      provider: 'xai',
      plan: {
        plan_type: 'paid', plan_label: 'SuperGrok', tier: 'standard',
        extra_usage: { is_enabled: true, monthly_limit_cents: 20000, used_credits_cents: 5700, currency: 'EUR' },
        usage_breakdown: [{ name: 'grok-4', share_percent: 96 }, { name: 'grok-3' }],
      },
    }));
    expect(screen.getByText('€57.00 of €200.00')).toBeTruthy();
    const breakdown = document.querySelector('[data-quota-usage-breakdown]') as HTMLElement;
    expect(within(breakdown).getByText('96%')).toBeTruthy();
    expect(within(breakdown).getByText('Unavailable')).toBeTruthy();
  });
});
