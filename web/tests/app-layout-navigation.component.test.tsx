import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { ThemeProvider } from '../src/theme/ThemeContext';
import { AppLayout } from '../src/components/common/AppLayout';

/**
 * The shell's breadcrumb, menu highlight and tab title against the rail model.
 *
 * A route no entry owns - a plugin page whose plugin has stopped, say - is still shell chrome:
 * the menu highlights the dashboard and the breadcrumb names the dashboard's group, while the
 * page's own name stays generic. That fallback predates the rail's resolution helper and must
 * survive it; the helper alone returns nothing for such a path.
 */

const clients: QueryClient[] = [];

const jsonResponse = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

function mount(pathname: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  clients.push(client);
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <ThemeProvider>
          <AntdApp>
            <MemoryRouter initialEntries={[pathname]}>
              <Routes>
                <Route path="*" element={<AppLayout />} />
              </Routes>
            </MemoryRouter>
          </AntdApp>
        </ThemeProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  window.__OMCPA_CONFIG__ = { basePath: '/omc', demo: false };
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent: () => false,
  }));
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    if (input.endsWith('/api/healthz')) {
      return jsonResponse({ status: 'healthy', uptime_seconds: 1, cpa_connected: true });
    }
    if (input.endsWith('/api/v1/preferences')) {
      return jsonResponse({ preferences: {}, time_zone: { server_timezone: 'UTC' } });
    }
    if (input.endsWith('/api/v1/management/plugins')) {
      return jsonResponse({ plugins: [] });
    }
    return jsonResponse({}, 404);
  }));
});

afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
});

describe('shell chrome for an unmatched route', () => {
  it('highlights the dashboard, names its group and keeps a generic page title', async () => {
    const { container } = mount('/plugin-pages/retired/0');
    await waitFor(() => {
      expect(document.title).toBe('Oh My CPA · Management center');
    });
    const crumb = container.querySelector('.app-breadcrumb');
    expect(crumb?.textContent).toContain('Operate');
    expect(crumb?.textContent).toContain('Management center');
    expect(container.querySelector('.app-menu .ant-menu-item-selected')?.textContent).toContain('Dashboard');
  });
});

