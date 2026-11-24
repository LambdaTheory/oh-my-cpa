/**
 * Acceptance fixtures for the cross-credential lending advice.
 *
 * One fixture definition feeds two surfaces so they cannot drift:
 *
 *   - `probeLending(state)` is the `/management/quota.lending` body the browser
 *     probes serve, with the page clock recomputing the moving figures.
 *   - `createCpaLendingFixture(name)` is the fake CPA side: the Codex credentials
 *     plus the `/requests/api-call` payloads the real backend reads, so the same
 *     states can be seen against a built binary.
 *
 * These states exist because the first review could only reach them through unit
 * tests: a reset known only as upstream text, a stale reading, a weekly/monthly
 * mismatch, a limited pool and a pool that still ranks with one restricted seat.
 * Their field shape follows `internal/quota/lending.go`: a card with no comparable
 * window keeps its remaining share but carries no day count or daily rate, and an
 * unavailable pool publishes no validity instant.
 *
 * Nothing here is product data and nothing outside a test imports it.
 */

export const DAY_MS = 86_400_000;
const WEEK_SECONDS = 604800;
const MONTH_SECONDS = 2592000;
const MIN_DAYS = 1 / 24;
const LOW_REMAINING_PERCENT = 10;

const round1 = (value) => Math.round(value * 10) / 10;

/** The same freshness bound the console uses: period/4, at least two hours, at most one day. */
function freshWindowMs(periodSeconds) {
  const quarter = (periodSeconds / 4) * 1000;
  return Math.min(Math.max(quarter, 2 * 3_600_000), 24 * 3_600_000);
}

/**
 * One card exactly as the Go projection would emit it.
 *
 * `service` names why a comparable card cannot take traffic (it still contributes
 * its reading); `hasLongWindow === false` is the one case where the suggestion
 * window does not exist, so the card carries no window fields at all.
 */
function card(authIndex, name, role, options) {
  const {
    now,
    remaining,
    resetSeconds = null,
    windowKind = 'weekly',
    periodSeconds = WEEK_SECONDS,
    observedAgoSeconds = 0,
    accuracy = 'exact',
    hasLongWindow = true,
    service = [],
    poolMismatch = false,
  } = options;
  const observedAt = now - observedAgoSeconds * 1000;
  const freshUntil = observedAt + freshWindowMs(periodSeconds);
  const reasons = [];
  if (hasLongWindow) {
    if (remaining == null) reasons.push('remaining_unknown');
    if (resetSeconds == null) reasons.push('reset_time_unknown');
    if (observedAgoSeconds * 1000 > freshWindowMs(periodSeconds)) reasons.push('reading_stale');
  } else {
    reasons.push('no_long_window');
  }
  const comparable = hasLongWindow && reasons.length === 0;
  const days = comparable ? resetSeconds / 86_400 : null;
  const daily = comparable ? remaining / days : null;
  const result = {
    auth_index: authIndex,
    name,
    role,
    window_id: hasLongWindow ? `${authIndex}-${windowKind}` : '',
    window_kind: hasLongWindow ? windowKind : '',
    remaining_percent: hasLongWindow && remaining != null ? remaining : undefined,
    reset_at_ms: hasLongWindow && resetSeconds != null ? now + resetSeconds * 1000 : undefined,
    reset_accuracy: hasLongWindow && resetSeconds != null ? accuracy : '',
    observed_at_ms: observedAt,
    fresh_until_ms: freshUntil,
    days_to_reset: days == null ? undefined : round1(days),
    daily_available_percent: daily == null ? undefined : round1(daily),
    reason_codes: [...new Set([...reasons, ...(comparable ? service : []), ...(poolMismatch ? ['window_type_mismatch'] : [])])].sort(),
    is_comparable: comparable,
  };
  return result;
}

