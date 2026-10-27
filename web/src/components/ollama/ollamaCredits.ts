import type { NormalizedQuota } from '../../types/quota';

export function readOllamaCredits(quota?: NormalizedQuota | null, status?: string) {
  const credits = quota?.ollama_credits;
  if (!credits || credits.source !== 'settings_usage_credits'
    || (status && status.toLowerCase() !== 'ok')
    || quota?.last_refresh?.outcome === 'failed' || quota?.status === 'error') return null;
  const readCents = (value: unknown): number | null => typeof value === 'number'
    && Number.isSafeInteger(value) && value >= 0 ? value : null;
  const remaining = readCents(credits.remaining_cents);
  const used = readCents(credits.used_cents);
  const total = remaining !== null && used !== null && Number.isSafeInteger(remaining + used)
    ? remaining + used : null;
  const usedPercent = total !== null && total > 0 && used !== null && used > 0
    ? used / total * 100 : null;
  return {
    remaining, used, total, usedPercent,
    refillText: credits.refill_text,
    models: credits.models?.filter((entry) => entry.model && Number.isSafeInteger(entry.requests) && entry.requests >= 0),
  };
}
