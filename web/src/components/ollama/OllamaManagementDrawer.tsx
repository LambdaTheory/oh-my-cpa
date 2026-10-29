import React, { useState } from 'react';
import { App as AntdApp, Button } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AppDrawer } from '../common/AppDrawer';
import { Switch } from '../common/Switch';
import { Tag } from '../common/Tag';
import { Notice, useToast } from '../feedback';
import { EmptyState } from '../common/EmptyState';
import { PageLoading } from '../common/PageLoading';
import { StatusLabel, type StatusTone } from '../common/StatusLabel';
import { PlusOutlined, DeleteOutlined, EditOutlined, ReloadOutlined, SyncOutlined } from '../icons';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import { getTimeZone } from '../../utils/time';
import { getDateTimeFormatter } from '../../utils/dateTimeFormat';
import { formatTimeWithCountdown } from '../../pages/quota/quotaFormat';
import { executeOllamaAccountRefresh } from '../../utils/ollamaRefresh';
import { credentialLabel } from '../../utils/credentialLabel';
import { OllamaCreditsView } from './OllamaCreditsView';
import { OllamaAccountEditorModal } from './OllamaAccountEditorModal';
import type { OllamaAccountDTO, OllamaAccountWritePayload, OllamaQuotaDTO } from '../../types/ollama';

interface OllamaManagementDrawerProps {
  open: boolean;
  onClose: () => void;
}

