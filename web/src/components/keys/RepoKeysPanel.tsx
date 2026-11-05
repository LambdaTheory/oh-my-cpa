import React from 'react';
import { Button, Input, Modal, Select } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api, ApiError, describeError } from '../../api/client';
import { useT } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import { useToast } from '../feedback';
import { Panel } from '../common/Panel';
import { ResponsiveList } from '../common/ResponsiveList';
import { Tag } from '../common/Tag';
import { SearchInput } from '../common/SearchInput';
import { CopyButton } from '../common/CopyButton';
import { useTimeZone } from '../../utils/TimeZoneProvider';
import dayjs from '../../utils/time';
import type {
  RepoKeyApplication,
  RepoKeyBinding,
  RepoKeyBindingDetail,
  RepoKeyReview,
  RepoKeyUsageWindow,
} from '../../types/repoKeys';
import styles from './RepoKeysPanel.module.css';

const STOP_CATEGORIES = ['rotate', 'misuse', 'leaked', 'other'] as const;
const USAGE_WINDOWS: RepoKeyUsageWindow[] = ['24h', '7d', '30d'];

type ReasonDialog =
  | { kind: 'reject'; id: string; title: string }
  | { kind: 'stop'; id: string; title: string }
  | { kind: 'reenable'; id: string; title: string }
  | { kind: 'reveal'; id: string; title: string }
  | null;

const STATUS_FILTERS = ['all', 'active', 'stopped'] as const;
type StatusFilter = (typeof STATUS_FILTERS)[number];

/**
 * Repository-owned keys, beside the gateway's own key list.
 *
 * The gateway list above edits one shared document; these keys belong to a repository and are
 * requested by an agent over MCP. This panel is where a person decides those requests, sees
 * which applications are still waiting to be fetched, and stops or restores a key. Nothing here
 * shows a key's value except the explicit, audited reveal.
 */
