#!/usr/bin/env node
// Seeds the experience environment's repository-key page through the real management API: two pending requests (one a re-enable),
// one awaiting delivery, two active keys and three stopped ones (rotation, misuse, leak). Fake data only (`example/...`), and the
// fake CPA's page-bearing plugin is stopped so the rail carries its twenty fixed entries.
//   node scripts/acceptance/capabilities-nav/experience-seed.mjs [app url] [binary]
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { apiClient, disableFixturePlugin } from './lib.mjs';
import { seedRepoKeys, stateCounts, workDir } from './seed.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const app = process.argv[2] || 'http://127.0.0.1:18491/omc';
const binary = process.argv[3] || path.resolve(here, '../../../tmp/omc-nav.exe');
const api = await apiClient(app);
try {
  await disableFixturePlugin(api);
  const existing = await stateCounts(api);
  if (existing.keys > 0) {
    console.log('already seeded:', JSON.stringify(existing));
  } else {
    await seedRepoKeys(api, { binary, home: workDir('omchome-experience') });
    console.log('seeded:', JSON.stringify(await stateCounts(api)));
  }
} finally {
  await api.dispose();
}
