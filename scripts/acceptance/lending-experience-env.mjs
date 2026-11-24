#!/usr/bin/env node
// Experience environment for the quota-lending first phase.
//
// Starts the in-process fake CPA with the `hero` scene (two Codex Pro 200 seats
// named codex-demo-a / codex-demo-b), starts the built Windows app against a
// durable data directory, turns the automatic quota policy off, and seeds one
// reading so the OAuth page shows the advice entry and the second-level page
// shows the two cards at the ratios the operator captured.
//
// Run: node scripts/acceptance/lending-experience-env.mjs
//      node scripts/acceptance/lending-experience-env.mjs --check
//      node scripts/acceptance/lending-experience-env.mjs --reseed
//
// The default mode keeps running until stopped. `--check` starts a private
// environment on 18441/18442, asserts the seeded numbers and the rendered entry
// and tag, then exits non-zero on any mismatch. `--reseed` asks the running
// default environment to re-anchor the hero scene at the current instant, which
// is also how the "two hours later it says balanced" drift is reset.

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium } from 'playwright-core';
import { createFakeCpaServer, FAKE_CPA_MANAGEMENT_KEY } from '../fake-cpa.mjs';
import { isolatedAppEnvironment } from './environment.mjs';
import { closeBrowser, closeServer, launchBrowser, stopProcess } from './lifecycle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const mode = process.argv.includes('--check') ? 'check' : process.argv.includes('--reseed') ? 'reseed' : 'run';
// The check run always uses the hero scene it asserts; a manual run may pick another fixture
// to inspect a scene the hero cards cannot express (for example two plans).
const HERO = mode === 'run' ? (process.env.OMC_LENDING_FIXTURE?.trim() || 'hero') : 'hero';
const PASSWORD = FAKE_CPA_MANAGEMENT_KEY;
const BINARY = path.resolve(root, process.env.OMCPA_BROWSER_BINARY?.trim() || 'tmp/omc-lending.exe');

function port(name, fallback) {
  const raw = process.env[name];
  const value = raw == null || raw === '' ? fallback : Number(raw);
  if (!Number.isInteger(value) || value <= 0) throw new Error(`${name} must be a positive integer`);
  return value;
}

const APP_PORT = port('OMC_LENDING_APP_PORT', mode === 'check' ? 18441 : 18431);
const CPA_PORT = port('OMC_LENDING_CPA_PORT', mode === 'check' ? 18442 : 18432);
const CONTROL_PORT = port('OMC_LENDING_CONTROL_PORT', 18433);
const dataDir = path.resolve(root, process.env.OMC_LENDING_DATA_DIR || (mode === 'check' ? 'tmp/lending-check-data' : 'tmp/lending-experience-data'));
const appURL = `http://127.0.0.1:${APP_PORT}/omc`;
const pageURL = `${appURL}/oauth-management?page_size=48`;
const lendingURL = `${appURL}/quota-lending`;
// A check run must not overwrite the long-running environment's state file, or the recorded
// PIDs and URL would point at the short-lived private ports.
// A second, one-off environment on another port must not overwrite the default one's state file.
const stateName = mode === 'check' ? 'lending-experience-env-check.json' : APP_PORT === 18431 ? 'lending-experience-env.json' : 'lending-experience-env-' + APP_PORT + '.json';
const statePath = path.join(root, 'tmp', stateName);
const logPath = path.join(root, 'tmp', 'lending-experience-app.log');

