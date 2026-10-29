import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { useOAuthWorkspaceActions } from '../src/pages/oauthManagement/useOAuthWorkspaceActions';
import { buildOAuthWorkspaceProjection, type OAuthWorkspaceRecord } from '../src/pages/oauthManagement/oauthWorkspaceLogic';
import { OAuthCredentialRecord } from '../src/pages/oauthManagement/OAuthCredentialRecord';
import { CompactQuotaView } from '../src/pages/quota/CompactQuotaView';
import type { ManagementAuthFile } from '../src/types/managementAuthFile';
import type { QuotaItem } from '../src/types/quota';
import type { OllamaAccountDTO } from '../src/types/ollama';

const FILES: ManagementAuthFile[] = Array.from({ length: 12 }, (_, index) => ({
  name: `Synthetic ${index}`, auth_index: `index-${index}`, type: 'codex', disabled: index >= 10,
  unavailable: false, runtime_only: false, success: 0, failed: 0,
}));
const QUOTAS: QuotaItem[] = FILES.map((file) => ({
  auth_index: file.auth_index!, name: file.name, type: 'codex', provider: 'codex', disabled: file.disabled,
  status: 'healthy', observed_at_ms: 1, windows: [], quota_exceeded: false,
  capabilities: { refresh_supported: true, clear_cooldown_supported: false, reset_credit_supported: false },
  recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
}));
const ACCOUNT: OllamaAccountDTO = {
  id: 'opaque-monitor', label: 'ollama:opaque-monitor: Legacy account', enabled: true, has_cookie: true, has_api_key: false,
  status: 'ok', attempt_count: 0, created_at_ms: 1, updated_at_ms: 1,
};
const RECORDS = buildOAuthWorkspaceProjection(FILES, QUOTAS, [], [ACCOUNT]).records;
let client: QueryClient;
let requests: Array<{ path: string; method: string; body: Record<string, unknown> }>;
let operations: Promise<void>[];
let heldRefresh: { promise: Promise<Response>; resolve: (response: Response) => void } | undefined;
let quotaRefreshResponse: (authIndexes: string[]) => unknown;
let quotaBatchTransportFailure: boolean;
let quotaSingleRefreshResponse: (authIndex: string) => unknown;

function response(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

function Surface() {
  const [selected, setSelected] = React.useState<string[]>([]);
  const actions = useOAuthWorkspaceActions(RECORDS, selected, setSelected, () => {});
  const picked = RECORDS.filter((record) => selected.includes(record.key));
  const run = (operation: Promise<void>) => { operations.push(operation); };
  const row = (record: OAuthWorkspaceRecord) => <OAuthCredentialRecord key={record.key}
    source={record.source} ollamaAccount={record.ollamaAccount} file={record.file} identityKey={record.key}
    displayProvider={record.displayProvider} selected={selected.includes(record.key)} busy={actions.isRecordBusy(record)}
    canTargetFile={record.canSelectForFileBatch} onSelect={(checked) => setSelected((previous) => checked ? [...previous, record.key] : previous.filter((key) => key !== record.key))}
    onToggle={() => run(actions.toggleOne(record))} onDelete={() => run(actions.deleteOne(record))}
    onRefreshQuota={() => run(actions.refreshQuotaForRecord(record))} canRefreshQuota={record.canRefreshQuota}
    onDownload={() => {}} onEdit={() => {}} onShowModels={() => {}} onShowDetails={() => {}} />;
  return <>
    <output aria-label="Selection">{picked.length}</output>
    <button onClick={() => run(actions.refreshQuota('all', RECORDS, RECORDS, RECORDS))}>Refresh all</button>
    <button onClick={() => run(actions.batchStatus(true, picked))}>Disable picked</button>
    <button onClick={() => run(actions.batchRefresh(picked))}>Refresh picked</button>
    <button onClick={() => run(actions.batchDelete(picked))}>Delete picked</button>
    {RECORDS.filter((record) => record.key === RECORDS[0].key || record.source === 'ollama').map(row)}
  </>;
}

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  requests = [];
  operations = [];
  heldRefresh = undefined;
  quotaBatchTransportFailure = false;
  quotaRefreshResponse = (authIndexes) => {
    const quotas = QUOTAS.filter((quota) => authIndexes.includes(quota.auth_index));
    return {
      status: 'ok', quotas, total: authIndexes.length,
      results: quotas.map((quota) => ({ auth_index: quota.auth_index, name: quota.name, outcome: 'success', quota })),
    };
  };
  quotaSingleRefreshResponse = (authIndex) => ({
    status: 'ok', quota: QUOTAS.find((quota) => quota.auth_index === authIndex),
  });
  client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  client.setQueryData(['ollama-accounts'], { accounts: [ACCOUNT] });
  client.setQueryData(['ollama-quota'], { accounts: [{ account_id: ACCOUNT.id, quota: QUOTAS[0] }] });
  vi.stubGlobal('fetch', vi.fn(async (input: string, options: RequestInit) => {
    const path = new URL(input, 'http://localhost').pathname;
    const body = options.body ? JSON.parse(String(options.body)) : {};
    requests.push({ path, method: options.method ?? 'GET', body });
    if (path.endsWith('/management/quota/refresh')) {
      if (body.auth_index) return response(quotaSingleRefreshResponse(body.auth_index as string));
      if (quotaBatchTransportFailure) throw new TypeError('Synthetic connection closed');
      return response(quotaRefreshResponse(body.auth_indexes as string[]));
    }
    if (path.endsWith(`/ollama/accounts/${ACCOUNT.id}/refresh`)) {
      return heldRefresh?.promise ?? response({ account: ACCOUNT, refreshed: true });
    }
    if (path.endsWith(`/ollama/accounts/${ACCOUNT.id}`)) return response({ ...ACCOUNT, ok: true });
    if (path.endsWith('/management/auth-files/refresh') || path.endsWith('/management/auth-files/status')) return response({ status: 'ok' });
    if (path.endsWith('/management/auth-files')) return response({ files: [FILES[0].name] });
    throw new Error(`Unexpected request ${options.method} ${path}`);
  }));
});

