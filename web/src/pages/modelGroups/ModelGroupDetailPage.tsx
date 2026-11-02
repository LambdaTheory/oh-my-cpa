import React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useNavigate, useParams } from 'react-router-dom';
import { useT } from '../../i18n';
import { PageHeader } from '../../components/common/PageHeader';
import { PageLoading } from '../../components/common/PageLoading';
import { RefreshButton } from '../../components/common/RefreshButton';
import { EmptyState } from '../../components/common/EmptyState';
import { ResponsiveList } from '../../components/common/ResponsiveList';
import { Tag } from '../../components/common/Tag';
import { Notice, LoadFailure, useToast } from '../../components/feedback';
import { ArrowLeftOutlined, CloseOutlined, DownOutlined, PlusOutlined, UpOutlined } from '../../components/icons';
import {
  MODEL_GROUPS_QUERY_KEY,
  modelGroupById,
  modelGroupRefusalCode,
  moveModelGroupMember,
  reorderModelGroupInList,
  withModelGroup,
  type ModelGroup,
  type ModelGroupList,
  type ModelGroupMember,
} from '../../types/modelGroups';
import { modelGroupApi } from './api';
import { ModelGroupNameDialog } from './ModelGroupNameDialog';
import styles from './ModelGroups.module.css';

/**
 * One group's members in the operator's order.
 *
 * Reordering saves on the move and the move is optimistic: the row the operator dragged appears
 * where it will land, and a refused save puts the last stored order back under the same notice.
 * Order is the only thing this page writes optimistically because it is the only write whose
 * outcome is the click itself; add, remove and rename wait for the server and then show its answer.
 */
