import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nProvider } from '../src/i18n';
import { AccountAutomationDrawer } from '../src/components/automation/AccountAutomationDrawer';
import { api } from '../src/api/client';
import type { AccountAutomationAccountDTO, AccountAutomationListResponse } from '../src/types/accountAutomation';

vi.mock('../src/api/client', () => ({ api: {
  getAccountAutomation: vi.fn(), putAccountAutomationSettings: vi.fn(), putAccountAutomationAccount: vi.fn(),
  postAccountAutomationAdopt: vi.fn(), postAccountAutomationUnadopt: vi.fn(), postAccountAutomationUnadoptMany: vi.fn(),
  postAccountAutomationCancel: vi.fn(), postAccountAutomationManualReset: vi.fn(),
} }));

const account = (label: string, overrides: Partial<AccountAutomationAccountDTO> = {}): AccountAutomationAccountDTO => ({
  auth_index: 'synthetic-' + label, account_label: label, name: label + '.json', provider: 'codex',
  is_adopted: true, is_enabled: true, burn_credit: false, removed: false, state: 'monitoring',
  monitor_reason: 'not_exhausted', auto_disable_supported: true, disabled_by_automation: false,
  attempt_count: 0, intent_version: 1, has_weekly_evidence: true, weekly_used_percent: 20, ...overrides,
});
const snapshot = (accounts: AccountAutomationAccountDTO[] = []): AccountAutomationListResponse => ({
  settings: { enabled: true, web_enabled: true, source: 'web', master_from_env: false, accounts: accounts.length },
  accounts,
  candidates: accounts.map(a => ({ auth_index: a.auth_index, account_label: a.account_label, name: a.name, provider: a.provider,
    disabled: a.disabled_by_automation, adopted: true, adoptable: true, controllable: true, auto_disable_supported: a.auto_disable_supported })),
});
function mount(data: AccountAutomationListResponse) {
  vi.mocked(api.getAccountAutomation).mockResolvedValue(data);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(<QueryClientProvider client={client}><I18nProvider><AntdApp><AccountAutomationDrawer open onClose={() => {}} /></AntdApp></I18nProvider></QueryClientProvider>);
  return { ...view, client };
}
const row = (name: string) => screen.getByRole('article', { name });
const details = (name: string) => fireEvent.click(within(row(name)).getByRole('button', { name: /Details|详情/ }));

beforeEach(() => { vi.resetAllMocks(); localStorage.setItem('omc-lang', 'en'); });

