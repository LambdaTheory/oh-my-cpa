// C-44 .. C-48: the demonstration mode (the same binary with the demonstration switch; no sign-in, base path "/").
// H is T9_DEMO, B is T9_BASE_DEMO (cases.md 0.5).
//   node scripts/acceptance/capabilities-nav/10-demo.mjs
import { BASE_DEMO, DEMO, Results, launch, newContext, same, shot, shotTall, shutdown, trackRequests, until, wants } from './lib.mjs';
import { collapseRail, open, openSidebarPanel, readPlaceholder, readRail, readRailMetrics, readSidebarPanel } from './nav.mjs';
import { KEYS, PENDING, AWAITING, compareSteps, runBoth, setBrowser } from './parity.mjs';

const R = new Results('10-demo');
const browser = await launch();
setBrowser(browser);
const DEMO_SIDES = [
  { side: 'B', app: BASE_DEMO, path: '/api-keys' },
  { side: 'H', app: DEMO, path: '/repo-keys' },
];
const names = (rail, title) => rail.groups.find((group) => group.title === title)?.items.map((item) => item.name);

/** The demonstration's page as a signed-out visitor (it has no sign-in). */
const demoContext = (app, options = {}) => newContext(browser, app, { signIn: false, ...options });

try {
  // ---- C-44: loading placeholder in the demonstration
  if (wants('C-44')) {
    const c = R.begin('C-44', '加载占位：演示模式', 'P1');
    const measure = async (app, label) => {
      const context = await demoContext(app);
      const held = [];
      await context.route(/\/api\/v1\/preferences(\?|$)/, (route) => { if (route.request().method() === 'GET') held.push(route); else route.continue(); });
      const page = await context.newPage();
      await page.goto(`${app}/dashboard`, { waitUntil: 'commit' });
      await until(async () => (await readPlaceholder(page).catch(() => null)) !== null && held.length > 0, { label: `${label} placeholder and held read` });
      const placeholder = await readPlaceholder(page);
      await shot(page, `C-44-${label}-demo-placeholder`);
      for (const route of held) await route.continue();
      await until(async () => (await page.locator('.app-menu li[data-route-path]').count()) > 0 && (await readPlaceholder(page)) === null, { label: `${label} real rail` });
      const real = await readRail(page);
      const metrics = await readRailMetrics(page);
      await shot(page, `C-44-${label}-demo-loaded`);
      await context.close();
      return { placeholder, real, metrics };
    };
    const h = await measure(DEMO, 'H');
    const b = await measure(BASE_DEMO, 'B');
    const hCounts = h.real.groups.map((group) => group.items.length);
    c.check('H 演示的占位分组数为 6', h.placeholder.groups.length === 6, JSON.stringify(h.placeholder.groups));
    c.check('H 演示的占位各组条数与加载后演示侧栏的固定条目数一致（3、3、5、3、2、3）', same(h.placeholder.groups, hCounts) && same(hCounts, [3, 3, 5, 3, 2, 3]), `${JSON.stringify(h.placeholder.groups)} vs ${JSON.stringify(hCounts)}`);
    const delta = Math.abs(h.placeholder.navScrollHeight - h.metrics.viewportScrollHeight);
    c.check('H 演示的占位与真实栏导航区高度之差不超过一个条目', delta <= h.metrics.entryHeight, `placeholder=${h.placeholder.navScrollHeight} real=${h.metrics.viewportScrollHeight} menu=${h.metrics.menuHeight} entry=${h.metrics.entryHeight}`);
    c.check('B 演示的占位是 4 组（4、3、4、4），与其真实栏不吻合（对照）', same(b.placeholder.groups, [4, 3, 4, 4]), `${JSON.stringify(b.placeholder.groups)} vs real ${JSON.stringify(b.real.groups.map((group) => group.items.length))}`);
    c.note(`H：${JSON.stringify({ placeholder: h.placeholder, metrics: h.metrics })}；B：${JSON.stringify({ placeholder: b.placeholder, metrics: b.metrics })}`);
  }

  // ---- C-45: the demonstration's rail
  if (wants('C-45')) {
    const c = R.begin('C-45', '演示模式的侧栏', 'P0');
    const read = {};
    for (const [side, app] of [['H', DEMO], ['B', BASE_DEMO]]) {
      const context = await demoContext(app);
      const page = await context.newPage();
      await open(page, `${app}/dashboard`);
      const rail = await readRail(page);
      await openSidebarPanel(page, app);
      const panel = await readSidebarPanel(page);
      await page.keyboard.press('Escape');
      await open(page, `${app}/dashboard`);
      await collapseRail(page);
      const collapsed = await readRail(page);
      const playground = await page.goto(`${app}/playground`, { waitUntil: 'domcontentloaded' });
      await page.locator('h1').first().waitFor();
      read[side] = { rail, panel, collapsed, playgroundPath: new URL(page.url()).pathname, playgroundStatus: playground?.status() };
      if (side === 'H') {
        c.evidenceFile(await shotTall(page, 'C-45-H-demo-collapsed'));
        await open(page, `${app}/dashboard`);
        c.evidenceFile(await shotTall(page, 'C-45-H-demo-rail-expanded'));
        await openSidebarPanel(page, app);
        c.evidenceFile(await shot(page, 'C-45-H-demo-settings-panel'));
      } else {
        await open(page, `${app}/dashboard`);
        c.evidenceFile(await shotTall(page, 'C-45-B-demo-rail-expanded'));
      }
      await context.close();
    }
    const h = read.H;
    const b = read.B;
    c.check('H 演示展开栏有 6 个分组', same(h.rail.groupTitles, ['运行', '网关', '观测', '能力', '插件', '控制']), JSON.stringify(h.rail.groupTitles));
    c.check('H 演示「运行」下是仪表盘、智能体、模型广场三项，没有操练场', same(names(h.rail, '运行'), ['仪表盘', '智能体', '模型广场']), JSON.stringify(names(h.rail, '运行')));
    c.check('H 演示「能力」下是模型组、仓库自有密钥、额度拆借', same(names(h.rail, '能力'), ['模型组', '仓库自有密钥', '额度拆借']), JSON.stringify(names(h.rail, '能力')));
    const panelEntries = h.panel.groups.flatMap((group) => group.entries);
    c.check('H 演示设置面板里没有操练场的开关', !panelEntries.some((entry) => entry.path === '/playground'), JSON.stringify(panelEntries.map((entry) => entry.path)));
    c.check('H 演示设置面板里「能力」下三项各有一个开关', same(h.panel.groups.find((group) => group.title === '能力')?.entries.map((entry) => entry.label), ['模型组', '仓库自有密钥', '额度拆借']), JSON.stringify(h.panel.groups.map((group) => group.title)));
    c.check('H 演示收起栏 19 个图标', h.collapsed.flat.length === 19, `count=${h.collapsed.flat.length}`);
    c.check('B 演示（对照）：5 个分组、模型组在「运行」、收起栏 17 个', b.rail.groupTitles.length === 5 && names(b.rail, '运行').includes('模型组') && b.collapsed.flat.length === 17, `${JSON.stringify(b.rail.groupTitles)} count=${b.collapsed.flat.length}`);
    c.check('直接输入 /playground：H 与 B 的结果相同', h.playgroundPath === b.playgroundPath && h.playgroundStatus === b.playgroundStatus, `${h.playgroundPath} ${h.playgroundStatus} vs ${b.playgroundPath} ${b.playgroundStatus}`);
    c.note(`/playground 结果：${h.playgroundPath}（状态 ${h.playgroundStatus}），B 同`);
  }

  // ---- C-46: the repository-key page in the demonstration shows the demonstration's data, like the old lower half
  if (wants('C-46')) {
    const c = R.begin('C-46', '演示模式里仓库自有密钥页可打开并显示演示数据', 'P0');
    const out = await runBoth({ useStub: false, signIn: false }, async (h) => {
      await h.snap('打开后的全部可见内容');
      await shot(h.page, `C-46-${h.side}-demo-repo-keys-zh`);
      if (h.side === 'H') {
        const mark = h.tracker.mark();
        await h.page.locator('.app-content').getByRole('button', { name: /^刷新$|刷\s*新/ }).first().click();
        await h.quiet();
        h.refreshWrites = h.tracker.since(mark).filter((entry) => entry.method !== 'GET').map((entry) => `${entry.method} ${entry.path}`);
        h.refreshReads = h.tracker.since(mark).filter((entry) => entry.path.includes('/api/')).map((entry) => `${entry.method} ${entry.path}`);
        await h.snap('点页头刷新之后', { refreshWrites: h.refreshWrites, refreshReads: h.refreshReads });
      }
    }, DEMO_SIDES);
    const h = out.H.snaps;
    const b = out.B.snaps;
    c.check('H 页面能打开，没有错误提示、没有一直停在加载', h[0].lines.length > 5 && !h[0].lines.some((line) => /失败|错误|加载中/.test(line)), h[0].lines.slice(0, 12).join(' | '));
    c.check('H 所显示的内容与 B 下半块逐项相同（演示数据现状为空：三块空态与推送未启用）', same(h[0].lines, b[0].lines) && same(h[0].controls, b[0].controls), `${JSON.stringify(h[0].lines.slice(0, 12))} vs ${JSON.stringify(b[0].lines.slice(0, 12))}`);
    c.check('点页头刷新只重读读接口，没有写请求', same(h[1].refreshWrites, []) && h[1].refreshReads.length > 0, JSON.stringify(h[1]));
    c.note(`演示里这一块的内容：${h[0].lines.join(' | ')}`);
  }

  // ---- C-47: writes in the demonstration are refused exactly as before (F12)
  if (wants('C-47')) {
    const c = R.begin('C-47', '演示模式下写操作与拆页前一样被拒（F12）', 'P0');
    // Step 1 and 2: reads answered from the dataset so row-level controls exist; the demonstration itself decides about writes.
    const out = await runBoth({ useStub: true, readsOnly: true, signIn: false }, async (h) => {
      const writesAttempted = () => h.tracker.state.log.filter((entry) => entry.method !== 'GET' && /\/repo-keys/.test(entry.path)).length;
      await h.snap('各写入口的禁用状态与提示');
      await shot(h.page, `C-47-${h.side}-demo-controls-zh`);
      // Opening a review shows the approve and reject controls inside the dialog.
      await h.rowButton(PENDING, 'example/alpha', '详情').click();
      await h.quiet();
      await h.snap('审阅详情里的批准与驳回');
      await h.closeDialogs();
      // A forced click on every disabled write control must send nothing.
      const before = writesAttempted();
      for (const [heading, repo, label] of [[PENDING, 'example/alpha', '批准'], [PENDING, 'example/alpha', '驳回'], [PENDING, 'example/alpha', '授权链接'], [AWAITING, 'example/gamma', '取走密文'], [KEYS, 'example/alpha-old', '停用'], [KEYS, 'example/alpha-old', '查看明文'], [KEYS, 'example/rotated', '重新启用']]) {
        await h.rowButton(heading, repo, label).click({ force: true, timeout: 3000 }).catch(() => undefined);
      }
      await h.page.getByRole('button', { name: /代\s*agent\s*提交申请/ }).click({ force: true, timeout: 3000 }).catch(() => undefined);
      await h.quiet();
      await h.snap('对禁用入口强行点击之后', { writesSent: writesAttempted() - before });
    }, DEMO_SIDES);
    compareSteps(c, 'C-47 入口', out, { fields: ['lines', 'controls', 'dialogs', 'reads', 'writes', 'toasts', 'writesSent'] });
    const controls = out.H.snaps[0].controls;
    const writeControls = controls.filter((entry) => /批 准|驳 回|授权链接|取走密文|停 用|重新启用|查看明文|代 agent 提交申请/.test(entry));
    c.check('每个写入口都是禁用的（H）', writeControls.length > 0 && writeControls.every((entry) => /disabled=true/.test(entry)), JSON.stringify(writeControls.filter((entry) => !/disabled=true/.test(entry))));
    c.check('禁用的入口点击不发请求（H 与 B 都是 0）', out.H.snaps.at(-1).writesSent === 0 && out.B.snaps.at(-1).writesSent === 0, JSON.stringify([out.H.snaps.at(-1).writesSent, out.B.snaps.at(-1).writesSent]));
    // Step 3: raw writes against the demonstration, no stub.
    const verdicts = { H: {}, B: {} };
    const endpoints = [
      ['POST', '/repo-keys/applications', { repo: 'example/x', purpose: 'p', agent: 'a', public_key: 'x25519:AAAA' }],
      ['POST', '/repo-keys/applications/x/approve', { reason: '' }],
      ['POST', '/repo-keys/applications/x/reject', { reason: 'r' }],
      ['POST', '/repo-keys/x/stop', { category: 'rotate', reason: 'r' }],
      ['POST', '/repo-keys/x/reenable-request', { reason: 'r' }],
      ['POST', '/repo-keys/applications/x/ciphertext', {}],
      ['POST', '/repo-keys/applications/x/approval-link', {}],
      ['POST', '/repo-keys/x/reveal', { reason: 'r' }],
      ['POST', '/repo-keys/apply-link/x/approve', { reason: '' }],
      ['POST', '/repo-keys/apply-link/x/reject', { reason: 'r' }],
    ];
    for (const [side, app] of [['H', DEMO], ['B', BASE_DEMO]]) {
      const context = await demoContext(app);
      const page = await context.newPage();
      await open(page, `${app}/dashboard`);
      for (const [method, route, body] of endpoints) {
        verdicts[side][`${method} ${route}`] = await page.evaluate(async ({ method: m, route: r, body: payload }) => {
          const response = await fetch(`/api/v1/management${r}`, { method: m, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
          return { status: response.status, body: (await response.text()).slice(0, 300) };
        }, { method, route, body });
      }
      await context.close();
    }
    for (const key of Object.keys(verdicts.H)) {
      c.check(`写接口 ${key}：被拒（非 2xx），状态码与应答体与 B 相同`, verdicts.H[key].status >= 400 && same(verdicts.H[key], verdicts.B[key]), `${JSON.stringify(verdicts.H[key])} vs ${JSON.stringify(verdicts.B[key])}`);
    }
    // Step 4: the one-time approval page in the demonstration.
    const apply = {};
    for (const [side, app] of [['H', DEMO], ['B', BASE_DEMO]]) {
      const context = await demoContext(app);
      const tracker = trackRequests(context);
      const page = await context.newPage();
      await page.goto(`${app}/repo-key-apply/demo-token`, { waitUntil: 'domcontentloaded' });
      await page.locator('h1').first().waitFor();
      await tracker.quiet({ quietMs: 800 });
      apply[side] = {
        text: (await page.locator('main').first().innerText()).replace(/\s+/g, ' ').trim(),
        buttons: await page.locator('main button').evaluateAll((nodes) => nodes.map((node) => `${node.textContent?.trim()}|disabled=${node.disabled}`)),
        reads: tracker.state.log.filter((entry) => entry.path.includes('/repo-keys/apply-link')).map((entry) => `${entry.method} ${entry.path}`),
      };
      if (side === 'H') c.evidenceFile(await shot(page, 'C-47-H-demo-apply-page-zh'));
      await context.close();
    }
    c.check('审批链接页在演示里只读：没有读取请求，没有批准按钮，与 B 相同', same(apply.H, apply.B) && apply.H.reads.length === 0 && !apply.H.buttons.some((entry) => /批准/.test(entry) && /disabled=false/.test(entry)), JSON.stringify(apply));
    c.note(`写接口应答：${JSON.stringify(verdicts.H)}`);
  }

  // ---- C-48: key management in the demonstration is unchanged
  if (wants('C-48')) {
    const c = R.begin('C-48', '演示模式下密钥管理页的行为不变', 'P1');
    const read = {};
    for (const [side, app] of [['H', DEMO], ['B', BASE_DEMO]]) {
      const context = await demoContext(app);
      const page = await context.newPage();
      await open(page, `${app}/api-keys`);
      await page.locator('[data-testid="keys-count"]').waitFor();
      await until(async () => (await page.locator('.keys-page .ant-table .ant-table-tbody .ant-table-row').count()) > 0 || /暂无/.test(await page.locator('.keys-page').innerText()), { label: `${side} demo list drawn` });
      const rows = await page.evaluate(() => [...document.querySelectorAll('.keys-page .ant-table .ant-table-tbody .ant-table-row')].map((row) => ({ text: (row.textContent ?? '').replace(/\s+/g, ' ').trim(), tag: (row.querySelector('[class*="repo-tag"]')?.textContent ?? '').trim(), buttons: [...row.querySelectorAll('button')].map((button) => `${button.getAttribute('aria-label') ?? button.textContent?.trim()}|disabled=${button.disabled}|title=${button.getAttribute('title') ?? ''}`) })));
      const add = page.getByRole('button', { name: /添加 API 密钥/ }).first();
      const hint = await page.locator('p[class*="repo-keys-moved"]').count() ? (await page.locator('p[class*="repo-keys-moved"]').innerText()).replace(/\s+/g, ' ') : null;
      const posts = await page.evaluate(async () => {
        const post = await fetch('/api/v1/management/api-keys', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ key: 'nav-demo-key' }) });
        const del = await fetch('/api/v1/management/api-keys/0', { method: 'DELETE' });
        return { post: post.status, del: del.status };
      });
      read[side] = { count: await page.locator('[data-testid="keys-count"]').innerText(), rows, addDisabled: await add.isDisabled(), addTitle: await add.getAttribute('title'), hint, posts };
      if (side === 'H') c.evidenceFile(await shot(page, 'C-48-H-demo-api-keys-zh'));
      await context.close();
    }
    c.check('密钥管理页在演示里公开可读，H 的列表（行内容、所属仓库小标）与 B 相同', same(read.H.rows.map((row) => [row.text, row.tag]), read.B.rows.map((row) => [row.text, row.tag])) && read.H.count === read.B.count, `${JSON.stringify(read.H.rows.map((row) => row.text))} vs ${JSON.stringify(read.B.rows.map((row) => row.text))}`);
    c.check('「添加 API 密钥」禁用，提示与 B 相同', read.H.addDisabled === true && read.H.addDisabled === read.B.addDisabled && read.H.addTitle === read.B.addTitle, JSON.stringify([read.H.addDisabled, read.H.addTitle, read.B.addDisabled, read.B.addTitle]));
    c.check('行内编辑与删除入口的状态与提示和 B 相同', same(read.H.rows.map((row) => row.buttons), read.B.rows.map((row) => row.buttons)), JSON.stringify(read.H.rows.map((row) => row.buttons)));
    c.check('POST 与 DELETE 写接口都被拒，与 B 相同', read.H.posts.post >= 400 && read.H.posts.del >= 400 && same(read.H.posts, read.B.posts), JSON.stringify([read.H.posts, read.B.posts]));
    c.check('H 页底有指路提示（B 没有，下半块已换成它）', Boolean(read.H.hint) && /仓库自有密钥已移到「能力」分组/.test(read.H.hint), String(read.H.hint));
  }
} finally {
  await shutdown(browser);
  R.finish();
}
