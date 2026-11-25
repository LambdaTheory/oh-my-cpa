// Shared helpers for the left-navigation "capabilities" acceptance (delivery 2026-11-23-omc-capabilities-nav).
//
// Every script in this directory drives a real browser against a built binary served with the fake CPA
// (see scripts/acceptance/lending-experience-env.mjs) and tags each check with its case id (C-n) from the
// delivery's cases.md. Nothing here touches production, a real account or the application's source.
//
// The pages under test are addressed by environment variables so that one script can run against the
// baseline (B) and the pull-request head (H) with the same steps:
//   T9_APP        H's application URL, e.g. http://127.0.0.1:18481/omc
//   T9_BASE_APP   B's application URL
//   T9_DEMO       H's demonstration URL (no base path), T9_BASE_DEMO for B
//   T9_OUT        output directory for screenshots and JSON (default: the delivery's acceptance directory)
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { chromium, request as playwrightRequest } from 'playwright-core';
import { closeBrowser, launchBrowser } from '../lifecycle.mjs';
import { until } from '../harness.mjs';

export { until };

const here = path.dirname(fileURLToPath(import.meta.url));
const repoRoot = path.resolve(here, '../../..');
export const DELIVERY = path.join(repoRoot, 'docs/delivery/2026-11-23-omc-capabilities-nav/acceptance');
export const OUT = process.env.T9_OUT ? path.resolve(process.env.T9_OUT) : DELIVERY;
export const SHOTS = path.join(OUT, 'shots');
export const NET = path.join(OUT, 'net', 's8');
fs.mkdirSync(SHOTS, { recursive: true });
fs.mkdirSync(NET, { recursive: true });

export const EDGE = process.env.T9_EDGE || 'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe';
export const PASSWORD = 'omc-e2e-management-key';
export const APP = (process.env.T9_APP || 'http://127.0.0.1:18481/omc').replace(/\/$/, '');
export const BASE_APP = (process.env.T9_BASE_APP || 'http://127.0.0.1:18484/omc').replace(/\/$/, '');
export const DEMO = (process.env.T9_DEMO || 'http://127.0.0.1:18487').replace(/\/$/, '');
export const BASE_DEMO = (process.env.T9_BASE_DEMO || 'http://127.0.0.1:18488').replace(/\/$/, '');

export const LANGS = ['zh', 'en', 'zh-Hant', 'ms'];
export const LANGUAGE_NAMES = { zh: '简体中文', en: 'English', 'zh-Hant': '繁體中文', ms: 'Bahasa Melayu' };

/** Target names from PRD 5.3, per language. */
export const NAMES = {
  zh: { group: '能力', modelGroups: '模型组', repoKeys: '仓库自有密钥', lending: '额度拆借' },
  en: { group: 'Capabilities', modelGroups: 'Model Groups', repoKeys: 'Repository keys', lending: 'Quota lending' },
  'zh-Hant': { group: '能力', modelGroups: '模型組', repoKeys: '倉庫自有金鑰', lending: '額度拆借' },
  ms: { group: 'Keupayaan', modelGroups: 'Kumpulan Model', repoKeys: 'Kunci repositori', lending: 'Pinjaman kuota' },
};

// ---------------------------------------------------------------------------------------------
// Evidence hygiene
// ---------------------------------------------------------------------------------------------

/**
 * The environments are fake, but what they generate (a repository key in the clear, a one-time approval
 * token, a 32-digit hexadecimal id) is shaped like a real secret and trips the secret scan when it lands
 * in a file. Everything this suite writes or prints goes through here first: a value of those shapes keeps
 * its first six characters and is padded with asterisks to the same length. The console's own masks (a key
 * drawn with dots) have another shape and pass through unchanged.
 */
