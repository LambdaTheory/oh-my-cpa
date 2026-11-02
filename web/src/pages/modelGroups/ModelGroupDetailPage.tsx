import React from 'react';
import clsx from 'clsx';
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
import { ArrowLeftOutlined, CloseOutlined, DownOutlined, HolderOutlined, PlusOutlined, UpOutlined } from '../../components/icons';
import {
  MODEL_GROUPS_QUERY_KEY,
  modelGroupById,
  modelGroupRefusalCode,
  moveModelGroupMember,
  reorderModelGroupInList,
  restoreModelOrder,
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
 * A move saves on the move and is optimistic: the row the operator dragged appears where it will
 * land, and a refused save puts the last stored order back under the same notice. Order is the
 * only write that is optimistic because it is the only one whose outcome is the gesture itself;
 * add, remove and rename wait for the server and then show its answer.
 *
 * The group is one write target, so its writes are serialised: while any of them is in flight the
 * others are disabled, and a refused reorder restores only the order rather than a whole group
 * snapshot. Both are load-bearing. A reorder that failed after a remove succeeded used to put the
 * removed model back on the page, and a stale snapshot can undo a rename just as easily.
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
  // Native HTML5 drag, armed by the grip: the row is draggable only after its own handle is
  // pressed, so a pointer press on a move or remove button never starts a drag.
  const [armedModel, setArmedModel] = React.useState<string | null>(null);
  const [draggingModel, setDraggingModel] = React.useState<string | null>(null);
  const [dropTargetModel, setDropTargetModel] = React.useState<string | null>(null);
  // Which edge of the target the dragged row will land on; a downward drag lands after it.
  const [isDropAfter, setIsDropAfter] = React.useState(false);
  // `isWriting` is the rendered state; this ref closes the same-tick window in which a second
  // click - a double press of Retry, most likely - still sees the previous render's `false`.
  const writeInFlight = React.useRef(false);

  const groupsQuery = useQuery({
    queryKey: MODEL_GROUPS_QUERY_KEY,
    queryFn: modelGroupApi.list,
  });
  const group = modelGroupById(groupsQuery.data, id);

  const refresh = () => { void groupsQuery.refetch(); };
  const settleList = () => { void queryClient.invalidateQueries({ queryKey: MODEL_GROUPS_QUERY_KEY }); };

  const reorderMutation = useMutation<ModelGroup, unknown, string[], { previousOrder?: string[] }>({
    mutationFn: (models) => modelGroupApi.reorder(id, models),
    onMutate: async (models) => {
      // A read that lands mid-move would replace the optimistic order with the stored one.
      await queryClient.cancelQueries({ queryKey: MODEL_GROUPS_QUERY_KEY });
      const current = queryClient.getQueryData<ModelGroupList>(MODEL_GROUPS_QUERY_KEY);
      const previousOrder = current?.groups.find((candidate) => candidate.id === id)?.members.map((member) => member.model);
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (cache) => reorderModelGroupInList(cache, id, models),
      );
      return { previousOrder };
    },
    onError: (_error, models, context) => {
      if (context?.previousOrder) {
        queryClient.setQueryData<ModelGroupList>(
          MODEL_GROUPS_QUERY_KEY,
          (cache) => restoreModelOrder(cache, id, context.previousOrder ?? []),
        );
      }
      setFailedOrder(models);
      setOrderFailed(true);
    },
    onSuccess: (saved) => {
      setOrderFailed(false);
      setFailedOrder(null);
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (cache) => withModelGroup(cache, saved),
      );
      settleList();
    },
    onSettled: () => { writeInFlight.current = false; },
  });

  const removeMutation = useMutation({
    mutationFn: (model: string) => modelGroupApi.removeMember(id, model),
    onSuccess: (saved, model) => {
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (cache) => withModelGroup(cache, saved),
      );
      settleList();
      toast.success(t('model_groups.removed', { model }));
    },
    onError: (error) => toast.error(t('model_groups.remove_failed'), { error }),
    onSettled: () => { writeInFlight.current = false; },
  });

  const renameMutation = useMutation({
    mutationFn: (name: string) => modelGroupApi.rename(id, name),
    onSuccess: (saved) => {
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (cache) => withModelGroup(cache, saved),
      );
      settleList();
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
    onSettled: () => { writeInFlight.current = false; },
  });

  /** The group is one write target: until the current write settles, no other one starts. */
  const isWriting = reorderMutation.isPending || removeMutation.isPending || renameMutation.isPending;

  /** Claims the group's one write slot; a same-tick second caller is refused. */
  const beginWrite = () => {
    if (writeInFlight.current || isWriting) return false;
    writeInFlight.current = true;
    return true;
  };

  const saveOrder = (models: string[]) => {
    // Retry is a second entry point to the same write; the guard also covers a held response
    // whose button has not re-rendered as disabled yet.
    if (!beginWrite()) return;
    reorderMutation.mutate(models);
  };

  const move = (index: number, delta: -1 | 1) => {
    if (!group) return;
    const models = group.members.map((member) => member.model);
    const next = moveModelGroupMember(models, index, delta);
    if (next === models) return;
    saveOrder(next);
  };

  /** Drops the dragged member into the target member's slot; the rest keep their relative order. */
  const dropOn = (dragged: string, target: string) => {
    if (!group || dragged === target) return;
    const models = group.members.map((member) => member.model);
    const from = models.indexOf(dragged);
    const to = models.indexOf(target);
    if (from < 0 || to < 0) return;
    const next = [...models];
    next.splice(from, 1);
    next.splice(to, 0, dragged);
    saveOrder(next);
  };

  const clearDrag = () => {
    setArmedModel(null);
    setDraggingModel(null);
    setDropTargetModel(null);
    setIsDropAfter(false);
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
      <Button type="text" disabled={isWriting} onClick={() => setIsRenaming(true)}>{t('model_groups.rename')}</Button>
      <RefreshButton onRefresh={refresh} isRefreshing={groupsQuery.isFetching} />
      <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>{t('model_groups.add_models')}</Button>
    </>
  );

  const columns: ColumnsType<ModelGroupMember> = [
    {
      key: 'position',
      title: t('model_groups.position'),
      width: 148,
      render: (_value, member, index) => (
        <span className={styles['order-cell']}>
          <span
            className={styles['drag-handle']}
            data-drag-handle={member.model}
            role="img"
            aria-label={t('model_groups.drag_to_reorder')}
            onPointerDown={() => { if (!isWriting) setArmedModel(member.model); }}
            onPointerUp={() => { if (!draggingModel) setArmedModel(null); }}
          >
            <HolderOutlined />
          </span>
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
            disabled={index === 0 || isWriting}
            onClick={() => move(index, -1)}
          />
          <Button
            size="small"
            className="row-action-btn"
            icon={<DownOutlined />}
            aria-label={t('model_groups.move_down_model', { model: member.model })}
            data-model-move="down"
            disabled={index === group.members.length - 1 || isWriting}
            onClick={() => move(index, 1)}
          />
          <Button
            size="small"
            danger
            className="row-action-btn"
            icon={<CloseOutlined />}
            aria-label={t('model_groups.remove_model', { model: member.model })}
            data-model-remove={member.model}
            disabled={isWriting}
            onClick={() => {
              if (!beginWrite()) return;
              removeMutation.mutate(member.model);
            }}
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
            <Button size="small" disabled={isWriting} onClick={() => saveOrder(failedOrder)}>{t('common.retry')}</Button>
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
          tableProps={{
            rowClassName: (member) => clsx(
              draggingModel === member.model && styles['is-dragging'],
              dropTargetModel === member.model && (isDropAfter ? styles['is-drop-after'] : styles['is-drop-before']),
            ),
            onRow: (member) => ({
              draggable: !isWriting && armedModel === member.model,
              onDragStart: (event) => {
                if (isWriting || armedModel !== member.model) return;
                event.dataTransfer.effectAllowed = 'move';
                event.dataTransfer.setData('text/plain', member.model);
                setDraggingModel(member.model);
              },
              onDragOver: (event) => {
                if (!draggingModel || draggingModel === member.model || isWriting) return;
                event.preventDefault();
                event.dataTransfer.dropEffect = 'move';
                const from = group.members.findIndex((candidate) => candidate.model === draggingModel);
                const to = group.members.findIndex((candidate) => candidate.model === member.model);
                setDropTargetModel(member.model);
                setIsDropAfter(from < to);
              },
              onDragLeave: (event) => {
                if (!event.currentTarget.contains(event.relatedTarget as Node | null)) {
                  setDropTargetModel((current) => (current === member.model ? null : current));
                }
              },
              onDrop: (event) => {
                event.preventDefault();
                if (!draggingModel || isWriting) return;
                dropOn(draggingModel, member.model);
                clearDrag();
              },
              onDragEnd: clearDrag,
            }),
          }}
        />
      )}

      <ModelGroupNameDialog
        open={isRenaming}
        mode="rename"
        initialName={group.name}
        groups={groupsQuery.data?.groups ?? []}
        exceptId={group.id}
        isSaving={isWriting}
        serverError={renameError}
        onDraftChange={() => setRenameError(null)}
        onCancel={() => { setIsRenaming(false); setRenameError(null); }}
        onSubmit={(name) => {
          if (!beginWrite()) return;
          renameMutation.mutate(name);
        }}
      />
    </div>
  );
};
