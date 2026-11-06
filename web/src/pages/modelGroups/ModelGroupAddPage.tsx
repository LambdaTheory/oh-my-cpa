import React from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Button, Checkbox } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useNavigate, useParams } from 'react-router-dom';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import { PageHeader } from '../../components/common/PageHeader';
import { PageLoading } from '../../components/common/PageLoading';
import { EmptyState } from '../../components/common/EmptyState';
import { ResponsiveList } from '../../components/common/ResponsiveList';
import { SearchInput } from '../../components/common/SearchInput';
import { Toolbar } from '../../components/common/Toolbar';
import { LoadFailure, useToast } from '../../components/feedback';
import { ArrowLeftOutlined } from '../../components/icons';
import { modelGroupApi } from './api';
import {
  buildModelSquareEntries,
  type ModelSquareEntry,
} from '../../types/modelSquare';
import {
  MODEL_GROUP_READINGS_QUERY_KEY,
  MODEL_GROUPS_QUERY_KEY,
  modelGroupById,
  modelGroupReading,
  withModelGroup,
  type ModelGroupList,
} from '../../types/modelGroups';
import { ModelGroupAvailabilityTag } from './ModelGroupAvailabilityTag';
import styles from './ModelGroups.module.css';

/**
 * The models a group may gain, chosen from what the gateway already serves.
 *
 * Candidates come from the model directory, not from the model square page's own list: an alias
 * is a call name like any other, and it is exactly what a group needs to hold. Models already in
 * this group are left out, and no candidate says which other groups hold it - membership is not
 * what this step is choosing between, and the PRD deliberately omits it.
 */
