import React from 'react';
import { Button } from 'antd';
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import type { DashboardPredictionsResponse, QuotaPredictionItem, QuotaPredictionVerdict } from '../../types/quotaPrediction';
import { formatCost, formatTokensFull } from '../../types/tokenDisplay';
import dayjs from '../../utils/time';
import { ConfirmAction } from '../common/ConfirmAction';
import { EmptyState } from '../common/EmptyState';
import { Meter } from '../common/Meter';
import { Panel } from '../common/Panel';
import { ParagraphPlaceholder } from '../common/ContentPlaceholder';
import { SectionHeading } from '../common/SectionHeading';
import { credentialProviderIconId } from '../common/providerMetadata';
import { ProviderBrandIcon } from '../LobeIcon';
import { RefreshButton } from '../common/RefreshButton';
import { Tag, type TagTone } from '../common/Tag';
import { LoadFailure, Notice, useToast } from '../feedback';
import { SendOutlined } from '../icons';
import styles from './QuotaPrediction.module.css';

export const QUOTA_PREDICTION_QUERY_KEY = 'dashboard-quota-predictions';
export const QUOTA_PREDICTION_REFETCH_INTERVAL_MS = 5 * 60 * 1000;
export const QUOTA_PREDICTION_REFRESH_OPTIONS = Object.freeze({
  refetchInterval: QUOTA_PREDICTION_REFETCH_INTERVAL_MS,
  refetchIntervalInBackground: false,
  refetchOnWindowFocus: true,
});

const PROVIDER_ORDER = ['antigravity', 'claude', 'codex', 'ollama'];
const VERDICT_TONES: Record<QuotaPredictionVerdict, TagTone> = {
  will_exhaust: 'danger',
  under_used: 'warn',
  on_track: 'success',
  unknown: 'neutral',
};

export interface QuotaPredictionPanelProps {
  data?: DashboardPredictionsResponse;
  isLoading?: boolean;
  isFetching?: boolean;
  isError?: boolean;
  error?: unknown;
  isSending?: boolean;
  isDemo?: boolean;
  onRefresh?: () => void;
  onSend?: () => void;
}

function formatPercent(value: number | null): string {
  return value === null ? '—' : `${value.toFixed(1)}%`;
}

function formatReset(value: number | null): string {
  return value === null ? '—' : dayjs(value).format('YYYY-MM-DD HH:mm');
}

function formatAge(seconds: number | null, t: ReturnType<typeof useT>): string {
  if (seconds === null) return t('dash.prediction.unknown');
  if (seconds < 60) return t('dash.prediction.age_seconds', { n: seconds });
  if (seconds < 3600) return t('dash.prediction.age_minutes', { n: Math.floor(seconds / 60) });
  if (seconds < 86400) return t('dash.prediction.age_hours', { n: Math.floor(seconds / 3600) });
  return t('dash.prediction.age_days', { n: Math.floor(seconds / 86400) });
}

function formatProjection(item: QuotaPredictionItem, t: ReturnType<typeof useT>): string {
  const parts: string[] = [];
  if (item.projected_tokens_at_reset !== null) {
    parts.push(t('dash.prediction.tokens_value', { n: formatTokensFull(item.projected_tokens_at_reset) }));
  }
  if (item.projected_cost_nanos_at_reset !== null) {
    parts.push(formatCost(item.projected_cost_nanos_at_reset / 1_000_000_000));
  }
  return parts.length > 0 ? parts.join(' · ') : '—';
}

function providerLabel(provider: string, t: ReturnType<typeof useT>): string {
  const key = `dash.prediction.provider.${provider}`;
  const label = t(key);
  return label === key ? provider : label;
}

function translatedValue(prefix: string, value: string, t: ReturnType<typeof useT>): string {
  const key = `${prefix}.${value}`;
  const label = t(key);
  return label === key ? value : label;
}

const PredictionRow: React.FC<{ item: QuotaPredictionItem }> = ({ item }) => {
  const t = useT();
  const isStale = item.reasons.includes('stale_reading');
  const credentialLabel = item.credential_label || item.auth_index || t('dash.prediction.credential_unknown');
  const windowLabel = translatedValue('dash.prediction.window', item.window_kind, t);
  const windowDetail = item.window_model || item.window_id;
  return (
    <li className={styles['row']} data-verdict={item.verdict}>
      <div className={styles['identity']}>
        <strong title={credentialLabel}>{credentialLabel}</strong>
        <span>{item.window_kind === 'model_scoped' ? `${windowLabel} · ${windowDetail}` : windowLabel}</span>
      </div>
      <div className={styles['verdict']}>
        <Tag size="small" tone={VERDICT_TONES[item.verdict]}>{t(`dash.prediction.verdict.${item.verdict}`)}</Tag>
        {isStale && <Tag size="small" tone="warn">{t('dash.prediction.stale')}</Tag>}
      </div>
      <div className={styles['meter-cell']}>
        <span>{t('dash.prediction.used')} <b>{formatPercent(item.used_percent)}</b></span>
        {item.used_percent !== null && <Meter value={item.used_percent} label={t('dash.prediction.used_meter', { name: credentialLabel })} />}
      </div>
      <div className={styles['meter-cell']}>
        <span>{t('dash.prediction.projected')} <b>{formatPercent(item.projected_percent_at_reset)}</b></span>
        {item.projected_percent_at_reset !== null && <Meter value={item.projected_percent_at_reset} label={t('dash.prediction.projected_meter', { name: credentialLabel })} />}
      </div>
      <dl className={styles['facts']}>
        <div><dt>{t('dash.prediction.reset')}</dt><dd title={formatReset(item.reset_at_ms)}>{formatReset(item.reset_at_ms)}</dd></div>
        <div><dt>{t('dash.prediction.projection')}</dt><dd title={formatProjection(item, t)}>{formatProjection(item, t)}</dd></div>
        {item.verdict === 'will_exhaust' && item.exhaustion_eta_ms !== null && (
          <div><dt>{t('dash.prediction.exhaustion')}</dt><dd title={formatReset(item.exhaustion_eta_ms)}>{formatReset(item.exhaustion_eta_ms)}</dd></div>
        )}
        <div><dt>{t('dash.prediction.data_age')}</dt><dd>{formatAge(item.data_age_seconds, t)}</dd></div>
      </dl>
      <div className={styles['reasons']}>
        <span>{t('dash.prediction.reasons')}</span>
        {item.reasons.length > 0
          ? item.reasons.map((reason) => <Tag key={reason} size="small">{translatedValue('dash.prediction.reason', reason, t)}</Tag>)
          : <span>—</span>}
      </div>
    </li>
  );
};