async function waitFor(url, timeoutMs = 60_000) {
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

function assertBinary() {
  if (!fs.existsSync(BINARY)) {
    console.error(`app binary does not exist: ${BINARY}`);
    console.error('build it first: pnpm build, then cross-compile cmd/oh-my-cpa into tmp/omc-lending.exe with docker golang:1.27.1');
    process.exit(2);
  }
}

async function startEnvironment() {
  assertBinary();
  fs.mkdirSync(dataDir, { recursive: true });
  const fakeCpa = createFakeCpaServer({ lendingFixture: HERO, lendingOnly: true });
  await new Promise((resolve, reject) => {
    fakeCpa.server.once('error', reject);
    fakeCpa.server.listen(CPA_PORT, '127.0.0.1', resolve);
  });

  const appLog = fs.createWriteStream(logPath, { flags: 'a' });
  const appProcess = spawn(BINARY, [], {
    cwd: root,
    env: isolatedAppEnvironment({
      OMCPA_LISTEN_ADDR: `127.0.0.1:${APP_PORT}`,
      OMCPA_BASE_PATH: '/omc',
      OMCPA_DATA_DIR: dataDir,
      OMCPA_MASTER_KEY: 'fixture-master-key-for-the-lending-experience',
      OMCPA_CPA_BASE_URL: `http://127.0.0.1:${CPA_PORT}`,
      OMCPA_CPA_MANAGEMENT_KEY: PASSWORD,
      OMCPA_CPA_USAGE_ADDR: '',
      OMCPA_USAGE_INGEST_ENABLED: 'false',
      OMCPA_UPDATE_CHECK_ENABLED: 'false',
      OMCPA_UPDATE_CHECK_ON_PAGE_LOAD: 'false',
      OMCPA_PUBLIC_URL: '',
      OMCPA_VERSION: 'v0.1.0-lending-experience',
      TZ: 'Asia/Shanghai',
    }),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  appProcess.stdout.on('data', (chunk) => appLog.write(chunk));
  appProcess.stderr.on('data', (chunk) => appLog.write(chunk));

  const stopApp = async () => {
    appLog.end();
    await stopProcess(appProcess, 5000);
    await closeServer(fakeCpa.server);
  };

  try {
    await waitFor(appURL);
    const login = await fetch(`${appURL}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ password: PASSWORD }),
    });
    if (login.status !== 200) throw new Error(`login returned ${login.status}`);
    const cookie = (login.headers.getSetCookie?.() ?? []).map((entry) => entry.split(';')[0]).join('; ');
    await fetch(`${appURL}/api/v1/preferences/omc_quota_auto_refresh`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json', Cookie: cookie },
      body: JSON.stringify({ interval_minutes: 0, refresh_on_reset: false }),
    });

    const seed = async () => {
      const response = await fetch(`${appURL}/api/v1/management/quota/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Cookie: cookie },
        body: JSON.stringify({ auth_indexes: fakeCpa.fixture.lendingAuthIndexes() }),
      });
      if (response.status !== 200) throw new Error(`seed refresh returned ${response.status}: ${await response.text()}`);
      return response.status;
    };
    await seed();

    return { fakeCpa, appProcess, cookie, seed, stop: stopApp };
  } catch (error) {
    await stopApp().catch(() => {});
    throw error;
  }
}
async function overview(environment) {
  const response = await fetch(`${appURL}/api/v1/management/quota`, {
    headers: { Cookie: environment.cookie },
  });
  if (response.status !== 200) throw new Error(`quota overview returned ${response.status}`);
  return response.json();
}

function findPool(lending) {
  return (lending?.pools ?? []).find((pool) => pool.provider === 'codex' && pool.plan_type === 'pro');
}

function findCard(pool, authIndex) {
  return (pool?.cards ?? []).find((card) => card.auth_index === authIndex);
}

function closeEnough(actual, expected, tolerance = 0.15) {
  return typeof actual === 'number' && Math.abs(actual - expected) <= tolerance;
}