afterEach(async () => {
  heldRefresh?.resolve(response({ account: ACCOUNT, refreshed: true }));
  await act(async () => { await Promise.all(operations); });
  client.clear();
  vi.unstubAllGlobals();
});

function mount() {
  return render(<React.StrictMode><QueryClientProvider client={client}><I18nProvider><AntdApp><Surface /></AntdApp></I18nProvider></QueryClientProvider></React.StrictMode>);
}

it('renders an accessible Ollama checkbox and routes mixed collection, refresh and delete by source', async () => {
  const view = mount();
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Legacy account' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Synthetic 0' }));
  expect(screen.getByLabelText('Selection').textContent).toBe('2');
  expect(view.container.textContent).not.toContain('opaque-monitor');
  fireEvent.click(screen.getByText('Refresh picked', { exact: true }));
  await act(async () => { await Promise.all(operations); });
  expect(requests.filter((request) => request.path.endsWith('/refresh')).map((request) => request.path)).toEqual(expect.arrayContaining([
    '/omc/api/v1/management/auth-files/refresh', '/omc/api/v1/ollama/accounts/opaque-monitor/refresh',
  ]));
  fireEvent.click(screen.getByText('Disable picked'));
  await act(async () => { await Promise.all(operations); });
  expect(requests.find((request) => request.method === 'PUT')?.body).toEqual({ enabled: false });
  expect(screen.getByLabelText('Selection').textContent).toBe('0');
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Legacy account' }));
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Synthetic 0' }));
  fireEvent.click(screen.getByText('Delete picked'));
  await act(async () => { await Promise.all(operations); });
  expect(requests.filter((request) => request.method === 'DELETE').map((request) => request.path)).toEqual(expect.arrayContaining([
    '/omc/api/v1/management/auth-files', '/omc/api/v1/ollama/accounts/opaque-monitor',
  ]));
  for (const request of requests.filter((entry) => entry.path.includes('/management/'))) {
    expect(JSON.stringify(request.body)).not.toContain('opaque-monitor');
  }
});

it('refreshes exactly eleven enabled targets, preserves selection and last-good quota through an Ollama 429', async () => {
  let resolve!: (response: Response) => void;
  heldRefresh = { promise: new Promise<Response>((complete) => { resolve = complete; }), resolve: (value) => resolve(value) };
  mount();
  fireEvent.click(screen.getByRole('checkbox', { name: 'Select Legacy account' }));
  fireEvent.click(screen.getByText('Refresh all'));
  await waitFor(() => expect(requests.some((request) => request.path.endsWith('/opaque-monitor/refresh'))).toBe(true));
  expect(screen.getByRole('checkbox', { name: 'Select Legacy account' }).hasAttribute('disabled')).toBe(true);
  const cpa = requests.find((request) => request.path.endsWith('/management/quota/refresh'))!;
  expect(cpa.body.auth_indexes).toEqual(FILES.slice(0, 10).map((file) => file.auth_index));
  await act(async () => {
    heldRefresh!.resolve(response({ retry_at_ms: Date.now() + 60_000, account: ACCOUNT }, 429));
    await Promise.all(operations);
  });
  expect(screen.getByLabelText('Selection').textContent).toBe('1');
  expect(client.getQueryData(['ollama-quota'])).toEqual({ accounts: [{ account_id: ACCOUNT.id, quota: QUOTAS[0] }] });
  expect(screen.getByTestId('quota-operation-report').textContent).toContain('10 succeeded');
});

