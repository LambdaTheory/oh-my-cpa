import type { QuotaItem } from '../types/quota';

export function verifiedQuotaCredits(item: QuotaItem | null | undefined) {
  // Older Ollama snapshots inferred money from unrelated prices on the page.
  if ((item?.provider === 'ollama' || item?.type === 'ollama')
    && item.raw_signals?.ollama_balance_source !== 'settings_balance') return undefined;
  return item?.plan?.credits;
}
