import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nProvider } from '../src/i18n';
import { AccountAutomationDrawer } from '../src/components/automation/AccountAutomationDrawer';
import { api } from '../src/api/client';
import type {
  AccountAutomationAccountDTO,
  AccountAutomationCredentialDTO,
  AccountAutomationListResponse,
} from '../src/types/accountAutomation';

vi.mock('../src/api/client', () => ({ api: {
  getAccountAutomation: vi.fn(), putAccountAutomationSettings: vi.fn(), putAccountAutomationAccount: vi.fn(),
  postAccountAutomationAdopt: vi.fn(), postAccountAutomationUnadopt: vi.fn(), postAccountAutomationUnadoptMany: vi.fn(),
  postAccountAutomationCancel: vi.fn(), postAccountAutomationManualReset: vi.fn(),
} }));

const plan = (label: string, overrides: Partial<AccountAutomationAccountDTO> = {}): AccountAutomationAccountDTO => ({
  auth_index: 'synthetic-' + label, account_label: label, name: label + '.json', provider: 'codex',
  is_adopted: true, is_enabled: true, burn_credit: false, removed: false, state: 'monitoring',
  monitor_reason: 'not_exhausted', auto_disable_supported: true, disabled_by_automation: false,
  attempt_count: 0, intent_version: 1, has_weekly_evidence: true, weekly_used_percent: 20, ...overrides,
});

const credential = (label: string, overrides: Partial<AccountAutomationCredentialDTO> = {}): AccountAutomationCredentialDTO => {
  const managed = overrides.managed ?? true;
  return {
    auth_index: 'synthetic-' + label, account_label: label, name: label + '.json', provider: 'codex',
    disabled: false, controllable: true, managed, adoptable: true, auto_disable_supported: true,
    plan: managed ? plan(label) : null, ...overrides,
  };
};

const snapshot = (credentials: AccountAutomationCredentialDTO[], overrides: Partial<AccountAutomationListResponse> = {}): AccountAutomationListResponse => {
  const available = overrides.inventory?.available ?? true;
  const plans = credentials.filter(row => row.managed && row.plan).map(row => row.plan as AccountAutomationAccountDTO);
  return {
    settings: { enabled: true, web_enabled: true, source: 'web', master_from_env: false, accounts: plans.length },
    inventory: {
      available,
      credentials: available ? credentials.length : null,
      managed: available ? credentials.filter(row => row.managed).length : null,
      unmanaged: available ? credentials.filter(row => !row.managed).length : null,
      disabled: available ? credentials.filter(row => row.disabled).length : null,
      pending_absence: 0,
    },
    credentials: available ? credentials : [],
    accounts: plans,
    accounts_last_known: !available,
    candidates: credentials.map(row => ({
      auth_index: row.auth_index, account_label: row.account_label, name: row.name, provider: row.provider,
      disabled: row.disabled, controllable: row.controllable, adopted: row.managed, adoptable: row.adoptable,
      adoption_blocked_reason: row.adoption_blocked_reason, auto_disable_supported: row.auto_disable_supported,
    })),
    ...overrides,
  };
};

function mount(data: AccountAutomationListResponse) {
  vi.mocked(api.getAccountAutomation).mockResolvedValue(data);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><I18nProvider><AntdApp><AccountAutomationDrawer open onClose={() => {}} /></AntdApp></I18nProvider></QueryClientProvider>);
  return { ...view, client };
}
const row = (name: string) => screen.getByRole('article', { name });
const details = (name: string) => fireEvent.click(within(row(name)).getByRole('button', { name: /Details|详情/ }));

beforeEach(() => { vi.resetAllMocks(); localStorage.setItem('omc-lang', 'en'); });

