import React, { useState, useMemo } from 'react';
import { App as AntdApp, Button, Checkbox, Input, Modal, Select, Space } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AppDrawer } from '../common/AppDrawer';
import { Switch } from '../common/Switch';
import { Tag } from '../common/Tag';
import { Notice, useToast } from '../feedback';
import { EmptyState } from '../common/EmptyState';
import { PageLoading } from '../common/PageLoading';
import { StatusLabel, type StatusTone } from '../common/StatusLabel';
import { ClockCircleOutlined, ReloadOutlined } from '../icons';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import { getTimeZone } from '../../utils/time';
import { getDateTimeFormatter } from '../../utils/dateTimeFormat';
import { credentialLabel } from '../../utils/credentialLabel';
import type {
  AccountAutomationAccountDTO,
  AccountAutomationCandidateDTO,
  AccountAutomationSettingsDTO,
} from '../../types/accountAutomation';

interface AccountAutomationDrawerProps {
  open: boolean;
  onClose: () => void;
}

function getAccountAutomationDisplayName(item: { auth_index?: string; account_label?: string; name?: string; provider?: string } | null | undefined, fallback: string): {
  primary: string;
  secondary?: string;
  provider?: string;
} {
  if (!item) return { primary: fallback };
  const provider = item.provider;
  const accountLabel = credentialLabel(item.account_label, [item.auth_index]);
  const name = credentialLabel(item.name, [item.auth_index]);
  if (accountLabel) {
    return {
      primary: accountLabel,
      secondary: name && name !== accountLabel ? name : undefined,
      provider,
    };
  }
  if (name) {
    return {
      primary: name,
      provider,
    };
  }
  return { primary: fallback, provider };
}

