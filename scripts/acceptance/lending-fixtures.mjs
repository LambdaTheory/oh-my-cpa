/**
 * Acceptance fixtures for the cross-credential lending advice.
 *
 * One fixture definition feeds two surfaces so they cannot drift:
 *
 *   - `probeLending(state)` is the `/management/quota.lending` body the browser
 *     probes serve, shaped by the second-version contract: the backend decides the
 *     order, the tier, the suggested number and whether a card needs a change, and
 *     the page only renders them.
 *   - `createCpaLendingFixture(name)` is the fake CPA side: the Codex credentials
 *     plus the `/requests/api-call` payloads the real backend reads, so the same
 *     states can be seen against a built binary.
 *
 * The states cover what the page has to say: a change, no change, a pool whose
 * numbers are close enough to leave alone, cards held back with a reason, a pool
 * with no ranking at all, same-model cards outside the pool, an unreadable current
 * number, a priority this console cannot write, and a name long enough to need a
 * middle ellipsis. Nothing here is product data and nothing outside a test imports it.
 */

export const DAY_MS = 86_400_000;
const WEEK_SECONDS = 604800;
const MONTH_SECONDS = 2592000;

function freshWindowMs(periodSeconds) {
  const quarter = (periodSeconds / 4) * 1000;
  return Math.min(Math.max(quarter, 2 * 3_600_000), 24 * 3_600_000);
}

/**
 * One card exactly as the second-version projection emits it.
 *
 * A suggested card carries a tier, a suggested number and a change flag; a held
 * card carries its reasons and never a suggested number. `priority` may be absent
 * while `priority_known` stays false, which is how the page tells a missing reading
 * apart from a zero.
 */
function card(authIndex, displayName, options = {}) {
  const {
    name = displayName,
    now = Date.now(),
    role = 'suggested',
    tier,
    priority = 0,
    priorityKnown = true,
    priorityDefault = priority === 0,
    canEdit = true,
    suggested,
    needsChange,
    remaining,
    resetSeconds = 4 * DAY_MS / 1000,
    resetAtMS,
    accuracy = 'exact',
    observedAgoSeconds = 0,
    observedAtMS,
    windowKind = 'weekly',
    periodSeconds = WEEK_SECONDS,
    reasons = [],
    primaryReason,
  } = options;
  const observedAt = observedAtMS ?? now - observedAgoSeconds * 1000;
  const resetAt = resetAtMS ?? (resetSeconds == null ? undefined : now + resetSeconds * 1000);
  const hold = role === 'hold';
  return {
    auth_index: authIndex,
    name,
    display_name: displayName,
    role,
    tier: hold ? undefined : tier,
    window_id: `${authIndex}-${windowKind}`,
    window_kind: windowKind,
    remaining_percent: remaining,
    reset_at_ms: resetAt,
    reset_accuracy: accuracy,
    observed_at_ms: observedAt,
    fresh_until_ms: observedAt + freshWindowMs(periodSeconds),
    priority: priorityKnown ? priority : undefined,
    priority_known: priorityKnown,
    priority_is_default: priorityKnown ? priorityDefault : false,
    can_edit_priority: canEdit,
    suggested_priority: hold ? undefined : suggested,
    needs_change: hold ? undefined : needsChange,
    primary_reason: hold ? (primaryReason ?? reasons[0]) : undefined,
    reason_codes: hold ? reasons : [],
    is_comparable: !hold,
  };
}

function outsideCard(authIndex, displayName, options = {}) {
  const {
    provider = 'codex',
    poolId,
    poolLabel,
    priority = 0,
    priorityKnown = true,
    priorityDefault = true,
  } = options;
  return {
    auth_index: authIndex,
    name: displayName,
    display_name: displayName,
    provider,
    pool_id: poolId,
    pool_label: poolLabel,
    priority: priorityKnown ? priority : undefined,
    priority_known: priorityKnown,
    priority_is_default: priorityKnown ? priorityDefault : false,
  };
}

function pool({ state = 'advised', cards, outside = [], reasons = [], validUntil, isReference = false, isBalanced = false, windowKind = 'weekly', plan = 'pro', label = 'Pro 200', id, provider = 'codex' }) {
  const observed = cards
    .map((entry) => entry.observed_at_ms)
    .filter((value) => value != null)
    .sort((left, right) => left - right)[0];
  const fresh = cards
    .map((entry) => entry.fresh_until_ms)
    .filter((value) => value != null)
    .sort((left, right) => left - right)[0];
  const suggested = cards.filter((entry) => entry.role === 'suggested');
  const earliestReset = suggested
    .map((entry) => entry.reset_at_ms)
    .filter((value) => value != null)
    .sort((left, right) => left - right)[0];
  return {
    id: id ?? `${provider}:${plan}`,
    provider,
    plan_type: plan,
    plan_label: label,
    window_kind: state === 'no_advice' ? '' : windowKind,
    state,
    valid_until_ms: state === 'no_advice' ? undefined : (validUntil ?? earliestReset),
    observed_at_ms: observed,
    fresh_until_ms: fresh,
    is_reference: isReference,
    is_balanced: isBalanced,
    needs_change_count: suggested.filter((entry) => entry.needs_change === true).length,
    hold_count: cards.filter((entry) => entry.role === 'hold').length,
    reason_codes: reasons,
    cards,
    outside_cards: outside,
  };
}

function overview(pools, unpooled = [], now) {
  return {
    generated_at_ms: now,
    pools,
    unpooled_count: unpooled.length,
    unpooled,
  };
}

// The two cards from the operator's capture: A resets 4 days 7h 24m out at 88%,
// B resets 2 days 2h 26m out at 40%. Every relative variant below keeps these
// offsets so the "about 4 days / about 2 days" shape survives reseeding.
const HERO_A_RESET = 4 * 86_400 + 7 * 3_600 + 24 * 60;
const HERO_B_RESET = 2 * 86_400 + 2 * 3_600 + 26 * 60;

function unpooledCard(authIndex, displayName, reasons) {
  return { auth_index: authIndex, name: `${displayName}.json`, reason_codes: reasons };
}

/**
 * The lending body for one named UI state.
 *
 * `null` is the old-backend shape (`lending: null`); a missing key is served by a
 * separate route and is not expressed here.
 */
