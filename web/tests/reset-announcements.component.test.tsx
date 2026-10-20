import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { ResetAnnouncements } from '../src/pages/quota/ResetAnnouncements';
import type { ResetAnnouncement } from '../src/types/resetAnnouncements';

const item = (overrides: Partial<ResetAnnouncement> = {}): ResetAnnouncement => ({
  id: 'announcement-1', provider: 'codex', source: 'manual', expected_reset_at_ms: null,
  scope: 'all', model: '', auth_index: '', confidence: 'confirmed', original_text: 'Public reset notice',
  registered_at_ms: Date.now(), created_at_ms: Date.now(), status: 'active', ...overrides,
});

beforeEach(() => {
  window.localStorage.setItem('omc-lang', 'en');
  vi.stubGlobal('ResizeObserver', class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal('matchMedia', () => ({ matches: false, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {} }));
});

function mount() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(<QueryClientProvider client={client}><I18nProvider><AntdApp><ResetAnnouncements /></AntdApp></I18nProvider></QueryClientProvider>);
}

it.each([false, true])('delays first-load feedback and keeps loaded content during refresh (slow read: %s)', async (isSlowRead) => {
  vi.useFakeTimers();
  let resolveRead!: (response: Response) => void;
  const fetcher = vi.fn(() => new Promise<Response>((resolve) => { resolveRead = resolve; }));
  vi.stubGlobal('fetch', fetcher);
  const view = mount();
  try {
    const region = screen.getByRole('region', { name: 'Active reset announcements' });
    expect(region.classList.contains('panel')).toBe(true);
    expect(within(region).queryByRole('status')).toBeNull();
    expect(within(region).queryByText('No active announcements')).toBeNull();
    await act(() => vi.advanceTimersByTimeAsync(199));
    expect(within(region).queryByRole('status')).toBeNull();
    if (isSlowRead) {
      await act(() => vi.advanceTimersByTimeAsync(1));
      expect(within(region).getByRole('status', { name: 'Loading…' }).classList.contains('block-loading')).toBe(true);
    }
    await act(async () => {
      resolveRead(new Response(JSON.stringify({ items: [], total: 0 })));
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(within(region).queryByRole('status')).toBeNull();
    expect(within(region).getByText('No active announcements').closest('.empty-state')?.classList.contains('is-compact')).toBe(true);
    fireEvent.click(within(region).getByRole('button', { name: 'Refresh' }));
    expect(fetcher).toHaveBeenCalledTimes(2);
    await act(() => vi.advanceTimersByTimeAsync(200));
    expect(within(region).queryByRole('status')).toBeNull();
    expect(within(region).getByText('No active announcements')).toBeTruthy();
    await act(async () => {
      resolveRead(new Response(JSON.stringify({ items: [item()], total: 1 })));
      await vi.advanceTimersByTimeAsync(1);
    });
    expect(within(region).getByText('Public reset notice')).toBeTruthy();
  } finally {
    view.unmount();
    vi.useRealTimers();
  }
});

it('validates required provider, confidence and future time, then posts and reloads active announcements', async () => {
  const items: ResetAnnouncement[] = [];
  const writes: unknown[] = [];
  const fetcher = vi.fn(async (input: RequestInfo | URL, options?: RequestInit) => {
    expect(String(input)).toContain('/api/v1/management/quota/reset-announcements');
    if (options?.method === 'POST') {
      const body = JSON.parse(String(options.body)); writes.push(body); items.push(item(body));
      return new Response(JSON.stringify(items[0]), { status: 201 });
    }
    expect(String(input)).toContain('status=active');
    return new Response(JSON.stringify({ items, total: items.length }));
  });
  vi.stubGlobal('fetch', fetcher);
  mount();
  await screen.findByText('No active announcements');
  fireEvent.click(screen.getByRole('button', { name: 'Register reset announcement' }));
  const dialog = await screen.findByRole('dialog');
  const submit = within(dialog).getByRole('button', { name: 'Register' });
  fireEvent.click(submit);
  await screen.findAllByText('This field is required');
  expect(writes).toHaveLength(0);
  fireEvent.change(screen.getByLabelText('Provider identifier'), { target: { value: 'Codex' } });
  await screen.findByText(/Start with a lowercase letter/);
  fireEvent.change(screen.getByLabelText('Provider identifier'), { target: { value: 'codex' } });
  fireEvent.change(screen.getByLabelText('Original announcement'), { target: { value: 'Public reset notice' } });
  fireEvent.mouseDown(screen.getByRole('combobox', { name: 'Confidence' }));
  fireEvent.click(await screen.findByText('Confirmed', { selector: '.ant-select-item-option-content' }));
  fireEvent.change(screen.getByLabelText('Expected reset time (UTC, optional)'), { target: { value: '2000-01-01T12:00' } });
  fireEvent.click(submit);
  await screen.findByText('Choose a future time or leave blank');
  expect(writes).toHaveLength(0);
  fireEvent.change(screen.getByLabelText('Expected reset time (UTC, optional)'), { target: { value: '' } });
  fireEvent.click(submit);
  await waitFor(() => expect(writes).toHaveLength(1));
  expect(writes[0]).toEqual({ provider: 'codex', source: 'manual', expected_reset_at_ms: null, scope: 'all', model: '', auth_index: '', confidence: 'confirmed', original_text: 'Public reset notice' });
  const region = screen.getByRole('region', { name: 'Active reset announcements' });
  await within(region).findByText('Public reset notice');
  expect(within(region).getByText('Source: Manual')).toBeTruthy();
});

it('hides expired announcements, including a cached active row whose time has passed', async () => {
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ items: [
    item({ id: 'expired', status: 'expired', original_text: 'Expired notice' }),
    item({ id: 'cached', expected_reset_at_ms: Date.now() - 1000, original_text: 'Cached past notice' }),
    item({ id: 'unknown', confidence: 'unverified', original_text: 'Untimed evidence' }),
  ], total: 3 }))));
  mount();
  await screen.findByText('Untimed evidence');
  expect(screen.queryByText('Expired notice')).toBeNull();
  expect(screen.queryByText('Cached past notice')).toBeNull();
  expect(screen.getByText('Confidence: Unverified')).toBeTruthy();
});
