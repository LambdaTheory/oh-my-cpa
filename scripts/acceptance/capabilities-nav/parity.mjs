// The side-by-side harness for the repository-key panel: B (the lower half of /api-keys) and H (the page /repo-keys), fed the same
// stub dataset and driven by the same steps. After each step both sides are read the same way and compared.
import { APP, BASE_APP, newContext, normalizeRequest, shot, spaced, trackRequests, until, writeJson } from './lib.mjs';
import { NOW, createDataset, installRepoKeyStub } from './fixture-repokeys.mjs';

export { shot };
let browser;
/** The shared browser the harness opens its contexts on. */
export const setBrowser = (value) => { browser = value; };
export const SIDES = [
  { side: 'B', app: BASE_APP, path: '/api-keys' },
  { side: 'H', app: APP, path: '/repo-keys' },
];
export const PENDING = /待审批（/;
export const AWAITING = /待交付（/;
export const KEYS = /^密钥（/;
export const AUDIT = /审计平台推送/;
export const SENTENCE = '每个仓库一把密钥，由 agent 通过 MCP 申请，人工审批一次；控制台只显示掩码，只有明确查看时才会显示明文。';
export const SENTENCE_EN = 'One key per repository. An agent applies over MCP, a person approves once, and this console shows masks until a value is deliberately revealed.';

/** One side, signed in, with the stub installed and every repository-key read and write recorded. */
export async function openSide({ side, app, path }, { empty = false, audit = 'on', viewport = { width: 1440, height: 900 }, touch = false, mode = 'normal', lang = 'zh', useStub = true, signIn = true, readsOnly = false } = {}) {
  const context = await newContext(browser, app, { viewport, touch, lang, signIn });
  await context.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: new URL(app).origin }).catch(() => undefined);
  // Relative times ("5 minutes ago") and dates are read against one instant, so both sides render the same text.
  if (useStub) await context.clock.setFixedTime(NOW + 60_000);
  const dataset = createDataset({ empty, audit });
  dataset.state.mode = mode;
  // The real-layer runs answer from the application itself; the unmatched list then stays empty.
  const stub = useStub ? await installRepoKeyStub(context, dataset, { readsOnly }) : { unmatched: [], log: [] };
  const tracker = trackRequests(context);
  const page = await context.newPage();
  const consoleLog = [];
  page.on('console', (message) => consoleLog.push(message.text()));
  const h = { side, consoleLog, app, path, context, page, dataset, stub, tracker, snaps: [], lastMark: 0 };
  h.go = async () => {
    await page.goto(`${app}${path}`, { waitUntil: 'domcontentloaded' });
    if (viewport.width >= 800) await page.locator('.app-menu li[data-route-path]').first().waitFor({ state: 'attached' });
  };
  h.ready = async () => {
    await page.getByRole('heading', { name: PENDING }).waitFor();
    await page.getByRole('heading', { name: AUDIT }).waitFor();
    await tracker.quiet({ quietMs: 600 });
  };
  h.quiet = () => tracker.quiet({ quietMs: 600 });
  h.section = (heading) => page.locator('section').filter({ has: page.getByRole('heading', { name: heading }) });
  h.row = (heading, text) => h.section(heading).locator('tbody tr', { hasText: text }).first();
  h.rowButton = (heading, text, name) => h.row(heading, text).getByRole('button', { name: spaced(name) });
  h.search = async (text) => { await page.getByPlaceholder(/按仓库、密钥或 agent 搜索|Search by repository/).fill(text); await h.quiet(); };
  h.filter = async (label) => {
    await page.getByRole('combobox', { name: /按状态筛选|Filter by status/ }).click({ force: true });
    await page.locator('.ant-select-item-option', { hasText: label }).first().click();
    await h.quiet();
  };
  /** The confirming button of the topmost open dialog or confirmation: the last button of its footer. */
  h.confirmButton = () => {
    const dialog = page.locator('[role="dialog"]:visible, .ant-popconfirm:visible').last();
    const footer = dialog.locator('.ant-modal-footer button, .ant-popconfirm-buttons button, .ant-popover-buttons button');
    return footer.last();
  };
  h.confirm = async () => {
    await h.confirmButton().click();
    await h.quiet();
  };
  h.closeDialogs = async () => {
    for (let guard = 0; guard < 4; guard += 1) {
      const open = page.locator('[role="dialog"]:visible');
      if ((await open.count()) === 0) break;
      await page.keyboard.press('Escape');
      await open.first().waitFor({ state: 'hidden', timeout: 3000 }).catch(() => undefined);
    }
  };
  /** Reads the side the same way for both, appending to h.snaps. */
  h.snap = async (label, extra = {}) => {
    const reading = await page.evaluate((isBaseline) => {
      const norm = (text) => text.split('\n').map((line) => line.replace(/[ \t]+/g, ' ').trim()).filter(Boolean);
      const first = [...document.querySelectorAll('section')].find((entry) => entry.querySelector('h3'));
      const root = first?.parentElement;
      let lines = root ? norm(root.innerText) : [];
      // The baseline panel draws its own title; the page header carries it on the new page. That one line is the allowed difference.
      if (isBaseline && ['仓库自有密钥', 'Repository keys'].includes(lines[0])) lines = lines.slice(1);
      const describe = (node) => `${node.tagName.toLowerCase()}|${(node.getAttribute('aria-label') || node.textContent || node.getAttribute('placeholder') || '').replace(/\s+/g, ' ').trim()}|disabled=${Boolean(node.disabled)}|title=${node.getAttribute('title') ?? ''}`;
      const controls = root ? [...root.querySelectorAll('button, a[href], input, textarea, [role="combobox"]')].map(describe) : [];
      const visible = (node) => node.getClientRects().length > 0;
      const dialogs = [...document.querySelectorAll('[role="dialog"], [role="alertdialog"], .ant-popconfirm')].filter(visible).map((node) => ({
        text: norm(node.innerText),
        controls: [...node.querySelectorAll('button, input, textarea, a[href], [role="radio"], [role="tab"]')].map(describe),
      }));
      const toasts = [...document.querySelectorAll('.ant-message-notice, .ant-notification-notice, [role="status"], [role="alert"]')].filter(visible).map((node) => node.innerText.replace(/\s+/g, ' ').trim()).filter(Boolean);
      return { lines, controls, dialogs, toasts };
    }, side === 'B');
    const since = tracker.since(h.lastMark).filter((entry) => entry.path.includes('/api/') && /\/repo-keys(\/|\?|$)/.test(entry.path));
    h.lastMark = tracker.mark();
    const reads = [...new Set(since.filter((entry) => entry.method === 'GET').map((entry) => normalizeRequest(entry)))].sort();
    const writes = since.filter((entry) => entry.method !== 'GET').map((entry) => `${normalizeRequest(entry)} ${entry.body ?? ''}`);
    const snapshot = { label, ...reading, reads, writes, unmatched: [...stub.unmatched], ...extra };
    h.snaps.push(snapshot);
    return snapshot;
  };
  return h;
}

