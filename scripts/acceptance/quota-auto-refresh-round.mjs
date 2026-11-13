#!/usr/bin/env node
// C-24 acceptance: one real automatic-refresh round over the three plugin credentials.
//
// The browser scenario turns the scheduler's policy off, so this check runs the
// scheduler for real: a fresh data directory, the interval policy set to five
// minutes, and two ticks (at 60s and 120s) inside one wait. The three plugin
// credentials have never been read, so the first tick reads each once; the second
// tick must find nothing due and send no further plugin POST. Meta is excluded by
// the scheduler and must show no attempt at all.
//
// The fixed 429 credential is disabled through the fixture: its whole-provider
// parking would otherwise race the Ollama reads this check asserts on.
//
// Run: node scripts/acceptance/quota-auto-refresh-round.mjs
//      (OMCPA_BROWSER_BINARY points at the Windows app binary; ASSISTANT_NO_SEND=1.)

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createFakeCpaServer, FAKE_CPA_MANAGEMENT_KEY } from '../fake-cpa.mjs';
import { isolatedAppEnvironment } from './environment.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const OLLAMA_INDEX = 'auth-index-e2e-ollama';
const COMMAND_CODE_INDEX = 'auth-index-e2e-command-code';
const OPENCODE_INDEX = 'auth-index-e2e-opencode';
const RATE_LIMITED_INDEX = 'auth-index-e2e-rate-limited';
const META_INDEX = 'auth-index-e2e-meta';
const QUOTA_PLUGIN_PREFIX = '/v8/management/plugins/';

