import type { QuotaItem } from '../types/quota';

/**
 * Only OMC's own Ollama accounts namespace their auth index `ollama:<id>`; a plugin
 * Ollama credential keeps CPA's opaque index. The guard asks which account the row
 * belongs to, so a plugin balance - a real reading the plugin returned - is shown,
 * while an OMC snapshot without the settings signal is still withheld.
 */
export function verifiedQuotaCredits(item: QuotaItem | null | undefined) {
  // Older OMC Ollama snapshots inferred money from unrelated prices on the page.
  if (item?.auth_index?.startsWith('ollama:')
    && item.raw_signals?.ollama_balance_source !== 'settings_balance') return undefined;
  return item?.plan?.credits;
}
