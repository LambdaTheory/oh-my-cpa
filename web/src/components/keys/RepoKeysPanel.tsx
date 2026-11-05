import React from 'react';
import { Button, Input, Modal, Select } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
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
} from '../../types/repoKeys';
import styles from './RepoKeysPanel.module.css';

const STOP_CATEGORIES = ['rotate', 'misuse', 'leaked', 'other'] as const;

type ReasonDialog =
  | { kind: 'reject'; id: string; title: string }
  | { kind: 'stop'; id: string; title: string }
  | { kind: 'reenable'; id: string; title: string }
  | { kind: 'reveal'; id: string; title: string }
  | null;

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
  const [detailID, setDetailID] = React.useState<string | null>(null);
  const [dialog, setDialog] = React.useState<ReasonDialog>(null);
  const [reason, setReason] = React.useState('');
  const [category, setCategory] = React.useState<string>('rotate');
  const [reenableKey, setReenableKey] = React.useState('');
  const [revealed, setRevealed] = React.useState<string | null>(null);

  const formatTime = React.useCallback(
    (ms?: number) => (ms ? dayjs(ms).format('YYYY-MM-DD HH:mm:ss') : '—'),
    [timeZone],
  );

  const keysQuery = useQuery({
    queryKey: ['management-repo-keys', 'keys', search],
    queryFn: () => api.getRepoKeys(search ? `q=${encodeURIComponent(search)}` : ''),
  });

  const pendingQuery = useQuery({
    queryKey: ['management-repo-keys', 'applications', 'pending'],
    queryFn: () => api.getRepoKeyApplications({ status: 'pending', limit: 100 }),
  });

  const awaitingQuery = useQuery({
    queryKey: ['management-repo-keys', 'applications', 'awaiting'],
    queryFn: () => api.getRepoKeyApplications({ status: 'approved', awaiting: true, limit: 100 }),
  });

  const detailQuery = useQuery({
    queryKey: ['management-repo-keys', 'detail', detailID],
    queryFn: () => api.getRepoKeyBinding(detailID as string),
    enabled: detailID !== null,
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
    mutationFn: (id: string) => api.approveRepoKeyApplication(id),
    onSuccess: async () => {
      toast.success(t('repo_keys.approved'));
      await invalidate();
    },
    onError: (err: unknown) => failureToast(err, t('repo_keys.failed')),
  });

  const rejectMutation = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => api.rejectRepoKeyApplication(id, reason),
    onSuccess: async () => {
      toast.success(t('repo_keys.rejected'));
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
    mutationFn: ({ id, reason, key }: { id: string; reason: string; key: string }) =>
      api.reenableRepoKeyBinding(id, reason, key),
    onSuccess: async () => {
      toast.success(t('repo_keys.reenabled'));
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

  const closeDialog = React.useCallback(() => {
    setDialog(null);
    setReason('');
    setReenableKey('');
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
      case 'reenable': {
        const key = reenableKey.trim();
        if (key === '') {
          toast.warning(t('repo_keys.key_required'));
          return;
        }
        reenableMutation.mutate({ id: dialog.id, reason: trimmed, key });
        break;
      }
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
      render: (value: RepoKeyApplication['kind']) => <Tag>{t(`repo_keys.kind_${value}`)}</Tag>,
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
      title: t('repo_keys.col_actions'),
      key: 'actions',
      width: 200,
      render: (_: unknown, row) => (
        <div className={styles['row-actions']}>
          <Button
            type="primary"
            size="small"
            disabled={isDemo}
            loading={approveMutation.isPending}
            onClick={() => approveMutation.mutate(row.id)}
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

  const bindingColumns: ColumnsType<RepoKeyBinding> = [
    {
      title: t('repo_keys.col_repo'),
      dataIndex: 'repo',
      render: (value: string) => <span className={styles['mono']}>{value}</span>,
    },
    {
      title: t('repo_keys.col_key'),
      dataIndex: 'key_mask',
      render: (value: string) => <span className={styles['mono']}>{value}</span>,
    },
    {
      title: t('repo_keys.col_status'),
      dataIndex: 'status',
      width: 120,
      render: (value: RepoKeyBinding['status'], row) =>
        value === 'active' ? (
          <Tag tone="success">{t('repo_keys.status_active')}</Tag>
        ) : (
          <Tag>{t(`repo_keys.stop_${row.stop_category || 'other'}`)}</Tag>
        ),
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
      width: 260,
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

  return (
    <div className={styles['repo-keys']}>
      <Panel className={styles['panel']}>
        <div className={styles['head']}>
          <div>
            <h2 className={styles['title']}>{t('repo_keys.title')}</h2>
            <p className={styles['subtitle']}>{t('repo_keys.subtitle')}</p>
          </div>
          <SearchInput
            label={t('repo_keys.search')}
            value={search}
            onChange={setSearch}
            className={styles['search']}
          />
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
          <ResponsiveList<RepoKeyApplication>
            rowKey="id"
            columns={applicationColumns}
            dataSource={awaiting}
            isLoading={awaitingQuery.isPending}
            isBlocked={awaitingQuery.isError}
            emptyText={t('repo_keys.awaiting_empty')}
            phone={{ identity: 'repo' }}
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
      </Panel>

      {/* The reason dialog: a rejection states why the caller was refused, a stop states why the
          key left service, a re-enable states why it came back, and a reveal states why the value
          has to be read. All four are part of the audit trail. */}
      <Modal
        open={dialog !== null}
        title={dialog ? t(`repo_keys.dialog_${dialog.kind}`, { repo: dialog.title }) : ''}
        onCancel={closeDialog}
        onOk={submitDialog}
        okText={dialog?.kind === 'reveal' ? t('repo_keys.reveal_confirm') : t('common.confirm')}
        cancelText={t('common.cancel')}
        confirmLoading={isDialogBusy}
        okButtonProps={{ disabled: isReasonMissing || (dialog?.kind === 'reenable' && reenableKey.trim() === '') }}
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
          </div>
        )}
        {dialog?.kind === 'reenable' && (
          <div className={styles['field']}>
            <label htmlFor="repo-key-reenable-value">{t('repo_keys.key_value')}</label>
            <Input.Password
              id="repo-key-reenable-value"
              value={reenableKey}
              onChange={(event) => setReenableKey(event.target.value)}
              placeholder={t('repo_keys.key_value_placeholder')}
              autoComplete="off"
            />
            <p className={styles['hint']}>{t('repo_keys.reenable_hint')}</p>
          </div>
        )}
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

      <Modal
        open={detailID !== null}
        title={t('repo_keys.detail_title', { repo: detail?.key.repo ?? '' })}
        footer={null}
        width={720}
        onCancel={() => setDetailID(null)}
        destroyOnHidden
      >
        {detailQuery.isPending && <p>{t('common.loading')}</p>}
        {detailQuery.isError && <p>{t('repo_keys.failed')}</p>}
        {detail && <RepoKeyDetail detail={detail} formatTime={formatTime} t={t} />}
      </Modal>
    </div>
  );
}

function RepoKeyDetail({
  detail,
  formatTime,
  t,
}: {
  detail: RepoKeyBindingDetail;
  formatTime: (ms?: number) => string;
  t: ReturnType<typeof useT>;
}) {
  return (
    <div className={styles['detail']}>
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
          <dt>{t('repo_keys.fact_requests')}</dt>
          <dd>{detail.usage.requests.toLocaleString()}</dd>
        </div>
        <div>
          <dt>{t('repo_keys.fact_last_used')}</dt>
          <dd>{formatTime(detail.usage.last_used_ms || undefined)}</dd>
        </div>
      </dl>

      <h4 className={styles['detail-title']}>{t('repo_keys.detail_applications')}</h4>
      {detail.applications.length === 0 ? (
        <p className={styles['muted']}>{t('repo_keys.detail_none')}</p>
      ) : (
        <ul className={styles['detail-list']}>
          {detail.applications.map((application) => (
            <li key={application.id}>
              <span className={styles['mono']}>{application.agent}</span> @ {application.device}
              {' · '}
              {t(`repo_keys.application_status_${application.status}`)}
              {' · '}
              {formatTime(application.created_at_ms)}
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
    </div>
  );
}
