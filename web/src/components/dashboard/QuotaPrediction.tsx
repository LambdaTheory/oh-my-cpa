import React from 'react';
import { Button } from 'antd';
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import type { DashboardPredictionsResponse, DashboardPredictionItem } from '../../types/quotaPrediction';
import { ConfirmAction } from '../common/ConfirmAction';
import { EmptyState } from '../common/EmptyState';
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
import {
  formatPredictionDeviation,
  formatPredictionETA,
  formatPredictionPercent,
  isExhausted,
  isStaleReading,
  predictionConclusion,
  predictionLasts,
  predictionUnavailableKey,
  sortPredictions,
  summarizePredictions,
  summarizeUnpushed,
  type PredictionLasts,
} from './quotaPredictionLogic';

export const QUOTA_PREDICTION_QUERY_KEY = 'dashboard-quota-predictions';
export const QUOTA_PREDICTION_REFETCH_INTERVAL_MS = 5 * 60 * 1000;
export const QUOTA_PREDICTION_REFRESH_OPTIONS = Object.freeze({
  refetchInterval: QUOTA_PREDICTION_REFETCH_INTERVAL_MS,
  refetchIntervalInBackground: false,
  refetchOnWindowFocus: true,
});

const PROVIDER_ORDER = ['antigravity', 'claude', 'codex', 'ollama', 'step'];

const LASTS_TONES: Record<PredictionLasts, TagTone> = { yes: 'success', no: 'danger', unknown: 'neutral' };

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

