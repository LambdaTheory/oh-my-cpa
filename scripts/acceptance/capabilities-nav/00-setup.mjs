// Provisions the two pairs of self-hosted environments (cases.md 0.3, step 5): seed B once (repository keys through the real
// API, plus the hidden-entry setting S1 stored before the change), stop it, copy its data directory to every other
// environment, then start them all. Pair 1 (B1 + H1) serves the read-only and ordinary cases; pair 2 (B2 + H2) is kept
// untouched for the write-for-write comparison (C-23). Ports stay inside 18481-18499.
//   node scripts/acceptance/capabilities-nav/00-setup.mjs
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { copyData, startEnvironment, stopEnvironment } from './env.mjs';
import { apiClient, disableFixturePlugin, writeJson } from './lib.mjs';
import { seedRepoKeys, stateCounts, workDir } from './seed.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const H_ROOT = path.resolve(here, '../../..');
const B_ROOT = process.env.T9_BASE_ROOT || 'C:/Users/lhw/AppData/Local/Temp/omc-t9-s8-base';
const H_BIN = path.join(H_ROOT, 'tmp/omc-nav.exe');
const B_BIN = path.join(B_ROOT, 'tmp/omc-nav.exe');
const DATA = workDir('data');

const ENVS = {
  b0: { root: B_ROOT, binary: B_BIN, data: path.join(DATA, 'b0'), appPort: 18484, cpaPort: 18485, controlPort: 18486 },
  b1: { root: B_ROOT, binary: B_BIN, data: path.join(DATA, 'b1'), appPort: 18484, cpaPort: 18485, controlPort: 18486 },
  h1: { root: H_ROOT, binary: H_BIN, data: path.join(DATA, 'h1'), appPort: 18481, cpaPort: 18482, controlPort: 18483 },
  b2: { root: B_ROOT, binary: B_BIN, data: path.join(DATA, 'b2'), appPort: 18497, cpaPort: 18498, controlPort: 18499 },
  h2: { root: H_ROOT, binary: H_BIN, data: path.join(DATA, 'h2'), appPort: 18494, cpaPort: 18495, controlPort: 18496 },
};
const S1 = ['/model-groups', '/api-keys'];
const summary = {};

// Phase 1: B, fresh data. The repository keys are made through the baseline's own API; S1 is stored by the baseline.
for (const role of ['b0', 'b1', 'h1', 'b2', 'h2']) stopEnvironment(role);
fs.rmSync(ENVS.b0.data, { recursive: true, force: true });
const b0 = await startEnvironment({ role: 'b0', ...ENVS.b0 });
const api = await apiClient(b0.app);
const seeded = await seedRepoKeys(api, { binary: B_BIN, home: workDir('omchome-seed') });
// Only what happened is recorded, not what the API answered: the answers carry one-time approval tokens that must not be kept.
summary.seed = { steps: seeded.steps.map((step) => step.name) };
const stored = await api.setHidden(S1);
summary.hiddenWriteStatus = stored.status;
summary.hiddenReadBack = await api.getHidden();
summary.countsOnB = await stateCounts(api);
console.log('seeded B:', JSON.stringify(summary.countsOnB), 'hidden:', JSON.stringify(summary.hiddenReadBack));
await api.dispose();
stopEnvironment('b0');

// Phase 2: one data directory for every environment.
for (const role of ['b1', 'h1', 'b2', 'h2']) copyData(ENVS.b0.data, ENVS[role].data);
const started = {};
for (const role of ['b1', 'h1', 'b2', 'h2']) {
  started[role] = await startEnvironment({ role, ...ENVS[role] });
  const client = await apiClient(started[role].app);
  await disableFixturePlugin(client);
  summary[`counts_${role}`] = await stateCounts(client);
  summary[`hidden_${role}`] = await client.getHidden();
  await client.dispose();
  console.log(role, started[role].app, JSON.stringify(summary[`counts_${role}`]), 'hidden:', JSON.stringify(summary[`hidden_${role}`]));
}
writeJson('setup-summary.json', { ...summary, ports: Object.fromEntries(Object.entries(ENVS).map(([role, entry]) => [role, [entry.appPort, entry.cpaPort, entry.controlPort]])), at: new Date().toISOString() });
console.log('environments ready');
