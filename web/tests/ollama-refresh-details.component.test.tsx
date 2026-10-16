import React from 'react';
import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { App as AntdApp } from 'antd';
import { I18nProvider } from '../src/i18n';
import { OllamaDetailDrawer } from '../src/components/ollama/OllamaDetailDrawer';
import { executeOllamaAccountRefresh } from '../src/utils/ollamaRefresh';
import { CompactQuotaView } from '../src/pages/quota/CompactQuotaView';
import { api, ApiError } from '../src/api/client';
import type { OllamaAccountDTO } from '../src/types/ollama';
import type { QuotaItem } from '../src/types/quota';

vi.mock('../src/api/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../src/api/client')>();
  return {
    ...actual,
    api: {
      ...actual.api,
      refreshOllamaAccount: vi.fn(),
      updateOllamaAccount: vi.fn(),
      deleteOllamaAccount: vi.fn(),
    },
  };
});

function renderWithClient(ui: React.ReactElement, queryClient = new QueryClient()) {
  return render(
    <I18nProvider>
      <AntdApp>
        <QueryClientProvider client={queryClient}>
          {ui}
        </QueryClientProvider>
      </AntdApp>
    </I18nProvider>
  );
}

function mockQuotaItem(overrides: Partial<QuotaItem> = {}): QuotaItem {
  return {
    auth_index: 'test:default',
    name: 'Default Key',
    type: 'codex',
    provider: 'codex',
    disabled: false,
    status: 'healthy',
    observed_at_ms: Date.now(),
    windows: [],
    quota_exceeded: false,
    recommendation: {
      status: 'healthy',
      priority: 'none',
      action: 'none',
      reason: '',
    },
    capabilities: {
      refresh_supported: true,
      clear_cooldown_supported: true,
      reset_credit_supported: false,
    },
    ...overrides,
  };
}