it('renders current credentials with real artwork, accessible labels and one status per row', async () => {
  const data = snapshot([
    credential('Codex account', { provider: 'codex' }),
    credential('Claude account', { provider: 'claude', disabled: true, plan: plan('Claude account', { provider: 'claude', is_enabled: false, recovery_at_ms: Date.now() + 100000, recovery_status: 'scheduled' }) }),
    credential('Antigravity account', { provider: 'antigravity', auto_disable_supported: false, plan: plan('Antigravity account', { provider: 'antigravity', auto_disable_supported: false, monitor_reason: 'no_weekly_evidence', has_weekly_evidence: false }) }),
    credential('Missing name', { managed: false, controllable: false, adoptable: false, adoption_blocked_reason: 'credential_missing_name' }),
    credential('Unmanaged one', { managed: false, disabled: true }),
    credential('Unknown reason', { plan: plan('Unknown reason', { monitor_reason: 'future_machine_code' }) }),
  ]);
  mount(data);
  await screen.findByText('Codex account');
  for (const [name, logo] of [['Codex account', 'codex'], ['Claude account', 'claude'], ['Antigravity account', 'antigravity']]) {
    const card = row(name);
    expect(card.querySelector('img')?.getAttribute('src')?.toLowerCase()).toContain(logo + '-color.svg');
    expect(card.querySelector('strong')?.title).toBe(name);
    expect(card.querySelectorAll('.status-label')).toHaveLength(1);
    expect(within(card).getByRole('switch', { name: 'Auto-management for ' + name })).toBeDefined();
  }
  expect(row('Claude account').textContent).toContain('Recovery plan retained');
  expect(row('Claude account').textContent).toContain('Paused');
  expect(row('Antigravity account').textContent).toContain('Capability unverified');
  expect(row('Missing name').textContent).toContain('No name; cannot be addressed by CPA');
  expect(row('Unmanaged one').textContent).toContain('Unmanaged');
  expect(within(row('Unmanaged one')).getByRole('button', { name: 'Adopt Unmanaged one' })).toBeDefined();
  expect(row('Unmanaged one').textContent).not.toContain('Paused');
  expect(row('Unknown reason').textContent).toContain('Awaiting status update');
  expect(screen.queryByText('future_machine_code')).toBeNull();
  expect(screen.getByTestId('automation-stats').textContent).toContain('Managed4');
  expect(screen.getByTestId('automation-stats').textContent).toContain('Credentials6');
  fireEvent.click(screen.getByRole('button', { name: 'Unmanaged' }));
  expect(screen.getAllByRole('article')).toHaveLength(2);
  fireEvent.click(screen.getByRole('button', { name: 'Paused' }));
  expect(screen.getAllByRole('article')).toHaveLength(1);
});

it('keeps two credentials with one display label as two independent rows', async () => {
  mount(snapshot([
    credential('ops@example.test', { auth_index: 'idx-a' }),
    credential('ops@example.test', { auth_index: 'idx-b' }),
    credential('ops@example.test', { auth_index: 'idx-c', managed: false }),
  ]));
  await waitFor(() => expect(screen.getAllByRole('article', { name: 'ops@example.test' })).toHaveLength(3));
  fireEvent.click(screen.getByRole('button', { name: 'Unmanaged' }));
  expect(screen.getAllByRole('article', { name: 'ops@example.test' })).toHaveLength(1);
});

