import React from 'react';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { I18nProvider } from '../src/i18n';
import { RepoKeyApplyPage } from '../src/pages/repo-key/RepoKeyApplyPage';

vi.mock('../src/types/demoMode', () => ({ isDemoMode: () => true }));

it('renders the read-only demo approval route without a pending network read', () => {
  window.localStorage.setItem('omc-lang', 'en');
  const fetch = vi.fn();
  vi.stubGlobal('fetch', fetch);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: Infinity } } });
  const view = render(
    <QueryClientProvider client={client}>
      <I18nProvider>
        <AntdApp>
          <MemoryRouter initialEntries={['/repo-key-apply/:token']}>
            <Routes><Route path="/repo-key-apply/:token" element={<RepoKeyApplyPage />} /></Routes>
          </MemoryRouter>
        </AntdApp>
      </I18nProvider>
    </QueryClientProvider>,
  );
  try {
    expect(screen.getByRole('heading', { name: 'Approve a repository key request' })).toBeTruthy();
    expect(screen.getByText(/One key per repository/)).toBeTruthy();
    const card = screen.getByRole('region', { name: 'Approve a repository key request' });
    expect(card.getAttribute('aria-busy')).not.toBe('true');
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.queryByRole('button', { name: 'Approve this request' })).toBeNull();
  } finally {
    view.unmount();
    client.clear();
  }
});
