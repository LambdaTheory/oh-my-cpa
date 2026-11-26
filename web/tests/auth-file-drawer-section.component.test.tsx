import { App } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { AuthFileDetailDrawer } from '../src/components/authFiles/AuthFileDetailDrawer';
import type { ManagementAuthFile } from '../src/types/managementAuthFile';

const FILE: ManagementAuthFile = {
  name: 'synthetic.json', auth_index: 'synthetic-index', type: 'claude', provider: 'claude',
  disabled: false, unavailable: false, runtime_only: false, success: 0, failed: 0, note: 'Synthetic note',
};
const FIELDS = { name: FILE.name, note: FILE.note, disable_cooling: false, websockets: false, using_api: false, excluded_models: [], model_aliases: [] };
const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false, media: query, onchange: null,
    addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false,
  }));
  vi.stubGlobal('fetch', async (path: string) => {
    const url = new URL(path, 'http://127.0.0.1');
    const result = url.pathname.endsWith('/auth-files/safe-fields') ? FIELDS : FIELDS;
    return new Response(JSON.stringify(result), { status: 200, headers: { 'Content-Type': 'application/json' } });
  });
});

function Harness({ token }: { token: number }) {
  return (
    <App>
      <QueryClientProvider client={client}>
        <I18nProvider>
          <AuthFileDetailDrawer
            file={FILE}
            open
            canEdit
            initialSection="overview"
            sectionRequest={{ section: 'configuration', token }}
            onClose={() => {}}
            onSaved={() => {}}
            onDownload={() => {}}
          />
        </I18nProvider>
      </QueryClientProvider>
    </App>
  );
}

const isSelected = (name: string) => screen.getByRole('tab', { name }).getAttribute('aria-selected') === 'true';

it('opens the configuration section on request and reopens it when the request token moves', async () => {
  const view = render(<Harness token={1} />);
  await waitFor(() => expect(isSelected('Configuration')).toBe(true));
  expect(screen.getByRole('tab', { name: 'Configuration' }).getAttribute('aria-selected')).toBe('true');

  fireEvent.click(screen.getByRole('tab', { name: 'Quota' }));
  await waitFor(() => expect(isSelected('Quota')).toBe(true));

  // A second request while the drawer stays open must act again; a plain initialSection cannot.
  view.rerender(<Harness token={2} />);
  await waitFor(() => expect(isSelected('Configuration')).toBe(true));
  view.unmount();
  client.clear();
});