it.each(['en', 'zh-CN'])('keeps active recovery distinct from historical timestamps and localizes missing evidence (%s)', async lang => {
  localStorage.setItem('omc-lang', lang);
  mount(snapshot([
    credential('Scheduled', { disabled: true, plan: plan('Scheduled', { disabled_by_automation: true, state: 'disabled', recovery_at_ms: Date.now() + 86400000, recovery_status: 'scheduled' }) }),
    credential('Overdue', { disabled: true, plan: plan('Overdue', { disabled_by_automation: true, state: 'error', recovery_at_ms: Date.now() - 1000, recovery_status: 'overdue', next_attempt_at_ms: Date.now() + 1000 }) }),
    credential('Recovered', { plan: plan('Recovered', { recovery_at_ms: Date.now() - 1000, has_weekly_evidence: false, monitor_reason: 'no_weekly_evidence' }) }),
    credential('Monitor', { auto_disable_supported: false, plan: plan('Monitor', { auto_disable_supported: false, has_weekly_evidence: false, monitor_reason: 'no_weekly_evidence' }) }),
  ]));
  await screen.findByText('Scheduled');
  expect(row('Scheduled').textContent).toContain(lang === 'en' ? 'Recovery at:' : '预计恢复:');
  expect(row('Recovered').textContent).not.toMatch(/Recovery at:|预计恢复:|waiting to retry|等待重试/);
  details('Overdue'); details('Recovered'); details('Monitor');
  expect(row('Overdue').textContent).toContain(lang === 'en' ? 'Recovery time passed; waiting to retry' : '已过预计恢复时间，等待重试');
  expect(row('Recovered').textContent).toContain(lang === 'en' ? 'No 7d evidence (not auto-disabled)' : '未上报 7d 数据（不参与自动关）');
  expect(row('Monitor').textContent).not.toMatch(/not auto-disabled|不参与自动关/);
});

it.each(['en', 'zh-CN'])('explains identity mismatch without pointing to empty error details (%s)', async lang => {
  localStorage.setItem('omc-lang', lang);
  mount(snapshot([credential('Mismatched evidence', { plan: plan('Mismatched evidence', { state: 'monitoring', monitor_reason: 'evidence_identity_mismatch', last_error: '' }) })]));
  await screen.findByText('Mismatched evidence');
  const card = row('Mismatched evidence');
  expect(card.querySelector('.status-label')?.textContent).toBe(lang === 'en' ? 'Error' : '异常');
  expect(card.textContent).toContain(lang === 'en' ? 'Identity mismatch, auto-action paused' : '凭据与观测身份不一致，暂停自动操作');
  expect(card.textContent).not.toMatch(/see details|查看详情/);
});

it('uses friendly labels for prefixed and standalone auth indexes and adopts an unmanaged credential', async () => {
  const credentials = [
    credential('prefixed', { auth_index: 'aaaaaaaaaaaaaaaa', account_label: 'aaaaaaaaaaaaaaaa: Friendly Claude', name: 'aaaaaaaaaaaaaaaa', managed: false }),
    credential('standalone', { auth_index: 'bbbbbbbbbbbbbbbb', account_label: 'bbbbbbbbbbbbbbbb', name: 'Friendly Codex', managed: false }),
  ];
  mount(snapshot(credentials));
  await screen.findByRole('article', { name: 'Friendly Claude' });
  expect(row('Friendly Codex')).toBeDefined();
  vi.mocked(api.postAccountAutomationAdopt).mockResolvedValue({ adopted: 1 });
  fireEvent.click(within(row('Friendly Claude')).getByRole('button', { name: 'Adopt Friendly Claude' }));
  await waitFor(() => expect(api.postAccountAutomationAdopt).toHaveBeenCalledWith({ auth_indexes: ['aaaaaaaaaaaaaaaa'], enable: true }));
  fireEvent.click(screen.getByRole('button', { name: 'Batch adopt' }));
  expect(screen.getByRole('checkbox', { name: 'Friendly Claude' })).toBeDefined();
  expect(screen.getByRole('checkbox', { name: 'Friendly Codex' })).toBeDefined();
  for (const item of credentials) expect(document.body.textContent).not.toContain(item.auth_index);
});