export const OllamaManagementDrawer: React.FC<OllamaManagementDrawerProps> = ({ open, onClose }) => {
  const t = useT();
  const toast = useToast();
  const { modal } = AntdApp.useApp();
  const queryClient = useQueryClient();
  const isDemo = isDemoMode();

  // Dialog State
  const [isEditorOpen, setIsEditorOpen] = useState(false);
  const [editingAccount, setEditingAccount] = useState<OllamaAccountDTO | null>(null);
  const [refreshingAccountIds, setRefreshingAccountIds] = useState<Record<string, boolean>>({});

  // Queries
  const accountsQuery = useQuery({
    queryKey: ['ollama-accounts'],
    queryFn: () => api.listOllamaAccounts(),
    enabled: open,
    refetchInterval: (query) => {
      // Auto-poll short interval (2.5s) if any account has refreshing: true within 45s, otherwise 20s
      const data = query.state.data;
      const now = Date.now();
      const isAnyRefreshing = data?.accounts?.some(
        (a) => a.refreshing && (!a.last_observed_at_ms || now - a.last_observed_at_ms < 45_000)
      );
      return isAnyRefreshing ? 2500 : 20_000;
    },
  });

  const quotaQuery = useQuery({
    queryKey: ['ollama-quota'],
    queryFn: () => api.getOllamaQuota(),
    enabled: open,
    refetchInterval: () => {
      const accounts = accountsQuery.data?.accounts;
      const now = Date.now();
      const isAnyRefreshing = accounts?.some(
        (a) => a.refreshing && (!a.last_observed_at_ms || now - a.last_observed_at_ms < 45_000)
      );
      return isAnyRefreshing ? 2500 : 20_000;
    },
  });

  // Mutations
  const saveMutation = useMutation({
    mutationFn: async (payload: OllamaAccountWritePayload) => {
      if (editingAccount) {
        return api.updateOllamaAccount(editingAccount.id, payload);
      }
      return api.createOllamaAccount(payload);
    },
    onSuccess: () => {
      toast.success(t('ollama.save_success'));
      setIsEditorOpen(false);
      setEditingAccount(null);
      void queryClient.invalidateQueries({ queryKey: ['ollama-accounts'] });
      void queryClient.invalidateQueries({ queryKey: ['ollama-quota'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  const toggleSwitchMutation = useMutation({
    mutationFn: async ({ account, enabled }: { account: OllamaAccountDTO; enabled: boolean }) => {
      return api.updateOllamaAccount(account.id, {
        label: account.label,
        enabled,
      });
    },
    onSuccess: () => {
      toast.success(t('ollama.switch_updated'));
      void queryClient.invalidateQueries({ queryKey: ['ollama-accounts'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => api.deleteOllamaAccount(id),
    onSuccess: () => {
      toast.success(t('ollama.delete_success'));
      void queryClient.invalidateQueries({ queryKey: ['ollama-accounts'] });
      void queryClient.invalidateQueries({ queryKey: ['ollama-quota'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  const accounts: OllamaAccountDTO[] = accountsQuery.data?.accounts ?? [];
  const quotaItems: OllamaQuotaDTO[] = quotaQuery.data?.accounts ?? [];
  const quotaMap = React.useMemo(() => {
    const map = new Map<string, OllamaQuotaDTO>();
    for (const q of quotaItems) {
      map.set(q.account_id, q);
    }
    return map;
  }, [quotaItems]);

  const formatTime = (timeMs?: number | null) => {
    if (!timeMs) return '—';
    try {
      const d = new Date(timeMs);
      return getDateTimeFormatter('zh-CN', {
        timeZone: getTimeZone(),
        month: '2-digit',
        day: '2-digit',
        hour: '2-digit',
        minute: '2-digit',
        second: '2-digit',
        hour12: false,
      }).format(d);
    } catch {
      return String(timeMs);
    }
  };

  const getStatusTone = (status?: string): { tone: StatusTone; label: string } => {
    switch (status) {
      case 'OK':
      case 'ok':
        return { tone: 'success', label: t('ollama.status_ok') };
      case 'AUTH_INVALID':
      case 'auth_invalid':
        return { tone: 'danger', label: t('ollama.status_auth_invalid') };
      case 'TRANSPORT_ERROR':
      case 'transport_error':
        return { tone: 'warn', label: t('ollama.status_transport_error') };
      case 'PARSE_ERROR':
      case 'parse_error':
        return { tone: 'warn', label: t('ollama.status_parse_error') };
      default:
        return { tone: 'neutral', label: t('ollama.status_unknown') };
    }
  };

  // Stats calculation
  const stats = React.useMemo(() => {
    let total = accounts.length;
    let collecting = 0;
    let healthy = 0;
    let issue = 0;

    for (const a of accounts) {
      if (a.enabled) collecting++;
      const s = a.status?.toLowerCase();
      if (s === 'ok') healthy++;
      if (s === 'auth_invalid' || s === 'transport_error' || s === 'parse_error') issue++;
    }

    return { total, collecting, healthy, issue };
  }, [accounts]);

  const openAdd = () => {
    setEditingAccount(null);
    setIsEditorOpen(true);
  };

  const openEdit = (a: OllamaAccountDTO) => {
    setEditingAccount(a);
    setIsEditorOpen(true);
  };

  return (
    <AppDrawer
      title={t('ollama.title')}
      width="wide"
      open={open}
      onClose={onClose}
      footerStart={
        <span style={{ fontSize: 13, color: 'var(--muted)' }}>
          {t('ollama.stat_total')}: {accounts.length}
        </span>
      }
      footer={
        <Button onClick={onClose}>
          {t('common.close')}
        </Button>
      }
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
        {/* Notice description */}
        <Notice
          tone="info"
          title={t('ollama.title')}
          description={t('ollama.notice')}
        />

        {/* Stats strip */}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(4, 1fr)',
            gap: 8,
            padding: 12,
            background: 'var(--surface)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius)',
            textAlign: 'center',
          }}
        >
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('ollama.stat_total')}</div>
            <div style={{ fontSize: 18, fontWeight: 600 }}>{stats.total}</div>
          </div>
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('ollama.stat_collecting')}</div>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--info)' }}>{stats.collecting}</div>
          </div>
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('ollama.stat_healthy')}</div>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--success)' }}>{stats.healthy}</div>
          </div>
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('ollama.stat_issue')}</div>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--danger)' }}>{stats.issue}</div>
          </div>
        </div>

        {/* Toolbar */}
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
          <Button
            type="primary"
            icon={<PlusOutlined />}
            disabled={isDemo}
            onClick={openAdd}
          >
            {t('ollama.btn_add')}
          </Button>
          <Button
            icon={<ReloadOutlined />}
            aria-label={t('common.refresh')}
            onClick={() => {
              void accountsQuery.refetch();
              void quotaQuery.refetch();
            }}
          />
        </div>

        {/* Accounts list */}
        {accountsQuery.isPending ? (
          <PageLoading />
        ) : accounts.length === 0 ? (
          <EmptyState title={t('af.empty_all')} />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
            {accounts.map((account) => {
              const statusInfo = getStatusTone(account.status);
              const snapshot = quotaMap.get(account.id);
              const displayLabel = credentialLabel(account.label, [`ollama:${account.id}`, account.id], t('credential.account_fallback', { provider: 'Ollama' }));
              const quotaData = snapshot?.quota;

              return (
                <div
                  key={account.id}
                  style={{
                    display: 'flex',
                    flexDirection: 'column',
                    gap: 10,
                    padding: '12px 14px',
                    background: 'var(--surface)',
                    border: '1px solid var(--border)',
                    borderRadius: 'var(--radius)',
                  }}
                >
                  <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', flexWrap: 'wrap', gap: 8 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      <span style={{ fontWeight: 600 }}>{displayLabel}</span>
                      <Tag size="small">{account.has_cookie ? t('ollama.cookie_saved') : t('ollama.cookie_missing')}</Tag>
                      <Tag size="small">{account.has_api_key ? t('ollama.api_key_saved') : t('ollama.api_key_missing')}</Tag>
                      <StatusLabel tone={statusInfo.tone}>{statusInfo.label}</StatusLabel>
                    </div>

                    <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                      <Switch
                        size="small"
                        checked={account.enabled}
                        disabled={isDemo || toggleSwitchMutation.isPending}
                        onChange={(checked) =>
                          toggleSwitchMutation.mutate({ account, enabled: checked })
                        }
                        aria-label={displayLabel}
                      />
                      <Button
                        size="small"
                        icon={<SyncOutlined spin={Boolean(refreshingAccountIds[account.id] || account.refreshing)} />}
                        disabled={Boolean(refreshingAccountIds[account.id] || account.refreshing) || isDemo || Boolean(account.retry_at_ms && account.retry_at_ms > Date.now())}
                        aria-label={t('quota.btn_refresh_quota')}
                        title={account.retry_at_ms && account.retry_at_ms > Date.now() ? formatTimeWithCountdown(account.retry_at_ms, Date.now(), t) : t('quota.btn_refresh_quota')}
                        onClick={async () => {
                          setRefreshingAccountIds((prev) => ({ ...prev, [account.id]: true }));
                          try {
                            await executeOllamaAccountRefresh(account.id, { queryClient, t, toast });
                          } finally {
                            setRefreshingAccountIds((prev) => ({ ...prev, [account.id]: false }));
                          }
                        }}
                      />
                      <Button
                        size="small"
                        icon={<EditOutlined />}
                        disabled={isDemo}
                        aria-label={t('common.edit')}
                        onClick={() => openEdit(account)}
                      />
                      <Button
                        size="small"
                        danger
                        icon={<DeleteOutlined />}
                        disabled={isDemo || deleteMutation.isPending}
                        aria-label={t('common.delete')}
                        onClick={() => {
                          modal.confirm({
                            title: t('ollama.delete_confirm'),
                            content: displayLabel,
                            okButtonProps: { danger: true },
                            onOk: () => deleteMutation.mutate(account.id),
                          });
                        }}
                      />
                    </div>
                  </div>

                  {/* Quota details */}
                  <div
                    style={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      flexWrap: 'wrap',
                      gap: 8,
                      fontSize: 12,
                      color: 'var(--fg2)',
                      background: 'var(--bg)',
                      padding: '8px 12px',
                      borderRadius: 4,
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: 16, flexWrap: 'wrap' }}>
                      <OllamaCreditsView quota={quotaData} status={account.status} />
                    </div>

                    <div style={{ display: 'flex', alignItems: 'center', gap: 12, color: 'var(--muted)' }}>
                      {account.last_success_at_ms && (
                        <span>{t('ollama.last_success', { time: formatTime(account.last_success_at_ms) })}</span>
                      )}
                      {account.next_attempt_at_ms && account.status !== 'OK' && account.status !== 'ok' && (
                        <span style={{ color: 'var(--warn)' }}>
                          {t('ollama.next_retry', { time: formatTime(account.next_attempt_at_ms) })}
                        </span>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      <OllamaAccountEditorModal
        open={isEditorOpen}
        account={editingAccount}
        isSaving={saveMutation.isPending}
        onClose={() => setIsEditorOpen(false)}
        onSave={(payload) => saveMutation.mutate(payload)}
      />
    </AppDrawer>
  );
};
