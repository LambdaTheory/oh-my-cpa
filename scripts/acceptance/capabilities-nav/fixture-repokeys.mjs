// DS-RK: a stateful stub of the repository-key management API (cases.md 0.7), fed unchanged to the baseline's
// panel on /api-keys and to the head's /repo-keys page. Reads are answered from the dataset; writes are recorded
// and applied to the dataset in memory only, never forwarded. All names are fake (`example/...`).
//
// The response shapes follow web/src/types/repoKeys.ts and the console's own client in web/src/api/client.ts.
export const NOW = Date.parse('2026-11-24T08:00:00Z');
const MINUTE = 60_000;
const HOUR = 3_600_000;
const DAY = 86_400_000;
const clone = (value) => JSON.parse(JSON.stringify(value));

export const SECRET_PLACEHOLDER = 'nav-check-secret';
export const CIPHERTEXT_PLACEHOLDER = 'age-encryption.org/v1 STUB-CIPHERTEXT-NOT-A-REAL-ENVELOPE';

const WINDOW_SPAN = { '24h': DAY, '7d': 7 * DAY, '30d': 30 * DAY };
const WINDOW_FACTOR = { '24h': 0.3, '7d': 1, '30d': 3.2 };

function usage(window, factor, { stoppedAfter } = {}) {
  const span = WINDOW_SPAN[window] ?? 7 * DAY;
  return {
    window, from_ms: NOW - span, to_ms: NOW,
    requests: Math.round(120 * factor), failed: Math.round(3 * factor), total_tokens: Math.round(45000 * factor),
    cost_nanos: Math.round(12_340_000_000 * factor), last_used_ms: factor === 0 ? 0 : NOW - 2 * HOUR,
    models: factor === 0 ? [] : [
      { model: 'gpt-5.2', requests: Math.round(80 * factor), failed: 1, tokens: Math.round(30000 * factor), cost_nanos: Math.round(8_000_000_000 * factor) },
      { model: 'claude-sonnet-4', requests: Math.round(40 * factor), failed: 2, tokens: Math.round(15000 * factor), cost_nanos: Math.round(4_340_000_000 * factor) },
    ],
    ...(stoppedAfter ? { stopped_after: stoppedAfter } : {}),
  };
}

const binding = (overrides) => ({
  repo_has_remote: true, created_at_ms: NOW - 10 * DAY, created_by: 'operator-a', status: 'active', ...overrides,
});

const KEYS = [
  binding({ id: 'rk-1', repo: 'example/alpha-old', key_mask: 'sk-…a1b2', key_fingerprint: 'ufp-1', alias: 'alpha key', last_recipient: 'agent-a', last_delivered_at_ms: NOW - 2 * DAY, usage_7d: usage('7d', 1) }),
  binding({ id: 'rk-2', repo: 'example/beta', key_mask: 'sk-…c3d4', key_fingerprint: 'ufp-2', usage_7d: usage('7d', 0) }),
  binding({ id: 'rk-3', repo: 'example/rotated', key_mask: 'sk-…e5f6', key_fingerprint: 'ufp-3', status: 'stopped', stop_category: 'rotate', stop_reason: 'scheduled rotation', stopped_at_ms: NOW - 3 * DAY, stopped_by: 'operator-b', usage_7d: usage('7d', 0) }),
  binding({ id: 'rk-4', repo: 'example/misused', key_mask: 'sk-…0718', key_fingerprint: 'ufp-4', status: 'stopped', stop_category: 'misuse', stop_reason: 'unexpected spend', stopped_at_ms: NOW - 4 * DAY, stopped_by: 'operator-b', usage_7d: usage('7d', 0) }),
  binding({ id: 'rk-5', repo: 'example/leaked', key_mask: 'sk-…9a9b', key_fingerprint: 'ufp-5', status: 'stopped', stop_category: 'leaked', stop_reason: 'pasted into a ticket', stopped_at_ms: NOW - 2 * DAY, stopped_by: 'operator-b', usage_7d: usage('7d', 0) }),
];