export const QuotaPredictionPanel: React.FC<QuotaPredictionPanelProps> = ({
  data,
  isLoading = false,
  isFetching = false,
  isError = false,
  error,
  isSending = false,
  isDemo = false,
  onRefresh,
  onSend,
}) => {
  const t = useT();
  const groups = React.useMemo(() => [...(data?.providers ?? [])]
    .filter((group) => group.items.length > 0)
    .sort((left, right) => {
      const leftIndex = PROVIDER_ORDER.indexOf(left.provider);
      const rightIndex = PROVIDER_ORDER.indexOf(right.provider);
      return (leftIndex < 0 ? PROVIDER_ORDER.length : leftIndex) - (rightIndex < 0 ? PROVIDER_ORDER.length : rightIndex)
        || left.provider.localeCompare(right.provider);
    }), [data?.providers]);

  return (
    <Panel className={styles['panel']} data-testid="dashboard-quota-prediction">
      <div className={styles['header']}>
        <SectionHeading as="h2" title={t('dash.prediction.title')} description={t('dash.prediction.description')} />
        <div className={styles['actions']}>
          {data && <span className={styles['duration']}>{t('dash.prediction.duration', { n: data.duration_ms })}</span>}
          {onSend && (
            <ConfirmAction
              title={t('dash.prediction.send_confirm_title')}
              description={t('dash.prediction.send_confirm_desc')}
              confirmLabel={t('dash.prediction.send_confirm')}
              isConfirming={isSending}
              onConfirm={onSend}
            >
              <Button icon={<SendOutlined />} disabled={isDemo || isSending || isLoading || isError || groups.length === 0}>{t('dash.prediction.send')}</Button>
            </ConfirmAction>
          )}
          {onRefresh && <RefreshButton isIconOnly label={t('dash.prediction.refresh')} isRefreshing={isFetching} disabled={isFetching} onRefresh={onRefresh} />}
        </div>
      </div>

      {isError && data && <Notice tone="warning" title={t('dash.prediction.stale_data')} description={error instanceof Error ? error.message : undefined} />}
      {isError && !data ? (
        <LoadFailure title={t('dash.prediction.error')} error={error} onRetry={onRefresh} />
      ) : isLoading && !data ? (
        <ParagraphPlaceholder rows={4} />
      ) : groups.length === 0 ? (
        <EmptyState size="compact" title={t('dash.prediction.empty')} description={t('dash.prediction.empty_desc')} />
      ) : (
        <div className={styles['groups']}>
          {groups.map((group) => (
            <section key={group.provider} className={styles['group']} aria-label={providerLabel(group.provider, t)}>
              <div className={styles['group-head']}>
                <ProviderBrandIcon iconId={credentialProviderIconId(group.provider)} size={18} />
                <SectionHeading as="h3" level="label" title={providerLabel(group.provider, t)} count={group.items.length} />
              </div>
              <ul>{group.items.map((item) => <PredictionRow key={JSON.stringify([item.auth_index ?? item.credential_label, item.window_id, item.window_scope, item.window_model])} item={item} />)}</ul>
            </section>
          ))}
          {data && data.omitted > 0 && <Notice tone="info" title={t('dash.prediction.omitted', { n: data.omitted })} />}
        </div>
      )}
    </Panel>
  );
};

export const QuotaPrediction: React.FC = () => {
  const t = useT();
  const toast = useToast();
  const query = useQuery({
    queryKey: [QUOTA_PREDICTION_QUERY_KEY],
    queryFn: api.getDashboardPredictions,
    ...QUOTA_PREDICTION_REFRESH_OPTIONS,
    placeholderData: keepPreviousData,
    meta: { silent: true },
  });
  const send = useMutation({
    mutationFn: api.postFeishuPredictionTest,
    onSuccess: (result) => {
      if (result.sent) {
        toast.success(t('dash.prediction.send_success'));
      } else if (result.reason === 'no_findings') {
        toast.info(t('dash.prediction.no_findings'));
      } else {
        toast.error(t('dash.prediction.send_failed'), { detail: result.error });
      }
    },
    onError: (error) => toast.error(t('dash.prediction.send_failed'), { error }),
  });
  return (
    <QuotaPredictionPanel
      data={query.data}
      isLoading={query.isLoading}
      isFetching={query.isFetching}
      isError={query.isError}
      error={query.error}
      isSending={send.isPending}
      isDemo={isDemoMode()}
      onRefresh={() => void query.refetch({ cancelRefetch: false })}
      onSend={() => send.mutate()}
    />
  );
};
