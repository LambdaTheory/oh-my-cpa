import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { api } from '../src/api/client';
import { I18nProvider } from '../src/i18n';
import { ApiKeysPage } from '../src/pages/ApiKeysPage';
import type { ConfigScalarsResponse } from '../src/types/configManagement';

vi.mock('../src/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../src/api/client')>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getConfigScalars: vi.fn(),
      getClientAPIKeys: vi.fn(),
      getClientKeyUsage: vi.fn(),
      getRepoKeys: vi.fn(),
    },
  };
});

function renderPage() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <AntdApp>
          <MemoryRouter initialEntries={['/api-keys']}>
            <Routes>
              <Route path="/api-keys" element={<ApiKeysPage />} />
              <Route path="/repo-keys" element={<div>repository keys destination</div>} />
            </Routes>
          </MemoryRouter>
        </AntdApp>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false, media: query, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  }));
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.mocked(api.getConfigScalars).mockResolvedValue({
    scalars: {} as ConfigScalarsResponse['scalars'],
    supported_keys: [], revision: 'r1', safe_yaml: 'access:\n  api-keys: []\n',
  });
  vi.mocked(api.getClientAPIKeys).mockResolvedValue({ keys: [], total: 0 });
  vi.mocked(api.getClientKeyUsage).mockResolvedValue({
    window: { from: Date.now() - 86_400_000, to: Date.now() }, usage: [],
  });
  vi.mocked(api.getRepoKeys).mockResolvedValue({ keys: [], pending_applications: 0, awaiting_delivery: 0 });
});

describe('key management page after the split', () => {
  it('keeps only the gateway list and points at the repository keys page', async () => {
    renderPage();
    expect(await screen.findByRole('heading', { level: 1, name: 'Key management' })).toBeTruthy();
    expect(await screen.findByText('No Client Keys')).toBeTruthy();

    expect(screen.getByText('Repository keys now live under Capabilities.')).toBeTruthy();
    const link = screen.getByRole('link', { name: 'Open repository keys' });
    expect(link.getAttribute('href')).toBe('/repo-keys');

    expect(screen.queryByText('Awaiting approval (0)')).toBeNull();
    expect(screen.queryByText(/One key per repository/)).toBeNull();
    expect(screen.queryByRole('button', { name: 'File for an agent' })).toBeNull();
    expect(screen.queryByText('Audit platform')).toBeNull();

    fireEvent.click(link);
    expect(await screen.findByText('repository keys destination')).toBeTruthy();
  });
});
