// C-24, C-26, C-27, C-28, C-29, C-30, C-31: addresses, highlight, breadcrumb and tab title.
//   T9_BIN (H's binary) is needed to mint receiver public keys for the approval-link pages.
//   node scripts/acceptance/capabilities-nav/03-routes.mjs
import { spawnSync } from 'node:child_process';
import { APP, BASE_APP, LANGS, NAMES, Results, wants, apiClient, disableFixturePlugin, launch, newContext, same, shot, shutdown, until } from './lib.mjs';
import { open, readBreadcrumb, readLocation, readRail, switchLanguage } from './nav.mjs';
import { fileApplication, firstModelGroupId, genPublicKey, workDir } from './seed.mjs';

const R = new Results('03-routes');
const BIN = process.env.T9_BIN || 'E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude/tmp/omc-nav.exe';
const browser = await launch();
const apis = {};
try {
  for (const [key, app] of [['H', APP], ['B', BASE_APP]]) {
    apis[key] = await apiClient(app);
    await apis[key].setHidden([]);
    await disableFixturePlugin(apis[key]);
  }
  const groupId = { H: await firstModelGroupId(apis.H), B: await firstModelGroupId(apis.B) };
  const token = {};
  for (const side of ['H', 'B']) {
    token[side] = [];
    for (const index of [1, 2]) {
      const filed = await fileApplication(apis[side], { repo: `example/route-check-${side.toLowerCase()}${index}`, publicKey: genPublicKey(BIN, workDir('omchome-routes'), `example/route-check-${side.toLowerCase()}${index}`) });
      token[side].push(filed.approval_token);
    }
  }

  const pages = (side) => {
    const app = side === 'H' ? APP : BASE_APP;
    const id = groupId[side];
    return [
      { key: 'model-groups', path: '/model-groups', feature: (page) => page.getByRole('button', { name: '新建组' }).waitFor() },
      { key: 'model-groups-detail', path: `/model-groups/${id}`, feature: (page) => page.getByRole('button', { name: '加入模型' }).first().waitFor() },
      { key: 'model-groups-add', path: `/model-groups/${id}/add`, feature: (page) => page.getByText(/加入模型到「/).first().waitFor() },
      { key: 'api-keys', path: '/api-keys', feature: (page) => page.getByRole('button', { name: /添加 API 密钥/ }).first().waitFor() },
      { key: 'oauth-management', path: '/oauth-management', feature: (page) => page.locator('h1').filter({ hasText: 'OAuth' }).first().waitFor() },
      { key: 'repo-key-apply', path: `/repo-key-apply/${token[side][0]}`, outside: true, feature: (page) => page.getByText('审批仓库密钥申请').first().waitFor() },
      ...(side === 'H' ? [{ key: 'repo-keys', path: '/repo-keys', feature: (page) => page.getByPlaceholder('按仓库、密钥或 agent 搜索').waitFor() }] : []),
    ].map((entry) => ({ ...entry, url: `${app}${entry.path}` }));
  };

  // ---- C-26
  if (wants('C-26')) {
    const c = R.begin('C-26', '既有地址的直接打开、刷新、前进后退', 'P0');
    for (const side of ['H', 'B']) {
      const app = side === 'H' ? APP : BASE_APP;
      const context = await newContext(browser, app);
      const page = await context.newPage();
      for (const entry of pages(side)) {
        const expectedPath = `/omc${entry.path}`;
        const where = `${side} ${entry.key}`;
        const direct = await page.goto(entry.url, { waitUntil: 'domcontentloaded' });
        await entry.feature(page);
        c.check(`${where}：直接输入，地址不变、文档 200、页面特征在`, new URL(page.url()).pathname === expectedPath && direct.status() === 200, `${new URL(page.url()).pathname} ${direct.status()}`);
        c.check(`${where}：直接输入时不落到仪表盘`, !new URL(page.url()).pathname.endsWith('/dashboard'));
        const reloaded = await page.reload({ waitUntil: 'domcontentloaded' });
        await entry.feature(page);
        c.check(`${where}：刷新后仍在原页，文档 200`, new URL(page.url()).pathname === expectedPath && reloaded.status() === 200, `${new URL(page.url()).pathname} ${reloaded.status()}`);
        await page.goto(`${app}/dashboard`, { waitUntil: 'domcontentloaded' });
        await page.goto(entry.url, { waitUntil: 'domcontentloaded' });
        await entry.feature(page);
        await page.goBack({ waitUntil: 'domcontentloaded' });
        await until(async () => new URL(page.url()).pathname === '/omc/dashboard', { label: `${where} back to dashboard` });
        c.check(`${where}：后退一次回到仪表盘`, new URL(page.url()).pathname === '/omc/dashboard', page.url());
        await page.goForward({ waitUntil: 'domcontentloaded' });
        await entry.feature(page);
        c.check(`${where}：前进一次回到该地址并显示该页`, new URL(page.url()).pathname === expectedPath, page.url());
        if (entry.key === 'repo-key-apply') c.check(`${where}：审批链接页在侧栏之外`, (await readLocation(page)).isOutsideShell);
        if (side === 'H' && ['model-groups', 'api-keys', 'repo-keys', 'repo-key-apply'].includes(entry.key)) c.evidenceFile(await shot(page, `C-26-H-${entry.key}`));
      }
      await context.close();
    }
  }

  // ---- C-27 (raw GET, no session)
  if (wants('C-27')) {
    const c = R.begin('C-27', '服务端对各地址的直接请求', 'P1');
    const results = { H: {}, B: {} };
    for (const side of ['H', 'B']) {
      for (const entry of pages(side)) {
        const run = spawnSync('curl.exe', ['-s', '-o', 'NUL', '-w', '%{http_code} %{content_type}', entry.url], { encoding: 'utf8', windowsHide: true });
        results[side][entry.key] = run.stdout.trim();
      }
    }
    for (const key of Object.keys(results.H)) {
      c.check(`${key}：H 返回 200 与 HTML，没有 301/302`, /^200 text\/html/.test(results.H[key]), results.H[key]);
      if (results.B[key] !== undefined) c.check(`${key}：H 与 B 的状态码与内容类型相同`, results.H[key] === results.B[key], `${results.H[key]} vs ${results.B[key]}`);
    }
    c.note(`curl 读数：${JSON.stringify(results)}`);
  }

  // ---- C-28: eight pages
  if (wants('C-28')) {
    const c = R.begin('C-28', '八个页面的高亮、面包屑与标签页标题', 'P0');
    const expectations = [
      { key: 'model-groups', group: '能力', current: '模型组', selected: ['/model-groups'] },
      { key: 'model-groups-detail', group: '能力', current: '模型组', selected: ['/model-groups'] },
      { key: 'model-groups-add', group: '能力', current: '模型组', selected: ['/model-groups'] },
      { key: 'repo-keys', group: '能力', current: '仓库自有密钥', selected: ['/repo-keys'] },
      { key: 'api-keys', group: '网关', current: '密钥管理', selected: ['/api-keys'] },
      { key: 'oauth-management', group: '网关', current: 'OAuth 管理', selected: ['/oauth-management'] },
      { key: 'quota-lending', group: '能力', current: '额度拆借', selected: ['/quota-lending'], path: '/quota-lending' },
      { key: 'repo-key-apply', outside: true },
    ];
    const read = async (side) => {
      const app = side === 'H' ? APP : BASE_APP;
      const context = await newContext(browser, app);
      const page = await context.newPage();
      const out = {};
      const all = [...pages(side).filter((entry) => entry.key !== 'repo-key-apply'), { key: 'quota-lending', path: '/quota-lending', url: `${app}/quota-lending`, feature: (p) => p.locator('[data-lending-pool], h1').first().waitFor() }, pages(side).find((entry) => entry.key === 'repo-key-apply')];
      for (const entry of all) {
        await page.goto(entry.url, { waitUntil: 'domcontentloaded' });
        await entry.feature(page);
        if (!entry.outside) await until(async () => (await readBreadcrumb(page)) !== null, { label: `${side} ${entry.key} breadcrumb` });
        out[entry.key] = await readLocation(page);
        if (side === 'H') c.evidenceFile(await shot(page, `C-28-H-${entry.key}`));
      }
      await context.close();
      return out;
    };
    const h = await read('H');
    const b = await read('B');
    for (const expected of expectations) {
      const got = h[expected.key];
      if (expected.outside) {
        c.check('H 审批链接页：在侧栏之外，没有高亮、没有面包屑', got.isOutsideShell && got.selected.length === 0 && got.crumb === null, JSON.stringify(got));
        continue;
      }
      c.check(`H ${expected.key}：高亮落在 ${expected.selected[0]}`, same(got.selected, expected.selected), JSON.stringify(got.selected));
      c.check(`H ${expected.key}：面包屑为「${expected.group} › ${expected.current}」`, got.crumb?.group === expected.group && got.crumb?.current === expected.current, JSON.stringify(got.crumb));
      c.check(`H ${expected.key}：任一时刻最多一个条目被选中`, got.selected.length <= 1);
      if (['repo-keys', 'quota-lending'].includes(expected.key)) c.check(`H ${expected.key}：标签页标题里的页名是条目名`, got.title.endsWith(expected.current), got.title);
      else c.check(`H ${expected.key}：标签页标题与 B 的同一页相同`, got.title === b[expected.key].title, `${got.title} vs ${b[expected.key].title}`);
    }
    c.check('H 打开额度拆借时「OAuth 管理」不高亮', !h['quota-lending'].selected.includes('/oauth-management'), JSON.stringify(h['quota-lending'].selected));
    c.check('H 额度拆借页内标题仍是「额度拆借建议」', h['quota-lending'].heading === '额度拆借建议', h['quota-lending'].heading);
    c.check('B 打开额度拆借时借用「OAuth 管理」的高亮（对照）', same(b['quota-lending'].selected, ['/oauth-management']), JSON.stringify(b['quota-lending']));
    c.note(`H 读数：${JSON.stringify(h)}`);
    c.note(`B 读数：${JSON.stringify(b)}`);
  }

  // ---- C-29: hidden entries opened by address
  if (wants('C-29')) {
    const c = R.begin('C-29', '被隐藏的条目用地址打开，面包屑仍写出分组', 'P1');
    await apis.H.setHidden(['/model-groups', '/repo-keys', '/quota-lending', '/api-keys']);
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    const expected = [['/model-groups', '能力', '模型组'], ['/repo-keys', '能力', '仓库自有密钥'], ['/quota-lending', '能力', '额度拆借'], ['/api-keys', '网关', '密钥管理']];
    for (const [route, group, current] of expected) {
      await open(page, `${APP}${route}`);
      const location = await readLocation(page);
      const rail = await readRail(page);
      c.check(`${route}：面包屑为「${group} › ${current}」`, location.crumb?.group === group && location.crumb?.current === current, JSON.stringify(location.crumb));
      c.check(`${route}：展开栏里没有「能力」标题，也没有对应条目`, !rail.groupTitles.includes('能力') && !rail.flat.some((item) => item.path === route), JSON.stringify(rail.groupTitles));
      c.check(`${route}：没有被选中的条目`, rail.selected.length === 0, JSON.stringify(rail.selected));
      c.check(`${route}：标签页标题仍是条目名`, location.title.endsWith(current), location.title);
      c.evidenceFile(await shot(page, `C-29-H-hidden-${route.slice(1).replace(/\//g, '-')}`));
    }
    await context.close();
    await apis.H.setHidden([]);
  }

  // ---- C-30: query strings do not change highlight or breadcrumb
  if (wants('C-30')) {
    const c = R.begin('C-30', '查询参数不影响高亮与面包屑', 'P1');
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    const expected = [['/oauth-management?page_size=48', '/oauth-management', '网关', 'OAuth 管理'], ['/model-groups?x=1', '/model-groups', '能力', '模型组'], ['/repo-keys?x=1', '/repo-keys', '能力', '仓库自有密钥'], ['/quota-lending?x=1', '/quota-lending', '能力', '额度拆借']];
    for (const [route, selected, group, current] of expected) {
      await open(page, `${APP}${route}`);
      const location = await readLocation(page);
      c.check(`${route}：高亮 ${selected}，面包屑「${group} › ${current}」`, same(location.selected, [selected]) && location.crumb?.group === group && location.crumb?.current === current, JSON.stringify({ selected: location.selected, crumb: location.crumb }));
    }
    c.evidenceFile(await shot(page, 'C-30-H-quota-lending-query'));
    await context.close();
  }

  // ---- C-31: breadcrumb in four languages, switched live
  if (wants('C-31')) {
    const c = R.begin('C-31', '四种语言下的面包屑', 'P2');
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    for (const [route, key] of [['/model-groups', 'modelGroups'], ['/repo-keys', 'repoKeys'], ['/quota-lending', 'lending']]) {
      await open(page, `${APP}${route}`);
      for (const lang of LANGS) {
        await switchLanguage(page, lang);
        const crumb = await readBreadcrumb(page);
        c.check(`${route} ${lang}：面包屑为「${NAMES[lang].group} › ${NAMES[lang][key]}」`, crumb.group === NAMES[lang].group && crumb.current === NAMES[lang][key], JSON.stringify(crumb));
        c.check(`${route} ${lang}：没有内部键名`, !/\bnav\./.test(crumb.text));
      }
      c.evidenceFile(await shot(page, `C-31-H-breadcrumb-${route.slice(1)}-ms`));
      await switchLanguage(page, 'zh');
    }
    await context.close();
  }
} finally {
  for (const api of Object.values(apis)) await api.dispose().catch(() => {});
  await shutdown(browser);
  R.finish();
}
