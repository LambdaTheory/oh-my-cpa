// C-10 .. C-14: the key management page after the panel moved out: only the gateway key list remains, a static pointer
// sits at the bottom, and the upper half behaves as before.
//   T9_BIN / T9_BASE_BIN mint receiver public keys for the repository key that makes a list row "bound".
//   node scripts/acceptance/capabilities-nav/06-apikeys.mjs
import { APP, BASE_APP, LANGS, NAMES, Results, apiClient, disableFixturePlugin, launch, newContext, normalizeRequest, same, shot, shutdown, spaced, trackRequests, until, wants } from './lib.mjs';
import { open, readLocation } from './nav.mjs';
import { approve, fileApplication, genPublicKey, listKeys, workDir } from './seed.mjs';

const R = new Results('06-apikeys');
const BIN = process.env.T9_BIN || 'E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude/tmp/omc-nav.exe';
const BASE_BIN = process.env.T9_BASE_BIN || 'C:/Users/lhw/AppData/Local/Temp/omc-t9-s8-base/tmp/omc-nav.exe';
const SENTENCE = '每个仓库一把密钥，由 agent 通过 MCP 申请，人工审批一次；控制台只显示掩码，只有明确查看时才会显示明文。';
const HINT_SELECTOR = 'p[class*="repo-keys-moved"]';
const browser = await launch();
const apis = {};
const sides = [['H', APP, BIN], ['B', BASE_APP, BASE_BIN]];

/** The gateway key list as data: its rows (text, owner tag) and the count line. Only the first table counts; the baseline page carries a second one. */
async function readList(page) {
  return page.evaluate(() => {
    const count = document.querySelector('[data-testid="keys-count"]')?.textContent?.replace(/\s+/g, ' ').trim() ?? null;
    const table = document.querySelector('.keys-page .ant-table');
    const rows = [...(table?.querySelectorAll('.ant-table-tbody .ant-table-row') ?? [])].map((row) => ({
      text: (row.textContent ?? '').replace(/\s+/g, ' ').trim(),
      tag: (row.querySelector('[class*="repo-tag"]')?.textContent ?? '').trim(),
    }));
    return { count, rows };
  });
}

/** Randomly generated key masks differ between two environments; they are data, not behavior. */
const maskless = (text) => text.replace(/sk-cpa-\S{1}•+\S{4}/g, 'MASK');

/** Counts of the panel's markers on the page (cases C-10 step 2). */
async function panelMarkers(page) {
  const count = async (locator) => locator.count();
  return {
    heading: await count(page.getByRole('heading', { name: '仓库自有密钥' })),
    sentence: await count(page.getByText(SENTENCE)),
    searchBox: await count(page.getByPlaceholder('按仓库、密钥或 agent 搜索')),
    statusFilter: await count(page.getByText('全部状态')),
    pendingBlock: await count(page.getByText(/待审批（\d+）/)),
    awaitingBlock: await count(page.getByText(/待交付（\d+）/)),
    keysBlock: await count(page.getByText(/(?<!待审批|待交付)密钥（\d+）/)),
    auditPush: await count(page.getByText('审计平台推送')),
    applyButton: await count(page.getByRole('button', { name: '代 agent 提交申请' })),
  };
}

