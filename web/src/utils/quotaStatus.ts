import type { QuotaItem } from '../types/quota';
import type { TFunc } from '../i18n';

export function quotaRefreshReasonLabel(attempt: QuotaItem['last_refresh'], t: TFunc): string {
  if (attempt?.http_status) return `HTTP ${attempt.http_status}`;
  switch (attempt?.reason) {
    case 'timeout': return t('quota.refresh_reason_timeout');
    case 'network': return t('quota.refresh_reason_network');
    case 'auth': return t('quota.refresh_reason_auth');
    case 'parse': return t('quota.refresh_reason_parse');
    default: return t('quota.refresh_reason_unknown');
  }
}

// Two idle scheduler cycles allow a delayed collection without trusting an old
// reading indefinitely. Ollama collects every 15 minutes; CPA adaptive idle is
// two hours. Short provider windows cap that tolerance to one quarter-cycle.
export const QUOTA_FRESHNESS_MS = 4 * 60 * 60_000;
export const OLLAMA_QUOTA_FRESHNESS_MS = 30 * 60_000;
export const QUOTA_WINDOW_FRESHNESS_FRACTION = 0.25;
export const QUOTA_MIN_FRESHNESS_MS = 2 * 60 * 60_000;

export type QuotaDisplayStatus = QuotaItem['status'] | 'refresh_failed' | 'rate_limited' | 'disabled';

export function quotaFreshnessMS(item: QuotaItem): number {
  if (item.provider === 'ollama') return OLLAMA_QUOTA_FRESHNESS_MS;
  const periods = (item.windows ?? []).map((window) => window.period_hours).filter((hours): hours is number => hours != null && hours > 0);
  const windowBound = periods.length ? Math.min(...periods) * 60 * 60_000 * QUOTA_WINDOW_FRESHNESS_FRACTION : QUOTA_FRESHNESS_MS;
  return Math.max(QUOTA_MIN_FRESHNESS_MS, Math.min(QUOTA_FRESHNESS_MS, windowBound));
}

export function resolveQuotaStatus(item: QuotaItem, nowMS = Date.now()): QuotaDisplayStatus {
  if (item.disabled) return 'disabled';
  if (item.last_refresh?.outcome === 'rate_limited') return 'rate_limited';
  if (item.last_refresh?.outcome === 'failed') return 'refresh_failed';
  if (item.active_cooldown?.is_active || item.status === 'cooldown') return 'cooldown';
  if (item.status === 'loading' || item.status === 'error') return item.status;
  if (item.observed_at_ms > 0 && nowMS - item.observed_at_ms > quotaFreshnessMS(item)) return 'stale';
  return item.status;
}

export const QUOTA_STATUS_LABELS: Record<QuotaDisplayStatus, string> = {
  healthy: 'quota.status_normal', warning: 'quota.status_warning', exhausted: 'quota.status_exceeded',
  cooldown: 'quota.status_cooldown', error: 'quota.status_error', stale: 'quota.status_data_stale',
  unpublished: 'quota.status_unpublished', loading: 'common.loading', idle: 'quota.status_idle',
  refresh_failed: 'quota.status_refresh_failed', rate_limited: 'quota.status_rate_limited', disabled: 'af.disabled',
};
