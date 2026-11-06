import React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from 'antd';
import { useNavigate } from 'react-router-dom';
import { useT } from '../../i18n';
import { PageHeader } from '../../components/common/PageHeader';
import { PageLoading } from '../../components/common/PageLoading';
import { RefreshButton } from '../../components/common/RefreshButton';
import { EmptyState } from '../../components/common/EmptyState';
import { ConfirmAction } from '../../components/common/ConfirmAction';
import { LoadFailure, Notice, useToast } from '../../components/feedback';
import { PlusOutlined } from '../../components/icons';
import {
  MODEL_GROUP_AVAILABILITY_BANDS,
  MODEL_GROUP_READINGS_QUERY_KEY,
  MODEL_GROUPS_QUERY_KEY,
  countModelGroupAvailability,
  formatModelGroupBalance,
  modelGroupReading,
  modelGroupRefusalCode,
  withoutModelGroup,
  type ModelGroup,
  type ModelGroupList,
  type ModelGroupRestoreResult,
} from '../../types/modelGroups';
import { modelGroupApi } from './api';
import { ModelGroupAvailabilityTag } from './ModelGroupAvailabilityTag';
import { ModelGroupNameDialog } from './ModelGroupNameDialog';
import styles from './ModelGroups.module.css';

type NameDialog = { mode: 'create' } | { mode: 'rename'; group: ModelGroup };

/**
 * The operator's model groups, one card each.
 *
 * A group is a name and an ordered list of model names, so the card states the facts a reader
 * needs to decide which one to open: how it is called, how many models it holds, how they fall
 * across the four availability bands, and which model comes first with its balance. The bands
 * come from `GET /management/model-groups/readings`, the same projection the per-group query
 * reports, so a card and a caller never read the same model differently. When no readings could
 * be loaded the card says so instead of drawing every member as "unknown".
 */
