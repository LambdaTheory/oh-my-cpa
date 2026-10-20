import assert from 'node:assert/strict';
import test from 'node:test';
import type { ManagementAuthFile } from '../web/src/types/managementAuthFile.ts';
import type { QuotaItem } from '../web/src/types/quota.ts';
import {
  buildOAuthWorkspaceProjection,
  buildQuotaRefreshTargets,
  consumeOneShotIntent,
  canonicalDisplayProviderKey,
  filterOAuthWorkspaceRecords,
  legacyOAuthManagementRedirect,
  matchesQuotaFilter,
  parseOAuthWorkspaceQuery,
  resolveAuthorizationProviderId,
  quotaRefreshOutcomeKind,
  sortOAuthWorkspaceRecords,
  updateWorkspaceSearch,
  workspaceProviderCounts,
  workspaceProviderOptions,
} from '../web/src/pages/oauthManagement/oauthWorkspaceLogic.ts';
import type { OAuthProviderChoice } from '../web/src/pages/oauthProviderLogic.ts';
import { pickCompactQuotaWindows, orderQuotaWindows, quotaWindowGroupKey, quotaWindowKindOf } from '../web/src/pages/quota/quotaWindowSelection.ts';
import { formatShortDateTime, quotaResetCountdown, quotaResetText, resetAccuracyMarker } from '../web/src/pages/quota/quotaFormat.ts';
import { matchesStatusFilter } from '../web/src/components/authFiles/authFileLogic.ts';
import type { TFunc } from '../web/src/i18n/index.tsx';

/** Stands in for the dictionary: the countdown assertions only need the arguments back. */
const echoT: TFunc = (key, vars) => (vars ? `${key}:${Object.values(vars).join('/')}` : key);

const choices: OAuthProviderChoice[] = [
  { id: 'anthropic', title: 'Claude', description: '', iconId: 'Claude', flow: 'manual-callback', loginLabel: '' },
  { id: 'codex', title: 'Codex', description: '', iconId: 'Codex', flow: 'manual-callback', loginLabel: '' },
  { id: 'kimi', title: 'Kimi', description: '', iconId: 'Kimi', flow: 'device', loginLabel: '' },
  { id: 'iflow', title: 'iFlow', description: '', iconId: '', flow: 'manual-callback', loginLabel: '', pluginId: 'iflow-auth' },
];

function file(overrides: Partial<ManagementAuthFile> = {}): ManagementAuthFile {
  return {
    name: 'credential.json',
    auth_index: 'index-1',
    type: 'codex',
    provider: 'codex',
    disabled: false,
    unavailable: false,
    runtime_only: false,
    success: 0,
    failed: 0,
    ...overrides,
  };
}

function quota(overrides: Partial<QuotaItem> = {}): QuotaItem {
  return {
    auth_index: 'index-1',
    name: 'credential.json',
    type: 'codex',
    provider: 'codex',
    disabled: false,
    status: 'healthy',
    observed_at_ms: 1,
    windows: [{ id: 'weekly', label: 'Weekly', kind: 'weekly', scope: 'standard', remaining_percent: 80 }],
    recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
    capabilities: { refresh_supported: true, clear_cooldown_supported: true, reset_credit_supported: true },
    quota_exceeded: false,
    ...overrides,
  };
}

test('join: auth files own membership and unique exact indexes join their quota', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file(), file({ name: 'second.json', auth_index: 'index-2' })],
    [quota(), quota({ auth_index: 'index-2', name: 'second.json' })],
    choices,
  );
  assert.equal(projection.records.length, 2);
  assert.equal(projection.records[0].quota?.auth_index, 'index-1');
  assert.equal(projection.records[0].quotaMatch, 'matched');
  assert.equal(projection.quotaOnly.length, 0);
});

test('join: no fallback is made from filename, email, provider or order', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file({ auth_index: 'actual', email: 'same@example.test' })],
    [quota({ auth_index: 'different', name: 'credential.json', provider: 'codex' })],
    choices,
  );
  assert.equal(projection.records[0].quota, undefined);
  assert.equal(projection.records[0].quotaMatch, 'missing-quota');
  assert.equal(projection.quotaOnly.length, 1);
  assert.equal(projection.quotaOnly[0].auth_index, 'different');
});