const checks = [];
function check(name, ok, detail = '') {
  checks.push({ name, ok: Boolean(ok), detail });
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${name}${detail ? ` — ${detail}` : ''}`);
}

function pluginPostsFor(requests, authIndex) {
  return requests.filter((entry) => {
    if (entry.method !== 'POST' || !entry.path.startsWith(QUOTA_PLUGIN_PREFIX) || !entry.path.endsWith('/quota')) return false;
    try {
      return (JSON.parse(entry.body)?.auth_index ?? '') === authIndex;
    } catch {
      return false;
    }
  });
}

async function freePort() {
  return await new Promise((resolve, reject) => {
    const server = http.createServer();
    server.once('error', reject);
    server.listen(0, '127.0.0.1', () => {
      const address = server.address();
      server.close((error) => (error ? reject(error) : resolve(address.port)));
    });
  });
}

async function waitFor(url, timeoutMs = 30000) {
  const deadline = Date.now() + timeoutMs;
  let lastError;
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url, { redirect: 'manual', signal: AbortSignal.timeout(1000) });
      if (response.status > 0) return response;
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`timed out waiting for ${url}: ${lastError?.message ?? 'no response'}`);
}

const binary = process.env.OMCPA_BROWSER_BINARY?.trim();
if (!binary) {
  console.error('OMCPA_BROWSER_BINARY must point at the app binary');
  process.exit(2);
}
const executable = path.resolve(root, binary);
if (!fs.existsSync(executable)) {
  console.error(`OMCPA_BROWSER_BINARY does not exist: ${executable}`);
  process.exit(2);
}

const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'omc-c24-'));
const appLog = [];
const fakeCpa = createFakeCpaServer();
fakeCpa.fixture.setCredentialDisabled(RATE_LIMITED_INDEX, true);
let appProcess;

try {
  await new Promise((resolve, reject) => {
    fakeCpa.server.once('error', reject);
    fakeCpa.server.listen(0, '127.0.0.1', resolve);
  });
  const cpaPort = fakeCpa.server.address().port;
  const appPort = await freePort();
  const appURL = `http://127.0.0.1:${appPort}/omc`;

  appProcess = spawn(executable, [], {
    cwd: root,
    env: isolatedAppEnvironment({
      OMCPA_LISTEN_ADDR: `127.0.0.1:${appPort}`,
      OMCPA_BASE_PATH: '/omc',
      OMCPA_DATA_DIR: path.join(temporary, 'data'),
      OMCPA_MASTER_KEY: 'fixture-master-key-for-the-auto-refresh-check',
      OMCPA_CPA_BASE_URL: `http://127.0.0.1:${cpaPort}`,
      OMCPA_CPA_MANAGEMENT_KEY: FAKE_CPA_MANAGEMENT_KEY,
      OMCPA_CPA_USAGE_ADDR: '',
      OMCPA_USAGE_INGEST_ENABLED: 'false',
      OMCPA_UPDATE_CHECK_ENABLED: 'false',
      OMCPA_UPDATE_CHECK_ON_PAGE_LOAD: 'false',
      OMCPA_PUBLIC_URL: '',
      OMCPA_VERSION: 'v0.1.0-c24',
    }),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  appProcess.stdout.on('data', (chunk) => appLog.push(chunk.toString()));
  appProcess.stderr.on('data', (chunk) => appLog.push(chunk.toString()));

  await waitFor(appURL);

  const login = await fetch(`${appURL}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password: FAKE_CPA_MANAGEMENT_KEY }),
  });
  const cookies = login.headers.getSetCookie?.() ?? [];
  const cookie = cookies.map((entry) => entry.split(';')[0]).join('; ');
  check('C-24 sign-in succeeds', login.status === 200 && cookie.includes('omc_session='), `status=${login.status}`);

  const policy = await fetch(`${appURL}/api/v1/preferences/omc_quota_auto_refresh`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({ interval_minutes: 5, refresh_on_reset: false }),
  });
  check('C-24 interval policy set to five minutes', policy.status === 200, `status=${policy.status}`);

  const startMS = Date.now();
  const mark = fakeCpa.requests.length;

  // Two scheduler ticks: the loop's ticker starts at boot, so 135s from here
  // covers the 60s tick and the 120s tick.
  await new Promise((resolve) => setTimeout(resolve, 135000));

  const overviewResponse = await fetch(`${appURL}/api/v1/management/quota`, { headers: { Cookie: cookie } });
  const overview = await overviewResponse.json();
  const item = (authIndex) => (overview.quotas ?? []).find((entry) => entry.auth_index === authIndex);
  const slice = fakeCpa.requests.slice(mark);

  check(
    'C-24 first tick reads each of the three plugin credentials once and the second tick repeats none',
    pluginPostsFor(slice, OLLAMA_INDEX).length === 1
      && pluginPostsFor(slice, COMMAND_CODE_INDEX).length === 1
      && pluginPostsFor(slice, OPENCODE_INDEX).length === 1,
    JSON.stringify({
      ollama: pluginPostsFor(slice, OLLAMA_INDEX).length,
      commandCode: pluginPostsFor(slice, COMMAND_CODE_INDEX).length,
      openCode: pluginPostsFor(slice, OPENCODE_INDEX).length,
    }),
  );
  check(
    'C-24 the automatic reads store fresh observations for the three',
    [OLLAMA_INDEX, COMMAND_CODE_INDEX, OPENCODE_INDEX].every((authIndex) => {
      const entry = item(authIndex);
      return entry?.last_refresh?.outcome === 'success'
        && entry?.observed_at_ms > startMS
        && entry?.last_success_at_ms === entry?.observed_at_ms;
    }),
    JSON.stringify([OLLAMA_INDEX, COMMAND_CODE_INDEX, OPENCODE_INDEX].map((authIndex) => {
      const entry = item(authIndex);
      return { auth_index: authIndex, observed_at_ms: entry?.observed_at_ms, last_refresh: entry?.last_refresh?.outcome };
    })),
  );
  check(
    'C-24 Meta is never read: no attempt, no snapshot and no upstream call',
    item(META_INDEX)?.last_refresh == null
      && item(META_INDEX)?.last_success_at_ms == null
      && (item(META_INDEX)?.windows ?? []).length === 0
      && pluginPostsFor(slice, META_INDEX).length === 0
      && slice.every((entry) => !String(entry.body ?? '').includes('api.meta.ai')),
    JSON.stringify({ last_refresh: item(META_INDEX)?.last_refresh ?? null, metaBodies: slice.filter((entry) => String(entry.body ?? '').includes('api.meta.ai')).length }),
  );
  check(
    'C-24 the round limited its work to the candidate list (no plugin POST for the disabled fixture)',
    pluginPostsFor(slice, RATE_LIMITED_INDEX).length === 0,
    JSON.stringify(slice.filter((entry) => entry.method === 'POST').map((entry) => entry.path)),
  );

  const roundLines = appLog.filter((line) => line.includes('automatic quota refresh round'));
  console.log(`INFO  scheduler round log lines: ${roundLines.length}`);
  for (const line of roundLines) console.log(`INFO  ${line.trim()}`);

  const failed = checks.filter((entry) => !entry.ok);
  console.log(`\n${checks.length - failed.length}/${checks.length} checks passed`);
  if (failed.length > 0) process.exitCode = 1;
} catch (error) {
  console.error(`C-24 run failed: ${error?.stack ?? error}`);
  process.exitCode = 1;
} finally {
  fs.writeFileSync(path.join(root, 'tmp', 'c24-app.log'), appLog.join(''));
  if (appProcess && appProcess.exitCode === null) {
    appProcess.kill();
    await new Promise((resolve) => {
      const timer = setTimeout(() => { appProcess.kill('SIGKILL'); resolve(); }, 5000);
      appProcess.once('exit', () => { clearTimeout(timer); resolve(); });
    });
  }
  await new Promise((resolve) => fakeCpa.server.close(resolve));
  try { fs.rmSync(temporary, { recursive: true, force: true }); } catch { /* best effort */ }
}
