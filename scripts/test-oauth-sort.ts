import assert from 'node:assert/strict';
import test from 'node:test';
import {
  parseOAuthManagementView,
  parseOAuthWorkspaceQuery,
  sortOAuthWorkspaceRecords,
} from '../web/src/pages/oauthManagement/oauthWorkspaceLogic';
import type { AuthFileSortKey } from '../web/src/components/authFiles/authFileLogic';
import type { QuotaItem, QuotaWindow } from '../web/src/types/quota';
import { createOAuthSortRecords, legacyOAuthSort, OAUTH_SORT_KEYS } from './fixtures/oauth-sort';

test('every OAuth sort preserves the baseline order, stable ties and input identity', () => {
  for (const count of [0, 1, 12, 120, 2300]) {
    const records = createOAuthSortRecords(count);
    const names = ['A2', 'a02', 'á2', 'a10', 'İ2', 'i2', 'é', 'e\u0301', '中2', '中10', '', '  '];
    if (count === 12) records.forEach((record, index) => { record.file.name = names[index]; });
    for (const record of records) { Object.freeze(record.file); Object.freeze(record); }
    Object.freeze(records);
    for (const sort of [...OAUTH_SORT_KEYS, 'unknown' as AuthFileSortKey]) {
      const actual = sortOAuthWorkspaceRecords(records, sort);
      assert.notEqual(actual, records);
      assert.deepEqual(actual, legacyOAuthSort(records, sort), `${count} records / ${sort}`);
      assert.ok(actual.every(record => records.includes(record)), 'retain record identities');
    }
  }
});

test('name ordering is numeric and base-sensitive with stable equivalent labels', () => {
  const records = createOAuthSortRecords(4);
  ['a10', 'A2', 'á2', 'a02'].forEach((name, index) => { records[index].file.name = name; });
  assert.deepEqual(sortOAuthWorkspaceRecords(records, 'name-asc').map(record => record.file.name), ['A2', 'á2', 'a02', 'a10']);
  assert.deepEqual(sortOAuthWorkspaceRecords(records, 'name-desc').map(record => record.file.name), ['a10', 'A2', 'á2', 'a02']);
});

test('numeric ranking keeps absent priority and weight defaults and name tiebreaks', () => {
  const records = createOAuthSortRecords(3);
  ['credential-10', 'credential-2', 'credential-1'].forEach((name, index) => { records[index].file.name = name; });
  records[0].file.priority = undefined; records[1].file.priority = 0; records[2].file.priority = 1;
  records[0].file.weight = undefined; records[1].file.weight = 1; records[2].file.weight = 0;
  records[0].file.success = 1; records[0].file.failed = 1;
  records[1].file.success = 2; records[1].file.failed = 0;
  records[2].file.success = 0; records[2].file.failed = 0;
  records.forEach((record) => {
    record.requestTelemetry.totalRequests = record.file.success + record.file.failed;
    record.requestTelemetry.success = record.file.success;
    record.requestTelemetry.failed = record.file.failed;
  });
  const indexes = (sort: AuthFileSortKey) => sortOAuthWorkspaceRecords(records, sort).map(record => records.indexOf(record));
  assert.deepEqual(indexes('priority-desc'), [2, 1, 0]);
  assert.deepEqual(indexes('weight-desc'), [1, 0, 2]);
  assert.deepEqual(indexes('requests-desc'), [1, 0, 2]);
});

const NOW = Date.UTC(2026, 10, 23, 12, 0);
const DAY = 86_400_000;
const HOUR = 3_600_000;

function resetWindow(kind: QuotaWindow['kind'], periodHours: number, resetInMS: number, remaining: number): QuotaWindow {
  return {
    id: `${kind}-${resetInMS}`,
    label: kind ?? 'window',
    kind,
    scope: 'standard',
    period_hours: periodHours,
    reset_at_ms: NOW + resetInMS,
    remaining_percent: remaining,
  };
}

function quotaWithWindows(authIndex: string, windows: QuotaWindow[]): QuotaItem {
  return {
    auth_index: authIndex,
    name: authIndex,
    type: 'codex',
    provider: 'codex',
    disabled: false,
    status: 'healthy',
    observed_at_ms: NOW,
    windows,
    recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
    capabilities: { refresh_supported: true, clear_cooldown_supported: false, reset_credit_supported: false },
    quota_exceeded: false,
  };
}

test('reset order takes the soonest long-window reset and ignores short windows', () => {
  const records = createOAuthSortRecords(5);
  ['E', 'D', 'C', 'B', 'A'].forEach((name, index) => { records[index].file.name = `${name}.json`; });
  records[0].quota = quotaWithWindows('E', [resetWindow('five_hour', 5, HOUR, 90)]);
  records[1].quota = quotaWithWindows('D', [resetWindow('weekly', 168, 9 * DAY, 70)]);
  records[2].quota = quotaWithWindows('C', [resetWindow('daily', 24, 3 * DAY, 60)]);
  // B's five-hour window resets first, but only the weekly window may decide the order.
  records[3].quota = quotaWithWindows('B', [
    resetWindow('five_hour', 5, HOUR, 90),
    resetWindow('weekly', 168, DAY, 70),
  ]);
  // A and B share an instant; the tighter remaining share goes first.
  records[4].quota = quotaWithWindows('A', [resetWindow('weekly', 168, DAY, 30)]);

  assert.deepEqual(
    sortOAuthWorkspaceRecords(records, 'reset-asc').map((record) => record.file.name),
    ['A.json', 'B.json', 'C.json', 'D.json', 'E.json'],
  );
});

test('reset order sinks credentials with no long window and keeps name order among them', () => {
  const records = createOAuthSortRecords(3);
  ['zeta', 'alpha', 'beta'].forEach((name, index) => { records[index].file.name = `${name}.json`; });
  records[0].quota = quotaWithWindows('zeta', [resetWindow('five_hour', 5, HOUR, 90)]);
  records[1].quota = quotaWithWindows('alpha', [resetWindow('five_hour', 5, 2 * HOUR, 90)]);
  records[2].quota = quotaWithWindows('beta', [resetWindow('weekly', 168, DAY, 40)]);

  assert.deepEqual(
    sortOAuthWorkspaceRecords(records, 'reset-asc').map((record) => record.file.name),
    ['beta.json', 'alpha.json', 'zeta.json'],
  );
});

test('the saved view accepts the reset sort and rejects an unknown one', () => {
  assert.equal(parseOAuthWorkspaceQuery(new URLSearchParams('sort=reset-asc')).sort, 'reset-asc');
  assert.equal(parseOAuthWorkspaceQuery(new URLSearchParams('sort=unknown')).sort, 'name-asc');
  assert.deepEqual(
    parseOAuthManagementView({ pageSize: 24, status: 'all', quota: 'all', sort: 'reset-asc' }),
    { pageSize: 24, status: 'all', quota: 'all', sort: 'reset-asc' },
  );
});
