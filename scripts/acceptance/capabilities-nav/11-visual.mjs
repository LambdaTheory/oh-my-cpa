// C-53, C-54, C-56: the other pages unchanged (operations, text and a pixel comparison of their content areas), dark mode / phone /
// four languages for the new group and page, and lazy loading of the new page.
//   node scripts/acceptance/capabilities-nav/11-visual.mjs
import fs from 'node:fs';
import path from 'node:path';
import { APP, BASE_APP, NAMES, Results, SHOTS, apiClient, disableFixturePlugin, launch, newContext, normalizeRequest, same, shot, shotTall, shutdown, spaced, trackRequests, until, wants, writeJson } from './lib.mjs';
import { open, openDrawer } from './nav.mjs';
import { createDataset, installRepoKeyStub, NOW } from './fixture-repokeys.mjs';
import { firstModelGroupId } from './seed.mjs';

const R = new Results('11-visual');
const browser = await launch();
const sides = [['B', BASE_APP], ['H', APP]];
const apis = {};
const mask = (text) => text.replace(/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/g, '<id>').replace(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/g, 'TS');
// Two fake CPAs seeded in different minutes anchor their readings to different clock times; the clock tokens are the environment's, not the page's.
const flat = (text) => mask(text.replace(/\s+/g, ' ').trim()).replace(/\b\d{1,2}:\d{2}\b/g, 'HH:MM');
const apiWrites = (tracker, mark) => tracker.since(mark).filter((entry) => entry.path.includes('/api/') && entry.method !== 'GET').map((entry) => `${normalizeRequest(entry)} ${JSON.stringify(entry.bodyKeys)}`);

/** Draws two PNG files on canvases inside a scratch page and returns the share of pixels that differ. */
async function pixelDiff(scratch, fileA, fileB) {
  const a = fs.readFileSync(fileA).toString('base64');
  const b = fs.readFileSync(fileB).toString('base64');
  return scratch.evaluate(async ({ a: left, b: right }) => {
    const load = (data) => new Promise((resolve, reject) => { const image = new Image(); image.onload = () => resolve(image); image.onerror = reject; image.src = `data:image/png;base64,${data}`; });
    const [imageA, imageB] = await Promise.all([load(left), load(right)]);
    const width = Math.max(imageA.width, imageB.width);
    const height = Math.max(imageA.height, imageB.height);
    const read = (image) => { const canvas = document.createElement('canvas'); canvas.width = width; canvas.height = height; const context = canvas.getContext('2d'); context.drawImage(image, 0, 0); return context.getImageData(0, 0, width, height).data; };
    const dataA = read(imageA);
    const dataB = read(imageB);
    let differing = 0;
    let firstRow = -1;
    let lastRow = -1;
    for (let index = 0; index < dataA.length; index += 4) {
      if (Math.abs(dataA[index] - dataB[index]) > 8 || Math.abs(dataA[index + 1] - dataB[index + 1]) > 8 || Math.abs(dataA[index + 2] - dataB[index + 2]) > 8) {
        differing += 1;
        const row = Math.floor(index / 4 / width);
        if (firstRow < 0) firstRow = row;
        lastRow = row;
      }
    }
    return { sizeA: [imageA.width, imageA.height], sizeB: [imageB.width, imageB.height], differing, total: width * height, ratio: differing / (width * height), firstRow, lastRow };
  }, { a, b });
}

