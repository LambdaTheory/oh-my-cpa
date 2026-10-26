import React from 'react';
import { Button, Tooltip } from 'antd';
import { keepPreviousData, useMutation, useQuery } from '@tanstack/react-query';
import { api } from '../../api/client';
import { useT } from '../../i18n';
import { isDemoMode } from '../../types/demoMode';
import type { DashboardPredictionsResponse, DashboardPredictionItem } from '../../types/quotaPrediction';
import { formatTokensFull } from '../../types/tokenDisplay';
import dayjs from '../../utils/time';
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
import { assessPrediction, classifyPredictionFailure, summarizePredictions, type PredictionSufficiency } from './quotaPredictionLogic';

export const QUOTA_PREDICTION_QUERY_KEY = 'dashboard-quota-predictions';
export const QUOTA_PREDICTION_REFETCH_INTERVAL_MS = 5 * 60 * 1000;
export const QUOTA_PREDICTION_REFRESH_OPTIONS = Object.freeze({
  refetchInterval: QUOTA_PREDICTION_REFETCH_INTERVAL_MS,
  refetchIntervalInBackground: false,
  refetchOnWindowFocus: true,
});

const PROVIDER_ORDER = ['antigravity', 'claude', 'codex', 'ollama', 'step'];

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

function formatPercent(value: number | null | undefined, t: ReturnType<typeof useT>): string {
  if (value == null || !Number.isFinite(value)) return t('dash.prediction.missing');
  return value > 0 && value < 0.1 ? '<0.1%' : `${value.toFixed(1)}%`;
}

function formatReset(value: number | null | undefined, t: ReturnType<typeof useT>): string {
  return value == null || value <= 0 ? t('dash.prediction.missing') : dayjs(value).format('YYYY-MM-DD HH:mm');
}

function formatEndurance(item: DashboardPredictionItem, generatedAtMS: number, t: ReturnType<typeof useT>): string {
  const assessment = assessPrediction(item, generatedAtMS);
  if (assessment.enduranceMS !== null) {
    if (assessment.enduranceMS <= 0) return t('dash.prediction.exhausted');
    if (assessment.enduranceMS < 60_000) return t('dash.prediction.less_minute');
    if (assessment.enduranceMS < 3_600_000) return t('dash.prediction.duration_minutes', { n: Math.ceil(assessment.enduranceMS / 60_000) });
    return t('dash.prediction.duration_hours', { n: (assessment.enduranceMS / 3_600_000).toFixed(1) });
  }
  if (assessment.hasForecast && item.verdict !== 'will_exhaust') return t('dash.prediction.through_reset');
  return t('dash.prediction.missing');
}