test('join: a missing or duplicate file index never becomes an ambiguous quota target', () => {
  const projection = buildOAuthWorkspaceProjection(
    [
      file({ name: 'one.json', auth_index: 'duplicate' }),
      file({ name: 'two.json', auth_index: 'duplicate' }),
      file({ name: 'runtime.json', auth_index: undefined, runtime_only: true }),
    ],
    [quota({ auth_index: 'duplicate' })],
    choices,
  );
  assert.equal(projection.records[0].quotaMatch, 'ambiguous-file-index');
  assert.equal(projection.records[0].quota, undefined);
  assert.equal(projection.records[0].canRefreshQuota, false);
  assert.equal(projection.records[2].quotaMatch, 'missing-index');
  assert.equal(projection.missingAuthIndexCount, 1);
  assert.deepEqual(projection.duplicateAuthIndexes, ['duplicate']);
});

test('join: duplicate quota observations are ambiguous and quota-only races stay diagnostics', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file({ auth_index: 'index-1' }), file({ name: 'other.json', auth_index: 'other' })],
    [quota({ auth_index: 'index-1' }), quota({ auth_index: 'index-1' }), quota({ auth_index: 'race' })],
    choices,
  );
  assert.equal(projection.records[0].quotaMatch, 'ambiguous-quota-index');
  assert.equal(projection.records[0].quota, undefined);
  assert.equal(projection.records[1].quotaMatch, 'missing-quota');
  assert.equal(projection.quotaOnly.length, 1, 'the racing index remains a diagnostic without creating a row');
});

test('identity: duplicate filenames disable writes even when auth indexes differ', () => {
  const projection = buildOAuthWorkspaceProjection(
    [
      file({ name: 'same.json', auth_index: 'one' }),
      file({ name: 'same.json', auth_index: 'two' }),
    ],
    [],
    choices,
  );
  assert.equal(projection.records.every((record) => record.canEditFile === false), true);
  assert.deepEqual(projection.duplicateFileNames, ['same.json']);
});

test('providers: Claude maps to anthropic while unknown providers remain selection-safe', () => {
  assert.equal(canonicalDisplayProviderKey(file({ type: 'claude', provider: undefined }), undefined), 'claude');
  assert.equal(resolveAuthorizationProviderId('claude', choices), 'anthropic');
  assert.equal(resolveAuthorizationProviderId('iflow', choices), 'iflow');
  assert.equal(resolveAuthorizationProviderId('mystery-plugin', choices), undefined);
  assert.notEqual(resolveAuthorizationProviderId('gemini', choices), 'antigravity');
});

test('quota conditions: capability is independent from observed health and authenticity', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file()],
    [quota({
      status: 'warning',
      capabilities: { refresh_supported: false, clear_cooldown_supported: false, reset_credit_supported: false },
    })],
    choices,
  );
  const record = projection.records[0];
  assert.equal(record.isQuotaUnsupported, true);
  assert.equal(record.isQuotaAttention, true);
  assert.equal(matchesQuotaFilter(record, 'unsupported'), true);
  assert.equal(matchesQuotaFilter(record, 'attention'), true);
  assert.equal(matchesQuotaFilter(record, 'warning'), true);
});

test('quota conditions: idle without evidence is unobserved, not exhausted or unlimited', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file()],
    [quota({ status: 'idle', windows: [], recommendation: { status: 'idle', priority: 'none', action: 'refresh', reason: '' } })],
    choices,
  );
  const record = projection.records[0];
  assert.equal(record.quotaCondition, 'unobserved');
  assert.equal(record.isQuotaAttention, false);
  assert.equal(matchesQuotaFilter(record, 'unobserved'), true);
});

test('quota conditions: an empty reading is unobserved without asking for another read', () => {
  // A provider that answered without publishing a window is a different status from an unread
  // credential, and the difference is carried by the status and the card's copy. Triage keeps both
  // in one bucket on purpose: an operator looking for a credential without a usable reading wants
  // them together, and only the card distinguishes which of the two happened.
  const projection = buildOAuthWorkspaceProjection(
    [file()],
    [quota({ status: 'unpublished', windows: [], recommendation: { status: 'unpublished', priority: 'low', action: 'none', reason: '' } })],
    choices,
  );
  const record = projection.records[0];
  assert.equal(record.quotaCondition, 'unobserved');
  assert.equal(record.isQuotaAttention, false);
  assert.equal(matchesQuotaFilter(record, 'unobserved'), true);
});

test('attention: stale is independently filterable and is not attention by itself', () => {
  const projection = buildOAuthWorkspaceProjection([file()], [quota({ status: 'stale' })], choices);
  const record = projection.records[0];
  assert.equal(record.isQuotaAttention, false);
  assert.equal(matchesQuotaFilter(record, 'stale'), true);
  assert.equal(matchesQuotaFilter(record, 'attention'), false);
});

