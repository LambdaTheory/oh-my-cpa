// C-06, C-07, C-41, C-42, C-43, C-55: names in four places and four languages, clicking through, the collapsed rail,
// the phone drawer, the loading placeholder, and keyboard reach.
//   node scripts/acceptance/capabilities-nav/02-presentation.mjs
import { APP, BASE_APP, LANGS, NAMES, Results, wants, apiClient, disableFixturePlugin, launch, newContext, same, shot, shotTall, shutdown, until } from './lib.mjs';
import { collapseRail, open, openDrawer, openSidebarPanel, readBreadcrumb, readDrawer, readPlaceholder, readRail, readRailMetrics, readSidebarPanel, switchLanguage, waitForShell } from './nav.mjs';

const R = new Results('02-presentation');
const browser = await launch();
const apis = [];
try {
  for (const app of [APP, BASE_APP]) {
    const api = await apiClient(app);
    apis.push(api);
    await api.setHidden([]);
    await disableFixturePlugin(api);
  }

  // ---- C-06: names in the menu, breadcrumb, tab title and settings panel, per language
  if (wants('C-06')) {
    const c = R.begin('C-06', '条目名称在四种语言、四个位置上一致', 'P0');
    const hContext = await newContext(browser, APP);
    const bContext = await newContext(browser, BASE_APP);
    const hPage = await hContext.newPage();
    const bPage = await bContext.newPage();
    await open(hPage, `${APP}/dashboard`);
    await open(bPage, `${BASE_APP}/dashboard`);
    const rows = {};
    for (const lang of LANGS) {
      const expected = NAMES[lang];
      await switchLanguage(hPage, lang);
      await switchLanguage(bPage, lang);
      const rail = await readRail(hPage);
      const menuNames = Object.fromEntries(rail.flat.map((item) => [item.path, item.name]));
      c.check(`${lang}：菜单里「模型组」=${expected.modelGroups}`, menuNames['/model-groups'] === expected.modelGroups, menuNames['/model-groups']);
      c.check(`${lang}：菜单里「仓库自有密钥」=${expected.repoKeys}`, menuNames['/repo-keys'] === expected.repoKeys, menuNames['/repo-keys']);
      const seen = { menu: [menuNames['/model-groups'], menuNames['/repo-keys']] };

      await hPage.locator('.app-menu [data-route-path="/model-groups"]').click();
      await until(async () => new URL(hPage.url()).pathname.endsWith('/model-groups'), { label: 'model-groups opened' });
      await waitForShell(hPage);
      const mgCrumb = await readBreadcrumb(hPage);
      const mgTitle = await hPage.title();
      c.check(`${lang}：模型组页面包屑末节与标签页标题都是「${expected.modelGroups}」`, mgCrumb.current === expected.modelGroups && mgTitle.endsWith(expected.modelGroups), `${mgCrumb.current} | ${mgTitle}`);
      await hPage.locator('.app-menu [data-route-path="/repo-keys"]').click();
      await until(async () => new URL(hPage.url()).pathname.endsWith('/repo-keys'), { label: 'repo-keys opened' });
      const rkCrumb = await readBreadcrumb(hPage);
      const rkTitle = await hPage.title();
      c.check(`${lang}：仓库自有密钥页面包屑末节与标签页标题都是「${expected.repoKeys}」`, rkCrumb.current === expected.repoKeys && rkTitle.endsWith(expected.repoKeys), `${rkCrumb.current} | ${rkTitle}`);
      seen.crumb = [mgCrumb.current, rkCrumb.current];
      seen.title = [mgTitle, rkTitle];

      await openSidebarPanel(hPage, APP);
      const panel = await readSidebarPanel(hPage);
      const labels = Object.fromEntries(panel.groups.flatMap((group) => group.entries.map((entry) => [entry.path, entry.label])));
      c.check(`${lang}：设置面板里两项名称与菜单一致`, labels['/model-groups'] === expected.modelGroups && labels['/repo-keys'] === expected.repoKeys, `${labels['/model-groups']} | ${labels['/repo-keys']}`);
      seen.panel = [labels['/model-groups'], labels['/repo-keys']];
      rows[lang] = seen;

      // Baseline: the same entry's name in the same language, in four places.
      const bRail = await readRail(bPage);
      const bName = bRail.flat.find((item) => item.path === '/model-groups')?.name;
      await bPage.locator('.app-menu [data-route-path="/model-groups"]').click();
      await until(async () => new URL(bPage.url()).pathname.endsWith('/model-groups'), { label: 'baseline model-groups opened' });
      const bCrumb = await readBreadcrumb(bPage);
      const bTitle = await bPage.title();
      await openSidebarPanel(bPage, BASE_APP);
      const bPanel = await readSidebarPanel(bPage);
      const bLabel = Object.fromEntries(bPanel.groups.flatMap((group) => group.entries.map((entry) => [entry.path, entry.label])))['/model-groups'];
      c.check(`${lang}：「模型组」的名称与 B 同语言下完全相同（菜单、面包屑、标签页、面板）`,
        bName === menuNames['/model-groups'] && bCrumb.current === mgCrumb.current && bTitle === mgTitle && bLabel === labels['/model-groups'],
        `B: ${bName} | ${bCrumb.current} | ${bTitle} | ${bLabel}`);
      if (lang === 'ms' || lang === 'en') c.evidenceFile(await shot(hPage, `C-06-H-settings-panel-${lang}`));
      await open(hPage, `${APP}/dashboard`);
      await open(bPage, `${BASE_APP}/dashboard`);
    }
    c.note(`各语言读数：${JSON.stringify(rows)}`);
    await hContext.close();
    await bContext.close();
  }

  // ---- C-07: clicking the two entries opens the right pages
  if (wants('C-07')) {
    const c = R.begin('C-07', '点击条目打开对应页面', 'P0');
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    await open(page, `${APP}/dashboard`);
    await page.locator('.app-menu [data-route-path="/model-groups"]').click();
    await until(async () => new URL(page.url()).pathname === '/omc/model-groups', { label: 'model groups' });
    await page.getByRole('button', { name: '新建组' }).waitFor();
    c.check('「模型组」点击后地址为 /omc/model-groups', new URL(page.url()).pathname === '/omc/model-groups', page.url());
    c.check('列表页有「新建组」与「恢复默认组」按钮', await page.getByRole('button', { name: '新建组' }).count() > 0 && await page.getByRole('button', { name: '恢复默认组' }).count() > 0);
    c.check('页面主标题为「模型组」', (await page.locator('h1').first().innerText()).trim() === '模型组', await page.locator('h1').first().innerText());
    c.evidenceFile(await shot(page, 'C-07-H-model-groups-zh'));
    await open(page, `${APP}/dashboard`);
    await page.locator('.app-menu [data-route-path="/repo-keys"]').click();
    await until(async () => new URL(page.url()).pathname === '/omc/repo-keys', { label: 'repo keys' });
    await page.getByPlaceholder('按仓库、密钥或 agent 搜索').waitFor();
    c.check('「仓库自有密钥」点击后地址为 /omc/repo-keys', new URL(page.url()).pathname === '/omc/repo-keys', page.url());
    c.check('页面主标题为「仓库自有密钥」', (await page.locator('h1').first().innerText()).trim() === '仓库自有密钥', await page.locator('h1').first().innerText());
    const sentence = '每个仓库一把密钥，由 agent 通过 MCP 申请，人工审批一次；控制台只显示掩码，只有明确查看时才会显示明文。';
    c.check('页面上有 F1 说明句', await page.getByText(sentence).count() > 0);
    c.check('页面上有搜索框「按仓库、密钥或 agent 搜索」', await page.getByPlaceholder('按仓库、密钥或 agent 搜索').count() > 0);
    c.evidenceFile(await shot(page, 'C-07-H-repo-keys-zh'));
    await context.close();
  }

  // ---- C-41: the collapsed rail
  if (wants('C-41')) {
    const c = R.begin('C-41', '收起栏的图标顺序、数量与名称', 'P0');
    const hContext = await newContext(browser, APP);
    const bContext = await newContext(browser, BASE_APP);
    const hPage = await hContext.newPage();
    const bPage = await bContext.newPage();
    await open(hPage, `${APP}/dashboard`);
    await open(bPage, `${BASE_APP}/dashboard`);
    const expanded = await readRail(hPage);
    const e = expanded.flat.map((item) => item.path);
    const counts = expanded.groups.map((group) => group.items.length);
    c.check('展开栏分组条数为 4、3、5、3、2、3', same(counts, [4, 3, 5, 3, 2, 3]), JSON.stringify(counts));
    await collapseRail(hPage);
    const collapsed = await readRail(hPage);
    c.check('收起栏的地址顺序等于展开栏拉平后的顺序', same(collapsed.flat.map((item) => item.path), e), JSON.stringify(collapsed.flat.map((item) => item.path)));
    c.check('收起栏共 20 个图标', collapsed.flat.length === 20, `count=${collapsed.flat.length}`);
    c.check('收起栏里没有任何分组标题', collapsed.groupTitles.length === 0 && collapsed.groups.length === 0, JSON.stringify(collapsed.groupTitles));
    c.check('每个图标都带名称（title），等于展开栏里的名称', collapsed.flat.every((item, index) => item.title === expanded.flat[index].name), JSON.stringify(collapsed.flat.filter((item, index) => item.title !== expanded.flat[index].name)));
    const capabilitiesAt = collapsed.flat.findIndex((item) => item.path === '/model-groups');
    c.check('「能力」的三个图标接在观测五个图标之后、插件之前', same(collapsed.flat.slice(capabilitiesAt - 5, capabilitiesAt + 4).map((item) => item.path), ['/insights', '/usage/events', '/pricing', '/logs', '/audit', '/model-groups', '/repo-keys', '/quota-lending', '/plugins']), JSON.stringify(collapsed.flat.slice(capabilitiesAt - 5, capabilitiesAt + 4).map((item) => item.path)));
    // Hover: is a custom tooltip drawn, or does the name rest on the native title? Record what the page does.
    const target = hPage.locator('.app-menu [data-route-path="/repo-keys"]');
    await target.hover();
    const customTooltip = await hPage.locator('.ant-tooltip:not(.ant-tooltip-hidden)').count();
    c.note(`悬停「仓库自有密钥」：自绘提示 ${customTooltip} 个；原生 title="${await target.getAttribute('title')}"`);
    c.evidenceFile(await shotTall(hPage, 'C-41-H-collapsed-zh', 1250));
    await collapseRail(bPage);
    const bCollapsed = await readRail(bPage);
    c.check('B 的收起栏为 18 个（对照）', bCollapsed.flat.length === 18, `count=${bCollapsed.flat.length}`);
    c.evidenceFile(await shotTall(bPage, 'C-41-B-collapsed-zh', 1250));
    await hContext.close();
    await bContext.close();
  }

  // ---- C-42: the phone drawer
  if (wants('C-42')) {
    const c = R.begin('C-42', '手机抽屉与展开栏同一份内容', 'P0');
    const wide = await newContext(browser, APP);
    const widePage = await wide.newPage();
    await open(widePage, `${APP}/dashboard`);
    const expanded = await readRail(widePage);
    await wide.close();

    const context = await newContext(browser, APP, { viewport: { width: 390, height: 844 }, touch: true });
    const page = await context.newPage();
    await page.goto(`${APP}/dashboard`, { waitUntil: 'domcontentloaded' });
    await page.locator('.app-header-left button').first().waitFor();
    await openDrawer(page);
    const drawer = await readDrawer(page);
    c.check('抽屉的分组标题与展开栏一致（6 组）', same(drawer.groups.map((group) => group.title), expanded.groups.map((group) => group.title)), JSON.stringify(drawer.groups.map((group) => group.title)));
    c.check('抽屉的条目、地址与顺序与展开栏一致（20 项）', same(drawer.flat, expanded.flat.map((item) => item.path)) && drawer.flat.length === 20, `count=${drawer.flat.length}`);
    c.check('抽屉里分组内条目名称与展开栏一致', same(drawer.groups.map((group) => group.items.map((item) => item.name)), expanded.groups.map((group) => group.items.map((item) => item.name))));
    c.evidenceFile(await shotTall(page, 'C-42-H-drawer-zh-390', 1250));
    // The drawer's last entry is reachable by scrolling it.
    const last = page.locator('.mobile-nav-drawer .app-menu [data-route-path="/system"]');
    await last.scrollIntoViewIfNeeded();
    c.check('抽屉能滚动到最后一个条目「系统信息」', await last.isVisible());
    c.check('抽屉打开时没有横向滚动', drawer.scrollWidth <= drawer.innerWidth, `scrollWidth=${drawer.scrollWidth} innerWidth=${drawer.innerWidth}`);
    for (const [route, name] of [['/repo-keys', '仓库自有密钥'], ['/quota-lending', '额度拆借']]) {
      await page.locator(`.mobile-nav-drawer .app-menu [data-route-path="${route}"]`).tap();
      await until(async () => new URL(page.url()).pathname === `/omc${route}`, { label: `${route} opened` });
      await page.locator('.mobile-nav-drawer').waitFor({ state: 'hidden' });
      c.check(`点「${name}」后抽屉关闭并进入 ${route}`, new URL(page.url()).pathname === `/omc${route}` && !(await page.locator('.mobile-nav-drawer').isVisible().catch(() => false)), page.url());
      c.evidenceFile(await shot(page, `C-42-H-after-tap-${route.slice(1)}-390`));
      await openDrawer(page);
    }
    await context.close();
  }

  // ---- C-43: the loading placeholder, kept on screen by holding the preferences read
  if (wants('C-43')) {
    const c = R.begin('C-43', '加载占位的分组数、条数与高度', 'P0');
    const measure = async (app, label, expectedGroups) => {
      const context = await newContext(browser, app);
      const held = [];
      await context.route(/\/api\/v1\/preferences(\?|$)/, (route) => {
        if (route.request().method() === 'GET') held.push(route); else route.continue();
      });
      const page = await context.newPage();
      await page.goto(`${app}/dashboard`, { waitUntil: 'commit' });
      await until(async () => (await readPlaceholder(page).catch(() => null)) !== null && held.length > 0, { label: `${label} placeholder and held read` });
      const placeholder = await readPlaceholder(page);
      const placeholderShot = await shot(page, `C-43-${label}-placeholder`);
      for (const route of held) await route.continue();
      await until(async () => (await page.locator('.app-menu li[data-route-path]').count()) > 0 && (await readPlaceholder(page)) === null, { label: `${label} real rail` });
      const real = await readRail(page);
      const metrics = await readRailMetrics(page);
      const realShot = await shot(page, `C-43-${label}-loaded`);
      await context.close();
      return { placeholder, real, metrics, placeholderShot, realShot, expectedGroups };
    };
    const h = await measure(APP, 'H', [4, 3, 5, 3, 2, 3]);
    c.evidenceFile(h.placeholderShot); c.evidenceFile(h.realShot);
    const hCounts = h.real.groups.map((group) => group.items.length);
    c.check('H 的占位有 6 组', h.placeholder.groups.length === 6, JSON.stringify(h.placeholder.groups));
    c.check('H 占位各组条数为 4、3、5、3、2、3', same(h.placeholder.groups, [4, 3, 5, 3, 2, 3]), JSON.stringify(h.placeholder.groups));
    c.check('H 占位与加载后的真实栏逐组条数相同', same(h.placeholder.groups, hCounts), `${JSON.stringify(h.placeholder.groups)} vs ${JSON.stringify(hCounts)}`);
    const delta = Math.abs(h.placeholder.navScrollHeight - h.metrics.viewportScrollHeight);
    c.check('H 占位与真实栏导航区 scrollHeight 之差不超过一个条目高度', delta <= h.metrics.entryHeight, `placeholder=${h.placeholder.navScrollHeight} real(viewport scrollHeight)=${h.metrics.viewportScrollHeight} delta=${delta} entryHeight=${h.metrics.entryHeight} menuHeight=${h.metrics.menuHeight}`);
    c.note(`H 高度读数：${JSON.stringify({ placeholder: h.placeholder, metrics: h.metrics })}`);
    const b = await measure(BASE_APP, 'B', [4, 3, 4, 4]);
    c.evidenceFile(b.placeholderShot); c.evidenceFile(b.realShot);
    c.check('B 的占位是 4 组（4、3、4、4），与它 5 组的真实栏不吻合（对照）', same(b.placeholder.groups, [4, 3, 4, 4]) && b.real.groups.length === 5, `placeholder=${JSON.stringify(b.placeholder.groups)} real=${JSON.stringify(b.real.groups.map((g) => g.items.length))}`);
    c.note(`B 高度读数：${JSON.stringify({ placeholder: b.placeholder, metrics: b.metrics })}`);
  }

  // ---- C-55: keyboard reach and accessible structure
  if (wants('C-55')) {
    const c = R.begin('C-55', '键盘操作与无障碍名称', 'P2');
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    await open(page, `${APP}/dashboard`);
    const menu = page.locator('.app-menu').first();
    await menu.focus();
    const reached = [];
    // The rail is one composite widget: arrow keys move through its entries in order.
    for (let step = 0; step < 24; step += 1) {
      await page.keyboard.press('ArrowDown');
      const active = await page.evaluate(() => {
        const node = document.querySelector('.app-menu .ant-menu-item-active') ?? document.activeElement;
        return node?.getAttribute?.('data-route-path') ?? null;
      });
      reached.push(active);
    }
    const flat = (await readRail(page)).flat.map((item) => item.path);
    c.check('键盘方向键依次到达「观测」与「能力」两组的每个条目', ['/insights', '/audit', '/model-groups', '/repo-keys', '/quota-lending'].every((path) => reached.includes(path)), JSON.stringify(reached));
    c.check('键盘到达的先后顺序与展开栏一致', same(reached.filter((path) => path && flat.includes(path)).filter((path, index, list) => list.indexOf(path) === index), flat.filter((path) => reached.includes(path))), JSON.stringify(reached));
    // Enter on the active capability entry opens it.
    await open(page, `${APP}/dashboard`);
    await page.locator('.app-menu').first().focus();
    for (let step = 0; step < 40; step += 1) {
      await page.keyboard.press('ArrowDown');
      const active = await page.evaluate(() => document.querySelector('.app-menu .ant-menu-item-active')?.getAttribute('data-route-path') ?? null);
      if (active === '/repo-keys') break;
    }
    await page.keyboard.press('Enter');
    await until(async () => new URL(page.url()).pathname === '/omc/repo-keys', { label: 'keyboard opened repo-keys' });
    c.check('在「仓库自有密钥」上按 Enter 打开该页', new URL(page.url()).pathname === '/omc/repo-keys', page.url());
    const snapshots = {};
    for (const title of ['观测', '能力']) {
      snapshots[title] = await page.locator('.app-menu .ant-menu-item-group', { has: page.locator('.ant-menu-item-group-title', { hasText: title }) }).first().ariaSnapshot();
    }
    const strip = (text) => text.replace(/[^\n]*(观测|能力|洞察|请求记录|费用与用量|日志查看|操作审计|模型组|仓库自有密钥|额度拆借)[^\n]*/g, 'ITEM');
    c.check('无障碍树里「观测」与「能力」两组的结构形状相同（分组名节点加条目节点）', strip(snapshots['观测']).split('\n').slice(0, 2).join('|') === strip(snapshots['能力']).split('\n').slice(0, 2).join('|'), JSON.stringify(snapshots));
    c.note(`无障碍树片段：${JSON.stringify(snapshots)}`);
    c.evidenceFile(await shot(page, 'C-55-H-keyboard-opened-repo-keys'));
    await context.close();
  }
} finally {
  for (const api of apis) await api.dispose().catch(() => {});
  await shutdown(browser);
  R.finish();
}
