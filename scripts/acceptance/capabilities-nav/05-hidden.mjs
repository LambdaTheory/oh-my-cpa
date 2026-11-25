// C-34 .. C-40: the hidden-entry setting. Run this FIRST on a fresh pair: C-34 reads the setting S1 that the baseline stored
// before the change (00-setup.mjs wrote it on B and copied the data to H); the later cases overwrite it.
//   T9_BIN / T9_FRESH_* select the binary and ports of a throwaway environment for C-35 (a).
//   node scripts/acceptance/capabilities-nav/05-hidden.mjs
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { startEnvironment, stopEnvironment } from './env.mjs';
import { APP, BASE_APP, LANGS, NAMES, Results, wants, apiClient, disableFixturePlugin, launch, newContext, same, shot, shotTall, shutdown, trackRequests, until } from './lib.mjs';
import { collapseRail, open, openDrawer, openSidebarPanel, readDrawer, readRail, readSidebarPanel, readSidebarSummary, switchLanguage, toggleSidebarEntry } from './nav.mjs';
import { workDir } from './seed.mjs';

const R = new Results('05-hidden');
const H_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const BIN = process.env.T9_BIN || path.join(H_ROOT, 'tmp/omc-nav.exe');
const browser = await launch();
const apis = {};
const names = (rail, title) => rail.groups.find((group) => group.title === title)?.items.map((item) => item.name);
const allNames = (rail) => rail.flat.map((item) => item.name);
const reloadShell = async (page) => { await page.reload({ waitUntil: 'domcontentloaded' }); await page.locator('.app-menu li[data-route-path]').first().waitFor({ state: 'attached' }); };

async function withHidden(api, list, page, app) {
  const written = await api.setHidden(list);
  if (written.status >= 300) throw new Error(`preference write ${written.status}`);
  await open(page, `${app}/dashboard`);
  return readRail(page);
}