const application = (overrides) => ({
  repo_has_remote: true, agent: 'agent-a', device: 'host-1', purpose: 'ci build', kind: 'issue', status: 'pending', source: 'agent',
  public_key_fingerprint: 'SHA256:AAAA1111BBBB2222CCCC3333DDDD4444EEEE5555FFFF6666', ciphertext_ready: false, created_at_ms: NOW - HOUR, ...overrides,
});

const PENDING = [
  application({ id: 'ap-1', repo: 'example/alpha', purpose: 'release pipeline', kind: 'issue' }),
  application({ id: 'ap-2', repo: 'example/beta', purpose: 'nightly job', kind: 'reuse', agent: 'agent-b', public_key_fingerprint: 'SHA256:ZZZZ9999YYYY8888XXXX7777WWWW6666VVVV5555UUUU4444' }),
  application({ id: 'ap-3', repo: 'example/rotated', purpose: 're-enable after rotation', kind: 'reuse', request_kind: 'reenable', target_binding_id: 'rk-3', public_key_fingerprint: '' }),
  application({ id: 'ap-4', repo: 'example/delta', purpose: 'filed on behalf', kind: 'issue', source: 'console', agent: 'agent-d' }),
];
const AWAITING = [
  application({ id: 'aw-1', repo: 'example/gamma', status: 'approved', ciphertext_ready: true, key_mask: 'sk-…aa11', decided_by: 'operator-a', decided_at_ms: NOW - 2 * HOUR }),
  application({ id: 'aw-2', repo: 'example/epsilon', status: 'approved', ciphertext_ready: true, key_mask: 'sk-…bb22', decided_by: 'operator-a', decided_at_ms: NOW - 3 * HOUR }),
];

export const AUDIT_ON = { enabled: true, pending: 3, quarantined: 1, earliest_ms: NOW - HOUR, last_success_ms: NOW - 5 * MINUTE };
export const AUDIT_OFF = { enabled: false, pending: 0, quarantined: 0, earliest_ms: 0, last_success_ms: 0 };