function report(check, name, condition, detail = '') {
  check.total += 1;
  if (!condition) check.failed.push(name);
  console.log(`${condition ? 'PASS' : 'FAIL'}  ${name}${detail ? ` - ${detail}` : ''}`);
}
async function checkHero() {
  const environment = await startEnvironment();
  let browser;
  const check = { total: 0, failed: [] };
  try {
    const data = await overview(environment);
    const lending = data.lending;
    const pool = findPool(lending);
    const cardA = findCard(pool, 'auth-lend-hero-a');
    const cardB = findCard(pool, 'auth-lend-hero-b');

    report(check, 'pool is advised', pool?.state === 'advised', `state=${pool?.state}`);
    report(check, 'A is preferred', cardA?.role === 'prefer', `role=${cardA?.role}`);
    report(check, 'B is avoided', cardB?.role === 'avoid', `role=${cardB?.role}`);
    report(check, 'A daily is about 20.4', closeEnough(cardA?.daily_available_percent, 20.4), `daily=${cardA?.daily_available_percent}`);
    report(check, 'B daily is about 19.0', closeEnough(cardB?.daily_available_percent, 19.0), `daily=${cardB?.daily_available_percent}`);
    const difference = (cardA?.daily_available_percent ?? 0) - (cardB?.daily_available_percent ?? 0);
    report(check, 'daily difference is about 1.4', closeEnough(difference, 1.4), `difference=${difference.toFixed(2)}`);
    report(check, 'pool has a validity instant', typeof pool?.valid_until_ms === 'number', `valid_until_ms=${pool?.valid_until_ms}`);

    browser = await launchBrowser(chromium, { headless: true });
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, reducedMotion: 'reduce' });
    await context.addInitScript(() => {
      if (!/^https?:$/.test(location.protocol)) return;
      localStorage.setItem('omc-lang', 'zh');
      localStorage.setItem('omc-theme', 'omc-light');
    });
    const login = await context.request.post(`${appURL}/api/auth/login`, { data: { password: PASSWORD } });
    report(check, 'browser context login', login.status() === 200, `status=${login.status()}`);

    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (error) => errors.push(error.message));
    await page.goto(pageURL, { waitUntil: 'domcontentloaded' });
    const entry = page.locator('[data-lending-entry="advised"]');
    await entry.waitFor({ timeout: 20_000 });
    const entryText = (await entry.innerText()).replace(/[ ]+/g, ' ').trim();
    report(check, 'OAuth entry says one suggestion', entryText.includes('额度拆借 · 1 条建议'), entryText);
    const isLink = await entry.evaluate((node) => node.tagName === 'A' && Boolean(node.getAttribute('href')));
    report(check, 'entry is a link', isLink, entryText);

    const rowState = await page.waitForFunction(() => {
      const text = document.body.innerText;
      return text.includes('codex-demo-a') && text.includes('codex-demo-b') ? {
        fast: Array.from(document.querySelectorAll('*'))
          .filter((node) => node.getAttribute('data-fast-reset') === 'low')
          .map((node) => {
            let current = node;
            for (let depth = 0; depth < 10 && current; depth += 1, current = current.parentElement) {
              const content = current.textContent ?? '';
              if (content.includes('codex-demo-a') || content.includes('codex-demo-b')) {
                return content.includes('codex-demo-b') ? 'b' : 'a';
              }
            }
            return 'unknown';
          }),
      } : false;
    }, null, { timeout: 20_000 });
    const owners = await rowState.jsonValue();
    report(check, 'B row carries the fast-reset low tag', owners.fast.includes('b'), JSON.stringify(owners.fast));
    report(check, 'A row does not carry the fast-reset low tag', !owners.fast.includes('a'), JSON.stringify(owners.fast));
    const fastTitles = await page.locator('[data-fast-reset=low]').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('title')));
    report(check, 'fast-reset tag uses the low wording', fastTitles.some((title) => title === '快重置 · 余量低'), JSON.stringify(fastTitles));

    await page.goto(lendingURL, { waitUntil: 'domcontentloaded' });
    const poolNode = page.locator('[data-lending-pool]');
    await poolNode.waitFor({ timeout: 20_000 });
    report(check, 'lending page pool state is advised', await poolNode.getAttribute('data-lending-state') === 'advised');
    const dailyText = await page.locator('[data-lending-card="auth-lend-hero-a"] [data-lending-metric="daily"]').innerText();
    report(check, 'lending page shows A daily 20.4', dailyText.includes('20.4'), dailyText.replace(/[ ]+/g, ' ').trim());
    report(check, 'no page error', errors.length === 0, errors.join(' | '));
    const state = {
      url: pageURL,
      lendingUrl: lendingURL,
      password: PASSWORD,
      appPort: APP_PORT,
      fakeCpaPort: CPA_PORT,
      controlPort: CONTROL_PORT,
      dataDir,
      binary: BINARY,
      nodePid: process.pid,
      appPid: environment.appProcess.pid,
      checkedAt: new Date().toISOString(),
    };
    fs.mkdirSync(path.dirname(statePath), { recursive: true });
    fs.writeFileSync(statePath, JSON.stringify(state, null, 2) + String.fromCharCode(10));

    console.log('');
    console.log(`${check.total - check.failed.length}/${check.total} checks passed`);
    if (check.failed.length > 0) {
      console.error(`failed: ${check.failed.join(', ')}`);
      process.exitCode = 1;
    }
  } finally {
    if (browser) await closeBrowser(browser).catch(() => {});
    await environment.stop().catch(() => {});
  }
}

