import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { RepoKeysPanel } from '../src/components/keys/RepoKeysPanel';
import { RepoKeyApplyPage } from '../src/pages/repo-key/RepoKeyApplyPage';
import type { RepoKeyApplication, RepoKeyBinding, RepoKeyReview, RepoKeyUsage } from '../src/types/repoKeys';

const CANONICAL_REPO = 't14-fixture/case-repo';
const DISPLAY_REPO = 'T14-Fixture/Case-Repo';
const CREATED_AT_MS = Date.parse('2026-11-06T08:00:00Z');
const USAGE: RepoKeyUsage = {
  window: '7d', from_ms: CREATED_AT_MS - 7 * 86_400_000, to_ms: CREATED_AT_MS,
  requests: 3, failed: 0, total_tokens: 300, cost_nanos: 123_000, last_used_ms: CREATED_AT_MS, models: [],
};
const APPLICATION: RepoKeyApplication = {
  id: 'case-application', repo: CANONICAL_REPO, repo_display: DISPLAY_REPO, repo_has_remote: true,
  agent: 'case-agent', device: 'case-device', purpose: 'Review the reported repository identity.',
  kind: 'reuse', status: 'pending', source: 'agent', public_key_fingerprint: 'x25519:sha256:display-fixture',
  ciphertext_ready: false, created_at_ms: CREATED_AT_MS,
};
const BINDING: RepoKeyBinding = {
  id: 'case-binding', repo: CANONICAL_REPO, repo_has_remote: true, key_mask: 'omc-…case',
  status: 'active', created_by: 'case-operator', created_at_ms: CREATED_AT_MS, usage_7d: USAGE,
};
const REVIEW: RepoKeyReview = {
  application: APPLICATION,
  repo: { repo: CANONICAL_REPO, repo_has_remote: true, state: 'active', key_mask: BINDING.key_mask, usage: USAGE, stops: [] },
  public_key_changed: false, impact: 'reuse', approval_expired: false,
};
const clients: QueryClient[] = [];

function jsonResponse(value: unknown) {
  return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
}

function mount(route = '/api-keys') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  clients.push(client);
  return render(
    <React.StrictMode>
      <QueryClientProvider client={client}>
        <I18nProvider>
          <AntdApp>
            <MemoryRouter initialEntries={[route]}>
              <Routes>
                <Route path="/api-keys" element={<RepoKeysPanel />} />
                <Route path="/repo-key-apply/:token" element={<RepoKeyApplyPage />} />
              </Routes>
            </MemoryRouter>
          </AntdApp>
        </I18nProvider>
      </QueryClientProvider>
    </React.StrictMode>,
  );
}

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false, media: query, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  }));
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('fetch', vi.fn(async (input: string, options: RequestInit = {}) => {
    const url = new URL(input, 'http://localhost');
    if ((options.method ?? 'GET') !== 'GET') throw new Error(`Unexpected component write: ${input}`);
    if (url.pathname.endsWith('/management/repo-keys')) {
      return jsonResponse({ keys: [BINDING], pending_applications: 1, awaiting_delivery: 0 });
    }
    if (url.pathname.endsWith('/management/repo-keys/applications')) {
      return jsonResponse({ applications: url.searchParams.get('status') === 'pending' ? [APPLICATION] : [] });
    }
    if (url.pathname.endsWith('/management/repo-keys/apply-link/case-link')
      || url.pathname.endsWith('/management/repo-keys/applications/case-application/review')) return jsonResponse(REVIEW);
    if (url.pathname.endsWith('/management/repo-keys/case-binding')) {
      return jsonResponse({ key: BINDING, stops: [], deliveries: [], applications: [{ ...APPLICATION, status: 'approved' }], usage: USAGE });
    }
    if (url.pathname.endsWith('/management/repo-keys/audit-push')) {
      return jsonResponse({ enabled: false, pending: 0, quarantined: 0, earliest_ms: 0, last_success_ms: 0 });
    }
    throw new Error(`Unexpected component request: ${input}`);
  }));
});

afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
});

describe('repository application display identity', () => {
  it('shows the reported repository spelling on the approval link', async () => {
    mount('/repo-key-apply/case-link');
    const repo = await screen.findByText(DISPLAY_REPO, { exact: true });
    expect(repo.tagName).toBe('DD');
    expect(screen.queryByText(CANONICAL_REPO, { exact: true })).toBeNull();
  });

  it('keeps the queue and its review in the reported spelling and the binding list canonical', async () => {
    mount();
    const request = await screen.findByText(DISPLAY_REPO, { exact: true });
    expect(screen.getByText(CANONICAL_REPO, { exact: true })).toBeTruthy();
    const row = request.closest('tr') as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: 'Details' }));
    const dialog = await screen.findByRole('dialog');
    expect(await within(dialog).findByText(DISPLAY_REPO, { exact: true })).toBeTruthy();
    expect(within(dialog).getByText(`${DISPLAY_REPO} · application`, { exact: true })).toBeTruthy();
  });

  it('retains the reported spelling in the bound key application history', async () => {
    mount();
    const identity = await screen.findByText('omc-…case', { exact: true });
    const row = identity.closest('tr') as HTMLElement;
    expect(within(row).getByText(CANONICAL_REPO, { exact: true })).toBeTruthy();
    fireEvent.click(within(row).getByRole('button', { name: 'Details' }));
    const dialog = await screen.findByRole('dialog');
    const application = await within(dialog).findByText('case-agent', { exact: true });
    const history = application.closest('li') as HTMLElement;
    expect(within(history).getByText(DISPLAY_REPO, { exact: true })).toBeTruthy();
    expect(within(history).queryByText(CANONICAL_REPO, { exact: true })).toBeNull();
  });
});
