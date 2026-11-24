// C-01, C-02, C-04, C-05, C-08, C-09: the rail's groups, members, names, title style and icons.
// Runs against H (T9_APP) and B (T9_BASE_APP), both with the fake CPA, hidden entries cleared and the page-bearing plugin stopped.
//   node scripts/acceptance/capabilities-nav/01-structure.mjs
import { APP, BASE_APP, LANGS, NAMES, Results, wants, apiClient, disableFixturePlugin, launch, newContext, same, shot, shotTall, shutdown, unique } from './lib.mjs';
import { collapseRail, groupTitleStyles, open, readRail, switchLanguage } from './nav.mjs';

const R = new Results('01-structure');
const EXPECT_H = [
  ['运行', ['仪表盘', '智能体', '操练场', '模型广场']],
  ['网关', ['AI 提供商', 'OAuth 管理', '密钥管理']],
  ['观测', ['洞察', '请求记录', '费用与用量', '日志查看', '操作审计']],
  ['能力', ['模型组', '仓库自有密钥', '额度拆借']],
  ['插件', ['插件管理', '插件商店']],
  ['控制', ['配置面板', 'OMC 设置', '系统信息']],
];

const browser = await launch();
const apis = [];
try {
  const hApi = await apiClient(APP);
  const bApi = await apiClient(BASE_APP);
  apis.push(hApi, bApi);
  for (const api of apis) { await api.setHidden([]); await disableFixturePlugin(api); }

  const hContext = await newContext(browser, APP);
  const bContext = await newContext(browser, BASE_APP);
  const hPage = await hContext.newPage();
  const bPage = await bContext.newPage();
  await open(hPage, `${APP}/dashboard`);
  await open(bPage, `${BASE_APP}/dashboard`);
  const h = await readRail(hPage);
  const b = await readRail(bPage);

  // ---- C-01
  if (wants('C-01')) {
    const c = R.begin('C-01', '展开栏的分组数量、顺序与各组条目', 'P0');
    c.check('H 的分组标题恰为 6 个', h.groupTitles.length === 6, JSON.stringify(h.groupTitles));
    c.check('H 分组自上而下：运行、网关、观测、能力、插件、控制', same(h.groupTitles, EXPECT_H.map(([title]) => title)), JSON.stringify(h.groupTitles));
    c.check('「能力」位于「观测」与「插件」之间', h.groupTitles.indexOf('能力') === h.groupTitles.indexOf('观测') + 1 && h.groupTitles.indexOf('插件') === h.groupTitles.indexOf('能力') + 1);
    for (const [title, names] of EXPECT_H) {
      const group = h.groups.find((entry) => entry.title === title);
      c.check(`H「${title}」的条目与先后`, group && same(group.items.map((item) => item.name), names), JSON.stringify(group?.items.map((item) => item.name)));
    }
    c.check('H 固定条目共 20 个', h.flat.length === 20, `count=${h.flat.length}`);
    c.check('H 没有空分组', h.groups.every((group) => group.items.length > 0));
    c.check('B 是 5 组，「运行」含模型组（对照）', b.groupTitles.length === 5 && b.groups[0].items.some((item) => item.name === '模型组'), JSON.stringify(b.groupTitles));
    for (const title of ['网关', '观测', '插件', '控制']) {
      const hGroup = h.groups.find((entry) => entry.title === title);
      const bGroup = b.groups.find((entry) => entry.title === title);
      c.check(`「${title}」的条目名称与组内先后与 B 相同`, same(hGroup?.items.map((item) => item.name), bGroup?.items.map((item) => item.name)), `${JSON.stringify(hGroup?.items.map((i) => i.name))} vs ${JSON.stringify(bGroup?.items.map((i) => i.name))}`);
    }
    const bRun = b.groups.find((entry) => entry.title === '运行').items.map((item) => item.name).filter((name) => name !== '模型组');
    c.check('「运行」除模型组外与 B 相同', same(h.groups[0].items.map((item) => item.name), bRun), JSON.stringify(bRun));
    c.evidenceFile(await shotTall(hPage, 'C-01-H-expanded-zh'));
    c.evidenceFile(await shotTall(bPage, 'C-01-B-expanded-zh'));
  }

  // ---- C-05
  if (wants('C-05')) {
    const c = R.begin('C-05', '「能力」组内的成员与顺序', 'P0');
    const group = h.groups.find((entry) => entry.title === '能力');
    c.check('恰为三项', group?.items.length === 3, `count=${group?.items.length}`);
    c.check('名称自上而下：模型组、仓库自有密钥、额度拆借', same(group?.items.map((item) => item.name), ['模型组', '仓库自有密钥', '额度拆借']), JSON.stringify(group?.items.map((item) => item.name)));
    c.check('地址：/model-groups、/repo-keys、/quota-lending', same(group?.items.map((item) => item.path), ['/model-groups', '/repo-keys', '/quota-lending']), JSON.stringify(group?.items.map((item) => item.path)));
    c.evidenceFile(await shot(hPage, 'C-05-H-capabilities-zh'));
  }

  // ---- C-09
  if (wants('C-09')) {
    const c = R.begin('C-09', '「运行」组不再有模型组', 'P0');
    c.check('「运行」下恰为仪表盘、智能体、操练场、模型广场', same(h.groups[0].items.map((item) => item.name), ['仪表盘', '智能体', '操练场', '模型广场']));
    const holders = h.groups.filter((group) => group.items.some((item) => item.name === '模型组')).map((group) => group.title);
    c.check('「模型组」只在「能力」下出现一次', same(holders, ['能力']), JSON.stringify(holders));
    c.check('整个展开栏里「模型组」只出现一次', h.flat.filter((item) => item.name === '模型组').length === 1);
    c.evidenceFile(await shot(hPage, 'C-09-H-operate-zh'));
  }

  // ---- C-02 (live switching, no reload)
  if (wants('C-02')) {
    const c = R.begin('C-02', '四种语言下的「能力」分组标题，现场切换', 'P0');
    const readings = {};
    for (const lang of LANGS) {
      await switchLanguage(hPage, lang);
      await switchLanguage(bPage, lang);
      const [hr, br] = [await readRail(hPage), await readRail(bPage)];
      readings[lang] = { h: hr.groupTitles, b: br.groupTitles };
      const expected = NAMES[lang].group.toLowerCase();
      c.check(`${lang}：H 的标题数为 6`, hr.groupTitles.length === 6, JSON.stringify(hr.groupTitles));
      c.check(`${lang}：第 4 个标题是「${NAMES[lang].group}」`, hr.groupTitles[3]?.toLowerCase() === expected, hr.groupTitles[3]);
      const others = hr.groupTitles.filter((_, index) => index !== 3);
      c.check(`${lang}：其余五个标题与 B 同语言逐字相同`, same(others, br.groupTitles), `${JSON.stringify(others)} vs ${JSON.stringify(br.groupTitles)}`);
      const everything = [...hr.groupTitles, ...hr.flat.map((item) => item.name)];
      c.check(`${lang}：没有内部键名、空白`, everything.every((text) => text.length > 0 && !/\bnav\.|\.\w+_\w+/.test(text)), JSON.stringify(everything.filter((text) => !text || /\bnav\./.test(text))));
      c.check(`${lang}：「能力」标题不是别的语言的回退文字`, lang === 'zh' || lang === 'zh-Hant' || !/[一-鿿]/.test(hr.groupTitles[3]), hr.groupTitles[3]);
      if (lang === 'en' || lang === 'ms') c.evidenceFile(await shotTall(hPage, `C-02-H-rail-${lang}`));
      if (lang === 'zh-Hant') c.evidenceFile(await shotTall(hPage, 'C-02-H-rail-zh-Hant'));
    }
    c.note(`各语言的分组标题（H / B）：${JSON.stringify(readings)}`);
    await switchLanguage(hPage, 'zh');
    await switchLanguage(bPage, 'zh');
  }
  await hContext.close();
  await bContext.close();

  // ---- C-04: four combinations
  if (wants('C-04')) {
    const c = R.begin('C-04', '「能力」标题样式与相邻分组一致', 'P0');
    const combos = [
      { key: 'zh-light', lang: 'zh', theme: 'omc-light', titles: ['运行', '网关', '观测', '能力'] },
      { key: 'en-light', lang: 'en', theme: 'omc-light', titles: ['Operate', 'Gateway', 'Observe', 'Capabilities'] },
      { key: 'zh-dark', lang: 'zh', theme: 'omc-dark', titles: ['运行', '网关', '观测', '能力'] },
      { key: 'en-dark', lang: 'en', theme: 'omc-dark', titles: ['Operate', 'Gateway', 'Observe', 'Capabilities'] },
    ];
    const fields = ['fontSize', 'fontWeight', 'color', 'textAlign', 'textTransform', 'letterSpacing', 'lineHeight', 'paddingLeft', 'marginLeft', 'boxLeft', 'boxHeight'];
    const table = {};
    for (const combo of combos) {
      const context = await newContext(browser, APP, { lang: combo.lang, theme: combo.theme });
      const page = await context.newPage();
      await open(page, `${APP}/dashboard`);
      const styles = [];
      for (const title of combo.titles) styles.push(await groupTitleStyles(page, title));
      table[combo.key] = styles;
      c.check(`${combo.key}：四个分组标题都取到`, styles.every(Boolean), JSON.stringify(combo.titles));
      if (styles.every(Boolean)) {
        const reference = styles[2];
        const capabilities = styles[3];
        for (const field of fields) {
          const differing = styles.filter((entry) => entry[field] !== reference[field]).length;
          c.check(`${combo.key}：${field} 在四个标题上相同`, differing === 0, styles.map((entry) => entry[field]).join(' | '));
        }
        c.check(`${combo.key}：「能力」标题没有子元素、伪元素内容为 none`, capabilities.children === 0 && ['none', 'normal'].includes(capabilities.beforeContent) && ['none', 'normal'].includes(capabilities.afterContent), `children=${capabilities.children} before=${capabilities.beforeContent} after=${capabilities.afterContent}`);
        const observe = styles[2].rect;
        const capability = styles[3].rect;
        const clip = (rect) => ({ x: Math.max(0, rect.x - 6), y: Math.max(0, rect.y - 6), width: 226, height: rect.height + 12 });
        c.evidenceFile(await shot(page, `C-04-${combo.key}-observe-title`, { clip: clip(observe) }));
        c.evidenceFile(await shot(page, `C-04-${combo.key}-capabilities-title`, { clip: clip(capability) }));
        c.evidenceFile(await shotTall(page, `C-04-${combo.key}-rail`));
      }
      await context.close();
    }
    c.note(`标题计算样式（按组合）：${JSON.stringify(table).slice(0, 2500)}`);
  }

  // ---- C-08: icons, expanded and collapsed
  if (wants('C-08')) {
    const c = R.begin('C-08', '固定条目的图标两两不同', 'P0');
    const context = await newContext(browser, APP, { deviceScaleFactor: 3 });
    const page = await context.newPage();
    await open(page, `${APP}/dashboard`);
    const expanded = await readRail(page);
    const signatures = expanded.flat.map((entry) => entry.icon);
    c.check('固定条目 20 个', expanded.flat.length === 20, `count=${expanded.flat.length}`);
    c.check('每个条目都有图标', signatures.every((signature) => signature.length > 0));
    c.check('20 个图标签名两两不同', unique(signatures), JSON.stringify(expanded.flat.filter((entry, index) => signatures.indexOf(entry.icon) !== index).map((entry) => entry.name)));
    const byPath = Object.fromEntries(expanded.flat.map((entry) => [entry.path, entry]));
    c.check('「仓库自有密钥」不同于「密钥管理」', byPath['/repo-keys'].icon !== byPath['/api-keys'].icon);
    c.check('「额度拆借」不同于其余 19 个', expanded.flat.filter((entry) => entry.path !== '/quota-lending').every((entry) => entry.icon !== byPath['/quota-lending'].icon));
    for (const [name, route] of [['api-keys', '/api-keys'], ['repo-keys', '/repo-keys'], ['quota-lending', '/quota-lending']]) {
      const icon = page.locator(`.app-menu [data-route-path="${route}"]`);
      await icon.scrollIntoViewIfNeeded();
      const box = await icon.boundingBox();
      c.evidenceFile(await shot(page, `C-08-icon-${name}`, { clip: { x: box.x, y: box.y, width: 236, height: box.height } }));
    }
    await collapseRail(page);
    const collapsed = await readRail(page);
    c.check('收起栏同样是 20 个条目', collapsed.flat.length === 20, `count=${collapsed.flat.length}`);
    c.check('同一条目在展开栏与收起栏里签名相同', expanded.flat.every((entry) => collapsed.flat.find((other) => other.path === entry.path)?.icon === entry.icon));
    c.evidenceFile(await shotTall(page, 'C-08-H-collapsed-icons', 1250));
    await context.close();
  }

} finally {
  for (const api of apis) await api.dispose().catch(() => {});
  await shutdown(browser);
  R.finish();
}
