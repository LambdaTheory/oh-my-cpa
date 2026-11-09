import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { test } from 'node:test';
import { isolatedAppEnvironment } from './acceptance/environment.mjs';

test('original total evidence survives decode, SQLite and HTTP before TPS calculation', () => {
  const output = execFileSync('go', ['test', './internal/api', '-run', '^TestLegacyTotalEvidenceThroughDTO$', '-count=1', '-v'], {
    encoding: 'utf8', env: isolatedAppEnvironment({ ASSISTANT_NO_SEND: '1' }),
  });
  const fixture = output.match(/TPS_DTO (\[.*\])/);
  assert.ok(fixture, 'the Go regression did not emit its real DTOs');
  const result = execFileSync(process.execPath, ['--experimental-strip-types', '--import', './scripts/ts-resolve.mjs', '--input-type=module', '-e', `
    import fs from 'node:fs';
    import { eventTokensPerSecond } from './web/src/types/usageEventMetrics.ts';
    const items = JSON.parse(fs.readFileSync(0, 'utf8'));
    console.log(JSON.stringify(items.map(item => ({ id: item.request_id,
      include: eventTokensPerSecond(item, 'include_ttft'), exclude: eventTokensPerSecond(item, 'exclude_ttft'),
    }))));
  `], { encoding: 'utf8', input: fixture[1] });
  const rows = JSON.parse(result);
  assert.equal(rows.length, 6);
  const absent = basis => ({ tps: null, formatted: '—', basis });
  for (const row of rows) {
    if (['missing', 'zero', 'historical'].includes(row.id)) {
      assert.deepEqual(row.include, absent('unavailable_accounting'), row.id);
      assert.deepEqual(row.exclude, absent('unavailable_accounting'), row.id);
    } else {
      const tps = row.id === 'separate' ? 60 : 50;
      assert.deepEqual(row.include, { tps, formatted: `${tps.toFixed(2)} t/s`, basis: 'include_ttft' }, row.id);
      assert.deepEqual(row.exclude, row.id === 'without-reasoning'
        ? { tps: 100, formatted: '100.00 t/s', basis: 'exclude_ttft' }
        : absent('unavailable_reasoning'), row.id);
    }
  }
});