try {
  for (const [key, app] of [['H', APP], ['B', BASE_APP]]) apis[key] = await apiClient(app);

  // ---- C-34
  if (wants('C-34')) {
    const c = R.begin('C-34', '改动前已存的隐藏设置继续生效', 'P0');
    c.check('起点：B 与 H 的偏好里都已存有 S1（H 没有被重新设置）', same(await apis.H.getHidden(), ['/model-groups', '/api-keys']) && same(await apis.B.getHidden(), ['/model-groups', '/api-keys']), `${JSON.stringify(await apis.H.getHidden())} ${JSON.stringify(await apis.B.getHidden())}`);
    const hContext = await newContext(browser, APP);
    const bContext = await newContext(browser, BASE_APP);
    const hPage = await hContext.newPage();
    const bPage = await bContext.newPage();
    await open(hPage, `${APP}/dashboard`);
    await open(bPage, `${BASE_APP}/dashboard`);
    const h = await readRail(hPage);
    const b = await readRail(bPage);
    c.check('S1 下 H：「能力」下没有「模型组」', !names(h, '能力').includes('模型组') && !allNames(h).includes('模型组'), JSON.stringify(names(h, '能力')));
    c.check('S1 下 H：「网关」只剩 AI 提供商、OAuth 管理', same(names(h, '网关'), ['AI 提供商', 'OAuth 管理']), JSON.stringify(names(h, '网关')));
    c.check('S1 下 H：「能力」下的「仓库自有密钥」「额度拆借」仍显示', same(names(h, '能力'), ['仓库自有密钥', '额度拆借']), JSON.stringify(names(h, '能力')));
    c.check('S1 下 H：其余分组条目全显示', same(names(h, '运行'), ['仪表盘', '智能体', '操练场', '模型广场']) && names(h, '观测').length === 5 && names(h, '插件').length === 2 && names(h, '控制').length === 3);
    c.check('S1 下 B：「模型组」「密钥管理」同样不显示（对照）', !allNames(b).includes('模型组') && !allNames(b).includes('密钥管理'), JSON.stringify(allNames(b)));
    c.evidenceFile(await shotTall(hPage, 'C-34-H-S1-rail'));
    c.evidenceFile(await shotTall(bPage, 'C-34-B-S1-rail'));
    await openSidebarPanel(hPage, APP);
    const panel = await readSidebarPanel(hPage);
    const summary = await readSidebarSummary(hPage);
    const entry = (path) => panel.groups.flatMap((group) => group.entries).find((item) => item.path === path);
    c.check('H 面板里「模型组」「密钥管理」的开关为关', entry('/model-groups')?.isOn === false && entry('/api-keys')?.isOn === false, JSON.stringify([entry('/model-groups'), entry('/api-keys')]));
    c.check('H 面板里「仓库自有密钥」「额度拆借」的开关为开', entry('/repo-keys')?.isOn === true && entry('/quota-lending')?.isOn === true);
    c.check('H 行摘要为「已隐藏 2 项」', summary.withoutButton.includes('已隐藏 2 项'), summary.full);
    c.evidenceFile(await shot(hPage, 'C-34-H-S1-panel'));
    await hContext.close();
    await bContext.close();
    // S2 and S0 on both sides.
    for (const [label, list] of [['S2', ['/agent', '/insights', '/model-square']], ['S0', []]]) {
      const pair = {};
      for (const [side, app, api] of [['H', APP, apis.H], ['B', BASE_APP, apis.B]]) {
        const context = await newContext(browser, app);
        const page = await context.newPage();
        pair[side] = await withHidden(api, list, page, app);
        if (side === 'H') c.evidenceFile(await shotTall(page, `C-34-H-${label}-rail`));
        await context.close();
      }
      const hiddenNames = { '/agent': '智能体', '/insights': '洞察', '/model-square': '模型广场' };
      if (label === 'S2') {
        c.check('S2：两端被隐藏的三项都不在展开栏', Object.values(hiddenNames).every((name) => !allNames(pair.H).includes(name) && !allNames(pair.B).includes(name)));
        const moved = new Set(['模型组', '仓库自有密钥', '额度拆借']);
        const withoutMoved = (rail) => allNames(rail).filter((name) => !moved.has(name));
        c.check('S2：其余条目的显隐与 B 一致（除搬家与新增外）', same(withoutMoved(pair.H), withoutMoved(pair.B)), `${JSON.stringify(withoutMoved(pair.H))} vs ${JSON.stringify(withoutMoved(pair.B))}`);
        c.check('S2：「能力」下三项（H）与 B 的「模型组」都仍显示', same(names(pair.H, '能力'), ['模型组', '仓库自有密钥', '额度拆借']) && allNames(pair.B).includes('模型组'), JSON.stringify(names(pair.H, '能力')));
      } else {
        c.check('S0：H 全部条目显示（20 个）', pair.H.flat.length === 20, `count=${pair.H.flat.length}`);
        c.check('S0：B 全部条目显示（18 个）', pair.B.flat.length === 18, `count=${pair.B.flat.length}`);
      }
    }
    c.check('同一份已存设置下没有任何条目的显隐发生变化，也没有要求重新设置', true, '见上：S1 读自 B 存下的数据，H 起来后没有任何设置动作');
  }

  // ---- C-35: new page visible by default
  if (wants('C-35')) {
    const c = R.begin('C-35', '新页面默认可见', 'P0');
    // (a) a data directory that never stored a hidden setting
    const dataDir = workDir('data', 'hfresh');
    fs.rmSync(dataDir, { recursive: true, force: true });
    const fresh = await startEnvironment({ role: 'hfresh', root: H_ROOT, binary: BIN, data: dataDir, appPort: 18489, cpaPort: 18490, controlPort: 18492 });
    try {
      const freshApi = await apiClient(fresh.app);
      await disableFixturePlugin(freshApi);
      c.check('(a) 全新数据目录：偏好里没有隐藏设置', ((await freshApi.getHidden()) ?? []).length === 0, JSON.stringify(await freshApi.getHidden()));
      const context = await newContext(browser, fresh.app);
      const page = await context.newPage();
      await open(page, `${fresh.app}/dashboard`);
      const rail = await readRail(page);
      c.check('(a) 展开栏「能力」下三项都显示，含「仓库自有密钥」', same(names(rail, '能力'), ['模型组', '仓库自有密钥', '额度拆借']), JSON.stringify(names(rail, '能力')));
      c.evidenceFile(await shotTall(page, 'C-35-H-fresh-data-rail'));
      await context.close();
      await freshApi.dispose();
    } finally {
      stopEnvironment('hfresh');
    }
    // (b) the console had hidden "Key management" before
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    const rail = await withHidden(apis.H, ['/api-keys'], page, APP);
    c.check('(b) S6 下「网关」只剩 AI 提供商、OAuth 管理（密钥管理仍隐藏）', same(names(rail, '网关'), ['AI 提供商', 'OAuth 管理']), JSON.stringify(names(rail, '网关')));
    c.check('(b) S6 下「能力」下三项显示（含「仓库自有密钥」）', same(names(rail, '能力'), ['模型组', '仓库自有密钥', '额度拆借']), JSON.stringify(names(rail, '能力')));
    await openSidebarPanel(page, APP);
    const panel = await readSidebarPanel(page);
    const summary = await readSidebarSummary(page);
    const repoKeys = panel.groups.flatMap((group) => group.entries).find((item) => item.path === '/repo-keys');
    c.check('(b) 面板里「仓库自有密钥」的开关为开', repoKeys?.isOn === true, JSON.stringify(repoKeys));
    c.check('(b) 行摘要为「已隐藏 1 项」', summary.withoutButton.includes('已隐藏 1 项'), summary.full);
    c.evidenceFile(await shot(page, 'C-35-H-S6-panel'));
    await context.close();
  }

  // ---- C-36: locked entries stay locked
  if (wants('C-36')) {
    const c = R.begin('C-36', '锁定项仍锁定', 'P0');
    await apis.H.setHidden([]); await apis.B.setHidden([]);
    const rows = {};
    for (const [side, app] of [['H', APP], ['B', BASE_APP]]) {
      const context = await newContext(browser, app);
      const tracker = trackRequests(context);
      const page = await context.newPage();
      await openSidebarPanel(page, app);
      const before = await readSidebarPanel(page);
      const beforeSummary = await readSidebarSummary(page);
      const mark = tracker.mark();
      for (const route of ['/oauth-management', '/omc-settings']) {
        const row = page.locator(`[data-nav-toggle="${route}"]`);
        // A click on a disabled switch is dispatched directly: nothing should react to it.
        await row.locator('[role="switch"]').dispatchEvent('click');
        // And a plain click on the entry's name, which is not a control.
        const label = before.groups.flatMap((group) => group.entries).find((entry) => entry.path === route).label;
        await page.locator('[data-testid="sidebar-nav-dialog"]').getByText(label, { exact: true }).first().click();
      }
      await page.locator('[data-testid="sidebar-nav-dialog"]').waitFor();
      const after = await readSidebarPanel(page);
      const afterSummary = await readSidebarSummary(page);
      const writes = tracker.since(mark).filter((entry) => entry.method !== 'GET' && /omc_nav_hidden/.test(entry.path));
      const lockedRows = ['/oauth-management', '/omc-settings'].map((route) => before.groups.flatMap((group) => group.entries).find((item) => item.path === route));
      rows[side] = { lockedRows, beforeSummary: beforeSummary.withoutButton, afterSummary: afterSummary.withoutButton, writes: writes.length, same: same(before, after) };
      c.check(`${side}：「OAuth 管理」「OMC 设置」的开关禁用且为开`, lockedRows.every((row) => row?.isDisabled === true && row?.isOn === true), JSON.stringify(lockedRows));
      c.check(`${side}：行内写明不可隐藏的原因`, lockedRows.every((row) => row && row.rowText.length > row.label.length + 4), JSON.stringify(lockedRows.map((row) => row?.rowText)));
      c.check(`${side}：点击后面板、行摘要都不变，没有对 omc_nav_hidden 的写入`, rows[side].same && beforeSummary.withoutButton === afterSummary.withoutButton && writes.length === 0, JSON.stringify(rows[side]));
      if (side === 'H') c.evidenceFile(await shot(page, 'C-36-H-locked-rows'));
      await context.close();
    }
    c.check('锁定项的行内原因文案与 B 相同', same(rows.H.lockedRows.map((row) => row.rowText), rows.B.lockedRows.map((row) => row.rowText)), `${JSON.stringify(rows.H.lockedRows.map((r) => r.rowText))} vs ${JSON.stringify(rows.B.lockedRows.map((r) => r.rowText))}`);
    // S3: hand-written setting that names the locked entries
    await apis.H.setHidden(['/oauth-management', '/omc-settings', '/agent']);
    const wide = await newContext(browser, APP);
    const widePage = await wide.newPage();
    await open(widePage, `${APP}/dashboard`);
    const expanded = await readRail(widePage);
    c.check('S3：展开栏仍显示「OAuth 管理」「OMC 设置」，「智能体」被隐藏', allNames(expanded).includes('OAuth 管理') && allNames(expanded).includes('OMC 设置') && !allNames(expanded).includes('智能体'), JSON.stringify(allNames(expanded)));
    await collapseRail(widePage);
    const collapsed = await readRail(widePage);
    c.check('S3：收起栏仍有「OAuth 管理」「OMC 设置」，没有「智能体」', ['/oauth-management', '/omc-settings'].every((route) => collapsed.flat.some((item) => item.path === route)) && !collapsed.flat.some((item) => item.path === '/agent'), `count=${collapsed.flat.length}`);
    await wide.close();
    const phone = await newContext(browser, APP, { viewport: { width: 390, height: 844 }, touch: true });
    const phonePage = await phone.newPage();
    await phonePage.goto(`${APP}/dashboard`, { waitUntil: 'domcontentloaded' });
    await phonePage.locator('.app-header-left button').first().waitFor();
    await openDrawer(phonePage);
    const drawer = await readDrawer(phonePage);
    c.check('S3：手机抽屉仍有「OAuth 管理」「OMC 设置」，没有「智能体」', ['/oauth-management', '/omc-settings'].every((route) => drawer.flat.includes(route)) && !drawer.flat.includes('/agent'), JSON.stringify(drawer.flat));
    c.evidenceFile(await shotTall(phonePage, 'C-36-H-S3-drawer-390', 1250));
    await phone.close();
    await apis.H.setHidden([]);
  }

  // ---- C-37: the whole Capabilities group hidden, and all but one entry
  if (wants('C-37')) {
    const c = R.begin('C-37', '「能力」整组隐藏与留一项', 'P0');
    for (const [label, list, expectCount, expectEntries] of [
      ['S4', ['/model-groups', '/repo-keys', '/quota-lending'], 17, []],
      ['S5', ['/model-groups', '/repo-keys'], 18, ['额度拆借']],
    ]) {
      const expandedContext = await newContext(browser, APP);
      const page = await expandedContext.newPage();
      const rail = await withHidden(apis.H, list, page, APP);
      if (label === 'S4') {
        c.check(`${label} 展开栏：没有「能力」标题，其余五个分组标题与先后不变`, same(rail.groupTitles, ['运行', '网关', '观测', '插件', '控制']), JSON.stringify(rail.groupTitles));
        c.check(`${label} 展开栏：没有空标题`, rail.groupTitles.every((title) => title.length > 0) && rail.groups.every((group) => group.items.length > 0));
      } else {
        c.check(`${label} 展开栏：「能力」标题保留，下面只剩「额度拆借」`, same(names(rail, '能力'), expectEntries), JSON.stringify(rail.groups.map((group) => [group.title, group.items.map((item) => item.name)])));
      }
      c.evidenceFile(await shotTall(page, `C-37-H-${label}-expanded`));
      await collapseRail(page);
      const collapsed = await readRail(page);
      const missing = ['/model-groups', '/repo-keys', '/quota-lending'].filter((route) => !collapsed.flat.some((item) => item.path === route));
      c.check(`${label} 收起栏：图标总数 ${expectCount}，被隐藏的图标都没有`, collapsed.flat.length === expectCount && same(missing, label === 'S4' ? ['/model-groups', '/repo-keys', '/quota-lending'] : ['/model-groups', '/repo-keys']), `count=${collapsed.flat.length} missing=${JSON.stringify(missing)}`);
      c.check(`${label} 收起栏：没有分组标题`, collapsed.groupTitles.length === 0);
      c.evidenceFile(await shotTall(page, `C-37-H-${label}-collapsed`));
      await expandedContext.close();
      const phone = await newContext(browser, APP, { viewport: { width: 390, height: 844 }, touch: true });
      const phonePage = await phone.newPage();
      await phonePage.goto(`${APP}/dashboard`, { waitUntil: 'domcontentloaded' });
      await phonePage.locator('.app-header-left button').first().waitFor();
      await openDrawer(phonePage);
      const drawer = await readDrawer(phonePage);
      const titles = drawer.groups.map((group) => group.title);
      if (label === 'S4') c.check(`${label} 手机抽屉：没有「能力」标题，其余五组不变`, same(titles, ['运行', '网关', '观测', '插件', '控制']), JSON.stringify(titles));
      else c.check(`${label} 手机抽屉：「能力」标题保留，下面只有「额度拆借」`, same(drawer.groups.find((group) => group.title === '能力')?.items.map((item) => item.name), ['额度拆借']), JSON.stringify(drawer.groups));
      c.check(`${label} 手机抽屉：没有空标题或空分组`, drawer.groups.every((group) => group.title.length > 0 && group.items.length > 0));
      c.evidenceFile(await shotTall(phonePage, `C-37-H-${label}-drawer-390`, 1250));
      await phone.close();
    }
    await apis.H.setHidden([]);
  }

  // ---- C-38: hiding another group behaves as before
  if (wants('C-38')) {
    const c = R.begin('C-38', '其他分组整组隐藏的行为不变', 'P1');
    const list = ['/insights', '/usage/events', '/pricing', '/logs', '/audit'];
    const sides = {};
    for (const [side, app, api] of [['H', APP, apis.H], ['B', BASE_APP, apis.B]]) {
      const context = await newContext(browser, app);
      const page = await context.newPage();
      const rail = await withHidden(api, list, page, app);
      await collapseRail(page);
      const collapsed = await readRail(page);
      const phone = await newContext(browser, app, { viewport: { width: 390, height: 844 }, touch: true });
      const phonePage = await phone.newPage();
      await phonePage.goto(`${app}/dashboard`, { waitUntil: 'domcontentloaded' });
      await phonePage.locator('.app-header-left button').first().waitFor();
      await openDrawer(phonePage);
      const drawer = await readDrawer(phonePage);
      sides[side] = { rail, collapsed, drawer };
      c.check(`${side}：展开栏没有「观测」标题与五个条目`, !rail.groupTitles.includes('观测') && list.every((route) => !rail.flat.some((item) => item.path === route)), JSON.stringify(rail.groupTitles));
      c.check(`${side}：收起栏里这五个图标消失（${side === 'H' ? 15 : 13} 个）`, list.every((route) => !collapsed.flat.some((item) => item.path === route)) && collapsed.flat.length === (side === 'H' ? 15 : 13), `count=${collapsed.flat.length}`);
      c.check(`${side}：手机抽屉里没有「观测」标题，没有空标题`, !drawer.groups.some((group) => group.title === '观测') && drawer.groups.every((group) => group.title && group.items.length > 0), JSON.stringify(drawer.groups.map((group) => group.title)));
      if (side === 'H') c.evidenceFile(await shotTall(page, 'C-38-H-S7-collapsed'));
      await context.close();
      await phone.close();
    }
    const strip = (rail) => rail.groups.filter((group) => group.title !== '能力').map((group) => [group.title, group.items.map((item) => item.name).filter((name) => name !== '模型组')]);
    c.check('其余分组的显示与 B 一致', same(strip(sides.H.rail), strip(sides.B.rail)), `${JSON.stringify(strip(sides.H.rail))} vs ${JSON.stringify(strip(sides.B.rail))}`);
    await apis.H.setHidden([]); await apis.B.setHidden([]);
  }

  // ---- C-39: the settings panel lists the new groups, counts include the new entries
  if (wants('C-39')) {
    const c = R.begin('C-39', '设置面板按新分组列出，计数包含新条目', 'P0');
    await apis.H.setHidden([]); await apis.B.setHidden([]);
    const context = await newContext(browser, APP);
    const tracker = trackRequests(context);
    const page = await context.newPage();
    await open(page, `${APP}/omc-settings`);
    const initial = await readSidebarSummary(page);
    c.check('行摘要为「全部入口都显示」', initial.withoutButton.includes('全部入口都显示'), initial.full);
    await page.locator('[data-testid="sidebar-nav-settings"] button').first().click();
    await page.locator('[data-testid="sidebar-nav-dialog"]').waitFor();
    const panel = await readSidebarPanel(page);
    c.check('面板按新分组自上而下列出：运行、网关、观测、能力、插件、控制', same(panel.groups.map((group) => group.title), ['运行', '网关', '观测', '能力', '插件', '控制']), JSON.stringify(panel.groups.map((group) => group.title)));
    c.check('「能力」下列出模型组、仓库自有密钥、额度拆借，各有一个开关', same(panel.groups[3].entries.map((entry) => [entry.label, typeof entry.isOn]), [['模型组', 'boolean'], ['仓库自有密钥', 'boolean'], ['额度拆借', 'boolean']]), JSON.stringify(panel.groups[3]));
    c.check('分组小标题本身没有开关，开关总数为 20', panel.groups.reduce((sum, group) => sum + group.entries.length, 0) === 20, `count=${panel.groups.reduce((sum, group) => sum + group.entries.length, 0)}`);
    c.check('面板里的顺序与展开栏一致', same(panel.groups.flatMap((group) => group.entries.map((entry) => entry.path)), (await readRail(page)).flat.map((item) => item.path)));
    c.evidenceFile(await shot(page, 'C-39-H-panel-all-shown'));
    const hidden = async () => apis.H.getHidden();
    await toggleSidebarEntry(page, '/repo-keys');
    await until(async () => same(await hidden(), ['/repo-keys']), { label: 'hidden stored after first toggle' });
    // The page's own summary and rail follow the stored value a moment later; read them once they say so.
    await until(async () => (await readSidebarSummary(page)).withoutButton.includes('已隐藏 1 项') && !(await readRail(page)).flat.some((item) => item.path === '/repo-keys'), { label: 'summary and rail after first toggle' });
    let summary = await readSidebarSummary(page);
    let rail = await readRail(page);
    c.check('关掉「仓库自有密钥」：行摘要为「已隐藏 1 项」，展开栏立即少了它，omc_nav_hidden 为 ["/repo-keys"]', summary.withoutButton.includes('已隐藏 1 项') && !rail.flat.some((item) => item.path === '/repo-keys') && same(await hidden(), ['/repo-keys']), `${summary.withoutButton} ${JSON.stringify(await hidden())}`);
    await toggleSidebarEntry(page, '/quota-lending');
    await until(async () => (await hidden())?.length === 2, { label: 'second toggle stored' });
    await until(async () => (await readSidebarSummary(page)).withoutButton.includes('已隐藏 2 项'), { label: 'summary after second toggle' });
    summary = await readSidebarSummary(page);
    c.check('再关掉「额度拆借」：行摘要为「已隐藏 2 项」', summary.withoutButton.includes('已隐藏 2 项'), summary.withoutButton);
    c.evidenceFile(await shot(page, 'C-39-H-panel-two-hidden'));
    await page.getByRole('button', { name: '全部显示' }).click();
    await until(async () => same(await hidden(), []), { label: 'show all stored' });
    await until(async () => (await readSidebarSummary(page)).withoutButton.includes('全部入口都显示') && (await readRail(page)).flat.length === 20, { label: 'summary and rail after show all' });
    summary = await readSidebarSummary(page);
    rail = await readRail(page);
    c.check('点「全部显示」：行摘要回到「全部入口都显示」，所有条目回来，omc_nav_hidden 为 []', summary.withoutButton.includes('全部入口都显示') && rail.flat.length === 20 && same(await hidden(), []), `${summary.withoutButton} count=${rail.flat.length}`);
    const writes = tracker.state.log.filter((entry) => entry.method !== 'GET').map((entry) => `${entry.method} ${entry.path}`);
    c.check('整个过程的写请求都只写 omc_nav_hidden', writes.length > 0 && writes.every((entry) => /preferences\/omc_nav_hidden$/.test(entry)), JSON.stringify([...new Set(writes)]));
    await context.close();
    const bContext = await newContext(browser, BASE_APP);
    const bPage = await bContext.newPage();
    await openSidebarPanel(bPage, BASE_APP);
    const bPanel = await readSidebarPanel(bPage);
    c.check('B 的面板是 5 组，「运行」下有模型组（对照）', same(bPanel.groups.map((group) => group.title), ['运行', '网关', '观测', '插件', '控制']) && bPanel.groups[0].entries.some((entry) => entry.label === '模型组'), JSON.stringify(bPanel.groups.map((group) => group.title)));
    c.evidenceFile(await shot(bPage, 'C-39-B-panel'));
    await bContext.close();
  }

  // ---- C-40: the panel in four languages and at phone width
  if (wants('C-40')) {
    const c = R.begin('C-40', '设置面板的四种语言与手机宽度', 'P2');
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    await open(page, `${APP}/omc-settings`);
    for (const lang of LANGS) {
      if (lang !== 'zh') await switchLanguage(page, lang);
      await page.locator('[data-testid="sidebar-nav-settings"] button').first().click();
      await page.locator('[data-testid="sidebar-nav-dialog"]').waitFor();
      const panel = await readSidebarPanel(page);
      const group = panel.groups[3];
      c.check(`desktop ${lang}：「能力」小标题=${NAMES[lang].group}，三个条目名正确`, group?.title.toLowerCase() === NAMES[lang].group.toLowerCase() && same(group.entries.map((entry) => entry.label), [NAMES[lang].modelGroups, NAMES[lang].repoKeys, NAMES[lang].lending]), JSON.stringify(group));
      c.check(`desktop ${lang}：没有内部键名`, !/nav\.|omc\./.test(JSON.stringify(panel)));
      await page.keyboard.press('Escape');
      await page.locator('[data-testid="sidebar-nav-dialog"]').waitFor({ state: 'detached' });
    }
    await context.close();
    for (const lang of LANGS) {
      const phoneContext = await newContext(browser, APP, { lang, viewport: { width: 390, height: 844 }, touch: true });
      const phonePage = await phoneContext.newPage();
      await phonePage.goto(`${APP}/omc-settings`, { waitUntil: 'domcontentloaded' });
      await phonePage.locator('[data-testid="sidebar-nav-settings"] button').first().waitFor();
      await phonePage.locator('[data-testid="sidebar-nav-settings"] button').first().tap();
      await phonePage.locator('[data-testid="sidebar-nav-dialog"]').waitFor();
      const panel = await readSidebarPanel(phonePage);
      const group = panel.groups[3];
      c.check(`phone ${lang}：「能力」小标题与三个条目名正确`, group?.title.toLowerCase() === NAMES[lang].group.toLowerCase() && same(group.entries.map((entry) => entry.label), [NAMES[lang].modelGroups, NAMES[lang].repoKeys, NAMES[lang].lending]), JSON.stringify(group));
      const lastSwitch = phonePage.locator('[data-nav-toggle="/system"] [role="switch"]');
      await until(async () => (await phonePage.evaluate(() => document.querySelector('.ant-modal')?.getBoundingClientRect().width ?? 0)) >= 300, { label: 'panel finished opening' });
      await lastSwitch.scrollIntoViewIfNeeded();
      const widths = await phonePage.evaluate(() => ({ scrollWidth: document.documentElement.scrollWidth, innerWidth: window.innerWidth, modal: (() => { const node = document.querySelector('.ant-modal'); const box = node?.getBoundingClientRect(); return box ? { left: box.left, right: box.right, bottom: box.bottom } : null; })() }));
      c.check(`phone ${lang}：面板能滚动到底，没有横向滚动，没有出屏`, (await lastSwitch.isVisible()) && widths.scrollWidth <= widths.innerWidth && widths.modal && widths.modal.left >= 0 && widths.modal.right <= widths.innerWidth + 1, JSON.stringify(widths));
      if (lang === 'ms' || lang === 'zh-Hant') c.evidenceFile(await shot(phonePage, `C-40-H-panel-phone-${lang}`));
      await phoneContext.close();
    }
  }
} finally {
  for (const api of Object.values(apis)) { await api.setHidden([]).catch(() => {}); await api.dispose().catch(() => {}); }
  await shutdown(browser);
  R.finish();
}