function pool({ state, cards, reasons = [], validUntil, isReference = false, windowKind = 'weekly', plan = 'pro', label = 'Pro 200' }) {
  const observed = cards
    .map((entry) => entry.observed_at_ms)
    .filter((value) => value != null)
    .sort((left, right) => left - right)[0];
  const fresh = cards
    .map((entry) => entry.fresh_until_ms)
    .filter((value) => value != null)
    .sort((left, right) => left - right)[0];
  return {
    id: `codex:${plan}`,
    provider: 'codex',
    plan_type: plan,
    plan_label: label,
    window_kind: state === 'unavailable' ? '' : windowKind,
    state,
    valid_until_ms: state === 'unavailable' ? undefined : validUntil,
    observed_at_ms: observed,
    fresh_until_ms: fresh,
    is_reference: isReference,
    reason_codes: reasons,
    cards,
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

/**
 * The lending body for one named UI state.
 *
 * `null` is the old-backend shape (`lending: null`); a missing key is served by a
 * separate route and is not expressed here.
 */
export function probeLending(state, now = Date.now()) {
  const base = { now };
  switch (state) {
    case 'base': {
      const a = card('auth-01', 'credential-01.json', 'prefer', { ...base, remaining: 88, resetSeconds: 3.9 * 86_400 });
      const b = card('auth-02', 'credential-02.json', 'avoid', { ...base, remaining: 40, resetSeconds: 2.3 * 86_400 });
      return overview([pool({ state: 'advised', cards: [a, b], validUntil: b.reset_at_ms })], [
        { auth_index: 'auth-03', name: 'credential-03.json', reason_codes: ['plan_type_unknown'] },
        { auth_index: 'auth-04', name: 'credential-04.json', reason_codes: ['pool_too_small', 'no_long_window'] },
      ], now);
    }
    case 'balanced': {
      const a = card('auth-01', 'credential-01.json', 'balanced', { ...base, remaining: 60, resetSeconds: 4 * 86_400 });
      const b = card('auth-02', 'credential-02.json', 'balanced', { ...base, remaining: 58, resetSeconds: 4 * 86_400 });
      return overview([pool({ state: 'balanced', cards: [a, b], validUntil: a.reset_at_ms })], [], now);
    }
    case 'limited': {
      // Only one comparable seat can serve: the other is below ten percent. The
      // healthy seat stays neutral instead of inheriting the restriction.
      const a = card('auth-01', 'credential-01.json', 'normal', { ...base, remaining: 40, resetSeconds: 2 * 86_400 });
      const b = card('auth-02', 'credential-02.json', 'limited', { ...base, remaining: 5, resetSeconds: 30 * 86_400, service: ['remaining_below_ten'] });
      return overview([pool({ state: 'limited', cards: [a, b], validUntil: a.reset_at_ms })], [], now);
    }
    case 'restricted': {
      // Two seats rank normally; the third is kept out of the ranking by the
      // ten-percent floor without turning the pool into "no preference".
      const a = card('auth-01', 'credential-01.json', 'prefer', { ...base, remaining: 40, resetSeconds: 2 * 86_400 });
      const b = card('auth-02', 'credential-02.json', 'avoid', { ...base, remaining: 30, resetSeconds: 3 * 86_400 });
      const c = card('auth-03', 'credential-03.json', 'limited', { ...base, remaining: 5, resetSeconds: 30 * 86_400, service: ['remaining_below_ten'] });
      return overview([pool({ state: 'advised', cards: [a, b, c], validUntil: a.reset_at_ms })], [], now);
    }
    case 'textOnly': {
      const a = card('auth-01', 'credential-01.json', 'unavailable', { ...base, remaining: 55 });
      const b = card('auth-02', 'credential-02.json', 'unavailable', { ...base, remaining: 60, resetSeconds: 3 * 86_400 });
      return overview([pool({ state: 'unavailable', cards: [a, b], reasons: ['reset_time_unknown'] })], [], now);
    }
    case 'stale': {
      const a = card('auth-01', 'credential-01.json', 'unavailable', {
        ...base, remaining: 70, resetSeconds: 3 * 86_400, observedAgoSeconds: 25 * 3_600,
      });
      const b = card('auth-02', 'credential-02.json', 'unavailable', { ...base, remaining: 65, resetSeconds: 3 * 86_400 });
      return overview([pool({ state: 'unavailable', cards: [a, b], reasons: ['reading_stale'] })], [], now);
    }
    case 'noLong': {
      const a = card('auth-01', 'credential-01.json', 'unavailable', { ...base, hasLongWindow: false });
      const b = card('auth-02', 'credential-02.json', 'unavailable', { ...base, remaining: 60, resetSeconds: 3 * 86_400 });
      const c = card('auth-03', 'credential-03.json', 'unavailable', { ...base, remaining: 55, resetSeconds: 4 * 86_400 });
      return overview([pool({ state: 'unavailable', cards: [a, b, c], reasons: ['no_long_window'] })], [], now);
    }
    case 'windowMismatch': {
      const a = card('auth-01', 'credential-01.json', 'unavailable', {
        ...base, remaining: 60, resetSeconds: 3 * 86_400, poolMismatch: true,
      });
      const b = card('auth-02', 'credential-02.json', 'unavailable', {
        ...base, remaining: 70, resetSeconds: 10 * 86_400, windowKind: 'monthly', periodSeconds: MONTH_SECONDS, poolMismatch: true,
      });
      return overview([pool({ state: 'unavailable', cards: [a, b], reasons: ['window_type_mismatch'] })], [], now);
    }
    case 'subHour': {
      const a = card('auth-01', 'credential-01.json', 'prefer', { ...base, remaining: 10, resetSeconds: 20 * 60 });
      const b = card('auth-02', 'credential-02.json', 'avoid', { ...base, remaining: 60, resetSeconds: 4 * 86_400 });
      return overview([pool({ state: 'advised', cards: [a, b], validUntil: a.reset_at_ms })], [], now);
    }
    case 'derived': {
      const a = card('auth-01', 'credential-01.json', 'balanced', { ...base, remaining: 50, resetSeconds: 4 * 86_400, accuracy: 'derived' });
      const b = card('auth-02', 'credential-02.json', 'balanced', { ...base, remaining: 49, resetSeconds: 4 * 86_400, accuracy: 'derived' });
      return overview([pool({ state: 'balanced', cards: [a, b], validUntil: a.reset_at_ms, isReference: true })], [], now);
    }
    case 'twoPools': {
      // Two pools each need a preference: the entry must count both, not fall back to one.
      const a = card('auth-01', 'credential-01.json', 'prefer', { ...base, remaining: 88, resetSeconds: 4 * 86_400 });
      const b = card('auth-02', 'credential-02.json', 'avoid', { ...base, remaining: 40, resetSeconds: 2 * 86_400 });
      // The team pool resets sooner, so a correct pool order puts it first even though it is listed second.
      const c = card('auth-03', 'credential-03.json', 'prefer', { ...base, remaining: 70, resetSeconds: 1 * 86_400 });
      const d = card('auth-04', 'credential-04.json', 'avoid', { ...base, remaining: 30, resetSeconds: 0.5 * 86_400 });
      // The backend sorts pools by their soonest reset; this payload mirrors that order so the
      // page can be checked for keeping it, not for inventing its own.
      return overview([
        pool({ state: 'advised', cards: [c, d], validUntil: d.reset_at_ms, plan: 'team', label: 'Team 300' }),
        pool({ state: 'advised', cards: [a, b], validUntil: b.reset_at_ms }),
      ], [], now);
    }
    case 'missingVsZero': {
      // A reported zero share and a missing share must not render the same: 0% is a reading,
      // a dash plus a reason is its absence.
      const a = card('auth-01', 'credential-01.json', 'limited', { ...base, remaining: 0, resetSeconds: 2 * 86_400, service: ['remaining_below_ten'] });
      const b = card('auth-02', 'credential-02.json', 'unavailable', { ...base, remaining: null, resetSeconds: 2 * 86_400 });
      return overview([pool({ state: 'limited', cards: [a, b], validUntil: a.reset_at_ms })], [], now);
    }    case 'mixed': {
      // One comparable pool beside one that cannot compare: the entry still reads balanced.
      const a = card('auth-01', 'credential-01.json', 'balanced', { ...base, remaining: 60, resetSeconds: 4 * 86_400 });
      const b = card('auth-02', 'credential-02.json', 'balanced', { ...base, remaining: 58, resetSeconds: 4 * 86_400 });
      const c = card('auth-03', 'credential-03.json', 'unavailable', { ...base, remaining: 60, resetSeconds: 3 * 86_400, poolMismatch: true });
      const d = card('auth-04', 'credential-04.json', 'unavailable', { ...base, remaining: 70, resetSeconds: 10 * 86_400, windowKind: 'monthly', periodSeconds: MONTH_SECONDS, poolMismatch: true });
      return overview([
        pool({ state: 'balanced', cards: [a, b], validUntil: a.reset_at_ms }),
        pool({ state: 'unavailable', cards: [c, d], reasons: ['window_type_mismatch'], plan: 'team', label: 'Team 300' }),
      ], [], now);
    }
    case 'single':
      return overview([], [{ auth_index: 'auth-01', name: 'credential-01.json', reason_codes: ['pool_too_small'] }], now);
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
    type: 'codex',
    provider: 'codex',
    label: item.label ?? `Fixture ${item.key.toUpperCase()}`,
    status: 'ok',
    disabled: false,
    unavailable: false,
    runtime_only: false,
    account_type: 'oauth',
    id_token: {
      chatgpt_account_id: `chatgpt-${prefix}-${item.key}`,
      plan_type: item.planType ?? 'pro',
      chatgpt_subscription_active_until: Math.floor((seedMS + (item.subscriptionDays ?? 25) * 86_400_000) / 1000),
    },
    success: 1,
    failed: 0,
    models: [{ id: 'gpt-5', display_name: 'GPT-5' }],
    priority: 1,
    weight: 1,
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
    usage(authIndex) {
      const item = byKey.get(authIndex);
      if (!item) return undefined;
      return usagePayload(item, seedMS);
    },
    subscription(authIndex) {
      const item = byKey.get(authIndex);
      if (!item || item.subscriptionDays == null) return undefined;
      return {
        plan_type: item.planType ?? 'pro',
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
  };
}

function usagePayload(item, seedMS) {
  const rateLimit = {};
  if (item.longWindow !== false) {
    rateLimit.secondary_window = {
      used_percent: 100 - item.remaining,
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
    plan_type: item.planType ?? 'pro',
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
        resetSeconds: 4 * 86_400 + 7 * 3_600 + 24 * 60, subscriptionDays: 25, resetCredits: 0,
      },
      {
        key: 'b', name: 'codex-demo-b.json', label: 'codex-demo-b', remaining: 40,
        resetSeconds: 2 * 86_400 + 2 * 3_600 + 26 * 60, subscriptionDays: 15, resetCredits: 1,
      },
    ],
  },
};