export const ModelGroupAddPage: React.FC = () => {
  const t = useT();
  const toast = useToast();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { id = '' } = useParams();
  const [search, setSearch] = React.useState('');
  const [selected, setSelected] = React.useState<string[]>([]);

  const groupsQuery = useQuery({
    queryKey: MODEL_GROUPS_QUERY_KEY,
    queryFn: modelGroupApi.list,
  });
  const directoryQuery = useQuery({
    queryKey: ['model-square'],
    queryFn: api.getModelSquare,
    staleTime: 30_000,
  });
  const readingsQuery = useQuery({
    queryKey: MODEL_GROUP_READINGS_QUERY_KEY,
    queryFn: modelGroupApi.readings,
  });
  const readings = readingsQuery.data;
  const group = modelGroupById(groupsQuery.data, id);

  const memberKey = group?.members.map((member) => member.model).join('\u0000') ?? '';
  const candidates = React.useMemo(() => {
    if (!directoryQuery.data) return [];
    const taken = new Set(memberKey === '' ? [] : memberKey.split('\u0000'));
    return buildModelSquareEntries(directoryQuery.data, search).filter((entry) => !taken.has(entry.identity));
  }, [directoryQuery.data, search, memberKey]);

  const toggle = (identity: string) => {
    setSelected((current) => current.includes(identity)
      ? current.filter((model) => model !== identity)
      : [...current, identity]);
  };

  const addMutation = useMutation({
    mutationFn: (models: string[]) => modelGroupApi.addMembers(id, models),
    onSuccess: (result) => {
      queryClient.setQueryData<ModelGroupList>(
        MODEL_GROUPS_QUERY_KEY,
        (current) => withModelGroup(current, result.group),
      );
      void queryClient.invalidateQueries({ queryKey: MODEL_GROUPS_QUERY_KEY });
      if (result.added.length > 0) {
        toast.success(t('model_groups.add_success', { added: result.added.length }));
      }
      if (result.skipped.length > 0) {
        toast.info(t('model_groups.add_skipped', { skipped: result.skipped.length }));
      }
      navigate(`/model-groups/${encodeURIComponent(id)}`, { replace: true });
    },
    onError: (error) => toast.error(t('model_groups.add_failed'), { error }),
  });

  const back = () => navigate(`/model-groups/${encodeURIComponent(id)}`);

  if (groupsQuery.isLoading) {
    return (
      <div className="terminal-page">
        <PageHeader title={t('model_groups.add_models')} />
        <PageLoading />
      </div>
    );
  }
  if (groupsQuery.isError && !groupsQuery.data) {
    return (
      <div className="terminal-page">
        <PageHeader title={t('model_groups.add_models')} />
        <LoadFailure
          title={t('model_groups.load_failed')}
          error={groupsQuery.error}
          onRetry={() => { void groupsQuery.refetch(); }}
        />
      </div>
    );
  }
  if (!group) {
    return (
      <div className="terminal-page">
        <PageHeader title={t('model_groups.add_models')} />
        <EmptyState title={t('model_groups.not_found_title')} description={t('model_groups.not_found_description')}>
          <Button onClick={() => navigate('/model-groups')}>{t('model_groups.back_to_list')}</Button>
        </EmptyState>
      </div>
    );
  }

  const title = t('model_groups.add_title', { name: group.name });
  const chosen = (entry: ModelSquareEntry) => selected.includes(entry.identity);
  const makerLabel = (entry: ModelSquareEntry): string => {
    if (entry.manufacturer.id === 'multiple') return t('models.multiple_makers');
    if (entry.manufacturer.id === 'unknown') return t('models.unknown_maker');
    return entry.manufacturer.name || entry.manufacturer.id;
  };

  const columns: ColumnsType<ModelSquareEntry> = [
    {
      key: 'select',
      title: t('model_groups.select'),
      width: 64,
      render: (_value, entry) => (
        <Checkbox
          checked={chosen(entry)}
          aria-label={entry.identity}
          data-select-model={entry.identity}
          onChange={() => toggle(entry.identity)}
        />
      ),
    },
    {
      key: 'model',
      title: t('model_groups.model'),
      render: (_value, entry) => {
        const maker = makerLabel(entry);
        const isMultipleRoutes = entry.routes.length > 1;
        return (
          <span className={styles['candidate']}>
            <code className={styles['model-name']}>{entry.identity}</code>
            {(maker !== '' || isMultipleRoutes) && (
              <span className={styles['candidate-caption']}>
                {[maker, isMultipleRoutes ? t('model_groups.alias_multiple') : ''].filter(Boolean).join(' · ')}
              </span>
            )}
          </span>
        );
      },
    },
    {
      key: 'availability',
      title: t('model_groups.availability'),
      width: 132,
      render: (_value, entry) => (
        <ModelGroupAvailabilityTag
          availability={readings ? modelGroupReading(readings, entry.identity).availability : undefined}
        />
      ),
    },
  ];

  const empty = search.trim() !== ''
    ? t('model_groups.candidates_empty')
    : (directoryQuery.data?.models.length ?? 0) > 0
      ? t('model_groups.all_added')
      : t('model_groups.no_candidates');

  return (
    <div className="terminal-page terminal-page-stack model-group-add-page">
      <PageHeader
        title={title}
        subtitle={t('model_groups.candidate_hint')}
        actions={<Button icon={<ArrowLeftOutlined />} onClick={back}>{t('model_groups.back_to_group')}</Button>}
      >
        <p className={styles['phone-title']} aria-hidden="true">{title}</p>
      </PageHeader>

      <Toolbar>
        <SearchInput
          value={search}
          onChange={setSearch}
          label={t('model_groups.search')}
          className={styles['candidate-search']}
        />
      </Toolbar>

      {directoryQuery.isLoading ? (
        <PageLoading variant="block" />
      ) : directoryQuery.isError && !directoryQuery.data ? (
        <LoadFailure
          title={t('model_groups.candidates_load_failed')}
          error={directoryQuery.error}
          onRetry={() => { void directoryQuery.refetch(); }}
        />
      ) : candidates.length === 0 ? (
        <EmptyState size="compact" title={empty} />
      ) : (
        <ResponsiveList
          columns={columns}
          dataSource={candidates}
          rowKey={(entry) => entry.identity}
          isLoading={false}
          emptyText={empty}
          phone={{ identity: 'model', actions: ['select'] }}
          pageSize={50}
        />
      )}

      <div className={styles['add-footer']}>
        <span className={styles['add-footer-count']}>{t('model_groups.selected_count', { count: selected.length })}</span>
        <Button onClick={back}>{t('common.cancel')}</Button>
        <Button
          type="primary"
          disabled={selected.length === 0}
          loading={addMutation.isPending}
          onClick={() => addMutation.mutate(selected)}
        >
          {t('model_groups.add_selected', { count: selected.length })}
        </Button>
      </div>
    </div>
  );
};