export const AccountAutomationDrawer: React.FC<AccountAutomationDrawerProps> = ({ open, onClose }) => {
  const t = useT();
  const displayName = (item: Parameters<typeof getAccountAutomationDisplayName>[0]) =>
    getAccountAutomationDisplayName(item, t('credential.account_fallback', { provider: item?.provider || '' }).trim());
  const toast = useToast();
  const { modal } = AntdApp.useApp();
  const queryClient = useQueryClient();
  const isDemo = isDemoMode();

  // Filters & State
  const [filterState, setFilterState] = useState<string>('all');
  const [searchQuery, setSearchQuery] = useState('');
  const [isAdoptModalOpen, setIsAdoptModalOpen] = useState(false);
  const [selectedAdoptKeys, setSelectedAdoptKeys] = useState<string[]>([]);
  const [adoptAutoEnable, setAdoptAutoEnable] = useState(true);
  const [adoptNote, setAdoptNote] = useState('');
  const [manualResetTarget, setManualResetTarget] = useState<AccountAutomationAccountDTO | null>(null);
  const [manualResetTime, setManualResetTime] = useState('');
  const [manualResetError, setManualResetError] = useState('');

  // Queries
  const listQuery = useQuery({
    queryKey: ['account-automation'],
    queryFn: () => api.getAccountAutomation(),
    enabled: open,
    refetchInterval: 15_000,
  });

  const settingsQuery = useQuery({
    queryKey: ['account-automation-settings'],
    queryFn: () => api.getAccountAutomationSettings(),
    enabled: open,
  });

  // Mutations
  const masterMutation = useMutation({
    mutationFn: (enabled: boolean) => api.putAccountAutomationSettings({ enabled }),
    onSuccess: () => {
      toast.success(t('automation.master_switch_updated'));
      void queryClient.invalidateQueries({ queryKey: ['account-automation-settings'] });
      void queryClient.invalidateQueries({ queryKey: ['account-automation'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  const accountSwitchMutation = useMutation({
    mutationFn: ({ authIndex, enabled }: { authIndex: string; enabled: boolean }) =>
      api.putAccountAutomationAccount(authIndex, { enabled }),
    onSuccess: () => {
      toast.success(t('automation.account_switch_updated'));
      void queryClient.invalidateQueries({ queryKey: ['account-automation'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  const adoptMutation = useMutation({
    mutationFn: () =>
      api.postAccountAutomationAdopt({
        auth_indexes: selectedAdoptKeys,
        enable: adoptAutoEnable,
        note: adoptNote.trim() || undefined,
      }),
    onSuccess: (res) => {
      toast.success(t('automation.adopt_success', { n: res.adopted }));
      setIsAdoptModalOpen(false);
      setSelectedAdoptKeys([]);
      setAdoptNote('');
      void queryClient.invalidateQueries({ queryKey: ['account-automation'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  const cancelMutation = useMutation({
    mutationFn: (authIndex: string) => api.postAccountAutomationCancel(authIndex),
    onSuccess: () => {
      toast.success(t('automation.cancel_success'));
      void queryClient.invalidateQueries({ queryKey: ['account-automation'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  const manualResetMutation = useMutation({
    mutationFn: ({ authIndex, resetAtMs }: { authIndex: string; resetAtMs: number }) =>
      api.postAccountAutomationManualReset(authIndex, { reset_at_ms: resetAtMs }),
    onSuccess: () => {
      toast.success(t('automation.manual_reset_success'));
      setManualResetTarget(null);
      setManualResetTime('');
      setManualResetError('');
      void queryClient.invalidateQueries({ queryKey: ['account-automation'] });
    },
    onError: (err: Error) => {
      toast.error(err.message);
    },
  });

  // Candidates & Accounts from live list
  const accounts: AccountAutomationAccountDTO[] = listQuery.data?.accounts ?? [];
  const candidates: AccountAutomationCandidateDTO[] = listQuery.data?.candidates ?? [];
  const settings: AccountAutomationSettingsDTO | undefined = settingsQuery.data;
  const adoptableCandidates = useMemo(() => {
    return candidates.filter((c) => !c.adopted && c.adoptable);
  }, [candidates]);

  // Formatters
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

  // Stats calculation
  const stats = useMemo(() => {
    let adopted = 0;
    let monitoring = 0;
    let disabled = 0;
    let waiting = 0;
    let error = 0;

    for (const a of accounts) {
      if (a.is_adopted) adopted++;
      const s = a.state?.toLowerCase();
      if (s === 'monitoring') monitoring++;
      if (s === 'disabled') disabled++;
      if (s === 'waiting') waiting++;
      if (s === 'error') error++;
    }

    const credentialTotal = listQuery.data?.candidates_degraded ? null : candidates.length;
    return { credentialTotal, adopted, monitoring, disabled, waiting, error };
  }, [accounts, candidates.length, listQuery.data?.candidates_degraded]);

  // Filtered accounts
  const filteredAccounts = useMemo(() => {
    return accounts.filter((a) => {
      const s = a.state?.toLowerCase();
      if (filterState === 'adopted' && !a.is_adopted) return false;
      if (filterState === 'unadopted' && a.is_adopted) return false;
      if (filterState === 'monitoring' && s !== 'monitoring') return false;
      if (filterState === 'disabled' && s !== 'disabled') return false;
      if (filterState === 'waiting' && s !== 'waiting') return false;
      if (filterState === 'error' && s !== 'error') return false;

      if (searchQuery.trim()) {
        const q = searchQuery.toLowerCase();
        const matchAuth = a.auth_index.toLowerCase().includes(q);
        const matchLabel = a.account_label?.toLowerCase().includes(q);
        const matchName = a.name?.toLowerCase().includes(q);
        if (!matchAuth && !matchLabel && !matchName) return false;
      }
      return true;
    });
  }, [accounts, filterState, searchQuery]);

  // State Tone & Label
  const getStatusInfo = (a: AccountAutomationAccountDTO): { tone: StatusTone; label: string } => {
    const s = a.state?.toLowerCase();
    switch (s) {
      case 'idle':
        return { tone: 'neutral', label: t('automation.state_idle') };
      case 'monitoring':
        return { tone: 'accent', label: t('automation.state_monitoring') };
      case 'disabled':
        return { tone: 'danger', label: t('automation.state_disabled') };
      case 'waiting':
        return { tone: 'warn', label: t('automation.state_waiting') };
      case 'error':
        return { tone: 'danger', label: t('automation.state_error') };
      default:
        return { tone: 'neutral', label: a.state || '—' };
    }
  };

  const getReasonLabel = (reason?: string) => {
    if (!reason) return '';
    switch (reason) {
      case 'NOT_ADOPTED':
      case 'not_adopted':
        return t('automation.reason_not_adopted');
      case 'NO_CONTROLLABLE_CREDENTIAL':
      case 'no_controllable_credential':
        return t('automation.reason_no_credential');
      case 'SWITCH_OFF':
      case 'switch_off':
        return t('automation.reason_switch_off');
      case 'MISSING_RESET':
      case 'missing_reset':
      case 'EXHAUSTED_MISSING_RESET':
      case 'exhausted_missing_reset':
        return t('automation.reason_missing_reset');
      case 'RECOVERY_SCHEDULED':
      case 'recovery_scheduled':
        return t('automation.reason_recovery_scheduled');
      case 'BACKOFF':
      case 'backoff':
        return t('automation.reason_backoff');
      case 'EXTERNALLY_DISABLED':
      case 'externally_disabled':
        return t('automation.reason_externally_disabled');
      case 'EXTERNALLY_RE_ENABLED':
      case 'externally_re_enabled':
        return t('automation.reason_externally_re_enabled');
      case 'UNVERIFIED_ACCOUNT_WEEKLY_CAPABILITY':
      case 'unverified_account_weekly_capability':
        return t('automation.reason_unverified_account_weekly_capability');
      case 'EVIDENCE_IDENTITY_MISMATCH':
      case 'evidence_identity_mismatch':
        return t('automation.reason_evidence_identity_mismatch');
      case 'default_off':
        return t('automation.source_default_off');
      case 'web':
        return t('automation.source_web');
      case 'env_disabled':
        return t('automation.source_env_disabled');
      case 'env_enabled':
        return t('automation.source_env_enabled');
      case 'demo':
        return t('automation.source_demo');
      case 'unavailable':
        return t('automation.source_unavailable');
      case 'error':
        return t('automation.source_error');
      default:
        return reason;
    }
  };

  const getSourceBadge = (source?: string) => {
    const s = source?.toLowerCase();
    switch (s) {
      case 'default_off':
        return <Tag size="small">{t('automation.source_default_off')}</Tag>;
      case 'web':
        return <Tag size="small" tone="success">{t('automation.source_web')}</Tag>;
      case 'env_enabled':
        return <Tag size="small" tone="accent">{t('automation.source_env_enabled')}</Tag>;
      case 'env_disabled':
        return <Tag size="small" tone="danger">{t('automation.source_env_disabled')}</Tag>;
      case 'demo':
        return <Tag size="small">{t('automation.source_demo')}</Tag>;
      case 'unavailable':
        return <Tag size="small" tone="warn">{t('automation.source_unavailable')}</Tag>;
      case 'error':
        return <Tag size="small" tone="danger">{t('automation.source_error')}</Tag>;
      default:
        return source ? <Tag size="small">{source}</Tag> : null;
    }
  };

  return (
    <AppDrawer
      title={t('automation.title')}
      width="wide"
      open={open}
      onClose={onClose}
      footerStart={
        <span style={{ fontSize: 13, color: 'var(--muted)' }}>
          {t('automation.adoption_summary', {
            adopted: stats.adopted,
            total: stats.credentialTotal ?? '—',
          })}
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
          title={t('automation.title')}
          description={t('automation.desc')}
        />

        {listQuery.data?.candidates_degraded && (
          <Notice
            tone="warning"
            title={t('automation.source_unavailable')}
            description={listQuery.data.candidates_error || t('af.error')}
          />
        )}

        {/* Master switch card */}
        <div
          style={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '14px 18px',
            background: 'var(--surface)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius)',
          }}
        >
          <div>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, fontWeight: 600 }}>
              <span>{t('automation.master_switch')}</span>
              {getSourceBadge(settings?.source)}
            </div>
            <div style={{ fontSize: 13, color: 'var(--muted)', marginTop: 4 }}>
              {t('automation.master_switch_desc')}
              {!settings?.enabled && settings?.disabled_reason && (
                <span style={{ marginLeft: 8, color: 'var(--warn)' }}>
                  ({getReasonLabel(settings.disabled_reason)})
                </span>
              )}
            </div>
          </div>
          <Switch
            checked={Boolean(settings?.enabled)}
            disabled={isDemo || masterMutation.isPending || settings?.master_from_env}
            onChange={(checked) => masterMutation.mutate(checked)}
            aria-label={t('automation.master_switch')}
          />
        </div>

        {/* Stat strip */}
        <div
          style={{
            display: 'grid',
            gridTemplateColumns: 'repeat(auto-fit, minmax(90px, 1fr))',
            gap: 8,
            padding: 12,
            background: 'var(--surface)',
            border: '1px solid var(--border)',
            borderRadius: 'var(--radius)',
            textAlign: 'center',
          }}
        >
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('automation.stat_adopted')}</div>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--accent)' }}>{stats.adopted}</div>
          </div>
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('automation.stat_credentials')}</div>
            <div style={{ fontSize: 18, fontWeight: 600 }}>{stats.credentialTotal ?? '—'}</div>
          </div>
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('automation.stat_monitoring')}</div>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--info)' }}>{stats.monitoring}</div>
          </div>
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('automation.stat_disabled')}</div>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--danger)' }}>{stats.disabled}</div>
          </div>
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('automation.stat_waiting')}</div>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--warn)' }}>{stats.waiting}</div>
          </div>
          <div>
            <div style={{ fontSize: 12, color: 'var(--muted)' }}>{t('automation.stat_error')}</div>
            <div style={{ fontSize: 18, fontWeight: 600, color: 'var(--danger)' }}>{stats.error}</div>
          </div>
        </div>

        {/* Toolbar & Filter */}
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', justifyContent: 'space-between' }}>
          <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', flex: 1 }}>
            <Input
              style={{ maxWidth: 220 }}
              placeholder={t('af.search_ph')}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              allowClear
            />
            <Select
              style={{ width: 140 }}
              value={filterState}
              onChange={setFilterState}
              options={[
                { value: 'all', label: t('automation.filter_all') },
                { value: 'adopted', label: t('automation.filter_adopted') },
                { value: 'unadopted', label: t('automation.filter_unadopted') },
                { value: 'monitoring', label: t('automation.filter_monitoring') },
                { value: 'disabled', label: t('automation.filter_disabled') },
                { value: 'waiting', label: t('automation.filter_waiting') },
                { value: 'error', label: t('automation.filter_error') },
              ]}
            />
          </div>
          <Space>
            <Button
              type="primary"
              disabled={isDemo || listQuery.isPending}
              onClick={() => {
                setSelectedAdoptKeys([]);
                setIsAdoptModalOpen(true);
              }}
            >
              {t('automation.btn_adopt')}
            </Button>
            <Button
              icon={<ReloadOutlined />}
              aria-label={t('common.refresh')}
              onClick={() => {
                void listQuery.refetch();
                void settingsQuery.refetch();
              }}
            />
          </Space>
        </div>

        {/* List of accounts */}
        {listQuery.isPending ? (
          <PageLoading />
        ) : filteredAccounts.length === 0 ? (
          <EmptyState title={t('af.empty_all')} />
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {filteredAccounts.map((account) => {
              const statusInfo = getStatusInfo(account);
              const s = account.state?.toLowerCase();
              const isWaitingOrError = s === 'waiting' || s === 'error';
              const r = account.monitor_reason?.toLowerCase();
              const isMissingReset = r === 'missing_reset' || r === 'exhausted_missing_reset';

                  const nameInfo = displayName(account);

                  return (
                    <div
                      key={account.auth_index}
                      style={{
                        display: 'flex',
                        flexDirection: 'column',
                        gap: 8,
                        padding: '12px 14px',
                        background: 'var(--surface)',
                        border: '1px solid var(--border)',
                        borderRadius: 'var(--radius)',
                      }}
                    >
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', flexWrap: 'wrap', gap: 8 }}>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                          <span style={{ fontWeight: 600 }}>
                            {nameInfo.primary}
                          </span>
                          {nameInfo.secondary && (
                            <span style={{ color: 'var(--muted)', fontSize: 12 }}>
                              ({nameInfo.secondary})
                            </span>
                          )}
                          {nameInfo.provider && (
                            <Tag size="small">{nameInfo.provider}</Tag>
                          )}
                          {account.is_adopted ? (
                            <Tag size="small" tone="accent">{t('automation.tag_adopted')}</Tag>
                          ) : (
                            <Tag size="small">{t('automation.tag_not_adopted')}</Tag>
                          )}
                          {account.disabled_by_automation ? (
                            <Tag size="small" tone="danger">{t('automation.tag_disabled_by_auto')}</Tag>
                          ) : !account.is_enabled ? (
                            <Tag size="small">{t('automation.tag_disabled_external')}</Tag>
                          ) : null}
                        </div>
                        <div style={{ display: 'flex', alignItems: 'center', gap: 12 }}>
                          <StatusLabel tone={statusInfo.tone}>{statusInfo.label}</StatusLabel>
                          <Switch
                            size="small"
                            checked={account.is_enabled}
                            disabled={isDemo || !account.is_adopted || accountSwitchMutation.isPending}
                            onChange={(checked) =>
                              accountSwitchMutation.mutate({ authIndex: account.auth_index, enabled: checked })
                            }
                            aria-label={nameInfo.primary}
                      />
                    </div>
                  </div>

                  {/* Quota & Reset Details Row */}
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
                      padding: '6px 10px',
                      borderRadius: 4,
                    }}
                  >
                    <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
                      {account.weekly_used_percent !== undefined && account.weekly_used_percent !== null ? (
                        <span>
                          {t('automation.weekly_used', { n: account.weekly_used_percent.toFixed(1) })}
                          {account.weekly_reset_at_ms && (
                            <span style={{ marginLeft: 6, color: 'var(--muted)' }}>
                              ({t('automation.weekly_reset', { time: formatTime(account.weekly_reset_at_ms) })})
                            </span>
                          )}
                        </span>
                      ) : (
                        <span style={{ color: 'var(--muted)' }}>{t('automation.no_weekly')}</span>
                      )}

                      {account.monitor_reason && (
                        <span style={{ color: 'var(--warn)' }}>
                          · {getReasonLabel(account.monitor_reason)}
                        </span>
                      )}
                    </div>

                    {/* Recovery / Retry information */}
                    <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                      {account.recovery_at_ms && (
                        <span style={{ color: 'var(--accent)' }}>
                          <ClockCircleOutlined style={{ marginRight: 4 }} />
                          {t('automation.recovery_at', { time: formatTime(account.recovery_at_ms) })}
                        </span>
                      )}
                      {account.next_attempt_at_ms && (
                        <span style={{ color: 'var(--warn)' }}>
                          {t('automation.next_attempt', {
                            time: formatTime(account.next_attempt_at_ms),
                            count: account.attempt_count ?? 1,
                          })}
                        </span>
                      )}
                      {isWaitingOrError && (
                        <Button
                          size="small"
                          danger
                          disabled={isDemo || cancelMutation.isPending}
                          onClick={() => {
                            modal.confirm({
                              title: t('automation.btn_cancel_retry'),
                              content: `${nameInfo.primary}: ${t('automation.cancel_confirm')}`,
                              onOk: () => cancelMutation.mutate(account.auth_index),
                            });
                          }}
                        >
                          {t('automation.btn_cancel_retry')}
                        </Button>
                      )}
                      {isMissingReset && (
                        <Button
                          size="small"
                          type="primary"
                          disabled={isDemo}
                          onClick={() => {
                            setManualResetTarget(account);
                            setManualResetTime('');
                            setManualResetError('');
                          }}
                        >
                          {t('automation.btn_manual_reset')}
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              );
            })}
          </div>
        )}
      </div>

      {/* Batch Adopt Modal */}
      <Modal
        open={isAdoptModalOpen}
        title={t('automation.adopt_title')}
        onCancel={() => setIsAdoptModalOpen(false)}
        footer={[
          <Button key="cancel" onClick={() => setIsAdoptModalOpen(false)}>
            {t('common.cancel')}
          </Button>,
          <Button
            key="submit"
            type="primary"
            disabled={selectedAdoptKeys.length === 0 || adoptMutation.isPending}
            loading={adoptMutation.isPending}
            onClick={() => adoptMutation.mutate()}
          >
            {t('automation.adopt_submit')}
          </Button>,
        ]}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <p style={{ color: 'var(--muted)', fontSize: 13 }}>{t('automation.adopt_desc')}</p>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
            <span style={{ fontSize: 13, fontWeight: 500 }}>
              {t('automation.adopt_selected_count', { n: selectedAdoptKeys.length })}
            </span>
            <Space size="small">
              <Button
                type="link"
                size="small"
                onClick={() => {
                  const unadopted = adoptableCandidates.length > 0
                    ? adoptableCandidates.map((c) => c.auth_index)
                    : accounts.filter((a) => !a.is_adopted).map((a) => a.auth_index);
                  setSelectedAdoptKeys(unadopted);
                }}
              >
                {t('automation.adopt_select_all')}
              </Button>
              <Button
                type="link"
                size="small"
                disabled={selectedAdoptKeys.length === 0}
                onClick={() => setSelectedAdoptKeys([])}
              >
                {t('automation.adopt_deselect_all')}
              </Button>
            </Space>
          </div>

          <div
            style={{
              maxHeight: 220,
              overflowY: 'auto',
              border: '1px solid var(--border)',
              borderRadius: 'var(--radius)',
              padding: 8,
              display: 'flex',
              flexDirection: 'column',
              gap: 6,
            }}
          >
            {candidates.length > 0 ? (
              candidates.map((c) => {
                const nameInfo = displayName(c);
                return (
                  <Checkbox
                    key={c.auth_index}
                    checked={selectedAdoptKeys.includes(c.auth_index)}
                    disabled={!c.adoptable}
                    aria-label={nameInfo.primary}
                    onChange={(e) => {
                      if (e.target.checked) {
                        setSelectedAdoptKeys([...selectedAdoptKeys, c.auth_index]);
                      } else {
                        setSelectedAdoptKeys(selectedAdoptKeys.filter((k) => k !== c.auth_index));
                      }
                    }}
                  >
                    <span style={{ fontWeight: 500, fontSize: 13 }}>{nameInfo.primary}</span>
                    {nameInfo.secondary && <span style={{ marginLeft: 6, color: 'var(--muted)', fontSize: 12 }}>({nameInfo.secondary})</span>}
                    {nameInfo.provider && <Tag size="small" style={{ marginLeft: 6 }}>{nameInfo.provider}</Tag>}
                    {c.adopted && <span style={{ marginLeft: 6, color: 'var(--muted)', fontSize: 12 }}>[{t('automation.tag_adopted')}]</span>}
                    {c.disabled && <span style={{ marginLeft: 6, color: 'var(--warn)', fontSize: 12 }}>[{t('automation.tag_disabled_external')}]</span>}
                    {!c.adoptable && c.adoption_blocked_reason && (
                      <span style={{ marginLeft: 6, color: 'var(--muted)', fontSize: 12 }}>
                        ({getReasonLabel(c.adoption_blocked_reason)})
                      </span>
                    )}
                    {c.adoptable && !c.auto_disable_supported && (
                      <span style={{ marginLeft: 6, color: 'var(--muted)', fontSize: 12 }}>
                        ({t('automation.adopt_monitor_only')})
                      </span>
                    )}
                  </Checkbox>
                );
              })
            ) : (
              accounts.map((a) => {
                const nameInfo = displayName(a);
                return (
                  <Checkbox
                    key={a.auth_index}
                    checked={selectedAdoptKeys.includes(a.auth_index)}
                    aria-label={nameInfo.primary}
                    onChange={(e) => {
                      if (e.target.checked) {
                        setSelectedAdoptKeys([...selectedAdoptKeys, a.auth_index]);
                      } else {
                        setSelectedAdoptKeys(selectedAdoptKeys.filter((k) => k !== a.auth_index));
                      }
                    }}
                  >
                    <span style={{ fontWeight: 500, fontSize: 13 }}>{nameInfo.primary}</span>
                    {nameInfo.secondary && <span style={{ marginLeft: 6, color: 'var(--muted)', fontSize: 12 }}>({nameInfo.secondary})</span>}
                    {nameInfo.provider && <Tag size="small" style={{ marginLeft: 6 }}>{nameInfo.provider}</Tag>}
                    {a.is_adopted && <span style={{ marginLeft: 6, color: 'var(--muted)', fontSize: 12 }}>[{t('automation.tag_adopted')}]</span>}
                  </Checkbox>
                );
              })
            )}
          </div>

          <Checkbox
            checked={adoptAutoEnable}
            onChange={(e) => setAdoptAutoEnable(e.target.checked)}
          >
            {t('automation.adopt_enable_switch')}
          </Checkbox>

          <Input
            placeholder={t('automation.adopt_note_placeholder')}
            value={adoptNote}
            maxLength={256}
            onChange={(e) => setAdoptNote(e.target.value)}
          />
        </div>
      </Modal>

      {/* Manual Reset Modal */}
      <Modal
        open={Boolean(manualResetTarget)}
        title={t('automation.manual_reset_title')}
        onCancel={() => setManualResetTarget(null)}
        footer={[
          <Button key="cancel" onClick={() => setManualResetTarget(null)}>
            {t('common.cancel')}
          </Button>,
          <Button
            key="submit"
            type="primary"
            disabled={!manualResetTime || Boolean(manualResetError) || manualResetMutation.isPending}
            loading={manualResetMutation.isPending}
            onClick={() => {
              if (!manualResetTarget) return;
              const date = new Date(manualResetTime);
              if (isNaN(date.getTime()) || date.getTime() <= Date.now()) {
                setManualResetError(t('automation.manual_reset_must_future'));
                return;
              }
              manualResetMutation.mutate({
                authIndex: manualResetTarget.auth_index,
                resetAtMs: date.getTime(),
              });
            }}
          >
            {t('common.confirm')}
          </Button>,
        ]}
      >
        <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
          <p style={{ color: 'var(--muted)', fontSize: 13 }}>{t('automation.manual_reset_desc')}</p>
          <div style={{ fontWeight: 600, display: 'flex', alignItems: 'center', gap: 8 }}>
            <span>{displayName(manualResetTarget).primary}</span>
            {displayName(manualResetTarget).secondary && (
              <span style={{ color: 'var(--muted)', fontSize: 12 }}>({displayName(manualResetTarget).secondary})</span>
            )}
            {displayName(manualResetTarget).provider && (
              <Tag size="small">{displayName(manualResetTarget).provider}</Tag>
            )}
          </div>
          <div>
            <label style={{ display: 'block', marginBottom: 4, fontSize: 13 }}>
              {t('automation.manual_reset_field')} ({getTimeZone()})
            </label>
            <input
              type="datetime-local"
              style={{
                width: '100%',
                padding: '6px 10px',
                background: 'var(--surface)',
                color: 'var(--fg)',
                border: '1px solid var(--border)',
                borderRadius: 'var(--radius)',
              }}
              value={manualResetTime}
              onChange={(e) => {
                setManualResetTime(e.target.value);
                const d = new Date(e.target.value);
                if (isNaN(d.getTime()) || d.getTime() <= Date.now()) {
                  setManualResetError(t('automation.manual_reset_must_future'));
                } else {
                  setManualResetError('');
                }
              }}
            />
            {manualResetError && (
              <span style={{ color: 'var(--danger)', fontSize: 12, marginTop: 4, display: 'block' }}>
                {manualResetError}
              </span>
            )}
          </div>
        </div>
      </Modal>
    </AppDrawer>
  );
};
