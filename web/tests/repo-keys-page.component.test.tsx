import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { RepoKeysPage } from '../src/pages/RepoKeysPage';
import type { RepoKeyBinding, RepoKeyBindingDetail } from '../src/types/repoKeys';

const KEY_READ = '/management/repo-keys';
const CREATED_AT_MS = Date.parse('2026-11-06T08:00:00Z');
const BINDING: RepoKeyBinding = {
  id: 'binding-alpha',
  repo: 'example/alpha',
  repo_has_remote: true,
  key_mask: 'omc-…alpha',
  status: 'active',
  created_at_ms: CREATED_AT_MS,
  created_by: 'operator',
};
const DETAIL: RepoKeyBindingDetail = {
  key: BINDING,
  stops: [],
  deliveries: [],
  applications: [],
  usage: {
    window: '7d',
    from_ms: CREATED_AT_MS - 7 * 86_400_000,
    to_ms: CREATED_AT_MS,
    requests: 0,
    failed: 0,
    total_tokens: 0,
    cost_nanos: 0,
    last_used_ms: 0,
    models: [],
  },
};

const requests: string[] = [];
const clients: QueryClient[] = [];
let bindings: RepoKeyBinding[] = [];

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
}

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  clients.push(client);
  const view = render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <AntdApp>
          <MemoryRouter initialEntries={['/repo-keys']}>
            <RepoKeysPage />
          </MemoryRouter>
        </AntdApp>
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { ...view, client };
}

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  requests.length = 0;
  bindings = [];
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false, media: query, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  }));
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    const url = new URL(input, 'http://localhost');
    requests.push(`${url.pathname}${url.search}`);
    if (url.pathname.endsWith(`${KEY_READ}/${BINDING.id}`)) return jsonResponse(DETAIL);
    if (url.pathname.endsWith(KEY_READ)) {
      return jsonResponse({ keys: bindings, pending_applications: 0, awaiting_delivery: 0 });
    }
    if (url.pathname.endsWith(`${KEY_READ}/applications`)) return jsonResponse({ applications: [] });
    if (url.pathname.endsWith(`${KEY_READ}/audit-push`)) {
      return jsonResponse({ enabled: false, pending: 0, quarantined: 0, earliest_ms: 0, last_success_ms: 0 });
    }
    throw new Error(`Unexpected request: ${input}`);
  }));
});

afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
});

describe('repository keys page', () => {
  it('draws the panel under the page header instead of repeating its title', async () => {
    renderPage();
    const title = await screen.findByRole('heading', { level: 1, name: 'Repository keys' });
    expect(title.tagName).toBe('H1');
    expect(screen.queryByRole('heading', { level: 2, name: 'Repository keys' })).toBeNull();
    expect(screen.getByText(/One key per repository/)).toBeTruthy();
    expect(await screen.findByText('Awaiting approval (0)')).toBeTruthy();
    expect(await screen.findByText('No applications are waiting')).toBeTruthy();
    expect(screen.getByText('Awaiting delivery (0)')).toBeTruthy();
    expect(screen.getByText('Keys (0)')).toBeTruthy();
    expect(screen.getByText('Audit platform')).toBeTruthy();
  });

  it("refreshes only the panel's own reads", async () => {
    renderPage();
    await screen.findByText('No applications are waiting');
    const keyReads = () => requests.filter((path) => path.endsWith(KEY_READ)).length;
    const before = keyReads();
    expect(before).toBeGreaterThan(0);
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(keyReads()).toBe(before + 1));
    expect(requests.every((path) => path.includes(KEY_READ))).toBe(true);
  });

  it('refreshes only the reads the page is showing', async () => {
    bindings = [BINDING];
    const { client } = renderPage();
    await screen.findByText('Keys (1)');
    await waitFor(() => expect(client.isFetching()).toBe(0));

    // Leave an inactive search result and an inactive detail read in the cache.
    const search = screen.getByRole('textbox', { name: 'Search by repository, key or agent' });
    fireEvent.change(search, { target: { value: 'alpha' } });
    await waitFor(() => expect(requests.some((path) => path.endsWith(`${KEY_READ}?q=alpha`))).toBe(true));
    fireEvent.change(search, { target: { value: '' } });
    await waitFor(() => expect(client.isFetching()).toBe(0));

    const row = (await screen.findByText('example/alpha')).closest('tr') as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: 'Details' }));
    await screen.findByRole('dialog');
    await waitFor(() => expect(requests.some((path) => path.endsWith(`${KEY_READ}/${BINDING.id}?window=7d`))).toBe(true));
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    await waitFor(() => expect(client.isFetching()).toBe(0));

    const listReads = () => requests.filter((path) => path.endsWith(KEY_READ)).length;
    const searchReads = () => requests.filter((path) => path.includes('?q=')).length;
    const detailReads = () => requests.filter((path) => path.includes(`${KEY_READ}/${BINDING.id}?window=`)).length;
    const before = { list: listReads(), search: searchReads(), detail: detailReads() };

    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    await waitFor(() => expect(listReads()).toBe(before.list + 1));
    await waitFor(() => expect(client.isFetching()).toBe(0));

    expect(searchReads()).toBe(before.search);
    expect(detailReads()).toBe(before.detail);
  });
});
