// C-49 and C-50: the data requests each page makes, before (B) and after (H), and the write requests of the whole console.
// Both sides run on the same data (the first pair). Every request is recorded with its method, path, query and body field names.
//   node scripts/acceptance/capabilities-nav/09-network.mjs
import { APP, BASE_APP, Results, apiClient, disableFixturePlugin, launch, newContext, normalizeRequest, same, shot, shutdown, trackRequests, until, wants, writeJson } from './lib.mjs';
import { collapseRail, expandRail, open, openSidebarPanel, switchLanguage, toggleSidebarEntry } from './nav.mjs';
import { fileApplication, firstModelGroupId, genPublicKey, workDir } from './seed.mjs';

const R = new Results('09-network');
const BIN = process.env.T9_BIN || 'E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude/tmp/omc-nav.exe';
const BASE_BIN = process.env.T9_BASE_BIN || 'C:/Users/lhw/AppData/Local/Temp/omc-t9-s8-base/tmp/omc-nav.exe';
const browser = await launch();
const sides = [['B', BASE_APP, BASE_BIN], ['H', APP, BIN]];
const apis = {};

const isApi = (entry) => entry.path.includes('/api/');
const describe = (entry) => `${normalizeRequest(entry)}${entry.method !== 'GET' ? ` ${JSON.stringify(entry.bodyKeys)}` : ''}`;
const family = (entry) => {
  if (/\/management\/repo-keys(\/|$)/.test(entry.path)) return 'repo-keys';
  if (/\/management\/(api-keys|client-key-usage|config)(\/|$)/.test(entry.path)) return 'gateway-keys';
  return 'other';
};

/** Opens `url` in a fresh context and returns the API requests it made until the network was quiet. */
async function requestsFor(app, url, { lang = 'zh' } = {}) {
  const context = await newContext(browser, app, { lang });
  const tracker = trackRequests(context);
  const page = await context.newPage();
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  await page.locator('.app-menu li[data-route-path]').first().waitFor({ state: 'attached' });
  await tracker.quiet({ quietMs: 1000, timeoutMs: 45_000 });
  const entries = tracker.state.log.slice();
  await context.close();
  return entries;
}

