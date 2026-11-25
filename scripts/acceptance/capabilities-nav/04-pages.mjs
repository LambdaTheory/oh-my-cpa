// C-24, C-32, C-33, C-57, C-58, C-59, C-60: the new page's titles and addresses, the approval link's way back,
// and the quota-lending mount.
//   T9_BIN (H's binary) and T9_BASE_BIN (B's binary) mint receiver public keys for the approval-link pages.
//   node scripts/acceptance/capabilities-nav/04-pages.mjs
import { APP, BASE_APP, LANGS, NAMES, Results, wants, apiClient, disableFixturePlugin, launch, newContext, normalizeRequest, same, shot, shutdown, trackRequests, until } from './lib.mjs';
import { open, openSidebarPanel, readBreadcrumb, readLocation, readRail, readSidebarPanel, switchLanguage, waitForShell } from './nav.mjs';
import { fileApplication, genPublicKey, workDir } from './seed.mjs';

const R = new Results('04-pages');
const BIN = process.env.T9_BIN || 'E:/works/Newoahil__oh-my-cpa/11-24-t9-s8-claude/tmp/omc-nav.exe';
const BASE_BIN = process.env.T9_BASE_BIN || 'C:/Users/lhw/AppData/Local/Temp/omc-t9-s8-base/tmp/omc-nav.exe';
const browser = await launch();
const apis = {};
const pathOf = (page) => new URL(page.url()).pathname;
try {
  for (const [key, app] of [['H', APP], ['B', BASE_APP]]) {
    apis[key] = await apiClient(app);
    await apis[key].setHidden([]);
    await disableFixturePlugin(apis[key]);
  }

  // ---- C-24: the new page's title, tab title, direct open, refresh, history, per language
  if (wants('C-24')) {
    const c = R.begin('C-24', '新页的标题、浏览器标签页标题与直接打开', 'P0');
    for (const lang of LANGS) {
      const expected = NAMES[lang];
      const context = await newContext(browser, APP, { lang });
      const page = await context.newPage();
      await open(page, `${APP}/dashboard`);
      await page.locator('.app-menu [data-route-path="/repo-keys"]').click();
      await until(async () => pathOf(page) === '/omc/repo-keys', { label: `${lang} opened by click` });
      await page.locator('h1').first().waitFor();
      let location = await readLocation(page);
      c.check(`${lang}：点「能力 › ${expected.repoKeys}」后页内主标题=${expected.repoKeys}`, location.heading === expected.repoKeys, location.heading);
      c.check(`${lang}：标签页标题里的页名是同一个名字`, location.title.endsWith(expected.repoKeys), location.title);
      c.check(`${lang}：地址为 /omc/repo-keys`, location.path === '/omc/repo-keys', location.path);
      if (lang !== 'zh') c.evidenceFile(await shot(page, `C-24-H-repo-keys-${lang}`));
      else c.evidenceFile(await shot(page, 'C-24-H-repo-keys-zh'));
      await page.goto(`${APP}/repo-keys`, { waitUntil: 'domcontentloaded' });
      await page.locator('h1').first().waitFor();
      location = await readLocation(page);
      c.check(`${lang}：直接输入后仍在 /repo-keys，标题=${expected.repoKeys}`, location.path === '/omc/repo-keys' && location.heading === expected.repoKeys, `${location.path} ${location.heading}`);
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.locator('h1').first().waitFor();
      location = await readLocation(page);
      c.check(`${lang}：刷新后仍在 /repo-keys，没有落到仪表盘`, location.path === '/omc/repo-keys' && location.heading === expected.repoKeys, `${location.path} ${location.heading}`);
      const trail = [];
      const record = async (label) => { await page.locator('h1').first().waitFor(); trail.push({ label, path: pathOf(page), heading: (await readLocation(page)).heading }); };
      await page.goto(`${APP}/dashboard`, { waitUntil: 'domcontentloaded' }); await record('dashboard');
      await page.goto(`${APP}/repo-keys`, { waitUntil: 'domcontentloaded' }); await record('repo-keys');
      await page.goto(`${APP}/audit`, { waitUntil: 'domcontentloaded' }); await record('audit');
      await page.goBack({ waitUntil: 'domcontentloaded' }); await record('back-1');
      await page.goBack({ waitUntil: 'domcontentloaded' }); await record('back-2');
      await page.goForward({ waitUntil: 'domcontentloaded' }); await record('forward-1');
      await page.goForward({ waitUntil: 'domcontentloaded' }); await record('forward-2');
      c.check(`${lang}：后退、后退、前进、前进依次落在 repo-keys、dashboard、repo-keys、audit`, same(trail.slice(3).map((entry) => entry.path), ['/omc/repo-keys', '/omc/dashboard', '/omc/repo-keys', '/omc/audit']), JSON.stringify(trail));
      await context.close();
    }
  }

  // ---- C-32: the approval link's way back
  if (wants('C-32')) {
    const c = R.begin('C-32', '审批链接页的返回入口改指新页', 'P0');
    const filed = {};
    for (const [side, bin, api] of [['H', BIN, apis.H], ['B', BASE_BIN, apis.B]]) {
      filed[side] = [];
      for (const index of [1, 2]) {
        const repo = `example/backlink-${side.toLowerCase()}${index}`;
        filed[side].push((await fileApplication(api, { repo, publicKey: genPublicKey(bin, workDir('omchome-pages'), repo) })).approval_token);
      }
    }
    for (const [lang, linkText, name] of [['zh', '仓库自有密钥', '仓库自有密钥'], ['en', 'Repository keys', 'Repository keys']]) {
      for (const side of ['H', 'B']) {
        const app = side === 'H' ? APP : BASE_APP;
        const context = await newContext(browser, app, { lang });
        const page = await context.newPage();
        await page.goto(`${app}/repo-key-apply/${filed[side][lang === 'zh' ? 0 : 1]}`, { waitUntil: 'domcontentloaded' });
        await page.locator('h1').first().waitFor();
        const outside = (await readLocation(page)).isOutsideShell;
        const link = page.getByRole('link', { name: linkText, exact: true });
        const href = await link.getAttribute('href');
        c.check(`${side} ${lang}：审批链接页在侧栏之外`, outside);
        c.check(`${side} ${lang}：页尾返回入口文案是「${name}」`, (await link.count()) === 1 && (await link.innerText()).trim() === name, await link.allInnerTexts().then((texts) => texts.join('|')));
        c.check(`${side} ${lang}：目标 ${side === 'H' ? '/omc/repo-keys' : '/omc/api-keys'}`, href === (side === 'H' ? '/omc/repo-keys' : '/omc/api-keys'), href);
        if (lang === 'zh') c.evidenceFile(await shot(page, `C-32-${side}-apply-footer-zh`));
        await link.click();
        await waitForShell(page);
        const landed = await readLocation(page);
        if (side === 'H') {
          c.check(`H ${lang}：点击后进入 /repo-keys 而不是密钥管理页`, landed.path === '/omc/repo-keys', landed.path);
          c.check(`H ${lang}：菜单高亮落在「仓库自有密钥」`, same(landed.selected, ['/repo-keys']), JSON.stringify(landed.selected));
        } else {
          c.check(`B ${lang}：同一入口回到密钥管理页（对照）`, landed.path === '/omc/api-keys', landed.path);
        }
        if (lang === 'zh') c.evidenceFile(await shot(page, `C-32-${side}-apply-landing-zh`));
        await context.close();
      }
    }
  }

  // ---- C-33: link format, lifetime, approve and reject behave the same on both sides
  if (wants('C-33')) {
    const c = R.begin('C-33', '审批链接的格式、有效期与批准驳回行为不变', 'P1');
    const outcome = {};
    for (const [side, bin, api, app] of [['H', BIN, apis.H, APP], ['B', BASE_BIN, apis.B, BASE_APP]]) {
      const items = [];
      for (const index of [1, 2]) {
        const repo = `example/linkcheck-${index}`;
        const before = Date.now();
        const body = await fileApplication(api, { repo, publicKey: genPublicKey(bin, workDir('omchome-pages'), `${repo}-${side}`) });
        items.push({ repo, url: body.approval_url, token: body.approval_token, lifetimeMs: (body.approval_expires_at_ms ?? 0) - before });
      }
      const context = await newContext(browser, app);
      const tracker = trackRequests(context);
      const page = await context.newPage();
      const writes = [];
      await page.goto(items[0].url.replace(/^https?:\/\/[^/]+/, app.replace(/\/omc$/, '')), { waitUntil: 'domcontentloaded' });
      await page.getByRole('button', { name: '批准这次申请' }).waitFor();
      const mark1 = tracker.mark();
      await page.getByRole('button', { name: '批准这次申请' }).click();
      await page.getByTestId('repo-key-apply-outcome').waitFor();
      const approvedText = (await page.getByTestId('repo-key-apply-outcome').innerText()).replace(/\s+/g, ' ');
      writes.push(...tracker.since(mark1).filter((entry) => entry.method !== 'GET').map((entry) => `${normalizeRequest(entry)} ${JSON.stringify(entry.bodyKeys)}`));
      if (side === 'H') c.evidenceFile(await shot(page, 'C-33-H-approved'));
      await page.goto(items[1].url.replace(/^https?:\/\/[^/]+/, app.replace(/\/omc$/, '')), { waitUntil: 'domcontentloaded' });
      await page.getByRole('button', { name: /驳\s*回/ }).waitFor();
      await page.locator('#repo-key-apply-reason').fill('nav-check');
      const mark2 = tracker.mark();
      await page.getByRole('button', { name: /驳\s*回/ }).click();
      await page.getByTestId('repo-key-apply-outcome').waitFor();
      const rejectedText = (await page.getByTestId('repo-key-apply-outcome').innerText()).replace(/\s+/g, ' ');
      writes.push(...tracker.since(mark2).filter((entry) => entry.method !== 'GET').map((entry) => `${normalizeRequest(entry)} ${JSON.stringify(entry.bodyKeys)}`));
      await page.goto(items[0].url.replace(/^https?:\/\/[^/]+/, app.replace(/\/omc$/, '')), { waitUntil: 'domcontentloaded' });
      await page.getByTestId('repo-key-apply-outcome').waitFor();
      const reopenedText = (await page.locator('main').first().innerText()).replace(/\s+/g, ' ');
      outcome[side] = { items, approvedText, rejectedText, reopenedText, writes };
      await context.close();
    }
    for (const side of ['H', 'B']) {
      for (const item of outcome[side].items) {
        c.check(`${side} ${item.repo}：链接格式为 …/repo-key-apply/<令牌>`, /\/repo-key-apply\/[^/]+$/.test(item.url), item.url);
        c.check(`${side} ${item.repo}：有效期约 24 小时`, Math.abs(item.lifetimeMs - 24 * 3600 * 1000) < 10 * 60 * 1000, `${item.lifetimeMs} ms`);
      }
    }
    c.check('批准后的页面状态与提示两端相同', outcome.H.approvedText === outcome.B.approvedText, `${outcome.H.approvedText} | ${outcome.B.approvedText}`);
    c.check('驳回后的页面状态与提示两端相同', outcome.H.rejectedText === outcome.B.rejectedText, `${outcome.H.rejectedText} | ${outcome.B.rejectedText}`);
    // Repository names, timestamps and key fingerprints are the data each side was given, not behavior.
    const normalize = (text) => text.replace(/example\/[\w-]+/g, 'REPO').replace(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/g, 'TS').replace(/x25519:sha256:[\w-]+/g, 'FP');
    c.check('重开已处理的链接，显示已处理的结果，两端相同', normalize(outcome.H.reopenedText) === normalize(outcome.B.reopenedText), `${normalize(outcome.H.reopenedText)} | ${normalize(outcome.B.reopenedText)}`);
    c.check('批准与驳回发出的写请求（方法、路径、体字段）两端相同', same(outcome.H.writes, outcome.B.writes), `${JSON.stringify(outcome.H.writes)} vs ${JSON.stringify(outcome.B.writes)}`);
    c.note(`写请求：${JSON.stringify(outcome.H.writes)}`);
  }

  // ---- C-57: the lending entry's position, highlight, breadcrumb, tab title
  if (wants('C-57')) {
    const c = R.begin('C-57', '额度拆借挂入：位置、高亮、面包屑、标签页标题与页内标题', 'P0');
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    await open(page, `${APP}/dashboard`);
    const rail = await readRail(page);
    const group = rail.groups.find((entry) => entry.title === '能力');
    c.check('「能力」第三项名称「额度拆借」，地址 /quota-lending', group.items[2]?.name === '额度拆借' && group.items[2]?.path === '/quota-lending', JSON.stringify(group.items[2]));
    await page.locator('.app-menu [data-route-path="/quota-lending"]').click();
    await until(async () => pathOf(page) === '/omc/quota-lending', { label: 'lending opened' });
    await page.locator('[data-lending-pool], h1').first().waitFor();
    await until(async () => (await readBreadcrumb(page))?.current === '额度拆借', { label: 'lending breadcrumb' });
    const location = await readLocation(page);
    c.check('点击后地址为 /omc/quota-lending', location.path === '/omc/quota-lending', location.path);
    c.check('高亮落在「额度拆借」自己', same(location.selected, ['/quota-lending']), JSON.stringify(location.selected));
    c.check('「OAuth 管理」不高亮', !location.selected.includes('/oauth-management'));
    c.check('面包屑为「能力 › 额度拆借」', location.crumb?.group === '能力' && location.crumb?.current === '额度拆借', JSON.stringify(location.crumb));
    c.check('标签页标题为「Oh My CPA · 额度拆借」', location.title === 'Oh My CPA · 额度拆借', location.title);
    c.check('页内主标题仍是「额度拆借建议」', location.heading === '额度拆借建议', location.heading);
    c.evidenceFile(await shot(page, 'C-57-H-quota-lending-zh'));
    await context.close();
    const bContext = await newContext(browser, BASE_APP);
    const bPage = await bContext.newPage();
    await open(bPage, `${BASE_APP}/quota-lending`);
    await bPage.locator('[data-lending-pool], h1').first().waitFor();
    const bLocation = await readLocation(bPage);
    c.check('B：高亮、面包屑、标签页标题都借用「OAuth 管理」（对照）', same(bLocation.selected, ['/oauth-management']) && bLocation.crumb?.current === 'OAuth 管理' && bLocation.title.endsWith('OAuth 管理'), JSON.stringify(bLocation));
    c.evidenceFile(await shot(bPage, 'C-57-B-quota-lending-zh'));
    await bContext.close();
  }

  // ---- C-58: the lending entry's name in four languages and four places
  if (wants('C-58')) {
    const c = R.begin('C-58', '额度拆借的四种语言名称', 'P0');
    const context = await newContext(browser, APP);
    const bContext = await newContext(browser, BASE_APP);
    const page = await context.newPage();
    const bPage = await bContext.newPage();
    await open(page, `${APP}/quota-lending`);
    await open(bPage, `${BASE_APP}/quota-lending`);
    const readings = {};
    for (const lang of LANGS) {
      await switchLanguage(page, lang);
      await switchLanguage(bPage, lang);
      const expected = NAMES[lang].lending;
      await page.locator('h1').first().waitFor();
      const location = await readLocation(page);
      const rail = await readRail(page);
      const menuName = rail.flat.find((item) => item.path === '/quota-lending')?.name;
      c.check(`${lang}：菜单里的名称=${expected}`, menuName === expected, menuName);
      c.check(`${lang}：面包屑末节=${expected}`, location.crumb?.current === expected, JSON.stringify(location.crumb));
      c.check(`${lang}：标签页标题里的页名=${expected}`, location.title.endsWith(expected), location.title);
      const bLocation = await readLocation(bPage);
      c.check(`${lang}：页内主标题与 B 同语言下相同`, location.heading === bLocation.heading && location.heading.length > 0, `${location.heading} vs ${bLocation.heading}`);
      readings[lang] = { menuName, crumb: location.crumb?.current, title: location.title, heading: location.heading, baseHeading: bLocation.heading };
      if (lang === 'ms') c.evidenceFile(await shot(page, 'C-58-H-quota-lending-ms'));
    }
    // The settings panel carries the same name.
    for (const lang of LANGS) {
      await switchLanguage(page, lang);
      await openSidebarPanel(page, APP);
      const panel = await readSidebarPanel(page);
      const label = panel.groups.flatMap((group) => group.entries).find((entry) => entry.path === '/quota-lending')?.label;
      c.check(`${lang}：设置面板里的名称=${NAMES[lang].lending}`, label === NAMES[lang].lending, label);
      await open(page, `${APP}/quota-lending`);
    }
    c.note(`各语言读数：${JSON.stringify(readings)}`);
    await context.close();
    await bContext.close();
  }

  // ---- C-59: from the OAuth page header's entry
  if (wants('C-59')) {
    const c = R.begin('C-59', '从 OAuth 管理页头的入口进入', 'P1');
    const entryText = {};
    for (const [side, app] of [['H', APP], ['B', BASE_APP]]) {
      const context = await newContext(browser, app);
      const page = await context.newPage();
      await open(page, `${app}/oauth-management?page_size=48`);
      const entry = page.locator('[data-lending-entry]').first();
      await entry.waitFor({ timeout: 30_000 });
      const text = (await entry.innerText()).replace(/\s+/g, ' ').trim();
      const isLink = await entry.evaluate((node) => node.tagName === 'A' && Boolean(node.getAttribute('href')));
      entryText[side] = { text, isLink };
      await entry.click();
      await until(async () => pathOf(page) === '/omc/quota-lending', { label: `${side} entry clicked` });
      await page.locator('[data-lending-pool], h1').first().waitFor();
      const after = await readLocation(page);
      if (side === 'H') {
        c.check('H：点进后高亮落在「额度拆借」自己', same(after.selected, ['/quota-lending']), JSON.stringify(after.selected));
        c.check('H：面包屑为「能力 › 额度拆借」', after.crumb?.group === '能力' && after.crumb?.current === '额度拆借', JSON.stringify(after.crumb));
        c.evidenceFile(await shot(page, 'C-59-H-after-entry-click'));
      } else {
        c.check('B：点进后借用「OAuth 管理」的高亮（对照）', same(after.selected, ['/oauth-management']), JSON.stringify(after.selected));
      }
      await page.goBack({ waitUntil: 'domcontentloaded' });
      await waitForShell(page);
      await until(async () => pathOf(page) === '/omc/oauth-management', { label: `${side} back to oauth` });
      await page.locator('[data-lending-entry]').first().waitFor({ timeout: 30_000 });
      const back = await readLocation(page);
      c.check(`${side}：后退回到 OAuth 管理页，高亮回到「OAuth 管理」，入口仍在`, same(back.selected, ['/oauth-management']) && (await page.locator('[data-lending-entry]').count()) > 0, JSON.stringify(back.selected));
      await context.close();
    }
    c.check('入口的文字与可点状态两端相同', entryText.H.text === entryText.B.text && entryText.H.isLink === entryText.B.isLink && entryText.H.isLink, JSON.stringify(entryText));
    c.note(`入口：${JSON.stringify(entryText)}`);
  }

  // ---- C-60: direct open, refresh, history on the lending page
  if (wants('C-60')) {
    const c = R.begin('C-60', '额度拆借页的直接打开、刷新、前进后退（烟测）', 'P2');
    const context = await newContext(browser, APP);
    const page = await context.newPage();
    const settle = async (label) => {
      await waitForShell(page);
      await page.locator('h1').first().waitFor();
      await until(async () => (await readBreadcrumb(page)) !== null, { label });
      return readLocation(page);
    };
    await page.goto(`${APP}/quota-lending`, { waitUntil: 'domcontentloaded' });
    let location = await settle('direct');
    c.check('直接输入：落在额度拆借页，高亮与面包屑正确，页内标题不变', location.path === '/omc/quota-lending' && same(location.selected, ['/quota-lending']) && location.crumb?.current === '额度拆借' && location.heading === '额度拆借建议', JSON.stringify(location));
    await page.reload({ waitUntil: 'domcontentloaded' });
    location = await settle('reload');
    c.check('刷新：仍在额度拆借页', location.path === '/omc/quota-lending' && same(location.selected, ['/quota-lending']), JSON.stringify(location));
    const trail = [];
    const record = async (label) => { const read = await settle(label); trail.push({ label, path: read.path, selected: read.selected, crumb: read.crumb?.current, heading: read.heading }); };
    await page.goto(`${APP}/dashboard`, { waitUntil: 'domcontentloaded' }); await record('dashboard');
    await page.goto(`${APP}/quota-lending`, { waitUntil: 'domcontentloaded' }); await record('quota-lending');
    await page.goto(`${APP}/oauth-management`, { waitUntil: 'domcontentloaded' }); await record('oauth-management');
    await page.goBack({ waitUntil: 'domcontentloaded' }); await record('back-1');
    await page.goBack({ waitUntil: 'domcontentloaded' }); await record('back-2');
    await page.goForward({ waitUntil: 'domcontentloaded' }); await record('forward-1');
    await page.goForward({ waitUntil: 'domcontentloaded' }); await record('forward-2');
    c.check('后退、后退、前进、前进依次落在 quota-lending、dashboard、quota-lending、oauth-management', same(trail.slice(3).map((entry) => entry.path), ['/omc/quota-lending', '/omc/dashboard', '/omc/quota-lending', '/omc/oauth-management']), JSON.stringify(trail.map((entry) => `${entry.label}:${entry.path}`)));
    const lendingStops = trail.filter((entry) => entry.path === '/omc/quota-lending');
    c.check('每次在额度拆借页时高亮、面包屑、页内标题同 C-57', lendingStops.every((entry) => same(entry.selected, ['/quota-lending']) && entry.crumb === '额度拆借' && entry.heading === '额度拆借建议'), JSON.stringify(lendingStops));
    await context.close();
  }
} finally {
  for (const api of Object.values(apis)) await api.dispose().catch(() => {});
  await shutdown(browser);
  R.finish();
}