const SECRET_SHAPES = [
  /sk-[A-Za-z0-9_-]{12,}/g,
  /(?<=\/(?:repo-key-apply|apply-link)\/)[A-Za-z0-9_-]{16,}/g,
  /(?<="(?:approval_token|approvalToken|token|plaintext|ciphertext|secret|password|api_key|apiKey)"\s*:\s*")[^"\\]{16,}(?=")/g,
  /\b[0-9a-fA-F]{32}\b/g,
];

export function maskSecrets(text) {
  let masked = String(text);
  for (const shape of SECRET_SHAPES) masked = masked.replace(shape, (value) => `${value.slice(0, 6)}${'*'.repeat(value.length - 6)}`);
  return masked;
}

// ---------------------------------------------------------------------------------------------
// Result recording
// ---------------------------------------------------------------------------------------------

/** Collects named checks per case and writes one JSON file per script. A case passes only if every check passed. */
export class Results {
  constructor(script) {
    this.script = script;
    this.cases = new Map();
    this.startedAt = new Date().toISOString();
  }

  /** @param {string} id @param {string} title @param {'P0'|'P1'|'P2'} [priority] */
  begin(id, title, priority = '') {
    const existing = this.cases.get(id);
    if (existing) return existing;
    const entry = {
      id, title, priority, checks: [], notes: [], evidence: [],
      check(name, ok, detail = '') {
        const passed = Boolean(ok);
        entry.checks.push({ name, ok: passed, detail: String(detail ?? '') });
        console.log(maskSecrets(`${passed ? 'PASS' : 'FAIL'}  ${id}  ${name}${detail ? `  - ${String(detail).slice(0, 400)}` : ''}`));
        return passed;
      },
      note(text) { entry.notes.push(String(text)); console.log(maskSecrets(`NOTE  ${id}  ${text}`)); },
      evidenceFile(file) { entry.evidence.push(path.relative(OUT, file).split(path.sep).join('/')); },
    };
    this.cases.set(id, entry);
    return entry;
  }

  summary() {
    const rows = [];
    for (const entry of this.cases.values()) {
      const failed = entry.checks.filter((check) => !check.ok);
      rows.push({
        id: entry.id, title: entry.title, priority: entry.priority,
        result: entry.checks.length === 0 ? 'no-checks' : failed.length === 0 ? 'pass' : 'fail',
        checks: entry.checks.length, failed: failed.map((check) => `${check.name}: ${check.detail}`.slice(0, 600)),
        notes: entry.notes, evidence: entry.evidence,
      });
    }
    return rows;
  }

  finish(extra = {}) {
    const rows = this.summary();
    const only = process.env.T9_ONLY ? `-only-${process.env.T9_ONLY.replace(/[^A-Za-z0-9,-]/g, '').replace(/,/g, '_')}` : '';
    const file = path.join(NET, `results-${this.script}${only}.json`);
    const payload = { script: this.script, startedAt: this.startedAt, finishedAt: new Date().toISOString(), ...extra, cases: rows, details: [...this.cases.values()].map(({ id, checks }) => ({ id, checks })) };
    fs.writeFileSync(file, maskSecrets(JSON.stringify(payload, null, 2)));
    // A partial re-run of named cases replaces those cases in the script's full result file, and says so.
    if (only) {
      const full = path.join(NET, `results-${this.script}.json`);
      if (fs.existsSync(full)) {
        const merged = JSON.parse(fs.readFileSync(full, 'utf8'));
        for (const row of rows) {
          const index = merged.cases.findIndex((entry) => entry.id === row.id);
          if (index >= 0) merged.cases[index] = { ...row, rerun: payload.finishedAt }; else merged.cases.push({ ...row, rerun: payload.finishedAt });
        }
        for (const detail of payload.details) {
          const index = merged.details.findIndex((entry) => entry.id === detail.id);
          if (index >= 0) merged.details[index] = detail; else merged.details.push(detail);
        }
        merged.reruns = [...(merged.reruns ?? []), { cases: rows.map((row) => row.id), at: payload.finishedAt }];
        fs.writeFileSync(full, maskSecrets(JSON.stringify(merged, null, 2)));
      }
    }
    console.log('');
    for (const row of rows) console.log(`${row.result.toUpperCase().padEnd(9)} ${row.id}  ${row.title}${row.failed.length ? `  [${row.failed.length} failed]` : ''}`);
    console.log(`results written to ${path.relative(repoRoot, file)}`);
    if (rows.some((row) => row.result !== 'pass')) process.exitCode = 1;
    return rows;
  }
}

// ---------------------------------------------------------------------------------------------
// Browser, contexts, sessions
// ---------------------------------------------------------------------------------------------

export async function launch() {
  return launchBrowser(chromium, { executablePath: EDGE, headless: true });
}

export async function shutdown(browser) {
  await closeBrowser(browser).catch(() => {});
}

/**
 * A browser context with a signed-in session and the console's stored language and theme applied once,
 * at the first load of each tab. Later changes made through the console's own controls survive reloads.
 */
export async function newContext(browser, app, {
  lang = 'zh', theme = 'omc-light', viewport = { width: 1440, height: 900 }, touch = false, signIn = true, timezoneId = 'Asia/Shanghai',
  deviceScaleFactor = 1,
} = {}) {
  const context = await browser.newContext({
    viewport, hasTouch: touch, isMobile: touch, reducedMotion: 'reduce', timezoneId, locale: 'en-US', serviceWorkers: 'block',
    deviceScaleFactor,
  });
  await context.addInitScript(({ lang: initialLang, theme: initialTheme }) => {
    if (!/^https?:$/.test(location.protocol)) return;
    try {
      if (!sessionStorage.getItem('t9-init')) {
        localStorage.setItem('omc-lang', initialLang);
        localStorage.setItem('omc-theme', initialTheme);
        sessionStorage.setItem('t9-init', '1');
      }
    } catch { /* storage unavailable: the console falls back to its defaults */ }
  }, { lang, theme });
  if (signIn) {
    const login = await context.request.post(`${app}/api/auth/login`, { data: { password: PASSWORD } });
    if (login.status() !== 200) throw new Error(`login against ${app} returned ${login.status()}`);
  }
  return context;
}

/** An API client signed in to `app`, for seeding and for reading state without a browser page. */
export async function apiClient(app) {
  const client = await playwrightRequest.newContext({ baseURL: `${app}/` });
  const login = await client.post(`${app}/api/auth/login`, { data: { password: PASSWORD } });
  if (login.status() !== 200) throw new Error(`api login against ${app} returned ${login.status()}`);
  const call = async (method, route, data) => {
    const response = await client.fetch(`${app}${route}`, { method, data, headers: data === undefined ? {} : { 'Content-Type': 'application/json' } });
    let body = null;
    const text = await response.text();
    try { body = text ? JSON.parse(text) : null; } catch { body = text; }
    return { status: response.status(), body };
  };
  return {
    get: (route) => call('GET', route),
    post: (route, data = {}) => call('POST', route, data),
    put: (route, data) => call('PUT', route, data),
    patch: (route, data) => call('PATCH', route, data),
    del: (route) => call('DELETE', route),
    /** Writes the hidden-entry preference (the list of hidden page addresses). */
    setHidden: (list) => call('PUT', '/api/v1/preferences/omc_nav_hidden', list),
    getHidden: async () => (await call('GET', '/api/v1/preferences')).body?.preferences?.omc_nav_hidden,
    dispose: () => client.dispose(),
  };
}

/** Disables the fake CPA's page-bearing plugin so the Plugins group carries only its two fixed entries (cases 0.3 step 6). */
export async function disableFixturePlugin(api) {
  return api.patch('/api/v1/management/plugins/fixture-logger/enabled', { enabled: false });
}

// ---------------------------------------------------------------------------------------------
// Request tracking and quiescence
// ---------------------------------------------------------------------------------------------

/**
 * Records every request a context makes (method, host, path, sorted query, body field names, status) and
 * tracks in-flight requests, so a test can wait on "nothing in flight for a quiet window" instead of a delay.
 */
export function trackRequests(context) {
  const state = { log: [], inflight: 0, lastActivity: Date.now(), marks: [] };
  const entryByRequest = new Map();
  context.on('request', (request) => {
    state.inflight += 1;
    state.lastActivity = Date.now();
    const url = new URL(request.url());
    let bodyKeys = [];
    const post = request.postData();
    if (post) {
      try { const parsed = JSON.parse(post); bodyKeys = parsed && typeof parsed === 'object' ? Object.keys(parsed).sort() : []; } catch { bodyKeys = ['<non-json>']; }
    }
    const entry = {
      method: request.method(), host: url.host, origin: url.origin, path: url.pathname,
      query: [...url.searchParams.entries()].sort(([a], [b]) => a.localeCompare(b)), bodyKeys, status: null, type: request.resourceType(), body: post ?? null,
    };
    entryByRequest.set(request, entry);
    state.log.push(entry);
  });
  const settle = (request, status) => {
    const entry = entryByRequest.get(request);
    if (entry && entry.status === null) entry.status = status;
    state.inflight = Math.max(0, state.inflight - 1);
    state.lastActivity = Date.now();
  };
  context.on('requestfinished', async (request) => { const response = await request.response().catch(() => null); settle(request, response ? response.status() : 0); });
  context.on('requestfailed', (request) => settle(request, -1));
  return {
    state,
    /** Index to slice the log from, so a step can look at only its own requests. */
    mark: () => state.log.length,
    since: (index) => state.log.slice(index),
    /** Waits until nothing is in flight and no new request started for `quietMs`. */
    async quiet({ quietMs = 1000, timeoutMs = 30_000 } = {}) {
      await until(() => state.inflight === 0 && Date.now() - state.lastActivity >= quietMs, { label: `network quiet for ${quietMs}ms`, timeoutMs });
    },
  };
}

/** A request description comparable across environments: no host, ids replaced by placeholders. */
export function normalizeRequest(entry, { apiPrefix = /^\/(omc\/)?api/ } = {}) {
  const route = entry.path.replace(apiPrefix, '')
    .replace(/\/(rk|ap|aw)-[A-Za-z0-9-]+/g, '/<id>')
    .replace(/\/[0-9a-f]{8}-[0-9a-f-]{27,}/gi, '/<id>')
    .replace(/\/apply-link\/[^/]+/g, '/apply-link/<token>');
  return `${entry.method} ${route}${entry.query.length ? `?${entry.query.map(([k, v]) => `${k}=${v}`).join('&')}` : ''}`;
}

// ---------------------------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------------------------

/** Evidence files are written masked (see maskSecrets): no generated key, token or 32-digit hex id reaches a file whole. */
export function writeJson(name, value) {
  const file = path.join(NET, name);
  fs.writeFileSync(file, maskSecrets(JSON.stringify(value, null, 2)));
  return file;
}

export async function shot(page, name, { fullPage = false, clip } = {}) {
  const file = path.join(SHOTS, `${name}.png`);
  await page.screenshot({ path: file, fullPage, ...(clip ? { clip } : {}) });
  return file;
}

/** Screenshot taller than the usual viewport, so a long rail is pictured whole; the viewport is restored afterwards. */
export async function shotTall(page, name, height = 1250) {
  const before = page.viewportSize();
  await page.setViewportSize({ width: before.width, height });
  try {
    await until(async () => (await page.evaluate(() => window.innerHeight)) === height, { label: 'viewport resized' });
    return await shot(page, name);
  } finally {
    await page.setViewportSize(before);
  }
}

/** Case selection for re-running part of a script: T9_ONLY=C-40,C-41 runs only those cases. */
export const wants = (id) => !process.env.T9_ONLY || process.env.T9_ONLY.split(',').map((entry) => entry.trim()).includes(id);
/** A matcher for button and link text that tolerates the space antd inserts between two CJK characters ("驳 回"). */
export const spaced = (text) => new RegExp([...text].map((ch) => ch.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')).join('\\s*'));
export const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);
export const unique = (list) => new Set(list).size === list.length;
