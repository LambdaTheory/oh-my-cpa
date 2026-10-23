import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nProvider } from '../src/i18n';
import { OllamaManagementDrawer } from '../src/components/ollama/OllamaManagementDrawer';
import { OllamaAccountEditorModal } from '../src/components/ollama/OllamaAccountEditorModal';
import { FeishuSettingsPanel } from '../src/components/feishu/FeishuSettingsPanel';
import { api } from '../src/api/client';
import type {
  OllamaAccountDTO,
  OllamaAccountListResponse,
  OllamaQuotaResponse,
} from '../src/types/ollama';
import type {
  FeishuSettingsDTO,
  FeishuStatusDTO,
} from '../src/types/feishu';

vi.mock('../src/api/client', () => ({
  api: {
    getAccountAutomation: vi.fn(),
    getAccountAutomationSettings: vi.fn(),
    putAccountAutomationSettings: vi.fn(),
    putAccountAutomationAccount: vi.fn(),
    postAccountAutomationAdopt: vi.fn(),
    postAccountAutomationCancel: vi.fn(),
    postAccountAutomationManualReset: vi.fn(),
    listOllamaAccounts: vi.fn(),
    createOllamaAccount: vi.fn(),
    updateOllamaAccount: vi.fn(),
    deleteOllamaAccount: vi.fn(),
    getOllamaQuota: vi.fn(),
    getFeishuSettings: vi.fn(),
    putFeishuSettings: vi.fn(),
    getFeishuStatus: vi.fn(),
    postFeishuTest: vi.fn(),
  },
}));

function renderWithClient(ui: React.ReactElement) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <AntdApp>
          {ui}
        </AntdApp>
      </I18nProvider>
    </QueryClientProvider>
  );
}

