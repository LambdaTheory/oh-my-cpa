import { spawn } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { createFakeCpaServer, FAKE_CPA_MANAGEMENT_KEY } from '../scripts/fake-cpa.mjs';
import { isolatedAppEnvironment } from '../scripts/acceptance/environment.mjs';

const root = path.resolve(import.meta.dirname, '..');
const executable = path.resolve(root, process.env.OMCPA_BROWSER_BINARY);
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'omc-probe-'));
const fakeCpa = createFakeCpaServer();
let app;

async function freePort() { return await new Promise((resolve) => { const s = http.createServer(); s.listen(0, '127.0.0.1', () => { const p = s.address().port; s.close(() => resolve(p)); }); }); }
async function waitFor(url) { for (let i = 0; i < 200; i += 1) { try { const r = await fetch(url, { redirect: 'manual' }); if (r.status > 0) return; } catch {} await new Promise((r) => setTimeout(r, 100)); } throw new Error('timeout'); }

try {
  await new Promise((resolve) => fakeCpa.server.listen(0, '127.0.0.1', resolve));
  const cpaPort = fakeCpa.server.address().port;
  const appPort = await freePort();
  const appURL = `http://127.0.0.1:${appPort}/omc`;
  app = spawn(executable, [], { cwd: root, env: isolatedAppEnvironment({
    OMCPA_LISTEN_ADDR: `127.0.0.1:${appPort}`, OMCPA_BASE_PATH: '/omc', OMCPA_DATA_DIR: path.join(temporary, 'data'),
    OMCPA_MASTER_KEY: 'probe-master-key-0123456789abcdef', OMCPA_CPA_BASE_URL: `http://127.0.0.1:${cpaPort}`,
    OMCPA_CPA_MANAGEMENT_KEY: FAKE_CPA_MANAGEMENT_KEY, OMCPA_CPA_USAGE_ADDR: '', OMCPA_USAGE_INGEST_ENABLED: 'false',
    OMCPA_UPDATE_CHECK_ENABLED: 'false', OMCPA_UPDATE_CHECK_ON_PAGE_LOAD: 'false', OMCPA_PUBLIC_URL: '', OMCPA_VERSION: 'probe',
  }), stdio: ['ignore', 'pipe', 'pipe'] });
  app.stdout.on('data', (chunk) => process.stderr.write(chunk));
  app.stderr.on('data', (chunk) => process.stderr.write(chunk));
  app.once('exit', (code) => console.error('app exited', code));
  await waitFor(appURL);
  const login = await fetch(`${appURL}/api/auth/login`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ password: FAKE_CPA_MANAGEMENT_KEY }) });
  const cookie = login.headers.getSetCookie().map((c) => c.split(';')[0]).join('; ');
  const post = (p, b) => fetch(`${appURL}/api/v1${p}`, { method: 'POST', headers: { 'Content-Type': 'application/json', Cookie: cookie }, body: JSON.stringify(b) }).then((r) => r.json());
  const get = (p) => fetch(`${appURL}/api/v1${p}`, { headers: { Cookie: cookie } }).then((r) => r.json());
  const item = async () => (await get('/management/quota')).quotas.find((q) => q.auth_index === 'auth-index-e2e-opencode');

  fakeCpa.fixture.setQuotaMode('ok');
  await post('/management/quota/refresh', { auth_index: 'auth-index-e2e-opencode' });
  const good = await item();
  console.log('OK state:', JSON.stringify({ status: good.status, observed: good.observed_at_ms, windows: good.windows?.length, percent: good.windows?.map((w) => w.remaining_percent) }));

  fakeCpa.fixture.setQuotaMode('missing');
  const refreshed = await post('/management/quota/refresh', { auth_index: 'auth-index-e2e-opencode' });
  console.log('missing refresh status:', JSON.stringify({ status: refreshed.status, quota_status: refreshed.quota?.status, windows: refreshed.quota?.windows?.map((w) => ({ label: w.label, percent: w.remaining_percent, period_hours: w.period_hours })) }));
  const sparse = await item();
  console.log('sparse state:', JSON.stringify({ status: sparse.status, observed: sparse.observed_at_ms, observed_age_ms: Date.now() - sparse.observed_at_ms, last_refresh: sparse.last_refresh, windows: sparse.windows?.map((w) => ({ label: w.label, percent: w.remaining_percent, period_hours: w.period_hours, stale: w.stale })) }));
} finally {
  if (app) app.kill();
  await new Promise((resolve) => fakeCpa.server.close(resolve));
  try { fs.rmSync(temporary, { recursive: true, force: true }); } catch {}
}