it('Credit sends only burn_credit with the lifecycle version, retains recovery, and locks repeated mutation until refreshed', async () => {
  let data = snapshot([credential('Recovering', { disabled: true, plan: plan('Recovering', { state: 'disabled', disabled_by_automation: true, recovery_at_ms: Date.now() + 86400000, recovery_status: 'scheduled' }) })]);
  mount(data);
  let release!: (value: AccountAutomationAccountDTO) => void;
  vi.mocked(api.putAccountAutomationAccount).mockImplementation(() => new Promise(resolve => { release = resolve; }));
  await screen.findByText('Recovering');
  const credit = within(row('Recovering')).getByRole('button', { name: 'Credit exemption for Recovering' });
  fireEvent.click(credit); fireEvent.click(credit);
  await waitFor(() => expect(api.putAccountAutomationAccount).toHaveBeenCalledTimes(1));
  expect(api.putAccountAutomationAccount).toHaveBeenCalledWith('synthetic-Recovering', { burn_credit: true, expected_intent_version: 1 });
  data = snapshot([credential('Recovering', { disabled: true, plan: { ...data.accounts[0], burn_credit: true } })]);
  vi.mocked(api.getAccountAutomation).mockResolvedValue(data);
  release(data.accounts[0]);
  await waitFor(() => expect(credit.getAttribute('aria-pressed')).toBe('true'));
  expect(row('Recovering').textContent).toContain('Auto-disabled');
  expect(screen.getByTestId('automation-stats').textContent).toContain('Gateway disabled1');
  expect(row('Recovering').textContent).toContain('Recovery at:');
  expect(credit.textContent).toBe('Credit');
});

