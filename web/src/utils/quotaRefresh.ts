import { api, ApiError } from '../api/client';
import type { TFunc } from '../i18n';
import { formatTimeWithCountdown } from '../pages/quota/quotaFormat';
import type { QuotaItem } from '../types/quota';

/** How many credentials one request asks the gateway to read upstream. */
const QUOTA_REFRESH_CHUNK_SIZE = 10;

export interface QuotaRefreshFailure {
  authIndex: string;
  name: string;
  /** What the gateway reported for this credential. */
  error?: string;
  /** What failed the request that carried this credential, when the request itself did. */
  cause?: unknown;
  /** The server omitted this credential from a per-target response. */
  isMissing?: boolean;
}

export interface QuotaRefreshRateLimit {
  authIndex: string;
  name: string;
  retryAtMS: number;
}

export interface QuotaRefreshOutcome {
  succeeded: number;
  successes: Array<{ authIndex: string; name: string }>;
  rateLimited: QuotaRefreshRateLimit[];
  failures: QuotaRefreshFailure[];
}

/**
 * Runs one live quota refresh over the given credentials and tallies each target's outcome.
 *
 * Every surface that offers the refresh goes through here, so they cannot disagree about what a
 * failed, an unanswered and a refreshed credential are. A request that fails takes only its own
 * chunk with it; the run goes on, because one provider timing out says nothing about the rest.
 * `onReturned` receives each chunk's readings as they arrive, for a caller that shows them at once.
 */
export async function refreshCredentialQuotas(
  authIndexes: string[],
  onReturned?: (quotas: QuotaItem[]) => void,
): Promise<QuotaRefreshOutcome> {
  const outcome: QuotaRefreshOutcome = { succeeded: 0, successes: [], rateLimited: [], failures: [] };
  for (let start = 0; start < authIndexes.length; start += QUOTA_REFRESH_CHUNK_SIZE) {
    const chunk = authIndexes.slice(start, start + QUOTA_REFRESH_CHUNK_SIZE);
    try {
      const response = await api.batchRefreshCredentialQuotas(chunk);
      const returned = new Map(response.results.map((item) => [item.auth_index, item]));
      onReturned?.(response.quotas);
      for (const authIndex of chunk) {
        const result = returned.get(authIndex);
        if (!result) outcome.failures.push({ authIndex, name: authIndex, isMissing: true });
        else if (result.outcome === 'success') {
          outcome.succeeded += 1;
          outcome.successes.push({ authIndex, name: result.name || result.quota?.name || authIndex });
        }
        else if (result.outcome === 'rate_limited') outcome.rateLimited.push({
          authIndex,
          name: result.name || authIndex,
          retryAtMS: result.retry_at_ms,
        });
        else outcome.failures.push({ authIndex, name: result.name || authIndex, error: result.error });
      }
    } catch (cause) {
      chunk.forEach((authIndex) => outcome.failures.push({ authIndex, name: authIndex, cause }));
    }
  }
  return outcome;
}

/** The reason a failed target is listed with, in the reader's language. */
export function quotaRefreshFailureReason(failure: QuotaRefreshFailure, t: TFunc): string {
  if (failure.isMissing) return t('omc.quota_refresh_missing');
  if (failure.cause === undefined) return failure.error || t('quota.status_error');
  if (failure.cause instanceof ApiError && failure.cause.status === 501) return t('af.unsupported');
  return failure.cause instanceof Error ? failure.cause.message : t('af.request_failed');
}

/** A rate-limit result stays operator-facing and never exposes its internal error code. */
export function quotaRefreshRateLimitReason(result: QuotaRefreshRateLimit, t: TFunc): string {
  return t('omc.quota_refresh_rate_limited', {
    time: formatTimeWithCountdown(result.retryAtMS, Date.now(), t),
  });
}