test('filters and sorting preserve the shared collection semantics', () => {
  const projection = buildOAuthWorkspaceProjection(
    [
      file({ name: 'b.json', auth_index: 'b', success: 10, priority: 1 }),
      file({ name: 'a.json', auth_index: 'a', success: 2, priority: 9, email: 'alice@example.test' }),
    ],
    [quota({ auth_index: 'a', status: 'exhausted' }), quota({ auth_index: 'b', status: 'healthy' })],
    choices,
  );
  assert.deepEqual(
    filterOAuthWorkspaceRecords(projection.records, 'alice', 'all', 'all', 'all').map((record) => record.fileName),
    ['a.json'],
  );
  assert.deepEqual(
    sortOAuthWorkspaceRecords(projection.records, 'priority-desc').map((record) => record.fileName),
    ['a.json', 'b.json'],
  );
});

test('refresh planning deduplicates indexes and reports every skip reason', () => {
  const projection = buildOAuthWorkspaceProjection(
    [
      file({ name: 'ok.json', auth_index: 'ok' }),
      file({ name: 'disabled.json', auth_index: 'disabled', disabled: true }),
      file({ name: 'unsupported.json', auth_index: 'unsupported' }),
      file({ name: 'missing.json', auth_index: 'missing' }),
      file({ name: 'no-index.json', auth_index: undefined }),
      file({ name: 'duplicate-a.json', auth_index: 'duplicate' }),
      file({ name: 'duplicate-b.json', auth_index: 'duplicate' }),
    ],
    [
      quota({ auth_index: 'ok' }),
      quota({ auth_index: 'disabled' }),
      quota({ auth_index: 'unsupported', capabilities: { refresh_supported: false, clear_cooldown_supported: false, reset_credit_supported: false } }),
    ],
    choices,
  );
  const plan = buildQuotaRefreshTargets(projection.records);
  assert.deepEqual(plan.eligibleIndexes, ['ok']);
  assert.deepEqual(
    plan.skipped.map((item) => item.reason).sort(),
    ['ambiguous-auth-index', 'ambiguous-auth-index', 'credential-disabled', 'missing-auth-index', 'missing-quota-observation', 'refresh-unsupported'].sort(),
  );
});

test('mixed refresh: thirteen entries mean eleven targets and two disabled skips', () => {
  const files = Array.from({ length: 12 }, (_, index) => file({
    name: `Synthetic ${index}`, auth_index: `index-${index}`, disabled: index >= 10,
  }));
  const quotas = files.map((entry) => quota({ auth_index: entry.auth_index! }));
  const account = {
    id: 'opaque-monitor', label: 'ollama:opaque-monitor: Legacy account', enabled: true, has_cookie: true,
    attempt_count: 0, created_at_ms: 1, updated_at_ms: 1,
  };
  const projection = buildOAuthWorkspaceProjection(files, quotas, choices, [account], []);
  const plan = buildQuotaRefreshTargets(projection.records);
  assert.equal(projection.records.length, 13);
  assert.equal(plan.targetCount, 11);
  assert.deepEqual(plan.eligibleIndexes, files.slice(0, 10).map((entry) => entry.auth_index));
  assert.deepEqual(plan.ollamaAccountIds, ['opaque-monitor']);
  assert.deepEqual(plan.skipped.map((entry) => entry.reason), ['credential-disabled', 'credential-disabled']);
  assert.equal(projection.records.at(-1)?.file.name, 'Legacy account');
  assert.equal(buildQuotaRefreshTargets([projection.records.at(-1)!]).targetCount, 1, 'an unread monitor can still be refreshed');
  assert.equal(buildQuotaRefreshTargets(projection.records.slice(10, 12)).targetCount, 0);
});

test('query parsing clamps unknown state and keeps one-shot OAuth intent separate', () => {
  const parsed = parseOAuthWorkspaceQuery(new URLSearchParams(
    'provider=CODEX&q=ops&status=bogus&quota=attention&sort=weight-desc&page=-4&page_size=99&density=compact&action=connect&connect_provider=iflow&focus=quota&code=secret',
  ));
  assert.equal(parsed.provider, 'codex');
  assert.equal(parsed.query, 'ops');
  assert.equal(parsed.status, 'all');
  assert.equal(parsed.quota, 'attention');
  assert.equal(parsed.sort, 'weight-desc');
  assert.equal(parsed.page, 1);
  assert.equal(parsed.pageSize, 12);
  assert.equal(parsed.density, 'compact');
  const durable = consumeOneShotIntent(new URLSearchParams(
    'provider=codex&q=ops&density=compact&action=connect&connect_provider=iflow&focus=quota',
  ));
  assert.equal(durable.get('provider'), 'codex');
  assert.equal(durable.get('q'), 'ops');
  assert.equal(durable.get('density'), 'compact');
  assert.equal(durable.has('action'), false);
  assert.equal(durable.has('connect_provider'), false);
  assert.equal(durable.has('focus'), false);
});

