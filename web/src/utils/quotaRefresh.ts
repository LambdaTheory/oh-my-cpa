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

function apiRetryAtMS(error: ApiError): number | undefined {
  if (error.status !== 429 || !error.data || typeof error.data !== 'object') return undefined;
  const retryAtMS = (error.data as { retry_at_ms?: unknown }).retry_at_ms;
  return typeof retryAtMS === 'number' && Number.isFinite(retryAtMS) ? retryAtMS : undefined;
}

function recordReturnedQuota(
  outcome: QuotaRefreshOutcome,
  authIndex: string,
  quota: QuotaItem,
  onReturned?: (quotas: QuotaItem[]) => void,
) {
  onReturned?.([quota]);
  const name = quota.name || authIndex;
  if (quota.error || quota.status === 'error' || quota.status === 'stale') {
    outcome.failures.push({ authIndex, name, error: quota.error });
    return;
  }
  outcome.succeeded += 1;
  outcome.successes.push({ authIndex, name });
}

async function recoverTransportFailure(
  chunk: string[],
  outcome: QuotaRefreshOutcome,
  onReturned?: (quotas: QuotaItem[]) => void,
) {
  // A dropped batch response says nothing about which upstream probes completed.
  // Isolate the retry to one credential at a time so another transport failure
  // cannot turn every target in the chunk into the same synthetic failure.
  for (const authIndex of chunk) {
    try {
      const response = await api.refreshCredentialQuota(authIndex);
      recordReturnedQuota(outcome, authIndex, response.quota, onReturned);
    } catch (cause) {
      if (cause instanceof ApiError) {
        const retryAtMS = apiRetryAtMS(cause);
        if (retryAtMS !== undefined) {
          outcome.rateLimited.push({ authIndex, name: authIndex, retryAtMS });
          continue;
        }
      }
      outcome.failures.push({ authIndex, name: authIndex, cause });
    }
  }
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
      if (cause instanceof ApiError && cause.status !== 0) {
        chunk.forEach((authIndex) => outcome.failures.push({ authIndex, name: authIndex, cause }));
      } else {
        await recoverTransportFailure(chunk, outcome, onReturned);
      }
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