it('explains disabled skips in the clean all-scope acknowledgement', async () => {
  mount();
  fireEvent.click(screen.getByText('Refresh all'));
  await act(async () => { await Promise.all(operations); });
  expect(screen.getByText('Quota refresh finished: 11 succeeded, 2 skipped')).toBeTruthy();
  expect(screen.getByText('Credential is disabled (2)')).toBeTruthy();
});

it('renders success, rate-limited, failed and skipped quota groups with per-target reasons', async () => {
  const retryAtMS = Date.now() + 120_000;
  quotaRefreshResponse = (authIndexes) => {
    const quotas = QUOTAS.filter((quota) => authIndexes.includes(quota.auth_index) && quota.auth_index !== 'index-1');
    return {
      status: 'ok', quotas, total: authIndexes.length,
      results: authIndexes.map((authIndex) => {
        const quota = QUOTAS.find((item) => item.auth_index === authIndex)!;
        if (authIndex === 'index-1') return { auth_index: authIndex, name: quota.name, outcome: 'rate_limited', retry_at_ms: retryAtMS, retry_after_ms: 120_000 };
        if (authIndex === 'index-2') return { auth_index: authIndex, name: quota.name, outcome: 'failed', error: 'Synthetic provider failure', quota };
        return { auth_index: authIndex, name: quota.name, outcome: 'success', quota };
      }),
    };
  };
  mount();
  fireEvent.click(screen.getByText('Refresh all'));
  await act(async () => { await Promise.all(operations); });

  const report = screen.getByTestId('quota-operation-report');
  expect(report.textContent).toContain('Quota refresh: 9 succeeded, 1 rate limited, 1 failed, 2 skipped');
  expect(within(report).getByText('Succeeded')).toBeTruthy();
  expect(within(report).getByText('Rate limited')).toBeTruthy();
  expect(within(report).getByText('Failed')).toBeTruthy();
  expect(within(report).getByText('Skipped')).toBeTruthy();
  expect(report.textContent).toContain('Synthetic 1');
  expect(report.textContent).toContain('retry at');
  expect(report.textContent).toContain('Synthetic provider failure');
  expect(report.textContent).not.toContain('rate_limited_backoff');
});

it('isolates a batch transport failure to per-credential refresh outcomes', async () => {
  quotaBatchTransportFailure = true;
  quotaSingleRefreshResponse = (authIndex) => {
    const quota = QUOTAS.find((item) => item.auth_index === authIndex)!;
    if (authIndex === 'index-2') return { status: 'ok', quota: { ...quota, status: 'error', error: 'Synthetic isolated failure' } };
    return { status: 'ok', quota };
  };
  mount();
  fireEvent.click(screen.getByText('Refresh all'));
  await act(async () => { await Promise.all(operations); });

  const report = screen.getByTestId('quota-operation-report');
  expect(report.textContent).toContain('Quota refresh: 10 succeeded, 0 rate limited, 1 failed, 2 skipped');
  expect(report.textContent).toContain('Synthetic isolated failure');
  expect(requests.filter((request) => request.body.auth_indexes).length).toBe(1);
  expect(requests.filter((request) => request.body.auth_index).length).toBe(10);
});

it('hides unverified Ollama money but retains verified zero and independent reset credits', () => {
  const quota: QuotaItem = { ...QUOTAS[0], provider: 'ollama', type: 'ollama', plan: {
    plan_type: 'pro', plan_label: 'Legacy', tier: 'premium', credits: { balance: '$0' },
  } };
  const view = render(<I18nProvider><CompactQuotaView item={quota} nowMS={1} /></I18nProvider>);
  expect(view.container.querySelector('[data-credit-balance]')).toBeNull();
  view.rerender(<I18nProvider><CompactQuotaView item={{ ...quota, raw_signals: { ollama_balance_source: 'settings_balance' } }} nowMS={1} /></I18nProvider>);
  expect(within(view.container).getByText(/Credit balance: \$0/)).toBeTruthy();
});