/** @param {{ empty?: boolean, audit?: 'on'|'off' }} [options] */
export function createDataset({ empty = false, audit = 'on' } = {}) {
  const state = {
    keys: empty ? [] : clone(KEYS),
    pending: empty ? [] : clone(PENDING),
    awaiting: empty ? [] : clone(AWAITING),
    audit: clone(empty || audit === 'off' ? AUDIT_OFF : AUDIT_ON),
    /** When true the on-behalf submission answers "receiver already known, approved automatically". */
    autoApprove: false,
    writes: [],
    /** 'normal' answers from the dataset; 'hold' parks every request until released; 'fail' answers HTTP 500. */
    mode: 'normal',
    held: [],
  };
  const find = (list, id) => list.find((item) => item.id === id);
  const repoView = (repo) => {
    const hit = state.keys.find((key) => key.repo === repo);
    const view = { repo, repo_has_remote: true, usage: usage('7d', 0), stops: [] };
    if (repo === 'example/fresh' || !hit) return { ...view, state: 'none' };
    return { ...view, state: hit.status, key_mask: hit.key_mask, created_by: hit.created_by, created_at_ms: hit.created_at_ms };
  };
  const detail = (id, window) => {
    const key = find(state.keys, id);
    if (!key) return null;
    const stopped = key.status === 'stopped';
    return {
      key,
      stops: stopped ? [{ id: `st-${id}`, category: key.stop_category, reason: key.stop_reason, stopped_by: key.stopped_by, stopped_at_ms: key.stopped_at_ms }] : [],
      deliveries: key.last_recipient ? [{ id: `dl-${id}`, application_id: 'ap-old', agent: key.last_recipient, device: 'host-1', kind: 'issue', operator: 'operator-a', key_mask: key.key_mask, public_key_fingerprint: 'SHA256:AAAA1111BBBB2222CCCC3333DDDD4444EEEE5555FFFF6666', delivered_at_ms: key.last_delivered_at_ms }] : [],
      applications: [application({ id: `ap-hist-${id}`, repo: key.repo, status: 'approved', decided_by: 'operator-a', decided_at_ms: NOW - 9 * DAY, created_at_ms: NOW - 10 * DAY })],
      usage: usage(window, id === 'rk-1' ? (WINDOW_FACTOR[window] ?? 1) : 0, { stoppedAfter: id === 'rk-5' ? { since_ms: NOW - 2 * DAY, rejected: 7, cost_nanos: 0 } : undefined }),
    };
  };
  const review = (id) => {
    const found = find(state.pending, id) ?? find(state.awaiting, id);
    if (!found) return null;
    return {
      application: found, repo: repoView(found.repo),
      ...(id === 'ap-2' ? { previous_public_key_fingerprint: 'SHA256:OLD00000OLD11111OLD22222OLD33333OLD44444OLD55555' } : {}),
      public_key_changed: id === 'ap-2',
      impact: found.request_kind === 'reenable' ? 'reenable' : found.kind,
      approval_expired: false,
    };
  };

  /**
   * @param {URL} url
   * @param {string} method
   * @param {any} body
   * @returns {{ status: number, json: any } | null} null when the route is not part of the repository-key API
   */
  function handle(url, method, body) {
    const route = url.pathname.replace(/^.*\/api\/v1\/management\/repo-keys/, '');
    const recordWrite = () => state.writes.push({ method, route, body });
    const ok = (json) => ({ status: 200, json });
    if (method === 'GET') {
      if (route === '' || route === '/') {
        const q = (url.searchParams.get('q') ?? '').toLowerCase();
        const status = url.searchParams.get('status');
        const keys = state.keys.filter((key) => (!q || [key.repo, key.key_mask, key.last_recipient ?? '', key.alias ?? ''].some((value) => value.toLowerCase().includes(q))) && (!status || key.status === status));
        return ok({ keys, pending_applications: state.pending.length, awaiting_delivery: state.awaiting.length });
      }
      if (route === '/applications') return ok({ applications: clone(url.searchParams.get('awaiting') === 'true' ? state.awaiting : state.pending) });
      if (route === '/audit-push') return ok(clone(state.audit));
      if (route === '/repo-status') return ok({ repo: repoView(url.searchParams.get('repo') ?? '') });
      let match = route.match(/^\/applications\/([^/]+)\/review$/);
      if (match) { const value = review(decodeURIComponent(match[1])); return value ? ok(value) : { status: 404, json: { error: 'not found' } }; }
      match = route.match(/^\/([^/]+)$/);
      if (match) { const value = detail(decodeURIComponent(match[1]), url.searchParams.get('window') ?? '7d'); return value ? ok(value) : { status: 404, json: { error: 'not found' } }; }
      return null;
    }
    if (method === 'POST') {
      if (route === '/applications') {
        recordWrite();
        if (state.autoApprove) return ok({ application_id: 'ap-auto', status: 'approved', kind: 'issue' });
        state.pending.push(application({ id: 'ap-new', repo: body.repo, agent: body.agent, purpose: body.purpose, source: 'console' }));
        return ok({ application_id: 'ap-new', status: 'pending', kind: 'issue', approval_token: 'stubtoken', approval_url: 'http://127.0.0.1/omc/repo-key-apply/stubtoken', approval_expires_at_ms: NOW + DAY });
      }
      let match = route.match(/^\/applications\/([^/]+)\/approve$/);
      if (match) {
        recordWrite();
        const id = decodeURIComponent(match[1]);
        const at = state.pending.findIndex((item) => item.id === id);
        const [done] = at >= 0 ? state.pending.splice(at, 1) : [];
        if (done && done.request_kind !== 'reenable') state.awaiting.push({ ...done, status: 'approved', ciphertext_ready: true, key_mask: 'sk-…ff00' });
        return ok({ application: { ...done, status: 'approved' } });
      }
      match = route.match(/^\/applications\/([^/]+)\/reject$/);
      if (match) {
        recordWrite();
        const id = decodeURIComponent(match[1]);
        const at = state.pending.findIndex((item) => item.id === id);
        const [done] = at >= 0 ? state.pending.splice(at, 1) : [];
        return ok({ application: { ...done, status: 'rejected' } });
      }
      match = route.match(/^\/applications\/([^/]+)\/ciphertext$/);
      if (match) {
        recordWrite();
        const id = decodeURIComponent(match[1]);
        const at = state.awaiting.findIndex((item) => item.id === id);
        const [done] = at >= 0 ? state.awaiting.splice(at, 1) : [];
        return ok({ application_id: id, kind: done?.kind ?? 'issue', key_mask: done?.key_mask ?? 'sk-…ff00', algorithm: 'x25519-chacha20poly1305', ciphertext: CIPHERTEXT_PLACEHOLDER, instructions: 'Pipe this into `oh-my-cpa key accept`.' });
      }
      match = route.match(/^\/applications\/([^/]+)\/approval-link$/);
      if (match) {
        recordWrite();
        const id = decodeURIComponent(match[1]);
        return ok({ application: find(state.pending, id), approval_url: `http://127.0.0.1/omc/repo-key-apply/stub-${id}`, approval_token: `stub-${id}`, approval_expires_at_ms: NOW + DAY });
      }
      match = route.match(/^\/([^/]+)\/stop$/);
      if (match) {
        recordWrite();
        const key = find(state.keys, decodeURIComponent(match[1]));
        if (key) Object.assign(key, { status: 'stopped', stop_category: body.category, stop_reason: body.reason, stopped_at_ms: NOW, stopped_by: 'operator-a' });
        return ok({ key });
      }
      match = route.match(/^\/([^/]+)\/reenable-request$/);
      if (match) {
        recordWrite();
        const id = decodeURIComponent(match[1]);
        return ok({ application: application({ id: `ap-re-${id}`, repo: find(state.keys, id)?.repo ?? 'example/unknown', request_kind: 'reenable', target_binding_id: id, purpose: body.reason }) });
      }
      match = route.match(/^\/([^/]+)\/reveal$/);
      if (match) { recordWrite(); return ok({ key: SECRET_PLACEHOLDER }); }
      return null;
    }
    return null;
  }
  /** Lets every parked request through to the dataset's answer (the mode must already be back to 'normal'). */
  async function releaseHeld() {
    const parked = state.held.splice(0);
    for (const route of parked) {
      const request = route.request();
      const url = new URL(request.url());
      let body = {};
      const post = request.postData();
      if (post) { try { body = JSON.parse(post); } catch { body = {}; } }
      const answer = handle(url, request.method(), body);
      await route.fulfill({ status: answer?.status ?? 501, contentType: 'application/json', body: JSON.stringify(answer?.json ?? { error: 'stub gap' }) }).catch(() => undefined);
    }
  }
  return { state, handle, releaseHeld };
}

