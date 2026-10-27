import React, { useEffect, useMemo, useRef, useState } from 'react';
import { App as AntdApp, Button, Checkbox, Input, Modal, Segmented, Tooltip } from 'antd';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { AppDrawer } from '../common/AppDrawer';
import { Switch } from '../common/Switch';
import { Notice, useToast } from '../feedback';
import { EmptyState } from '../common/EmptyState';
import { PageLoading } from '../common/PageLoading';
import { StatusLabel, type StatusTone } from '../common/StatusLabel';
import { ReloadOutlined } from '../icons';
import { ProviderBrandIcon } from '../LobeIcon';
import { credentialProviderIconId } from '../common/providerMetadata';
import { api } from '../../api/client';
import { useI18n, languageLocale } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import { getAppConfig } from '../../types/config';
import dayjs, { getTimeZone } from '../../utils/time';
import { getDateTimeFormatter } from '../../utils/dateTimeFormat';
import { credentialLabel } from '../../utils/credentialLabel';
import type { AccountAutomationAccountDTO, AccountAutomationCredentialDTO, AccountAutomationPatch } from '../../types/accountAutomation';
import { automationReasonKey, automationRowReasonKey, matchesAutomationFacet, projectAutomationRoster, type AutomationFacet } from './accountAutomationModel';
import styles from './AccountAutomationDrawer.module.css';

const FACETS: AutomationFacet[] = ['all', 'managed', 'unmanaged', 'paused', 'disabled', 'monitor_only', 'error'];
const TONES: Record<string, StatusTone> = { unmanaged: 'neutral', monitoring: 'accent', disabled: 'warn', paused: 'neutral', credit: 'accent', monitor_only: 'neutral', error: 'danger' };