export const ModelGroupDetailPage: React.FC = () => {
  const t = useT();
  const toast = useToast();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { id = '' } = useParams();
  const [isRenaming, setIsRenaming] = React.useState(false);
  const [renameError, setRenameError] = React.useState<string | null>(null);
  const [failedOrder, setFailedOrder] = React.useState<string[] | null>(null);
  const [orderFailed, setOrderFailed] = React.useState(false);

  const groupsQuery = useQuery({
    queryKey: MODEL_GROUPS_QUERY_KEY,
    queryFn: modelGroupApi.list,
  });
  const group = modelGroupById(groupsQuery.data, id);

  const refresh = () => { void groupsQuery.refetch(); };

  const reorderMutation = useMutation<ModelGroup, unknown, string[], { previous?: ModelGroupList }>({
    mutationFn: (models) => modelGroupApi.reorder(id, models),
    onMutate: async (models) => {
      // A read that lands mid-move would replace the optimistic order with the stored one.
      await queryClient.cancelQueries({ queryKey: MODEL_GROUPS_QUERY_KEY });
      const previous = queryClient.getQueryData<ModelGroupList>(MODEL_GROUPS_QUERY_KEY);
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (current) => reorderModelGroupInList(current, id, models),
      );
      return { previous };
    },
    onError: (_error, models, context) => {
      if (context?.previous) queryClient.setQueryData(MODEL_GROUPS_QUERY_KEY, context.previous);
      setFailedOrder(models);
      setOrderFailed(true);
    },
    onSuccess: (saved) => {
      setOrderFailed(false);
      setFailedOrder(null);
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (current) => withModelGroup(current, saved),
      );
    },
  });

  const removeMutation = useMutation({
    mutationFn: (model: string) => modelGroupApi.removeMember(id, model),
    onSuccess: (saved, model) => {
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (current) => withModelGroup(current, saved),
      );
      toast.success(t('model_groups.removed', { model }));
    },
    onError: (error) => toast.error(t('model_groups.remove_failed'), { error }),
  });

  const renameMutation = useMutation({
    mutationFn: (name: string) => modelGroupApi.rename(id, name),
    onSuccess: (saved) => {
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (current) => withModelGroup(current, saved),
      );
      toast.success(t('model_groups.renamed', { name: saved.name }));
      setIsRenaming(false);
      setRenameError(null);
    },
    onError: (error) => {
      const code = modelGroupRefusalCode(error);
      setRenameError(code === 'model_group_name_conflict'
        ? t('model_groups.name_conflict')
        : t('model_groups.save_failed'));
    },
  });

  const move = (index: number, delta: -1 | 1) => {
    if (!group) return;
    const models = group.members.map((member) => member.model);
    const next = moveModelGroupMember(models, index, delta);
    if (next === models) return;
    reorderMutation.mutate(next);
  };

  const backToList = () => navigate('/model-groups');
  const openAdd = () => navigate(`/model-groups/${encodeURIComponent(id)}/add`);

  // The transient states keep the page's own name: a group that is gone has no name to show,
  // and a reader still needs to know which page they are on, on a phone as much as anywhere.
  if (groupsQuery.isLoading) {
    return (
      <div className="terminal-page">
        <PageHeader title={t('model_groups.title')} />
        <PageLoading />
      </div>
    );
  }
  if (groupsQuery.isError && !groupsQuery.data) {
    return (
      <div className="terminal-page">
        <PageHeader title={t('model_groups.title')} />
        <LoadFailure title={t('model_groups.load_failed')} error={groupsQuery.error} onRetry={refresh} />
      </div>
    );
  }
  if (!group) {
    return (
      <div className="terminal-page">
        <PageHeader title={t('model_groups.title')} />
        <EmptyState title={t('model_groups.not_found_title')} description={t('model_groups.not_found_description')}>
          <Button onClick={backToList}>{t('model_groups.back_to_list')}</Button>
        </EmptyState>
      </div>
    );
  }

  const actions = (
    <>
      <Button icon={<ArrowLeftOutlined />} onClick={backToList}>{t('model_groups.back_to_list')}</Button>
      <Button type="text" onClick={() => setIsRenaming(true)}>{t('model_groups.rename')}</Button>
      <RefreshButton onRefresh={refresh} isRefreshing={groupsQuery.isFetching} />
      <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>{t('model_groups.add_models')}</Button>
    </>
  );

  const columns: ColumnsType<ModelGroupMember> = [
    {
      key: 'position',
      title: t('model_groups.position'),
      width: 132,
      render: (_value, _member, index) => (
        <span className={styles['order-cell']}>
          <span className={styles['order-number']}>{index + 1}</span>
          {index === 0 && <Tag size="small" tone="accent">{t('model_groups.preferred')}</Tag>}
        </span>
      ),
    },
    {
      key: 'model',
      title: t('model_groups.model'),
      render: (_value, member) => (
        <code className={styles['model-name']} data-model-group-member={member.model}>{member.model}</code>
      ),
    },
    {
      key: 'actions',
      title: '',
      width: 156,
      render: (_value, member, index) => (
        <div className="row-actions">
          <Button
            size="small"
            className="row-action-btn"
            icon={<UpOutlined />}
            aria-label={t('model_groups.move_up_model', { model: member.model })}
            data-model-move="up"
            disabled={index === 0 || reorderMutation.isPending}
            onClick={() => move(index, -1)}
          />
          <Button
            size="small"
            className="row-action-btn"
            icon={<DownOutlined />}
            aria-label={t('model_groups.move_down_model', { model: member.model })}
            data-model-move="down"
            disabled={index === group.members.length - 1 || reorderMutation.isPending}
            onClick={() => move(index, 1)}
          />
          <Button
            size="small"
            danger
            className="row-action-btn"
            icon={<CloseOutlined />}
            aria-label={t('model_groups.remove_model', { model: member.model })}
            data-model-remove={member.model}
            disabled={removeMutation.isPending}
            onClick={() => removeMutation.mutate(member.model)}
          />
        </div>
      ),
    },
  ];

  return (
    <div className="terminal-page terminal-page-stack model-group-detail-page">
      <PageHeader
        title={group.name}
        subtitle={t('model_groups.member_count', { count: group.members.length })}
        actions={actions}
        mobileActions={<Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>{t('model_groups.add_models')}</Button>}
      >
        {/* The phone head hides the page title, and a group's name is the one thing this page is. */}
        <p className={styles['phone-title']} aria-hidden="true">{group.name}</p>
      </PageHeader>

      {orderFailed && (
        <Notice
          tone="warning"
          title={t('model_groups.order_failed')}
          description={t('model_groups.order_failed_description')}
          action={failedOrder ? (
            <Button size="small" onClick={() => reorderMutation.mutate(failedOrder)}>{t('common.retry')}</Button>
          ) : undefined}
        />
      )}

      {group.members.length === 0 ? (
        <EmptyState
          title={t('model_groups.members_empty_title')}
          description={t('model_groups.members_empty_description')}
        >
          <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>{t('model_groups.add_models')}</Button>
        </EmptyState>
      ) : (
        <ResponsiveList
          columns={columns}
          dataSource={group.members}
          rowKey={(member) => member.model}
          isLoading={groupsQuery.isFetching && group.members.length === 0}
          emptyText={t('model_groups.members_empty_title')}
          phone={{ identity: 'model', actions: ['actions'] }}
        />
      )}

      <ModelGroupNameDialog
        open={isRenaming}
        mode="rename"
        initialName={group.name}
        groups={groupsQuery.data?.groups ?? []}
        exceptId={group.id}
        isSaving={renameMutation.isPending}
        serverError={renameError}
        onDraftChange={() => setRenameError(null)}
        onCancel={() => { setIsRenaming(false); setRenameError(null); }}
        onSubmit={(name) => renameMutation.mutate(name)}
      />
    </div>
  );
};
