import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { ModelGroupsPage } from '../src/pages/modelGroups/ModelGroupsPage';
import { ModelGroupDetailPage } from '../src/pages/modelGroups/ModelGroupDetailPage';
import { MODEL_GROUPS_QUERY_KEY, type ModelGroupList } from '../src/types/modelGroups';

/**
 * The decisions the group pages make that a pure function cannot: a move is optimistic, a refused
 * save restores only the order, the group's writes serialise, and a name cannot be submitted
 * twice. The server's own rules, the ordering arithmetic and the independent orders of two groups
 * are `scripts/test-model-groups.ts`'s claims.
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
let readings: unknown[];
let readingsFailure: boolean;
let readingsRequests: number;
let orderRequests: string[][];
let createRequests: string[];
let deleteRequests: string[];
let shouldRefuseOrder: boolean;

/** A held response: the request has been read, but its answer waits for `release`. */
interface Gate {
  method: string;
  pattern: RegExp;
  wait: Promise<void>;
  release: () => void;
}

let gates: Gate[];

function holdNext(method: string, pattern: RegExp): () => void {
  let release!: () => void;
  const wait = new Promise<void>((resolve) => { release = resolve; });
  gates.push({ method, pattern, wait, release });
  return release;
}

function mount(children: React.ReactNode, route: string) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  clients.push(client);
  const rendered = render(
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
  return { ...rendered, client };
}

function mountDetail() {
  return mount(
    <Routes>
      <Route path="/model-groups/:id" element={<ModelGroupDetailPage />} />
    </Routes>,
    '/model-groups/preset-labor',
  );
}

function mountList() {
  return mount(
    <Routes>
      <Route path="/model-groups" element={<ModelGroupsPage />} />
    </Routes>,
    '/model-groups',
  );
}

/** The models the detail table draws, in the order it draws them. */
function drawnOrder(): string[] {
  return [...document.querySelectorAll('[data-model-group-member]')].map((node) => node.textContent ?? '');
}

function rowOf(model: string): HTMLElement {
  return document.querySelector(`[data-model-group-member="${model}"]`)?.closest('tr') as HTMLElement;
}

function dataTransfer() {
  return { effectAllowed: '', dropEffect: '', setData: vi.fn(), getData: vi.fn(() => '') };
}

