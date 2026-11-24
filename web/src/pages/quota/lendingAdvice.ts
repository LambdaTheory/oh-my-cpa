import type { TFunc } from '../../i18n';
import type { TagTone } from '../../components/common/Tag';
import type { LendingCard, LendingCardRole, LendingDetail, LendingOverview, LendingPool, LendingUnpooled } from '../../types/quota';

const MS_PER_DAY = 86_400_000;
/** The smallest horizon the daily figure uses: under an hour is counted as one hour. */
const MIN_DAYS = 1 / 24;

function round1(value: number): number {
  return Math.round(value * 10) / 10;
}

function withReason(codes: string[], reason: string): string[] {
  return codes.includes(reason) ? codes : [...codes, reason];
}

function isServiceableRole(role: LendingCardRole): boolean {
  return role === 'prefer' || role === 'avoid' || role === 'normal' || role === 'balanced';
}

function recomputeCard(card: LendingCard, nowMS: number): LendingCard {
  // A card with no comparable window keeps the absence; a zero would assert a reading it does not have.
  if (!card.is_comparable || card.reset_at_ms == null || card.remaining_percent == null) {
    return { ...card, days_to_reset: undefined, daily_available_percent: undefined };
  }
  const days = Math.max((card.reset_at_ms - nowMS) / MS_PER_DAY, MIN_DAYS);
  return {
    ...card,
    days_to_reset: round1(days),
    daily_available_percent: round1(card.remaining_percent / days),
  };
}

/**
 * One pool re-read against the page clock.
 *
 * The backend's grouping, window choice and static reasons are the evidence; only the facts that
 * move with time are recomputed here. Re-deriving the pool from credentials would duplicate the
 * server's rules and let the list and the detail disagree, which is the drift AC-15 forbids.
 *
 * A pool whose freshness bound has passed cannot be advised from an old reading, so it degrades
 * to `unavailable` with `reading_stale` on the pool and on every card. It never falls back to
 * ordering by remaining percentage.
 */
export function deriveLendingView(pool: LendingPool, nowMS: number): LendingPool {
  if (pool.fresh_until_ms != null && nowMS > pool.fresh_until_ms) {
    return {
      ...pool,
      state: 'unavailable',
      valid_until_ms: undefined,
      reason_codes: withReason(pool.reason_codes, 'reading_stale'),
      cards: pool.cards.map((card) => ({
        ...card,
        role: 'unavailable',
        days_to_reset: undefined,
        daily_available_percent: undefined,
        reason_codes: withReason(card.reason_codes, 'reading_stale'),
      })),
    };
  }

  const cards = pool.cards.map((card) => recomputeCard(card, nowMS));
  if (pool.state === 'unavailable') return { ...pool, cards };

  const rankable = cards.filter((card) => (
    card.is_comparable
    && isServiceableRole(card.role)
    && card.remaining_percent != null
    && card.daily_available_percent != null
  ));
  // Time alone does not change how many credentials can serve, so this preserves the limit the
  // backend established rather than inventing a preference between cards that cannot be compared.
  if (rankable.length < 2) return { ...pool, state: 'limited', cards };

  const ordered = [...rankable].sort((left, right) => (
    (right.daily_available_percent ?? 0) - (left.daily_available_percent ?? 0)
      || left.name.localeCompare(right.name)
      || left.auth_index.localeCompare(right.auth_index)
  ));
  const highest = ordered[0].daily_available_percent ?? 0;
  const lowest = ordered[ordered.length - 1].daily_available_percent ?? 0;
  const balanced = highest - lowest < 1;
  const roles = new Map<LendingCard, LendingCardRole>();
  ordered.forEach((card, index) => {
    roles.set(card, balanced
      ? 'balanced'
      : index === 0 ? 'prefer'
      : index === ordered.length - 1 ? 'avoid'
      : 'normal');
  });
  return {
    ...pool,
    state: balanced ? 'balanced' : 'advised',
    cards: cards.map((card) => {
      const role = roles.get(card);
      return role ? { ...card, role } : card;
    }),
  };
}

/** The role and pool a card currently carries, or null when it is in no pool. */
export function findLendingCard(
  lending: LendingOverview,
  authIndex: string,
  nowMS: number,
): { pool: LendingPool; card: LendingCard } | null {
  for (const pool of lending.pools) {
    const view = deriveLendingView(pool, nowMS);
    const card = view.cards.find((entry) => entry.auth_index === authIndex);
    if (card) return { pool: view, card };
  }
  return null;
}