test('legacy redirects preserve only documented navigation intent', () => {
  assert.equal(
    legacyOAuthManagementRedirect('/oauth', new URLSearchParams('provider=claude')),
    '/oauth-management?provider=claude&action=connect&connect_provider=anthropic',
  );
  assert.equal(
    legacyOAuthManagementRedirect('/oauth', new URLSearchParams('provider=codex&state=secret&code=abc')),
    '/oauth-management?provider=codex&action=connect&connect_provider=codex',
  );
  assert.equal(
    legacyOAuthManagementRedirect('/auth-files', new URLSearchParams('provider=claude&q=example')),
    '/oauth-management?provider=claude&q=example',
  );
  assert.equal(
    legacyOAuthManagementRedirect('/quota', new URLSearchParams('provider=codex&session_id=secret')),
    '/oauth-management?provider=codex&density=expanded&focus=quota',
  );
});

test('runtime-null quota arrays remain unobserved instead of crashing the workspace', () => {
  const malformed = {
    ...quota({ status: 'idle' }),
    windows: null,
    recommendation: null,
    capabilities: null,
  } as unknown as QuotaItem;
  const projection = buildOAuthWorkspaceProjection([file()], [malformed], choices);
  assert.equal(projection.records[0].quotaCondition, 'unobserved');
  assert.equal(projection.records[0].isQuotaUnsupported, true);
  assert.equal(projection.records[0].isQuotaAttention, false);
});

test('provider strip lists display families without duplicate or authorization-only ids', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file({ type: 'claude', provider: 'claude' }), file({ name: 'other.json', auth_index: 'two', type: 'mystery', provider: 'mystery' })],
    [],
    choices,
  );
  const options = workspaceProviderOptions(projection.records, choices);
  assert.equal(options.includes('all'), false);
  assert.equal(options.includes('anthropic'), false);
  assert.equal(options.includes('claude'), true);
  assert.equal(options.includes('iflow'), true, 'plugin authorization ids are displayable when they are their own family');
  assert.equal(options.includes('mystery'), true, 'unknown observed providers remain filterable');
});

test('auth-index joining is exact, so whitespace near-misses remain diagnostics', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file({ name: 'exact.json', auth_index: 'index-1' })],
    [quota({ auth_index: ' index-1' })],
    choices,
  );
  assert.equal(projection.records[0].quota, undefined);
  assert.equal(projection.records[0].quotaMatch, 'missing-quota');
  assert.equal(projection.quotaOnly[0].auth_index, ' index-1');
});

test('the all sentinel does not erase a literal search for the word all', () => {
  const updated = updateWorkspaceSearch(new URLSearchParams('provider=codex&q=old'), { q: 'all', page: null });
  assert.equal(updated.get('q'), 'all');
  assert.equal(updated.get('provider'), 'codex');
});

test('a compact row carries the five-hour and weekly limits of one group', () => {
  const picked = pickCompactQuotaWindows([
    { id: 'a-five', label: 'Gemini Models · Five Hour Limit Remaining', kind: 'five_hour', scope: 'group' },
    { id: 'a-week', label: 'Gemini Models · Weekly Limit Remaining', kind: 'weekly', scope: 'group' },
    { id: 'b-five', label: 'Claude Models · Five Hour Limit Remaining', kind: 'five_hour', scope: 'group' },
  ]);
  assert.deepEqual(picked.map((window) => window.id), ['a-five', 'a-week']);
});

test('a compact row fills its lines from whatever the provider returned', () => {
  assert.deepEqual(
    pickCompactQuotaWindows([{ id: 'week', label: 'Weekly', kind: 'weekly', scope: 'standard' }]).map((window) => window.id),
    ['week'],
    'a provider with one window shows that window rather than an empty line',
  );
  assert.deepEqual(
    pickCompactQuotaWindows([
      { id: 'day', label: 'Daily', kind: 'daily', scope: 'standard' },
      { id: 'month', label: 'Monthly', kind: 'monthly', scope: 'standard' },
      { id: 'other', label: 'Other', kind: 'custom', scope: 'standard' },
    ]).map((window) => window.id),
    ['day', 'month'],
    'a provider with neither kind is still represented by its own first windows',
  );
  assert.deepEqual(pickCompactQuotaWindows([]), []);
});

