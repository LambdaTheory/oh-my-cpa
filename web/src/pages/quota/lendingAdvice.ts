import type { TFunc } from '../../i18n';
import type {
  LendingCard,
  LendingDetail,
  LendingOutsideCard,
  LendingOverview,
  LendingPool,
  LendingUnpooled,
} from '../../types/quota';

/**
 * The suggestion model, kept in one leaf module so the page, the OAuth entry and the credential
 * drawer read the same numbers.
 *
 * Every ordering, tier, suggested value and change flag is the backend's: recomputing them against
 * the page clock is what let the list and the drawer disagree, and AC-23 asks for the opposite -
 * the order and numbers stay put until the reading itself moves. The clock is used only to format
 * reset instants.
 */

export type LendingEntryKind = 'change' | 'keep' | 'plain';

/** The name-bearing part of a pool card or an outside card. */
export interface LendingNamedCard {
  display_name?: string;
  name?: string;
  auth_index: string;
}

/**
 * The one name a card is shown by. The friendly name comes from the credential's file name; a
 * payload that predates it falls back to the file name and then to the auth index, so every
 * surface renders the same name instead of failing on an absent one (PRD §7).
 */
export function lendingCardName(card: LendingNamedCard): string {
  return card.display_name || card.name || card.auth_index;
}

export interface LendingEntryState {
  kind: LendingEntryKind;
  changeCount: number;
}

/**
 * The OAuth header entry's three states from the backend's own counts.
 *
 * Any pool with a card to change wins; otherwise a pool that did produce a ranking means there is
 * advice and none of it asks for action; only pools without a ranking at all fall back to the
 * plain label.
 */
export function lendingEntryState(lending: LendingOverview | null | undefined): LendingEntryState | null {
  if (!lending) return null;
  const changeCount = lending.pools.reduce((sum, pool) => sum + pool.needs_change_count, 0);
  if (changeCount > 0) return { kind: 'change', changeCount };
  if (lending.pools.some((pool) => pool.state === 'advised')) return { kind: 'keep', changeCount: 0 };
  return { kind: 'plain', changeCount: 0 };
}

/** The pool a credential belongs to, or null when it belongs to none. */
export function findLendingCard(
  lending: LendingOverview,
  authIndex: string,
): { pool: LendingPool; card: LendingCard } | null {
  for (const pool of lending.pools) {
    const card = pool.cards.find((entry) => entry.auth_index === authIndex);
    if (card) return { pool, card };
  }
  return null;
}

/**
 * A credential's detail view, projected from the overview the page already holds.
 *
 * The drawer renders the same pool object the page shows, so the two cannot state different
 * numbers. A credential outside every pool keeps the overview's own reasons.
 */
export function lendingDetailFor(
  lending: LendingOverview | null | undefined,
  authIndex: string,
): LendingDetail | undefined {
  if (!lending) return undefined;
  const found = findLendingCard(lending, authIndex);
  const unpooled = lending.unpooled.find((entry) => entry.auth_index === authIndex);
  return {
    generated_at_ms: lending.generated_at_ms,
    pool: found?.pool ?? null,
    unpooled_reason_codes: found ? [] : (unpooled?.reason_codes ?? []),
  };
}

/**
 * The instant the page's advice is grounded in: the oldest pool reading it shows, or the
 * overview's own generation time when no pool carries one.
 */
export function lendingBasisMS(lending: LendingOverview): number {
  const observed = lending.pools
    .map((pool) => pool.observed_at_ms)
    .filter((value): value is number => value != null)
    .sort((left, right) => left - right)[0];
  return observed ?? lending.generated_at_ms;
}

/** A priority as the integer an operator would type into the CPA field. */
export function lendingPriorityText(value: number | undefined): string {
  return value == null ? '—' : String(value);
}

/** The pool header's right-hand verdict. */
export function lendingPoolHeadline(pool: LendingPool, t: TFunc): string {
  if (pool.state === 'no_advice') return t('quota.lending.pool_no_advice');
  return pool.needs_change_count > 0
    ? t('quota.lending.pool_change', { n: pool.needs_change_count })
    : t('quota.lending.pool_keep');
}