export const ModelGroupsPage: React.FC = () => {
  const t = useT();
  const toast = useToast();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [dialog, setDialog] = React.useState<NameDialog | null>(null);
  const [nameError, setNameError] = React.useState<string | null>(null);

  const groupsQuery = useQuery({
    queryKey: MODEL_GROUPS_QUERY_KEY,
    queryFn: modelGroupApi.list,
  });
  const groups = groupsQuery.data?.groups ?? [];
  const readingsQuery = useQuery({
    queryKey: MODEL_GROUP_READINGS_QUERY_KEY,
    queryFn: modelGroupApi.readings,
  });
  const readings = readingsQuery.data;

  const refresh = () => {
    void groupsQuery.refetch();
    void readingsQuery.refetch();
  };
  const settleList = () => { void queryClient.invalidateQueries({ queryKey: MODEL_GROUPS_QUERY_KEY }); };

  /** A refusal the inline checks could not predict, in the reader's language. */
  const refusalMessage = (error: unknown): string | undefined => {
    switch (modelGroupRefusalCode(error)) {
      case 'model_group_name_conflict': return t('model_groups.name_conflict');
      case 'model_group_invalid_name': return t('model_groups.name_invalid');
      case 'model_group_not_found': return t('model_groups.not_found');
      default: return undefined;
    }
  };

  const createMutation = useMutation({
    mutationFn: (name: string) => modelGroupApi.create(name),
    onSuccess: (group) => {
      queryClient.setQueryData<ModelGroupList>(MODEL_GROUPS_QUERY_KEY, (current) => ({
        groups: [...(current?.groups ?? []), group],
      }));
      settleList();
      toast.success(t('model_groups.created', { name: group.name }));
      setDialog(null);
      setNameError(null);
    },
    onError: (error) => setNameError(refusalMessage(error) ?? t('model_groups.save_failed')),
  });

  const renameMutation = useMutation({
    mutationFn: ({ id, name }: { id: string; name: string }) => modelGroupApi.rename(id, name),
    onSuccess: (group) => {
      queryClient.setQueryData<ModelGroupList>(MODEL_GROUPS_QUERY_KEY, (current) => ({
        groups: (current?.groups ?? []).map((candidate) => (candidate.id === group.id ? group : candidate)),
      }));
      settleList();
      toast.success(t('model_groups.renamed', { name: group.name }));
      setDialog(null);
      setNameError(null);
    },
    onError: (error) => setNameError(refusalMessage(error) ?? t('model_groups.save_failed')),
  });

  const deleteMutation = useMutation({
    mutationFn: (group: ModelGroup) => modelGroupApi.remove(group.id).then(() => group),
    onSuccess: (group) => {
      queryClient.setQueryData<ModelGroupList>(MODEL_GROUPS_QUERY_KEY, (current) => withoutModelGroup(current, group.id));
      settleList();
      toast.success(t('model_groups.deleted', { name: group.name }));
    },
    onError: (error) => toast.error(t('model_groups.delete_failed'), { error }),
  });

  const restoreMutation = useMutation({
    mutationFn: modelGroupApi.restoreDefaults,
    onSuccess: (result: ModelGroupRestoreResult) => {
      settleList();
      if (result.created.length === 0) {
        toast.info(t('model_groups.restored_none'));
        return;
      }
      toast.success(t('model_groups.restored', { created: result.created.length, skipped: result.skipped.length }));
    },
    onError: (error) => toast.error(t('model_groups.restore_failed'), { error }),
  });

  const openDialog = (next: NameDialog) => {
    setNameError(null);
    setDialog(next);
  };

  const submitName = (name: string) => {
    if (!dialog) return;
    if (dialog.mode === 'create') createMutation.mutate(name);
    else renameMutation.mutate({ id: dialog.group.id, name });
  };

  return (
    <div className="terminal-page terminal-page-stack model-groups-page">
      <PageHeader
        title={t('model_groups.title')}
        subtitle={groupsQuery.data ? t('model_groups.count', { count: groups.length }) : undefined}
        actions={
          <>
            <RefreshButton onRefresh={refresh} isRefreshing={groupsQuery.isFetching} />
            <ConfirmAction
              title={t('model_groups.restore_title')}
              description={t('model_groups.restore_description')}
              confirmLabel={t('model_groups.restore_confirm')}
              isConfirming={restoreMutation.isPending}
              onConfirm={() => restoreMutation.mutate()}
            >
              <Button>{t('model_groups.restore_defaults')}</Button>
            </ConfirmAction>
            <Button type="primary" icon={<PlusOutlined />} onClick={() => openDialog({ mode: 'create' })}>
              {t('model_groups.create')}
            </Button>
          </>
        }
      />

      {readingsQuery.isError && !readings && (
        <Notice tone="warning" title={t('model_groups.readings_failed')} description={t('model_groups.readings_failed_description')} />
      )}

      {groupsQuery.isLoading ? (
        <PageLoading variant="block" />
      ) : groupsQuery.isError && !groupsQuery.data ? (
        <LoadFailure
          title={t('model_groups.load_failed')}
          error={groupsQuery.error}
          onRetry={refresh}
        />
      ) : groups.length === 0 ? (
        <EmptyState
          title={t('model_groups.empty_title')}
          description={t('model_groups.empty_description')}
        >
          <Button type="primary" icon={<PlusOutlined />} onClick={() => openDialog({ mode: 'create' })}>
            {t('model_groups.create')}
          </Button>
        </EmptyState>
      ) : (
        <div className={styles['cards']}>
          {groups.map((group) => {
            const preferred = group.members[0];
            const counts = countModelGroupAvailability(group.members.map((member) => member.model), readings);
            const preferredReading = preferred ? modelGroupReading(readings, preferred.model) : undefined;
            return (
              <article key={group.id} className={styles['card']} data-model-group-card={group.id}>
                <div className={styles['card-head']}>
                  <h2 className={styles['card-name']}>{group.name}</h2>
                </div>
                <p className={styles['card-meta']}>{t('model_groups.member_count', { count: group.members.length })}</p>
                {readings && group.members.length > 0 && (
                  <>
                    <ul className={styles['card-bands']} aria-label={t('model_groups.band_counts')}>
                      {MODEL_GROUP_AVAILABILITY_BANDS.map((band) => (
                        <li key={band} className={styles['card-band']} data-model-group-band={band}>
                          <ModelGroupAvailabilityTag availability={band} />
                          <span className={styles['card-band-count']}>{counts[band]}</span>
                        </li>
                      ))}
                    </ul>
                    <p className={styles['card-preferred']} data-model-group-preferred={preferred.model}>
                      <span className={styles['card-preferred-label']}>{t('model_groups.preferred')}</span>
                      <code className={styles['model-name']}>{preferred.model}</code>
                      <span className={styles['card-balance']}>
                        {formatModelGroupBalance(preferredReading?.balance) || '—'}
                      </span>
                    </p>
                  </>
                )}
                <div className={styles['card-actions']}>
                  <Button onClick={() => navigate(`/model-groups/${encodeURIComponent(group.id)}`)}>
                    {t('model_groups.open')}
                  </Button>
                  <Button type="text" size="small" onClick={() => openDialog({ mode: 'rename', group })}>
                    {t('model_groups.rename')}
                  </Button>
                  <ConfirmAction
                    title={t('model_groups.delete_title', { name: group.name })}
                    description={t('model_groups.delete_description')}
                    confirmLabel={t('common.delete')}
                    isDanger
                    isConfirming={deleteMutation.isPending}
                    onConfirm={() => deleteMutation.mutate(group)}
                  >
                    <Button type="text" size="small" danger>{t('common.delete')}</Button>
                  </ConfirmAction>
                </div>
              </article>
            );
          })}
        </div>
      )}

      <ModelGroupNameDialog
        open={dialog !== null}
        mode={dialog?.mode ?? 'create'}
        initialName={dialog?.mode === 'rename' ? dialog.group.name : undefined}
        groups={groups}
        exceptId={dialog?.mode === 'rename' ? dialog.group.id : undefined}
        isSaving={createMutation.isPending || renameMutation.isPending}
        serverError={nameError}
        onDraftChange={() => setNameError(null)}
        onCancel={() => { setDialog(null); setNameError(null); }}
        onSubmit={submitName}
      />
    </div>
  );
};
