import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { ModelGroupsPage } from '../src/pages/modelGroups/ModelGroupsPage';
import { ModelGroupDetailPage } from '../src/pages/modelGroups/ModelGroupDetailPage';

/**
 * The two decisions the group pages make that a pure function cannot: a move is optimistic and a
 * refused save must put the last stored order back, and a name the operator can already see is
 * refused before a request is sent. The server's own rules, the ordering arithmetic and the
 * independent orders of two groups are `scripts/test-model-groups.ts`'s claims.
 */

const clients: QueryClient[] = [];

function jsonResponse(value: unknown, status = 200) {
  return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
}

function member(model: string, position: number) {
  return { model, position, added_at_ms: position };
}

function labor() {
  return {
    id: 'preset-labor',
    name: 'Labor',
    is_preset: true,
    created_at_ms: 1,
    updated_at_ms: 1,
    members: [member('claude-sonnet', 1), member('gpt-5', 2)],
  };
}

let groups: unknown[];
let orderRequests: string[][];
let createRequests: string[];
let shouldRefuseOrder: boolean;

function mount(children: React.ReactNode, route: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  clients.push(client);
  return render(
    <React.StrictMode>
      <QueryClientProvider client={client}>
        <I18nProvider>
          <AntdApp>
            <MemoryRouter initialEntries={[route]}>{children}</MemoryRouter>
          </AntdApp>
        </I18nProvider>
      </QueryClientProvider>
    </React.StrictMode>,
  );
}

/** The models the detail table draws, in the order it draws them. */
function drawnOrder(): string[] {
  return [...document.querySelectorAll('[data-model-group-member]')].map((node) => node.textContent ?? '');
}

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  groups = [labor()];
  orderRequests = [];
  createRequests = [];
  shouldRefuseOrder = false;
  vi.stubGlobal('matchMedia', (query: string) => ({
    matches: false, media: query, onchange: null,
    addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {}, dispatchEvent: () => false,
  }));
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  });
  vi.stubGlobal('fetch', vi.fn(async (input: string, options: RequestInit = {}) => {
    const method = (options.method ?? 'GET').toUpperCase();
    if (input === '/omc/api/v1/management/model-groups' && method === 'GET') {
      return jsonResponse({ groups });
    }
    if (input === '/omc/api/v1/management/model-groups' && method === 'POST') {
      const name = JSON.parse(String(options.body)).name;
      createRequests.push(name);
      const created = { id: 'created', name, is_preset: false, created_at_ms: 2, updated_at_ms: 2, members: [] };
      groups = [...groups, created];
      return jsonResponse(created, 201);
    }
    if (/\/management\/model-groups\/[^/]+$/.test(input) && method === 'PATCH') {
      const id = decodeURIComponent(input.split('/').pop() ?? '');
      const name = JSON.parse(String(options.body)).name;
      groups = (groups as ReturnType<typeof labor>[]).map((group) => (group.id === id ? { ...group, name } : group));
      return jsonResponse((groups as ReturnType<typeof labor>[]).find((group) => group.id === id));
    }
    if (/\/management\/model-groups\/[^/]+\/order$/.test(input) && method === 'PUT') {
      const models: string[] = JSON.parse(String(options.body)).models;
      orderRequests.push(models);
      if (shouldRefuseOrder) return jsonResponse({ error: 'model_group_invalid_order', code: 'model_group_invalid_order' }, 400);
      const current = groups[0] as ReturnType<typeof labor>;
      groups = [{ ...current, members: models.map((model, index) => member(model, index + 1)) }];
      return jsonResponse(groups[0]);
    }
    throw new Error(`Unexpected component request: ${method} ${input}`);
  }));
});

afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
});

describe('model group detail order', () => {
  it('saves a move and reflects it while the write is in flight', async () => {
    mount(
      <Routes>
        <Route path="/model-groups/:id" element={<ModelGroupDetailPage />} />
      </Routes>,
      '/model-groups/preset-labor',
    );
    await screen.findByText('claude-sonnet');
    expect(drawnOrder()).toEqual(['claude-sonnet', 'gpt-5']);

    fireEvent.click(screen.getByRole('button', { name: 'Move claude-sonnet down' }));
    await waitFor(() => expect(orderRequests).toEqual([['gpt-5', 'claude-sonnet']]));
    await waitFor(() => expect(drawnOrder()).toEqual(['gpt-5', 'claude-sonnet']));
  });

  it('puts the last saved order back and says so when the save is refused', async () => {
    mount(
      <Routes>
        <Route path="/model-groups/:id" element={<ModelGroupDetailPage />} />
      </Routes>,
      '/model-groups/preset-labor',
    );
    await screen.findByText('claude-sonnet');
    shouldRefuseOrder = true;

    fireEvent.click(screen.getByRole('button', { name: 'Move claude-sonnet down' }));
    await screen.findByText('The order was not saved');
    expect(drawnOrder()).toEqual(['claude-sonnet', 'gpt-5']);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy());
  });
});

describe('model group name dialog', () => {
  it('refuses a name the list already uses without sending a request', async () => {
    mount(
      <Routes>
        <Route path="/model-groups" element={<ModelGroupsPage />} />
      </Routes>,
      '/model-groups',
    );
    await screen.findByText('Labor');
    fireEvent.click(screen.getByRole('button', { name: 'New group' }));

    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Group name'), { target: { value: 'labor' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'New group' }));

    await within(dialog).findByText('A group with this name already exists');
    expect(createRequests).toEqual([]);
  });

  it('renames a group from its card', async () => {
    mount(
      <Routes>
        <Route path="/model-groups" element={<ModelGroupsPage />} />
      </Routes>,
      '/model-groups',
    );
    await screen.findByText('Labor');
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));

    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Group name'), { target: { value: 'Labor 2' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await screen.findByText('Labor 2');
    expect(screen.queryByText('Labor')).toBeNull();
  });

  it('creates the group once the name is free', async () => {
    mount(
      <Routes>
        <Route path="/model-groups" element={<ModelGroupsPage />} />
      </Routes>,
      '/model-groups',
    );
    await screen.findByText('Labor');
    fireEvent.click(screen.getByRole('button', { name: 'New group' }));

    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Group name'), { target: { value: '  Frontend  ' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'New group' }));

    await waitFor(() => expect(createRequests).toEqual(['Frontend']));
    await screen.findByText('Frontend');
  });
});