/**
 * Installs the stub on a browser context. Requests the dataset does not recognise are answered 501 and listed
 * in `unmatched`, so a gap in the stub is visible instead of silently falling through to the real application.
 */
export async function installRepoKeyStub(context, dataset, { log = [], readsOnly = false } = {}) {
  const unmatched = [];
  await context.route(/\/api\/v1\/management\/repo-keys(\/|\?|$)/, async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    // The one-time approval link has its own pair of routes and is not part of the dataset.
    if (/\/repo-keys\/apply-link\//.test(url.pathname)) return route.fallback();
    // Reads-only: writes go to the real application unchanged (the demonstration refuses them itself).
    if (readsOnly && request.method() !== 'GET') return route.fallback();
    if (dataset.state.mode === 'hold') { dataset.state.held.push(route); return undefined; }
    if (dataset.state.mode === 'fail') {
      log.push({ method: request.method(), path: url.pathname, search: url.search, body: request.postData() ?? null, matched: true });
      return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"stub failure"}' });
    }
    let body = {};
    const post = request.postData();
    if (post) { try { body = JSON.parse(post); } catch { body = {}; } }
    const answer = dataset.handle(url, request.method(), body);
    log.push({ method: request.method(), path: url.pathname, search: url.search, body: post ?? null, matched: Boolean(answer) });
    if (!answer) { unmatched.push(`${request.method()} ${url.pathname}${url.search}`); return route.fulfill({ status: 501, contentType: 'application/json', body: '{"error":"stub gap"}' }); }
    return route.fulfill({ status: answer.status, contentType: 'application/json', body: JSON.stringify(answer.json) });
  });
  return { unmatched, log };
}