function formatReset(value: number | null | undefined, t: ReturnType<typeof useT>): string {
  if (value == null || value <= 0) return t('dash.prediction.unknown_short');
  return new Date(value).toLocaleString();
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

const PredictionRow: React.FC<{ item: DashboardPredictionItem; generatedAtMS: number }> = ({ item, generatedAtMS }) => {
  const t = useT();
  const credentialLabel = item.credential_label || item.auth_index || t('dash.prediction.credential_unknown');
  const windowLabel = translatedValue('dash.prediction.window', item.window_kind, t);
  const windowDetail = item.window_model || item.window_id;
  const lasts = predictionLasts(item);
  const stale = isStaleReading(item);
  const conclusion = predictionConclusion(item, generatedAtMS);
  const reasons = item.reasons.map((reason) => translatedValue('dash.prediction.reason', reason, t));
  const diagnostics = [item.verdict, item.evidence, ...reasons].filter(Boolean).join(' · ');
  return (
    <li className={styles['row']} data-lasts={lasts} data-exhausted={isExhausted(item) ? 'true' : undefined}>
      <div className={styles['identity']}>
        <strong title={credentialLabel}>{credentialLabel}</strong>
        <span>{item.window_kind === 'model_scoped' ? windowLabel + ' · ' + windowDetail : windowLabel}</span>
      </div>
      <div className={styles['verdict']}>
        <Tag size="small" tone={LASTS_TONES[lasts]}>{t(`dash.prediction.lasts.${lasts}`)}</Tag>
        {stale && <Tag size="small">{t('dash.prediction.expired')}</Tag>}
      </div>
      <div className={styles['combined-reading']}>
        <div><span>{t('dash.prediction.actual')}</span><b>{formatPredictionPercent(item.used_percent, t)}</b></div>
        <div><span>{t('dash.prediction.expected')}</span><b>{formatPredictionPercent(item.expected_used_percent, t)}</b></div>
        <div className={styles['deviation']}><span>{t('dash.prediction.deviation')}</span><b>{formatPredictionDeviation(item, t)}</b></div>
      </div>
      <p className={styles['context']}>
        {t('dash.prediction.eta_label')} <span>{formatPredictionETA(item, t)}</span>
        {' · '}
        {t('dash.prediction.reset')} <span title={formatReset(item.reset_at_ms, t)}>{formatReset(item.reset_at_ms, t)}</span>
      </p>
      <p className={styles['advice']}>
        {t(conclusion.key, conclusion.vars)}
        {lasts === 'no' && !stale && !isExhausted(item) && ` ${t('dash.prediction.advice_backup')}`}
      </p>
      <details className={styles['diagnostics']}>
        <summary>{t('dash.prediction.diagnostics')}</summary>
        <p>{diagnostics || t('dash.prediction.unknown_short')}</p>
        {item.unavailable_reason && <p>{t(predictionUnavailableKey(item.unavailable_reason))}</p>}
        {(item.sample_count != null || item.coverage_seconds != null) && (
          <p>
            {item.sample_count != null && t('dash.prediction.sample', { n: item.sample_count })}
            {item.sample_count != null && item.coverage_seconds != null && ' · '}
            {item.coverage_seconds != null && t('dash.prediction.coverage', { n: Math.round((item.coverage_seconds / 3600) * 10) / 10 })}
          </p>
        )}
      </details>
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

  const generatedAtMS = data?.generated_at_ms ?? 0;
  const allItems = React.useMemo(() => groups.flatMap((group) => group.items), [groups]);
  const summary = summarizePredictions(allItems);
  const unpushed = summarizeUnpushed(allItems);

  return (
    <Panel className={styles['panel']} data-testid="dashboard-quota-prediction">
      <div className={styles['header']}>
        <SectionHeading as="h2" title={t('dash.prediction.combined_title')} description={t('dash.prediction.combined_description')} />
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

      {data && groups.length > 0 && (
        <p className={styles['summary-count']} data-testid="prediction-summary">
          {t('dash.prediction.summary_window_counts', { cannot: summary.cannot, unknown: summary.unknown })}
          {data.omitted > 0 ? ` · ${t('dash.prediction.summary_omitted', { n: data.omitted })}` : ''}
        </p>
      )}
      {isError && data && <Notice tone="warning" title={t('dash.prediction.stale_data')} description={error instanceof Error ? error.message : undefined} />}
      {isError && !data ? (
        <LoadFailure title={t('dash.prediction.error')} error={error} onRetry={onRefresh} />
      ) : isLoading && !data ? (
        <ParagraphPlaceholder rows={4} />
      ) : groups.length === 0 ? (
        <EmptyState size="compact" title={t('dash.prediction.empty')} description={t('dash.prediction.empty_desc')} />
      ) : (
        <div className={styles['groups']}>
          {groups.map((group) => {
            const groupSummary = summarizePredictions(group.items);
            const sorted = sortPredictions(group.items, generatedAtMS);
            return (
              <section key={group.provider} className={styles['group']} aria-label={providerLabel(group.provider, t)}>
                <details>
                  <summary className={styles['group-head']}>
                    <ProviderBrandIcon iconId={credentialProviderIconId(group.provider === 'step' ? 'stepfun' : group.provider)} size={18} />
                    <strong>{providerLabel(group.provider, t)}</strong>
                    <span>{t('dash.prediction.group_summary', { credentials: group.items.length, cannot: groupSummary.cannot })}</span>
                    <Tag size="small" tone={groupSummary.cannot > 0 ? 'danger' : groupSummary.unknown > 0 ? 'neutral' : 'success'}>
                      {groupSummary.cannot > 0
                        ? t('dash.prediction.lasts.no')
                        : groupSummary.unknown > 0 ? t('dash.prediction.lasts.unknown') : t('dash.prediction.lasts.yes')}
                    </Tag>
                  </summary>
                  <ul>{sorted.map((item) => (
                    <PredictionRow
                      key={JSON.stringify([item.auth_index ?? item.credential_label, item.window_id, item.window_scope, item.window_model])}
                      item={item}
                      generatedAtMS={generatedAtMS}
                    />
                  ))}</ul>
                </details>
              </section>
            );
          })}
          {data && data.omitted > 0 && <Notice tone="info" title={t('dash.prediction.omitted', { n: data.omitted })} />}
        </div>
      )}
      {data && !isError && groups.length > 0 && (
        <p className={styles['footer']}>{t('dash.prediction.unpushed', unpushed)}</p>
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
