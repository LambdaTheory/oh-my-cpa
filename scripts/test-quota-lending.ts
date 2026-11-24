/**
 * Locks the read-only lending layer's time-derived behaviour.
 *
 * The backend owns grouping, window choice and the static reasons; the console only re-reads the
 * facts that move with the page clock. These assertions fix the boundaries that decide what an
 * operator is told: the one-point-per-day threshold, the one-hour floor on the daily figure, the
 * fast-reset `period/3` and 50% edges, the stale-reading downgrade, and the reset sort key's
 * long-window rule. A drift here would let the list, the drawer and the reason sentence disagree.
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import type { TFunc } from '../web/src/i18n/index.tsx';
import type { LendingCard, LendingOverview, LendingPool } from '../web/src/types/quota.ts';
import type { QuotaWindow } from '../web/src/types/quota.ts';
import {
  deriveLendingView,
  findLendingCard,
  groupLendingUnpooled,
  lendingBasisMS,
  lendingEntryState,
  lendingDetailFor,
  lendingReasonLabel,
  lendingRoleLabel,
  lendingRoleTone,
} from '../web/src/pages/quota/lendingAdvice.ts';
import {
  earliestLongWindow,
  isFastResetWindow,
  isLongStandardWindow,
  quotaFastResetState,
} from '../web/src/pages/quota/quotaWindowSelection.ts';
import {
  formatCountdown,
  formatShortDateTime,
  quotaResetCountdownLabel,
} from '../web/src/pages/quota/quotaFormat.ts';

const DAY = 86_400_000;
const HOUR = 3_600_000;
const NOW = Date.UTC(2026, 10, 23, 12, 0);

// The formatters only need their keys and placeholders; the stub keeps this suite independent of
// the dictionary's copy.
const t = ((key: string, params?: Record<string, unknown>) => (
  params
    ? `${key}(${Object.keys(params).map((name) => `${name}=${String(params[name])}`).join(',')})`
    : key
)) as TFunc;

function makeCard(overrides: Partial<LendingCard> & Pick<LendingCard, 'auth_index' | 'name'>): LendingCard {
  return {
    role: 'normal',
    reason_codes: [],
    is_comparable: true,
    remaining_percent: 50,
    reset_at_ms: NOW + 4 * DAY,
    reset_accuracy: 'exact',
    window_id: 'weekly',
    window_kind: 'weekly',
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    days_to_reset: 4,
    daily_available_percent: 12.5,
    ...overrides,
  };
}

function makePool(cards: LendingCard[], overrides: Partial<LendingPool> = {}): LendingPool {
  const resets = cards
    .map((card) => card.reset_at_ms)
    .filter((value): value is number => value != null);
  return {
    id: 'codex:pro',
    provider: 'codex',
    plan_type: 'pro',
    plan_label: 'Pro 200',
    window_kind: 'weekly',
    state: 'advised',
    valid_until_ms: resets.length > 0 ? Math.min(...resets) : undefined,
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    is_reference: false,
    reason_codes: [],
    cards,
    ...overrides,
  };
}

function makeWindow(overrides: Partial<QuotaWindow> & Pick<QuotaWindow, 'id'>): QuotaWindow {
  return {
    label: overrides.id,
    kind: 'weekly',
    scope: 'standard',
    period_hours: 168,
    reset_at_ms: NOW + DAY,
    ...overrides,
  };
}

test('the ranking follows the page clock instead of the loaded snapshot', () => {
  const cards = [
    makeCard({ auth_index: 'a', name: 'A', remaining_percent: 88, reset_at_ms: NOW + 4 * DAY }),
    makeCard({ auth_index: 'b', name: 'B', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
  ];

  const atLoad = deriveLendingView(makePool(cards), NOW);
  assert.equal(atLoad.state, 'advised');
  assert.deepEqual(atLoad.cards.map((card) => [card.name, card.role]), [['A', 'prefer'], ['B', 'avoid']]);
  assert.deepEqual(atLoad.cards.map((card) => card.days_to_reset), [4, 2]);
  assert.deepEqual(atLoad.cards.map((card) => card.daily_available_percent), [22, 20]);

  const oneDayLater = deriveLendingView(makePool(cards), NOW + DAY);
  assert.deepEqual(oneDayLater.cards.map((card) => [card.name, card.role]), [['A', 'avoid'], ['B', 'prefer']]);
  assert.deepEqual(oneDayLater.cards.map((card) => card.days_to_reset), [3, 1]);
  assert.deepEqual(oneDayLater.cards.map((card) => card.daily_available_percent), [29.3, 40]);
});

test('a one-point-per-day spread is advised; anything smaller is balanced', () => {
  const advised = deriveLendingView(makePool([
    makeCard({ auth_index: 'a', name: 'A', remaining_percent: 22, reset_at_ms: NOW + 2 * DAY }),
    makeCard({ auth_index: 'b', name: 'B', remaining_percent: 40, reset_at_ms: NOW + 4 * DAY }),
  ]), NOW);
  assert.equal(advised.state, 'advised');
  assert.deepEqual(advised.cards.map((card) => card.role), ['prefer', 'avoid']);

  const balanced = deriveLendingView(makePool([
    makeCard({ auth_index: 'a', name: 'A', remaining_percent: 21.8, reset_at_ms: NOW + 2 * DAY }),
    makeCard({ auth_index: 'b', name: 'B', remaining_percent: 40, reset_at_ms: NOW + 4 * DAY }),
  ]), NOW);
  assert.equal(balanced.state, 'balanced');
  assert.deepEqual(balanced.cards.map((card) => card.role), ['balanced', 'balanced']);
});

test('a reset under an hour uses one hour for the daily figure', () => {
  const view = deriveLendingView(makePool([
    makeCard({ auth_index: 'a', name: 'A', remaining_percent: 10, reset_at_ms: NOW + 30 * 60_000 }),
    makeCard({ auth_index: 'b', name: 'B', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
  ]), NOW);
  assert.equal(view.cards[0].days_to_reset, 0);
  assert.equal(view.cards[0].daily_available_percent, 240);
});

test('a pool past its freshness bound becomes unavailable, never a percentage order', () => {
  const pool = makePool([
    makeCard({
      auth_index: 'a', name: 'A', role: 'prefer', remaining_percent: 88,
      reset_at_ms: NOW + 4 * DAY, reason_codes: ['needs_reauth'],
    }),
    makeCard({ auth_index: 'b', name: 'B', role: 'avoid', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
  ], { fresh_until_ms: NOW - 1, valid_until_ms: NOW + 2 * DAY });

  const view = deriveLendingView(pool, NOW);
  assert.equal(view.state, 'unavailable');
  assert.equal(view.valid_until_ms, undefined);
  assert.ok(view.reason_codes.includes('reading_stale'));
  for (const card of view.cards) {
    assert.equal(card.role, 'unavailable');
    assert.equal(card.days_to_reset, undefined);
    assert.equal(card.daily_available_percent, undefined);
    assert.ok(card.reason_codes.includes('reading_stale'));
  }
  assert.ok(view.cards[0].reason_codes.includes('needs_reauth'), 'static reasons survive the downgrade');

  const atBoundary = deriveLendingView({ ...pool, fresh_until_ms: NOW }, NOW);
  assert.equal(atBoundary.state, 'advised');
});

test('fewer than two serviceable cards stays limited and keeps each reason', () => {
  const view = deriveLendingView(makePool([
    makeCard({ auth_index: 'a', name: 'A', role: 'normal' }),
    makeCard({
      auth_index: 'b', name: 'B', role: 'limited', is_comparable: false,
      reason_codes: ['window_exhausted'],
    }),
  ], { state: 'limited' }), NOW);
  assert.equal(view.state, 'limited');
  assert.deepEqual(view.cards.map((card) => card.role), ['normal', 'limited']);
  assert.deepEqual(view.cards[1].reason_codes, ['window_exhausted']);
});

test('an unavailable pool keeps its state, roles and window choice', () => {
  const view = deriveLendingView(makePool([
    makeCard({
      auth_index: 'a', name: 'A', role: 'unavailable', is_comparable: false,
      reason_codes: ['reset_time_unknown'],
    }),
  ], {
    state: 'unavailable',
    reason_codes: ['reset_time_unknown'],
    valid_until_ms: undefined,
  }), NOW);
  assert.equal(view.state, 'unavailable');
  assert.equal(view.cards[0].role, 'unavailable');
  assert.equal(view.cards[0].days_to_reset, undefined);
  assert.deepEqual(view.cards[0].reason_codes, ['reset_time_unknown']);
});

test('the reset sort key uses only long standard windows with a stated instant', () => {
  assert.equal(isLongStandardWindow(makeWindow({ id: 'weekly', period_hours: 168 })), true);
  assert.equal(isLongStandardWindow(makeWindow({ id: 'daily', kind: 'daily', period_hours: 24 })), true);
  assert.equal(isLongStandardWindow(makeWindow({ id: 'short', kind: 'five_hour', period_hours: 5 })), false);
  assert.equal(isLongStandardWindow(makeWindow({ id: 'rolling', kind: 'rolling' })), false);
  assert.equal(isLongStandardWindow(makeWindow({ id: 'credit', kind: 'credit_usage' })), false);
  assert.equal(isLongStandardWindow(makeWindow({ id: 'model', scope: 'model' })), false);
  assert.equal(isLongStandardWindow(makeWindow({ id: 'no-period', period_hours: undefined })), false);

  const key = earliestLongWindow([
    makeWindow({ id: 'five-hour', kind: 'five_hour', period_hours: 5, reset_at_ms: NOW + HOUR, remaining_percent: 90 }),
    makeWindow({ id: 'weekly', reset_at_ms: NOW + 3 * DAY, remaining_percent: 40 }),
    makeWindow({ id: 'monthly', kind: 'monthly', period_hours: 720, reset_at_ms: NOW + 9 * DAY, remaining_percent: 70 }),
  ]);
  assert.deepEqual(key, { resetAtMS: NOW + 3 * DAY, remainingPercent: 40 });

  assert.equal(earliestLongWindow([makeWindow({ id: 'short', kind: 'five_hour', period_hours: 5 })]), null);
  assert.equal(earliestLongWindow([makeWindow({ id: 'text-only', reset_at_ms: undefined })]), null);
});

test('the reset sort key breaks a tie by the tighter remaining share', () => {
  const key = earliestLongWindow([
    makeWindow({ id: 'b', reset_at_ms: NOW + DAY, remaining_percent: 70 }),
    makeWindow({ id: 'a', reset_at_ms: NOW + DAY, remaining_percent: 30 }),
  ]);
  assert.deepEqual(key, { resetAtMS: NOW + DAY, remainingPercent: 30 });
});

test('the fast-reset mark opens at exactly a third of the period', () => {
  assert.equal(isFastResetWindow({ period_hours: 24, reset_at_ms: NOW + 8 * HOUR }, NOW), true);
  assert.equal(isFastResetWindow({ period_hours: 24, reset_at_ms: NOW + 8 * HOUR + 1 }, NOW), false);
  assert.equal(isFastResetWindow({ period_hours: 24, reset_at_ms: NOW - 1 }, NOW), false);
  assert.equal(isFastResetWindow({ period_hours: undefined, reset_at_ms: NOW + HOUR }, NOW), false);
  assert.equal(isFastResetWindow({ period_hours: 0, reset_at_ms: NOW + HOUR }, NOW), false);
  assert.equal(isFastResetWindow({ period_hours: 5, reset_at_ms: NOW + HOUR }, NOW), true);
});

test('the fast-reset level splits at exactly 50% of the raw share', () => {
  const at = (remaining: number) => quotaFastResetState(
    { period_hours: 24, reset_at_ms: NOW + HOUR, remaining_percent: remaining },
    NOW,
  );
  assert.equal(at(50), 'high');
  assert.equal(at(49.9), 'low');
  assert.equal(quotaFastResetState(
    { period_hours: 24, reset_at_ms: NOW + HOUR, used_percent: 50 },
    NOW,
  ), 'high');
  assert.equal(quotaFastResetState({ period_hours: 24, reset_at_ms: NOW + HOUR }, NOW), null);
  assert.equal(quotaFastResetState(
    { period_hours: 24, reset_at_ms: NOW + 20 * HOUR, remaining_percent: 90 },
    NOW,
  ), null);
});

test('the reset line labels precision and a text-only boundary', () => {
  const reset = NOW + 3 * HOUR;
  const exact = quotaResetCountdownLabel({ reset_at_ms: reset, reset_accuracy: 'exact' }, NOW, t);
  assert.equal(exact, t('quota.reset_countdown_label', {
    time: formatCountdown(reset, NOW, t) ?? '',
    date: formatShortDateTime(reset),
  }));
  assert.ok(exact.includes(formatShortDateTime(reset)));

  const derived = quotaResetCountdownLabel({ reset_at_ms: reset, reset_accuracy: 'derived' }, NOW, t);
  assert.ok(derived.startsWith('~'));
  assert.ok(derived.endsWith(` · ${t('quota.reset_reference')}`));

  const approximate = quotaResetCountdownLabel({ reset_at_ms: reset, reset_accuracy: 'approximate' }, NOW, t);
  assert.ok(approximate.startsWith('~'));

  assert.equal(
    quotaResetCountdownLabel({ reset_label: 'later this week' }, NOW, t),
    t('quota.reset_time_unknown_label', { label: 'later this week' }),
  );
  assert.equal(
    quotaResetCountdownLabel({ reset_at_ms: NOW - 1, reset_accuracy: 'exact' }, NOW, t),
    t('quota.recovered'),
  );
  assert.equal(
    quotaResetCountdownLabel({ reset_at_ms: NOW - 1, reset_accuracy: 'derived', reset_label: 'upstream text' }, NOW, t),
    `~upstream text · ${t('quota.reset_reference')}`,
  );
  assert.equal(quotaResetCountdownLabel({}, NOW, t), '');
});

test('role and reason helpers cover every contract value and pass an unknown code through', () => {
  const roles = ['prefer', 'avoid', 'normal', 'balanced', 'limited', 'unavailable'] as const;
  assert.deepEqual(roles.map((role) => lendingRoleLabel(role, t)), roles.map((role) => `quota.lending.role.${role}`));
  assert.deepEqual(roles.map((role) => lendingRoleTone(role)),
    ['accent', 'warn', 'neutral', 'neutral', 'warn', 'neutral']);
  assert.equal(lendingReasonLabel('window_exhausted', t), 'quota.lending.reason.window_exhausted');
  assert.equal(lendingReasonLabel('future_code', t), 'future_code');
});

test('the detail view is projected from the same overview without a second request', () => {
  const lending: LendingOverview = {
    generated_at_ms: NOW,
    pools: [makePool([
      makeCard({ auth_index: 'a', name: 'A', remaining_percent: 88, reset_at_ms: NOW + 4 * DAY }),
      makeCard({ auth_index: 'b', name: 'B', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
    ])],
    unpooled_count: 1,
    unpooled: [{ auth_index: 'x', name: 'X', reason_codes: ['plan_type_unknown'] }],
  };

  const found = findLendingCard(lending, 'a', NOW);
  assert.equal(found?.pool.id, 'codex:pro');
  assert.equal(found?.card.role, 'prefer');
  assert.equal(findLendingCard(lending, 'x', NOW), null);

  const member = lendingDetailFor(lending, 'a', NOW);
  assert.equal(member?.pool?.id, 'codex:pro');
  assert.equal(member?.pool?.cards.find((card) => card.auth_index === 'a')?.role, 'prefer');
  assert.deepEqual(member?.unpooled_reason_codes, []);

  const unpooled = lendingDetailFor(lending, 'x', NOW);
  assert.equal(unpooled?.pool, null);
  assert.deepEqual(unpooled?.unpooled_reason_codes, ['plan_type_unknown']);

  assert.equal(lendingDetailFor(undefined, 'a', NOW), undefined);
});

test('the page basis is the oldest pool reading, or the overview generation time without one', () => {
  const overview = {
    generated_at_ms: NOW,
    pools: [
      makePool([makeCard({ auth_index: 'a', name: 'A' })], { observed_at_ms: NOW + DAY }),
      makePool([makeCard({ auth_index: 'b', name: 'B' })], { observed_at_ms: NOW + 2 * DAY }),
    ],
    unpooled_count: 0,
    unpooled: [],
  };
  assert.equal(lendingBasisMS(overview), NOW + DAY);

  const withoutReadings = {
    ...overview,
    pools: [makePool([makeCard({ auth_index: 'a', name: 'A' })], { observed_at_ms: undefined })],
  };
  assert.equal(lendingBasisMS(withoutReadings), NOW);
});

test('the header entry has three states and no entry when the field is absent', () => {
  assert.equal(lendingEntryState(undefined, NOW), null);

  const advised = {
    generated_at_ms: NOW,
    pools: [
      makePool([
        makeCard({ auth_index: 'a', name: 'A', remaining_percent: 88, reset_at_ms: NOW + 4 * DAY }),
        makeCard({ auth_index: 'b', name: 'B', remaining_percent: 40, reset_at_ms: NOW + 2 * DAY }),
      ]),
      makePool([
        makeCard({ auth_index: 'c', name: 'C', remaining_percent: 90, reset_at_ms: NOW + 4 * DAY }),
        makeCard({ auth_index: 'd', name: 'D', remaining_percent: 30, reset_at_ms: NOW + 2 * DAY }),
      ], { id: 'codex:team' }),
    ],
    unpooled_count: 0,
    unpooled: [],
  };
  assert.deepEqual(lendingEntryState(advised, NOW), { kind: 'advised', advisedCount: 2 });

  const balanced = {
    ...advised,
    pools: [makePool([
      makeCard({ auth_index: 'a', name: 'A', remaining_percent: 21.8, reset_at_ms: NOW + 2 * DAY }),
      makeCard({ auth_index: 'b', name: 'B', remaining_percent: 40, reset_at_ms: NOW + 4 * DAY }),
    ], { state: 'balanced' })],
  };
  assert.deepEqual(lendingEntryState(balanced, NOW), { kind: 'balanced', advisedCount: 0 });

  const limited = {
    ...advised,
    pools: [makePool([
      makeCard({ auth_index: 'a', name: 'A' }),
      makeCard({ auth_index: 'b', name: 'B', role: 'limited', is_comparable: false }),
    ], { state: 'limited' })],
  };
  assert.deepEqual(lendingEntryState(limited, NOW), { kind: 'plain', advisedCount: 0 });

  const stale = {
    ...advised,
    pools: [makePool([makeCard({ auth_index: 'a', name: 'A' })], {
      fresh_until_ms: NOW - 1,
      valid_until_ms: NOW + DAY,
    })],
  };
  assert.deepEqual(lendingEntryState(stale, NOW), { kind: 'plain', advisedCount: 0 });
});

test('the unpooled groups count each reason once per credential and keep the backend order by code', () => {
  assert.deepEqual(groupLendingUnpooled([]), []);

  const groups = groupLendingUnpooled([
    { auth_index: 'x', name: 'xai-primary', reason_codes: ['plan_type_unknown', 'no_long_window'] },
    { auth_index: 'y', name: 'claude-secondary', reason_codes: ['plan_type_unknown', 'plan_type_unknown'] },
  ]);
  assert.deepEqual(groups.map((group) => group.code), ['no_long_window', 'plan_type_unknown']);
  assert.deepEqual(groups.map((group) => group.members.map((member) => member.auth_index)), [
    ['x'],
    ['x', 'y'],
  ]);
});