export const AccountAutomationDrawer: React.FC<{ open: boolean; onClose: () => void }> = ({ open, onClose }) => {
  const { t, lang } = useI18n();
  const toast = useToast();
  const { modal } = AntdApp.useApp();
  const queryClient = useQueryClient();
  const isDemo = isDemoMode();
  const [facet, setFacet] = useState<AutomationFacet>('all');
  const [search, setSearch] = useState('');
  const [expanded, setExpanded] = useState<string[]>([]);
  const [isBatchOpen, setIsBatchOpen] = useState(false);
  const [batchMode, setBatchMode] = useState<'adopt' | 'unadopt'>('adopt');
  const [selected, setSelected] = useState<string[]>([]);
  const [shouldEnable, setShouldEnable] = useState(true);
  const [note, setNote] = useState('');
  const [resetTarget, setResetTarget] = useState<AccountAutomationAccountDTO | null>(null);
  const [resetTime, setResetTime] = useState('');
  const [resetError, setResetError] = useState('');
  const mutationLock = useRef(false);
  const listQuery = useQuery({ queryKey: ['account-automation'], queryFn: () => api.getAccountAutomation(), enabled: open, refetchInterval: 15_000 });
  const { rows, stats, available, lastKnownAccounts } = useMemo(() => projectAutomationRoster(listQuery.data), [listQuery.data]);
  const settings = listQuery.data?.settings;
  const candidates = listQuery.data?.candidates ?? [];
  const isInventoryAvailable = Boolean(listQuery.data && !listQuery.isError && available);
  const canUseInventory = isInventoryAvailable;
  const eligibleKeys = useMemo(() => batchMode === 'adopt'
    ? canUseInventory ? rows.filter(row => !row.isManaged && row.credential.adoptable).map(row => row.credential.auth_index) : []
    : rows.filter(row => row.isManaged).map(row => row.credential.auth_index), [batchMode, canUseInventory, rows]);
  // The lifecycle each managed row carried in the view the operator selected from. Batch
  // unadopt names it per member so a delayed request cannot act on a replacement.
  const versions = useMemo(() => new Map(rows.filter(row => row.isManaged).map(row => [row.credential.auth_index, row.plan.intent_version])), [rows]);
  const selection = selected.filter(key => eligibleKeys.includes(key));

  useEffect(() => {
    setSelected(keys => keys.filter(key => eligibleKeys.includes(key)));
  }, [eligibleKeys]);
  useEffect(() => {
    if (!open) { setIsBatchOpen(false); setSelected([]); setResetTarget(null); setExpanded([]); }
  }, [open]);

  const writeMutation = useMutation({
    mutationFn: async ({ action }: { action: () => Promise<unknown>; success: string; done?: () => void }) => action(),
    onSuccess: async (_, variables) => {
      variables.done?.();
      toast.success(variables.success);
      await queryClient.invalidateQueries({ queryKey: ['account-automation'] });
    },
    onError: (error: Error) => { toast.error(error.message); },
    onSettled: () => { mutationLock.current = false; },
  });
  const isBusy = writeMutation.isPending;
  const canWrite = !isDemo && !isBusy && Boolean(listQuery.data) && !listQuery.isError;
  const write = (action: () => Promise<unknown>, success: string, done?: () => void) => {
    if (!canWrite || mutationLock.current) return;
    mutationLock.current = true;
    writeMutation.mutate({ action, success, done });
  };
  const patchAccount = (row: AccountAutomationCredentialDTO, patch: Omit<AccountAutomationPatch, 'expected_intent_version'>) =>
    write(() => api.putAccountAutomationAccount(row.auth_index, { ...patch, expected_intent_version: row.plan?.intent_version ?? undefined } as AccountAutomationPatch), t('automation.account_switch_updated'));
  const displayName = (item: { auth_index: string; account_label?: string; name?: string; provider?: string }) => {
    const primary = credentialLabel(item.account_label, [item.auth_index]) || credentialLabel(item.name, [item.auth_index]) || t('credential.account_fallback', { provider: item.provider || '' }).trim();
    const name = credentialLabel(item.name, [item.auth_index]);
    return { primary, secondary: name !== primary ? name : undefined };
  };
  const formatTime = (value?: number | null) => value ? getDateTimeFormatter(languageLocale(lang), { timeZone: getTimeZone(), month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }).format(value) : '—';
  const closeBatch = () => { setIsBatchOpen(false); setSelected([]); setNote(''); };
  const filtered = rows.filter(row => matchesAutomationFacet(row, facet) &&
    (!search.trim() || [row.credential.account_label, row.credential.name, row.credential.provider, row.credential.auth_index].some(value => value?.toLowerCase().includes(search.trim().toLowerCase()))));
  const confirmUnadopt = (row: AccountAutomationCredentialDTO) => modal.confirm({
    title: t('automation.unadopt_title'), content: displayName(row).primary + ': ' + t('automation.unadopt_desc'),
    okText: t('automation.unadopt_submit'), cancelText: t('common.cancel'),
    onOk: () => write(() => api.postAccountAutomationUnadopt(row.auth_index, row.plan?.intent_version ?? undefined), t('automation.unadopt_success')),
  });
  const adoptOne = (row: AccountAutomationCredentialDTO) => write(
    () => api.postAccountAutomationAdopt({ auth_indexes: [row.auth_index], enable: true }),
    t('automation.adopt_success', { n: 1 }));
  const openBatch = (mode: 'adopt' | 'unadopt') => { setBatchMode(mode); setSelected([]); setShouldEnable(true); setNote(''); setIsBatchOpen(true); };
  const statItems = [
    ['credentials', stats.credentials], ['managed', stats.managed], ['unmanaged', stats.unmanaged],
    ['disabled', stats.gatewayDisabled], ['paused', stats.paused], ['pending_absence', stats.pendingAbsence],
  ].map(([key, value]) => [key, value ?? '—'] as const);

  return <AppDrawer title={t('automation.title')} width="wide" open={open} onClose={onClose}
    footerStart={<span className={styles['muted']}>{available
      ? t('automation.adoption_summary', { adopted: stats.managed ?? '—', total: stats.credentials ?? '—' })
      : t('automation.inventory_last_known', { count: lastKnownAccounts.length })}</span>}
    footer={<Button onClick={onClose}>{t('common.close')}</Button>}>
    <div className={styles['content']}>
      <p className={styles['intro']}>{t('automation.desc')}</p>
      {listQuery.isError && <Notice tone="error" title={t('automation.load_failed')} description={t('automation.refresh_help')} />}
      {listQuery.data && !listQuery.isError && !available && <Notice tone="warning" title={t('automation.inventory_unavailable')} description={t('automation.inventory_degraded_notice')} />}
      <div className={styles['master']}>
        <div><strong>{t('automation.master_switch')}</strong><p>{t('automation.master_switch_desc')}</p>
          {settings && <span className={styles['muted']}>{t(['default_off', 'web', 'env_enabled', 'env_disabled', 'demo', 'unavailable', 'error'].includes(settings.source) ? `automation.source_${settings.source}` : 'automation.source_error')}</span>}</div>
        <Switch checked={Boolean(settings?.enabled)} disabled={!canWrite || settings?.master_from_env}
          aria-label={t('automation.master_switch')} onChange={enabled => write(() => api.putAccountAutomationSettings({ enabled }), t('automation.master_switch_updated'))} />
      </div>
      <div className={styles['stats']} data-testid="automation-stats">{statItems.map(([key, value]) => <div key={key} title={t(`automation.stat_help_${key}`)}><span>{t(`automation.stat_${key}`)}</span><strong>{value}</strong></div>)}</div>
      <div className={styles['toolbar']}>
        <Input className={styles['search']} aria-label={t('af.search_ph')} placeholder={t('af.search_ph')} value={search} onChange={event => setSearch(event.target.value)} allowClear />
        <Button icon={<ReloadOutlined />} aria-label={t('common.refresh')} loading={listQuery.isFetching} disabled={isBusy} onClick={() => { void listQuery.refetch(); }}>{t('common.refresh')}</Button>
        <Button type="primary" disabled={!canWrite || !isInventoryAvailable} onClick={() => openBatch('adopt')}>{t('automation.batch_adopt')}</Button>
        <Button disabled={!canWrite || !isInventoryAvailable || !stats.managed} onClick={() => openBatch('unadopt')}>{t('automation.batch_unadopt')}</Button>
      </div>
      <div className={styles['facets']} role="group" aria-label={t('automation.filter_label')}>{FACETS.map(value => <button type="button" key={value} aria-pressed={facet === value} onClick={() => setFacet(value)}>{t(`automation.facet_${value}`)}</button>)}</div>
      <div className={styles['list-header']} aria-hidden="true"><span>{t('automation.account_credential')}</span><span>{t('automation.state_reason')}</span><span>{t('automation.col_actions')}</span></div>
      {listQuery.isPending ? <PageLoading /> : !available ? <EmptyState title={t('automation.inventory_unavailable')} /> : filtered.length === 0 ? <EmptyState title={t('automation.empty')} /> :
        <div className={styles['list']}>{filtered.map(row => {
          const item = row.credential;
          const account = row.account;
          const name = displayName(item);
          const isExpanded = expanded.includes(item.auth_index);
          const detailId = 'automation-details-' + encodeURIComponent(item.auth_index);
          const reason = t(automationRowReasonKey(row), { time: formatTime(account?.recovery_at_ms), count: String(account?.attempt_count ?? 0) });
          return <article key={item.auth_index} className={styles['account-card']} data-testid="automation-account" aria-label={name.primary}>
            <div className={styles['account-row']}>
              <div className={styles['identity']} data-testid="automation-identity"><ProviderBrandIcon iconId={credentialProviderIconId(item.provider || '')} size={26} />
                <div className={styles['identity-text']}><strong title={name.primary}>{name.primary}</strong><span title={name.secondary || item.provider}>{name.secondary || item.provider || '—'}</span></div></div>
              <div className={styles['status']} data-testid="automation-status"><StatusLabel tone={TONES[row.state]}>{t(`automation.display_${row.state}`)}</StatusLabel><span className={styles['reason']} title={reason}>{reason}</span></div>
              <div className={styles['actions']} data-testid="automation-actions">
                {row.isManaged ? <>
                  <Tooltip title={t('automation.credit_help')}><button type="button" className={styles['credit']} aria-label={t('automation.credit_account', { name: name.primary })} aria-pressed={Boolean(account?.burn_credit)} disabled={!canWrite || row.isMonitorOnly} onClick={() => patchAccount(item, { burn_credit: !account?.burn_credit })}>{t('automation.credit')}</button></Tooltip>
                  <label className={styles['switch-hit']} htmlFor={detailId + '-switch'}><Switch id={detailId + '-switch'} checked={Boolean(account?.is_enabled)} disabled={!canWrite} aria-label={t('automation.switch_account', { name: name.primary })} onChange={enabled => patchAccount(item, { enabled })} /></label>
                  <Button type="text" aria-expanded={isExpanded} aria-controls={detailId} onClick={() => setExpanded(keys => isExpanded ? keys.filter(key => key !== item.auth_index) : [...keys, item.auth_index])}>{t('automation.details')}</Button>
                </> : <Button type="primary" disabled={!canWrite || !item.adoptable} aria-label={t('automation.adopt_account', { name: name.primary })} onClick={() => adoptOne(item)}>{t('automation.adopt_action')}</Button>}
              </div>
            </div>
            {row.isManaged && account && isExpanded && <div id={detailId} className={styles['details']}>
              <dl className={styles['detail-grid']}>
                <div><dt>{t('automation.col_weekly')}</dt><dd>{account.has_weekly_evidence && account.weekly_used_percent != null ? t('automation.weekly_used', { n: account.weekly_used_percent.toFixed(1) }) : t(row.isMonitorOnly ? 'automation.no_weekly_summary' : 'automation.no_weekly')}</dd></div>
                <div><dt>{t('automation.reset_time')}</dt><dd>{formatTime(account.weekly_reset_at_ms)}</dd></div>
                <div><dt>{t('automation.last_disable')}</dt><dd>{formatTime(account.last_disable_at_ms)}</dd></div>
                <div><dt>{t('automation.last_enable')}</dt><dd>{formatTime(account.last_enable_at_ms)}</dd></div>
              </dl>
              {row.hasRecovery && <p>{t(account.recovery_status === 'overdue' ? 'automation.recovery_overdue' : 'automation.recovery_at', { time: formatTime(account.recovery_at_ms) })}</p>}
              {account.next_attempt_at_ms && <p>{t('automation.next_attempt', { time: formatTime(account.next_attempt_at_ms), count: account.attempt_count })}</p>}
              {account.last_error && <p className={styles['error']}>{t('automation.last_error')}: {account.last_error}</p>}
              <div className={styles['detail-actions']}>
                <Button href={getAppConfig().basePath.replace(/[/]$/, '') + '/audit?q=' + encodeURIComponent(item.auth_index)}>{t('nav.audit')}</Button>
                {(row.hasBackoff || account.state === 'waiting' || account.state === 'error') && <Button disabled={!canWrite} onClick={() => modal.confirm({ title: t('automation.btn_cancel_retry'), content: t('automation.cancel_confirm'), onOk: () => write(() => api.postAccountAutomationCancel(item.auth_index, account.intent_version), t('automation.cancel_success')) })}>{t('automation.btn_cancel_retry')}</Button>}
                {row.reason === 'missing_reset' && <Button disabled={!canWrite} onClick={() => { setResetTarget(account); setResetTime(''); setResetError(''); }}>{t('automation.btn_manual_reset')}</Button>}
                <Button danger disabled={!canWrite} onClick={() => confirmUnadopt(item)}>{t('automation.unadopt_title')}</Button>
              </div>
            </div>}
          </article>;
        })}</div>}
    </div>
    <Modal open={isBatchOpen} title={t(batchMode === 'adopt' ? 'automation.batch_adopt' : 'automation.batch_unadopt')} onCancel={() => { if (!isBusy) closeBatch(); }} closable={!isBusy} mask={{ closable: !isBusy }}
      footer={<><Button disabled={isBusy} onClick={closeBatch}>{t('common.cancel')}</Button><Button type="primary" danger={batchMode === 'unadopt'} loading={isBusy} disabled={!canWrite || !selection.length || listQuery.isFetching} onClick={() => {
        const keys = [...selection];
        if (batchMode === 'adopt') write(() => api.postAccountAutomationAdopt({ auth_indexes: keys, enable: shouldEnable, note: note.trim() || undefined }), t('automation.adopt_success', { n: keys.length }), closeBatch);
        else {
          // Every selected row names the lifecycle the console read, so a stale batch is
          // refused as a whole instead of removing membership from a re-adopted incarnation.
          const targets = keys.map(key => ({ auth_index: key, expected_intent_version: versions.get(key) })).filter(target => target.expected_intent_version != null);
          write(() => api.postAccountAutomationUnadoptMany({ auth_indexes: keys, targets }), t('automation.unadopt_success'), closeBatch);
        }
      }}>{t(batchMode === 'adopt' ? 'automation.adopt_submit' : 'automation.unadopt_submit')}</Button></>}>
      <div className={styles['batch-content']}>
        <Segmented block disabled={isBusy} value={batchMode} onChange={value => { setBatchMode(value as 'adopt' | 'unadopt'); setSelected([]); }} options={[{ label: t('automation.adopt_title'), value: 'adopt' }, { label: t('automation.unadopt_title'), value: 'unadopt' }]} />
        <p>{t(batchMode === 'adopt' ? 'automation.adopt_desc' : 'automation.unadopt_desc')}</p>
        <div className={styles['batch-tools']}><span>{t('automation.adopt_selected_count', { n: selection.length })}</span><Button size="small" disabled={isBusy || !eligibleKeys.length} onClick={() => setSelected(eligibleKeys)}>{t('automation.select_all')}</Button><Button size="small" disabled={isBusy || !selection.length} onClick={() => setSelected([])}>{t('automation.adopt_deselect_all')}</Button></div>
        <div className={styles['batch-list']}>
          {(batchMode === 'adopt' ? candidates.filter(candidate => !candidate.adopted) : rows.filter(row => row.isManaged).map(row => row.credential)).map(item => {
            const name = displayName(item);
            return <Checkbox key={item.auth_index} aria-label={name.primary} checked={selection.includes(item.auth_index)} disabled={isBusy || !eligibleKeys.includes(item.auth_index)} onChange={event => setSelected(keys => event.target.checked ? [...keys, item.auth_index] : keys.filter(key => key !== item.auth_index))}>
              <span className={styles['batch-name']} title={name.primary}>{name.primary}</span>
              {name.secondary && <span className={styles['muted']}> {name.secondary}</span>}
              {'adoptable' in item && !item.adoptable && <span className={styles['muted']}> · {t(automationReasonKey(item.adoption_blocked_reason))}</span>}
              {'adoptable' in item && item.adoptable && !item.auto_disable_supported && <span className={styles['muted']}> · {t('automation.adopt_monitor_only')}</span>}
            </Checkbox>;
          })}
        </div>
        {batchMode === 'adopt' && <><Checkbox disabled={isBusy} checked={shouldEnable} onChange={event => setShouldEnable(event.target.checked)}>{t('automation.adopt_enable_switch')}</Checkbox><Input disabled={isBusy} aria-label={t('automation.adopt_note_placeholder')} placeholder={t('automation.adopt_note_placeholder')} value={note} maxLength={256} onChange={event => setNote(event.target.value)} /></>}
      </div>
    </Modal>
    <Modal open={Boolean(resetTarget)} title={t('automation.manual_reset_title')} onCancel={() => { if (!isBusy) setResetTarget(null); }} confirmLoading={isBusy}
      okButtonProps={{ disabled: !canWrite || !resetTime || Boolean(resetError) }} onOk={() => {
        if (!resetTarget) return;
        const resetAtMs = dayjs(resetTime).valueOf();
        if (!Number.isFinite(resetAtMs) || resetAtMs <= Date.now()) { setResetError(t('automation.manual_reset_must_future')); return; }
        write(() => api.postAccountAutomationManualReset(resetTarget.auth_index, { reset_at_ms: resetAtMs, expected_intent_version: resetTarget.intent_version }), t('automation.manual_reset_success'), () => setResetTarget(null));
      }}>
      <p>{t('automation.manual_reset_desc')}</p><label htmlFor="automation-reset-time">{t('automation.manual_reset_field')} ({getTimeZone()})</label>
      <Input id="automation-reset-time" type="datetime-local" value={resetTime} disabled={isBusy} onChange={event => {
        setResetTime(event.target.value);
        const value = dayjs(event.target.value).valueOf();
        setResetError(Number.isFinite(value) && value > Date.now() ? '' : t('automation.manual_reset_must_future'));
      }} />{resetError && <p className={styles['error']}>{resetError}</p>}
    </Modal>
  </AppDrawer>;
};