it('renders real provider artwork, accessible full labels and one state/reason per row', async () => {
  const data = snapshot([
    account('Codex account', { provider: 'codex' }),
    account('Claude account', { provider: 'claude', is_enabled: false, recovery_at_ms: Date.now() + 100000, recovery_status: 'scheduled' }),
    account('Antigravity account', { provider: 'antigravity', auto_disable_supported: false, monitor_reason: 'no_weekly_evidence', has_weekly_evidence: false }),
    account('Missing name', { monitor_reason: 'credential_missing_name' }),
    account('Missing gateway', { monitor_reason: 'credential_not_found' }),
    account('Unknown reason', { monitor_reason: 'future_machine_code' }),
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
  expect(row('Antigravity account').textContent).toContain('Capability unverified');
  expect(row('Antigravity account').textContent).not.toContain('No 7d evidence');
  expect(row('Missing name').textContent).toContain('Missing name; update it in CPA');
  expect(row('Missing gateway').textContent).toContain('Credential absent from gateway');
  expect(row('Unknown reason').textContent).toContain('Awaiting status update');
  expect(screen.queryByText('future_machine_code')).toBeNull();
  expect(screen.getByTestId('automation-stats').textContent).toContain('Monitor only3');
  fireEvent.click(screen.getByRole('button', { name: 'Monitor only' }));
  expect(screen.getAllByRole('article')).toHaveLength(3);
  fireEvent.click(screen.getByRole('button', { name: 'Paused' }));
  expect(screen.getAllByRole('article')).toHaveLength(1);
});

it.each(['en', 'zh-CN'])('keeps active recovery distinct from historical timestamps and localizes missing evidence (%s)', async lang => {
  localStorage.setItem('omc-lang', lang);
  mount(snapshot([
    account('Scheduled', { disabled_by_automation: true, state: 'disabled', recovery_at_ms: Date.now() + 86400000, recovery_status: 'scheduled' }),
    account('Overdue', { disabled_by_automation: true, state: 'error', recovery_at_ms: Date.now() - 1000, recovery_status: 'overdue', next_attempt_at_ms: Date.now() + 1000 }),
    account('Recovered', { recovery_at_ms: Date.now() - 1000, has_weekly_evidence: false, monitor_reason: 'no_weekly_evidence' }),
    account('Monitor', { auto_disable_supported: false, has_weekly_evidence: false, monitor_reason: 'no_weekly_evidence' }),
  ]));
  await screen.findByText('Scheduled');
  expect(row('Scheduled').textContent).toContain(lang === 'en' ? 'Recovery at:' : '预计恢复:');
  expect(row('Recovered').textContent).not.toMatch(/Recovery at:|预计恢复:|waiting to retry|等待重试/);
  details('Overdue'); details('Recovered'); details('Monitor');
  expect(row('Overdue').textContent).toContain(lang === 'en' ? 'Recovery time passed; waiting to retry' : '已过预计恢复时间，等待重试');
  expect(row('Recovered').textContent).toContain(lang === 'en' ? 'No 7d evidence (not auto-disabled)' : '未上报 7d 数据（不参与自动关）');
  expect(row('Monitor').textContent).not.toMatch(/not auto-disabled|不参与自动关/);
});

it('Credit sends only burn_credit, retains recovery, and locks repeated mutation until refreshed', async () => {
  let data = snapshot([account('Recovering', { state: 'disabled', disabled_by_automation: true, recovery_at_ms: Date.now() + 86400000, recovery_status: 'scheduled' })]);
  mount(data);
  let release!: (value: AccountAutomationAccountDTO) => void;
  vi.mocked(api.putAccountAutomationAccount).mockImplementation(() => new Promise(resolve => { release = resolve; }));
  await screen.findByText('Recovering');
  const credit = within(row('Recovering')).getByRole('button', { name: 'Credit exemption for Recovering' });
  fireEvent.click(credit); fireEvent.click(credit);
  await waitFor(() => expect(api.putAccountAutomationAccount).toHaveBeenCalledTimes(1));
  expect(api.putAccountAutomationAccount).toHaveBeenCalledWith('synthetic-Recovering', { burn_credit: true });
  data = snapshot([{ ...data.accounts[0], burn_credit: true }]);
  vi.mocked(api.getAccountAutomation).mockResolvedValue(data);
  release(data.accounts[0]);
  await waitFor(() => expect(credit.getAttribute('aria-pressed')).toBe('true'));
  expect(row('Recovering').textContent).toContain('Auto-disabled');
  expect(row('Recovering').textContent).toContain('Recovery at:');
  expect(credit.textContent).toBe('Credit');
});

it('single and batch unadopt remove roster membership without a gateway toggle', async () => {
  let data = snapshot([account('Single', { is_enabled: false, disabled_by_automation: true }), account('Batch')]);
  mount(data);
  vi.mocked(api.postAccountAutomationUnadopt).mockImplementation(async () => {
    const removed = { ...data.accounts[0], is_adopted: false, removed: true };
    data = { ...data, accounts: data.accounts.slice(1) };
    vi.mocked(api.getAccountAutomation).mockResolvedValue(data);
    return removed;
  });
  vi.mocked(api.postAccountAutomationUnadoptMany).mockResolvedValue({ removed: 1 });
  await screen.findByText('Single'); details('Single');
  fireEvent.click(within(row('Single')).getByRole('button', { name: 'Unadopt' }));
  expect(await screen.findByText(/Only removes roster membership/)).toBeDefined();
  fireEvent.click(screen.getByRole('button', { name: 'Confirm Unadopt' }));
  await waitFor(() => expect(screen.queryByRole('article', { name: 'Single' })).toBeNull());
  expect(api.postAccountAutomationUnadopt).toHaveBeenCalledWith('synthetic-Single');
  fireEvent.click(screen.getByRole('button', { name: 'Batch Operations' }));
  fireEvent.click(screen.getByRole('radio', { name: 'Unadopt' }));
  expect(screen.queryByRole('checkbox', { name: /immediately/ })).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Select all' }));
  fireEvent.click(within(screen.getByRole('radio', { name: 'Unadopt' }).closest('.ant-modal') as HTMLElement).getByRole('button', { name: 'Confirm Unadopt' }));
  await waitFor(() => expect(api.postAccountAutomationUnadoptMany).toHaveBeenCalledWith({ auth_indexes: ['synthetic-Batch'] }));
  expect(api.putAccountAutomationAccount).not.toHaveBeenCalled();
});

it('pausing and resuming use the automation switch while keeping the retained recovery visible', async () => {
  let data = snapshot([account('Paused plan', { state: 'disabled', disabled_by_automation: true, recovery_at_ms: Date.now() + 86400000, recovery_status: 'scheduled' })]);
  mount(data);
  vi.mocked(api.putAccountAutomationAccount).mockImplementation(async (_, patch) => {
    const updated = { ...data.accounts[0], is_enabled: patch.enabled! };
    data = snapshot([updated]);
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
  expect(api.putAccountAutomationAccount).toHaveBeenNthCalledWith(1, 'synthetic-Paused plan', { enabled: false });
  expect(api.putAccountAutomationAccount).toHaveBeenNthCalledWith(2, 'synthetic-Paused plan', { enabled: true });
  expect(row('Paused plan').textContent).toContain('Recovery at:');
});

it('a failed refresh preserves the last roster but blocks mutation until a successful refresh', async () => {
  const data = snapshot([account('Last good')]);
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

it('batch adoption selects only eligible live credentials, retains default-on and optional notes', async () => {
  const data = snapshot();
  data.candidates = [
    { auth_index: 'a', account_label: 'a: Verified', provider: 'claude', adopted: false, adoptable: true, controllable: true, auto_disable_supported: true, disabled: true },
    { auth_index: 'b', account_label: 'Monitor', provider: 'antigravity', adopted: false, adoptable: true, controllable: true, auto_disable_supported: false, disabled: false },
    { auth_index: 'c', account_label: 'Blocked', adopted: false, adoptable: false, controllable: false, auto_disable_supported: false, disabled: false, adoption_blocked_reason: 'credential_missing_name' },
  ];
  mount(data);
  vi.mocked(api.postAccountAutomationAdopt).mockResolvedValue({ adopted: 2 });
  await waitFor(() => expect((screen.getByRole('button', { name: 'Batch Operations' }) as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(screen.getByRole('button', { name: 'Batch Operations' }));
  expect((screen.getByRole('button', { name: 'Confirm Adoption' }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole('checkbox', { name: /immediately/ }) as HTMLInputElement).checked).toBe(true);
  expect((screen.getByRole('checkbox', { name: 'Blocked' }) as HTMLInputElement).disabled).toBe(true);
  expect(screen.getByText(/Capability unverified; monitor-only after adoption/)).toBeDefined();
  fireEvent.click(screen.getByRole('button', { name: 'Select all' }));
  fireEvent.change(screen.getByRole('textbox', { name: /Batch note/ }), { target: { value: '  synthetic batch  ' } });
  fireEvent.click(screen.getByRole('button', { name: 'Confirm Adoption' }));
  await waitFor(() => expect(api.postAccountAutomationAdopt).toHaveBeenCalledWith({ auth_indexes: ['a', 'b'], enable: true, note: 'synthetic batch' }));
});

it('refresh prunes stale selection, keeps cleanup silent, and refuses degraded adoption', async () => {
  const data = snapshot([account('Absent')]);
  data.candidates.push({ auth_index: 'new', account_label: 'New', disabled: false, controllable: true, adopted: false, adoptable: true, auto_disable_supported: true });
  const { client } = mount(data);
  await screen.findByText('Absent');
  fireEvent.click(screen.getByRole('button', { name: 'Batch Operations' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'New' }));
  const refreshed = { ...data, accounts: [], candidates: [] };
  vi.mocked(api.getAccountAutomation).mockResolvedValue(refreshed);
  await client.invalidateQueries({ queryKey: ['account-automation'] });
  await waitFor(() => expect(screen.queryByRole('article', { name: 'Absent' })).toBeNull());
  expect(screen.getByText('0 accounts selected')).toBeDefined();
  expect((screen.getByRole('button', { name: 'Confirm Adoption' }) as HTMLButtonElement).disabled).toBe(true);
  expect(document.querySelector('.omc-toast')).toBeNull();
  expect(document.body.textContent).not.toMatch(/absent from gateway|cleaned|清理/);
  vi.mocked(api.getAccountAutomation).mockResolvedValue({ ...data, candidates_degraded: true });
  await client.invalidateQueries({ queryKey: ['account-automation'] });
  await waitFor(() => expect((screen.getByRole('checkbox', { name: 'New' }) as HTMLInputElement).disabled).toBe(true));
  expect(screen.getByText('Adopted 1 / Gateway — credentials')).toBeDefined();
});

it('details retain retry cancellation and future manual-reset submission', async () => {
  const data = snapshot([account('Retry', { state: 'error', next_attempt_at_ms: Date.now() + 1000 }), account('Reset', { monitor_reason: 'missing_reset' })]);
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
