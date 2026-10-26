import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { DashboardProviders } from '../src/components/dashboard/DashboardProviders';
import { CredentialQuotaPanel } from '../src/components/dashboard/CredentialQuota';
import type { ManagementOverview } from '../src/types/management';
import { AccountBalance } from '../src/components/common/AccountBalance';
import { CompactQuotaView } from '../src/pages/quota/CompactQuotaView';
import { CredentialQuotaBody } from '../src/pages/quota/CredentialQuotaBody';
import type { QuotaItem } from '../src/types/quota';

const NOW_MS = Date.parse('2026-10-26T08:00:00Z');
const PREPAID: QuotaItem = {
  auth_index: 'synthetic-step', name: 'Synthetic StepFun', type: 'step', provider: 'stepfun',
  disabled: false, status: 'healthy', observed_at_ms: NOW_MS, windows: [], quota_exceeded: false,
  recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
  capabilities: { refresh_supported: true, clear_cooldown_supported: true, reset_credit_supported: false },
  plan: { plan_type: 'prepaid', plan_label: '', tier: 'unknown', account_balance: {
    account_type: 'prepaid', currency: 'CNY', balance: 75, total_cash_balance: 80, total_voucher_balance: 20, remaining_percent: 75,
  } },
};

beforeEach(() => window.localStorage.setItem('omc-lang', 'en'));

describe('StepFun account balance', () => {
  it('uses the normalized provider in the dashboard credential reading', () => {
    render(<MemoryRouter><I18nProvider><CredentialQuotaPanel nowMS={NOW_MS} quotas={[PREPAID]} files={[{
      name: PREPAID.name, auth_index: PREPAID.auth_index, type: 'openai', provider: 'step',
      disabled: false, runtime_only: false, unavailable: false, success: 0, failed: 0,
    }]} /></I18nProvider></MemoryRouter>);
    expect(screen.getByText('Balance: CNY 75.00')).toBeTruthy();
    expect(screen.getByRole('meter').closest('.quota-row-windows')).toBeTruthy();
  });

  it.each([
    { upstream: 'step', display: 'Primary account', hasBalance: true },
    { upstream: 'another-provider', display: 'step', hasBalance: false },
  ])('uses CPA identity for provider $upstream displayed as $display', ({ upstream, display, hasBalance }) => {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity, gcTime: Infinity } } });
    client.setQueryData(['preferences'], {});
    client.setQueryData(['management-providers', false], { providers: [{
      id: 'openai-compat-0', family: 'openai-compatibility', name: display, upstream_name: upstream,
      protocol: 'OpenAI', disabled: false, key_configured: true, auth_indexes: ['synthetic-step'],
    }] });
    client.setQueryData(['management-plugins'], { plugins: [] });
    client.setQueryData(['management-quota'], { quotas: [{ ...PREPAID, observed_at_ms: Date.now() }] });
    const overview: ManagementOverview = { status: 'connected', cpa_connected: true, counts: { management_keys: 1, provider_keys: 1, credentials: 1, models: 1 }, traffic: null, providers: [], credentials: null, partial_errors: [] };
    const view = render(<QueryClientProvider client={client}><MemoryRouter><I18nProvider><AntdApp>
      <DashboardProviders overview={overview} />
    </AntdApp></I18nProvider></MemoryRouter></QueryClientProvider>);
    try {
      expect(screen.getByText(display)).toBeTruthy();
      expect(Boolean(screen.queryByText('Balance: CNY 75.00'))).toBe(hasBalance);
    } finally { view.unmount(); client.clear(); }
  });

  it.each(['compact', 'detail'] as const)('shows prepaid money and an estimated share in the %s view', (view) => {
    const { container } = render(<I18nProvider>{view === 'compact'
      ? <CompactQuotaView item={PREPAID} nowMS={NOW_MS} />
      : <CredentialQuotaBody item={PREPAID} nowMS={NOW_MS} isDemo={false} />}</I18nProvider>);
    expect(screen.getByText('Balance: CNY 75.00')).toBeTruthy();
    expect(screen.getByText('Estimated remaining 75%')).toBeTruthy();
    expect(screen.getByText('Estimated from total top-ups and gifts; may differ from actual availability')).toBeTruthy();
    expect(screen.getByRole('meter').getAttribute('aria-valuenow')).toBe('75');
    expect(container.querySelector('[data-quota-window-count]')?.getAttribute('data-quota-window-count')).toBe('0');
    expect(screen.queryByText('Quota windows')).toBeNull();
  });

  it.each(['compact', 'detail'] as const)('shows postpaid debt without any percentage meter in the %s view', (view) => {
    const item: QuotaItem = { ...PREPAID, plan: { ...PREPAID.plan!, account_balance: {
      account_type: 'postpaid', currency: 'CNY', balance: -12.5, remaining_percent: 75,
    } } };
    render(<I18nProvider>{view === 'compact'
      ? <CompactQuotaView item={item} nowMS={NOW_MS} />
      : <CredentialQuotaBody item={item} nowMS={NOW_MS} isDemo={false} />}</I18nProvider>);
    expect(screen.getByText('Balance: CNY -12.50')).toBeTruthy();
    expect(screen.getByText('Postpaid · no window')).toBeTruthy();
    expect(screen.queryByRole('meter')).toBeNull();
    expect(screen.queryByText(/75%/)).toBeNull();
  });

  it.each([
    undefined,
    { ...PREPAID, plan: undefined },
    { ...PREPAID, error: 'HTTP 401' },
    { ...PREPAID, status: 'error' as const },
    { ...PREPAID, observed_at_ms: NOW_MS - 5 * 3_600_000 },
    { ...PREPAID, last_refresh: { outcome: 'failed' as const, attempted_at_ms: NOW_MS, reason: 'timeout' as const } },
  ])('shows missing data without an amount or meter after an unavailable read (%#)', (item) => {
    render(<I18nProvider><AccountBalance item={item} nowMS={NOW_MS} /></I18nProvider>);
    expect(screen.getByText('Balance: Data missing')).toBeTruthy();
    expect(screen.queryByText(/CNY/)).toBeNull();
    expect(screen.queryByRole('meter')).toBeNull();
  });

  it('preserves a measured zero without inventing a percentage when funding is unknown', () => {
    const item: QuotaItem = { ...PREPAID, plan: { ...PREPAID.plan!, account_balance: { account_type: 'prepaid', currency: 'CNY', balance: 0 } } };
    render(<I18nProvider><AccountBalance item={item} nowMS={NOW_MS} /></I18nProvider>);
    expect(screen.getByText('Balance: CNY 0.00')).toBeTruthy();
    expect(screen.queryByRole('meter')).toBeNull();
  });

  it('localizes postpaid and missing states in Simplified Chinese', () => {
    window.localStorage.setItem('omc-lang', 'zh');
    const item: QuotaItem = { ...PREPAID, plan: { ...PREPAID.plan!, account_balance: { account_type: 'postpaid', currency: 'CNY', balance: 10 } } };
    const { rerender } = render(<I18nProvider><AccountBalance item={item} nowMS={NOW_MS} /></I18nProvider>);
    expect(screen.getByText('后付费·无窗口')).toBeTruthy();
    rerender(<I18nProvider><AccountBalance nowMS={NOW_MS} /></I18nProvider>);
    expect(screen.getByText('余额: 数据缺失')).toBeTruthy();
  });
});