export interface LendingSentence {
  text: string;
  /** A second, smaller line under the sentence; absent when there is nothing to add. */
  note?: string;
}

/**
 * The pool's one sentence: who to set to what, and who to leave alone.
 *
 * It is assembled only from the rows the cards already show, so a reader can check it against the
 * numbers without opening the evidence. A card whose current priority is unreadable is never
 * named as "no change needed": that judgment was not made.
 */
export function lendingPoolSentence(pool: LendingPool, t: TFunc): LendingSentence {
  const changed = pool.cards.filter((card) => card.role === 'suggested' && card.needs_change === true);
  const kept = pool.cards.filter((card) => card.role === 'suggested' && card.needs_change === false);
  let text: string;
  if (changed.length > 0) {
    const separator = t('quota.lending.list_separator');
    const pairs = changed
      .map((card) => t('quota.lending.say_pair', { name: lendingCardName(card), n: lendingPriorityText(card.suggested_priority) }))
      .join(separator);
    const setPart = t('quota.lending.say_set', { pairs });
    if (kept.length === 1) {
      text = t('quota.lending.say_with_keep', { set: setPart, keep: lendingCardName(kept[0]) });
    } else if (kept.length > 1) {
      text = t('quota.lending.say_with_rest', {
        set: setPart,
        names: kept.map((card) => lendingCardName(card)).join(separator),
      });
    } else {
      text = setPart;
    }
  } else if (pool.is_balanced) {
    text = t(pool.cards.length === 2 ? 'quota.lending.say_balanced_pair' : 'quota.lending.say_balanced_other', {
      n: pool.cards.length,
    });
  } else {
    text = t('quota.lending.say_keep');
  }
  const note = pool.is_balanced
    ? t('quota.lending.say_balanced_note')
    : pool.hold_count > 0
      ? t('quota.lending.say_hold_suffix', { n: pool.hold_count })
      : undefined;
  return { text, note };
}

export interface LendingHoldCount {
  code: string;
  count: number;
}

/**
 * Hold cards counted once by primary reason.
 *
 * A credential can carry several reasons; the collapsed line shows one count per reason so the
 * numbers add up to the cards, and the expanded rows keep each card's full list. Codes follow the
 * contract's vocabulary order rather than first appearance, so two pools list the same reasons in
 * the same order.
 */
export function lendingHoldCounts(pool: LendingPool): LendingHoldCount[] {
  const counts = new Map<string, number>();
  for (const card of pool.cards) {
    if (card.role !== 'hold') continue;
    const code = card.primary_reason ?? card.reason_codes[0];
    if (!code) continue;
    counts.set(code, (counts.get(code) ?? 0) + 1);
  }
  const order = (code: string) => {
    const index = pool.reason_codes.indexOf(code);
    return index === -1 ? Number.MAX_SAFE_INTEGER : index;
  };
  return [...counts.entries()]
    .map(([code, count]) => ({ code, count }))
    .sort((left, right) => order(left.code) - order(right.code) || left.code.localeCompare(right.code));
}

/**
 * A suggested card's position inside the pool's ranking.
 *
 * Cards in one tier share the position, so the table marks them tied rather than giving one tier
 * several numbers; a card without a tier has no ranking to show and falls back to the collapse copy.
 */
export function lendingRankLabel(pool: LendingPool, card: LendingCard, t: TFunc): string {
  if (card.tier == null) return t('quota.lending.rank_same');
  const tied = pool.cards.some((entry) => (
    entry.auth_index !== card.auth_index && entry.role === 'suggested' && entry.tier === card.tier
  ));
  return tied ? t('quota.lending.rank_tied', { n: card.tier }) : t('quota.lending.rank_n', { n: card.tier });
}

/** Where an outside card lives: another pool's label, or its provider when it is in no pool. */
export function lendingOutsidePoolLabel(card: LendingOutsideCard): string {
  return card.pool_label || card.provider;
}

const HOLD_REASON_KEYS: Record<string, string> = {
  needs_reauth: 'needs_reauth',
  cooldown_active: 'cooldown_active',
  reading_failed: 'reading_failed',
  window_exhausted: 'window_exhausted',
  reading_stale: 'reading_stale',
  reset_time_unknown: 'reset_time_unknown',
  remaining_unknown: 'remaining_unknown',
  no_long_window: 'no_long_window',
  remaining_below_ten: 'remaining_below_ten',
  window_type_mismatch: 'window_type_mismatch',
};