export function RepoKeysPanel() {
  const t = useT();
  const toast = useToast();
  const queryClient = useQueryClient();
  const timeZone = useTimeZone();
  const isDemo = isDemoMode();

  const [search, setSearch] = React.useState('');
  const [statusFilter, setStatusFilter] = React.useState<StatusFilter>('all');
  const [detailID, setDetailID] = React.useState<string | null>(null);
  const [detailWindow, setDetailWindow] = React.useState<RepoKeyUsageWindow>('7d');
  const [dialog, setDialog] = React.useState<ReasonDialog>(null);
  const [reason, setReason] = React.useState('');
  const [category, setCategory] = React.useState<string>('rotate');
  const [revealed, setRevealed] = React.useState<string | null>(null);
  const [reviewID, setReviewID] = React.useState<string | null>(null);
  const [ciphertextApp, setCiphertextApp] = React.useState<RepoKeyApplication | null>(null);
  const [ciphertext, setCiphertext] = React.useState<string | null>(null);
  const [proxyOpen, setProxyOpen] = React.useState(false);
  const [proxyRepo, setProxyRepo] = React.useState('');
  const [proxyPurpose, setProxyPurpose] = React.useState('');
  const [proxyAgent, setProxyAgent] = React.useState('');
  const [proxyPublicKey, setProxyPublicKey] = React.useState('');

  const formatTime = React.useCallback(
    (ms?: number) => (ms ? dayjs(ms).format('YYYY-MM-DD HH:mm:ss') : '—'),
    [timeZone],
  );

  const keysQuery = useQuery({
    queryKey: ['management-repo-keys', 'keys', search, statusFilter],
    queryFn: () => {
      const params = new URLSearchParams();
      if (search.trim()) params.set('q', search.trim());
      if (statusFilter !== 'all') params.set('status', statusFilter);
      const query = params.toString();
      return api.getRepoKeys(query);
    },
  });

  const pendingQuery = useQuery({
    queryKey: ['management-repo-keys', 'applications', 'pending'],
    queryFn: () => api.getRepoKeyApplications({ status: 'pending', limit: 100 }),
    // A re-enable request waits here too, so the queue is not only about first applications.
    refetchInterval: 30_000,
  });

  const awaitingQuery = useQuery({
    queryKey: ['management-repo-keys', 'applications', 'awaiting'],
    queryFn: () => api.getRepoKeyApplications({ status: 'approved', awaiting: true, limit: 100 }),
  });

  const detailQuery = useQuery({
    queryKey: ['management-repo-keys', 'detail', detailID, detailWindow],
    queryFn: () => api.getRepoKeyBinding(detailID as string, detailWindow),
    enabled: detailID !== null,
  });

  const reviewQuery = useQuery({
    queryKey: ['management-repo-keys', 'review', reviewID],
    queryFn: () => api.getRepoKeyReview(reviewID as string),
    enabled: reviewID !== null,
  });

  const auditPushQuery = useQuery({
    queryKey: ['management-repo-keys', 'audit-push'],
    queryFn: () => api.getRepoKeyAuditPush(),
    refetchInterval: 30_000,
  });

  const invalidate = React.useCallback(async () => {
    await queryClient.invalidateQueries({ queryKey: ['management-repo-keys'] });
  }, [queryClient]);

  const failureToast = React.useCallback(
    (err: unknown, fallback: string) => {
      toast.error(err instanceof ApiError ? describeError(err) : fallback);
    },
    [toast],
  );

  const approveMutation = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => api.approveRepoKeyApplication(id, reason),
    onSuccess: async (result) => {
      toast.success(t(result.application.request_kind === 'reenable' ? 'repo_keys.reenabled' : 'repo_keys.approved'));
      setReviewID(null);
      await invalidate();
    },
    onError: (err: unknown) => failureToast(err, t('repo_keys.failed')),
  });

  const rejectMutation = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => api.rejectRepoKeyApplication(id, reason),
    onSuccess: async () => {
      toast.success(t('repo_keys.rejected'));
      setReviewID(null);
      await invalidate();
    },
    onError: (err: unknown) => failureToast(err, t('repo_keys.failed')),
  });

  const stopMutation = useMutation({
    mutationFn: ({ id, category, reason }: { id: string; category: string; reason: string }) =>
      api.stopRepoKeyBinding(id, category, reason),
    onSuccess: async () => {
      toast.success(t('repo_keys.stopped'));
      await invalidate();
    },
    onError: (err: unknown) => failureToast(err, t('repo_keys.failed')),
  });

  const reenableMutation = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => api.requestRepoKeyReenable(id, reason),
    onSuccess: async () => {
      toast.success(t('repo_keys.reenable_requested'));
      await invalidate();
    },
    onError: (err: unknown) => failureToast(err, t('repo_keys.failed')),
  });

  const revealMutation = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => api.revealRepoKeyPlaintext(id, reason),
    onSuccess: async (result: { key: string }) => {
      // The value is held in memory for this dialog only; it is never written to a cache.
      setRevealed(result.key);
      await invalidate();
    },
    onError: (err: unknown) => failureToast(err, t('repo_keys.failed')),
  });

  const ciphertextMutation = useMutation({
    mutationFn: (id: string) => api.fetchRepoKeyCiphertext(id),
    onSuccess: async (result) => {
      setCiphertext(result.ciphertext);
      toast.success(t('repo_keys.ciphertext_fetched'));
      // The application left the delivery queue; the record now carries this hand-out.
      await invalidate();
    },
    onError: (err: unknown) => failureToast(err, t('repo_keys.failed')),
  });

  const proxyMutation = useMutation({
    mutationFn: () =>
      api.applyRepoKeyOnBehalf({
        repo: proxyRepo.trim(),
        purpose: proxyPurpose.trim(),
        agent: proxyAgent.trim(),
        public_key: proxyPublicKey.trim(),
      }),
    onSuccess: async (result) => {
      toast.success(
        result.status === 'approved' ? t('repo_keys.proxy_auto_approved') : t('repo_keys.proxy_submitted'),
      );
      setProxyOpen(false);
      setProxyRepo('');
      setProxyPurpose('');
      setProxyAgent('');
      setProxyPublicKey('');
      await invalidate();
    },
    onError: (err: unknown) => failureToast(err, t('repo_keys.failed')),
  });

  const closeDialog = React.useCallback(() => {
    setDialog(null);
    setReason('');
    setCategory('rotate');
    setRevealed(null);
  }, []);

  const submitDialog = () => {
    if (!dialog) return;
    const trimmed = reason.trim();
    if (trimmed === '') {
      toast.warning(t('repo_keys.reason_required'));
      return;
    }
    switch (dialog.kind) {
      case 'reject':
        rejectMutation.mutate({ id: dialog.id, reason: trimmed });
        break;
      case 'stop':
        stopMutation.mutate({ id: dialog.id, category, reason: trimmed });
        break;
      case 'reenable':
        // A request, not a switch: the key comes back only after a person approves (AC-31).
        reenableMutation.mutate({ id: dialog.id, reason: trimmed });
        break;
      case 'reveal':
        revealMutation.mutate({ id: dialog.id, reason: trimmed });
        return; // stays open so the value can be read
    }
    closeDialog();
  };

  const isDialogBusy =
    rejectMutation.isPending || stopMutation.isPending || reenableMutation.isPending || revealMutation.isPending;

  const bindings = keysQuery.data?.keys ?? [];
  const pending = pendingQuery.data?.applications ?? [];
  const awaiting = awaitingQuery.data?.applications ?? [];

  const applicationColumns: ColumnsType<RepoKeyApplication> = [
    {
      title: t('repo_keys.col_repo'),
      dataIndex: 'repo',
      render: (_: unknown, row) => <span className={styles['mono']}>{row.repo}</span>,
    },
    {
      title: t('repo_keys.col_caller'),
      key: 'caller',
      render: (_: unknown, row) => (
        <span>
          {row.agent} <span className={styles['muted']}>@ {row.device}</span>
        </span>
      ),
    },
    {
      title: t('repo_keys.col_purpose'),
      dataIndex: 'purpose',
      render: (value: string) => <span className={styles['purpose']}>{value}</span>,
    },
    {
      title: t('repo_keys.col_kind'),
      dataIndex: 'kind',
      width: 96,
      render: (value: RepoKeyApplication['kind'], row) =>
        row.request_kind === 'reenable' ? (
          <Tag tone="warn">{t('repo_keys.kind_reenable')}</Tag>
        ) : (
          <Tag>{t(`repo_keys.kind_${value}`)}</Tag>
        ),
    },
    {
      title: t('repo_keys.col_requested'),
      dataIndex: 'created_at_ms',
      width: 170,
      render: (value: number) => formatTime(value),
    },
  ];

  const pendingColumns: ColumnsType<RepoKeyApplication> = [
    ...applicationColumns,
    {
      title: t('repo_keys.col_fingerprint'),
      dataIndex: 'public_key_fingerprint',
      width: 170,
      render: (value: string) =>
        value ? <span className={styles['fingerprint']}>{shortFingerprint(value)}</span> : <span className={styles['muted']}>—</span>,
    },
    {
      title: t('repo_keys.col_actions'),
      key: 'actions',
      width: 240,
      render: (_: unknown, row) => (
        <div className={styles['row-actions']}>
          <Button size="small" onClick={() => setReviewID(row.id)}>
            {t('repo_keys.detail')}
          </Button>
          <Button
            type="primary"
            size="small"
            disabled={isDemo}
            loading={approveMutation.isPending}
            onClick={() => approveMutation.mutate({ id: row.id, reason: '' })}
          >
            {t('repo_keys.approve')}
          </Button>
          <Button
            size="small"
            danger
            disabled={isDemo}
            onClick={() => setDialog({ kind: 'reject', id: row.id, title: row.repo })}
          >
            {t('repo_keys.reject')}
          </Button>
        </div>
      ),
    },
  ];

  const awaitingColumns: ColumnsType<RepoKeyApplication> = [
    ...applicationColumns,
    {
      title: t('repo_keys.col_actions'),
      key: 'actions',
      width: 170,
      render: (_: unknown, row) => (
        <Button
          size="small"
          disabled={isDemo}
          onClick={() => {
            setCiphertextApp(row);
            setCiphertext(null);
            ciphertextMutation.mutate(row.id);
          }}
        >
          {t('repo_keys.fetch_ciphertext')}
        </Button>
      ),
    },
  ];

  const bindingColumns: ColumnsType<RepoKeyBinding> = [
    {
      title: t('repo_keys.col_repo'),
      dataIndex: 'repo',
      render: (value: string, row) => (
        <span>
          <span className={styles['mono']}>{value}</span>
          {row.alias ? <span className={styles['muted']}> · {row.alias}</span> : null}
        </span>
      ),
    },
    {
      title: t('repo_keys.col_key'),
      dataIndex: 'key_mask',
      render: (value: string) => <span className={styles['mono']}>{value}</span>,
    },
    {
      title: t('repo_keys.col_status'),
      dataIndex: 'status',
      width: 130,
      render: (value: RepoKeyBinding['status'], row) =>
        value === 'active' ? (
          <Tag tone="success">{t('repo_keys.status_active')}</Tag>
        ) : (
          <Tag>{t(`repo_keys.stop_${row.stop_category || 'other'}`)}</Tag>
        ),
    },
    {
      title: t('repo_keys.col_last_recipient'),
      dataIndex: 'last_recipient',
      width: 170,
      render: (value: string | undefined) =>
        value ? <span className={styles['purpose']}>{value}</span> : <span className={styles['muted']}>—</span>,
    },
    {
      title: t('repo_keys.col_usage7d'),
      key: 'usage_7d',
      width: 110,
      render: (_: unknown, row) => (row.usage_7d?.requests ?? 0).toLocaleString(),
    },
    {
      title: t('repo_keys.col_created'),
      dataIndex: 'created_at_ms',
      width: 170,
      render: (value: number) => formatTime(value),
    },
    {
      title: t('repo_keys.col_actions'),
      key: 'actions',
      width: 280,
      render: (_: unknown, row) => (
        <div className={styles['row-actions']}>
          <Button size="small" onClick={() => setDetailID(row.id)}>
            {t('repo_keys.detail')}
          </Button>
          {row.status === 'active' ? (
            <>
              <Button
                size="small"
                disabled={isDemo}
                onClick={() => setDialog({ kind: 'reveal', id: row.id, title: row.repo })}
              >
                {t('repo_keys.reveal')}
              </Button>
              <Button
                size="small"
                danger
                disabled={isDemo}
                onClick={() => setDialog({ kind: 'stop', id: row.id, title: row.repo })}
              >
                {t('repo_keys.stop')}
              </Button>
            </>
          ) : (
            <Button
              size="small"
              disabled={isDemo || row.stop_category === 'leaked'}
              title={row.stop_category === 'leaked' ? t('repo_keys.leaked_hint') : undefined}
              onClick={() => setDialog({ kind: 'reenable', id: row.id, title: row.repo })}
            >
              {t('repo_keys.reenable')}
            </Button>
          )}
        </div>
      ),
    },
  ];

  const detail = detailQuery.data;
  const isReasonMissing = reason.trim() === '';
  const auditPush = auditPushQuery.data;

  return (
    <div className={styles['repo-keys']}>
      <Panel className={styles['panel']}>
        <div className={styles['head']}>
          <div>
            <h2 className={styles['title']}>{t('repo_keys.title')}</h2>
            <p className={styles['subtitle']}>{t('repo_keys.subtitle')}</p>
          </div>
          <div className={styles['filters']}>
            <SearchInput
              label={t('repo_keys.search')}
              value={search}
              onChange={setSearch}
              className={styles['search']}
            />
            <Select<StatusFilter>
              aria-label={t('repo_keys.filter_status')}
              value={statusFilter}
              onChange={setStatusFilter}
              className={styles['status-filter']}
              options={STATUS_FILTERS.map((value) => ({
                value,
                label: t(`repo_keys.status_filter_${value}`),
              }))}
            />
            <Button disabled={isDemo} onClick={() => setProxyOpen(true)}>
              {t('repo_keys.proxy_apply')}
            </Button>
          </div>
        </div>

        <section className={styles['section']}>
          <h3 className={styles['section-title']}>
            {t('repo_keys.pending_title', { n: pending.length })}
          </h3>
          <ResponsiveList<RepoKeyApplication>
            rowKey="id"
            columns={pendingColumns}
            dataSource={pending}
            isLoading={pendingQuery.isPending}
            isBlocked={pendingQuery.isError}
            emptyText={t('repo_keys.pending_empty')}
            phone={{ identity: 'repo', actions: ['actions'] }}
            tableProps={{ size: 'small' }}
          />
        </section>

        <section className={styles['section']}>
          <h3 className={styles['section-title']}>
            {t('repo_keys.awaiting_title', { n: awaiting.length })}
          </h3>
          <p className={styles['hint']}>{t('repo_keys.awaiting_hint')}</p>
          <ResponsiveList<RepoKeyApplication>
            rowKey="id"
            columns={awaitingColumns}
            dataSource={awaiting}
            isLoading={awaitingQuery.isPending}
            isBlocked={awaitingQuery.isError}
            emptyText={t('repo_keys.awaiting_empty')}
            phone={{ identity: 'repo', actions: ['actions'] }}
            tableProps={{ size: 'small' }}
          />
        </section>

        <section className={styles['section']}>
          <h3 className={styles['section-title']}>{t('repo_keys.keys_title', { n: bindings.length })}</h3>
          <ResponsiveList<RepoKeyBinding>
            rowKey="id"
            columns={bindingColumns}
            dataSource={bindings}
            isLoading={keysQuery.isPending}
            isBlocked={keysQuery.isError}
            emptyText={t('repo_keys.keys_empty')}
            phone={{ identity: 'repo', actions: ['actions'] }}
            tableProps={{ size: 'small' }}
          />
        </section>

        {/* The audit platform link is what makes "every action is recorded" checkable from here. */}
        <section className={styles['section']}>
          <h3 className={styles['section-title']}>{t('repo_keys.audit_push_title')}</h3>
          {auditPushQuery.isPending && <p className={styles['hint']}>{t('common.loading')}</p>}
          {auditPushQuery.isError && <p className={styles['hint']}>{t('repo_keys.failed')}</p>}
          {auditPush && (
            <dl className={styles['facts']}>
              <div>
                <dt>{t('repo_keys.audit_push_state')}</dt>
                <dd>
                  <Tag tone={auditPush.enabled ? 'success' : undefined}>
                    {t(auditPush.enabled ? 'repo_keys.audit_push_enabled' : 'repo_keys.audit_push_disabled')}
                  </Tag>
                </dd>
              </div>
              <div>
                <dt>{t('repo_keys.audit_push_pending')}</dt>
                <dd>{auditPush.pending.toLocaleString()}</dd>
              </div>
              <div>
                <dt>{t('repo_keys.audit_push_quarantined')}</dt>
                <dd>{auditPush.quarantined.toLocaleString()}</dd>
              </div>
              <div>
                <dt>{t('repo_keys.audit_push_last_success')}</dt>
                <dd>{formatTime(auditPush.last_success_ms || undefined)}</dd>
              </div>
            </dl>
          )}
          <Link className={styles['audit-link']} to="/audit">
            {t('repo_keys.detail_audit')}
          </Link>
        </section>
      </Panel>

      {/* The reason dialog: a rejection states why the caller was refused, a stop states why the
          key left service, a re-enable states why it should come back, and a reveal states why the
          value has to be read. All four are part of the audit trail. */}
      <Modal
        open={dialog !== null}
        title={dialog ? t(`repo_keys.dialog_${dialog.kind}`, { repo: dialog.title }) : ''}
        onCancel={closeDialog}
        onOk={submitDialog}
        okText={dialog?.kind === 'reveal' ? t('repo_keys.reveal_confirm') : t('common.confirm')}
        cancelText={t('common.cancel')}
        confirmLoading={isDialogBusy}
        okButtonProps={{ disabled: isReasonMissing }}
        destroyOnHidden
      >
        {dialog?.kind === 'stop' && (
          <div className={styles['field']}>
            <label htmlFor="repo-key-stop-category">{t('repo_keys.category')}</label>
            <Select
              id="repo-key-stop-category"
              value={category}
              onChange={setCategory}
              options={STOP_CATEGORIES.map((value) => ({
                value,
                label: t(`repo_keys.stop_${value}`),
              }))}
            />
            <p className={styles['hint']}>{t('repo_keys.stop_warning')}</p>
          </div>
        )}
        {dialog?.kind === 'reenable' && <p className={styles['hint']}>{t('repo_keys.reenable_hint')}</p>}
        {dialog?.kind === 'reveal' && revealed === null && (
          <p className={styles['hint']}>{t('repo_keys.reveal_hint')}</p>
        )}
        <div className={styles['field']}>
          <label htmlFor="repo-key-reason">{t('repo_keys.reason')}</label>
          <Input.TextArea
            id="repo-key-reason"
            value={reason}
            onChange={(event) => setReason(event.target.value)}
            rows={3}
            maxLength={200}
            showCount
            disabled={revealed !== null}
          />
        </div>
        {revealed !== null && (
          <div className={styles['revealed']}>
            <label htmlFor="repo-key-revealed">{t('repo_keys.revealed_value')}</label>
            <Input id="repo-key-revealed" readOnly value={revealed} className={styles['mono']} />
            <div className={styles['revealed-actions']}>
              <CopyButton text={revealed} label={t('repo_keys.copy')} showLabel />
              <Button size="small" danger onClick={closeDialog}>
                {t('repo_keys.hide')}
              </Button>
            </div>
          </div>
        )}
      </Modal>

      {/* The same snapshot the one-time link shows, so a person decides with the same information
          from the queue and from a link (AC-8). */}
      <Modal
        open={reviewID !== null}
        title={reviewQuery.data ? t('repo_keys.review_title', { repo: reviewQuery.data.application.repo }) : ''}
        onCancel={() => setReviewID(null)}
        destroyOnHidden
        width={760}
        footer={
          reviewQuery.data?.application.status === 'pending' ? (
            <div className={styles['row-actions']}>
              <Button onClick={() => setReviewID(null)}>{t('common.cancel')}</Button>
              <Button
                danger
                disabled={isDemo}
                onClick={() => {
                  const application = reviewQuery.data?.application;
                  if (application) setDialog({ kind: 'reject', id: application.id, title: application.repo });
                }}
              >
                {t('repo_keys.reject')}
              </Button>
              <Button
                type="primary"
                disabled={isDemo || reviewQuery.data.approval_expired}
                loading={approveMutation.isPending}
                onClick={() => {
                  const application = reviewQuery.data?.application;
                  if (application) approveMutation.mutate({ id: application.id, reason: '' });
                }}
              >
                {t('repo_keys.approve')}
              </Button>
            </div>
          ) : null
        }
      >
        {reviewQuery.isPending && <p>{t('common.loading')}</p>}
        {reviewQuery.isError && <p>{t('repo_keys.failed')}</p>}
        {reviewQuery.data && <RepoKeyReviewView review={reviewQuery.data} formatTime={formatTime} t={t} />}
      </Modal>

      {/* An awaiting envelope is taken here; the row leaves the queue after the first fetch. */}
      <Modal
        open={ciphertextApp !== null}
        title={t('repo_keys.ciphertext_title', { repo: ciphertextApp?.repo ?? '' })}
        onCancel={() => {
          setCiphertextApp(null);
          setCiphertext(null);
        }}
        footer={null}
        destroyOnHidden
      >
        <p className={styles['hint']}>{t('repo_keys.ciphertext_hint')}</p>
        <dl className={styles['facts']}>
          <div>
            <dt>{t('repo_keys.col_caller')}</dt>
            <dd>
              {ciphertextApp?.agent} @ {ciphertextApp?.device}
            </dd>
          </div>
          <div>
            <dt>{t('repo_keys.col_key')}</dt>
            <dd className={styles['mono']}>{ciphertextApp?.key_mask || '—'}</dd>
          </div>
          <div>
            <dt>{t('repo_keys.col_fingerprint')}</dt>
            <dd className={styles['fingerprint']}>
              {ciphertextApp?.public_key_fingerprint ? shortFingerprint(ciphertextApp.public_key_fingerprint) : '—'}
            </dd>
          </div>
        </dl>
        {ciphertextMutation.isPending && <p>{t('common.loading')}</p>}
        {ciphertext && (
          <div className={styles['revealed']}>
            <label htmlFor="repo-key-ciphertext">{t('repo_keys.ciphertext_value')}</label>
            <Input.TextArea id="repo-key-ciphertext" readOnly value={ciphertext} autoSize={{ minRows: 3, maxRows: 8 }} />
            <div className={styles['revealed-actions']}>
              <CopyButton text={ciphertext} label={t('repo_keys.copy')} showLabel />
            </div>
          </div>
        )}
      </Modal>

      {/* The console's stand-in for an agent that cannot reach MCP yet (AC-29). */}
      <Modal
        open={proxyOpen}
        title={t('repo_keys.proxy_title')}
        onCancel={() => setProxyOpen(false)}
        onOk={() => proxyMutation.mutate()}
        okText={t('repo_keys.proxy_submit')}
        cancelText={t('common.cancel')}
        confirmLoading={proxyMutation.isPending}
        okButtonProps={{
          disabled:
            proxyRepo.trim() === '' ||
            proxyPurpose.trim() === '' ||
            proxyAgent.trim() === '' ||
            proxyPublicKey.trim() === '',
        }}
        destroyOnHidden
      >
        <p className={styles['hint']}>{t('repo_keys.proxy_hint')}</p>
        <div className={styles['field']}>
          <label htmlFor="repo-key-proxy-repo">{t('repo_keys.col_repo')}</label>
          <Input
            id="repo-key-proxy-repo"
            value={proxyRepo}
            onChange={(event) => setProxyRepo(event.target.value)}
            placeholder="LambdaTheory/oh-my-cpa"
          />
        </div>
        <div className={styles['field']}>
          <label htmlFor="repo-key-proxy-agent">{t('repo_keys.proxy_agent')}</label>
          <Input id="repo-key-proxy-agent" value={proxyAgent} onChange={(event) => setProxyAgent(event.target.value)} />
        </div>
        <div className={styles['field']}>
          <label htmlFor="repo-key-proxy-purpose">{t('repo_keys.col_purpose')}</label>
          <Input
            id="repo-key-proxy-purpose"
            value={proxyPurpose}
            onChange={(event) => setProxyPurpose(event.target.value)}
          />
        </div>
        <div className={styles['field']}>
          <label htmlFor="repo-key-proxy-public-key">{t('repo_keys.proxy_public_key')}</label>
          <Input.TextArea
            id="repo-key-proxy-public-key"
            value={proxyPublicKey}
            onChange={(event) => setProxyPublicKey(event.target.value)}
            autoSize={{ minRows: 2, maxRows: 4 }}
            placeholder="x25519:..."
          />
        </div>
      </Modal>

      <Modal
        open={detailID !== null}
        title={t('repo_keys.detail_title', { repo: detail?.key.repo ?? '' })}
        footer={null}
        width={820}
        onCancel={() => setDetailID(null)}
        destroyOnHidden
      >
        {detailQuery.isPending && <p>{t('common.loading')}</p>}
        {detailQuery.isError && <p>{t('repo_keys.failed')}</p>}
        {detail && (
          <RepoKeyDetail
            detail={detail}
            formatTime={formatTime}
            t={t}
            window={detailWindow}
            onWindowChange={setDetailWindow}
          />
        )}
      </Modal>
    </div>
  );
}

