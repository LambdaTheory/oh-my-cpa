import type { PropsWithChildren } from 'react';
import { render, screen } from '@testing-library/react';
import { Outlet } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AppRoutes } from '../src/App';
import { STATIC_NAV_GROUPS, buildNavGroups } from '../src/components/common/navigation';
import type { PluginPageEntry } from '../src/components/plugins/pluginPages';

vi.mock('../src/components/common/AuthGate', () => ({
  AuthGate: ({ children }: PropsWithChildren) => <>{children}</>,
}));
vi.mock('../src/utils/TimeZoneProvider', () => ({
  TimeZoneProvider: ({ children }: PropsWithChildren) => <>{children}</>,
}));
vi.mock('../src/components/common/AppLayout', () => ({ AppLayout: () => <Outlet /> }));
vi.mock('../src/components/common/ShellLoading', () => ({ ShellLoading: () => null }));
vi.mock('../src/pages/playground/PlaygroundPage', () => ({
  PlaygroundPage: () => <div data-testid="playground-view">Playground Content</div>,
}));
vi.mock('../src/pages/DashboardPage', () => ({
  DashboardPage: () => <div data-testid="dashboard-view">Dashboard Content</div>,
}));

const pluginPage: PluginPageEntry = {
  route: '/plugin-pages/test/0',
  navLabel: 'Test Plugin',
  logo: '',
  pluginId: 'test',
  pageIndex: 0,
  pluginName: 'Test Plugin',
  label: 'Test Plugin',
  resourcePath: '/test',
};

const navKeys = (groups: ReturnType<typeof buildNavGroups>) => groups.flatMap(group => group.items.map(item => item.key));

describe('Navigation Groups', () => {
  it('offers Playground in the static operate group', () => {
    const allKeys = navKeys([...STATIC_NAV_GROUPS]);
    expect(allKeys).toContain('/playground');
    const allLabelKeys = STATIC_NAV_GROUPS.flatMap(group => group.items.map(item => item.labelKey));
    expect(allLabelKeys).toContain('nav.playground');
  });

  it('keeps the existing operate routes alongside Playground', () => {
    const operateGroup = STATIC_NAV_GROUPS.find(group => group.key === 'operate');
    expect(operateGroup).toBeDefined();
    expect(operateGroup?.items.map(item => item.key)).toEqual([
      '/dashboard', '/agent', '/playground', '/model-square', '/model-groups',
    ]);
  });

  it('keeps Playground when plugin pages are merged', () => {
    const allKeys = navKeys(buildNavGroups([pluginPage]));
    expect(allKeys).toContain('/playground');
    expect(allKeys).toContain(pluginPage.route);
  });

  it('excludes Playground from the demo rail and settings navigation model without mutating the self-hosted model', () => {
    window.__OMCPA_CONFIG__ = { demo: true };
    const demoGroups = buildNavGroups([pluginPage]);
    const demoKeys = navKeys(demoGroups);
    expect(demoKeys).not.toContain('/playground');
    expect(demoGroups.flatMap(group => group.items.map(item => item.labelKey))).not.toContain('nav.playground');
    expect(demoKeys).toContain('/agent');
    expect(demoKeys).toContain(pluginPage.route);
    window.__OMCPA_CONFIG__ = { demo: false };
    expect(navKeys(buildNavGroups([]))).toContain('/playground');
  });
});

describe('Application Route Navigation', () => {
  it.each(['/omc', '/nested/console'])('opens the real Playground route under %s when self-hosted', async basePath => {
    window.__OMCPA_CONFIG__ = { basePath, demo: false };
    window.history.replaceState({}, '', `${basePath}/playground`);
    render(<AppRoutes />);
    expect(await screen.findByTestId('playground-view')).not.toBeNull();
    expect(screen.queryByTestId('dashboard-view')).toBeNull();
    expect(window.location.pathname).toBe(`${basePath}/playground`);
  });

  it('keeps the real demo Playground route redirected without mounting the workspace', async () => {
    window.__OMCPA_CONFIG__ = { basePath: '/omc', demo: true };
    window.history.replaceState({}, '', '/omc/playground');
    render(<AppRoutes />);
    expect(await screen.findByTestId('dashboard-view')).not.toBeNull();
    expect(screen.queryByTestId('playground-view')).toBeNull();
    expect(window.location.pathname).toBe('/omc/dashboard');
  });
});