try {
  for (const [side, app] of sides) { apis[side] = await apiClient(app); await apis[side].setHidden([]); await disableFixturePlugin(apis[side]); }
  const scratchContext = await browser.newContext();
  const scratch = await scratchContext.newPage();

  // ---- C-53
  if (wants('C-53')) {
    const c = R.begin('C-53', '其余页面的功能与外观与 B 一致', 'P1');
    // Leftovers of an earlier run would make "create" a duplicate.
    for (const [, app] of sides) {
      const api = await apiClient(app);
      for (const group of ((await api.get('/api/v1/management/model-groups')).body.groups ?? []).filter((entry) => /^nav-check-group/.test(entry.name))) await api.del(`/api/v1/management/model-groups/${group.id}`);
      await api.dispose();
    }
    const trace = { B: [], H: [] };
    for (const [side, app] of sides) {
      const context = await newContext(browser, app);
      const tracker = trackRequests(context);
      const page = await context.newPage();
      const step = async (name, action) => {
        const mark = tracker.mark();
        await action();
        await tracker.quiet({ quietMs: 800 });
        const toasts = await page.locator('.ant-message-notice, .ant-notification-notice, [role="status"], [role="alert"]').allInnerTexts().catch(() => []);
        trace[side].push({ name, text: flat(await page.locator('.app-content').innerText()), buttons: await page.locator('.app-content button').evaluateAll((nodes) => nodes.map((node) => `${(node.getAttribute('aria-label') || node.textContent || '').replace(/\s+/g, ' ').trim()}|${node.disabled}`)), writes: apiWrites(tracker, mark), toasts: toasts.map(flat) });
      };
      const dialogConfirm = async () => { await page.locator('[role="dialog"]:visible .ant-modal-footer button').last().click(); await page.locator('[role="dialog"]:visible').waitFor({ state: 'hidden' }); };
      await open(page, `${app}/model-groups`);
      await page.getByRole('button', { name: '新建组' }).first().waitFor();
      await step('0 模型组列表', async () => {});
      await step('1 新建组 nav-check-group', async () => {
        await page.getByRole('button', { name: '新建组' }).first().click();
        await page.locator('[role="dialog"]:visible input').fill('nav-check-group');
        await dialogConfirm();
        await until(async () => (await page.getByText('nav-check-group').count()) > 0, { label: `${side} group listed` });
      });
      await step('2 在详情页加入 2 个模型', async () => {
        await page.getByRole('button', { name: spaced('打开') }).last().click();
        await page.locator('.app-content h1').filter({ hasText: 'nav-check-group' }).waitFor();
        await page.getByRole('button', { name: '加入模型' }).first().click();
        await page.locator('.app-content h1').filter({ hasText: '加入模型到' }).waitFor();
        await until(async () => (await page.getByText('gpt-e2e-second').count()) > 0, { label: `${side} candidates drawn` });
        for (const model of ['gpt-e2e', 'gpt-e2e-second']) await page.locator('.app-content tr').filter({ has: page.locator('code').filter({ hasText: new RegExp(`^${model}$`) }) }).first().locator('input[type="checkbox"]').evaluate((input) => input.click());
        await page.getByText(/已选 2 个/).waitFor();
        await page.getByRole('button', { name: /加入 2 个模型/ }).click();
        await page.locator('.app-content h1').filter({ hasText: 'nav-check-group' }).waitFor();
        await until(async () => (await page.getByText('gpt-e2e-second').count()) > 0, { label: `${side} members drawn` });
      });
      c.evidenceFile(await shot(page, `C-53-${side}-group-detail-zh`));
      await step('3 调整一次成员顺序（下移 gpt-e2e）', async () => { await page.getByRole('button', { name: '下移 gpt-e2e', exact: true }).click(); });
      await step('4 改名为 nav-check-group-2', async () => {
        await page.getByRole('button', { name: '改名' }).first().click();
        await page.locator('[role="dialog"]:visible input').fill('nav-check-group-2');
        await dialogConfirm();
        await page.locator('.app-content h1').filter({ hasText: 'nav-check-group-2' }).waitFor();
      });
      await step('5 删除该组', async () => {
        await page.getByRole('button', { name: /返回模型组/ }).click();
        await page.getByRole('button', { name: '新建组' }).first().waitFor();
        // The group made in this flow is the last one in the list.
        await page.getByRole('button', { name: spaced('删除') }).last().click();
        await page.locator('.ant-popconfirm').getByRole('button', { name: spaced('删除') }).click();
        await until(async () => (await page.getByText('nav-check-group-2').count()) === 0, { label: `${side} group removed` });
      });
      await step('6 恢复默认组', async () => { await page.getByRole('button', { name: spaced('恢复默认组') }).click(); });
      await context.close();
    }
    writeJson('c53-model-groups-trace.json', trace);
    for (let index = 0; index < trace.H.length; index += 1) {
      const h = trace.H[index];
      const b = trace.B[index];
      c.check(`模型组 ${h.name}：界面文字、按钮状态两端相同`, h.text === b.text && same(h.buttons, b.buttons), h.text === b.text ? JSON.stringify(h.buttons) : `${h.text.slice(0, 200)} vs ${b.text.slice(0, 200)}`);
      c.check(`模型组 ${h.name}：写请求与提示两端相同`, same(h.writes, b.writes) && same(h.toasts, b.toasts), `${JSON.stringify(h.writes)} ${JSON.stringify(h.toasts)} vs ${JSON.stringify(b.writes)} ${JSON.stringify(b.toasts)}`);
    }
    // OAuth page header, the lending entry, the first five credential rows; and the lending page.
    const pages = {};
    for (const [side, app] of sides) {
      const context = await newContext(browser, app);
      const page = await context.newPage();
      await open(page, `${app}/oauth-management?page_size=48`);
      const entry = page.locator('[data-lending-entry]').first();
      await entry.waitFor({ timeout: 30_000 });
      await until(async () => (await page.getByText('codex-demo-a').count()) > 0, { label: `${side} credential rows` });
      const header = flat(await page.locator('.app-content h1').first().evaluate((node) => node.closest('header, [class*="head"], div')?.innerText ?? node.innerText));
      // The credential list is drawn as cards, not table rows: compare the page's whole text (header, entry, credentials).
      const rows = [flat(await page.locator('.app-content').innerText())];
      const entryText = flat(await entry.innerText());
      await entry.click();
      await page.locator('[data-lending-pool]').waitFor({ timeout: 30_000 });
      const lending = flat(await page.locator('.app-content').innerText());
      const lendingTitle = (await page.locator('.app-content h1').first().innerText()).trim();
      pages[side] = { header, entryText, rows, lending, lendingTitle };
      await context.close();
    }
    writeJson('c53-oauth-lending.json', pages);
    c.check('OAuth 管理页：页头统计行与额度拆借入口的文字（三态之一）两端相同', pages.H.entryText === pages.B.entryText && pages.H.header === pages.B.header, `${pages.H.entryText} | ${pages.B.entryText}`);
    c.check('OAuth 管理页：页内全部文字（页头统计行、额度拆借入口、凭证列表）两端相同，且读到了凭证', same(pages.H.rows, pages.B.rows) && /codex-demo-a/.test(pages.H.rows[0]), `${JSON.stringify(pages.H.rows).slice(0, 400)} vs ${JSON.stringify(pages.B.rows).slice(0, 400)}`);
    c.check('额度拆借页：页内标题与两张卡的内容两端相同', pages.H.lendingTitle === pages.B.lendingTitle && pages.H.lending === pages.B.lending, `${pages.H.lending.slice(0, 300)} vs ${pages.B.lending.slice(0, 300)}`);
    // Content-area pictures, side by side, with a pixel comparison.
    const groupId = { B: await firstModelGroupId(apis.B), H: await firstModelGroupId(apis.H) };
    const targets = [['model-groups', () => '/model-groups'], ['group-detail', (side) => `/model-groups/${groupId[side]}`], ['group-add', (side) => `/model-groups/${groupId[side]}/add`], ['oauth-management', () => '/oauth-management'], ['quota-lending', () => '/quota-lending']];
    const diffs = {};
    for (const [name, route] of targets) {
      const files = {};
      for (const [side, app] of sides) {
        const context = await newContext(browser, app, { viewport: { width: 1440, height: 2000 } });
        await context.clock.setFixedTime(NOW);
        const page = await context.newPage();
        await open(page, `${app}${route(side)}`);
        await page.locator('.app-content h1').first().waitFor();
        await trackRequests(context).quiet({ quietMs: 1200, timeoutMs: 45_000 });
        files[side] = path.join(SHOTS, `C-53-${name}-${side}.png`);
        await page.locator('.app-content').screenshot({ path: files[side] });
        await context.close();
      }
      diffs[name] = await pixelDiff(scratch, files.B, files.H);
      c.evidenceFile(files.B); c.evidenceFile(files.H);
    }
    writeJson('c53-pixel-diff.json', diffs);
    for (const [name, diff] of Object.entries(diffs)) {
      c.check(`${name}：内容区图像两端逐像素对比，差异像素占比 ≤ 1%（含随时间变化的文字）`, diff.ratio <= 0.01, JSON.stringify(diff));
    }
    c.note(`图像对比：${JSON.stringify(diffs)}`);
  }

  // ---- C-54: dark, phone and four languages for the new group and page
  if (wants('C-54')) {
    const c = R.begin('C-54', '深色、手机与四种语言下的新分组与新页', 'P2');
    const combos = [
      { key: 'dark-zh-desktop', lang: 'zh', theme: 'omc-dark', phone: false },
      { key: 'dark-en-desktop', lang: 'en', theme: 'omc-dark', phone: false },
      { key: 'light-zhHant-phone', lang: 'zh-Hant', theme: 'omc-light', phone: true },
      { key: 'light-ms-phone', lang: 'ms', theme: 'omc-light', phone: true },
    ];
    for (const combo of combos) {
      const viewport = combo.phone ? { width: 390, height: 844 } : { width: 1440, height: 900 };
      const context = await newContext(browser, APP, { lang: combo.lang, theme: combo.theme, viewport, touch: combo.phone });
      await context.clock.setFixedTime(NOW + 60_000);
      await installRepoKeyStub(context, createDataset());
      const page = await context.newPage();
      if (combo.phone) {
        await page.goto(`${APP}/dashboard`, { waitUntil: 'domcontentloaded' });
        await page.locator('.app-header-left button').first().waitFor();
        await openDrawer(page);
        c.evidenceFile(await shotTall(page, `C-54-${combo.key}-menu`, 1250));
      } else {
        await open(page, `${APP}/dashboard`);
        c.evidenceFile(await shotTall(page, `C-54-${combo.key}-menu`));
      }
      const target = combo.phone ? '.mobile-nav-drawer .app-menu' : '.app-menu';
      const menu = await page.evaluate((selector) => {
        const root = document.querySelector(selector);
        const labels = [...(root?.querySelectorAll('.ant-menu-title-content') ?? [])];
        const titles = [...(root?.querySelectorAll('.ant-menu-item-group-title') ?? [])];
        return {
          truncated: labels.filter((node) => node.scrollWidth > node.clientWidth + 1).map((node) => node.textContent),
          overflowsX: root ? root.scrollWidth > root.clientWidth + 1 : null,
          text: root?.innerText ?? '',
          titles: titles.map((node) => ({ text: node.textContent?.trim(), color: getComputedStyle(node).color })),
          pageOverflow: document.documentElement.scrollWidth > window.innerWidth,
        };
      }, target);
      const expected = NAMES[combo.lang];
      c.check(`${combo.key}：「${expected.group}」分组与三个条目名都在菜单里，没有内部键名`, [expected.group, expected.modelGroups, expected.repoKeys, expected.lending].every((text) => menu.text.toLowerCase().includes(text.toLowerCase())) && !/\bnav\.|\brepo_keys\./.test(menu.text), menu.text.replace(/\s+/g, ' ').slice(0, 300));
      c.check(`${combo.key}：菜单里没有被截断的文字，没有横向滚动`, menu.truncated.length === 0 && menu.overflowsX === false && menu.pageOverflow === false, JSON.stringify({ truncated: menu.truncated, overflowsX: menu.overflowsX, pageOverflow: menu.pageOverflow }));
      if (combo.theme === 'omc-dark') {
        const observe = menu.titles.find((entry) => entry.text?.toLowerCase() === (combo.lang === 'zh' ? '观测' : 'observe'));
        const capabilities = menu.titles.find((entry) => entry.text?.toLowerCase() === expected.group.toLowerCase());
        c.check(`${combo.key}：深色下「能力」标题与「观测」标题的颜色相同`, Boolean(observe && capabilities) && observe.color === capabilities.color, JSON.stringify([observe, capabilities]));
      }
      await context.close();
      // The new page itself, with the stub's data.
      const pageContext = await newContext(browser, APP, { lang: combo.lang, theme: combo.theme, viewport, touch: combo.phone });
      await pageContext.clock.setFixedTime(NOW + 60_000);
      await installRepoKeyStub(pageContext, createDataset());
      const repoPage = await pageContext.newPage();
      await repoPage.goto(`${APP}/repo-keys`, { waitUntil: 'domcontentloaded' });
      await repoPage.getByRole('heading', { name: /\(\d+\)|（\d+）/ }).first().waitFor();
      await trackRequests(pageContext).quiet({ quietMs: 800, timeoutMs: 30_000 });
      const state = await repoPage.evaluate(() => {
        const content = document.querySelector('.app-content');
        const wide = [...(content?.querySelectorAll('*') ?? [])].filter((node) => { const rect = node.getBoundingClientRect(); return rect.width > 0 && rect.right > window.innerWidth + 1 && !node.closest('.ant-table-content, .ant-table-body, [class*="overlay"], [class*="scroll"]'); }).slice(0, 3).map((node) => node.tagName + '.' + String(node.className).slice(0, 40));
        return { pageOverflow: document.documentElement.scrollWidth > window.innerWidth, wide, text: content?.innerText ?? '' };
      });
      c.check(`${combo.key}：仓库自有密钥页清晰可读，没有横向滚动，没有内部键名`, state.pageOverflow === false && !/\brepo_keys\.|\bkeys\.[a-z_]+\b/.test(state.text), JSON.stringify({ pageOverflow: state.pageOverflow, wide: state.wide }));
      c.evidenceFile(await shot(repoPage, `C-54-${combo.key}-repo-keys`));
      await pageContext.close();
    }
  }

  // ---- C-56: the new page loads only when it is opened
  if (wants('C-56')) {
    const c = R.begin('C-56', '新页按需加载，不拖慢首屏', 'P2');
    const firstScreen = {};
    const lazy = {};
    for (const [side, app] of sides) {
      const context = await newContext(browser, app);
      const scripts = [];
      context.on('requestfinished', async (request) => {
        if (request.resourceType() !== 'script') return;
        const sizes = await request.sizes().catch(() => null);
        scripts.push({ url: new URL(request.url()).pathname.replace(/^.*\/assets\//, 'assets/'), bytes: sizes?.responseBodySize ?? 0 });
      });
      const tracker = trackRequests(context);
      const page = await context.newPage();
      await open(page, `${app}/dashboard`);
      await tracker.quiet({ quietMs: 1500, timeoutMs: 45_000 });
      firstScreen[side] = { count: scripts.length, bytes: scripts.reduce((sum, entry) => sum + entry.bytes, 0), names: scripts.map((entry) => entry.url) };
      if (side === 'H') {
        const before = scripts.length;
        await page.locator('.app-menu [data-route-path="/repo-keys"]').click();
        await until(async () => new URL(page.url()).pathname === '/omc/repo-keys', { label: 'repo-keys opened' });
        await tracker.quiet({ quietMs: 1500, timeoutMs: 45_000 });
        lazy.H = scripts.slice(before).map((entry) => `${entry.url} ${entry.bytes}`);
      }
      await context.close();
    }
    const growth = (firstScreen.H.bytes - firstScreen.B.bytes) / firstScreen.B.bytes;
    c.check('首屏脚本请求里没有仓库自有密钥页的脚本', !firstScreen.H.names.some((name) => /RepoKeysPage/.test(name)), JSON.stringify(firstScreen.H.names.filter((name) => /Keys/.test(name))));
    c.check('点进仓库自有密钥页时才新增该页的脚本请求', lazy.H.some((entry) => /RepoKeysPage/.test(entry)), JSON.stringify(lazy.H));
    c.check('首屏脚本总字节相对基线的增幅不超过 5%（超过只记范围外观察）', growth <= 0.05, `B=${firstScreen.B.bytes} (${firstScreen.B.count} 个) H=${firstScreen.H.bytes} (${firstScreen.H.count} 个) 增幅 ${(growth * 100).toFixed(2)}%`);
    c.note(`首屏：B ${firstScreen.B.count} 个脚本 ${firstScreen.B.bytes} 字节；H ${firstScreen.H.count} 个脚本 ${firstScreen.H.bytes} 字节；点进新页新增：${JSON.stringify(lazy.H)}`);
    writeJson('c56-scripts.json', { firstScreen, lazy });
  }
  await scratchContext.close();
} finally {
  for (const api of Object.values(apis)) await api.dispose().catch(() => {});
  await shutdown(browser);
  R.finish();
}