it('single and batch unadopt remove roster membership without a gateway toggle', async () => {
  let data = snapshot([credential('Single', { plan: plan('Single', { is_enabled: false, disabled_by_automation: true }) }), credential('Batch')]);
  mount(data);
  vi.mocked(api.postAccountAutomationUnadopt).mockImplementation(async () => {
    const removed = { ...data.accounts[0], is_adopted: false, removed: true };
    // After unadoption the credential is still a current row, now unmanaged.
    data = snapshot([credential('Single', { managed: false }), credential('Batch')]);
    vi.mocked(api.getAccountAutomation).mockResolvedValue(data);
    return removed;
  });
  vi.mocked(api.postAccountAutomationUnadoptMany).mockResolvedValue({ removed: 1 });
  await screen.findByText('Single'); details('Single');
  fireEvent.click(within(row('Single')).getByRole('button', { name: 'Unadopt' }));
  expect(await screen.findByText(/Only removes roster membership/)).toBeDefined();
  fireEvent.click(screen.getByRole('button', { name: 'Confirm Unadopt' }));
  await waitFor(() => expect(within(row('Single')).getByRole('button', { name: 'Adopt Single' })).toBeDefined());
  expect(api.postAccountAutomationUnadopt).toHaveBeenCalledWith('synthetic-Single');
  fireEvent.click(screen.getByRole('button', { name: 'Batch unadopt' }));
  expect(screen.queryByRole('checkbox', { name: /immediately/ })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Select all' }));
  fireEvent.click(within(screen.getByRole('radio', { name: 'Unadopt' }).closest('.ant-modal') as HTMLElement).getByRole('button', { name: 'Confirm Unadopt' }));
  await waitFor(() => expect(api.postAccountAutomationUnadoptMany).toHaveBeenCalledWith({ auth_indexes: ['synthetic-Batch'] }));
  expect(api.putAccountAutomationAccount).not.toHaveBeenCalled();
});

it('pausing and resuming use the automation switch while keeping the retained recovery visible', async () => {
  let data = snapshot([credential('Paused plan', { plan: plan('Paused plan', { state: 'disabled', disabled_by_automation: true, recovery_at_ms: Date.now() + 86400000, recovery_status: 'scheduled' }) })]);
  mount(data);
  vi.mocked(api.putAccountAutomationAccount).mockImplementation(async (_, patch) => {
    const updated = { ...data.accounts[0], is_enabled: patch.enabled! };
    data = snapshot([credential('Paused plan', { plan: updated })]);
    vi.mocked(api.getAccountAutomation).mockResolvedValue(data);
    return updated;
  });
  await screen.findByText('Paused plan');
  const control = within(row('Paused plan')).getByRole('switch', { name: 'Auto-management for Paused plan' });
  fireEvent.click(control);
  await waitFor(() => expect(control.getAttribute('aria-checked')).toBe('false'));
  expect(row('Paused plan').textContent).toContain('Recovery plan retained');
  await waitFor(() => expect((control as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(control);
  await waitFor(() => expect(control.getAttribute('aria-checked')).toBe('true'));
  expect(api.putAccountAutomationAccount).toHaveBeenNthCalledWith(1, 'synthetic-Paused plan', { enabled: false, expected_intent_version: 1 });
  expect(api.putAccountAutomationAccount).toHaveBeenNthCalledWith(2, 'synthetic-Paused plan', { enabled: true, expected_intent_version: 1 });
  expect(row('Paused plan').textContent).toContain('Recovery at:');
});

it('a failed refresh preserves the last rows but blocks mutation until a successful refresh', async () => {
  const data = snapshot([credential('Last good')]);
  const { client } = mount(data);
  await screen.findByText('Last good');
  vi.mocked(api.getAccountAutomation).mockRejectedValue(new Error('Synthetic unavailable'));
  await client.invalidateQueries({ queryKey: ['account-automation'] });
  await screen.findByText('Could not refresh automation status');
  expect(row('Last good')).toBeDefined();
  expect((within(row('Last good')).getByRole('switch') as HTMLButtonElement).disabled).toBe(true);
  vi.mocked(api.getAccountAutomation).mockResolvedValue(data);
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
  await waitFor(() => expect((within(row('Last good')).getByRole('switch') as HTMLButtonElement).disabled).toBe(false));
  expect(screen.queryByText('Could not refresh automation status')).toBeNull();
});

it('an unavailable inventory shows protection and last-known plans instead of an empty gateway', async () => {
  const data = snapshot([credential('Last known')], {
    inventory: { available: false, error: 'credential list unavailable', credentials: null, managed: null, unmanaged: null, disabled: null },
    accounts_last_known: true,
  });
  mount(data);
  expect((await screen.findAllByText('Credential inventory unavailable')).length).toBeGreaterThan(0);
  expect(screen.queryAllByRole('article')).toHaveLength(0);
  const outageStats = screen.getByTestId('automation-stats');
  for (const label of ['Credentials', 'Managed', 'Unmanaged', 'Gateway disabled', 'Paused', 'Pending absence']) {
    expect(within(outageStats).getByText(label).nextSibling?.textContent).toBe('—');
  }
  expect(screen.getByText('Last known plans: 1 (not current state)')).toBeDefined();
  expect((screen.getByRole('button', { name: 'Batch adopt' }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole('button', { name: 'Batch unadopt' }) as HTMLButtonElement).disabled).toBe(true);
  expect(document.body.textContent).not.toMatch(/No matching accounts|暂无匹配账号/);
  // A refresh that reports a complete empty inventory is an empty gateway, explicitly.
  vi.mocked(api.getAccountAutomation).mockResolvedValue(snapshot([]));
  fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
  await screen.findByText('No matching accounts');
  expect(screen.getByTestId('automation-stats').textContent).toContain('Credentials0');
});

it('batch adoption selects only eligible unmanaged credentials, retains default-on and optional notes', async () => {
  const data = snapshot([
    credential('a', { auth_index: 'a', account_label: 'a: Verified', provider: 'claude', managed: false, disabled: true }),
    credential('b', { auth_index: 'b', account_label: 'Monitor', provider: 'antigravity', managed: false, auto_disable_supported: false }),
    credential('c', { auth_index: 'c', account_label: 'Blocked', managed: false, controllable: false, adoptable: false, adoption_blocked_reason: 'credential_missing_name' }),
  ]);
  mount(data);
  vi.mocked(api.postAccountAutomationAdopt).mockResolvedValue({ adopted: 2 });
  await waitFor(() => expect((screen.getByRole('button', { name: 'Batch adopt' }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole('button', { name: 'Batch adopt' }));
  expect((screen.getByRole('button', { name: 'Confirm Adoption' }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole('checkbox', { name: /immediately/ }) as HTMLInputElement).checked).toBe(true);
  expect((screen.getByRole('checkbox', { name: 'Blocked' }) as HTMLInputElement).disabled).toBe(true);
  expect(screen.getByText(/Capability unverified; monitor-only after adoption/)).toBeDefined();
  fireEvent.click(screen.getByRole('button', { name: 'Select all' }));
  fireEvent.change(screen.getByRole('textbox', { name: /Batch note/ }), { target: { value: '  synthetic batch  ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Confirm Adoption' }));
  await waitFor(() => expect(api.postAccountAutomationAdopt).toHaveBeenCalledWith({ auth_indexes: ['a', 'b'], enable: true, note: 'synthetic batch' }));
});

it('refresh prunes stale selection, keeps cleanup silent, and refuses adoption while unavailable', async () => {
  const data = snapshot([credential('Absent', { managed: false })]);
  const { client } = mount(data);
  await screen.findByText('Absent');
  fireEvent.click(screen.getByRole('button', { name: 'Batch adopt' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Absent' }));
  const refreshed = snapshot([]);
  vi.mocked(api.getAccountAutomation).mockResolvedValue(refreshed);
  await client.invalidateQueries({ queryKey: ['account-automation'] });
  await waitFor(() => expect(screen.queryByRole('article', { name: 'Absent' })).toBeNull());
  expect(screen.getByText('0 accounts selected')).toBeDefined();
  expect((screen.getByRole('button', { name: 'Confirm Adoption' }) as HTMLButtonElement).disabled).toBe(true);
  expect(document.querySelector('.omc-toast')).toBeNull();
  expect(document.body.textContent).not.toMatch(/absent from gateway|cleaned|清理/);
  vi.mocked(api.getAccountAutomation).mockResolvedValue(snapshot([credential('Absent', { managed: false })], {
    inventory: { available: false, credentials: null, managed: null, unmanaged: null, disabled: null },
  }));
  await client.invalidateQueries({ queryKey: ['account-automation'] });
  await waitFor(() => expect((screen.getByRole('button', { name: 'Batch adopt' }) as HTMLButtonElement).disabled).toBe(true));
  expect(screen.getByTestId('automation-stats').textContent).toContain('Credentials—');
});

it('details retain retry cancellation and future manual-reset submission', async () => {
  const data = snapshot([credential('Retry', { plan: plan('Retry', { state: 'error', next_attempt_at_ms: Date.now() + 1000 }) }), credential('Reset', { plan: plan('Reset', { monitor_reason: 'missing_reset' }) })]);
  mount(data);
  vi.mocked(api.postAccountAutomationCancel).mockResolvedValue(data.accounts[0]);
  vi.mocked(api.postAccountAutomationManualReset).mockResolvedValue(data.accounts[1]);
  await screen.findByText('Retry'); details('Retry'); details('Reset');
  fireEvent.click(screen.getByRole('button', { name: 'Cancel Retry' }));
  fireEvent.click(await screen.findByRole('button', { name: 'OK' }));
  await waitFor(() => expect(api.postAccountAutomationCancel).toHaveBeenCalledWith('synthetic-Retry'));
  await waitFor(() => expect((screen.getByRole('button', { name: 'Set Manual Reset' }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole('button', { name: 'Set Manual Reset' }));
  const field = screen.getByLabelText(/Recovery time/);
  fireEvent.change(field, { target: { value: '2000-01-01T10:00' } });
  expect(screen.getByText('Recovery time must be in the future')).toBeDefined();
  fireEvent.change(field, { target: { value: '2099-01-01T10:00' } });
  fireEvent.click(within(field.closest('.ant-modal') as HTMLElement).getByRole('button', { name: 'OK' }));
  await waitFor(() => expect(api.postAccountAutomationManualReset).toHaveBeenCalledWith('synthetic-Reset', { reset_at_ms: Date.UTC(2099, 0, 1, 10) }));
});