function formatRemaining(item: DashboardPredictionItem, generatedAtMS: number, t: ReturnType<typeof useT>): string {
  const remaining = assessPrediction(item, generatedAtMS).remainingPercent;
  if (remaining === null) return t('dash.prediction.missing');
  if (remaining === 0) return t('dash.prediction.exhausted');
  return formatPercent(remaining, t);
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

const SUFFICIENCY_TONES: Record<PredictionSufficiency, TagTone> = { enough: 'success', insufficient: 'danger', unknown: 'neutral' };

const PredictionRow: React.FC<{ item: DashboardPredictionItem; generatedAtMS: number; hasReadError: boolean }> = ({ item, generatedAtMS, hasReadError }) => {
  const t = useT();
  const credentialLabel = item.credential_label || item.auth_index || t('dash.prediction.credential_unknown');
  const windowLabel = translatedValue('dash.prediction.window', item.window_kind, t);
  const windowDetail = item.window_model || item.window_id;
  const assessment = assessPrediction(item, generatedAtMS);
  const failure = hasReadError ? 'reading_failed' : classifyPredictionFailure(item, generatedAtMS);
  const sufficiency = hasReadError ? 'unknown' : assessment.untilReset;
  const details = item.reasons.map((reason) => translatedValue('dash.prediction.reason', reason, t));
  if (item.capacity_unavailable) details.push(translatedValue('dash.prediction.reason', item.capacity_unavailable, t));
  const failureDetail = details.join(' · ') || t('dash.prediction.missing');
  const advice = hasReadError || sufficiency === 'unknown' ? t('dash.prediction.advice_check')
    : assessment.enduranceMS === 0 ? t('dash.prediction.advice_exhausted')
    : assessment.isLow ? t('dash.prediction.advice_low')
      : assessment.isUrgent ? t('dash.prediction.advice_urgent', { n: Math.max(1, Math.ceil((assessment.enduranceMS ?? 0) / 3_600_000)) })
        : sufficiency === 'insufficient' ? t('dash.prediction.advice_later') : t('dash.prediction.advice_none');
  const hasTokenEstimate = assessment.remainingPercent !== null && item.remaining_tokens != null
    && item.remaining_tokens > 0 && item.capacity_error_percent != null;
  const hasWindowEstimate = assessment.remainingPercent !== null && item.remaining_window_hours != null
    && item.remaining_window_hours > 0 && item.window_error_hours != null;
  return (
    <li className={styles['row']} data-sufficiency={sufficiency}>
      <div className={styles['identity']}>
        <strong title={credentialLabel}>{credentialLabel}</strong>
        <span>{item.window_kind === 'model_scoped' ? windowLabel + ' · ' + windowDetail : windowLabel}</span>
      </div>
      <div className={styles['verdict']}>
        <Tag size="small" tone={SUFFICIENCY_TONES[sufficiency]}>{t(`dash.prediction.sufficiency.${sufficiency}`)}</Tag>
        {failure && <Tooltip title={failureDetail} trigger={['hover', 'focus']}><span tabIndex={0} className={styles['reason-trigger']}><Tag size="small">{t(`dash.prediction.failure.${failure}`)}</Tag></span></Tooltip>}
      </div>
      <div className={styles['combined-reading']}>
        <div><span>{t('dash.prediction.remaining_amount')}</span><b>{hasReadError ? t('dash.prediction.missing') : formatRemaining(item, generatedAtMS, t)}</b></div>
        <div><span>{t('dash.prediction.endurance')}</span><b>{hasReadError ? t('dash.prediction.missing') : formatEndurance(item, generatedAtMS, t)}</b></div>
      </div>
      {!hasReadError && hasTokenEstimate ? (
        <p className={styles['estimate']}>{t('dash.prediction.token_estimate', { n: formatTokensFull(item.remaining_tokens!), error: item.capacity_error_percent! })}</p>
      ) : !hasReadError && hasWindowEstimate ? (
        <p className={styles['estimate']}>{t('dash.prediction.window_estimate', { n: Number(item.remaining_window_hours!.toPrecision(3)), error: Number(item.window_error_hours!.toPrecision(2)) })}</p>
      ) : null}
      <p className={styles['context']}>{t('dash.prediction.reset')} <span title={formatReset(item.reset_at_ms, t)}>{formatReset(item.reset_at_ms, t)}</span></p>
      <p className={styles['advice']}>{advice}</p>
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

  const insufficientCount = isError ? null : groups.filter((group) => summarizePredictions(group.items, data!.generated_at_ms, data!.omitted > 0).untilReset === 'insufficient').length;
  const hasUnknownProvider = groups.some((group) => summarizePredictions(group.items, data!.generated_at_ms, data!.omitted > 0).untilReset === 'unknown');

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

      {data && groups.length > 0 && <p className={styles['summary-count']}>{insufficientCount === null ? t('dash.prediction.sufficiency.unknown') : insufficientCount > 0 ? t('dash.prediction.providers_short', { n: insufficientCount }) : hasUnknownProvider ? t('dash.prediction.sufficiency.unknown') : t('dash.prediction.providers_clear')}</p>}
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
              <details>
              <summary className={styles['group-head']}>
                <ProviderBrandIcon iconId={credentialProviderIconId(group.provider === 'step' ? 'stepfun' : group.provider)} size={18} />
                <strong>{providerLabel(group.provider, t)}</strong>
                <Tag size="small" tone={SUFFICIENCY_TONES[isError ? 'unknown' : summarizePredictions(group.items, data!.generated_at_ms, data!.omitted > 0).untilReset]}>
                  {t(`dash.prediction.sufficiency.${isError ? 'unknown' : summarizePredictions(group.items, data!.generated_at_ms, data!.omitted > 0).untilReset}`)}
                </Tag>
              </summary>
              <ul>{group.items.map((item) => <PredictionRow key={JSON.stringify([item.auth_index ?? item.credential_label, item.window_id, item.window_scope, item.window_model])} item={item} generatedAtMS={data!.generated_at_ms} hasReadError={isError} />)}</ul>
              </details>
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
