import { render, screen } from '@testing-library/react';
import { createMemoryRouter, RouterProvider, Navigate, Outlet } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { staticNavGroups, buildNavGroups } from '../src/components/common/AppLayout';
import type { PluginPageEntry } from '../src/components/plugins/pluginPages';

describe('Navigation Groups', () => {
  it('does not contain /playground entry in staticNavGroups', () => {
    const allKeys = staticNavGroups.flatMap(group => group.items.map(item => item.key));
    expect(allKeys).not.toContain('/playground');

    const allLabelKeys = staticNavGroups.flatMap(group => group.items.map(item => item.labelKey));
    expect(allLabelKeys).not.toContain('nav.playground');
  });

  it('keeps only expected operate routes', () => {
    const operateGroup = staticNavGroups.find(group => group.key === 'operate');
    expect(operateGroup).toBeDefined();
    const operateKeys = operateGroup?.items.map(item => item.key);
    expect(operateKeys).toEqual(['/dashboard', '/agent', '/model-square']);
  });

  it('does not contain /playground when plugin pages are merged', () => {
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
    const navGroups = buildNavGroups([pluginPage]);
    const allKeys = navGroups.flatMap(group => group.items.map(item => item.key));
    expect(allKeys).not.toContain('/playground');
  });
});

describe('Route Navigation', () => {
  it('redirects /playground directly to /dashboard', () => {
    const routes = [
      {
        path: '/',
        element: <Outlet />,
        children: [
          { index: true, element: <Navigate to="/dashboard" replace /> },
          { path: 'dashboard', element: <div data-testid="dashboard-view">Dashboard Content</div> },
          { path: 'playground', element: <Navigate to="/dashboard" replace /> },
        ],
      },
    ];

    const router = createMemoryRouter(routes, {
      initialEntries: ['/playground'],
    });

    render(<RouterProvider router={router} />);

    expect(screen.getByTestId('dashboard-view')).not.toBeNull();
    expect(screen.getByText('Dashboard Content')).not.toBeNull();
    expect(router.state.location.pathname).toBe('/dashboard');
  });
});