test('a compact row never shows the same window twice', () => {
  const five = { id: 'five', label: 'Five hour', kind: 'five_hour', scope: 'standard' } as const;
  const week = { id: 'week', label: 'Weekly', kind: 'weekly', scope: 'standard' } as const;
  const picked = pickCompactQuotaWindows([five, five, week]);
  assert.deepEqual(
    picked.map((window) => window.id),
    ['five', 'week'],
    'the same window reported twice spends one of the row’s two lines on a repeat',
  );
});

test('two readings of one window id are one window', () => {
  const picked = pickCompactQuotaWindows([
    { id: 'weekly', label: 'Weekly', kind: 'weekly', scope: 'standard', remaining_percent: 80 },
    { id: 'weekly', label: 'Weekly', kind: 'weekly', scope: 'standard', remaining_percent: 75 },
    { id: 'five_hour', label: 'Five hour', kind: 'five_hour', scope: 'standard' },
  ]);
  assert.deepEqual(
    picked.map((window) => window.id),
    ['five_hour', 'weekly'],
    'deduplication is by id, not by object identity or by kind',
  );
});

test('distinct windows that share a kind keep both of the row’s lines', () => {
  const picked = pickCompactQuotaWindows([
    { id: 'a-week', label: 'Family A · Weekly', kind: 'weekly', scope: 'group' },
    { id: 'b-week', label: 'Family A · Weekly (secondary)', kind: 'weekly', scope: 'group' },
  ]);
  assert.deepEqual(
    picked.map((window) => window.id),
    ['a-week', 'b-week'],
    'a kind is not an identity: two distinct limits both belong in the row',
  );
});

test('a daily limit leads its own weekly limit', () => {
  const daily = { id: 'devin_daily', label: 'daily', kind: 'daily', scope: 'standard' } as const;
  const weekly = { id: 'devin_weekly', label: 'weekly', kind: 'weekly', scope: 'standard' } as const;
  assert.deepEqual(
    orderQuotaWindows([weekly, daily]).map((window) => window.id),
    ['devin_daily', 'devin_weekly'],
    'the shorter limit binds first even when the provider lists the weekly one first',
  );
  assert.deepEqual(
    pickCompactQuotaWindows(orderQuotaWindows([weekly, daily])).map((window) => window.id),
    ['devin_daily', 'devin_weekly'],
  );
});

test('a countdown marks an instant the provider did not state exactly', () => {
  const now = 1_700_000_000_000;
  const inTwoHours = now + 2 * 3_600_000;
  assert.equal(resetAccuracyMarker('exact'), '');
  assert.equal(resetAccuracyMarker('derived'), '~');
  assert.equal(resetAccuracyMarker(undefined), '');
  assert.equal(
    quotaResetCountdown({ reset_at_ms: inTwoHours, reset_accuracy: 'exact' }, now, echoT),
    'quota.in_hours:2',
    'an exact instant reads as a plain countdown',
  );
  assert.equal(
    quotaResetCountdown({ reset_at_ms: inTwoHours, reset_accuracy: 'derived' }, now, echoT),
    '~quota.in_hours:2',
    'a derived instant keeps its marker rather than reading as a verified deadline',
  );
  assert.equal(
    quotaResetCountdown({ reset_at_ms: now - 60_000, reset_accuracy: 'exact' }, now, echoT),
    'quota.recovered',
  );
  assert.equal(
    quotaResetCountdown({ reset_at_ms: now - 60_000, reset_accuracy: 'derived', reset_label: '已恢复' }, now, echoT),
    '~已恢复',
    'a passed derived instant defers to what upstream stated instead of claiming recovery',
  );
  assert.equal(
    quotaResetCountdown({ reset_at_ms: now - 60_000, reset_accuracy: 'approximate' }, now, echoT),
    '',
    'nothing is claimed when upstream stated neither recovery nor a label',
  );
  assert.equal(
    quotaResetText({ reset_at_ms: inTwoHours, reset_accuracy: 'derived' }, now, echoT),
    `~${formatShortDateTime(inTwoHours)} · quota.in_hours:2`,
    'the tooltip keeps the marker too, so the two readings cannot disagree',
  );
});

test('a window without a kind is named from the period it covers', () => {
  const groupWindow = (periodHours: number) => ({ id: `w-${periodHours}`, label: 'Gemini Models · Five Hour Limit Remaining', scope: 'group' as const, period_hours: periodHours });
  assert.equal(quotaWindowKindOf(groupWindow(5)), 'five_hour');
  assert.equal(quotaWindowKindOf(groupWindow(24)), 'daily');
  assert.equal(quotaWindowKindOf(groupWindow(168)), 'weekly');
  assert.equal(quotaWindowKindOf(groupWindow(720)), 'monthly');
  assert.equal(quotaWindowKindOf(groupWindow(2)), undefined, 'an unknown period stays unnamed rather than being forced into a bucket');
  assert.equal(quotaWindowKindOf({ ...groupWindow(2), kind: 'custom' }), 'custom', 'an explicit kind always wins');
});