/**
 * A credential's detail view, projected from the overview the page already holds.
 *
 * The overview carries every pool and the unpooled list, so the Quota tab needs no second request
 * and cannot show a different snapshot from the row that opened it. A credential outside every
 * pool keeps the overview's own reasons. A gateway that predates the field yields `undefined`,
 * which the panel renders as the empty state rather than hiding the feature.
 */
export function lendingDetailFor(
  lending: LendingOverview | null | undefined,
  authIndex: string,
  nowMS: number,
): LendingDetail | undefined {
  if (!lending) return undefined;
  const found = findLendingCard(lending, authIndex, nowMS);
  const unpooled = lending.unpooled.find((entry) => entry.auth_index === authIndex);
  return {
    generated_at_ms: lending.generated_at_ms,
    pool: found?.pool ?? null,
    unpooled_reason_codes: found ? [] : (unpooled?.reason_codes ?? []),
  };
}

export function lendingRoleTone(role: LendingCardRole): TagTone {
  if (role === 'prefer') return 'accent';
  if (role === 'avoid' || role === 'limited') return 'warn';
  return 'neutral';
}

export function lendingRoleLabel(role: LendingCardRole, t: TFunc): string {
  switch (role) {
    case 'prefer': return t('quota.lending.role.prefer');
    case 'avoid': return t('quota.lending.role.avoid');
    case 'normal': return t('quota.lending.role.normal');
    case 'balanced': return t('quota.lending.role.balanced');
    case 'limited': return t('quota.lending.role.limited');
    case 'unavailable': return t('quota.lending.role.unavailable');
  }
}

const REASON_KEYS: Record<string, string> = {
  plan_type_unknown: 'quota.lending.reason.plan_type_unknown',
  pool_too_small: 'quota.lending.reason.pool_too_small',
  no_long_window: 'quota.lending.reason.no_long_window',
  window_type_mismatch: 'quota.lending.reason.window_type_mismatch',
  reset_time_unknown: 'quota.lending.reason.reset_time_unknown',
  reading_stale: 'quota.lending.reason.reading_stale',
  reading_failed: 'quota.lending.reason.reading_failed',
  remaining_unknown: 'quota.lending.reason.remaining_unknown',
  remaining_below_ten: 'quota.lending.reason.remaining_below_ten',
  window_exhausted: 'quota.lending.reason.window_exhausted',
  cooldown_active: 'quota.lending.reason.cooldown_active',
  needs_reauth: 'quota.lending.reason.needs_reauth',
  credential_disabled: 'quota.lending.reason.credential_disabled',
  not_serviceable: 'quota.lending.reason.not_serviceable',
};

/** A reason code's sentence, or the raw code for one this console does not know yet. */
export function lendingReasonLabel(code: string, t: TFunc): string {
  const key = REASON_KEYS[code];
  return key ? t(key) : code;
}


/**
 * The instant the page's advice is grounded in: the oldest pool reading it re-reads, or the
 * overview's own generation time when no pool carries one. The page header names this instant, so
 * a reader knows how far the recomputed figures have moved from the stored snapshot.
 */
export function lendingBasisMS(lending: LendingOverview): number {
  const observed = lending.pools
    .map((pool) => pool.observed_at_ms)
    .filter((value): value is number => value != null)
    .sort((left, right) => left - right)[0];
  return observed ?? lending.generated_at_ms;
}


export type LendingEntryKind = 'advised' | 'balanced' | 'plain';

export interface LendingEntryState {
  kind: LendingEntryKind;
  advisedCount: number;
}

/**
 * The OAuth header entry's three states, derived from the same clock as the page.
 *
 * Any advised pool wins, and the count is how many pools need a preference. With no advised pool
 * a balanced one still means the readings were comparable, so the entry says "balanced"; only
 * when every pool is limited or unavailable does it fall back to the plain label. A gateway that
 * predates the field has no entry at all.
 */
export function lendingEntryState(
  lending: LendingOverview | null | undefined,
  nowMS: number,
): LendingEntryState | null {
  if (!lending) return null;
  const states = lending.pools.map((pool) => deriveLendingView(pool, nowMS).state);
  const advisedCount = states.filter((state) => state === 'advised').length;
  if (advisedCount > 0) return { kind: 'advised', advisedCount };
  if (states.includes('balanced')) return { kind: 'balanced', advisedCount: 0 };
  return { kind: 'plain', advisedCount: 0 };
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