/** Drags one member's row onto another's, through the grip the page arms. */
function dragOnto(dragged: string, target: string) {
  fireEvent.pointerDown(document.querySelector(`[data-drag-handle="${dragged}"]`) as HTMLElement);
  const transfer = dataTransfer();
  fireEvent.dragStart(rowOf(dragged), { dataTransfer: transfer });
  fireEvent.dragOver(rowOf(target), { dataTransfer: transfer });
  fireEvent.drop(rowOf(target), { dataTransfer: transfer });
}

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  groups = [labor()];
  readings = [
    { model: 'claude-sonnet', availability: 'tight', balance: { amount: '3.2', currency: 'USD' } },
    { model: 'gpt-5', availability: 'available', balance: null },
  ];
  readingsFailure = false;
  readingsRequests = 0;
  orderRequests = [];
  createRequests = [];
  deleteRequests = [];
  shouldRefuseOrder = false;
  gates = [];
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
    // The bookkeeping happens before the gate: a held response has still been requested, and the
    // tests assert on the requests the page sent, not only on the answers it received.
    const gateIndex = gates.findIndex((gate) => gate.method === method && gate.pattern.test(input));
    const gate = gateIndex >= 0 ? gates.splice(gateIndex, 1)[0] : undefined;
    let response: Response;
    if (input === '/omc/api/v1/management/model-groups' && method === 'GET') {
      response = jsonResponse({ groups });
    } else if (input === '/omc/api/v1/management/model-groups/readings' && method === 'GET') {
      // The readings are a second read of the same page, not a substitute for the list: an
      // answer that fails here must leave the groups on screen.
      readingsRequests += 1;
      response = readingsFailure
        ? jsonResponse({ error: 'readings_unavailable' }, 500)
        : jsonResponse({ models: readings });
    } else if (input === '/omc/api/v1/management/model-groups' && method === 'POST') {
      const name = JSON.parse(String(options.body)).name;
      createRequests.push(name);
      const created = { id: 'created', name, is_preset: false, created_at_ms: 2, updated_at_ms: 2, members: [] };
      groups = [...groups, created];
      response = jsonResponse(created, 201);
    } else if (/\/management\/model-groups\/[^/]+$/.test(input) && method === 'PATCH') {
      const id = decodeURIComponent(input.split('/').pop() ?? '');
      const name = JSON.parse(String(options.body)).name;
      groups = (groups as ReturnType<typeof labor>[]).map((group) => (group.id === id ? { ...group, name } : group));
      response = jsonResponse((groups as ReturnType<typeof labor>[]).find((group) => group.id === id));
    } else if (/\/management\/model-groups\/[^/]+\/members\/[^/]+$/.test(input) && method === 'DELETE') {
      const model = decodeURIComponent(input.split('/').pop() ?? '');
      deleteRequests.push(model);
      const current = groups[0] as ReturnType<typeof labor>;
      groups = [{
        ...current,
        members: current.members
          .filter((entry) => entry.model !== model)
          .map((entry, index) => member(entry.model, index + 1)),
      }];
      response = jsonResponse(groups[0]);
    } else if (/\/management\/model-groups\/[^/]+\/order$/.test(input) && method === 'PUT') {
      const models: string[] = JSON.parse(String(options.body)).models;
      orderRequests.push(models);
      if (shouldRefuseOrder) {
        response = jsonResponse({ error: 'model_group_invalid_order', code: 'model_group_invalid_order' }, 400);
      } else {
        const current = groups[0] as ReturnType<typeof labor>;
        groups = [{ ...current, members: models.map((model, index) => member(model, index + 1)) }];
        response = jsonResponse(groups[0]);
      }
    } else {
      throw new Error(`Unexpected component request: ${method} ${input}`);
    }
    if (gate) await gate.wait;
    return response;
  }));
});

afterEach(() => {
  for (const client of clients.splice(0)) client.clear();
});

describe('model group readings', () => {
  /** The band counts a card draws: the band it names and the number it printed. */
  function drawnBands(): Array<[string | null, string]> {
    return [...document.querySelectorAll('[data-model-group-band]')].map((node) => [
      node.getAttribute('data-model-group-band'),
      node.lastElementChild?.textContent ?? '',
    ]);
  }

  it('counts a card’s members by band and states the preferred model’s balance', async () => {
    mountList();
    await screen.findByText('Labor');

    await waitFor(() => expect(drawnBands()).toEqual([
      ['available', '1'],
      ['tight', '1'],
      ['unknown', '0'],
      ['unavailable', '0'],
    ]));
    const preferred = document.querySelector('[data-model-group-preferred]') as HTMLElement;
    // The first member is the group's preference, and its reading is what the card prints.
    expect(preferred.getAttribute('data-model-group-preferred')).toBe('claude-sonnet');
    expect(within(preferred).getByText('claude-sonnet')).toBeTruthy();
    expect(within(preferred).getByText('$3.20')).toBeTruthy();
  });

  it('draws each member’s band and balance on the detail table', async () => {
    mountDetail();
    await screen.findByText('claude-sonnet');

    await waitFor(() => expect(within(rowOf('claude-sonnet')).getByText('Usable, nearly spent')).toBeTruthy());
    expect(within(rowOf('claude-sonnet')).getByText('$3.20')).toBeTruthy();
    expect(within(rowOf('gpt-5')).getByText('Available')).toBeTruthy();
    // A member with no published balance draws the blank cell, not a zero.
    expect(within(rowOf('gpt-5')).getByText('—')).toBeTruthy();
  });

  it('draws a member the server did not report as unknown with no balance, rather than inventing one', async () => {
    groups = [{
      ...labor(),
      members: [member('claude-sonnet', 1), member('gemini', 2)],
    }];
    mountDetail();
    await screen.findByText('gemini');

    // A name with no reading is the same answer as a reading the server could not measure:
    // the unknown band and the blank balance cell.
    await waitFor(() => expect(within(rowOf('gemini')).getByText('Unknown')).toBeTruthy());
    expect(within(rowOf('gemini')).getByText('—')).toBeTruthy();
    expect(within(rowOf('claude-sonnet')).getByText('Usable, nearly spent')).toBeTruthy();
  });

  it('keeps the groups on screen and says so when the readings cannot be read', async () => {
    readingsFailure = true;
    mountList();
    await screen.findByText('Labor');

    await screen.findByText('Could not read availability and balances');
    expect(readingsRequests).toBeGreaterThan(0);
    // No reading means no band counts: the card must not draw four zeroes as if it had looked.
    expect(drawnBands()).toEqual([]);
    expect(screen.getByRole('button', { name: 'Open' })).toBeTruthy();
  });
});