export function probeLending(state, now = Date.now(), options = {}) {
  const base = { now };
  const pageNow = options.pageNow ?? now;
  switch (state) {
    case 'base': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 88, resetSeconds: 3.9 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 2, priority: 0, suggested: -10, needsChange: true,
        remaining: 40, resetSeconds: 2.3 * 86_400,
      });
      return overview([pool({ cards: [a, b], validUntil: b.reset_at_ms })], [
        { auth_index: 'auth-03', name: 'credential-03', reason_codes: ['plan_type_unknown'] },
        { auth_index: 'auth-04', name: 'credential-04', reason_codes: ['pool_too_small'] },
      ], now);
    }
    case 'keep': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 10, priorityDefault: false, suggested: 10, needsChange: false,
        remaining: 88, resetSeconds: 3.9 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 2, priority: 0, suggested: 0, needsChange: false,
        remaining: 40, resetSeconds: 2.3 * 86_400,
      });
      return overview([pool({ cards: [a, b], validUntil: b.reset_at_ms })], [], now);
    }
    case 'balanced': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 10, priorityDefault: false, suggested: 10, needsChange: false,
        remaining: 60, resetSeconds: 4 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 58, resetSeconds: 4 * 86_400,
      });
      return overview([pool({ cards: [a, b], validUntil: a.reset_at_ms, isBalanced: true })], [], now);
    }
    case 'hold': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 10, priorityDefault: false, suggested: 10, needsChange: false,
        remaining: 70, resetSeconds: 3 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 2, priority: 0, suggested: -10, needsChange: true,
        remaining: 40, resetSeconds: 2 * 86_400,
      });
      const c = card('auth-03', 'credential-03', {
        ...base, role: 'hold', priorityKnown: false, remaining: 40, resetSeconds: 2 * 86_400,
        reasons: ['window_exhausted'],
      });
      return overview([pool({ cards: [a, b, c], validUntil: b.reset_at_ms })], [], now);
    }
    case 'noAdvice': {
      const a = card('auth-01', 'credential-01', {
        ...base, role: 'hold', remaining: 40, resetSeconds: 2 * 86_400, reasons: ['window_exhausted'],
      });
      const b = card('auth-02', 'credential-02', {
        ...base, role: 'hold', remaining: 40, resetSeconds: 3 * 86_400, reasons: ['reading_failed'],
      });
      return overview([pool({ state: 'no_advice', cards: [a, b] })], [], now);
    }
    case 'outside': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 10, priorityDefault: false, suggested: 10, needsChange: false,
        remaining: 70, resetSeconds: 3 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 2, priority: 0, suggested: -10, needsChange: true,
        remaining: 40, resetSeconds: 2 * 86_400,
      });
      return overview([pool({
        cards: [a, b],
        validUntil: b.reset_at_ms,
        outside: [
          outsideCard('auth-05', 'credential-05', { poolId: 'codex:team', poolLabel: 'codex · Team 300' }),
          outsideCard('auth-06', 'credential-06', { provider: 'claude', priorityKnown: false }),
        ],
      })], [], now);
    }
    case 'unknown': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priorityKnown: false, suggested: 5,
        remaining: 70, resetSeconds: 3 * 86_400,
      });
      return overview([pool({ cards: [a], validUntil: a.reset_at_ms })], [], now);
    }
    case 'cannotEdit': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, suggested: -10, needsChange: true, canEdit: false,
        remaining: 70, resetSeconds: 3 * 86_400,
      });
      return overview([pool({ cards: [a], validUntil: a.reset_at_ms })], [], now);
    }
    case 'longName': {
      const a = card('auth-01', 'synthetic-credential-with-a-very-long-name', {
        ...base, name: 'credential-long.json', tier: 1, suggested: 0, needsChange: false,
        remaining: 70, resetSeconds: 3 * 86_400,
      });
      return overview([pool({ cards: [a], validUntil: a.reset_at_ms })], [], now);
    }
    case 'limited': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 40, resetSeconds: 2 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, role: 'hold', remaining: 5, resetSeconds: 30 * 86_400, reasons: ['remaining_below_ten'],
      });
      return overview([pool({ cards: [a, b], validUntil: a.reset_at_ms })], [], now);
    }
    case 'restricted': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 10, priorityDefault: false, suggested: 10, needsChange: false,
        remaining: 40, resetSeconds: 2 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 2, priority: 0, suggested: -10, needsChange: true,
        remaining: 30, resetSeconds: 3 * 86_400,
      });
      const c = card('auth-03', 'credential-03', {
        ...base, role: 'hold', remaining: 5, resetSeconds: 30 * 86_400, reasons: ['remaining_below_ten'],
      });
      return overview([pool({ cards: [a, b, c], validUntil: a.reset_at_ms })], [], now);
    }
    case 'textOnly': {
      const a = card('auth-01', 'credential-01', {
        ...base, role: 'hold', remaining: 55, resetSeconds: null, reasons: ['reset_time_unknown'],
      });
      const b = card('auth-02', 'credential-02', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400, reasons: ['reset_time_unknown'],
      });
      return overview([pool({ state: 'no_advice', cards: [a, b], reasons: ['reset_time_unknown'] })], [], now);
    }
    case 'stale': {
      const a = card('auth-01', 'credential-01', {
        ...base, role: 'hold', remaining: 70, resetSeconds: 3 * 86_400,
        observedAgoSeconds: 25 * 3_600, reasons: ['reading_stale'],
      });
      const b = card('auth-02', 'credential-02', {
        ...base, role: 'hold', remaining: 65, resetSeconds: -60, reasons: ['reading_stale'],
      });
      const c = card('auth-03', 'credential-03', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400,
        observedAgoSeconds: 60, reasons: ['reading_stale'],
      });
      return overview([pool({ state: 'no_advice', cards: [a, b, c], reasons: ['reading_stale'] })], [], now);
    }
    case 'noLong': {
      const a = card('auth-01', 'credential-01', {
        ...base, role: 'hold', remaining: 40, resetSeconds: null, reasons: ['no_long_window'],
      });
      const b = card('auth-02', 'credential-02', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400, reasons: ['no_long_window'],
      });
      return overview([pool({ state: 'no_advice', cards: [a, b], reasons: ['no_long_window'] })], [], now);
    }
    case 'windowMismatch': {
      const a = card('auth-01', 'credential-01', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400, reasons: ['window_type_mismatch'],
      });
      const b = card('auth-02', 'credential-02', {
        ...base, role: 'hold', remaining: 70, resetSeconds: 10 * 86_400, windowKind: 'monthly', reasons: ['window_type_mismatch'],
      });
      return overview([pool({ state: 'no_advice', cards: [a, b], reasons: ['window_type_mismatch'] })], [], now);
    }
    case 'subHour': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 10, resetSeconds: 90,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 2, priority: 0, suggested: -10, needsChange: true,
        remaining: 60, resetSeconds: 4 * 86_400,
      });
      return overview([pool({ cards: [a, b], validUntil: a.reset_at_ms })], [], now);
    }
    case 'derived': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 10, priorityDefault: false, suggested: 10, needsChange: false,
        remaining: 50, resetSeconds: 4 * 86_400, accuracy: 'derived',
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 49, resetSeconds: 4 * 86_400, accuracy: 'derived',
      });
      return overview([pool({ cards: [a, b], validUntil: a.reset_at_ms, isBalanced: true, isReference: true })], [], now);
    }
    case 'twoPools': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 88, resetSeconds: 4 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, tier: 2, priority: 0, suggested: -10, needsChange: true,
        remaining: 40, resetSeconds: 2.3 * 86_400,
      });
      const c = card('auth-03', 'credential-03', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 70, resetSeconds: 1 * 86_400,
      });
      const d = card('auth-04', 'credential-04', {
        ...base, tier: 2, priority: 0, suggested: -10, needsChange: true,
        remaining: 30, resetSeconds: 0.5 * 86_400,
      });
      // The backend sorts pools by their soonest reset; this payload mirrors that order so the
      // page can be checked for keeping it, not for inventing its own.
      return overview([
        pool({ cards: [c, d], validUntil: d.reset_at_ms, plan: 'team', label: 'Team 300' }),
        pool({ cards: [a, b], validUntil: b.reset_at_ms }),
      ], [], now);
    }
    case 'missingVsZero': {
      const a = card('auth-01', 'credential-01', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 0, resetSeconds: 2 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, role: 'hold', remaining: undefined, resetSeconds: 2 * 86_400, reasons: ['remaining_unknown'],
      });
      return overview([pool({ cards: [a, b], validUntil: a.reset_at_ms })], [], now);
    }
    case 'mixed': {
      const a = card('auth-01', 'credential-01', {
        ...base, priority: 10, priorityDefault: false, suggested: 10, needsChange: false,
        remaining: 60, resetSeconds: 4 * 86_400,
      });
      const b = card('auth-02', 'credential-02', {
        ...base, priority: 0, suggested: 0, needsChange: false,
        remaining: 58, resetSeconds: 4 * 86_400,
      });
      const c = card('auth-03', 'credential-03', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400, reasons: ['window_type_mismatch'],
      });
      const d = card('auth-04', 'credential-04', {
        ...base, role: 'hold', remaining: 70, resetSeconds: 10 * 86_400, windowKind: 'monthly', reasons: ['window_type_mismatch'],
      });
      return overview([
        pool({ cards: [a, b], validUntil: a.reset_at_ms, isBalanced: true }),
        pool({ state: 'no_advice', cards: [c, d], reasons: ['window_type_mismatch'], plan: 'team', label: 'Team 300' }),
      ], [], now);
    }
    case 'rev-hero': {
      const a = card('auth-hero-a', 'codex-demo-a', {
        ...base, tier: 1, priority: 0, suggested: 0, needsChange: false,
        remaining: 88, resetSeconds: HERO_A_RESET,
      });
      const b = card('auth-hero-b', 'codex-demo-b', {
        ...base, tier: 2, priority: 0, suggested: -10, needsChange: true,
        remaining: 40, resetSeconds: HERO_B_RESET,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-hero-keep': {
      const a = card('auth-hero-a', 'codex-demo-a', {
        ...base, tier: 1, priority: 10, suggested: 10, needsChange: false,
        remaining: 88, resetSeconds: HERO_A_RESET,
      });
      const b = card('auth-hero-b', 'codex-demo-b', {
        ...base, tier: 2, priority: 0, suggested: 0, needsChange: false,
        remaining: 40, resetSeconds: HERO_B_RESET,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-two-tier': {
      // N-2's twelve rows only vary the current numbers; the case supplies the
      // tier/suggestion/verdict it expects so the fixture stays input-only.
      const [priorityA, priorityB] = options.priorities ?? [0, 0];
      const [suggestedA, suggestedB] = options.suggested ?? [0, -10];
      const [changeA, changeB] = options.change ?? [false, true];
      const a = card('auth-two-a', 'codex-demo-a', {
        ...base, tier: 1, priority: priorityA, suggested: suggestedA, needsChange: changeA,
        remaining: 88, resetSeconds: HERO_A_RESET,
      });
      const b = card('auth-two-b', 'codex-demo-b', {
        ...base, tier: 2, priority: priorityB, suggested: suggestedB, needsChange: changeB,
        remaining: 40, resetSeconds: HERO_B_RESET,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-fast-high': {
      const a = card('auth-fast-high-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 30, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-fast-high-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 95, resetSeconds: 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-same-pct': {
      const a = card('auth-same-pct-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 60, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-same-pct-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 60, resetSeconds: 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-near': {
      const a = card('auth-near-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 70, resetSeconds: 3 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-near-b', 'codex-demo-b', {
        ...base, tier: 1, remaining: 50, resetSeconds: 3.5 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      return overview([pool({ cards: [a, b], isBalanced: true })], [], pageNow);
    }
    case 'rev-gap': {
      // N-7's six boundary rows: options.offsetSeconds is A's reset distance from
      // B (null means the same instant), options.priorities feeds the last row.
      const [priorityA, priorityB] = options.priorities ?? [0, 0];
      const offset = options.offsetSeconds ?? 24 * 3_600;
      const a = card('auth-gap-a', 'codex-demo-a', {
        ...base, tier: offset < 86_400 ? 1 : 1, remaining: 88,
        resetSeconds: HERO_B_RESET + offset,
        priority: priorityA, suggested: options.suggested?.[0] ?? 0,
        needsChange: options.change?.[0] ?? false,
      });
      const b = card('auth-gap-b', 'codex-demo-b', {
        ...base, tier: offset < 86_400 ? 1 : 2, remaining: 40,
        resetSeconds: HERO_B_RESET,
        priority: priorityB, suggested: options.suggested?.[1] ?? -10,
        needsChange: options.change?.[1] ?? true,
      });
      const sameTier = offset < 86_400;
      return overview([pool({ cards: [a, b], isBalanced: sameTier })], [], pageNow);
    }
    case 'rev-twin': {
      const a = card('auth-twin-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 60, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-twin-b', 'codex-demo-b', {
        ...base, tier: 1, remaining: 60, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      return overview([pool({ cards: [a, b], isBalanced: true })], [], pageNow);
    }
    case 'rev-derived-2': {
      const a = card('auth-derived2-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 60, resetSeconds: 5 * 86_400, accuracy: 'derived',
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-derived2-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: 2 * 86_400, accuracy: 'derived',
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [a, b], isReference: true })], [], pageNow);
    }
    case 'rev-three': {
      const [nowX, nowY, nowZ] = options.priorities ?? [0, 0, 0];
      const [sugX, sugY, sugZ] = options.suggested ?? [0, -10, -20];
      const [changeX, changeY, changeZ] = options.change ?? [false, true, true];
      const x = card('auth-three-x', 'codex-demo-x', {
        ...base, tier: 1, remaining: 70, resetSeconds: 5 * 86_400,
        priority: nowX, suggested: sugX, needsChange: changeX,
      });
      const y = card('auth-three-y', 'codex-demo-y', {
        ...base, tier: 2, remaining: 55, resetSeconds: 3 * 86_400,
        priority: nowY, suggested: sugY, needsChange: changeY,
      });
      const z = card('auth-three-z', 'codex-demo-z', {
        ...base, tier: 3, remaining: 35, resetSeconds: 86_400,
        priority: nowZ, suggested: sugZ, needsChange: changeZ,
      });
      return overview([pool({ cards: [x, y, z] })], [], pageNow);
    }
    case 'rev-four': {
      const [nowW, nowX, nowY, nowZ] = options.priorities ?? [0, 0, 0, 0];
      const [sugW, sugX, sugY, sugZ] = options.suggested ?? [0, -10, -20, -30];
      const change = options.change ?? [false, true, true, true];
      const w = card('auth-four-w', 'codex-demo-w', {
        ...base, tier: 1, remaining: 80, resetSeconds: 6 * 86_400,
        priority: nowW, suggested: sugW, needsChange: change[0],
      });
      const x = card('auth-four-x', 'codex-demo-x', {
        ...base, tier: 2, remaining: 65, resetSeconds: 4 * 86_400,
        priority: nowX, suggested: sugX, needsChange: change[1],
      });
      const y = card('auth-four-y', 'codex-demo-y', {
        ...base, tier: 3, remaining: 50, resetSeconds: 2.5 * 86_400,
        priority: nowY, suggested: sugY, needsChange: change[2],
      });
      const z = card('auth-four-z', 'codex-demo-z', {
        ...base, tier: 4, remaining: 35, resetSeconds: 86_400,
        priority: nowZ, suggested: sugZ, needsChange: change[3],
      });
      return overview([pool({ cards: [w, x, y, z] })], [], pageNow);
    }
    case 'rev-tier-mix': {
      const [nowX, nowY, nowZ] = options.priorities ?? [0, 0, 0];
      const x = card('auth-tier-mix-x', 'codex-demo-x', {
        ...base, tier: 1, remaining: 70, resetSeconds: 5.0 * 86_400,
        priority: nowX, suggested: options.suggested?.[0] ?? 0,
        needsChange: options.change?.[0] ?? false,
      });
      const y = card('auth-tier-mix-y', 'codex-demo-y', {
        ...base, tier: 1, remaining: 55, resetSeconds: 4.5 * 86_400,
        priority: nowY, suggested: options.suggested?.[1] ?? 0,
        needsChange: options.change?.[1] ?? false,
      });
      const z = card('auth-tier-mix-z', 'codex-demo-z', {
        ...base, tier: 2, remaining: 35, resetSeconds: 2.0 * 86_400,
        priority: nowZ, suggested: options.suggested?.[2] ?? -10,
        needsChange: options.change?.[2] ?? true,
      });
      return overview([pool({ cards: [x, y, z] })], [], pageNow);
    }
    case 'rev-chain': {
      // N-8 is blocked on P-01; this scene records the adjacent-tier reading the
      // fixture list names, not a settled expectation.
      const x = card('auth-chain-x', 'codex-demo-x', {
        ...base, tier: 1, remaining: 70, resetSeconds: 3.0 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const y = card('auth-chain-y', 'codex-demo-y', {
        ...base, tier: 1, remaining: 55, resetSeconds: 2.4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const z = card('auth-chain-z', 'codex-demo-z', {
        ...base, tier: 2, remaining: 40, resetSeconds: 1.8 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [x, y, z] })], [], pageNow);
    }
    case 'rev-dual-window': {
      // C's suggestion window is the monthly one it drains faster (30% left),
      // not its weekly window, so both cards are monthly and D never shares a board
      // with C's weekly reading.
      const c = card('auth-dual-c', 'codex-demo-c', {
        ...base, tier: 1, remaining: 30, resetSeconds: 12 * 86_400, windowKind: 'monthly',
        priority: 0, suggested: 0, needsChange: false,
      });
      const d = card('auth-dual-d', 'codex-demo-d', {
        ...base, tier: 2, remaining: 70, resetSeconds: 9 * 86_400, windowKind: 'monthly',
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [c, d], windowKind: 'monthly' })], [], pageNow);
    }
    case 'rev-mixed-type-1': {
      const x = card('auth-mixed1-x', 'codex-demo-x', {
        ...base, tier: 1, remaining: 70, resetSeconds: 5 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const y = card('auth-mixed1-y', 'codex-demo-y', {
        ...base, tier: 2, remaining: 55, resetSeconds: 3 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const z = card('auth-mixed1-z', 'codex-demo-z', {
        ...base, tier: 3, remaining: 35, resetSeconds: 86_400,
        priority: 0, suggested: -20, needsChange: true,
      });
      const m = card('auth-mixed1-m', 'codex-demo-m', {
        ...base, role: 'hold', remaining: 80, resetSeconds: 12 * 86_400, windowKind: 'monthly',
        priority: 0, reasons: ['window_type_mismatch'],
      });
      return overview([pool({ cards: [x, y, z, m] })], [], pageNow);
    }
    case 'rev-mixed-type-2a': {
      const w1 = card('auth-mixed2a-w1', 'codex-demo-w1', {
        ...base, tier: 2, remaining: 60, resetSeconds: 3 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const w2 = card('auth-mixed2a-w2', 'codex-demo-w2', {
        ...base, tier: 1, remaining: 60, resetSeconds: 5 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const m1 = card('auth-mixed2a-m1', 'codex-demo-m1', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 10 * 86_400, windowKind: 'monthly',
        priority: 0, reasons: ['window_type_mismatch'],
      });
      const m2 = card('auth-mixed2a-m2', 'codex-demo-m2', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 12 * 86_400, windowKind: 'monthly',
        priority: 0, reasons: ['window_type_mismatch'],
      });
      return overview([pool({ cards: [w2, w1, m1, m2] })], [], pageNow);
    }
    case 'rev-mixed-type-2b': {
      const m1 = card('auth-mixed2b-m1', 'codex-demo-m1', {
        ...base, tier: 2, remaining: 60, resetSeconds: 2 * 86_400, windowKind: 'monthly',
        priority: 0, suggested: -10, needsChange: true,
      });
      const m2 = card('auth-mixed2b-m2', 'codex-demo-m2', {
        ...base, tier: 1, remaining: 60, resetSeconds: 4 * 86_400, windowKind: 'monthly',
        priority: 0, suggested: 0, needsChange: false,
      });
      const w1 = card('auth-mixed2b-w1', 'codex-demo-w1', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400,
        priority: 0, reasons: ['window_type_mismatch'],
      });
      const w2 = card('auth-mixed2b-w2', 'codex-demo-w2', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 6 * 86_400,
        priority: 0, reasons: ['window_type_mismatch'],
      });
      return overview([pool({ cards: [m2, m1, w1, w2], windowKind: 'monthly' })], [], pageNow);
    }
    case 'rev-ten': {
      const x = card('auth-ten-x', 'codex-demo-x', {
        ...base, tier: 1, remaining: 10, resetSeconds: 5 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const z = card('auth-ten-z', 'codex-demo-z', {
        ...base, tier: 2, remaining: 55, resetSeconds: 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const y = card('auth-ten-y', 'codex-demo-y', {
        ...base, role: 'hold', remaining: 9, resetSeconds: 3 * 86_400,
        priority: 0, reasons: ['remaining_below_ten'],
      });
      const u = card('auth-ten-u', 'codex-demo-u', {
        ...base, role: 'hold', remaining: 9.9, resetSeconds: 4 * 86_400,
        priority: 0, reasons: ['remaining_below_ten'],
      });
      return overview([pool({ cards: [x, z, y, u] })], [], pageNow);
    }
    case 'rev-prio': {
      const p = card('auth-prio-p', 'codex-demo-p', {
        ...base, tier: 1, remaining: 90, resetSeconds: 4 * 86_400,
        priority: 7, suggested: 7, needsChange: false,
      });
      const q = card('auth-prio-q', 'codex-demo-q', {
        ...base, tier: 2, remaining: 70, resetSeconds: 3 * 86_400,
        priorityKnown: false, suggested: -3,
      });
      const r = card('auth-prio-r', 'codex-demo-r', {
        ...base, tier: 3, remaining: 50, resetSeconds: 2 * 86_400,
        priority: 0, suggested: -13, needsChange: true,
      });
      const s = card('auth-prio-s', 'codex-demo-s', {
        ...base, tier: 4, remaining: 40, resetSeconds: 86_400,
        priorityKnown: false, suggested: -23,
      });
      return overview([pool({ cards: [p, q, r, s] })], [], pageNow);
    }
    case 'rev-unreadable-a': {
      const a = card('auth-unreadable-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 70, resetSeconds: 4 * 86_400,
        priority: 5, suggested: 5, needsChange: false,
      });
      const b = card('auth-unreadable-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 50, resetSeconds: 2 * 86_400,
        priorityKnown: false, suggested: -5,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-unreadable-b': {
      const a = card('auth-unreadable-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 70, resetSeconds: 4 * 86_400,
        priorityKnown: false, suggested: 3,
      });
      const b = card('auth-unreadable-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 50, resetSeconds: 2 * 86_400,
        priority: 3, suggested: -7, needsChange: true,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-hidden': {
      const a = card('auth-hidden-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-hidden-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const c = card('auth-hidden-c', 'codex-demo-c', {
        ...base, role: 'hold', remaining: 6, resetSeconds: 3 * 86_400,
        priority: 99, reasons: ['remaining_below_ten'],
      });
      const d = card('auth-hidden-d', 'codex-demo-d', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400,
        priority: -5, observedAgoSeconds: 26 * 3_600, reasons: ['reading_stale'],
      });
      const outside = [
        outsideCard('auth-hidden-team-1', 'codex-demo-team-1', {
          poolId: 'codex:team', poolLabel: 'Team 300', priority: 50,
        }),
        outsideCard('auth-hidden-team-2', 'codex-demo-team-2', {
          poolId: 'codex:team', poolLabel: 'Team 300', priorityKnown: false,
        }),
      ];
      return overview([pool({ cards: [a, b, c, d], outside })], [], pageNow);
    }
    case 'rev-reasons': {
      const h1 = card('auth-reasons-h1', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const h2 = card('auth-reasons-h2', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const hold = (key, reason, extra = {}) => card(`auth-reasons-${key}`, `codex-demo-${key}`, {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400,
        priority: 0, reasons: [reason], ...extra,
      });
      const cards = [
        h1, h2,
        hold('r1', 'needs_reauth'),
        hold('r2', 'cooldown_active'),
        hold('r3', 'reading_failed'),
        hold('r4', 'window_exhausted', { remaining: 0 }),
        hold('r5', 'reading_stale', { observedAgoSeconds: 26 * 3_600 }),
        hold('r6', 'reset_time_unknown', { resetSeconds: null }),
        hold('r7', 'remaining_unknown', { remaining: undefined }),
        hold('r8', 'no_long_window', { resetSeconds: null, windowKind: 'five_hour' }),
        hold('r9', 'remaining_below_ten', { remaining: 6 }),
        hold('r10', 'window_type_mismatch', { windowKind: 'monthly', resetSeconds: 10 * 86_400 }),
      ];
      return overview([pool({ cards })], [], pageNow);
    }
    case 'rev-multi': {
      const h1 = card('auth-multi-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const h2 = card('auth-multi-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const m1 = card('auth-multi-m1', 'codex-demo-m1', {
        ...base, role: 'hold', remaining: 6, resetSeconds: 3 * 86_400,
        priority: 0, observedAgoSeconds: 26 * 3_600, reasons: ['reading_stale', 'remaining_below_ten'],
      });
      const m2 = card('auth-multi-m2', 'codex-demo-m2', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400,
        priority: 0, reasons: ['cooldown_active', 'reading_failed'],
      });
      const m3 = card('auth-multi-m3', 'codex-demo-m3', {
        ...base, role: 'hold', remaining: undefined, resetSeconds: null,
        priority: 0, reasons: ['reset_time_unknown', 'remaining_unknown'],
      });
      return overview([pool({ cards: [h1, h2, m1, m2, m3] })], [], pageNow);
    }
    case 'rev-stale3': {
      const a = card('auth-stale3-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-stale3-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const stale = (n, remaining) => card(`auth-stale3-s${n}`, `codex-demo-s${n}`, {
        ...base, role: 'hold', remaining, resetSeconds: 3 * 86_400,
        priority: 0, observedAgoSeconds: 26 * 3_600, reasons: ['reading_stale'],
      });
      return overview([pool({ cards: [a, b, stale(1, 70), stale(2, 65), stale(3, 60)] })], [], pageNow);
    }
    case 'rev-gap3-a': {
      const a = card('auth-gap3-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 70, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-gap3-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 50, resetSeconds: 2 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const c = card('auth-gap3-c', 'codex-demo-c', {
        ...base, role: 'hold', remaining: 60, resetSeconds: null,
        priority: 0, reasons: ['reset_time_unknown'],
      });
      return overview([pool({ cards: [a, b, c] })], [], pageNow);
    }
    case 'rev-gap3-b': {
      const a = card('auth-gap3-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 70, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-gap3-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 50, resetSeconds: 2 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const c = card('auth-gap3-c', 'codex-demo-c', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 3 * 86_400,
        priority: 0, observedAgoSeconds: 26 * 3_600, reasons: ['reading_stale'],
      });
      return overview([pool({ cards: [a, b, c] })], [], pageNow);
    }
    case 'rev-gap3-c': {
      const a = card('auth-gap3-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 70, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-gap3-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 50, resetSeconds: 2 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const c = card('auth-gap3-c', 'codex-demo-c', {
        ...base, role: 'hold', remaining: 60, resetSeconds: 2 * 3_600, windowKind: 'five_hour',
        priority: 0, reasons: ['no_long_window'],
      });
      return overview([pool({ cards: [a, b, c] })], [], pageNow);
    }
    case 'rev-gap3-d': {
      const a = card('auth-gap3-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 70, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-gap3-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 50, resetSeconds: 2 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const c = card('auth-gap3-c', 'codex-demo-c', {
        ...base, role: 'hold', remaining: undefined, resetSeconds: 3 * 86_400,
        priority: 0, reasons: ['remaining_unknown'],
      });
      return overview([pool({ cards: [a, b, c] })], [], pageNow);
    }
    case 'rev-disabled-a': {
      const a = card('auth-disabled-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-disabled-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [a, b] })], [
        unpooledCard('auth-disabled-c', 'codex-demo-c', ['credential_disabled']),
      ], pageNow);
    }
    case 'rev-disabled-b': {
      return overview([], [
        unpooledCard('auth-disabled-a', 'codex-demo-a', ['pool_too_small']),
        unpooledCard('auth-disabled-b', 'codex-demo-b', ['credential_disabled']),
      ], pageNow);
    }
    case 'rev-unpooled': {
      const a = card('auth-unpooled-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-unpooled-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [a, b] })], [
        unpooledCard('auth-unpooled-u1', 'codex-demo-u1', ['plan_type_unknown']),
        unpooledCard('auth-unpooled-u2', 'codex-demo-u2', ['pool_too_small']),
        unpooledCard('auth-unpooled-u3', 'codex-demo-u3', ['credential_disabled']),
      ], pageNow);
    }
    case 'rev-mixed-plan': {
      return overview([], [
        unpooledCard('auth-mixed-plan-plus', 'codex-demo-plus-1', ['pool_too_small']),
        unpooledCard('auth-mixed-plan-team', 'codex-demo-team-solo', ['pool_too_small']),
        unpooledCard('auth-mixed-plan-noplan', 'codex-demo-noplan-1', ['plan_type_unknown']),
      ], pageNow);
    }
    case 'rev-outside': {
      const a = card('auth-outside-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-outside-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const outside = [
        outsideCard('auth-outside-team-1', 'codex-demo-team-1', {
          poolId: 'codex:team', poolLabel: 'Team 300', priority: 50,
        }),
        outsideCard('auth-outside-team-2', 'codex-demo-team-2', {
          poolId: 'codex:team', poolLabel: 'Team 300', priorityKnown: false,
        }),
        outsideCard('auth-outside-compat-1', 'compat-demo-1', {
          provider: 'openai-compat-demo', priorityKnown: false,
        }),
      ];
      return overview([pool({ cards: [a, b], outside })], [], pageNow);
    }
    case 'rev-outside-full': {
      const a = card('auth-outside-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-outside-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const c = card('auth-outside-c', 'codex-demo-c', {
        ...base, role: 'hold', remaining: 6, resetSeconds: 3 * 86_400,
        priority: 0, reasons: ['remaining_below_ten'],
      });
      const outside = [
        outsideCard('auth-outside-team-1', 'codex-demo-team-1', {
          poolId: 'codex:team', poolLabel: 'Team 300', priority: 50,
        }),
        outsideCard('auth-outside-team-2', 'codex-demo-team-2', {
          poolId: 'codex:team', poolLabel: 'Team 300', priorityKnown: false,
        }),
        outsideCard('auth-outside-compat-1', 'compat-demo-1', {
          provider: 'openai-compat-demo', priorityKnown: false,
        }),
        outsideCard('auth-outside-o3', 'compat-demo-o3', {
          provider: 'openai-compat-demo', priorityKnown: false,
        }),
        outsideCard('auth-outside-o6', 'codex-demo-o6', { priorityKnown: false }),
        outsideCard('auth-outside-o7', 'codex-demo-o7', {
          poolId: 'codex:team', poolLabel: 'Team 300', priority: 0,
        }),
      ];
      return overview([pool({ cards: [a, b, c], outside })], [], pageNow);
    }
    case 'rev-outside-none': {
      const a = card('auth-outside-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-outside-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const other = card('auth-outside-claude-1', 'claude-demo-1', {
        ...base, role: 'hold', remaining: 70, resetSeconds: 3 * 86_400,
        priority: 0, windowKind: 'monthly', reasons: ['window_type_mismatch'],
      });
      return overview([pool({ cards: [a, b, other] })], [], pageNow);
    }
    case 'rev-outside-high': {
      const a = card('auth-outside-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 50, suggested: 50, needsChange: false,
      });
      const b = card('auth-outside-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 50, suggested: 50, needsChange: false,
      });
      const outside = [
        outsideCard('auth-outside-compat-1', 'compat-demo-1', {
          provider: 'openai-compat-demo', priorityKnown: false,
        }),
      ];
      return overview([pool({ cards: [a, b], outside })], [], pageNow);
    }
    case 'rev-names': {
      const n1 = card('auth-names-n1', 'demo-account-a', {
        ...base, name: 'codex-demo-a.json', tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const n2 = card('auth-names-n2', 'codex-demo-b.json', {
        ...base, name: 'codex-demo-b.json', tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [n1, n2] })], [], pageNow);
    }
    case 'rev-long-name': {
      const l1 = card('auth-long-l1', 'commandcode-goat-demo-credential-long-display-name-0001', {
        ...base, name: 'commandcode-goat-demo-credential-long-display-name-0001',
        tier: 1, remaining: 88, resetSeconds: 5 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const l2 = card('auth-long-l2', 'demo-account-with-an-unusually-long-display-name-0002', {
        ...base, name: 'codex-demo-credential-file-0002.json',
        tier: 2, remaining: 60, resetSeconds: 3 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const l3 = card('auth-long-l3', 'codex-demo-c', {
        ...base, tier: 3, remaining: 40, resetSeconds: 1 * 86_400,
        priority: 0, suggested: -20, needsChange: true,
      });
      return overview([pool({ cards: [l1, l2, l3] })], [], pageNow);
    }
    case 'rev-no-edit': {
      const a = card('auth-noedit-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: 5 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-noedit-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 60, resetSeconds: 3 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      const p = card('auth-noedit-p', 'plugin-demo-p', {
        ...base, tier: 3, remaining: 40, resetSeconds: 86_400,
        priority: 0, suggested: -20, needsChange: true, canEdit: false,
      });
      return overview([pool({ cards: [a, b, p] })], [], pageNow);
    }
    case 'rev-two-pools': {
      const a = card('auth-twopools-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-twopools-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const t1 = card('auth-twopools-t1', 'codex-demo-t1', {
        ...base, tier: 1, remaining: 70, resetSeconds: 3 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const t2 = card('auth-twopools-t2', 'codex-demo-t2', {
        ...base, tier: 2, remaining: 30, resetSeconds: 0.5 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([
        pool({ cards: [a, b] }),
        pool({ cards: [t1, t2], plan: 'team', label: 'Team 300' }),
      ], [], pageNow);
    }
    case 'rev-entry-s2': {
      const x = card('auth-entry2-x', 'codex-demo-x', {
        ...base, tier: 1, remaining: 70, resetSeconds: 5 * 86_400,
        priority: 0, suggested: 7, needsChange: true,
      });
      const y = card('auth-entry2-y', 'codex-demo-y', {
        ...base, tier: 2, remaining: 55, resetSeconds: 3 * 86_400,
        priority: 7, suggested: -3, needsChange: true,
      });
      const z = card('auth-entry2-z', 'codex-demo-z', {
        ...base, tier: 3, remaining: 35, resetSeconds: 86_400,
        priority: 2, suggested: -13, needsChange: true,
      });
      const t1 = card('auth-entry2-t1', 'codex-demo-t1', {
        ...base, tier: 1, remaining: 70, resetSeconds: 3 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const t2 = card('auth-entry2-t2', 'codex-demo-t2', {
        ...base, tier: 2, remaining: 30, resetSeconds: 0.5 * 86_400,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([
        pool({ cards: [x, y, z] }),
        pool({ cards: [t1, t2], plan: 'team', label: 'Team 300' }),
      ], [], pageNow);
    }
    case 'rev-entry-s3': {
      const a = card('auth-entry3-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-entry3-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: -10, needsChange: true,
      });
      const holds = [1, 2, 3].map((n) => card(`auth-entry3-t${n}`, `codex-demo-t${n}`, {
        ...base, role: 'hold', remaining: 60, resetSeconds: null,
        priority: 0, reasons: ['reset_time_unknown'],
      }));
      return overview([
        pool({ cards: [a, b] }),
        pool({ state: 'no_advice', cards: holds, reasons: ['reset_time_unknown'], plan: 'team', label: 'Team 300' }),
      ], [], pageNow);
    }
    case 'rev-entry-s4': {
      const a = card('auth-entry4-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 10, suggested: 10, needsChange: false,
      });
      const b = card('auth-entry4-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const t1 = card('auth-entry4-t1', 'codex-demo-t1', {
        ...base, tier: 1, remaining: 70, resetSeconds: 3.0 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const t2 = card('auth-entry4-t2', 'codex-demo-t2', {
        ...base, tier: 1, remaining: 30, resetSeconds: 3.5 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      return overview([
        pool({ cards: [a, b] }),
        pool({ cards: [t1, t2], plan: 'team', label: 'Team 300', isBalanced: true }),
      ], [], pageNow);
    }
    case 'rev-refresh-f1': {
      const a = card('auth-refresh-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 88, resetSeconds: HERO_A_RESET,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-refresh-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: HERO_B_RESET,
        priority: -10, suggested: -10, needsChange: false,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-refresh-f2': {
      const a = card('auth-refresh-a', 'codex-demo-a', {
        ...base, tier: 2, remaining: 70, resetSeconds: 2 * 86_400 + 4 * 3_600 + 57 * 60,
        priority: 0, suggested: -10, needsChange: true,
      });
      const b = card('auth-refresh-b', 'codex-demo-b', {
        ...base, tier: 1, remaining: 100, resetSeconds: 6 * 86_400 + 23 * 3_600 + 59 * 60,
        priority: -10, suggested: 0, needsChange: true,
      });
      return overview([pool({ cards: [b, a] })], [], pageNow);
    }
    case 'rev-edge': {
      const a = card('auth-edge-a', 'codex-demo-a', {
        ...base, tier: 1, remaining: 70, resetSeconds: 4 * 86_400,
        priority: 0, suggested: 0, needsChange: false,
      });
      const b = card('auth-edge-b', 'codex-demo-b', {
        ...base, tier: 2, remaining: 40, resetSeconds: 10 * 3_600,
        priority: 0, suggested: -10, needsChange: true,
      });
      return overview([pool({ cards: [a, b] })], [], pageNow);
    }
    case 'rev-legacy-4': {
      const body = probeLending('rev-hero', now, options);
      for (const entry of body.pools[0].cards) delete entry.display_name;
      return body;
    }
    case 'rev-legacy-5': {
      const body = probeLending('rev-hero', now, options);
      for (const entry of body.pools[0].cards) {
        delete entry.priority;
        delete entry.priority_known;
        delete entry.priority_is_default;
        delete entry.needs_change;
      }
      return body;
    }
    case 'rev-legacy-6': {
      const body = probeLending('rev-outside', now, options);
      for (const entry of body.pools) delete entry.outside_cards;
      return body;
    }
    case 'single':
      return overview([], [{ auth_index: 'auth-01', name: 'credential-01', reason_codes: ['pool_too_small'] }], now);
    case 'empty':
      return overview([], [], now);
    case 'absent':
      return null;
    default:
      throw new Error(`unknown lending probe state: ${state}`);
  }
}
/**
 * Fake-CPA data for one named state.
 *
 * `seedMS` fixes the reset instants at seed time: the hero scene must drift
 * exactly as a real reading would, so a later read may not re-anchor "4 days out".
 */
export function createCpaLendingFixture(name, { seedMS = Date.now() } = {}) {
  const fixture = CPA_FIXTURES[name];
  if (!fixture) throw new Error(`unknown CPA lending fixture: ${name}`);
  const prefix = fixture.prefix ?? `lend-${name}`;
  const authFiles = fixture.cards.map((item) => ({
    id: `${prefix}-${item.key}`,
    auth_index: `${prefix}-${item.key}`,
    name: item.name ?? `codex-${name}-${item.key}.json`,
    type: item.type ?? 'codex',
    provider: item.provider ?? 'codex',
    label: item.label ?? `Fixture ${item.key.toUpperCase()}`,
    status: item.status ?? 'ok',
    disabled: item.disabled ?? false,
    unavailable: item.unavailable ?? false,
    runtime_only: item.runtimeOnly ?? false,
    account_type: 'oauth',
    ...(item.email ? { email: item.email } : {}),
    ...(item.projectId ? { project_id: item.projectId } : {}),
    id_token: {
      chatgpt_account_id: `chatgpt-${prefix}-${item.key}`,
      ...(item.planType === null ? {} : { plan_type: item.planType ?? 'pro' }),
      chatgpt_subscription_active_until: Math.floor((seedMS + (item.subscriptionDays ?? 25) * 86_400_000) / 1000),
    },
    success: 1,
    failed: 0,
    models: (item.models ?? ['gpt-5']).map((id) => ({ id, display_name: id })),
    ...(item.priority === null ? {} : { priority: item.priority ?? 1 }),
    weight: item.weight ?? 1,
    ...(item.cooldownSeconds == null ? {} : {
      cooldowns: [{
        scope: 'model',
        model_key: item.cooldownModelKey ?? '',
        reason: item.cooldownReason ?? 'rate_limited',
        http_status: 429,
        retry_at: Math.floor((seedMS + item.cooldownSeconds * 1000) / 1000),
      }],
    }),
    prefix: '',
    proxy_url: '',
    disable_cooling: false,
    websockets: false,
    using_api: false,
    excluded_models: [],
  }));
  const byKey = new Map(fixture.cards.map((item) => [`${prefix}-${item.key}`, item]));
  return {
    authFiles,
    // Credentials with no quota route still belong on the roster (the outside-pool
    // reminder reads them) but must not be handed to the refresh pass: an unsupported
    // read would record a failed attempt and turn them into "reading failed".
    seedIndexes: fixture.cards
      .filter((item) => item.noQuotaRead !== true)
      .map((item) => `${prefix}-${item.key}`),
    usage(authIndex) {
      const item = byKey.get(authIndex);
      if (!item) return undefined;
      if (item.readingFailure === 'reauth') return { failure_status: 401 };
      if (item.readingFailure === 'error') return { failure_status: 500 };
      return usagePayload(item, seedMS);
    },
    subscription(authIndex) {
      const item = byKey.get(authIndex);
      if (!item || item.subscriptionDays == null) return undefined;
      return {
        ...(item.planType === null ? {} : { plan_type: item.planType ?? 'pro' }),
        active_start: new Date(seedMS - 5 * 86_400_000).toISOString(),
        active_until: new Date(seedMS + item.subscriptionDays * 86_400_000).toISOString(),
        will_renew: true,
        is_delinquent: false,
      };
    },
    credits(authIndex) {
      const item = byKey.get(authIndex);
      if (!item || item.resetCredits == null) return undefined;
      return {
        available_count: item.resetCredits,
        applicable_available_count: item.resetCredits,
        credits: item.resetCredits > 0
          ? [{
            id: `${prefix}-credit-1`, status: 'available', reset_type: 'codex_rate_limits',
            granted_at: new Date(seedMS - 10 * 86_400_000).toISOString(),
            expires_at: new Date(seedMS + 19 * 86_400_000).toISOString(),
          }]
          : [],
      };
    },
    commandcode(authIndex) {
      const item = byKey.get(authIndex);
      if (!item || item.type !== 'commandcode') return undefined;
      // The credits document is the read whose failure is fatal to the snapshot, so
      // the same switch the Codex scenes use drives "reading failed" and "needs
      // reauth" for a Command Code credential.
      if (item.readingFailure === 'reauth') return { failure_status: 401 };
      if (item.readingFailure === 'error') return { failure_status: 500 };
      const window = (spec) => spec == null ? undefined : {
        used: spec.used,
        cap: spec.cap,
        ...(spec.exceeded ? { exceeded: true } : {}),
        ...(spec.resetSeconds == null ? {} : { resetAt: seedMS + spec.resetSeconds * 1000 }),
      };
      return {
        credits: {
          credits: {
            ...(item.monthlyCredits === undefined ? {} : { monthlyCredits: item.monthlyCredits }),
            ...(item.purchasedCredits === undefined ? {} : { purchasedCredits: item.purchasedCredits }),
          },
          windowLimits: {
            ...(item.limited === undefined ? {} : { limited: item.limited }),
            ...(item.fiveHour == null ? {} : { fiveHour: window(item.fiveHour) }),
            ...(item.weekly == null ? {} : { weekly: window(item.weekly) }),
          },
        },
        subscription: item.planId ? {
          success: true,
          data: {
            planId: item.planId,
            status: 'active',
            currentPeriodEnd: new Date(seedMS + (item.subscriptionDays ?? 20) * 86_400_000).toISOString(),
          },
        } : undefined,
      };
    },
  };
}

function usagePayload(item, seedMS) {
  const rateLimit = {};
  if (item.longWindow !== false) {
    rateLimit.secondary_window = {
      ...(item.remaining === undefined ? {} : { used_percent: 100 - item.remaining }),
      limit_window_seconds: item.periodSeconds ?? WEEK_SECONDS,
      ...resetFields(item, seedMS),
    };
  }
  // The short window rides beside the long one so the page can prove it is not used
  // for the pool: five hours out, and either full or nearly empty.
  rateLimit.primary_window = {
    used_percent: 100 - (item.shortRemaining ?? 90),
    limit_window_seconds: 18000,
    reset_after_seconds: item.shortResetSeconds ?? 7200,
  };
  return {
    ...(item.planType === null ? {} : { plan_type: item.planType ?? 'pro' }),
    rate_limit: rateLimit,
    ...(item.resetCredits != null ? {
      rate_limit_reset_credits: { available_count: item.resetCredits, applicable_available_count: item.resetCredits },
    } : {}),
  };
}

function resetFields(item, seedMS) {
  if (item.resetSeconds == null) return {};
  if (item.accuracy === 'derived') {
    // A derived reset is computed by the reader from "now"; keeping the distance
    // stable is what makes the precision marker reproducible.
    return { reset_after_seconds: Math.max(1, Math.round(item.resetSeconds)) };
  }
  const resetAtMS = seedMS + item.resetSeconds * 1000;
  return {
    reset_at: Math.floor(resetAtMS / 1000),
    reset_after_seconds: Math.max(1, Math.round((resetAtMS - Date.now()) / 1000)),
  };
}

const CPA_FIXTURES = {
  base: {
    cards: [
      { key: 'a', remaining: 88, resetSeconds: 3.9 * 86_400, shortRemaining: 5, shortResetSeconds: 1200 },
      { key: 'b', remaining: 40, resetSeconds: 2.3 * 86_400 },
    ],
  },
  fastHigh: {
    cards: [
      { key: 'a', remaining: 30, resetSeconds: 4 * 86_400 },
      { key: 'b', remaining: 95, resetSeconds: 86_400 },
    ],
  },
  fastLow: {
    cards: [
      { key: 'a', remaining: 5, resetSeconds: 30 * 86_400, periodSeconds: MONTH_SECONDS },
      { key: 'b', remaining: 80, resetSeconds: 2 * 86_400, periodSeconds: MONTH_SECONDS },
    ],
  },
  close: {
    cards: [
      { key: 'a', remaining: 60, resetSeconds: 4 * 86_400 },
      { key: 'b', remaining: 58, resetSeconds: 4 * 86_400 },
    ],
  },
  subHour: {
    cards: [
      { key: 'a', remaining: 10, resetSeconds: 1200 },
      { key: 'b', remaining: 60, resetSeconds: 4 * 86_400 },
    ],
  },
  derived: {
    cards: [
      { key: 'a', remaining: 50, resetSeconds: 4 * 86_400, accuracy: 'derived' },
      { key: 'b', remaining: 49, resetSeconds: 4 * 86_400, accuracy: 'derived' },
    ],
  },
  textOnly: {
    cards: [
      { key: 'a', remaining: 55, resetSeconds: null },
      { key: 'b', remaining: 60, resetSeconds: 3 * 86_400 },
    ],
  },
  noLong: {
    cards: [
      { key: 'a', remaining: 40, resetSeconds: null, longWindow: false },
      { key: 'b', remaining: 60, resetSeconds: 3 * 86_400 },
      { key: 'c', remaining: 55, resetSeconds: 4 * 86_400 },
    ],
  },
  windowMismatch: {
    cards: [
      { key: 'a', remaining: 60, resetSeconds: 3 * 86_400, periodSeconds: WEEK_SECONDS },
      { key: 'b', remaining: 70, resetSeconds: 10 * 86_400, periodSeconds: MONTH_SECONDS },
    ],
  },
  limited: {
    cards: [
      { key: 'a', remaining: 40, resetSeconds: 2 * 86_400 },
      { key: 'b', remaining: 5, resetSeconds: 30 * 86_400 },
    ],
  },
  restricted: {
    cards: [
      { key: 'a', remaining: 40, resetSeconds: 2 * 86_400 },
      { key: 'b', remaining: 30, resetSeconds: 3 * 86_400 },
      { key: 'c', remaining: 5, resetSeconds: 30 * 86_400 },
    ],
  },
  single: { cards: [{ key: 'a', remaining: 60, resetSeconds: 4 * 86_400 }] },
  twoPools: {
    prefix: 'auth-lend-two',
    cards: [
      { key: 'a', planType: 'pro', remaining: 88, resetSeconds: 4 * 86_400, subscriptionDays: 25, resetCredits: 0 },
      { key: 'b', planType: 'pro', remaining: 40, resetSeconds: 2.3 * 86_400, subscriptionDays: 15, resetCredits: 1 },
      { key: 'c', planType: 'team', remaining: 70, resetSeconds: 1 * 86_400, subscriptionDays: 20, resetCredits: 0 },
      { key: 'd', planType: 'team', remaining: 30, resetSeconds: 0.5 * 86_400, subscriptionDays: 10, resetCredits: 0 },
    ],
  },
  hero: {
    prefix: 'auth-lend-hero',
    cards: [
      {
        key: 'a', name: 'codex-demo-a.json', label: 'codex-demo-a', remaining: 88,
        resetSeconds: HERO_A_RESET, subscriptionDays: 25, resetCredits: 0,
        shortRemaining: 5, shortResetSeconds: 1200, priority: null,
      },
      {
        key: 'b', name: 'codex-demo-b.json', label: 'codex-demo-b', remaining: 40,
        resetSeconds: HERO_B_RESET, subscriptionDays: 15, resetCredits: 1,
        shortRemaining: 90, shortResetSeconds: 7200, priority: null,
      },
    ],
  },
  revHeroKeep: {
    prefix: 'auth-lend-hero',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', label: 'codex-demo-a', remaining: 88, resetSeconds: HERO_A_RESET, priority: 10 },
      { key: 'b', name: 'codex-demo-b.json', label: 'codex-demo-b', remaining: 40, resetSeconds: HERO_B_RESET, priority: 0 },
    ],
  },
  revSamePct: {
    cards: [
      { key: 'a', remaining: 60, resetSeconds: 4 * 86_400, priority: null },
      { key: 'b', remaining: 60, resetSeconds: 86_400, priority: null },
    ],
  },
  revNear: {
    cards: [
      { key: 'a', remaining: 70, resetSeconds: 3.0 * 86_400, priority: null },
      { key: 'b', remaining: 50, resetSeconds: 3.5 * 86_400, priority: null },
    ],
  },
  revGap: {
    cards: [
      { key: 'a', remaining: 88, resetSeconds: HERO_B_RESET + 86_400, priority: null },
      { key: 'b', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
    ],
  },
  revTwin: {
    cards: [
      { key: 'a', remaining: 60, resetSeconds: 4 * 86_400, priority: null },
      { key: 'b', remaining: 60, resetSeconds: 4 * 86_400, priority: null },
    ],
  },
  revDerived2: {
    cards: [
      { key: 'a', remaining: 60, resetSeconds: 5 * 86_400, accuracy: 'derived', priority: null },
      { key: 'b', remaining: 40, resetSeconds: 2 * 86_400, accuracy: 'derived', priority: null },
    ],
  },
  revThree: {
    cards: [
      { key: 'x', name: 'codex-demo-x.json', remaining: 70, resetSeconds: 5 * 86_400, priority: null },
      { key: 'y', name: 'codex-demo-y.json', remaining: 55, resetSeconds: 3 * 86_400, priority: null },
      { key: 'z', name: 'codex-demo-z.json', remaining: 35, resetSeconds: 86_400, priority: null },
    ],
  },
  revFour: {
    cards: [
      { key: 'w', name: 'codex-demo-w.json', remaining: 80, resetSeconds: 6 * 86_400, priority: null },
      { key: 'x', name: 'codex-demo-x.json', remaining: 65, resetSeconds: 4 * 86_400, priority: null },
      { key: 'y', name: 'codex-demo-y.json', remaining: 50, resetSeconds: 2.5 * 86_400, priority: null },
      { key: 'z', name: 'codex-demo-z.json', remaining: 35, resetSeconds: 86_400, priority: null },
    ],
  },
  revTierMix: {
    cards: [
      { key: 'x', name: 'codex-demo-x.json', remaining: 70, resetSeconds: 5.0 * 86_400, priority: null },
      { key: 'y', name: 'codex-demo-y.json', remaining: 55, resetSeconds: 4.5 * 86_400, priority: null },
      { key: 'z', name: 'codex-demo-z.json', remaining: 35, resetSeconds: 2.0 * 86_400, priority: null },
    ],
  },
  revChain: {
    cards: [
      { key: 'x', name: 'codex-demo-x.json', remaining: 70, resetSeconds: 3.0 * 86_400, priority: null },
      { key: 'y', name: 'codex-demo-y.json', remaining: 55, resetSeconds: 2.4 * 86_400, priority: null },
      { key: 'z', name: 'codex-demo-z.json', remaining: 40, resetSeconds: 1.8 * 86_400, priority: null },
    ],
  },
  revDualWindow: {
    cards: [
      { key: 'c', name: 'codex-demo-c.json', remaining: 30, resetSeconds: 12 * 86_400, periodSeconds: MONTH_SECONDS, priority: null },
      { key: 'd', name: 'codex-demo-d.json', remaining: 70, resetSeconds: 9 * 86_400, periodSeconds: MONTH_SECONDS, priority: null },
    ],
  },
  revMixedType1: {
    cards: [
      { key: 'x', name: 'codex-demo-x.json', remaining: 70, resetSeconds: 5 * 86_400, priority: null },
      { key: 'y', name: 'codex-demo-y.json', remaining: 55, resetSeconds: 3 * 86_400, priority: null },
      { key: 'z', name: 'codex-demo-z.json', remaining: 35, resetSeconds: 86_400, priority: null },
      { key: 'm', name: 'codex-demo-m.json', remaining: 80, resetSeconds: 12 * 86_400, periodSeconds: MONTH_SECONDS, priority: null },
    ],
  },
  revMixedType2a: {
    cards: [
      { key: 'w1', name: 'codex-demo-w1.json', remaining: 60, resetSeconds: 3 * 86_400, priority: null },
      { key: 'w2', name: 'codex-demo-w2.json', remaining: 60, resetSeconds: 5 * 86_400, priority: null },
      { key: 'm1', name: 'codex-demo-m1.json', remaining: 60, resetSeconds: 10 * 86_400, periodSeconds: MONTH_SECONDS, priority: null },
      { key: 'm2', name: 'codex-demo-m2.json', remaining: 60, resetSeconds: 12 * 86_400, periodSeconds: MONTH_SECONDS, priority: null },
    ],
  },
  revMixedType2b: {
    cards: [
      { key: 'm1', name: 'codex-demo-m1.json', remaining: 60, resetSeconds: 2 * 86_400, periodSeconds: MONTH_SECONDS, priority: null },
      { key: 'm2', name: 'codex-demo-m2.json', remaining: 60, resetSeconds: 4 * 86_400, periodSeconds: MONTH_SECONDS, priority: null },
      { key: 'w1', name: 'codex-demo-w1.json', remaining: 60, resetSeconds: 3 * 86_400, priority: null },
      { key: 'w2', name: 'codex-demo-w2.json', remaining: 60, resetSeconds: 6 * 86_400, priority: null },
    ],
  },
  revTen: {
    cards: [
      { key: 'x', name: 'codex-demo-x.json', remaining: 10, resetSeconds: 5 * 86_400, priority: null },
      { key: 'y', name: 'codex-demo-y.json', remaining: 9, resetSeconds: 3 * 86_400, priority: null },
      { key: 'z', name: 'codex-demo-z.json', remaining: 55, resetSeconds: 86_400, priority: null },
      { key: 'u', name: 'codex-demo-u.json', remaining: 9.9, resetSeconds: 4 * 86_400, priority: null },
    ],
  },
  revPrio: {
    cards: [
      { key: 'p', name: 'codex-demo-p.json', remaining: 90, resetSeconds: 4 * 86_400, priority: 7 },
      { key: 'q', name: 'codex-demo-q.json', remaining: 70, resetSeconds: 3 * 86_400, priority: null },
      { key: 'r', name: 'codex-demo-r.json', remaining: 50, resetSeconds: 2 * 86_400, priority: 0 },
      { key: 's', name: 'codex-demo-s.json', remaining: 40, resetSeconds: 86_400, type: 'openai-compatibility', provider: 'openai-compatibility', noQuotaRead: true },
    ],
  },
  revHidden: {
    prefix: 'auth-lend-hidden',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
      { key: 'c', name: 'codex-demo-c.json', remaining: 6, resetSeconds: 3 * 86_400, priority: 99 },
      { key: 'd', name: 'codex-demo-d.json', remaining: 60, resetSeconds: null, priority: -5 },
      { key: 'team-1', name: 'codex-demo-team-1.json', planType: 'team', remaining: 80, resetSeconds: 3 * 86_400, priority: 50 },
      { key: 'team-2', name: 'codex-demo-team-2.json', planType: 'team', remaining: 50, resetSeconds: 86_400, priority: null },
    ],
  },
  revReasons: {
    prefix: 'auth-lend-reasons',
    cards: [
      { key: 'h1', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'h2', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
      { key: 'r1', name: 'codex-demo-r1.json', remaining: 60, resetSeconds: 3 * 86_400, readingFailure: 'reauth', priority: null },
      { key: 'r2', name: 'codex-demo-r2.json', remaining: 60, resetSeconds: 3 * 86_400, cooldownSeconds: 1800, priority: null },
      { key: 'r3', name: 'codex-demo-r3.json', remaining: 60, resetSeconds: 3 * 86_400, readingFailure: 'error', priority: null },
      { key: 'r4', name: 'codex-demo-r4.json', remaining: 0, resetSeconds: 3 * 86_400, priority: null },
      { key: 'r6', name: 'codex-demo-r6.json', remaining: 60, resetSeconds: null, priority: null },
      { key: 'r7', name: 'codex-demo-r7.json', remaining: undefined, resetSeconds: 3 * 86_400, priority: null },
      { key: 'r8', name: 'codex-demo-r8.json', longWindow: false, priority: null },
      { key: 'r9', name: 'codex-demo-r9.json', remaining: 6, resetSeconds: 3 * 86_400, priority: null },
      { key: 'r10', name: 'codex-demo-r10.json', remaining: 60, resetSeconds: 10 * 86_400, periodSeconds: MONTH_SECONDS, priority: null },
    ],
  },
  revMulti: {
    prefix: 'auth-lend-multi',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
      { key: 'm1', name: 'codex-demo-m1.json', remaining: 6, resetSeconds: 3 * 86_400, priority: null },
      { key: 'm2', name: 'codex-demo-m2.json', remaining: 60, resetSeconds: 3 * 86_400, cooldownSeconds: 1800, readingFailure: 'error', priority: null },
      { key: 'm3', name: 'codex-demo-m3.json', remaining: undefined, resetSeconds: null, priority: null },
    ],
  },
  revGap3a: {
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 70, resetSeconds: 4 * 86_400, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 50, resetSeconds: 2 * 86_400, priority: null },
      { key: 'c', name: 'codex-demo-c.json', remaining: 60, resetSeconds: null, priority: null },
    ],
  },
  revGap3c: {
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 70, resetSeconds: 4 * 86_400, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 50, resetSeconds: 2 * 86_400, priority: null },
      { key: 'c', name: 'codex-demo-c.json', longWindow: false, priority: null },
    ],
  },
  revGap3d: {
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 70, resetSeconds: 4 * 86_400, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 50, resetSeconds: 2 * 86_400, priority: null },
      { key: 'c', name: 'codex-demo-c.json', remaining: undefined, resetSeconds: 3 * 86_400, priority: null },
    ],
  },
  revDisabledA: {
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
      { key: 'c', name: 'codex-demo-c.json', remaining: 60, resetSeconds: 3 * 86_400, disabled: true, noQuotaRead: true, priority: null },
    ],
  },
  revDisabledB: {
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, disabled: true, noQuotaRead: true, priority: null },
    ],
  },
  revUnpooled: {
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
      { key: 'u1', name: 'codex-demo-u1.json', planType: null, remaining: 70, resetSeconds: 3 * 86_400, priority: null },
      { key: 'u2', name: 'codex-demo-u2.json', planType: 'plus', remaining: 60, resetSeconds: 3 * 86_400, priority: null },
      { key: 'u3', name: 'codex-demo-u3.json', remaining: 50, resetSeconds: 3 * 86_400, disabled: true, noQuotaRead: true, priority: null },
    ],
  },
  revMixedPlan: {
    cards: [
      { key: 'plus-1', name: 'codex-demo-plus-1.json', planType: 'plus', remaining: 60, resetSeconds: 4 * 86_400, priority: null },
      { key: 'team-solo', name: 'codex-demo-team-solo.json', planType: 'team', remaining: 50, resetSeconds: 3 * 86_400, priority: null },
      { key: 'noplan-1', name: 'codex-demo-noplan-1.json', planType: null, remaining: 40, resetSeconds: 2 * 86_400, priority: null },
    ],
  },
  revOutside: {
    prefix: 'auth-lend-outside',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, models: ['gpt-5', 'gpt-5-codex'], priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, models: ['gpt-5', 'gpt-5-codex'], priority: null },
      { key: 'team-1', name: 'codex-demo-team-1.json', planType: 'team', remaining: 80, resetSeconds: 3 * 86_400, priority: 50 },
      { key: 'team-2', name: 'codex-demo-team-2.json', planType: 'team', remaining: 50, resetSeconds: 86_400, priority: null },
      { key: 'compat-1', name: 'compat-demo-1.json', type: 'openai-compatibility', provider: 'openai-compat-demo', noQuotaRead: true, remaining: 60, resetSeconds: 3 * 86_400 },
    ],
  },
  revOutsideFull: {
    prefix: 'auth-lend-outside-full',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, models: ['gpt-5', 'gpt-5-codex'], priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, models: ['gpt-5', 'gpt-5-codex'], priority: null },
      { key: 'c', name: 'codex-demo-c.json', remaining: 6, resetSeconds: 3 * 86_400, priority: null },
      { key: 'team-1', name: 'codex-demo-team-1.json', planType: 'team', remaining: 80, resetSeconds: 3 * 86_400, priority: 50 },
      { key: 'team-2', name: 'codex-demo-team-2.json', planType: 'team', remaining: 50, resetSeconds: 86_400, priority: null },
      { key: 'compat-1', name: 'compat-demo-1.json', type: 'openai-compatibility', provider: 'openai-compat-demo', noQuotaRead: true },
      { key: 'o3', name: 'compat-demo-o3.json', type: 'openai-compatibility', provider: 'openai-compat-demo', models: ['gpt-5', 'claude-sonnet-demo'], noQuotaRead: true },
      { key: 'o4', name: 'claude-demo-o4.json', type: 'claude', provider: 'claude', models: ['claude-sonnet-demo'], remaining: 60, resetSeconds: 3 * 86_400 },
      { key: 'o5', name: 'codex-demo-o5.json', remaining: 60, resetSeconds: 3 * 86_400, disabled: true, noQuotaRead: true },
      { key: 'o6', name: 'codex-demo-o6.json', planType: null, remaining: 60, resetSeconds: 3 * 86_400 },
      { key: 'o7', name: 'codex-demo-o7.json', planType: 'team', remaining: 6, resetSeconds: 3 * 86_400, priority: null },
    ],
  },
  revOutsideNone: {
    prefix: 'auth-lend-outside-none',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, models: ['gpt-5', 'gpt-5-codex'], priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, models: ['gpt-5', 'gpt-5-codex'], priority: null },
      { key: 'claude-1', name: 'claude-demo-1.json', type: 'claude', provider: 'claude', models: ['claude-sonnet-demo'], remaining: 70, resetSeconds: 3 * 86_400 },
    ],
  },
  revOutsideHigh: {
    prefix: 'auth-lend-outside-high',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, models: ['gpt-5', 'gpt-5-codex'], priority: 50 },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, models: ['gpt-5', 'gpt-5-codex'], priority: 50 },
      { key: 'compat-1', name: 'compat-demo-1.json', type: 'openai-compatibility', provider: 'openai-compat-demo', noQuotaRead: true },
    ],
  },
  revNames: {
    prefix: 'auth-lend-names',
    cards: [
      { key: 'n1', name: 'codex-demo-a.json', email: 'demo-account-a', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'n2', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
    ],
  },
  revLongName: {
    prefix: 'auth-lend-long',
    cards: [
      { key: 'l1', name: 'commandcode-goat-demo-credential-long-display-name-0001', remaining: 88, resetSeconds: 5 * 86_400, priority: null },
      { key: 'l2', name: 'codex-demo-credential-file-0002.json', email: 'demo-account-with-an-unusually-long-display-name-0002', remaining: 60, resetSeconds: 3 * 86_400, priority: null },
      { key: 'l3', name: 'codex-demo-c.json', remaining: 40, resetSeconds: 86_400, priority: null },
    ],
  },
  revNoEdit: {
    prefix: 'auth-lend-noedit',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: 5 * 86_400, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 60, resetSeconds: 3 * 86_400, priority: null },
      { key: 'p', name: 'plugin-demo-p.json', remaining: 40, resetSeconds: 86_400, runtimeOnly: true, priority: null },
    ],
  },
  revTwoPools: {
    prefix: 'auth-lend-twopools',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
      { key: 't1', name: 'codex-demo-t1.json', planType: 'team', remaining: 70, resetSeconds: 3 * 86_400, priority: null },
      { key: 't2', name: 'codex-demo-t2.json', planType: 'team', remaining: 30, resetSeconds: 0.5 * 86_400, priority: null },
    ],
  },
  // A Command Code pool for the experience environment: one plan (Go, 70 credits a
  // month), four seats carrying different window shapes. Credits are illustrative,
  // not a capture.
  revCommandCode: {
    prefix: 'auth-lend-cmdcode',
    cards: [
      {
        key: 'cc1', name: 'commandcode-demo-1.json', type: 'commandcode', provider: 'commandcode',
        planId: 'individual-goat', monthlyCredits: 49, limited: true, subscriptionDays: 18,
        fiveHour: { used: 3, cap: 60, resetSeconds: 1_800 },
        weekly: { used: 14, cap: 70, resetSeconds: 3 * 86_400 },
        priority: null,
      },
      {
        key: 'cc2', name: 'commandcode-demo-2.json', type: 'commandcode', provider: 'commandcode',
        planId: 'individual-goat', monthlyCredits: 56, limited: true, subscriptionDays: 11,
        fiveHour: { used: 54, cap: 60, resetSeconds: 7_200 },
        weekly: { used: 28, cap: 70, resetSeconds: 2 * 86_400 },
        priority: null,
      },
      {
        // No five-hour window at all, and the monthly credits are under a tenth.
        key: 'cc3', name: 'commandcode-demo-3.json', type: 'commandcode', provider: 'commandcode',
        planId: 'individual-goat', monthlyCredits: 4, limited: true, subscriptionDays: 9,
        weekly: { used: 66, cap: 70, resetSeconds: 2 * 86_400 },
        priority: null,
      },
      {
        // The weekly window reports a cap but no usage, so it has no percentage.
        key: 'cc4', name: 'commandcode-demo-4.json', type: 'commandcode', provider: 'commandcode',
        planId: 'individual-goat', monthlyCredits: 35, limited: true, subscriptionDays: 21,
        fiveHour: { used: 30, cap: 60, resetSeconds: 3_600 },
        weekly: { cap: 70 },
        priority: null,
      },
    ],
  },
  // The two ways a Command Code billing read can fail. A credential that never had a
  // successful read has no plan either, so it lands outside the pool rather than on
  // its held list; this scene exists to prove the failure mapping, not to be pretty.
  revCommandCodeFail: {
    prefix: 'auth-lend-cmdcode-fail',
    cards: [
      { key: 'cc1', name: 'commandcode-demo-1.json', type: 'commandcode', provider: 'commandcode', planId: 'individual-goat', monthlyCredits: 49, limited: true, subscriptionDays: 18, weekly: { used: 14, cap: 70, resetSeconds: 3 * 86_400 }, priority: null },
      { key: 'cc5', name: 'commandcode-demo-5.json', type: 'commandcode', provider: 'commandcode', planId: 'individual-goat', readingFailure: 'error', priority: null },
      { key: 'cc6', name: 'commandcode-demo-6.json', type: 'commandcode', provider: 'commandcode', planId: 'individual-goat', readingFailure: 'reauth', priority: null },
    ],
  },
  revEntryS2: {
    prefix: 'auth-lend-entry-s2',
    cards: [
      { key: 'x', name: 'codex-demo-x.json', remaining: 70, resetSeconds: 5 * 86_400, priority: 0 },
      { key: 'y', name: 'codex-demo-y.json', remaining: 55, resetSeconds: 3 * 86_400, priority: 7 },
      { key: 'z', name: 'codex-demo-z.json', remaining: 35, resetSeconds: 86_400, priority: 2 },
      { key: 't1', name: 'codex-demo-t1.json', planType: 'team', remaining: 70, resetSeconds: 3 * 86_400, priority: 0 },
      { key: 't2', name: 'codex-demo-t2.json', planType: 'team', remaining: 30, resetSeconds: 0.5 * 86_400, priority: 0 },
    ],
  },
  revEntryS3: {
    prefix: 'auth-lend-entry-s3',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: null },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: null },
      { key: 't1', name: 'codex-demo-t1.json', planType: 'team', remaining: 70, resetSeconds: null, priority: 0 },
      { key: 't2', name: 'codex-demo-t2.json', planType: 'team', remaining: 60, resetSeconds: null, priority: 0 },
      { key: 't3', name: 'codex-demo-t3.json', planType: 'team', remaining: 50, resetSeconds: null, priority: 0 },
    ],
  },
  revEntryS4: {
    prefix: 'auth-lend-entry-s4',
    cards: [
      { key: 'a', name: 'codex-demo-a.json', remaining: 88, resetSeconds: HERO_A_RESET, priority: 10 },
      { key: 'b', name: 'codex-demo-b.json', remaining: 40, resetSeconds: HERO_B_RESET, priority: 0 },
      { key: 't1', name: 'codex-demo-t1.json', planType: 'team', remaining: 70, resetSeconds: 3.0 * 86_400, priority: 0 },
      { key: 't2', name: 'codex-demo-t2.json', planType: 'team', remaining: 30, resetSeconds: 3.5 * 86_400, priority: 0 },
    ],
  },
};