describe('Ollama Refresh & Detail Drawer Acceptance Tests', () => {
  let queryClient: QueryClient;
  const mockToast = {
    success: vi.fn(),
    warning: vi.fn(),
    error: vi.fn(),
    info: vi.fn(),
  };

  beforeEach(() => {
    queryClient = new QueryClient({
      defaultOptions: {
        queries: { retry: false },
      },
    });
    vi.clearAllMocks();
  });

  describe('executeOllamaAccountRefresh logic', () => {
    it('handles refreshed: true and status: ok as successful refresh', async () => {
      const refreshedAccount: OllamaAccountDTO = {
        id: 'acc_1',
        label: 'Account 1',
        enabled: true,
        has_cookie: true,
        status: 'ok',
        last_observed_at_ms: Date.now(),
        last_success_at_ms: Date.now(),
        created_at_ms: Date.now() - 1000,
        updated_at_ms: Date.now(),
        attempt_count: 0,
      };

      vi.mocked(api.refreshOllamaAccount).mockResolvedValueOnce({
        account: refreshedAccount,
        refreshed: true,
        retry_at_ms: null,
      });

      const res = await executeOllamaAccountRefresh('acc_1', {
        queryClient,
        t: ((k: string) => k) as any,
        toast: mockToast,
      });

      expect(res.success).toBe(true);
      expect(res.refreshed).toBe(true);
      expect(mockToast.success).toHaveBeenCalledWith('ollama.refresh_success');
    });

    it('distinguishes healthy vs failed scrape: does not report success when status is error', async () => {
      const failedAccount: OllamaAccountDTO = {
        id: 'acc_err',
        label: 'Account Error',
        enabled: true,
        has_cookie: true,
        status: 'auth_invalid',
        newest_error: 'Invalid session token',
        last_observed_at_ms: Date.now(),
        created_at_ms: Date.now() - 1000,
        updated_at_ms: Date.now(),
        attempt_count: 1,
      };

      vi.mocked(api.refreshOllamaAccount).mockResolvedValueOnce({
        account: failedAccount,
        refreshed: true,
        retry_at_ms: null,
      });

      const res = await executeOllamaAccountRefresh('acc_err', {
        queryClient,
        t: ((k: string, p?: { reason?: string }) => (p?.reason ? `${k}: ${p.reason}` : k)) as any,
        toast: mockToast,
      });

      expect(res.success).toBe(false);
      expect(mockToast.success).not.toHaveBeenCalled();
      expect(mockToast.warning).toHaveBeenCalledWith(
        expect.stringContaining('Invalid session token')
      );
    });

    it('handles 429 rate limit with retry_at_ms and warns with countdown', async () => {
      const retryTime = Date.now() + 60_000;
      vi.mocked(api.refreshOllamaAccount).mockRejectedValueOnce(
        new ApiError('Rate limited', 429, {
          retry_at_ms: retryTime,
          account: { id: 'acc_429', label: 'Rate limited' },
        })
      );

      const res = await executeOllamaAccountRefresh('acc_429', {
        queryClient,
        t: (k: string, p?: Record<string, string | number>) => `${k}${JSON.stringify(p || {})}`,
        toast: mockToast,
      });

      expect(res.success).toBe(false);
      expect(res.refreshed).toBe(false);
      expect(res.error).toBe('rate_limited');
      expect(res.retryAtMS).toBe(retryTime);
      expect(mockToast.warning).toHaveBeenCalled();
    });

    it('handles 503 collector unavailable by indicating service currently unavailable', async () => {
      vi.mocked(api.refreshOllamaAccount).mockRejectedValueOnce(
        new ApiError('Service unavailable', 503)
      );

      const res = await executeOllamaAccountRefresh('acc_503', {
        queryClient,
        t: (k: string, p?: Record<string, string | number>) => (p && Object.keys(p).length > 0 ? `${k}${JSON.stringify(p)}` : k),
        toast: mockToast,
      });

      expect(res.success).toBe(false);
      expect(res.refreshed).toBe(false);
      expect(res.error).toBe('service_disabled');
      expect(mockToast.error).toHaveBeenCalledWith('ollama.refresh_service_disabled');
    });
  });

  describe('OllamaDetailDrawer UI rendering & isolation', () => {
    const mockAccount: OllamaAccountDTO = {
      id: 'internal_secret_uuid_12345',
      label: 'Production Ollama Worker',
      enabled: true,
      has_cookie: true,
      status: 'ok',
      last_observed_at_ms: Date.now() - 30_000,
      last_success_at_ms: Date.now() - 30_000,
      created_at_ms: Date.now() - 86400_000,
      updated_at_ms: Date.now() - 30_000,
      attempt_count: 0,
    };

    it('hides raw account id and internal ollama:{id} index from visible UI', () => {
      renderWithClient(
        <OllamaDetailDrawer
          open={true}
          onClose={() => {}}
          account={mockAccount}
        />
      );

      // Label is visible
      expect(screen.getAllByText('Production Ollama Worker').length).toBeGreaterThan(0);
      // Raw internal ID and internal prefix are NOT rendered in text
      expect(screen.queryByText('internal_secret_uuid_12345')).toBeNull();
      expect(screen.queryByText('ollama:internal_secret_uuid_12345')).toBeNull();
    });

    it('clearly communicates inference is not connected and does not fabricate proxy 0 tokens', () => {
      renderWithClient(
        <OllamaDetailDrawer
          open={true}
          onClose={() => {}}
          account={mockAccount}
        />
      );

      // Inference telemetry shows dash — and tags "未接入 CPA 推理代理" or "Not connected"
      expect(screen.getAllByText('—').length).toBeGreaterThan(0);
      expect(screen.getAllByText(/未接入 CPA 推理代理|Not connected to CPA proxy/).length).toBeGreaterThan(0);
    });

    it('displays dedicated last success label without colon trimming artifacts', () => {
      renderWithClient(
        <OllamaDetailDrawer
          open={true}
          onClose={() => {}}
          account={mockAccount}
        />
      );

      // col_last_success label is used
      expect(screen.getByText(/上次成功采集|Last successful collection/)).toBeDefined();
    });
  });

  describe('Codex balance rendering in CompactQuotaView', () => {
    it('renders credit balance when balance is 0 without swallowing', () => {
      const quotaWithZeroBalance: QuotaItem = mockQuotaItem({
        auth_index: 'codex:zero_bal',
        plan: {
          plan_type: 'tier',
          tier: 'free',
          plan_label: 'Codex Pro',
          credits: {
            balance: '0',
            unlimited: false,
          },
        },
      });

      const { container } = renderWithClient(<CompactQuotaView item={quotaWithZeroBalance} nowMS={Date.now()} />);
      const creditEl = container.querySelector('[data-credit-balance="true"]');
      expect(creditEl).not.toBeNull();
      expect(creditEl?.textContent).toContain('0');
    });

    it('renders unlimited when plan.credits.unlimited is true', () => {
      const quotaUnlimited: QuotaItem = mockQuotaItem({
        auth_index: 'codex:unlimited',
        plan: {
          plan_type: 'tier',
          tier: 'premium',
          plan_label: 'Enterprise',
          credits: {
            unlimited: true,
          },
        },
      });

      const { container } = renderWithClient(<CompactQuotaView item={quotaUnlimited} nowMS={Date.now()} />);
      const creditEl = container.querySelector('[data-credit-balance="true"]');
      expect(creditEl).not.toBeNull();
      expect(creditEl?.textContent).toMatch(/不限|Unlimited/i);
    });

    it('omits credit balance when credits is nil/absent', () => {
      const quotaNoCredits: QuotaItem = mockQuotaItem({
        auth_index: 'codex:no_credits',
        plan: {
          plan_type: 'tier',
          tier: 'standard',
          plan_label: 'Standard',
        },
      });

      const { container } = renderWithClient(<CompactQuotaView item={quotaNoCredits} nowMS={Date.now()} />);
      const creditEl = container.querySelector('[data-credit-balance="true"]');
      expect(creditEl).toBeNull();
    });
  });
});
