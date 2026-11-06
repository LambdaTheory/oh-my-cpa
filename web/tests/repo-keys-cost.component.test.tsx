import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { RepoKeysPanel } from '../src/components/keys/RepoKeysPanel';
import type { RepoKeyApplication, RepoKeyBinding, RepoKeyReview } from '../src/types/repoKeys';

const clients: QueryClient[] = [];
const CREATED_AT_MS = Date.parse('2026-11-06T08:00:00Z');
const BINDING: RepoKeyBinding = {
  id: 'binding-cost',
  repo: 'example/cost-regression',
  repo_has_remote: true,
  key_mask: 'omc-…cost',
  status: 'active',
  created_at_ms: CREATED_AT_MS,
  created_by: 'cost-operator',
  last_recipient: 'cost-agent @ cost-device',
  usage_7d: {
    window: '7d',
    from_ms: CREATED_AT_MS - 7 * 86_400_000,
    to_ms: CREATED_AT_MS,
    requests: 9876,
    failed: 7,
    total_tokens: 54321,
    cost_nanos: 12_345_600,
    last_used_ms: CREATED_AT_MS - 60_000,
    models: [],
  },
};
const APPLICATION: RepoKeyApplication = {
  id: 'application-cost', repo: 'example/review-cost', repo_has_remote: true,
  agent: 'review-agent', device: 'review-device', purpose: 'Review repository usage.',
  kind: 'reuse', status: 'pending', source: 'agent', public_key_fingerprint: 'x25519:sha256:cost-fixture',
  ciphertext_ready: false, created_at_ms: CREATED_AT_MS,
};
const REVIEW: RepoKeyReview = {
  application: APPLICATION,
  repo: {
    repo: APPLICATION.repo, repo_has_remote: true, state: 'active', key_mask: BINDING.key_mask,
    usage: BINDING.usage_7d!, stops: [],
  },
  public_key_changed: false, impact: 'reuse', approval_expired: false,
};
let pendingApplications: RepoKeyApplication[];

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
}

function mount(isPhone: boolean, language: string) {
  window.localStorage.setItem('omc-lang', language);
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: isPhone && query === '(max-width: 640px)', media: query, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  clients.push(client);
  return render(
    <React.StrictMode>
      <QueryClientProvider client={client}>
        <I18nProvider>
          <AntdApp>
            <MemoryRouter><RepoKeysPanel /></MemoryRouter>
          </AntdApp>
        </I18nProvider>
      </QueryClientProvider>
    </React.StrictMode>,
  );
}

beforeEach(() => {
  pendingApplications = [];
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('fetch', vi.fn(async (input: string, options: RequestInit = {}) => {
    const url = new URL(input, 'http://localhost');
    if ((options.method ?? 'GET') !== 'GET') throw new Error(`Unexpected component write: ${input}`);
    if (url.pathname.endsWith('/management/repo-keys')) {
      return jsonResponse({ keys: [BINDING], pending_applications: pendingApplications.length, awaiting_delivery: 0 });
    }
    if (url.pathname.endsWith('/management/repo-keys/applications')) {
      return jsonResponse({ applications: url.searchParams.get('status') === 'pending' ? pendingApplications : [] });
    }
    if (url.pathname.endsWith('/management/repo-keys/applications/application-cost/review')) return jsonResponse(REVIEW);
    if (url.pathname.endsWith('/management/repo-keys/audit-push')) {
      return jsonResponse({ enabled: false, pending: 0, quarantined: 0, earliest_ms: 0, last_success_ms: 0 });
    }
    throw new Error(`Unexpected component request: ${input}`);
  }));
});

afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
});

it('shows money under the same seven-day cost label in an approval review', async () => {
  pendingApplications = [APPLICATION];
  mount(false, 'en');
  const identity = await screen.findByText('example/review-cost');
  const row = identity.closest('tr') as HTMLElement;
  fireEvent.click(within(row).getByRole('button', { name: 'Details' }));
  const dialog = await screen.findByRole('dialog');
  const label = await within(dialog).findByText('Cost (7d)', { exact: true });
  expect(label.nextElementSibling?.textContent).toBe('$0.0123');
});

describe.each([
  { language: 'en', costLabel: 'Cost (7d)' },
  { language: 'zh', costLabel: '近 7 天费用' },
  { language: 'zh-Hant', costLabel: '近 7 天費用' },
  { language: 'ms', costLabel: 'Kos (7 hari)' },
])('repository key seven-day cost in $language', ({ language, costLabel }) => {
  it.each([false, true])('renders money rather than the different request count (phone=%s)', async (isPhone) => {
    // The deferred catalogs (ms, zh-Hant) arrive in an effect, so their first paint and
    // first read settle after render() returns; act keeps that rendezvous inside the mount.
    await act(async () => { mount(isPhone, language); });
    const identity = await screen.findByText('example/cost-regression');
    const row = identity.closest(isPhone ? 'article' : 'tr') as HTMLElement;
    expect(within(row).getByText('$0.0123')).toBeTruthy();
    expect(within(row).queryByText('9,876')).toBeNull();
    if (isPhone) {
      const label = within(row).getByText(costLabel, { exact: true });
      expect(label.tagName).toBe('DT');
      expect(label.nextElementSibling?.textContent).toBe('$0.0123');
    } else {
      expect(screen.getByRole('columnheader', { name: costLabel })).toBeTruthy();
    }
  });
});
