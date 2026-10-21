import type { QuotaItem } from '../types/quota';

export interface ClaudeResetGrantSummary {
  /** Resets still claimable across every grant, not the number of grants. */
  resetsLeft: number;
  /** The soonest end among grants that still hold a reset; absent when none states one. */
  earliestEndsAtMS?: number;
}

/**
 * What a credential's Claude reset grants still offer, or null when nothing remains.
 *
 * A grant whose resets are spent is not an offer, so it neither counts nor sets the
 * earliest expiry; otherwise an exhausted grant ending first would mask the real deadline.
 */
export function claudeResetGrantSummary(item: QuotaItem | null | undefined): ClaudeResetGrantSummary | null {
  let resetsLeft = 0;
  let earliestEndsAtMS: number | undefined;
  for (const grant of item?.reset_grants ?? []) {
    if (!(grant.resets_left > 0)) continue;
    resetsLeft += grant.resets_left;
    if (grant.ends_at_ms && (earliestEndsAtMS === undefined || grant.ends_at_ms < earliestEndsAtMS)) {
      earliestEndsAtMS = grant.ends_at_ms;
    }
  }
  return resetsLeft > 0 ? { resetsLeft, earliestEndsAtMS } : null;
}

/** Whether the account holds any unspent reset: banked Codex credits or Claude grants. */
export function hasAvailableQuotaReset(item: QuotaItem | null | undefined): boolean {
  return (item?.reset_credits?.available_count ?? 0) > 0 || claudeResetGrantSummary(item) !== null;
}