async function reseed() {
  const response = await fetch(`http://127.0.0.1:${CONTROL_PORT}/reseed`, { method: 'POST' });
  const body = await response.text();
  if (response.status !== 200) {
    console.error(`reseed failed (${response.status}): ${body}`);
    process.exit(1);
  }
  console.log(`reseeded the hero scene at ${new Date().toISOString()}`);
  console.log(body);
}
async function run() {
  const environment = await startEnvironment();
  const control = http.createServer(async (request, response) => {
    if (request.method === 'POST' && request.url === '/reseed') {
      try {
        environment.fakeCpa.fixture.setLendingFixture(HERO);
        const status = await environment.seed();
        response.writeHead(200, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ ok: true, seedStatus: status, seededAt: new Date().toISOString() }));
      } catch (error) {
        response.writeHead(500, { 'Content-Type': 'application/json' });
        response.end(JSON.stringify({ ok: false, error: String(error?.message ?? error) }));
      }
      return;
    }
    if (request.method === 'GET' && request.url === '/requests') {
      response.writeHead(200, { 'Content-Type': 'application/json' });
      response.end(JSON.stringify({
        requests: environment.fakeCpa.requests.map(({ method, path, query }) => ({ method, path, query })),
      }));
      return;
    }
    if (request.method === 'GET' && request.url === '/status') {
      response.writeHead(200, { 'Content-Type': 'application/json' });
      response.end(JSON.stringify({ ok: true, url: pageURL, lendingUrl: lendingURL }));
      return;
    }
    response.writeHead(404, { 'Content-Type': 'application/json' });
    response.end(JSON.stringify({ error: 'not found' }));
  });  await new Promise((resolve, reject) => {
    control.once('error', reject);
    control.listen(CONTROL_PORT, '127.0.0.1', resolve);
  });

  let stopping = false;
  const stop = async (code = 0) => {
    if (stopping) return;
    stopping = true;
    await new Promise((resolve) => control.close(resolve));
    await environment.stop().catch(() => {});
    process.exit(code);
  };
  process.on('SIGINT', () => { void stop(0); });
  process.on('SIGTERM', () => { void stop(0); });

  const state = {
    url: pageURL,
    lendingUrl: lendingURL,
    password: PASSWORD,
    appPort: APP_PORT,
    fakeCpaPort: CPA_PORT,
    controlPort: CONTROL_PORT,
    dataDir,
    binary: BINARY,
    nodePid: process.pid,
    appPid: environment.appProcess.pid,
    startedAt: new Date().toISOString(),
  };
  fs.mkdirSync(path.dirname(statePath), { recursive: true });
  fs.writeFileSync(statePath, JSON.stringify(state, null, 2) + String.fromCharCode(10));  console.log('lending experience environment ready');
  console.log(`URL:       ${pageURL}`);
  console.log(`lending:   ${lendingURL}`);
  console.log(`password:  ${PASSWORD}`);
  console.log(`fake CPA:  http://127.0.0.1:${CPA_PORT}`);
  console.log(`state:     ${path.relative(root, statePath)}`);
  console.log(`log:       ${path.relative(root, logPath)}`);
  console.log('reseed:    node scripts/acceptance/lending-experience-env.mjs --reseed');
  console.log(`stop:      stop this node process (PID ${process.pid}); the app (PID ${environment.appProcess.pid}) stops with it`);

  await new Promise(() => {});
}

try {
  if (mode === 'check') await checkHero();
  else if (mode === 'reseed') await reseed();
  else await run();
} catch (error) {
  console.error(`lending experience environment failed: ${error?.stack ?? error}`);
  process.exit(1);
}