try {
  for (const [side, app, bin] of sides) {
    apis[side] = await apiClient(app);
    await apis[side].setHidden([]);
    await disableFixturePlugin(apis[side]);
    // A repository key whose gateway key lands in the list, so one row is "bound" and the fixture's own key is not.
    const existing = (await listKeys(apis[side])).keys.find((key) => key.repo === 'example/gateway-bound');
    if (!existing) {
      const filed = await fileApplication(apis[side], { repo: 'example/gateway-bound', publicKey: genPublicKey(bin, workDir('omchome-apikeys'), 'example/gateway-bound') });
      if (filed.status !== 'approved') await approve(apis[side], filed.application_id);
    }
  }

  // ---- C-10
  if (wants('C-10')) {
    const c = R.begin('C-10', '密钥管理页只剩网关客户端密钥列表', 'P0');
    const read = {};
    for (const [side, app] of sides) {
      const context = await newContext(browser, app);
      const tracker = trackRequests(context);
      const page = await context.newPage();
      await open(page, `${app}/api-keys`);
      await page.locator('[data-testid="keys-count"]').waitFor();
      await until(async () => (await readList(page)).rows.length >= 2, { label: `${side} list rows` });
      await tracker.quiet();
      read[side] = { markers: await panelMarkers(page), list: await readList(page), text: (await page.locator('.keys-page').innerText()).replace(/\s+/g, ' ') };
      await page.locator(HINT_SELECTOR).scrollIntoViewIfNeeded().catch(() => undefined);
      c.evidenceFile(await shot(page, `C-10-${side}-api-keys-zh`, { fullPage: false }));
      await context.close();
    }
    const h = read.H;
    const b = read.B;
    c.check('H 页面只有「API 密钥列表」一块（页面里没有「仓库自有密钥」标题）', h.markers.heading === 0 && /API 密钥列表/.test(h.text), JSON.stringify(h.markers));
    for (const [name, count] of Object.entries(h.markers)) c.check(`H 页面里找不到面板的「${name}」`, count === 0, `count=${count}`);
    c.check('B 页面里这些都找得到（对照，说明用例前置有效）', Object.values(b.markers).every((count) => count >= 1), JSON.stringify(b.markers));
    c.check('列表每一行仍标出所属仓库或「未绑定仓库」', h.list.rows.length >= 2 && h.list.rows.every((row) => row.tag.length > 0), JSON.stringify(h.list.rows.map((row) => row.tag)));
    c.check('列表里同时有绑定了仓库的和「未绑定仓库」的各一把', h.list.rows.some((row) => row.tag === 'example/gateway-bound') && h.list.rows.some((row) => row.tag === '未绑定仓库'), JSON.stringify(h.list.rows.map((row) => row.tag)));
    c.check('每一行的所属标记与 B 逐行相同', same(h.list.rows.map((row) => row.tag), b.list.rows.map((row) => row.tag)), `${JSON.stringify(h.list.rows.map((row) => row.tag))} vs ${JSON.stringify(b.list.rows.map((row) => row.tag))}`);
    c.check('列表行内容（名称、掩码、用量列，随机生成的密钥掩码除外）与 B 逐行相同', same(h.list.rows.map((row) => maskless(row.text)), b.list.rows.map((row) => maskless(row.text))), `${JSON.stringify(h.list.rows.map((row) => maskless(row.text)))} vs ${JSON.stringify(b.list.rows.map((row) => maskless(row.text)))}`);
    c.note(`H 读数：${JSON.stringify(h.list)}；B 读数：${JSON.stringify(b.list)}`);
  }

  // ---- C-11: the upper half's operations give the same results
  if (wants('C-11')) {
    const c = R.begin('C-11', '密钥管理页上半块的操作结果与 B 一致', 'P1');
    const trace = { H: [], B: [] };
    for (const [side, app] of sides) {
      const context = await newContext(browser, app);
      const tracker = trackRequests(context);
      const page = await context.newPage();
      await open(page, `${app}/api-keys`);
      await page.locator('[data-testid="keys-count"]').waitFor();
      await until(async () => (await readList(page)).rows.length >= 2, { label: `${side} list rows` });
      const rowOf = (text) => page.locator('.keys-page .ant-table .ant-table-tbody .ant-table-row', { hasText: text }).first();
      const removeRow = async (text) => {
        const row = rowOf(text);
        await row.getByRole('button', { name: /^更多/ }).click();
        await page.locator('.ant-dropdown .ant-dropdown-menu-item', { hasText: /删/ }).click();
        await page.locator('.ant-popconfirm').getByRole('button', { name: spaced('删除') }).click();
        await until(async () => (await rowOf(text).count()) === 0, { label: `${side} row ${text} removed` });
      };
      // Leftovers of an earlier run would make the add a duplicate; clear them the same way on both sides, outside the trace.
      while ((await rowOf('nav-').count()) > 0) await removeRow('nav-');
      await tracker.quiet();
      const step = async (name, action) => {
        const mark = tracker.mark();
        await action();
        await tracker.quiet();
        const list = await readList(page);
        const writes = tracker.since(mark).filter((entry) => entry.method !== 'GET').map((entry) => `${normalizeRequest(entry)} ${JSON.stringify(entry.bodyKeys)}`);
        const toasts = await page.locator('.ant-message-notice, .ant-notification-notice, [role="status"], [role="alert"]').allInnerTexts().catch(() => []);
        trace[side].push({ name, count: list.count, rows: list.rows.map((row) => `${row.tag} | ${maskless(row.text)}`), writes, toasts: toasts.map((text) => text.replace(/\s+/g, ' ').trim()).filter(Boolean), repoPanel: (await panelMarkers(page)).heading });
      };
      await step('1 添加 nav-check-key', async () => {
        await page.getByRole('button', { name: /添加 API 密钥/ }).first().click();
        await page.locator('#gateway-key-value').fill('nav-check-key');
        await page.getByRole('button', { name: /创建密钥/ }).click();
        await until(async () => (await rowOf('nav-').count()) > 0, { label: `${side} new row` });
        await page.locator('.ant-modal').waitFor({ state: 'hidden' });
      });
      await step('2 设别名 nav-check-alias', async () => {
        await rowOf('nav-').getByRole('button', { name: /^编辑/ }).click();
        await page.locator('#gateway-key-alias').fill('nav-check-alias');
        await page.getByRole('button', { name: spaced('保存') }).click();
        await until(async () => (await rowOf('nav-check-alias').count()) > 0, { label: `${side} alias shown` });
        await page.locator('.ant-modal').waitFor({ state: 'hidden' });
      });
      c.evidenceFile(await shot(page, `C-11-${side}-after-alias`));
      await step('3 删除', async () => { await removeRow('nav-check-alias'); });
      await step('4 页头刷新', async () => {
        await page.locator('.app-content').getByRole('button', { name: /^刷新$|刷\s*新/ }).first().click();
      });
      c.evidenceFile(await shot(page, `C-11-${side}-after-refresh`));
      await context.close();
    }
    for (let index = 0; index < trace.H.length; index += 1) {
      const h = trace.H[index];
      const b = trace.B[index];
      c.check(`${h.name}：列表内容与计数两端相同`, h.count === b.count && same(h.rows, b.rows), `${h.count} ${JSON.stringify(h.rows)} vs ${b.count} ${JSON.stringify(b.rows)}`);
      c.check(`${h.name}：提示文案两端相同`, same(h.toasts, b.toasts), `${JSON.stringify(h.toasts)} vs ${JSON.stringify(b.toasts)}`);
      c.check(`${h.name}：发出的写请求两端相同`, same(h.writes, b.writes), `${JSON.stringify(h.writes)} vs ${JSON.stringify(b.writes)}`);
      c.check(`${h.name}：每把密钥的所属仓库小标始终在，H 上没有出现仓库自有密钥面板`, h.rows.every((row) => !row.startsWith(' |')) && h.repoPanel === 0, JSON.stringify(h.rows));
    }
    c.note(`H 全程：${JSON.stringify(trace.H).slice(0, 3000)}`);
    c.note(`B 全程：${JSON.stringify(trace.B).slice(0, 3000)}`);
  }

  // ---- C-12: the static pointer
  if (wants('C-12')) {
    const c = R.begin('C-12', '页底指路提示：存在、文字、可点', 'P0');
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    await open(page, `${APP}/api-keys`);
    await page.locator(HINT_SELECTOR).waitFor();
    await page.locator(HINT_SELECTOR).scrollIntoViewIfNeeded();
    const text = (await page.locator(HINT_SELECTOR).innerText()).replace(/\s+/g, ' ');
    c.check('页底有一行静态提示，含「仓库自有密钥」与「能力」两个词', /仓库自有密钥/.test(text) && /能力/.test(text), text);
    const isLast = await page.evaluate((selector) => {
      const hint = document.querySelector(selector);
      const page = document.querySelector('.keys-page');
      return Boolean(hint && page && page.lastElementChild === hint);
    }, HINT_SELECTOR);
    c.check('提示在页面最底部（原面板所在位置）', isLast);
    c.evidenceFile(await shot(page, 'C-12-H-api-keys-bottom-zh'));
    const link = page.locator(`${HINT_SELECTOR} a`);
    c.check('提示里有可点的入口', (await link.count()) === 1 && Boolean(await link.getAttribute('href')), await link.getAttribute('href'));
    await link.click();
    await until(async () => new URL(page.url()).pathname === '/omc/repo-keys', { label: 'repo-keys opened from the pointer' });
    await page.getByPlaceholder('按仓库、密钥或 agent 搜索').waitFor();
    const landed = await readLocation(page);
    c.check('点击后地址为 /omc/repo-keys，打开的是仓库自有密钥页', landed.path === '/omc/repo-keys' && landed.heading === '仓库自有密钥', JSON.stringify(landed));
    c.check('菜单高亮落在「仓库自有密钥」', same(landed.selected, ['/repo-keys']), JSON.stringify(landed.selected));
    c.evidenceFile(await shot(page, 'C-12-H-landed-repo-keys-zh'));
    await context.close();
  }

  // ---- C-13: empty, failed and pending states
  if (wants('C-13')) {
    const c = R.begin('C-13', '列表为空、读取失败、读取未完成时提示仍在', 'P1');
    const panels = { H: {}, B: {} };
    for (const [side, app] of sides) {
      for (const state of ['empty', 'failed', 'pending']) {
        const context = await newContext(browser, app);
        const held = [];
        await context.route(/\/api\/v1\/management\//, async (route) => {
          const request = route.request();
          if (request.method() !== 'GET') return route.continue();
          const url = new URL(request.url());
          if (state === 'failed') return route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"stub failure"}' });
          if (state === 'pending') { held.push(route); return undefined; }
          if (/\/management\/api-keys$/.test(url.pathname)) return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ keys: [], total: 0 }) });
          if (/\/management\/config$/.test(url.pathname)) {
            const response = await route.fetch();
            const json = await response.json();
            return route.fulfill({ response, json: { ...json, safe_yaml: 'access:\n  api-keys: []\n' } });
          }
          return route.continue();
        });
        const page = await context.newPage();
        await page.goto(`${app}/api-keys`, { waitUntil: 'domcontentloaded' });
        await page.locator('.app-menu li[data-route-path]').first().waitFor({ state: 'attached' });
        if (side === 'H') {
          await page.locator(HINT_SELECTOR).waitFor();
        }
        const panel = page.locator('.keys-page [class*="keys-panel"]').first();
        const settled = { empty: /暂无客户端密钥/, failed: /无法获取配置基线/, pending: /^$/ }[state];
        if (state === 'pending') await until(async () => held.length > 0, { label: `${side} reads held` });
        else await until(async () => settled.test(await panel.innerText()), { label: `${side} ${state} state drawn`, timeoutMs: 20_000 });
        // Only the gateway list's own panel is compared: the baseline page carries the repository panel below it.
        panels[side][state] = (await panel.innerText()).replace(/\s+/g, ' ').trim();
        if (side === 'H') {
          const hint = (await page.locator(HINT_SELECTOR).innerText()).replace(/\s+/g, ' ');
          c.check(`H ${state}：页底提示在，文字相同`, /仓库自有密钥已移到「能力」分组。/.test(hint), hint);
          c.evidenceFile(await shot(page, `C-13-H-${state}-zh`));
          await page.locator(HINT_SELECTOR).locator('a').click();
          await until(async () => new URL(page.url()).pathname === '/omc/repo-keys', { label: `${state} pointer click` });
          c.check(`H ${state}：点击提示进入 /omc/repo-keys`, new URL(page.url()).pathname === '/omc/repo-keys', page.url());
        }
        for (const route of held) await route.abort().catch(() => undefined);
        await context.close();
      }
    }
    c.check('空态：H 的列表区提示与 B 同状态下相同', panels.H.empty === panels.B.empty, `${panels.H.empty} | ${panels.B.empty}`);
    c.check('读取失败：H 的列表区提示与 B 同状态下相同', panels.H.failed === panels.B.failed, `${panels.H.failed} | ${panels.B.failed}`);
    c.check('读取未完成：H 的列表区呈现与 B 同状态下相同', panels.H.pending === panels.B.pending, `${panels.H.pending} | ${panels.B.pending}`);
    c.note(`列表区文字：${JSON.stringify(panels)}`);
  }

  // ---- C-14: four languages, two viewports
  if (wants('C-14')) {
    const c = R.begin('C-14', '页底提示的四种语言与手机宽度', 'P2');
    for (const [label, viewport, touch] of [['desktop', { width: 1440, height: 900 }, false], ['phone', { width: 390, height: 844 }, true]]) {
      for (const lang of LANGS) {
        const context = await newContext(browser, APP, { lang, viewport, touch });
        const page = await context.newPage();
        await page.goto(`${APP}/api-keys`, { waitUntil: 'domcontentloaded' });
        await page.locator(HINT_SELECTOR).waitFor();
        await page.locator(HINT_SELECTOR).scrollIntoViewIfNeeded();
        const text = (await page.locator(HINT_SELECTOR).innerText()).replace(/\s+/g, ' ');
        const widths = await page.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, innerWidth: window.innerWidth }));
        c.check(`${label} ${lang}：提示文字齐全（含分组名「${NAMES[lang].group}」），没有内部键名`, text.length > 8 && !/\bkeys\.|repo_keys/.test(text) && text.includes(NAMES[lang].group), text);
        c.check(`${label} ${lang}：没有横向滚动`, widths.scrollWidth <= widths.innerWidth, JSON.stringify(widths));
        if (label === 'phone') {
          const box = await page.locator(HINT_SELECTOR).boundingBox();
          c.check(`phone ${lang}：提示完整可见`, box && box.x >= 0 && box.x + box.width <= widths.innerWidth + 1, JSON.stringify(box));
          if (lang === 'zh' || lang === 'ms') c.evidenceFile(await shot(page, `C-14-H-api-keys-bottom-${lang}-390`));
          await page.locator(`${HINT_SELECTOR} a`).tap();
          await until(async () => new URL(page.url()).pathname === '/omc/repo-keys', { label: `${lang} tap pointer` });
          c.check(`phone ${lang}：点击进入 /omc/repo-keys`, new URL(page.url()).pathname === '/omc/repo-keys', page.url());
        }
        await context.close();
      }
    }
  }
} finally {
  for (const api of Object.values(apis)) await api.dispose().catch(() => {});
  await shutdown(browser);
  R.finish();
}
