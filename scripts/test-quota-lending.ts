/**
 * Locks the lending layer's pure projections.
 *
 * Everything here reads what the backend already decided: the entry counts, the one sentence, the
 * hold grouping, the rank wording and the unpooled reasons. The first release recomputed roles and
 * daily figures against the page clock; that logic is gone (AC-23), so these assertions exist to
 * catch a console that starts inventing numbers again.
 */
import assert from 'node:assert/strict';
import test from 'node:test';
import type { TFunc } from '../web/src/i18n/index.tsx';
import type {
  LendingCard,
  LendingOutsideCard,
  LendingOverview,
  LendingPool,
} from '../web/src/types/quota.ts';
import {
  findLendingCard,
  groupLendingUnpooled,
  lendingBasisMS,
  lendingDetailFor,
  lendingEntryState,
  lendingHoldCounts,
  lendingHoldLabel,
  lendingHoldRowNote,
  lendingOutsidePoolLabel,
  lendingPoolHeadline,
  lendingPoolSentence,
  lendingPriorityText,
  lendingRankLabel,
  lendingReasonLabel,
} from '../web/src/pages/quota/lendingAdvice.ts';

const DAY = 86_400_000;
const NOW = Date.UTC(2026, 10, 23, 12, 0);

// The projections only need their keys and placeholders; the stub keeps this suite independent of
// the dictionary's copy.
const t = ((key: string, params?: Record<string, unknown>) => (
  params
    ? `${key}(${Object.keys(params).map((name) => `${name}=${String(params[name])}`).join(',')})`
    : key
)) as TFunc;

function makeCard(overrides: Partial<LendingCard> & Pick<LendingCard, 'auth_index' | 'name'>): LendingCard {
  return {
    display_name: overrides.name,
    role: 'suggested',
    reason_codes: [],
    is_comparable: true,
    remaining_percent: 50,
    reset_at_ms: NOW + 4 * DAY,
    reset_accuracy: 'exact',
    window_id: 'weekly',
    window_kind: 'weekly',
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    priority: 0,
    priority_known: true,
    priority_is_default: true,
    can_edit_priority: true,
    suggested_priority: 0,
    needs_change: false,
    ...overrides,
  };
}

function makeHold(overrides: Partial<LendingCard> & Pick<LendingCard, 'auth_index' | 'name'>): LendingCard {
  return makeCard({
    role: 'hold',
    priority: undefined,
    priority_known: false,
    priority_is_default: false,
    suggested_priority: undefined,
    needs_change: undefined,
    tier: undefined,
    ...overrides,
  });
}

function makePool(cards: LendingCard[], overrides: Partial<LendingPool> = {}): LendingPool {
  const suggested = cards.filter((entry) => entry.role === 'suggested');
  return {
    id: 'codex:pro',
    provider: 'codex',
    plan_type: 'pro',
    plan_label: 'Pro 200',
    window_kind: 'weekly',
    state: 'advised',
    valid_until_ms: NOW + 2 * DAY,
    observed_at_ms: NOW,
    fresh_until_ms: NOW + 7 * DAY,
    is_reference: false,
    is_balanced: false,
    needs_change_count: suggested.filter((entry) => entry.needs_change === true).length,
    hold_count: cards.filter((entry) => entry.role === 'hold').length,
    reason_codes: [],
    cards,
    outside_cards: [],
    ...overrides,
  };
}

const member = (authIndex: string, name: string, extra: Partial<LendingCard> = {}) => makeCard({
  auth_index: authIndex,
  name,
  display_name: name,
  ...extra,
});

test('a priority renders as the integer CPA accepts, and an absent one as an em dash', () => {
  assert.equal(lendingPriorityText(0), '0');
  assert.equal(lendingPriorityText(-10), '-10');
  assert.equal(lendingPriorityText(undefined), '—');
});

test('the pool headline counts the cards to change, or says none', () => {
  assert.equal(lendingPoolHeadline(makePool([member('a', 'A')], { needs_change_count: 2 }), t), 'quota.lending.pool_change(n=2)');
  assert.equal(lendingPoolHeadline(makePool([member('a', 'A')], { needs_change_count: 0 }), t), 'quota.lending.pool_keep');
  assert.equal(lendingPoolHeadline(makePool([], { state: 'no_advice' }), t), 'quota.lending.pool_no_advice');
});