test('a grouped row is picked by period, not by the order the provider listed', () => {
  const weekly = { id: 'g-week', label: 'Gemini Models · Weekly Limit Remaining', scope: 'group' as const, period_hours: 168 };
  const fiveHour = { id: 'g-five', label: 'Gemini Models · Five Hour Limit Remaining', scope: 'group' as const, period_hours: 5 };
  const otherFamily = { id: 'c-five', label: 'Claude and GPT models · Five Hour Limit Remaining', scope: 'group' as const, period_hours: 5 };
  assert.deepEqual(
    pickCompactQuotaWindows([weekly, fiveHour, otherFamily]).map((window) => window.id),
    ['g-five', 'g-week'],
  );
});

const creditInfo = (available: number, applicable: number): QuotaItem['reset_credits'] => ({
  available_count: available,
  applicable_available_count: applicable,
});

test('redeeming is offered whenever the account holds a reset credit', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file({ type: 'codex', provider: 'codex' })],
    [quota({ reset_credits: creditInfo(2, 0) })],
    choices,
  );
  assert.equal(
    projection.records[0].canRedeemCredit,
    true,
    'an applicable count of zero gates nothing: Codex resets voluntarily and reports the outcome itself',
  );
});

test('applicability alone never offers a redemption the account cannot spend', () => {
  const projection = buildOAuthWorkspaceProjection(
    [file({ type: 'codex', provider: 'codex' })],
    [quota({ reset_credits: creditInfo(0, 3) })],
    choices,
  );
  assert.equal(projection.records[0].canRedeemCredit, false);
});

test('the redemption offer respects capability, disablement and identity ambiguity', () => {
  const noCreditSupport = buildOAuthWorkspaceProjection(
    [file({ type: 'claude', provider: 'claude' })],
    [quota({ capabilities: { refresh_supported: true, clear_cooldown_supported: true, reset_credit_supported: false }, reset_credits: creditInfo(1, 1) })],
    choices,
  );
  assert.equal(noCreditSupport.records[0].canRedeemCredit, false);

  const disabledFile = buildOAuthWorkspaceProjection(
    [file({ type: 'codex', provider: 'codex', disabled: true })],
    [quota({ reset_credits: creditInfo(1, 1) })],
    choices,
  );
  assert.equal(disabledFile.records[0].canRedeemCredit, false);

  const ambiguousIndex = buildOAuthWorkspaceProjection(
    [file({ name: 'one.json', auth_index: 'duplicate' }), file({ name: 'two.json', auth_index: 'duplicate' })],
    // The quota carries spendable credits so this case fails if identity protection
    // is; a quota without them would disable both records for the wrong reason.
    [quota({ auth_index: 'duplicate', reset_credits: creditInfo(2, 2) })],
    choices,
  );
  assert.deepEqual(ambiguousIndex.records.map((record) => record.canRedeemCredit), [false, false]);
});

test('the list summary and the status filter agree on what is healthy', () => {
  // The page counts "Active" and filters "enabled" over the same records. Both must read the
  // shared predicates, or a row could be counted active and filtered out of the enabled list.
  const statusMessage = 'token expired';
  const cases: Array<[string, ManagementAuthFile, string[]]> = [
    ['a plainly enabled file', file({ type: 'codex', provider: 'codex' }), ['all', 'enabled']],
    ['a disabled file', file({ disabled: true }), ['all', 'disabled']],
    ['an unavailable file', file({ unavailable: true }), ['all', 'problem']],
    ['a file whose status is error', file({ status: 'error' }), ['all', 'problem']],
    ['a file carrying a warning message', file({ status_message: statusMessage }), ['all', 'problem']],
    ['a file whose message is a healthy one', file({ status_message: 'healthy' }), ['all', 'enabled']],
  ];
  for (const [label, authFile, expected] of cases) {
    const projection = buildOAuthWorkspaceProjection([authFile], [], choices);
    const record = projection.records[0];
    for (const filter of ['all', 'enabled', 'disabled', 'problem'] as const) {
      const matched = filterOAuthWorkspaceRecords([record], '', 'all', filter, 'all').length === 1;
      assert.equal(
        matched,
        expected.includes(filter),
        `${label}: filter "${filter}" ${matched ? 'matched' : 'did not match'}`,
      );
    }
    // The summary strip is built from the same two predicates, so it can only agree.
    const countedActive = !record.file.disabled && matchesStatusFilter(record.file, 'enabled');
    assert.equal(
      countedActive,
      expected.includes('enabled'),
      `${label}: the Active count and the enabled filter disagree`,
    );
  }
});