describe('model group detail order', () => {
  it('saves a drag on drop and shows the new order while the response is held', async () => {
    mountDetail();
    await screen.findByText('claude-sonnet');
    expect(drawnOrder()).toEqual(['claude-sonnet', 'gpt-5']);

    const release = holdNext('PUT', /\/order$/);
    dragOnto('claude-sonnet', 'gpt-5');

    await waitFor(() => expect(orderRequests).toEqual([['gpt-5', 'claude-sonnet']]));
    await waitFor(() => expect(drawnOrder()).toEqual(['gpt-5', 'claude-sonnet']));
    // The keyboard route survives dragging: the move buttons are still there after the drop.
    expect(screen.getByRole('button', { name: 'Move gpt-5 up' })).toBeTruthy();

    release();
    await waitFor(() => expect(drawnOrder()).toEqual(['gpt-5', 'claude-sonnet']));
  });

  it('puts the last saved order back and says so when the save is refused', async () => {
    mountDetail();
    await screen.findByText('claude-sonnet');
    shouldRefuseOrder = true;

    fireEvent.click(screen.getByRole('button', { name: 'Move claude-sonnet down' }));
    await screen.findByText('The order was not saved');
    expect(drawnOrder()).toEqual(['claude-sonnet', 'gpt-5']);
    await waitFor(() => expect(screen.getByRole('button', { name: 'Retry' })).toBeTruthy());
  });

  it('sends one reorder per Retry press while the previous answer is held', async () => {
    mountDetail();
    await screen.findByText('claude-sonnet');
    shouldRefuseOrder = true;
    fireEvent.click(screen.getByRole('button', { name: 'Move claude-sonnet down' }));
    await screen.findByText('The order was not saved');

    shouldRefuseOrder = false;
    const release = holdNext('PUT', /\/order$/);
    const retry = screen.getByRole('button', { name: 'Retry' });
    fireEvent.click(retry);
    await waitFor(() => expect(orderRequests).toHaveLength(2));
    await waitFor(() => expect((retry as HTMLButtonElement).disabled).toBe(true));
    fireEvent.click(retry);
    expect(orderRequests).toHaveLength(2);

    release();
    await waitFor(() => expect(screen.queryByText('The order was not saved')).toBeNull());
  });

  it('does not start a remove while a reorder is in flight, and recovers after it settles', async () => {
    mountDetail();
    await screen.findByText('claude-sonnet');

    shouldRefuseOrder = true;
    const release = holdNext('PUT', /\/order$/);
    fireEvent.click(screen.getByRole('button', { name: 'Move claude-sonnet down' }));
    await waitFor(() => expect(orderRequests).toHaveLength(1));

    const removeButton = screen.getByRole('button', { name: 'Remove claude-sonnet' }) as HTMLButtonElement;
    await waitFor(() => expect(removeButton.disabled).toBe(true));
    fireEvent.click(removeButton);
    expect(deleteRequests).toEqual([]);

    release();
    await screen.findByText('The order was not saved');
    expect(drawnOrder()).toEqual(['claude-sonnet', 'gpt-5']);
    await waitFor(() => expect(removeButton.disabled).toBe(false));

    fireEvent.click(removeButton);
    await waitFor(() => expect(deleteRequests).toEqual(['claude-sonnet']));
    await waitFor(() => expect(drawnOrder()).toEqual(['gpt-5']));
  });

  it('a refused order does not bring back a member the group no longer holds', async () => {
    const { client } = mountDetail();
    await screen.findByText('claude-sonnet');

    shouldRefuseOrder = true;
    const release = holdNext('PUT', /\/order$/);
    fireEvent.click(screen.getByRole('button', { name: 'Move claude-sonnet down' }));
    await waitFor(() => expect(orderRequests).toHaveLength(1));

    // A membership change that landed in the cache after the move was captured, as a refresh or
    // another write would deliver it. The rollback must not resurrect the removed member.
    act(() => {
      client.setQueryData<ModelGroupList>(MODEL_GROUPS_QUERY_KEY, (current) => ({
        groups: (current?.groups ?? []).map((group) => group.id === 'preset-labor'
          ? { ...group, members: group.members.filter((entry) => entry.model !== 'gpt-5') }
          : group),
      }));
    });
    release();
    await screen.findByText('The order was not saved');
    await waitFor(() => expect(drawnOrder()).toEqual(['claude-sonnet']));
  });
});