/** Runs one flow on both sides and returns [B, H] harnesses (each with .snaps). */
export async function runBoth(options, flow, sides = SIDES) {
  const out = {};
  for (const entry of sides) {
    const h = await openSide(entry, options);
    try {
      await h.go();
      if (options.ready !== false) await h.ready();
      await flow(h);
    } finally {
      out[entry.side] = { snaps: h.snaps, writes: h.dataset.state.writes, unmatched: h.stub.unmatched };
      await h.context.close();
    }
  }
  return out;
}

/** Compares the two sides' readings step by step; returns the number of differing steps. */
export function compareSteps(c, label, out, { fields = ['lines', 'controls', 'dialogs', 'reads', 'writes', 'toasts'], tolerate = () => false } = {}) {
  const b = out.B.snaps;
  const h = out.H.snaps;
  // The raw side-by-side readings are kept as evidence next to the verdicts.
  writeJson(`parity-${label.replace(/[^A-Za-z0-9-]+/g, '_')}.json`, { B: b, H: h, writes: { B: out.B.writes, H: out.H.writes } });
  c.check(`${label}：两端读数步数相同（${b.length}）`, b.length === h.length, `${b.length} vs ${h.length}`);
  let differing = 0;
  for (let index = 0; index < Math.min(b.length, h.length); index += 1) {
    for (const field of fields) {
      const left = JSON.stringify(b[index][field]);
      const right = JSON.stringify(h[index][field]);
      const ok = left === right || tolerate(field, b[index], h[index]);
      if (!ok) differing += 1;
      c.check(`${label}／${b[index].label}／${field} 两端相同`, ok, ok ? '' : `B=${left.slice(0, 700)} H=${right.slice(0, 700)}`);
    }
  }
  c.check(`${label}：桩没有漏接的路由`, out.B.unmatched.length === 0 && out.H.unmatched.length === 0, JSON.stringify([out.B.unmatched, out.H.unmatched]));
  return differing;
}

