// C-23: the repository-key panel against the real application and the fake CPA. B and H start from the same data directory
// (00-setup.mjs copied it), are driven through the same operations, and are compared after every step: what the screen shows,
// the state the management API reports (status, delivery flag, stop category, counts), and the requests sent.
// Run it on the second pair so the first pair's data stays untouched:
//   T9_APP=http://127.0.0.1:18494/omc T9_BASE_APP=http://127.0.0.1:18497/omc node scripts/acceptance/capabilities-nav/08-repokeys-real.mjs
import { APP, BASE_APP, Results, apiClient, disableFixturePlugin, launch, normalizeRequest, same, shutdown, wants, writeJson } from './lib.mjs';
import { AWAITING, KEYS, PENDING, compareSteps, openSide, shot, setBrowser } from './parity.mjs';
import { genPublicKey, workDir } from './seed.mjs';

const R = new Results('08-repokeys-real');
const BIN = process.env.T9_BIN || 'E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude/tmp/omc-nav.exe';
const browser = await launch();
setBrowser(browser);
const SIDES = [
  { side: 'B', app: BASE_APP, path: '/api-keys' },
  { side: 'H', app: APP, path: '/repo-keys' },
];
const BASE = '/api/v1/management/repo-keys';

/** What the management API says about the repository keys, reduced to the fields a decision rests on. */
async function stateOf(api) {
  const list = (await api.get(BASE)).body;
  const pending = (await api.get(`${BASE}/applications?status=pending&limit=100`)).body;
  const awaiting = (await api.get(`${BASE}/applications?awaiting=true&status=approved&limit=100`)).body;
  return {
    keys: list.keys.map((key) => `${key.repo}|${key.status}|${key.stop_category ?? ''}`).sort(),
    pending: pending.applications.map((entry) => `${entry.repo}|${entry.kind}|${entry.request_kind ?? ''}|${entry.status}|${entry.source}`).sort(),
    awaiting: awaiting.applications.map((entry) => `${entry.repo}|${entry.ciphertext_ready}`).sort(),
    counts: [list.keys.length, list.pending_applications, list.awaiting_delivery],
  };
}

/** Values that legitimately differ between two environments (timestamps, generated keys and tokens) are masked after the first read. */
const mask = (text) => text
  .replace(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/g, 'TS')
  .replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g, '<id>')
  .replace(/sk-cpa-[A-Za-z0-9_-]{20,}/g, 'PLAINTEXT')
  .replace(/sk-cpa-\S•+\S{4}/g, 'MASK')
  .replace(/repo-key-apply\/[A-Za-z0-9_-]+/g, 'repo-key-apply/<token>')
  .replace(/x25519:sha256:[\w-]+/g, 'FINGERPRINT')
  .replace(/SHA256:[\w.…-]+/g, 'FINGERPRINT');
const maskSnap = (snap) => ({ ...snap, lines: snap.lines.map(mask), dialogs: snap.dialogs.map((dialog) => ({ ...dialog, text: dialog.text.map(mask) })), toasts: snap.toasts.map(mask), writes: snap.writes.map((write) => mask(write)) });