test('the sentence names every changed card and every card left alone', () => {
  const oneKeep = makePool([
    member('a', 'A', { tier: 1, suggested_priority: 0, needs_change: false }),
    member('b', 'B', { tier: 2, suggested_priority: -10, needs_change: true }),
  ]);
  const single = lendingPoolSentence(oneKeep, t);
  assert.match(single.text, /quota\.lending\.say_set/);
  assert.match(single.text, /quota\.lending\.say_pair\(name=B,n=-10\)/);
  assert.match(single.text, /quota\.lending\.say_with_keep\(set=.*keep=A\)/);
  assert.equal(single.note, undefined);

  const manyKeeps = makePool([
    member('a', 'A', { tier: 1, suggested_priority: 0, needs_change: false }),
    member('b', 'B', { tier: 2, suggested_priority: 0, needs_change: false }),
    member('c', 'C', { tier: 3, suggested_priority: -10, needs_change: true }),
  ]);
  assert.match(lendingPoolSentence(manyKeeps, t).text, /quota\.lending\.say_with_rest\(set=.*names=A.*list_separator.*B\)/);

  const onlyChange = makePool([
    member('a', 'A', { tier: 1, suggested_priority: -10, needs_change: true }),
  ]);
  const only = lendingPoolSentence(onlyChange, t);
  assert.doesNotMatch(only.text, /say_with/);
  assert.equal(only.note, undefined);
});

test('an unreadable current number is never counted as "no change needed"', () => {
  const pool = makePool([
    member('a', 'A', { tier: 1, priority_known: false, priority: undefined, suggested_priority: 5, needs_change: undefined }),
    member('b', 'B', { tier: 2, suggested_priority: 5, needs_change: true }),
  ]);
  const sentence = lendingPoolSentence(pool, t);
  assert.match(sentence.text, /name=B,n=5/);
  assert.doesNotMatch(sentence.text, /say_with_keep|say_with_rest/);
});

test('a pool with nothing to change speaks about balance or about the current settings', () => {
  const balancedPair = makePool([
    member('a', 'A', { suggested_priority: 5 }),
    member('b', 'B', { suggested_priority: 0 }),
  ], { is_balanced: true });
  assert.equal(lendingPoolSentence(balancedPair, t).text, 'quota.lending.say_balanced_pair(n=2)');
  assert.equal(lendingPoolSentence(balancedPair, t).note, 'quota.lending.say_balanced_note');

  const balancedMany = makePool([
    member('a', 'A'), member('b', 'B'), member('c', 'C'),
  ], { is_balanced: true });
  assert.equal(lendingPoolSentence(balancedMany, t).text, 'quota.lending.say_balanced_other(n=3)');

  const plain = makePool([member('a', 'A')]);
  assert.equal(lendingPoolSentence(plain, t).text, 'quota.lending.say_keep');
});

test('hold cards add their count to the sentence note', () => {
  const pool = makePool([
    member('a', 'A', { suggested_priority: -10, needs_change: true }),
    makeHold({ auth_index: 'h', name: 'H', primary_reason: 'reading_failed', reason_codes: ['reading_failed'] }),
  ]);
  assert.equal(lendingPoolSentence(pool, t).note, 'quota.lending.say_hold_suffix(n=1)');
});

test('hold counts collapse by primary reason and follow the contract vocabulary order', () => {
  const pool = makePool([
    makeHold({ auth_index: 'h1', name: 'H1', primary_reason: 'remaining_below_ten', reason_codes: ['remaining_below_ten'] }),
    makeHold({ auth_index: 'h2', name: 'H2', primary_reason: 'window_exhausted', reason_codes: ['window_exhausted', 'remaining_below_ten'] }),
    makeHold({ auth_index: 'h3', name: 'H3', primary_reason: 'window_exhausted', reason_codes: ['window_exhausted'] }),
  ], { reason_codes: ['window_exhausted', 'remaining_below_ten'] });
  assert.deepEqual(lendingHoldCounts(pool), [
    { code: 'window_exhausted', count: 2 },
    { code: 'remaining_below_ten', count: 1 },
  ]);

  const unknownOrder = makePool([
    makeHold({ auth_index: 'h1', name: 'H1', primary_reason: 'future_code', reason_codes: ['future_code'] }),
    makeHold({ auth_index: 'h2', name: 'H2', primary_reason: 'reading_failed', reason_codes: ['reading_failed'] }),
  ], { reason_codes: ['reading_failed'] });
  assert.deepEqual(lendingHoldCounts(unknownOrder).map((entry) => entry.code), ['reading_failed', 'future_code']);
});