/** A hold card's short label, or the raw code for one this console does not know yet. */
export function lendingHoldLabel(code: string | undefined, t: TFunc): string {
  if (!code) return t('quota.lending.hold.unknown');
  const key = HOLD_REASON_KEYS[code];
  return key ? t(`quota.lending.hold.${key}`) : code;
}

export interface LendingHoldRowNoteContext {
  /** The overview's generated instant; the refill verdict must not follow the page clock. */
  generatedAtMS?: number;
  /** The page clock, used only for the reading's age. */
  nowMS: number;
  /** `quotaWindowLabel` for the card's own window, resolved by the view. */
  windowLabel?: string;
}

/**
 * The expanded hold row's one sentence.
 *
 * The primary reason picks the sentence, as the PRD's table does. A stale reading whose refill
 * instant has already arrived is named before its age: the reading cannot be current even when it
 * was taken minutes ago. That comparison uses the pool's generated instant, not the page clock,
 * so the sentence does not flip as the page ticks.
 */
export function lendingHoldRowNote(
  card: LendingCard,
  t: TFunc,
  context: LendingHoldRowNoteContext,
): string | undefined {
  const key = card.primary_reason ? HOLD_REASON_KEYS[card.primary_reason] : undefined;
  if (!key) return undefined;
  if (key === 'reading_stale') {
    if (card.reset_at_ms != null && context.generatedAtMS != null && card.reset_at_ms <= context.generatedAtMS) {
      return t('quota.lending.hold_note.reading_stale_expired');
    }
    if (card.observed_at_ms != null) {
      const ageMS = Math.max(0, context.nowMS - card.observed_at_ms);
      if (ageMS < 3_600_000) {
        return t('quota.lending.hold_note.reading_stale_minutes', {
          n: Math.max(1, Math.round(ageMS / 60_000)),
        });
      }
      return t('quota.lending.hold_note.reading_stale', { n: Math.round(ageMS / 3_600_000) });
    }
    return t('quota.lending.hold_note.reading_stale_unknown');
  }
  if (key === 'remaining_below_ten') {
    if (card.remaining_percent != null && card.window_kind && context.windowLabel) {
      return t('quota.lending.hold_note.remaining_below_ten_window', {
        window: context.windowLabel,
        n: Math.round(card.remaining_percent),
      });
    }
    return t('quota.lending.hold_note.remaining_below_ten');
  }
  return t(`quota.lending.hold_note.${key}`);
}

const UNPOOLED_REASON_KEYS: Record<string, string> = {
  plan_type_unknown: 'quota.lending.reason.plan_type_unknown',
  pool_too_small: 'quota.lending.reason.pool_too_small',
  credential_disabled: 'quota.lending.reason.credential_disabled',
};

/** An unpooled credential's reason sentence, or the raw code for an unknown one. */
export function lendingReasonLabel(code: string, t: TFunc): string {
  const key = UNPOOLED_REASON_KEYS[code];
  return key ? t(key) : code;
}

export interface LendingUnpooledMember {
  auth_index: string;
  name: string;
}

export interface LendingUnpooledGroup {
  code: string;
  members: LendingUnpooledMember[];
}

/**
 * Group the unpooled credentials by the reason the backend gave.
 *
 * A credential can carry more than one reason, so it appears once in each of its reason groups;
 * the overview's distinct `unpooled_count` stays the total a reader sees first. A repeated code on
 * one credential is collapsed so it cannot count twice inside one group.
 */
export function groupLendingUnpooled(unpooled: LendingUnpooled[]): LendingUnpooledGroup[] {
  const groups = new Map<string, LendingUnpooledMember[]>();
  for (const entry of unpooled) {
    for (const code of new Set(entry.reason_codes)) {
      const members = groups.get(code) ?? [];
      members.push({ auth_index: entry.auth_index, name: entry.name });
      groups.set(code, members);
    }
  }
  return [...groups.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([code, members]) => ({ code, members }));
}
