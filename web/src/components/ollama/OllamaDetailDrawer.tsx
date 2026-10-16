import React, { useState } from 'react';
import { App as AntdApp, Button } from 'antd';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { AppDrawer } from '../common/AppDrawer';
import { Switch } from '../common/Switch';
import { Tag } from '../common/Tag';
import { FactList } from '../common/FactList';
import { Notice, useToast } from '../feedback';
import { StatusLabel, type StatusTone } from '../common/StatusLabel';
import { SectionHeading } from '../common/SectionHeading';
import { DeleteOutlined, EditOutlined, SyncOutlined } from '../icons';
import { api } from '../../api/client';
import { useT, useI18n, languageLocale } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import { getTimeZone } from '../../utils/time';
import { getDateTimeFormatter } from '../../utils/dateTimeFormat';
import { formatTimeWithCountdown } from '../../pages/quota/quotaFormat';
import { QuotaProgressBar } from '../../pages/quota/QuotaProgressBar';
import { executeOllamaAccountRefresh } from '../../utils/ollamaRefresh';
import { credentialLabel } from '../../utils/credentialLabel';
import { verifiedQuotaCredits } from '../../utils/quotaCredits';
import type { OllamaAccountDTO } from '../../types/ollama';
import type { NormalizedQuota } from '../../types/quota';
import styles from '../authFiles/AuthFileDetailDrawer.module.css';

export interface OllamaDetailDrawerProps {
  open: boolean;
  onClose: () => void;
  account: OllamaAccountDTO | null;
  quota?: NormalizedQuota | null;
  onEdit?: (account: OllamaAccountDTO) => void;
  onDeleted?: (id: string) => void;
}