test('a refresh is acknowledged or reported, chosen by whether it needs inspecting', () => {
  assert.equal(
    quotaRefreshOutcomeKind({ failed: 0, rateLimited: 0 }),
    'acknowledgement',
    'a run whose targets all answered is an acknowledgement that leaves on its own',
  );
  assert.equal(
    quotaRefreshOutcomeKind({ failed: 1, rateLimited: 0 }),
    'report',
    'a failure needs its per-target reason, so its toast stays until it is closed',
  );
  assert.equal(
    quotaRefreshOutcomeKind({ failed: 0, rateLimited: 2 }),
    'report',
    'a rate-limited target needs its retry time, so its toast stays until it is closed',
  );
});

test('one model’s limits form one family whatever period each covers', () => {
  // The label carries the period, so keying the family by label would split one model into
  // two and let the row and the Drawer pick from different sets.
  const fiveHour = {
    id: 'addl_0_p', label: 'gpt-reserve 5小时配额', kind: 'model_scoped', scope: 'model', model: 'gpt-reserve',
  } as const;
  const weekly = {
    id: 'addl_0_s', label: 'gpt-reserve 每周配额', kind: 'model_scoped', scope: 'model', model: 'gpt-reserve',
  } as const;
  assert.equal(quotaWindowGroupKey(fiveHour), quotaWindowGroupKey(weekly));
  assert.notEqual(
    quotaWindowGroupKey(weekly),
    quotaWindowGroupKey({ ...weekly, id: 'addl_1_s', model: 'o-series' }),
    'two models are still two families',
  );
});

test('a limit outside the plan’s windows is not folded into them', () => {
  const standard = { id: 'weekly', label: 'Weekly', kind: 'weekly', scope: 'standard' } as const;
  const codeReview = { id: 'code_review_weekly', label: '代码审查 每周配额', scope: 'code_review' } as const;
  assert.equal(quotaWindowGroupKey(standard), 'standard');
  assert.notEqual(
    quotaWindowGroupKey(codeReview),
    quotaWindowGroupKey(standard),
    'a code-review allowance is not one of the credential’s five-hour or weekly limits',
  );
  // The row carries the credential's own plan, not the allowance beside it.
  assert.deepEqual(
    pickCompactQuotaWindows(orderQuotaWindows([standard, codeReview])).map((window) => window.id),
    ['weekly'],
  );
});

test('a target nobody could refresh is reported, not counted as clean', () => {
  // A conflict is resolved before the run, so it must not be filed with the credentials that
  // were never eligible: a run whose every target was held elsewhere refreshed nothing, and
  // `skipped` is the count the clean acknowledgement is built from.
  const kindFor = (failed: number, rateLimited: number) => quotaRefreshOutcomeKind({ failed, rateLimited });
  assert.equal(kindFor(0, 3), 'report', 'a fully conflicted run is not an acknowledgement');
  assert.equal(kindFor(0, 0), 'acknowledgement');
});

test('an allowance outside the plan is one family, not one per window', () => {
  // Keyed by id as well, the code-review allowance's five-hour and weekly windows became two
  // families of one window each, so a credential whose only limits were those would show one
  // of them and hide the other.
  const fiveHour = { id: 'code_review_5h', label: '代码审查 5小时配额', scope: 'code_review' } as const;
  const weekly = { id: 'code_review_weekly', label: '代码审查 每周配额', scope: 'code_review' } as const;
  assert.equal(quotaWindowGroupKey(fiveHour), quotaWindowGroupKey(weekly));
  assert.notEqual(quotaWindowGroupKey(weekly), quotaWindowGroupKey({ id: 'weekly', label: 'Weekly', scope: 'standard' }));
  assert.deepEqual(
    pickCompactQuotaWindows(orderQuotaWindows([fiveHour, weekly])).map((window) => window.id),
    ['code_review_5h', 'code_review_weekly'],
    'both of an allowance’s windows belong in the row when the allowance is all it has',
  );
});