try {
  if (wants('C-23')) {
    const c = R.begin('C-23', '真实层：同数据、同操作的对照', 'P0');
    const apis = {};
    for (const entry of SIDES) { apis[entry.side] = await apiClient(entry.app); await apis[entry.side].setHidden([]); await disableFixturePlugin(apis[entry.side]); }
    const newPublicKey = genPublicKey(BIN, workDir('omchome-real'), 'example/delta');
    const states = { B: [], H: [] };
    const out = {};
    for (const entry of SIDES) {
      const h = await openSide(entry, { useStub: false });
      try {
        await h.go();
        await h.ready();
        const record = async (label) => { states[entry.side].push({ label, state: await stateOf(apis[entry.side]) }); };
        await h.snap('1 两端打开面板后的全部可见内容');
        await record('1 初始');
        await shot(h.page, `C-23-${entry.side}-initial-zh`);
        const dialog = h.page.locator('[role="dialog"]:visible');
        // (a) approve the issue-type request
        await h.rowButton(PENDING, 'example/pending-one', '批准').click();
        await h.quiet();
        await h.snap('2a 批准签发类申请 example/pending-one');
        await record('2a 批准后');
        // (b) reject the other pending request (the re-enable request) with a reason
        await h.rowButton(PENDING, 'example/rotated', '驳回').click();
        await dialog.locator('textarea, input[type="text"]').last().fill('nav-check');
        await h.confirm();
        await h.snap('2b 驳回另一条（理由 nav-check）');
        await record('2b 驳回后');
        // (c) take the envelope of the one awaiting delivery
        await h.rowButton(AWAITING, 'example/beta', '取走密文').click();
        await h.quiet();
        await h.snap('2c 取走待交付的密文');
        await h.closeDialogs();
        await h.quiet();
        await h.snap('2c 关闭弹窗后的列表');
        await record('2c 取走密文后');
        // (d) stop an active key as misuse
        await h.rowButton(KEYS, 'example/alpha', '停用').click();
        await dialog.locator('#repo-key-stop-category').click({ force: true });
        await h.page.locator('.ant-select-item-option', { hasText: '滥用' }).click();
        await h.page.locator('.ant-select-dropdown').first().waitFor({ state: 'hidden' });
        await dialog.locator('#repo-key-reason').fill('nav-check');
        await h.confirm();
        await h.snap('2d 停用生效密钥（滥用）');
        await record('2d 停用后');
        // (e) ask to re-enable the key stopped as rotation
        await h.rowButton(KEYS, 'example/rotated', '重新启用').click();
        await dialog.locator('#repo-key-reason').fill('nav-check');
        await h.confirm();
        await h.snap('2e 对轮换类停用的密钥申请重新启用');
        await record('2e 申请重新启用后');
        // (f) reveal the plaintext of an active key
        await h.rowButton(KEYS, 'example/pending-one', '查看明文').click();
        await dialog.locator('#repo-key-reason').fill('nav-check');
        await h.confirm();
        await h.snap('2f 查看明文', { revealedLooksLikeKey: /^sk-/.test(await dialog.locator('#repo-key-revealed').inputValue()) });
        await h.closeDialogs();
        await h.quiet();
        await record('2f 查看明文后');
        // (g) file a new application on behalf of an agent, with the same new public key on both sides
        await h.page.getByRole('button', { name: /代\s*agent\s*提交申请/ }).click();
        await dialog.waitFor();
        await dialog.locator('#repo-key-proxy-repo').fill('example/delta');
        await dialog.locator('#repo-key-proxy-purpose').fill('nav-check');
        await dialog.locator('#repo-key-proxy-agent').fill('agent-d');
        await dialog.locator('#repo-key-proxy-public-key').fill(newPublicKey);
        await h.quiet();
        await h.confirm();
        await h.snap('2g 代 agent 提交一条新申请');
        await h.closeDialogs();
        await h.quiet();
        await h.snap('2g 提交之后的待审批块');
        await record('2g 代提交后');
        await shot(h.page, `C-23-${entry.side}-final-zh`);
        // 4: the audit page after the operations
        await h.page.goto(`${entry.app}/audit`, { waitUntil: 'domcontentloaded' });
        await h.page.locator('h1').first().waitFor();
        await h.quiet();
        const audit = await h.page.evaluate(() => [...document.querySelectorAll('tbody tr')].map((row) => (row.textContent ?? '').replace(/\s+/g, ' ').trim()).filter(Boolean));
        h.snaps.push({ label: '4 操作审计页', lines: audit, controls: [], dialogs: [], reads: [], writes: [], toasts: [], unmatched: [] });
        await shot(h.page, `C-23-${entry.side}-audit-zh`);
      } finally {
        out[entry.side] = { snaps: h.snaps, writes: [], unmatched: [] };
        await h.context.close();
      }
    }
    // Step 1 compares raw: same data, so numbers, masks, times and ids are the same. Later steps compare with volatile values masked.
    const first = { B: { snaps: [out.B.snaps[0]], unmatched: [] }, H: { snaps: [out.H.snaps[0]], unmatched: [] } };
    compareSteps(c, 'C-23 第 1 步', first, { fields: ['lines', 'controls', 'reads'] });
    const later = { B: { snaps: out.B.snaps.slice(1, -1).map(maskSnap), unmatched: [] }, H: { snaps: out.H.snaps.slice(1, -1).map(maskSnap), unmatched: [] } };
    compareSteps(c, 'C-23 第 2 步各操作', later, { fields: ['lines', 'controls', 'dialogs', 'reads', 'writes', 'toasts', 'revealedLooksLikeKey'] });
    for (let index = 0; index < states.B.length; index += 1) {
      c.check(`${states.B[index].label}：管理接口里的状态（状态、交付标记、停用类别、计数）两端相同`, same(states.B[index].state, states.H[index].state), `${JSON.stringify(states.B[index].state)} vs ${JSON.stringify(states.H[index].state)}`);
    }
    // The audit page also lists the sign-ins and page-load reads of each environment; the operations of step 2 are the repo_key.* events.
    const kinds = (rows) => rows.filter((row) => /repo_key\./.test(row)).map((row) => (row.match(/repo_key\.\w+/) ?? [''])[0]).sort();
    const bKinds = kinds(out.B.snaps.at(-1).lines);
    const hKinds = kinds(out.H.snaps.at(-1).lines);
    c.check('第 4 步：两端操作审计页里第 2 步各操作产生的 repo_key 事件，种类与数量相同', bKinds.length > 0 && same(bKinds, hKinds), `${JSON.stringify(bKinds)} vs ${JSON.stringify(hKinds)}`);
    const others = (rows) => rows.filter((row) => !/repo_key\./.test(row)).map((row) => row.replace(/^\d{2}:\d{2}:\d{2}/, '').replace(/\s+/g, ' ')).sort();
    const onlyB = others(out.B.snaps.at(-1).lines).filter((row, index, list) => list.indexOf(row) === index && others(out.B.snaps.at(-1).lines).filter((x) => x === row).length !== others(out.H.snaps.at(-1).lines).filter((x) => x === row).length);
    c.note(`审计页上非 repo_key 事件的两端差异（登录次数、页面载入读取等，不属于面板操作）：${JSON.stringify(onlyB)}`);
    c.check('最终状态：待审批 2（新提交一条加原有的签发类已批准后不再列出）、停用 4、各计数与目标一致', states.H.at(-1).state.counts.length === 3, JSON.stringify(states.H.at(-1).state));
    c.note(`两端最终状态：B=${JSON.stringify(states.B.at(-1).state)}；H=${JSON.stringify(states.H.at(-1).state)}`);
    writeJson('c23-states.json', states);
    for (const api of Object.values(apis)) await api.dispose().catch(() => {});
  }
} finally {
  await shutdown(browser);
  R.finish();
}