export const OllamaDetailDrawer: React.FC<OllamaDetailDrawerProps> = ({
  open,
  onClose,
  account,
  quota,
  onEdit,
  onDeleted,
}) => {
  const t = useT();
  const { lang } = useI18n();
  const { modal } = AntdApp.useApp();
  const toast = useToast();
  const queryClient = useQueryClient();
  const isDemo = isDemoMode();
  const displayLabel = credentialLabel(account?.label, [account?.id ? `ollama:${account.id}` : undefined, account?.id], t('credential.account_fallback', { provider: 'Ollama' }));
  const credits = verifiedQuotaCredits(quota);

  const [isRefreshing, setIsRefreshing] = useState(false);
  const [, setTick] = useState(0);

  // Timer to automatically re-render and lift cooldown when retry_at_ms arrives
  React.useEffect(() => {
    if (!account?.retry_at_ms) return;
    const delay = account.retry_at_ms - Date.now();
    if (delay <= 0) return;
    const timer = setTimeout(() => {
      setTick((prev) => prev + 1);
    }, delay + 50);
    return () => clearTimeout(timer);
  }, [account?.retry_at_ms]);

  const toggleSwitchMutation = useMutation({
    mutationFn: async (enabled: boolean) => {
      if (!account) return;
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
    mutationFn: async (id: string) => api.deleteOllamaAccount(id),
    onSuccess: (_, id) => {
      toast.success(t('ollama.delete_success'));
      void queryClient.invalidateQueries({ queryKey: ['ollama-accounts'] });
      void queryClient.invalidateQueries({ queryKey: ['ollama-quota'] });
      onDeleted?.(id);
      onClose();
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  if (!account) return null;

  const formatTime = (timeMs?: number | null) => {
    if (!timeMs) return '—';
    try {
      const d = new Date(timeMs);
      return getDateTimeFormatter(languageLocale(lang), {
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
    switch (status?.toLowerCase()) {
      case 'ok':
        return { tone: 'success', label: t('ollama.status_ok') };
      case 'auth_invalid':
        return { tone: 'danger', label: t('ollama.status_auth_invalid') };
      case 'transport_error':
        return { tone: 'warn', label: t('ollama.status_transport_error') };
      case 'parse_error':
        return { tone: 'warn', label: t('ollama.status_parse_error') };
      default:
        return { tone: 'neutral', label: t('ollama.status_unknown') };
    }
  };

  const statusInfo = getStatusTone(account.status);
  const now = Date.now();
  const isCooldown = Boolean(account.retry_at_ms && account.retry_at_ms > now);

  const handleRefresh = async () => {
    if (isRefreshing || isDemo || isCooldown) return;
    setIsRefreshing(true);
    try {
      await executeOllamaAccountRefresh(account.id, { queryClient, t, toast });
    } finally {
      setIsRefreshing(false);
    }
  };

  const handleDelete = () => {
    modal.confirm({
      title: t('ollama.delete_confirm'),
      content: displayLabel,
      okButtonProps: { danger: true },
      onOk: () => deleteMutation.mutate(account.id),
    });
  };

  const windows = quota?.windows ?? [];

  return (
    <AppDrawer
      title={displayLabel}
      width="wide"
      open={open}
      onClose={onClose}
      footer={
        <div style={{ display: 'flex', justifyContent: 'space-between', width: '100%', alignItems: 'center' }}>
          <div style={{ display: 'flex', gap: 8 }}>
            <Button
              icon={<EditOutlined />}
              disabled={isDemo}
              onClick={() => {
                onEdit?.(account);
              }}
            >
              {t('common.edit')}
            </Button>
            <Button
              danger
              icon={<DeleteOutlined />}
              disabled={isDemo || deleteMutation.isPending}
              onClick={handleDelete}
            >
              {t('common.delete')}
            </Button>
          </div>
          <Button onClick={onClose}>
            {t('common.close')}
          </Button>
        </div>
      }
    >
      <div className={styles['drawer-content']}>
        {/* Header Tags & Quick Actions */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            flexWrap: 'wrap',
            gap: 12,
            padding: '10px 14px',
            background: 'var(--surface)',
            border: '1px solid var(--border-soft)',
            borderRadius: 'var(--radius-sm)',
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
            <Tag tone="accent">Ollama</Tag>
            <Tag size="small">{account.has_cookie ? t('ollama.cookie_saved') : t('ollama.cookie_missing')}</Tag>
            <StatusLabel tone={statusInfo.tone}>{statusInfo.label}</StatusLabel>
          </div>

          <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
              <span style={{ fontSize: 13, color: 'var(--muted)' }}>{t('ollama.col_collection_switch')}:</span>
              <Switch
                size="small"
                checked={account.enabled}
                disabled={isDemo || toggleSwitchMutation.isPending}
                onChange={(checked) => toggleSwitchMutation.mutate(checked)}
                aria-label={t('ollama.col_collection_switch')}
              />
            </div>

            <Button
              size="small"
              icon={<SyncOutlined spin={isRefreshing || account.refreshing} />}
              disabled={isRefreshing || account.refreshing || isDemo || isCooldown}
              onClick={handleRefresh}
              title={isCooldown && account.retry_at_ms ? formatTimeWithCountdown(account.retry_at_ms, now, t) : undefined}
            >
              {t('quota.btn_refresh_quota')}
            </Button>
          </div>
        </div>

        {/* Quota & Plan Section */}
        <div className={styles['drawer-section']}>
          <SectionHeading
            className={styles['section-header']}
            title={t('ollama.detail_collection_heading')}
            extra={quota?.plan?.plan_label ? <Tag size="small">{quota.plan.plan_label}</Tag> : undefined}
          />
          <div className={styles['section-body']}>
            <FactList
              emphasis="quiet"
              facts={[
                {
                  key: 'plan',
                  label: t('quota.col_plan'),
                  value: <span className="mono-num">{quota?.plan?.plan_label || '—'}</span>,
                },
                ...(credits?.unlimited ? [{
                  key: 'balance',
                  label: t('quota.col_credit_balance'),
                  value: <span className="mono-num">{t('quota.credits_unlimited')}</span>,
                }] : (credits?.balance !== undefined && credits?.balance !== null && credits?.balance !== '') ? [{
                  key: 'balance',
                  label: t('quota.col_credit_balance'),
                  value: <span className="mono-num">{String(credits.balance)}</span>,
                }] : []),
              ]}
            />

            {windows.length > 0 ? (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 14 }}>
                {windows.map((win) => {
                  const used = win.used_percent;
                  const remaining = win.remaining_percent ?? (used != null ? Math.max(0, 100 - used) : null);
                  return (
                    <QuotaProgressBar
                      key={win.id || win.label}
                      nowMS={now}
                      label={win.label}
                      usedPercent={used}
                      remainingPercent={remaining}
                      resetAtMS={win.reset_at_ms}
                      resetLabel={win.reset_label}
                      resetAccuracy={win.reset_accuracy === 'derived' ? 'derived' : 'exact'}
                    />
                  );
                })}
              </div>
            ) : (
              <div style={{ color: 'var(--muted)', fontSize: 13, marginTop: 10 }}>
                {t('ollama.unreported')}
              </div>
            )}
          </div>
        </div>

        {/* Observation & Collection Diagnostics */}
        <div className={styles['drawer-section']}>
          <SectionHeading
            className={styles['section-header']}
            title={t('ollama.col_observed')}
            extra={<StatusLabel tone={statusInfo.tone}>{statusInfo.label}</StatusLabel>}
          />
          <div className={styles['section-body']}>
            <FactList
              emphasis="quiet"
              facts={[
                {
                  key: 'last_observed',
                  label: t('ollama.col_observed'),
                  value: <span className="mono-num">{formatTime(account.last_observed_at_ms)}</span>,
                },
                {
                  key: 'last_success',
                  label: t('ollama.col_last_success'),
                  value: <span className="mono-num">{formatTime(account.last_success_at_ms)}</span>,
                },
                {
                  key: 'status',
                  label: t('ollama.col_status'),
                  value: statusInfo.label,
                },
                ...(account.next_attempt_at_ms ? [{
                  key: 'next_attempt',
                  label: t('ollama.field_next_attempt'),
                  value: (
                    <span className="mono-num" style={{ color: 'var(--warn)' }}>
                      {formatTimeWithCountdown(account.next_attempt_at_ms, now, t)}
                    </span>
                  ),
                }] : []),
                {
                  key: 'attempts',
                  label: t('ollama.field_attempt_count'),
                  value: <span className="mono-num">{account.attempt_count}</span>,
                },
              ]}
            />

            {account.newest_error && (
              <Notice
                tone="error"
                title={t('ollama.field_newest_error')}
                description={
                  <div>
                    <div>{account.newest_error}</div>
                    {account.newest_error_at_ms && (
                      <div style={{ fontSize: 11, color: 'var(--muted)', marginTop: 4 }}>
                        {formatTime(account.newest_error_at_ms)}
                      </div>
                    )}
                  </div>
                }
                className={styles['identity-alert']}
              />
            )}
          </div>
        </div>

        {/* Inference & Proxy Telemetry - Honest: Not connected */}
        <div className={styles['drawer-section']}>
          <SectionHeading className={styles['section-header']} title={t('ollama.detail_inference_heading')} />
          <div className={styles['section-body']}>
            <FactList
              emphasis="quiet"
              facts={[
                {
                  key: 'requests',
                  label: t('af.detail_total_requests'),
                  value: <span className="mono-num">—</span>,
                },
                {
                  key: 'tokens',
                  label: t('dash.total_tokens'),
                  value: <span className="mono-num">—</span>,
                },
                {
                  key: 'cost',
                  label: t('dash.total_cost'),
                  value: <span className="mono-num">—</span>,
                },
                {
                  key: 'connection',
                  label: t('af.detail_status'),
                  value: <Tag>{t('ollama.inference_not_connected')}</Tag>,
                },
              ]}
            />
            <Notice
              tone="info"
              title={t('ollama.inference_not_connected')}
              description={t('ollama.inference_not_applicable_note')}
              className={styles['identity-alert']}
            />
          </div>
        </div>

        {/* Identity & Metadata */}
        <div className={styles['drawer-section']}>
          <SectionHeading className={styles['section-header']} title={t('ollama.detail_identity_heading')} />
          <div className={styles['section-body']}>
            <FactList
              emphasis="quiet"
              facts={[
                ...(account.label ? [{
                  key: 'label',
                  label: t('ollama.col_account'),
                  value: displayLabel,
                }] : []),
                {
                  key: 'cookie',
                  label: t('ollama.col_cookie'),
                  value: (
                    <Tag size="small">
                      {account.has_cookie ? t('ollama.cookie_saved') : t('ollama.cookie_missing')}
                    </Tag>
                  ),
                },
                {
                  key: 'created',
                  label: t('common.created_at'),
                  value: <span className="mono-num">{formatTime(account.created_at_ms)}</span>,
                },
                {
                  key: 'updated',
                  label: t('common.updated_at'),
                  value: <span className="mono-num">{formatTime(account.updated_at_ms)}</span>,
                },
              ]}
            />
          </div>
        </div>
      </div>
    </AppDrawer>
  );
};