/** The queue's and the link's shared snapshot: the request, the repository now, and the impact. */
function RepoKeyReviewView({
  review,
  formatTime,
  t,
}: {
  review: RepoKeyReview;
  formatTime: (ms?: number) => string;
  t: ReturnType<typeof useT>;
}) {
  const application = review.application;
  const repo = review.repo;
  return (
    <div className={styles['detail']}>
      {review.approval_expired && <p className={styles['impact']}>{t('repo_keys.review_expired')}</p>}
      <dl className={styles['facts']}>
        <div>
          <dt>{t('repo_keys.col_repo')}</dt>
          <dd className={styles['mono']}>{application.repo}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_caller')}</dt>
          <dd>
            {application.agent} @ {application.device}
          </dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_purpose')}</dt>
          <dd>{application.purpose}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_kind')}</dt>
          <dd>{t(application.request_kind === 'reenable' ? 'repo_keys.kind_reenable' : `repo_keys.kind_${application.kind}`)}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_fingerprint')}</dt>
          <dd className={styles['fingerprint']}>
            {application.public_key_fingerprint ? shortFingerprint(application.public_key_fingerprint) : '—'}
          </dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_requested')}</dt>
          <dd>{formatTime(application.created_at_ms)}</dd>
        </div>
      </dl>

      {review.public_key_changed && (
        <p className={styles['impact']}>
          {t('repo_keys.review_fingerprint_changed', {
            previous: shortFingerprint(review.previous_public_key_fingerprint || ''),
          })}
        </p>
      )}

      <h4 className={styles['detail-title']}>{t('repo_keys.review_repo_state')}</h4>
      <dl className={styles['facts']}>
        <div>
          <dt>{t('repo_keys.col_status')}</dt>
          <dd>{t(`repo_keys.repo_state_${repo.state}`)}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_key')}</dt>
          <dd className={styles['mono']}>{repo.key_mask || '—'}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.fact_created_by')}</dt>
          <dd>{repo.created_by || '—'}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_last_recipient')}</dt>
          <dd>{repo.last_recipient || '—'}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_usage7d')}</dt>
          <dd>{repo.usage.requests.toLocaleString()}</dd>
        </div>
      </dl>

      <p className={styles['impact']}>{t(`repo_keys.impact_${review.impact}`)}</p>

      {repo.stops.length > 0 && (
        <>
          <h4 className={styles['detail-title']}>{t('repo_keys.detail_stops')}</h4>
          <ul className={styles['detail-list']}>
            {repo.stops.map((stop) => (
              <li key={stop.id}>
                {t(`repo_keys.stop_${stop.category}`)} · {stop.reason} · {formatTime(stop.stopped_at_ms)}
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function RepoKeyDetail({
  detail,
  formatTime,
  t,
  window,
  onWindowChange,
}: {
  detail: RepoKeyBindingDetail;
  formatTime: (ms?: number) => string;
  t: ReturnType<typeof useT>;
  window: RepoKeyUsageWindow;
  onWindowChange: (value: RepoKeyUsageWindow) => void;
}) {
  const usage = detail.usage;
  return (
    <div className={styles['detail']}>
      <h4 className={styles['detail-title']}>{t('repo_keys.detail_basic')}</h4>
      <dl className={styles['facts']}>
        <div>
          <dt>{t('repo_keys.col_repo')}</dt>
          <dd className={styles['mono']}>{detail.key.repo}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_key')}</dt>
          <dd className={styles['mono']}>{detail.key.key_mask}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_status')}</dt>
          <dd>{detail.key.status === 'active' ? t('repo_keys.status_active') : t(`repo_keys.stop_${detail.key.stop_category || 'other'}`)}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.fact_created_by')}</dt>
          <dd>{detail.key.created_by || '—'}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_created')}</dt>
          <dd>{formatTime(detail.key.created_at_ms)}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.col_last_recipient')}</dt>
          <dd>{detail.key.last_recipient || '—'}</dd>
        </div>
        {detail.key.status === 'stopped' && (
          <div>
            <dt>{t('repo_keys.stop')}</dt>
            <dd>
              {detail.key.stopped_by || '—'}
              {detail.key.stop_reason ? ` · ${detail.key.stop_reason}` : ''}
              {detail.key.stopped_at_ms ? ` · ${formatTime(detail.key.stopped_at_ms)}` : ''}
            </dd>
          </div>
        )}
      </dl>

      <h4 className={styles['detail-title']}>{t('repo_keys.detail_usage')}</h4>
      <div className={styles['filters']}>
        <Select<RepoKeyUsageWindow>
          aria-label={t('repo_keys.detail_window')}
          value={window}
          onChange={onWindowChange}
          options={USAGE_WINDOWS.map((value) => ({
            value,
            label: t(`repo_keys.window_${value}`),
          }))}
        />
      </div>
      <dl className={styles['facts']}>
        <div>
          <dt>{t('repo_keys.fact_requests')}</dt>
          <dd>{usage.requests.toLocaleString()}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.fact_failed')}</dt>
          <dd>{usage.failed.toLocaleString()}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.fact_tokens')}</dt>
          <dd>{usage.total_tokens.toLocaleString()}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.fact_last_used')}</dt>
          <dd>{formatTime(usage.last_used_ms || undefined)}</dd>
        </div>
      </dl>

      <h4 className={styles['detail-title']}>{t('repo_keys.detail_models')}</h4>
      {usage.models.length === 0 ? (
        <p className={styles['muted']}>{t('repo_keys.detail_none')}</p>
      ) : (
        <ul className={styles['detail-list']}>
          {usage.models.map((model) => (
            <li key={model.model}>
              <span className={styles['mono']}>{model.model}</span>
              {' · '}
              {t('repo_keys.model_usage', {
                requests: model.requests.toLocaleString(),
                failed: model.failed.toLocaleString(),
                tokens: model.tokens.toLocaleString(),
              })}
            </li>
          ))}
        </ul>
      )}

      {usage.stopped_after && (
        <p className={styles['impact']}>
          {t('repo_keys.detail_stopped_after', {
            rejected: usage.stopped_after.rejected.toLocaleString(),
            since: formatTime(usage.stopped_after.since_ms),
          })}
        </p>
      )}

      <h4 className={styles['detail-title']}>{t('repo_keys.detail_applications')}</h4>
      {detail.applications.length === 0 ? (
        <p className={styles['muted']}>{t('repo_keys.detail_none')}</p>
      ) : (
        <ul className={styles['detail-list']}>
          {detail.applications.map((application) => (
            <li key={application.id}>
              <span className={styles['mono']}>{application.agent}</span> @ {application.device}
              {' · '}
              {t(`repo_keys.kind_${application.kind}`)}
              {application.key_mask ? ` · ${application.key_mask}` : ''}
              {' · '}
              {t(`repo_keys.application_status_${application.status}`)}
              {' · '}
              {formatTime(application.created_at_ms)}
              {application.decided_by ? ` · ${t('repo_keys.decision_by', { name: application.decided_by })}` : ''}
              {application.decision_reason ? ` · ${application.decision_reason}` : ''}
              {application.reject_reason ? ` · ${application.reject_reason}` : ''}
            </li>
          ))}
        </ul>
      )}

      <h4 className={styles['detail-title']}>{t('repo_keys.detail_deliveries')}</h4>
      {detail.deliveries.length === 0 ? (
        <p className={styles['muted']}>{t('repo_keys.detail_none')}</p>
      ) : (
        <ul className={styles['detail-list']}>
          {detail.deliveries.map((delivery) => (
            <li key={delivery.id}>
              {delivery.agent} @ {delivery.device}
              {' · '}
              {t(`repo_keys.kind_${delivery.kind as 'issue' | 'reuse' | 'reissue'}`)}
              {' · '}
              {formatTime(delivery.delivered_at_ms)}
              {' · '}
              {t('repo_keys.delivery_operator', { name: delivery.operator })}
              {delivery.public_key_fingerprint
                ? ` · ${t('repo_keys.delivery_fingerprint', { value: shortFingerprint(delivery.public_key_fingerprint) })}`
                : ''}
            </li>
          ))}
        </ul>
      )}

      <h4 className={styles['detail-title']}>{t('repo_keys.detail_stops')}</h4>
      {detail.stops.length === 0 ? (
        <p className={styles['muted']}>{t('repo_keys.detail_none')}</p>
      ) : (
        <ul className={styles['detail-list']}>
          {detail.stops.map((stop) => (
            <li key={stop.id}>
              {t(`repo_keys.stop_${stop.category}`)}
              {' · '}
              {stop.reason}
              {' · '}
              {formatTime(stop.stopped_at_ms)}
              {stop.reenabled_at_ms ? ` · ${t('repo_keys.detail_reenabled')} ${formatTime(stop.reenabled_at_ms)}` : ''}
            </li>
          ))}
        </ul>
      )}

      <Link className={styles['audit-link']} to={`/audit?q=${encodeURIComponent(detail.key.repo)}`}>
        {t('repo_keys.detail_audit')}
      </Link>
    </div>
  );
}

/** A fingerprint is an identity, not a secret; the head and tail are enough to compare two. */
function shortFingerprint(value: string): string {
  if (value.length <= 24) return value;
  return `${value.slice(0, 14)}…${value.slice(-8)}`;
}