test('rank wording shares one position across a tier and marks the tie', () => {
  const pool = makePool([member('a', 'A', { tier: 2 })]);
  assert.equal(lendingRankLabel(pool, pool.cards[0], t), 'quota.lending.rank_n(n=2)');

  const tied = makePool([
    member('a', 'A', { tier: 1 }),
    member('b', 'B', { tier: 1 }),
    member('c', 'C', { tier: 2 }),
  ]);
  assert.equal(lendingRankLabel(tied, tied.cards[0], t), 'quota.lending.rank_tied(n=1)');
  assert.equal(lendingRankLabel(tied, tied.cards[1], t), 'quota.lending.rank_tied(n=1)');
  assert.equal(lendingRankLabel(tied, tied.cards[2], t), 'quota.lending.rank_n(n=2)');

  const untiered = makePool([member('a', 'A', { tier: undefined })]);
  assert.equal(lendingRankLabel(untiered, untiered.cards[0], t), 'quota.lending.rank_same');
});

test('an outside card names its pool label, falling back to its provider', () => {
  const withPool: LendingOutsideCard = {
    auth_index: 'o1', name: 'O1', display_name: 'O1', provider: 'codex', pool_label: 'codex · Team',
    priority: 0, priority_known: true, priority_is_default: true,
  };
  const withoutPool: LendingOutsideCard = {
    auth_index: 'o2', name: 'O2', display_name: 'O2', provider: 'claude',
    priority_known: false, priority_is_default: false,
  };
  assert.equal(lendingOutsidePoolLabel(withPool), 'codex · Team');
  assert.equal(lendingOutsidePoolLabel(withoutPool), 'claude');
});

test('hold labels cover the fixed vocabulary and pass an unknown code through', () => {
  assert.equal(lendingHoldLabel('needs_reauth', t), 'quota.lending.hold.needs_reauth');
  assert.equal(lendingHoldLabel('future_code', t), 'future_code');
  assert.equal(lendingHoldLabel(undefined, t), 'quota.lending.hold.unknown');
});

test('an expanded hold row states a passed refill, a short age, and the low-window share', () => {
  const stale = makeHold({
    auth_index: 'stale', name: 'Stale', primary_reason: 'reading_stale', reason_codes: ['reading_stale'],
  });
  const context = { generatedAtMS: NOW, nowMS: NOW };

  assert.equal(
    lendingHoldRowNote({ ...stale, reset_at_ms: NOW - 1, observed_at_ms: NOW - 26 * 3_600_000 }, t, context),
    'quota.lending.hold_note.reading_stale_expired',
  );
  assert.equal(
    lendingHoldRowNote({ ...stale, reset_at_ms: NOW + DAY, observed_at_ms: NOW - 26 * 3_600_000 }, t, context),
    'quota.lending.hold_note.reading_stale(n=26)',
  );
  assert.equal(
    lendingHoldRowNote({ ...stale, reset_at_ms: NOW + DAY, observed_at_ms: NOW - 60_000 }, t, context),
    'quota.lending.hold_note.reading_stale_minutes(n=1)',
  );
  assert.equal(
    lendingHoldRowNote({ ...stale, reset_at_ms: NOW + DAY, observed_at_ms: undefined }, t, context),
    'quota.lending.hold_note.reading_stale_unknown',
  );

  const low = makeHold({
    auth_index: 'low', name: 'Low', primary_reason: 'remaining_below_ten', reason_codes: ['remaining_below_ten'],
    remaining_percent: 6, window_kind: 'weekly',
  });
  assert.equal(
    lendingHoldRowNote(low, t, { ...context, windowLabel: 'quota.window_weekly' }),
    'quota.lending.hold_note.remaining_below_ten_window(window=quota.window_weekly,n=6)',
  );
  assert.equal(lendingHoldRowNote(low, t, context), 'quota.lending.hold_note.remaining_below_ten');
  assert.equal(
    lendingHoldRowNote({ ...low, remaining_percent: undefined }, t, { ...context, windowLabel: 'quota.window_weekly' }),
    'quota.lending.hold_note.remaining_below_ten',
  );
  assert.equal(
    lendingHoldRowNote({ ...low, window_kind: undefined }, t, context),
    'quota.lending.hold_note.remaining_below_ten',
  );
  assert.equal(lendingHoldRowNote({ ...stale, primary_reason: 'future_code' }, t, context), undefined);
  assert.equal(lendingHoldRowNote({ ...stale, primary_reason: undefined }, t, context), undefined);
});

