import type { PropsWithChildren } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { render, screen } from '@testing-library/react';
import { Outlet } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { AppRoutes } from '../src/App';
import { STATIC_NAV_GROUPS, buildNavGroups } from '../src/components/common/navigation';
import type { PluginPageEntry } from '../src/components/plugins/pluginPages';
import { shellNavGroupSizes } from '../src/types/navGroupSizes';
import { resolveNavLocation, retainVisibleGroups } from '../src/types/sidebarNav';

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
      '/dashboard', '/agent', '/playground', '/model-square',
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

describe('Capabilities group', () => {
  const groupKeys = (groups: ReturnType<typeof buildNavGroups>) => groups.map((group) => group.key);

  it('orders the six groups with Capabilities between Observe and Plugins', () => {
    expect(groupKeys(buildNavGroups([]))).toEqual([
      'operate', 'gateway', 'observe', 'capabilities', 'plugins', 'control',
    ]);
  });

  it('holds model groups, repository keys and quota lending in that order', () => {
    const capabilities = buildNavGroups([]).find((group) => group.key === 'capabilities');
    expect(capabilities?.labelKey).toBe('nav.group.capabilities');
    expect(capabilities?.items.map((item) => item.key)).toEqual([
      '/model-groups', '/repo-keys', '/quota-lending',
    ]);
    expect(capabilities?.items.map((item) => item.labelKey)).toEqual([
      'nav.model_groups', 'repo_keys.title', 'nav.quota_lending',
    ]);
  });

  it('keeps Capabilities in the demo rail while the Playground stays closed', () => {
    window.__OMCPA_CONFIG__ = { demo: true };
    const groups = buildNavGroups([]);
    expect(navKeys(groups)).not.toContain('/playground');
    expect(groups.find((group) => group.key === 'capabilities')?.items.map((item) => item.key)).toEqual([
      '/model-groups', '/repo-keys', '/quota-lending',
    ]);
    window.__OMCPA_CONFIG__ = { demo: false };
  });

  it('pins the placeholder group sizes to the real rail, demo included', () => {
    window.__OMCPA_CONFIG__ = { demo: false };
    expect(shellNavGroupSizes(false)).toEqual(buildNavGroups([]).map((group) => group.items.length));
    window.__OMCPA_CONFIG__ = { demo: true };
    expect(shellNavGroupSizes(true)).toEqual(buildNavGroups([]).map((group) => group.items.length));
    window.__OMCPA_CONFIG__ = { demo: false };
  });

  it('flattens the collapsed rail in group order', () => {
    expect(navKeys(buildNavGroups([]))).toEqual([
      '/dashboard', '/agent', '/playground', '/model-square',
      '/ai-providers', '/oauth-management', '/api-keys',
      '/insights', '/usage/events', '/pricing', '/logs', '/audit',
      '/model-groups', '/repo-keys', '/quota-lending',
      '/plugins', '/plugins/store',
      '/config', '/omc-settings', '/system',
    ]);
  });

  it('gives every fixed entry its own graphic', () => {
    // `createIcon` gives each export its own component and default class, so two exports that
    // draw the same Lucide glyph have different types. Compare the rendered graphic without
    // the class names that only name the alias.
    const fixedEntries = (isDemo: boolean) => {
      window.__OMCPA_CONFIG__ = { demo: isDemo };
      const groups = buildNavGroups([]);
      const plugins = groups.find((group) => group.key === 'plugins');
      return groups.filter((group) => group.key !== 'plugins').flatMap((group) => group.items)
        .concat(plugins ? plugins.items.slice(0, 2) : []);
    };
    for (const isDemo of [false, true]) {
      const entries = fixedEntries(isDemo);
      const seen = new Map<string, string>();
      for (const entry of entries) {
        const graphic = renderToStaticMarkup(entry.icon).replace(/\sclass="[^"]*"/g, '');
        const drawnBy = seen.get(graphic);
        expect(drawnBy, `${entry.key} draws the same graphic as ${drawnBy}`).toBeUndefined();
        seen.set(graphic, entry.key);
      }
      expect(seen.size).toBe(entries.length);
    }
    window.__OMCPA_CONFIG__ = { demo: false };
  });

  it('drops the Capabilities heading when its every entry is hidden', () => {
    const visible = retainVisibleGroups(buildNavGroups([]), new Set(['/model-groups', '/repo-keys', '/quota-lending']));
    expect(visible.map((group) => group.key)).toEqual(['operate', 'gateway', 'observe', 'plugins', 'control']);
  });
});

describe('Nav location resolution', () => {
  const groups = () => buildNavGroups([]);

  it('resolves model group sub-pages to Model Groups under Capabilities', () => {
    for (const pathname of ['/model-groups', '/model-groups/custom', '/model-groups/custom/add']) {
      const { entry, group } = resolveNavLocation(groups(), pathname);
      expect(entry?.key).toBe('/model-groups');
      expect(group?.key).toBe('capabilities');
      expect(group?.labelKey).toBe('nav.group.capabilities');
      expect(entry?.labelKey).toBe('nav.model_groups');
    }
  });

  it('resolves repository keys and quota lending to their own entries', () => {
    const repo = resolveNavLocation(groups(), '/repo-keys');
    expect(repo.entry?.key).toBe('/repo-keys');
    expect(repo.group?.key).toBe('capabilities');

    const lending = resolveNavLocation(groups(), '/quota-lending');
    expect(lending.entry?.key).toBe('/quota-lending');
    expect(lending.entry?.labelKey).toBe('nav.quota_lending');
    expect(lending.group?.key).toBe('capabilities');
    // A trailing slash is the same page and must not fall back to another entry.
    expect(resolveNavLocation(groups(), '/quota-lending/').entry?.key).toBe('/quota-lending');
  });

  it('still names the group of an entry the operator hid', () => {
    const model = groups();
    const visibleKeys = retainVisibleGroups(model, new Set(['/model-groups']))
      .flatMap((group) => group.items.map((item) => item.key));
    expect(visibleKeys).not.toContain('/model-groups');
    expect(visibleKeys).toContain('/repo-keys');
    // Resolution reads the full model, so a page opened by URL still writes its breadcrumb.
    const { entry, group } = resolveNavLocation(model, '/model-groups/custom');
    expect(entry?.key).toBe('/model-groups');
    expect(group?.labelKey).toBe('nav.group.capabilities');
  });

  it('prefers the longest matching entry', () => {
    expect(resolveNavLocation(groups(), '/plugins/store').entry?.key).toBe('/plugins/store');
    expect(resolveNavLocation(groups(), '/plugins').entry?.key).toBe('/plugins');
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
