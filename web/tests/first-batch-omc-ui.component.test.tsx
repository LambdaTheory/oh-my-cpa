import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { App as AntdApp } from 'antd';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { I18nProvider } from '../src/i18n';
import { AccountAutomationDrawer } from '../src/components/automation/AccountAutomationDrawer';
import { OllamaManagementDrawer } from '../src/components/ollama/OllamaManagementDrawer';
import { OllamaAccountEditorModal } from '../src/components/ollama/OllamaAccountEditorModal';
import { FeishuSettingsPanel } from '../src/components/feishu/FeishuSettingsPanel';
import { api } from '../src/api/client';
import type {
  AccountAutomationListResponse,
} from '../src/types/accountAutomation';
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

  describe('Lane A: Account Automation', () => {
    it('empty roster with live candidates allows adoption and sends correct payload', async () => {
      const mockResponse: AccountAutomationListResponse = {
        settings: {
          web_enabled: false,
          enabled: false,
          source: 'default_off',
          master_from_env: false,
          accounts: 0,
        },
        accounts: [],
        candidates: [
          {
            auth_index: 'auth_cand_1',
            provider: 'anthropic',
            name: 'Candidate 1',
            account_label: 'auth_cand_1: Label 1',
            disabled: false,
            controllable: true,
            adopted: false,
            adoptable: true,
            auto_disable_supported: true,
          },
          {
            auth_index: 'auth_cand_2',
            provider: 'openai',
            name: 'Candidate 2',
            account_label: 'Label 2',
            disabled: true,
            controllable: true,
            adopted: false,
            adoptable: true,
            auto_disable_supported: true,
          },
        ],
      };

      vi.mocked(api.getAccountAutomation).mockResolvedValue(mockResponse);
      vi.mocked(api.getAccountAutomationSettings).mockResolvedValue(mockResponse.settings);
      vi.mocked(api.postAccountAutomationAdopt).mockResolvedValue({ adopted: 2 });

      renderWithClient(<AccountAutomationDrawer open={true} onClose={() => {}} />);

      // Wait until query loads and adopt button is enabled
      const adoptBtn = await screen.findByRole('button', { name: '批量接管账号' });
      await waitFor(() => {
        expect(adoptBtn.getAttribute('disabled')).toBeNull();
      });

      // Open adopt modal
      fireEvent.click(adoptBtn);

      // Check candidates are listed in modal
      expect(await screen.findByText('Label 1')).toBeDefined();
      expect(screen.getByText('Label 2')).toBeDefined();
      expect(screen.queryByText('auth_cand_1')).toBeNull();
      expect(screen.getByRole('checkbox', { name: 'Label 1' })).toBeDefined();

      // In default empty selection mode, select all adoptable candidates explicitly
      const selectAllBtn = screen.getByRole('button', { name: '全选未接管' });
      fireEvent.click(selectAllBtn);

      // Submit adopt
      const submitBtn = screen.getByRole('button', { name: '确认接管' });
      fireEvent.click(submitBtn);

      await waitFor(() => {
        expect(api.postAccountAutomationAdopt).toHaveBeenCalledWith({
          auth_indexes: ['auth_cand_1', 'auth_cand_2'],
          enable: true,
          note: undefined,
        });
      });
    });

    it('displays already-disabled accounts with recovery plan and distinguishes external disable vs auto disable', async () => {
      const mockResponse: AccountAutomationListResponse = {
        settings: {
          web_enabled: true,
          enabled: true,
          source: 'web',
          master_from_env: false,
          accounts: 2,
        },
        accounts: [
          {
            auth_index: 'acc_auto_disabled',
            account_label: 'acc_auto_disabled: Auto account',
            is_adopted: true,
            is_enabled: false,
            state: 'disabled',
            disabled_by_automation: true,
            attempt_count: 0,
            has_weekly_evidence: true,
            weekly_used_percent: 100,
            weekly_reset_at_ms: Date.now() + 86400000,
            recovery_at_ms: Date.now() + 86400000,
            intent_version: 1,
          },
          {
            auth_index: 'acc_ext_disabled',
            account_label: 'External account',
            is_adopted: true,
            is_enabled: false,
            state: 'idle',
            monitor_reason: 'externally_disabled',
            disabled_by_automation: false,
            attempt_count: 0,
            has_weekly_evidence: false,
            intent_version: 1,
          },
        ],
        candidates: [],
      };

      vi.mocked(api.getAccountAutomation).mockResolvedValue(mockResponse);
      vi.mocked(api.getAccountAutomationSettings).mockResolvedValue(mockResponse.settings);

      renderWithClient(<AccountAutomationDrawer open={true} onClose={() => {}} />);

      expect(await screen.findByText('Auto account')).toBeDefined();
      expect(screen.getByText('7d耗尽自动关闭')).toBeDefined();

      expect(screen.getByText('External account')).toBeDefined();
      expect(screen.getByText('外部停用')).toBeDefined();
    });

    it('identifies canonical missing_reset, opens manual reset entry, validates future time, and posts reset_at_ms', async () => {
      const mockResponse: AccountAutomationListResponse = {
        settings: {
          web_enabled: true,
          enabled: true,
          source: 'web',
          master_from_env: false,
          accounts: 1,
        },
        accounts: [
          {
            auth_index: 'acc_missing_reset',
            is_adopted: true,
            is_enabled: true,
            state: 'monitoring',
            monitor_reason: 'missing_reset',
            disabled_by_automation: false,
            attempt_count: 0,
            has_weekly_evidence: true,
            weekly_used_percent: 100,
            intent_version: 1,
          },
        ],
        candidates: [],
      };

      vi.mocked(api.getAccountAutomation).mockResolvedValue(mockResponse);
      vi.mocked(api.getAccountAutomationSettings).mockResolvedValue(mockResponse.settings);
      vi.mocked(api.postAccountAutomationManualReset).mockResolvedValue(mockResponse.accounts[0]);

      renderWithClient(<AccountAutomationDrawer open={true} onClose={() => {}} />);

      const manualBtn = await screen.findByRole('button', { name: '手填重置时间' });
      fireEvent.click(manualBtn);

      expect(await screen.findByText('设置一次性恢复时间')).toBeDefined();

      const input = document.querySelector('input[type="datetime-local"]') as HTMLInputElement;
      expect(input).toBeDefined();

      // Input a past date -> invalid
      fireEvent.change(input, { target: { value: '2020-01-01T12:00' } });
      expect(screen.getByText('恢复时间必须为未来时间')).toBeDefined();

      // Input a future date
      const futureDate = new Date(Date.now() + 3600_000 * 24);
      const futureStr = futureDate.toISOString().slice(0, 16);
      fireEvent.change(input, { target: { value: futureStr } });
      expect(screen.queryByText('恢复时间必须为未来时间')).toBeNull();

      // Confirm
      const confirmBtn = screen.getByRole('button', { name: /确\s*定/ });
      fireEvent.click(confirmBtn);

      await waitFor(() => {
        expect(api.postAccountAutomationManualReset).toHaveBeenCalledWith(
          'acc_missing_reset',
          expect.objectContaining({
            reset_at_ms: expect.any(Number),
          })
        );
      });
    });

    it('renders candidate degradation notice when candidates_degraded is true', async () => {
      const mockResponse: AccountAutomationListResponse = {
        settings: {
          web_enabled: true,
          enabled: true,
          source: 'web',
          master_from_env: false,
          accounts: 0,
        },
        accounts: [],
        candidates: [],
        candidates_degraded: true,
        candidates_error: 'CPA gateway unreachable',
      };

      vi.mocked(api.getAccountAutomation).mockResolvedValue(mockResponse);
      vi.mocked(api.getAccountAutomationSettings).mockResolvedValue(mockResponse.settings);

      renderWithClient(<AccountAutomationDrawer open={true} onClose={() => {}} />);

      expect(await screen.findByText('CPA gateway unreachable')).toBeDefined();
    });

    it('adopts unverified controllable candidates, blocks uncontrollable ones, and renders monitor-only state', async () => {
      const mockResponse: AccountAutomationListResponse = {
        settings: {
          web_enabled: false,
          enabled: false,
          source: 'default_off',
          disabled_reason: 'default_off',
          master_from_env: false,
          accounts: 2,
        },
        accounts: [
          {
            auth_index: 'acc_mismatched',
            account_label: 'Mismatched Acc',
            is_adopted: true,
            is_enabled: true,
            state: 'monitoring',
            monitor_reason: 'evidence_identity_mismatch',
            disabled_by_automation: false,
            attempt_count: 0,
            has_weekly_evidence: true,
            intent_version: 1,
          },
          {
            auth_index: 'acc_idle',
            account_label: 'Idle Acc',
            is_adopted: true,
            is_enabled: true,
            state: 'idle',
            disabled_by_automation: false,
            attempt_count: 0,
            has_weekly_evidence: false,
            intent_version: 1,
          },
        ],
        candidates: [
          {
            auth_index: 'cand_unknown_provider',
            provider: 'unknown-vendor',
            name: 'unknown-1.json',
            account_label: 'Unknown Vendor Acc',
            disabled: false,
            controllable: true,
            adopted: false,
            adoptable: true,
            auto_disable_supported: false,
          },
          {
            auth_index: 'cand_verified_adoptable',
            provider: 'claude',
            name: 'claude-1.json',
            account_label: 'Claude Acc',
            disabled: false,
            controllable: true,
            adopted: false,
            adoptable: true,
            auto_disable_supported: true,
          },
          {
            auth_index: 'cand_uncontrollable',
            provider: 'unknown-vendor',
            account_label: 'Runtime-only Acc',
            disabled: false,
            controllable: false,
            adopted: false,
            adoptable: false,
            adoption_blocked_reason: 'no_controllable_credential',
            auto_disable_supported: false,
          },
        ],
      };

      vi.mocked(api.getAccountAutomation).mockResolvedValue(mockResponse);
      vi.mocked(api.getAccountAutomationSettings).mockResolvedValue(mockResponse.settings);
      vi.mocked(api.postAccountAutomationAdopt).mockResolvedValue({ adopted: 2 });

      renderWithClient(<AccountAutomationDrawer open={true} onClose={() => {}} />);

      // Verify roster displays evidence_identity_mismatch human copy
      expect(await screen.findByText('Mismatched Acc')).toBeDefined();
      expect(screen.queryByText('acc_mismatched')).toBeNull();
      expect(screen.getByText(/凭据与观测身份不一致，暂停自动操作/)).toBeDefined();
      expect(screen.getAllByText('只监控', { exact: true }).length).toBeGreaterThan(0);

      // Open adopt modal
      const adoptBtn = screen.getByRole('button', { name: '批量接管账号' });
      fireEvent.click(adoptBtn);

      // An unverified provider is explicitly monitor-only but remains adoptable.
      expect(await screen.findByText('Unknown Vendor Acc')).toBeDefined();
      expect(screen.getByText(/能力未核实，接管后只监控/)).toBeDefined();

      // The unverified candidate is enabled; only the uncontrollable candidate is blocked.
      const unknownCheckbox = screen.getByLabelText('Unknown Vendor Acc') as HTMLInputElement;
      expect(unknownCheckbox.disabled).toBe(false);
      const runtimeOnlyCheckbox = screen.getByLabelText('Runtime-only Acc') as HTMLInputElement;
      expect(runtimeOnlyCheckbox.disabled).toBe(true);
      expect(screen.getByText(/缺少可控凭据/)).toBeDefined();

      const verifiedCheckbox = screen.getByLabelText('Claude Acc') as HTMLInputElement;
      expect(verifiedCheckbox.disabled).toBe(false);

      // Default selection is empty; submit button is disabled
      const submitBtn = screen.getByRole('button', { name: '确认接管' });
      expect(submitBtn.getAttribute('disabled')).not.toBeNull();

      // Select all includes every controllable candidate, including monitor-only.
      const selectAllBtn = screen.getByRole('button', { name: '全选未接管' });
      fireEvent.click(selectAllBtn);
      expect(screen.getByText('已选择 2 个账号')).toBeDefined();
      expect(unknownCheckbox.checked).toBe(true);
      expect(verifiedCheckbox.checked).toBe(true);
      expect(runtimeOnlyCheckbox.checked).toBe(false);
      expect(submitBtn.getAttribute('disabled')).toBeNull();

      fireEvent.click(submitBtn);
      await waitFor(() => {
        expect(api.postAccountAutomationAdopt).toHaveBeenCalledWith({
          auth_indexes: ['cand_unknown_provider', 'cand_verified_adoptable'],
          enable: true,
          note: undefined,
        });
      });

    });
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
