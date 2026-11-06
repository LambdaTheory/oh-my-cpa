import { render, screen, waitFor } from '@testing-library/react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { createMemoryRouter, RouterProvider } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../src/api/client';
import { I18nProvider } from '../src/i18n';
import { RepoKeyApplyPage } from '../src/pages/repo-key/RepoKeyApplyPage';

const TOKEN = 'probe-approval-link';

function mount(isDemo: boolean) {
  window.__OMCPA_CONFIG__ = { demo: isDemo };
  // Keep the real query pending so a disabled read cannot be mistaken for a completed one.
  const read = vi.spyOn(api, 'getRepoKeyApprovalLink').mockImplementation(() => new Promise(() => {}));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } });
  const router = createMemoryRouter([
    { path: '/repo-key-apply/:token', element: <RepoKeyApplyPage /> },
  ], { initialEntries: [`/repo-key-apply/${TOKEN}`] });
  render(
    <QueryClientProvider client={client}>
      <I18nProvider><AntdApp><RouterProvider router={router} /></AntdApp></I18nProvider>
    </QueryClientProvider>,
  );
  return { client, read };
}

beforeEach(() => localStorage.setItem('omc-lang', 'en'));

it('does not mark the approval card busy when the demonstration disables its read', () => {
  const { client, read } = mount(true);
  const card = screen.getByRole('region', { name: 'Approve a repository key request' });
  expect(client.getQueryState(['repo-key-apply-link', TOKEN])?.status).toBe('pending');
  expect(client.getQueryState(['repo-key-apply-link', TOKEN])?.fetchStatus).toBe('idle');
  expect(read).not.toHaveBeenCalled();
  expect(card.getAttribute('aria-busy')).toBeNull();
});

it('marks the approval card busy while an enabled read is pending', async () => {
  const { client, read } = mount(false);
  await waitFor(() => expect(read).toHaveBeenCalledWith(TOKEN));
  expect(client.getQueryState(['repo-key-apply-link', TOKEN])?.status).toBe('pending');
  expect(client.getQueryState(['repo-key-apply-link', TOKEN])?.fetchStatus).toBe('fetching');
  expect(screen.getByRole('region', { name: 'Approve a repository key request' }).getAttribute('aria-busy')).toBe('true');
});