test('unpooled reasons keep the three documented sentences and pass an unknown code through', () => {
  assert.equal(lendingReasonLabel('plan_type_unknown', t), 'quota.lending.reason.plan_type_unknown');
  assert.equal(lendingReasonLabel('pool_too_small', t), 'quota.lending.reason.pool_too_small');
  assert.equal(lendingReasonLabel('credential_disabled', t), 'quota.lending.reason.credential_disabled');
  assert.equal(lendingReasonLabel('future_code', t), 'future_code');
});

test('the entry count is the sum of needs_change_count and its state comes from the pools', () => {
  assert.equal(lendingEntryState(undefined), null);
  assert.equal(lendingEntryState(null), null);

  const overview: LendingOverview = {
    generated_at_ms: NOW,
    pools: [
      makePool([member('a', 'A', { needs_change: true })], { needs_change_count: 1 }),
      makePool([member('c', 'C', { needs_change: true })], { id: 'codex:team', needs_change_count: 1 }),
    ],
    unpooled_count: 0,
    unpooled: [],
  };
  assert.deepEqual(lendingEntryState(overview), { kind: 'change', changeCount: 2 });

  assert.deepEqual(
    lendingEntryState({ ...overview, pools: [makePool([member('a', 'A')], { needs_change_count: 0 })] }),
    { kind: 'keep', changeCount: 0 },
  );
  assert.deepEqual(
    lendingEntryState({ ...overview, pools: [makePool([], { state: 'no_advice', needs_change_count: 0 })] }),
    { kind: 'plain', changeCount: 0 },
  );
  assert.deepEqual(lendingEntryState({ generated_at_ms: NOW, pools: [], unpooled_count: 0, unpooled: [] }), { kind: 'plain', changeCount: 0 });
});

test('a credential lookup finds its card and its detail projection without a second request', () => {
  const pool = makePool([
    member('a', 'A'),
    makeHold({ auth_index: 'h', name: 'H', primary_reason: 'reading_failed', reason_codes: ['reading_failed'] }),
  ]);
  const overview: LendingOverview = {
    generated_at_ms: NOW,
    pools: [pool],
    unpooled_count: 1,
    unpooled: [{ auth_index: 'x', name: 'X', reason_codes: ['plan_type_unknown'] }],
  };

  assert.equal(findLendingCard(overview, 'a')?.card.auth_index, 'a');
  assert.equal(findLendingCard(overview, 'h')?.card.role, 'hold');
  assert.equal(findLendingCard(overview, 'x'), null);

  const inside = lendingDetailFor(overview, 'a');
  assert.equal(inside?.pool?.id, 'codex:pro');
  assert.deepEqual(inside?.unpooled_reason_codes, []);

  const outside = lendingDetailFor(overview, 'x');
  assert.equal(outside?.pool, null);
  assert.deepEqual(outside?.unpooled_reason_codes, ['plan_type_unknown']);

  assert.equal(lendingDetailFor(null, 'a'), undefined);
});

test('the page basis is the oldest pool reading, or the overview generation time without one', () => {
  const overview: LendingOverview = {
    generated_at_ms: NOW,
    pools: [
      makePool([member('a', 'A')], { observed_at_ms: NOW + DAY }),
      makePool([member('b', 'B')], { observed_at_ms: NOW + 2 * DAY }),
    ],
    unpooled_count: 0,
    unpooled: [],
  };
  assert.equal(lendingBasisMS(overview), NOW + DAY);

  const withoutReadings: LendingOverview = {
    ...overview,
    pools: [makePool([member('a', 'A')], { observed_at_ms: undefined })],
  };
  assert.equal(lendingBasisMS(withoutReadings), NOW);
});

test('the unpooled groups count each reason once per credential and stay ordered by code', () => {
  assert.deepEqual(groupLendingUnpooled([]), []);

  const groups = groupLendingUnpooled([
    { auth_index: 'x', name: 'xai-primary', reason_codes: ['plan_type_unknown', 'no_long_window'] },
    { auth_index: 'y', name: 'claude-secondary', reason_codes: ['plan_type_unknown', 'plan_type_unknown'] },
  ]);
  assert.deepEqual(groups.map((group) => group.code), ['no_long_window', 'plan_type_unknown']);
  assert.deepEqual(groups.map((group) => group.members.map((entry) => entry.auth_index)), [
    ['x'],
    ['x', 'y'],
  ]);
});