describe('model group name dialog', () => {
  it('refuses a name the list already uses without sending a request', async () => {
    mountList();
    await screen.findByText('Labor');
    fireEvent.click(screen.getByRole('button', { name: 'New group' }));

    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Group name'), { target: { value: 'labor' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'New group' }));

    await within(dialog).findByText('A group with this name already exists');
    expect(createRequests).toEqual([]);
  });

  it('renames a group from its card', async () => {
    mountList();
    await screen.findByText('Labor');
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));

    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Group name'), { target: { value: 'Labor 2' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));

    await screen.findByText('Labor 2');
    expect(screen.queryByText('Labor')).toBeNull();
  });

  it('creates the group once the name is free', async () => {
    mountList();
    await screen.findByText('Labor');
    fireEvent.click(screen.getByRole('button', { name: 'New group' }));

    const dialog = await screen.findByRole('dialog');
    fireEvent.change(within(dialog).getByLabelText('Group name'), { target: { value: '  Frontend  ' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'New group' }));

    await waitFor(() => expect(createRequests).toEqual(['Frontend']));
    await screen.findByText('Frontend');
  });

  it('sends one create when Enter is pressed again while the first response is held', async () => {
    mountList();
    await screen.findByText('Labor');
    fireEvent.click(screen.getByRole('button', { name: 'New group' }));

    const dialog = await screen.findByRole('dialog');
    const input = within(dialog).getByLabelText('Group name') as HTMLInputElement;
    fireEvent.change(input, { target: { value: 'Frontend' } });

    const release = holdNext('POST', /\/management\/model-groups$/);
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(createRequests).toEqual(['Frontend']));
    await waitFor(() => expect(input.disabled).toBe(true));

    // A second Enter, a click on the saving button and a click on Cancel are all no-ops while the
    // first request is in flight; editing is closed for the same reason.
    fireEvent.keyDown(input, { key: 'Enter' });
    fireEvent.click(within(dialog).getByText('New group').closest('button') as HTMLButtonElement);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
    expect(createRequests).toEqual(['Frontend']);
    expect(screen.getByRole('dialog')).toBeTruthy();

    release();
    // The created group is the durable evidence: the earlier answer can no longer have closed a
    // dialog that was still submitting, because there was never a second submission.
    await screen.findByText('Frontend');
    expect(createRequests).toEqual(['Frontend']);
  });
});