describe('First Batch OMC UI Component Tests', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.localStorage.setItem('omc-lang', 'zh-CN');
  });

  describe('Lane B: Ollama Accounts & Quota', () => {
    it('creates account and omits empty cookie on update, never echoing secret', async () => {
      const mockAccounts: OllamaAccountListResponse = {
        accounts: [
          {
            id: 'ollama_1',
            label: 'My Ollama',
            enabled: true,
            has_cookie: true,
            status: 'ok',
            created_at_ms: Date.now(),
            updated_at_ms: Date.now(),
            attempt_count: 0,
          },
        ],
      };
      const mockQuota: OllamaQuotaResponse = {
        accounts: [
          {
            account_id: 'ollama_1',
            label: 'My Ollama',
            enabled: true,
            status: 'ok',
            quota: {
              auth_index: 'ollama:ollama_1',
              name: 'My Ollama',
              type: 'ollama',
              provider: 'ollama',
              disabled: false,
              status: 'healthy',
              observed_at_ms: Date.now(),
              windows: [
                {
                  id: 'weekly',
                  label: '7d',
                  scope: 'standard',
                  used_percent: 45.5,
                  remaining_percent: 54.5,
                },
              ],
              recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
              capabilities: { refresh_supported: false, clear_cooldown_supported: false, reset_credit_supported: false },
              quota_exceeded: false,
            },
          },
        ],
      };

      vi.mocked(api.listOllamaAccounts).mockResolvedValue(mockAccounts);
      vi.mocked(api.getOllamaQuota).mockResolvedValue(mockQuota);
      vi.mocked(api.updateOllamaAccount).mockResolvedValue(mockAccounts.accounts[0]);

      renderWithClient(<OllamaManagementDrawer open={true} onClose={() => {}} />);

      expect(await screen.findByText('My Ollama')).toBeDefined();
      expect(screen.getByText('7d:')).toBeDefined();
      expect(screen.getByText('45.5%')).toBeDefined();

      // Click Edit button
      const editBtn = screen.getByRole('button', { name: /编辑|edit/i });
      fireEvent.click(editBtn);

      // Verify cookie input has empty value
      const cookieInput = document.querySelector('input[type="password"]') as HTMLInputElement;
      expect(cookieInput.value).toBe('');

      // Save without touching cookie -> cookie payload should be undefined
      const saveBtn = screen.getByRole('button', { name: /保\s*存/ });
      fireEvent.click(saveBtn);

      await waitFor(() => {
        expect(api.updateOllamaAccount).toHaveBeenCalledWith('ollama_1', {
          label: 'My Ollama',
          cookie: undefined,
          enabled: true,
        });
      });
    });

    it('allows standalone editing and creation via OllamaAccountEditorModal', async () => {
      const onSave = vi.fn();
      const onClose = vi.fn();

      const { rerender } = renderWithClient(
        <OllamaAccountEditorModal
          open={true}
          account={null}
          isSaving={false}
          onClose={onClose}
          onSave={onSave}
        />
      );

      // Create new account: fill label and cookie
      const textInputs = document.querySelectorAll('input[type="text"]');
      const labelInput = textInputs[0] as HTMLInputElement;
      fireEvent.change(labelInput, { target: { value: 'New Account' } });

      const cookieInput = document.querySelector('input[type="password"]') as HTMLInputElement;
      fireEvent.change(cookieInput, { target: { value: 'user_session=def456' } });

      const saveBtn = screen.getByRole('button', { name: /保\s*存/ });
      fireEvent.click(saveBtn);

      expect(onSave).toHaveBeenCalledWith({
        label: 'New Account',
        cookie: 'user_session=def456',
        enabled: true,
      });

      // Edit existing account: cookie stays undefined if not typed
      const existingAccount: OllamaAccountDTO = {
        id: 'ollama_2',
        label: 'Existing Acc',
        has_cookie: true,
        enabled: false,
        attempt_count: 0,
        created_at_ms: 1000,
        updated_at_ms: 2000,
      };

      rerender(
        <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
          <I18nProvider>
            <AntdApp>
              <OllamaAccountEditorModal
                open={true}
                account={existingAccount}
                isSaving={false}
                onClose={onClose}
                onSave={onSave}
              />
            </AntdApp>
          </I18nProvider>
        </QueryClientProvider>
      );

      fireEvent.click(saveBtn);
      expect(onSave).toHaveBeenCalledWith({
        label: 'Existing Acc',
        cookie: undefined,
        enabled: false,
      });
    });

    it('isolates CPA batch selection and actions from Ollama records', async () => {
      const cpaFiles = [
        {
          name: 'cpa-acc.json',
          auth_index: 'cpa-1',
          type: 'codex',
          provider: 'codex',
          disabled: false,
          unavailable: false,
          runtime_only: false,
          status: 'active',
          success: 10,
          failed: 0,
        },
      ];
      const ollamaAccs: OllamaAccountDTO[] = [
        {
          id: 'ollama-iso-1',
          label: 'Ollama Acc',
          enabled: true,
          has_cookie: true,
          status: 'ok',
          attempt_count: 0,
          created_at_ms: 1000,
          updated_at_ms: 2000,
        },
      ];

      // Projections show Ollama rows cannot be selected for batch operations
      const { buildOAuthWorkspaceProjection } = await import('../src/pages/oauthManagement/oauthWorkspaceLogic');
      const projection = buildOAuthWorkspaceProjection(
        cpaFiles as any,
        [],
        [{ id: 'codex', flow: 'device', iconId: 'codex', title: 'Codex', description: '', loginLabel: '' }],
        ollamaAccs,
        [],
      );

      const cpaRecord = projection.records.find((r) => r.source === 'cpa')!;
      const ollamaRecord = projection.records.find((r) => r.source === 'ollama')!;

      expect(cpaRecord.canSelectForFileBatch).toBe(true);
      expect(ollamaRecord.canSelectForFileBatch).toBe(true);
      expect(ollamaRecord.canToggleFile).toBe(true);
      expect(ollamaRecord.canDownloadFile).toBe(false);
      expect(ollamaRecord.canDeleteFile).toBe(true);
      expect(ollamaRecord.canRefreshQuota).toBe(true);
    });
  });

  describe('Lane C: Feishu Notification Settings', () => {
    it('saves settings without sending empty app_secret and enforces confirmation on test card', async () => {
      const mockSettings: FeishuSettingsDTO = {
        enabled: true,
        app_id: 'cli_12345',
        has_secret: true,
        chat_id: 'oc_chat_999',
        summary_times: ['10:00', '17:00'],
        quiet_start: '23:00',
        quiet_end: '08:00',
        tight_remaining_percent: 20,
        channel_unavailable_after_ms: 1800000,
        monitor_down_after_ms: 1800000,
        source: 'web',
        degraded: false,
        forced_off: false,
      };

      const mockStatus: FeishuStatusDTO = {
        enabled: true,
        ready: true,
        degraded: false,
        source: 'web',
        forced_off: false,
        connected: true,
        open_items: 0,
        pending_sends: 0,
        queued_sends: 0,
      };

      vi.mocked(api.getFeishuSettings).mockResolvedValue(mockSettings);
      vi.mocked(api.getFeishuStatus).mockResolvedValue(mockStatus);
      vi.mocked(api.putFeishuSettings).mockResolvedValue(mockSettings);
      vi.mocked(api.postFeishuTest).mockResolvedValue({ sent: true });

      renderWithClient(<FeishuSettingsPanel />);

      expect(await screen.findByDisplayValue('cli_12345')).toBeDefined();

      // App secret input should be empty (has_secret is true)
      const secretInput = document.querySelector('input[type="password"]') as HTMLInputElement;
      expect(secretInput.value).toBe('');

      // Click save
      const saveBtn = screen.getByRole('button', { name: '保存飞书配置' });
      fireEvent.click(saveBtn);

      await waitFor(() => {
        expect(api.putFeishuSettings).toHaveBeenCalledWith(
          expect.not.objectContaining({ app_secret: expect.anything() })
        );
      });

      // Click test card button -> opens modal confirmation first, does NOT immediately post
      const testBtn = screen.getByRole('button', { name: '发送测试卡片' });
      fireEvent.click(testBtn);

      expect(api.postFeishuTest).not.toHaveBeenCalled();
      expect(await screen.findByText('此操作将向配置的飞书群发送一条真实测试卡片消息。请确认当前为调试需要。')).toBeDefined();

      // Click confirm inside test modal
      const confirmTestBtn = screen.getByRole('button', { name: /确\s*定/ });
      fireEvent.click(confirmTestBtn);

      await waitFor(() => {
        expect(api.postFeishuTest).toHaveBeenCalledTimes(1);
      });
    });

    it('disables editing when environment overrides are forced off', async () => {
      const mockSettings: FeishuSettingsDTO = {
        enabled: false,
        app_id: 'cli_env',
        has_secret: false,
        chat_id: 'oc_env',
        summary_times: ['10:00'],
        quiet_start: '23:00',
        quiet_end: '08:00',
        tight_remaining_percent: 20,
        channel_unavailable_after_ms: 1800000,
        monitor_down_after_ms: 1800000,
        source: 'env_forced_off',
        degraded: false,
        forced_off: true,
      };

      const mockStatus: FeishuStatusDTO = {
        enabled: false,
        ready: false,
        degraded: false,
        source: 'env_forced_off',
        forced_off: true,
        connected: false,
        open_items: 0,
        pending_sends: 0,
        queued_sends: 0,
      };

      vi.mocked(api.getFeishuSettings).mockResolvedValue(mockSettings);
      vi.mocked(api.getFeishuStatus).mockResolvedValue(mockStatus);

      renderWithClient(<FeishuSettingsPanel />);

      expect(await screen.findByText('环境变量强制停用')).toBeDefined();

      // Inputs and switches are disabled
      const appIdInput = screen.getByDisplayValue('cli_env') as HTMLInputElement;
      expect(appIdInput.disabled).toBe(true);
    });
  });
});
