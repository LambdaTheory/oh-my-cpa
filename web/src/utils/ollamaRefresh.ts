import type { QueryClient } from '@tanstack/react-query';
import { api, ApiError, describeError } from '../api/client';
import type { TFunc } from '../i18n';
import type {
  OllamaAccountListResponse,
  OllamaQuotaResponse,
  OllamaRefreshResponse,
} from '../types/ollama';
import { formatTimeWithCountdown } from '../pages/quota/quotaFormat';

export interface OllamaRefreshFeedback {
  success: boolean;
  refreshed: boolean;
  status?: string;
  error?: string;
  retryAtMS?: number | null;
}

export interface OllamaRefreshOptions {
  queryClient: QueryClient;
  t: TFunc;
  toast: {
    success: (msg: string) => void;
    warning: (msg: string) => void;
    error: (msg: string) => void;
    info: (msg: string) => void;
  };
}

export async function executeOllamaAccountRefresh(
  id: string,
  options: OllamaRefreshOptions,
): Promise<OllamaRefreshFeedback> {
  const { queryClient, t, toast } = options;

  try {
    const response: OllamaRefreshResponse = await api.refreshOllamaAccount(id);
    const { account, quota, refreshed, retry_at_ms } = response;

    // Update query cache with refreshed account and quota
    queryClient.setQueryData<OllamaAccountListResponse>(['ollama-accounts'], (old) => {
      if (!old) return old;
      return {
        ...old,
        accounts: old.accounts.map((a) => (a.id === id ? { ...a, ...account } : a)),
      };
    });

    if (quota) {
      queryClient.setQueryData<OllamaQuotaResponse>(['ollama-quota'], (old) => {
        if (!old) return old;
        const exists = old.accounts.some((q) => q.account_id === id);
        if (exists) {
          return {
            ...old,
            accounts: old.accounts.map((q) =>
              q.account_id === id
                ? {
                    ...q,
                    status: account.status,
                    observed_at_ms: account.last_observed_at_ms ?? q.observed_at_ms,
                    quota,
                  }
                : q,
            ),
          };
        }
        return {
          ...old,
          accounts: [
            ...old.accounts,
            {
              account_id: id,
              label: account.label,
              enabled: account.enabled,
              status: account.status,
              observed_at_ms: account.last_observed_at_ms,
              quota,
            },
          ],
        };
      });
    }

    void queryClient.invalidateQueries({ queryKey: ['ollama-accounts'] });
    void queryClient.invalidateQueries({ queryKey: ['ollama-quota'] });

    const statusNormalized = (account.status ?? '').toLowerCase();
    const isHealthy = statusNormalized === 'ok';

    if (refreshed && isHealthy) {
      toast.success(t('ollama.refresh_success'));
      return {
        success: true,
        refreshed,
        status: account.status,
        retryAtMS: retry_at_ms,
      };
    }

    // Collection was not fully refreshed or resulted in unhealthy error status:
    // Do NOT report success! Keep last good snapshot.
    const failureReason = account.newest_error
      || (account.status ? t(`ollama.status_${statusNormalized}`) : t('af.error'));
    toast.warning(t('ollama.refresh_scrape_failed', { reason: failureReason }));
    return {
      success: false,
      refreshed,
      status: account.status,
      error: failureReason,
      retryAtMS: retry_at_ms,
    };
  } catch (err: unknown) {
    if (err instanceof ApiError) {
      if (err.status === 429) {
        const errorData = err.data as Partial<OllamaRefreshResponse> | undefined;
        const retryAtMS = errorData?.retry_at_ms ?? null;
        if (errorData?.account) {
          queryClient.setQueryData<OllamaAccountListResponse>(['ollama-accounts'], (old) => {
            if (!old) return old;
            return {
              ...old,
              accounts: old.accounts.map((a) => (a.id === id ? { ...a, ...errorData.account, retry_at_ms: retryAtMS } : a)),
            };
          });
        }
        const timeStr = retryAtMS ? formatTimeWithCountdown(retryAtMS, Date.now(), t) : '';
        toast.warning(timeStr ? t('ollama.refresh_rate_limited', { time: timeStr }) : t('ollama.refresh_rate_limited_default'));
        return {
          success: false,
          refreshed: false,
          retryAtMS,
          error: 'rate_limited',
        };
      }
      if (err.status === 503) {
        toast.error(t('ollama.refresh_service_disabled'));
        return {
          success: false,
          refreshed: false,
          error: 'service_disabled',
        };
      }
    }
    toast.error(describeError(err));
    return {
      success: false,
      refreshed: false,
      error: describeError(err),
    };
  }
}