test('ollama: accounts project as distinct non-CPA records with strict action isolation', () => {
  const ollamaAccount = {
    id: 'ollama-test-1',
    label: 'Team Ollama',
    enabled: true,
    has_cookie: true,
    status: 'ok',
    attempt_count: 0,
    created_at_ms: 1000,
    updated_at_ms: 1000,
  };
  const ollamaQuota = {
    account_id: 'ollama-test-1',
    label: 'Team Ollama',
    enabled: true,
    status: 'ok',
    observed_at_ms: 2000,
    quota: {
      auth_index: 'ollama:ollama-test-1',
      name: 'Team Ollama',
      type: 'ollama',
      provider: 'ollama',
      disabled: false,
      status: 'healthy' as const,
      observed_at_ms: 2000,
      windows: [{ id: 'weekly', label: '7d', scope: 'standard', used_percent: 30, remaining_percent: 70 }],
      recommendation: { status: 'healthy' as const, priority: 'none' as const, action: 'none' as const, reason: '' },
      capabilities: { refresh_supported: false, clear_cooldown_supported: false, reset_credit_supported: false },
      quota_exceeded: false,
    },
  };

  const projection = buildOAuthWorkspaceProjection([file()], [quota()], choices, [ollamaAccount], [ollamaQuota]);
  assert.equal(projection.records.length, 2);

  const cpaRecord = projection.records.find((r) => r.source === 'cpa')!;
  const ollamaRecord = projection.records.find((r) => r.source === 'ollama')!;

  assert.equal(cpaRecord.key.startsWith('cpa:'), true);
  assert.equal(cpaRecord.canSelectForFileBatch, true);
  assert.equal(cpaRecord.canRefreshQuota, true);

  assert.equal(ollamaRecord.key, 'ollama:ollama-test-1');
  assert.equal(ollamaRecord.displayProvider, 'ollama');
  assert.equal(ollamaRecord.canSelectForFileBatch, true, 'Ollama records participate in source-aware selection');
  assert.equal(ollamaRecord.canToggleFile, true, 'Ollama collection can be toggled through its own API');
  assert.equal(ollamaRecord.canDownloadFile, false, 'Ollama accounts have no credential JSON download');
  assert.equal(ollamaRecord.canDeleteFile, true, 'Ollama deletion uses its own API');
  assert.equal(ollamaRecord.canRefreshQuota, true, 'Unified refresh dispatches to the dedicated Ollama endpoint');

  // Provider options and counts
  const options = workspaceProviderOptions(projection.records, choices);
  assert.equal(options.includes('ollama'), true);
  const counts = workspaceProviderCounts(projection.records);
  assert.equal(counts.ollama, 1);
  assert.equal(counts.all, 2);

  // Search filter matches Ollama label
  const searchResults = filterOAuthWorkspaceRecords(projection.records, 'Team Ollama', 'all', 'all', 'all');
  assert.equal(searchResults.length, 1);
  assert.equal(searchResults[0].key, 'ollama:ollama-test-1');
});

test('ollama request telemetry: requests-desc sort orders known CPA ahead of unknown Ollama telemetry without fabricating zero', () => {
  const cpaZero = file({ name: 'cpa-zero.json', success: 0, failed: 0 });
  const cpaActive = file({ name: 'cpa-active.json', success: 10, failed: 2 });
  const ollama1 = {
    id: 'ollama-acc-1',
    label: 'Same Label',
    enabled: true,
    has_cookie: true,
    attempt_count: 0,
    created_at_ms: 1000,
    updated_at_ms: 1000,
  };
  const ollama2 = {
    id: 'ollama-acc-2',
    label: 'Same Label',
    enabled: false,
    has_cookie: true,
    attempt_count: 0,
    created_at_ms: 1000,
    updated_at_ms: 1000,
  };

  const projection = buildOAuthWorkspaceProjection([cpaZero, cpaActive], [], choices, [ollama1, ollama2], []);
  assert.equal(projection.records.length, 4);

  const sorted = sortOAuthWorkspaceRecords(projection.records, 'requests-desc');
  const sortedKeys = sorted.map((r) => r.key);

  // 1. Known CPA with requests (12) must come first
  assert.equal(sorted[0].fileName, 'cpa-active.json');
  assert.equal(sorted[0].requestTelemetry.known, true);
  assert.equal(sorted[0].requestTelemetry.totalRequests, 12);

  // 2. Known CPA with 0 requests must precede unknown Ollama accounts
  assert.equal(sorted[1].fileName, 'cpa-zero.json');
  assert.equal(sorted[1].requestTelemetry.known, true);
  assert.equal(sorted[1].requestTelemetry.totalRequests, 0);

  // 3 & 4. Unknown Ollama accounts must NOT be treated as 0 requests and come after known CPA records,
  // ordered deterministically by name collator then stable key
  assert.equal(sortedKeys[2], 'ollama:ollama-acc-1');
  assert.equal(sorted[2].requestTelemetry.known, false);
  assert.equal(sorted[2].requestTelemetry.totalRequests, null);

  assert.equal(sortedKeys[3], 'ollama:ollama-acc-2');
  assert.equal(sorted[3].requestTelemetry.known, false);
  assert.equal(sorted[3].requestTelemetry.totalRequests, null);
});