try {
  for (const [side, app] of sides) { apis[side] = await apiClient(app); await apis[side].setHidden([]); await disableFixturePlugin(apis[side]); }

  // ---- C-49
  if (wants('C-49')) {
    const c = R.begin('C-49', '打开新页与密钥管理页发出的数据请求，对照拆页前', 'P0');
    const shell = {};
    const read = {};
    for (const [side, app] of sides) {
      const settings = await requestsFor(app, `${app}/omc-settings`);
      shell[side] = new Set(settings.filter(isApi).map(describe));
    }
    read.B1 = await requestsFor(BASE_APP, `${BASE_APP}/api-keys`);
    read.H1 = await requestsFor(APP, `${APP}/api-keys`);
    read.H2 = await requestsFor(APP, `${APP}/repo-keys`);
    const own = (key, side) => read[key].filter(isApi).filter((entry) => !shell[side].has(describe(entry)));
    const byFamily = (entries) => ({
      'repo-keys': [...new Set(entries.filter((entry) => family(entry) === 'repo-keys').map(describe))].sort(),
      'gateway-keys': [...new Set(entries.filter((entry) => family(entry) === 'gateway-keys').map(describe))].sort(),
      other: [...new Set(entries.filter((entry) => family(entry) === 'other').map(describe))].sort(),
    });
    const b1 = byFamily(own('B1', 'B'));
    const h1 = byFamily(own('H1', 'H'));
    const h2 = byFamily(own('H2', 'H'));
    for (const [name, app, route] of [['B-api-keys-with-panel', BASE_APP, '/api-keys'], ['H-api-keys', APP, '/api-keys'], ['H-repo-keys', APP, '/repo-keys']]) {
      const context = await newContext(browser, app);
      const page = await context.newPage();
      await page.goto(`${app}${route}`, { waitUntil: 'domcontentloaded' });
      await page.locator('.app-menu li[data-route-path]').first().waitFor({ state: 'attached' });
      await trackRequests(context).quiet({ quietMs: 1000, timeoutMs: 45_000 });
      c.evidenceFile(await shot(page, `C-49-${name}-zh`));
      await context.close();
    }
    writeJson('c49-requests.json', { shell: { B: [...shell.B], H: [...shell.H] }, B1: b1, H1: h1, H2: h2, raw: { B1: read.B1.map(describe), H1: read.H1.map(describe), H2: read.H2.map(describe) } });
    c.check('新页 H2：仓库密钥族请求与拆页前 B1 逐条相同（方法、路径、查询参数、条数）', same(h2['repo-keys'], b1['repo-keys']), `${JSON.stringify(h2['repo-keys'])} vs ${JSON.stringify(b1['repo-keys'])}`);
    c.check('新页 H2：仓库密钥族恰为四条（列表、待审批、待交付、审计推送状态）', h2['repo-keys'].length === 4, JSON.stringify(h2['repo-keys']));
    c.check('新页 H2：没有网关密钥族请求', h2['gateway-keys'].length === 0, JSON.stringify(h2['gateway-keys']));
    c.check('新页 H2：其余请求都在壳层集合 S 里（没有 S 之外的请求）', h2.other.length === 0, JSON.stringify(h2.other));
    c.check('密钥管理页 H1：仍有网关密钥族请求', h1['gateway-keys'].length > 0 && same(h1['gateway-keys'], b1['gateway-keys']), `${JSON.stringify(h1['gateway-keys'])} vs ${JSON.stringify(b1['gateway-keys'])}`);
    c.check('密钥管理页 H1：仓库密钥族只剩列表一条（供所属仓库小标）', same(h1['repo-keys'], ['GET /v1/management/repo-keys']), JSON.stringify(h1['repo-keys']));
    c.check('密钥管理页 H1 是 B1 的子集（不多于改动前）', [...Object.values(h1).flat()].every((entry) => [...Object.values(b1).flat()].includes(entry)), JSON.stringify(h1));
    const removed = b1['repo-keys'].filter((entry) => !h1['repo-keys'].includes(entry));
    c.check('B1 比 H1 多出的正是待审批、待交付、审计推送状态三类读取', removed.length === 3 && removed.some((entry) => /status=pending/.test(entry)) && removed.some((entry) => /awaiting=true/.test(entry)) && removed.some((entry) => /audit-push/.test(entry)), JSON.stringify(removed));
    c.check('没有新增：H1、H2 里没有 B1 与 B 的 S 里都不存在的请求', [...Object.values(h1).flat(), ...Object.values(h2).flat()].every((entry) => [...Object.values(b1).flat(), ...shell.B].includes(entry)), '');
    const hosts = new Set([...read.B1, ...read.H1, ...read.H2].map((entry) => entry.host));
    c.check('所有请求的主机都只有应用自己，没有外部主机', [...hosts].every((host) => /^127\.0\.0\.1:(18481|18484)$/.test(host)), JSON.stringify([...hosts]));
    c.check('三个页面都没有写请求', [...read.B1, ...read.H1, ...read.H2].every((entry) => entry.method === 'GET'), JSON.stringify([...read.B1, ...read.H1, ...read.H2].filter((entry) => entry.method !== 'GET').map(describe)));
    c.note(`B1：${JSON.stringify(b1)}`);
    c.note(`H1：${JSON.stringify(h1)}`);
    c.note(`H2：${JSON.stringify(h2)}`);
  }

  // ---- C-50
  if (wants('C-50')) {
    const c = R.begin('C-50', '整个控制台没有新增写操作', 'P0');
    const tokens = {};
    for (const [side, , bin] of sides) {
      const repo = `example/netcheck-${side.toLowerCase()}`;
      tokens[side] = (await fileApplication(apis[side], { repo, publicKey: genPublicKey(bin, workDir('omchome-net'), repo) })).approval_token;
    }
    const groupId = { B: await firstModelGroupId(apis.B), H: await firstModelGroupId(apis.H) };
    const fixed = ['/dashboard', '/agent', '/playground', '/model-square', '/ai-providers', '/oauth-management', '/api-keys', '/insights', '/usage/events', '/pricing', '/logs', '/audit', '/model-groups', '/repo-keys', '/quota-lending', '/plugins', '/plugins/store', '/config', '/omc-settings', '/system'];
    const writes = { B: {}, H: {} };
    for (const [side, app] of sides) {
      const routes = [...fixed, `/model-groups/${groupId[side]}`, `/model-groups/${groupId[side]}/add`, `/repo-key-apply/${tokens[side]}`];
      for (const route of routes) {
        if (side === 'B' && ['/repo-keys'].includes(route)) continue;
        const context = await newContext(browser, app);
        const tracker = trackRequests(context);
        const page = await context.newPage();
        await page.goto(`${app}${route}`, { waitUntil: 'domcontentloaded' });
        await page.locator('h1, .app-menu li[data-route-path]').first().waitFor({ state: 'attached', timeout: 45_000 });
        await tracker.quiet({ quietMs: 1000, timeoutMs: 60_000 });
        writes[side][route.replace(/\/[0-9a-f-]{36}|\/[A-Za-z0-9_-]{40,}/g, '/<id>')] = tracker.state.log.filter((entry) => isApi(entry) && entry.method !== 'GET').map(describe);
        await context.close();
      }
    }
    writeJson('c50-open-only-writes.json', writes);
    const hWrites = Object.entries(writes.H).filter(([, list]) => list.length > 0);
    const bWrites = Object.entries(writes.B).filter(([, list]) => list.length > 0);
    c.check(`仅打开页面（H ${Object.keys(writes.H).length} 个、B ${Object.keys(writes.B).length} 个）：H 的非 GET 请求集合是 B 的子集`, hWrites.every(([route, list]) => list.every((entry) => (writes.B[route] ?? []).includes(entry))), JSON.stringify({ hWrites, bWrites }));
    // A write that B also makes on the same page is existing behavior, not a new write; only a write H makes and B does not would be new.
    const newWrites = hWrites.flatMap(([route, list]) => list.filter((entry) => !(writes.B[route] ?? []).includes(entry)).map((entry) => `${route} ${entry}`));
    c.check('仅打开页面时 H 与 B 逐页的非 GET 请求集合逐条相同（B 没有而 H 有才算新增写操作）', same(hWrites, bWrites) && newWrites.length === 0, JSON.stringify({ hWrites, bWrites, newWrites }));
    c.note(`既有、非新增：${JSON.stringify(bWrites)}（操练场页自己持久化会话偏好，PlaygroundPage 的 persistSession，本需求没有动 Playground 的任何文件）`);

    // Sidebar-related actions: language, collapse, expand, visibility toggles.
    const actions = { B: [], H: [] };
    for (const [side, app] of sides) {
      const context = await newContext(browser, app);
      const tracker = trackRequests(context);
      const page = await context.newPage();
      await open(page, `${app}/dashboard`);
      await tracker.quiet();
      const step = async (name, action) => {
        const mark = tracker.mark();
        await action();
        await tracker.quiet({ quietMs: 800 });
        actions[side].push({ name, writes: tracker.since(mark).filter((entry) => isApi(entry) && entry.method !== 'GET').map(describe) });
      };
      await step('切换语言到 English', () => switchLanguage(page, 'en'));
      await step('切回中文', () => switchLanguage(page, 'zh'));
      await step('收起侧栏', () => collapseRail(page));
      await step('展开侧栏', () => expandRail(page));
      await step('设置面板：关掉一个条目', async () => { await openSidebarPanel(page, app); await toggleSidebarEntry(page, '/insights'); await until(async () => (await apis[side].getHidden())?.includes('/insights'), { label: `${side} hidden stored` }); });
      await step('设置面板：全部显示', async () => { await page.getByRole('button', { name: /全部显示/ }).click(); await until(async () => ((await apis[side].getHidden()) ?? []).length === 0, { label: `${side} show all stored` }); });
      c.evidenceFile(await shot(page, `C-50-${side}-settings-panel-after-actions-zh`));
      await context.close();
    }
    writeJson('c50-sidebar-writes.json', actions);
    c.check('侧栏相关操作产生的写请求，H 与 B 逐步相同', same(actions.H, actions.B), `${JSON.stringify(actions.H)} vs ${JSON.stringify(actions.B)}`);
    c.check('显隐开关只写 omc_nav_hidden', actions.H.filter((entry) => /设置面板/.test(entry.name)).every((entry) => entry.writes.length > 0 && entry.writes.every((write) => /preferences\/omc_nav_hidden/.test(write))), JSON.stringify(actions.H.filter((entry) => /设置面板/.test(entry.name))));
    c.note(`侧栏操作的写请求：${JSON.stringify(actions.H)}`);
    c.note('面板各写操作（批准、驳回、取密文、停用、申请重新启用、查看明文、代提交、生成链接）在两端发出的请求：见 07、08 脚本的并排读数文件 parity-*.json 与 C-17～C-20、C-23 的结论，两端「方法、路径、体字段」逐条相同。');
  }
} finally {
  for (const api of Object.values(apis)) await api.dispose().catch(() => {});
  await shutdown(browser);
  R.finish();
}
