#!/usr/bin/env node
// Local experience environment for the three-credential quota work.
//
// Starts the in-process fake CPA and the built Windows app against a durable
// data directory, signs in, turns the automatic policy off so the page stays
// still, and reads the three plugin credentials once so the quota page has a
// fresh reading to show. Keeps running until this process is stopped; stopping
// it stops the app too.
//
// Run: node scripts/acceptance/experience-env.mjs
//      (OMCPA_BROWSER_BINARY points at the app binary.)

import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createFakeCpaServer, FAKE_CPA_MANAGEMENT_KEY } from '../fake-cpa.mjs';
import { isolatedAppEnvironment } from './environment.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const BINARY = process.env.OMCPA_BROWSER_BINARY?.trim();
if (!BINARY) {
  console.error('OMCPA_BROWSER_BINARY must point at the app binary');
  process.exit(2);
}
const executable = path.resolve(root, BINARY);
if (!fs.existsSync(executable)) {
  console.error(`OMCPA_BROWSER_BINARY does not exist: ${executable}`);
  process.exit(2);
}

const CPA_PORT = Number(process.env.OMC_EXPERIENCE_CPA_PORT ?? 18321);
const APP_PORT = Number(process.env.OMC_EXPERIENCE_APP_PORT ?? 18322);
const dataDir = path.join(root, 'tmp', 'experience-data');
fs.mkdirSync(dataDir, { recursive: true });
const appURL = `http://127.0.0.1:${APP_PORT}/omc`;
const pageURL = `${appURL}/oauth-management?page_size=48`;

const appLog = fs.createWriteStream(path.join(root, 'tmp', 'experience-app.log'), { flags: 'a' });
const fakeCpa = createFakeCpaServer();
let appProcess;
let stopping = false;

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

async function stop(exitCode = 0) {
  if (stopping) return;
  stopping = true;
  if (appProcess && appProcess.exitCode === null) {
    appProcess.kill();
    await new Promise((resolve) => {
      const timer = setTimeout(() => { appProcess.kill('SIGKILL'); resolve(); }, 5000);
      appProcess.once('exit', () => { clearTimeout(timer); resolve(); });
    });
  }
  await new Promise((resolve) => fakeCpa.server.close(resolve));
  process.exit(exitCode);
}
process.on('SIGINT', () => { void stop(0); });
process.on('SIGTERM', () => { void stop(0); });

try {
  await new Promise((resolve, reject) => {
    fakeCpa.server.once('error', reject);
    fakeCpa.server.listen(CPA_PORT, '127.0.0.1', resolve);
  });

  appProcess = spawn(executable, [], {
    cwd: root,
    env: isolatedAppEnvironment({
      OMCPA_LISTEN_ADDR: `127.0.0.1:${APP_PORT}`,
      OMCPA_BASE_PATH: '/omc',
      OMCPA_DATA_DIR: dataDir,
      OMCPA_MASTER_KEY: 'fixture-master-key-for-the-experience-environment',
      OMCPA_CPA_BASE_URL: `http://127.0.0.1:${CPA_PORT}`,
      OMCPA_CPA_MANAGEMENT_KEY: FAKE_CPA_MANAGEMENT_KEY,
      OMCPA_CPA_USAGE_ADDR: '',
      OMCPA_USAGE_INGEST_ENABLED: 'false',
      OMCPA_UPDATE_CHECK_ENABLED: 'false',
      OMCPA_UPDATE_CHECK_ON_PAGE_LOAD: 'false',
      OMCPA_PUBLIC_URL: '',
      OMCPA_VERSION: 'v0.1.0-experience',
    }),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  appProcess.stdout.on('data', (chunk) => appLog.write(chunk));
  appProcess.stderr.on('data', (chunk) => appLog.write(chunk));
  appProcess.once('exit', (code) => {
    appLog.write(`[experience] app exited with ${code}\n`);
    void stop(code ?? 0);
  });

  await waitFor(appURL);

  const login = await fetch(`${appURL}/api/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ password: FAKE_CPA_MANAGEMENT_KEY }),
  });
  const cookie = (login.headers.getSetCookie?.() ?? []).map((entry) => entry.split(';')[0]).join('; ');
  await fetch(`${appURL}/api/v1/preferences/omc_quota_auto_refresh`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({ interval_minutes: 0, refresh_on_reset: false }),
  });
  const seeded = await fetch(`${appURL}/api/v1/management/quota/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Cookie: cookie },
    body: JSON.stringify({
      auth_indexes: ['auth-index-e2e-ollama', 'auth-index-e2e-command-code', 'auth-index-e2e-opencode'],
    }),
  });

  fs.writeFileSync(path.join(root, 'tmp', 'experience-env.json'), JSON.stringify({
    url: pageURL,
    password: FAKE_CPA_MANAGEMENT_KEY,
    fakeCpa: `http://127.0.0.1:${CPA_PORT}`,
    dataDir,
    nodePid: process.pid,
    appPid: appProcess.pid,
  }, null, 2));

  console.log(`experience environment ready (seed refresh status ${seeded.status})`);
  console.log(`URL:      ${pageURL}`);
  console.log(`password: ${FAKE_CPA_MANAGEMENT_KEY}`);
  console.log(`stop:     stop this node process (PID ${process.pid}); the app (PID ${appProcess.pid}) stops with it`);
  console.log('state:    tmp/experience-env.json · log: tmp/experience-app.log');

  await new Promise(() => {});
} catch (error) {
  console.error(`experience environment failed: ${error?.stack ?? error}`);
  await stop(1);
}
