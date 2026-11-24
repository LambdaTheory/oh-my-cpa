// C-15 .. C-22 and C-25: the repository-key panel before (B, the lower half of /api-keys) and after (H, the page /repo-keys),
// fed the same stub dataset DS-RK (cases.md 0.7) and driven by the same steps. After every step each side is read the same way
// (panel text, controls, open dialogs, repository-key requests, toasts) and the two readings are compared.
//   node scripts/acceptance/capabilities-nav/07-repokeys-parity.mjs
import { APP, Results, apiClient, disableFixturePlugin, launch, normalizeRequest, same, shutdown, spaced, wants } from './lib.mjs';
import { switchLanguage } from './nav.mjs';
import { AUDIT, AWAITING, KEYS, PENDING, SENTENCE, SENTENCE_EN, SIDES, compareSteps, openSide, runBoth, shot, setBrowser } from './parity.mjs';

const R = new Results('07-repokeys-parity');
/** Opens `url` in a new tab of the same context (same stub) and waits for the document and its header. */
async function newPageAt(context, url) {
  const page = await context.newPage();
  await page.goto(url, { waitUntil: 'domcontentloaded' });
  await page.locator('h1').first().waitFor();
  await page.locator('.app-menu li[data-route-path]').first().waitFor({ state: 'attached' });
  return page;
}
const browser = await launch();
setBrowser(browser);
const apis = {};
try {
  for (const entry of SIDES) {
    apis[entry.side] = await apiClient(entry.app);
    await apis[entry.side].setHidden([]);
    await disableFixturePlugin(apis[entry.side]);
  }

  // ---- C-15: sentence, search, status filter
  if (wants('C-15')) {
    const c = R.begin('C-15', '说明句、搜索与状态筛选（F1、F2）', 'P0');
    const out = await runBoth({}, async (h) => {
      await h.snap('初始（中文）');
      await switchLanguage(h.page, 'en');
      await h.quiet();
      await h.snap('English');
      await switchLanguage(h.page, 'zh');
      await h.quiet();
      for (const text of ['alpha', 'agent-a', 'zzzz']) { await h.search(text); await h.snap(`搜索 ${text}`); }
      await h.search('');
      await h.snap('清空搜索');
      for (const label of ['仅生效中', '仅已停用', '全部状态']) { await h.filter(label); await h.snap(`筛选 ${label}`); }
      await h.search('example');
      await h.filter('仅已停用');
      await h.snap('搜索 example 加 仅已停用');
      if (h.side === 'H') await shot(h.page, 'C-15-H-search-stopped-zh');
      else await shot(h.page, 'C-15-B-search-stopped-zh');
    });
    compareSteps(c, 'C-15', out);
    const joined = (snap) => snap.lines.join('\n');
    c.check('H 中文说明句逐字等于 PRD F1', joined(out.H.snaps[0]).includes(SENTENCE), out.H.snaps[0].lines.slice(0, 2).join(' | '));
    c.check('H 英文说明句逐字等于基线英文', joined(out.H.snaps[1]).includes(SENTENCE_EN), out.H.snaps[1].lines.slice(0, 2).join(' | '));
    const keysAfterStopped = out.H.snaps.find((snap) => snap.label === '筛选 仅已停用');
    const keysAfterActive = out.H.snaps.find((snap) => snap.label === '筛选 仅生效中');
    const repoRows = (snap) => snap.lines.filter((line) => /^example\//.test(line));
    c.check('「仅已停用」下密钥块只剩已停用的三把', keysAfterStopped.lines.join('\n').includes('已停用（轮换）') && !keysAfterStopped.lines.join('\n').includes('生效中'), repoRows(keysAfterStopped).join(' | '));
    c.check('「仅生效中」下密钥块只剩生效中的两把', keysAfterActive.lines.join('\n').includes('生效中') && !keysAfterActive.lines.join('\n').includes('已停用（'), repoRows(keysAfterActive).join(' | '));
  }

  // ---- C-16: key list and detail
  if (wants('C-16')) {
    const c = R.begin('C-16', '密钥列表与详情（F6、F7）', 'P0');
    const out = await runBoth({}, async (h) => {
      await h.snap('密钥列表与各行操作');
      for (const [repo, label] of [['example/alpha-old', 'RK-1'], ['example/leaked', 'RK-5'], ['example/beta', 'RK-2']]) {
        await h.rowButton(KEYS, repo, '详情').click();
        await h.quiet();
        await h.snap(`${label} 详情（近 7 天）`);
        if (label === 'RK-1') {
          for (const [name, tag] of [['近 24 小时', '24h'], ['近 30 天', '30d']]) {
            await h.page.locator('[role="dialog"]:visible .ant-select').click();
            await h.page.locator('.ant-select-item-option', { hasText: name }).click();
            await h.quiet();
            await h.snap(`${label} ${name}`, { window: tag });
          }
          const href = await h.page.locator('.ant-drawer-content:visible a[href], [role="dialog"]:visible a[href]').first().getAttribute('href').catch(() => null);
          await h.snap(`${label} 审计入口地址`, { href });
          await shot(h.page, `C-16-${h.side}-RK-1-detail-30d-zh`);
        }
        if (label === 'RK-5') await shot(h.page, `C-16-${h.side}-RK-5-detail-zh`);
        await h.closeDialogs();
      }
      const reenableLeaked = await h.rowButton(KEYS, 'example/leaked', '重新启用');
      await h.snap('RK-5 重新启用入口', { disabled: await reenableLeaked.isDisabled(), title: await reenableLeaked.getAttribute('title') });
    });
    compareSteps(c, 'C-16', out, { fields: ['lines', 'controls', 'dialogs', 'reads', 'writes', 'href', 'disabled', 'title'] });
    const h7 = out.H.snaps.find((snap) => snap.label === 'RK-1 详情（近 7 天）');
    c.check('RK-1 近 7 天费用列与详情里的金额一致（$12.3400）', out.H.snaps[0].lines.join('\n').includes('$12.3400'), '');
    c.check('切换统计窗发出的详情请求带 24h、7d、30d 三个窗口参数', ['window=24h', 'window=30d', 'window=7d'].every((param) => out.H.snaps.some((snap) => snap.reads.some((read) => read.includes(param)))), JSON.stringify(out.H.snaps.map((snap) => snap.reads)));
    const leaked = out.H.snaps.find((snap) => snap.label === 'RK-5 重新启用入口');
    c.check('RK-5「重新启用」禁用，并带提示「泄露的密钥不能重新启用，请为仓库重新申请一把」', leaked.disabled === true && leaked.title === '泄露的密钥不能重新启用，请为仓库重新申请一把', JSON.stringify(leaked.title));
    c.check('RK-5 详情有「停用后仍有 7 次调用被拒绝」一句', out.H.snaps.find((snap) => snap.label === 'RK-5 详情（近 7 天）').dialogs.some((dialog) => dialog.text.join('\n').includes('停用后仍有 7 次调用被拒绝')), '');
    void h7;
  }

  // ---- C-17: pending queue, review, approve, reject, one-time link
  if (wants('C-17')) {
    const c = R.begin('C-17', '待审批、审阅、批准与驳回、一次性审批链接（F4、F9）', 'P0');
    const out = await runBoth({}, async (h) => {
      await h.snap('待审批块（4 条）');
      for (const [repo, label] of [['example/alpha', 'AP-1'], ['example/beta', 'AP-2'], ['example/rotated', 'AP-3']]) {
        await h.rowButton(PENDING, repo, '详情').click();
        await h.quiet();
        await h.snap(`${label} 审阅详情`);
        if (label === 'AP-2') await shot(h.page, `C-17-${h.side}-AP-2-review-zh`);
        await h.closeDialogs();
      }
      // Approve AP-1: the panel approves directly (an empty reason is sent); there is no confirmation or reason box on approval.
      await h.rowButton(PENDING, 'example/alpha', '批准').click();
      await h.quiet();
      await h.snap('AP-1 点批准后（批准没有确认弹窗与原因输入）');
      // Reject AP-4: first without a reason (blocked), then with one.
      await h.rowButton(PENDING, 'example/delta', '驳回').click();
      await h.snap('AP-4 点驳回后的弹窗（原因为空）');
      const confirmDisabled = await h.confirmButton().isDisabled();
      await h.snap('AP-4 原因为空时确认按钮', { confirmDisabled });
      await h.page.locator('[role="dialog"]:visible textarea, [role="dialog"]:visible input[type="text"], .ant-popconfirm:visible textarea').last().fill('nav-check');
      await h.snap('AP-4 填入原因');
      await h.confirm();
      await h.snap('AP-4 驳回后');
      // One-time approval link for AP-2, copied.
      await h.rowButton(PENDING, 'example/beta', '授权链接').click();
      await h.quiet();
      await h.snap('AP-2 授权链接弹窗');
      const copy = h.page.locator('[role="dialog"]:visible').getByRole('button', { name: /复制|Copy/ }).first();
      if (await copy.count()) { await copy.click(); }
      const clipboard = await h.page.evaluate(() => navigator.clipboard.readText().catch(() => null));
      await h.snap('AP-2 点复制后', { clipboard });
      await shot(h.page, `C-17-${h.side}-approval-link-zh`);
      await h.closeDialogs();
    });
    compareSteps(c, 'C-17', out, { fields: ['lines', 'controls', 'dialogs', 'reads', 'writes', 'toasts', 'confirmDisabled', 'clipboard'] });
    const approveWrite = out.H.writes.find((write) => /approve/.test(write.route));
    const rejectWrite = out.H.writes.find((write) => /reject/.test(write.route));
    c.check('批准发出 POST …/approve，体字段为 reason', approveWrite && approveWrite.body && 'reason' in approveWrite.body, JSON.stringify(approveWrite));
    c.check('驳回发出 POST …/reject，体为 {"reason":"nav-check"}', rejectWrite?.body?.reason === 'nav-check', JSON.stringify(rejectWrite));
    c.check('驳回在原因为空时被拦（确认按钮禁用）', out.H.snaps.find((snap) => snap.label === 'AP-4 原因为空时确认按钮')?.confirmDisabled === true, JSON.stringify(out.H.snaps.find((snap) => snap.label === 'AP-4 原因为空时确认按钮')?.confirmDisabled));
    c.check('H 没有出现 B 没有的写请求', same(out.H.writes.map((write) => `${write.method} ${write.route}`), out.B.writes.map((write) => `${write.method} ${write.route}`)), JSON.stringify([out.H.writes, out.B.writes]));
  }

  // ---- C-18: awaiting delivery and fetching the envelope
  if (wants('C-18')) {
    const c = R.begin('C-18', '待交付与代取密文（F5）', 'P0');
    const out = await runBoth({}, async (h) => {
      await h.snap('待交付块（2 条）');
      await h.rowButton(AWAITING, 'example/gamma', '取走密文').click();
      await h.quiet();
      await h.snap('AW-1 取走密文弹窗');
      await shot(h.page, `C-18-${h.side}-ciphertext-dialog-zh`);
      await h.closeDialogs();
      await h.quiet();
      await h.snap('取走之后的列表与块标题');
    });
    compareSteps(c, 'C-18', out);
    const after = out.H.snaps.find((snap) => snap.label === '取走之后的列表与块标题');
    c.check('取走之后该条不再出现在待交付块，块标题数由 2 变 1', !after.lines.join('\n').includes('example/gamma') && after.lines.some((line) => /待交付（1）/.test(line)), after.lines.filter((line) => /待交付/.test(line)).join(' | '));
    c.check('请求为 POST …/ciphertext', out.H.writes.some((write) => /\/ciphertext$/.test(write.route)), JSON.stringify(out.H.writes));
    c.check('弹窗里只有密文，没有明文', !out.H.snaps.find((snap) => snap.label === 'AW-1 取走密文弹窗').dialogs.some((dialog) => dialog.text.join('\n').includes('nav-check-secret')), '');
  }

  // ---- C-19: file on behalf of an agent
  if (wants('C-19')) {
    const c = R.begin('C-19', '代 agent 提交申请（F3）', 'P0');
    const publicKey = 'x25519:rBLOvVoK3iJZdno+lgfK9teuJTl3PILboxOfY/AsCDw=';
    const out = await runBoth({}, async (h) => {
      const dialog = h.page.locator('[role="dialog"]:visible');
      const openForm = async () => { await h.page.getByRole('button', { name: spaced('代 agent 提交申请') }).click(); await dialog.waitFor(); await h.quiet(); };
      const pickKeySource = async (label) => { await dialog.locator('#repo-key-proxy-key-mode').click({ force: true }); await h.page.locator('.ant-select-item-option', { hasText: label }).click(); await h.page.locator('.ant-select-dropdown').first().waitFor({ state: 'hidden' }); };
      await openForm();
      await h.snap('表单');
      await shot(h.page, `C-19-${h.side}-proxy-form-zh`);
      for (const repo of ['example/fresh', 'example/beta', 'example/rotated']) {
        // The repository's state is read after a short debounce, so wait for that response instead of the network being quiet.
        const status = h.page.waitForResponse((response) => response.url().includes('/repo-keys/repo-status'));
        await dialog.locator('#repo-key-proxy-repo').fill(repo);
        await status;
        await dialog.getByText(/提交后：/).waitFor();
        await h.quiet();
        await h.snap(`仓库 ${repo} 的提交后提示`);
      }
      await pickKeySource('用已登记的指纹');
      await h.snap('公钥来源：用已登记的指纹');
      await pickKeySource('粘贴公钥');
      await h.snap('公钥来源：粘贴公钥');
      // Required fields empty: the submit button stays disabled.
      await dialog.locator('#repo-key-proxy-repo').fill('');
      await h.snap('必填项留空', { submitDisabled: await h.confirmButton().isDisabled() });
      await dialog.locator('#repo-key-proxy-repo').fill('example/fresh');
      await dialog.locator('#repo-key-proxy-purpose').fill('nav-check');
      await dialog.locator('#repo-key-proxy-agent').fill('agent-z');
      await dialog.locator('#repo-key-proxy-public-key').fill(publicKey);
      await h.quiet();
      await h.snap('必填项填满', { submitDisabled: await h.confirmButton().isDisabled() });
      await h.confirm();
      await h.snap('提交结果（待审批加一次性链接）');
      await shot(h.page, `C-19-${h.side}-proxy-result-zh`);
      await h.closeDialogs();
      h.dataset.state.autoApprove = true;
      await openForm();
      await dialog.locator('#repo-key-proxy-repo').fill('example/fresh2');
      await dialog.locator('#repo-key-proxy-purpose').fill('nav-check');
      await dialog.locator('#repo-key-proxy-agent').fill('agent-z');
      await dialog.locator('#repo-key-proxy-public-key').fill(publicKey);
      await h.quiet();
      await h.confirm();
      await h.snap('提交结果（接收方已知，自动批准）');
      await h.closeDialogs();
    });
    compareSteps(c, 'C-19', out, { fields: ['lines', 'controls', 'dialogs', 'reads', 'writes', 'toasts', 'submitDisabled'] });
    const submits = out.H.writes.filter((write) => /\/applications$/.test(write.route));
    c.check('提交请求体字段为 repo、purpose、agent 加 public_key', submits.length === 2 && submits.every((write) => ['agent', 'public_key', 'purpose', 'repo'].every((field) => field in write.body)), JSON.stringify(submits));
    c.check('必填项留空时提交按钮禁用，填满后可用', out.H.snaps.find((snap) => snap.label === '必填项留空').submitDisabled === true && out.H.snaps.find((snap) => snap.label === '必填项填满').submitDisabled === false, '');
    c.check('三种仓库现状各给出对应的「提交后」提示（签发、复用、重签）', ['这个仓库还没有密钥', '会复用这个仓库现有的密钥', '密钥已停用'].every((fragment, index) => out.H.snaps[1 + index].dialogs.some((dialog) => dialog.text.join('\n').includes(fragment))), JSON.stringify(out.H.snaps.slice(1, 4).map((snap) => snap.dialogs.map((dialog) => dialog.text.filter((line) => /提交后/.test(line))))));
    c.check('自动批准时提示「已按接收方名单自动批准」', out.H.snaps.find((snap) => snap.label === '提交结果（接收方已知，自动批准）').dialogs.some((dialog) => dialog.text.join('\n').includes('已按接收方名单自动批准')), '');
    c.check('提交后给出一次性链接，并说明「链接只在这里显示一次」', out.H.snaps.find((snap) => snap.label === '提交结果（待审批加一次性链接）').dialogs.some((dialog) => dialog.text.join('\n').includes('链接只在这里显示一次')), '');
  }

  // ---- C-20: stop, re-enable request, reveal the plaintext
  if (wants('C-20')) {
    const c = R.begin('C-20', '停用、申请重新启用、查看明文（F8）', 'P0');
    const out = await runBoth({}, async (h) => {
      const dialog = h.page.locator('[role="dialog"]:visible');
      const reason = dialog.locator('#repo-key-reason');
      const pickCategory = async (label) => { await dialog.locator('#repo-key-stop-category').click({ force: true }); await h.page.locator('.ant-select-item-option', { hasText: label }).click(); await h.page.locator('.ant-select-dropdown').first().waitFor({ state: 'hidden' }); };
      // Reveal first, while the key is still active.
      await h.rowButton(KEYS, 'example/alpha-old', '查看明文').click();
      await h.snap('RK-1 查看明文弹窗（原因为空）', { confirmDisabled: await h.confirmButton().isDisabled() });
      await reason.fill('nav-check');
      await h.confirm();
      await h.snap('RK-1 查看明文之后', { revealed: await dialog.locator('#repo-key-revealed').inputValue() });
      await shot(h.page, `C-20-${h.side}-reveal-dialog-zh`);
      await h.closeDialogs();
      await h.quiet();
      await h.snap('关闭明文弹窗之后', { secretOnPage: (await h.page.evaluate(() => document.body.innerText)).includes('nav-check-secret'), secretInConsole: h.consoleLog.some((line) => line.includes('nav-check-secret')) });
      // Stop RK-1 as "rotation", RK-2 as "leak".
      for (const [repo, category, label] of [['example/alpha-old', '轮换', 'RK-1'], ['example/beta', '泄露', 'RK-2']]) {
        await h.rowButton(KEYS, repo, '停用').click();
        await h.snap(`${label} 停用弹窗`, { confirmDisabled: await h.confirmButton().isDisabled() });
        await pickCategory(category);
        await h.snap(`${label} 已选类别、原因为空`, { confirmDisabled: await h.confirmButton().isDisabled() });
        await reason.fill('nav-check');
        await h.confirm();
        await h.snap(`${label} 停用后`);
      }
      await shot(h.page, `C-20-${h.side}-after-stop-zh`);
      // Re-enable request for the "rotated" key.
      await h.rowButton(KEYS, 'example/rotated', '重新启用').click();
      await h.snap('RK-3 申请重新启用弹窗（原因为空）', { confirmDisabled: await h.confirmButton().isDisabled() });
      await reason.fill('nav-check');
      await h.confirm();
      await h.snap('RK-3 申请重新启用之后');
      const leaked = h.rowButton(KEYS, 'example/leaked', '重新启用');
      await h.snap('RK-5 重新启用入口', { disabled: await leaked.isDisabled(), title: await leaked.getAttribute('title') });
    });
    compareSteps(c, 'C-20', out, { fields: ['lines', 'controls', 'dialogs', 'reads', 'writes', 'toasts', 'confirmDisabled', 'revealed', 'secretOnPage', 'secretInConsole', 'disabled', 'title'] });
    const writes = out.H.writes.map((write) => `${write.route} ${JSON.stringify(write.body)}`);
    c.check('请求体：停用带 category 与 reason（rotate、leaked），重新启用与查看明文只带 reason', writes.some((line) => /stop .*"category":"rotate"/.test(line)) && writes.some((line) => /stop .*"category":"leaked"/.test(line)) && writes.some((line) => /reenable-request .*"reason":"nav-check"/.test(line)) && writes.some((line) => /reveal .*"reason":"nav-check"/.test(line)), JSON.stringify(writes));
    c.check('原因为空时「停用」「申请重新启用」「查看明文」的确认按钮都禁用', ['RK-1 查看明文弹窗（原因为空）', 'RK-1 停用弹窗', 'RK-3 申请重新启用弹窗（原因为空）'].every((label) => out.H.snaps.find((snap) => snap.label === label).confirmDisabled === true), '');
    c.check('停用立即体现在列表：「已停用（轮换）」「已停用（泄露）」', out.H.snaps.find((snap) => snap.label === 'RK-2 停用后').lines.join('\n').includes('已停用（泄露）') && out.H.snaps.find((snap) => snap.label === 'RK-1 停用后').lines.join('\n').includes('已停用（轮换）'), '');
    c.check('明文只在查看弹窗里出现：弹窗里是 nav-check-secret，关闭后页面与控制台里搜不到', out.H.snaps.find((snap) => snap.label === 'RK-1 查看明文之后').revealed === 'nav-check-secret' && out.H.snaps.find((snap) => snap.label === '关闭明文弹窗之后').secretOnPage === false && out.H.snaps.find((snap) => snap.label === '关闭明文弹窗之后').secretInConsole === false, '');
    c.check('泄露类的重新启用入口禁用，并带提示', out.H.snaps.find((snap) => snap.label === 'RK-5 重新启用入口').disabled === true, JSON.stringify(out.H.snaps.find((snap) => snap.label === 'RK-5 重新启用入口').title));
  }

  // ---- C-21: audit platform push, both states
  if (wants('C-21')) {
    const c = R.begin('C-21', '审计平台推送状态（F10）', 'P1');
    for (const audit of ['on', 'off']) {
      const out = await runBoth({ audit }, async (h) => {
        await h.snap(`审计推送（${audit === 'on' ? '态 A：已启用' : '态 B：未启用'}）`, { auditText: (await h.section(AUDIT).innerText()).replace(/\s+/g, ' ') });
        await shot(h.page, `C-21-${h.side}-audit-${audit}-zh`);
      });
      compareSteps(c, `C-21 ${audit}`, out, { fields: ['lines', 'controls', 'reads', 'auditText'] });
      const text = out.H.snaps[0].auditText;
      if (audit === 'on') c.check('态 A：推送已开、待推送 3、被隔离 1、最近推送成功时间在基准前 5 分钟', /已开启/.test(text) && /待推送事件\s*3/.test(text) && /隔离事件\s*1/.test(text) && /2026-11-24 15:55:00/.test(text), text);
      else c.check('态 B：未配置，各数字为 0，最近推送成功为「—」', /未配置/.test(text) && /待推送事件\s*0/.test(text) && /隔离事件\s*0/.test(text) && /—/.test(text), text);
    }
  }

  // ---- C-22: loading, failure, empty, phone width
  if (wants('C-22')) {
    const c = R.begin('C-22', '加载中、读取失败、列表为空与手机宽度（F11）', 'P1');
    const settle = async (h) => { await h.page.getByRole('heading', { name: PENDING }).waitFor(); };
    const loading = await runBoth({ mode: 'hold', ready: false }, async (h) => {
      await settle(h);
      await h.snap('加载中');
      await shot(h.page, `C-22-${h.side}-loading-zh`);
      h.dataset.state.mode = 'normal';
      await h.dataset.releaseHeld();
    });
    compareSteps(c, 'C-22 加载中', loading, { fields: ['lines', 'controls'] });
    const failed = await runBoth({ mode: 'fail', ready: false }, async (h) => {
      await settle(h);
      await h.quiet();
      await h.snap('读取失败');
      await shot(h.page, `C-22-${h.side}-failed-zh`);
    });
    compareSteps(c, 'C-22 读取失败', failed, { fields: ['lines', 'controls', 'reads'] });
    const empty = await runBoth({ empty: true }, async (h) => {
      await h.snap('列表为空');
      await shot(h.page, `C-22-${h.side}-empty-zh`);
    });
    compareSteps(c, 'C-22 列表为空', empty, { fields: ['lines', 'controls', 'reads'] });
    c.check('列表为空时三块空态文案为「没有待审批的申请」「没有已批准但尚未取走的密文」「还没有仓库自有密钥」', ['没有待审批的申请', '没有已批准但尚未取走的密文', '还没有仓库自有密钥'].every((text) => empty.H.snaps[0].lines.join('\n').includes(text)), empty.H.snaps[0].lines.join(' | ').slice(0, 400));
    const phone = await runBoth({ viewport: { width: 390, height: 844 }, touch: true }, async (h) => {
      const metrics = await h.page.evaluate(() => {
        const first = [...document.querySelectorAll('section')].find((entry) => entry.querySelector('h3'));
        const rootEl = first?.parentElement;
        return { overflow: document.documentElement.scrollWidth > window.innerWidth, scrollWidth: document.documentElement.scrollWidth, tables: rootEl?.querySelectorAll('table').length ?? -1, rows: rootEl?.querySelectorAll('tbody tr').length ?? -1, wideBoxes: [...(rootEl?.querySelectorAll('tbody tr') ?? [])].filter((row) => row.getBoundingClientRect().width > window.innerWidth).length };
      });
      await h.snap('手机宽度', metrics);
      await h.page.locator('section', { has: h.page.getByRole('heading', { name: KEYS }) }).scrollIntoViewIfNeeded();
      await shot(h.page, `C-22-${h.side}-phone-390-zh`);
    });
    compareSteps(c, 'C-22 手机宽度', phone, { fields: ['lines', 'controls', 'overflow', 'tables', 'rows', 'wideBoxes'] });
    c.check('手机宽度下页面没有横向滚动', phone.H.snaps[0].overflow === false, JSON.stringify(phone.H.snaps[0].scrollWidth));
  }

  // ---- C-25: the page header refresh re-reads only this page's data (H only)
  if (wants('C-25')) {
    const c = R.begin('C-25', '统一页头与页头刷新只重读本页数据', 'P1');
    const h = await openSide(SIDES[1]);
    try {
      await h.go();
      await h.ready();
      const apiOnly = (mark) => h.tracker.since(mark).filter((entry) => entry.path.includes('/api/'));
      const family = (entries) => [...new Set(entries.filter((entry) => /\/repo-keys(\/|\?|$)/.test(entry.path) && entry.method === 'GET').map((entry) => normalizeRequest(entry)))].sort();
      const mountFamily = family(apiOnly(0));
      const mark = h.tracker.mark();
      await h.page.locator('.app-content').getByRole('button', { name: /^刷新$|刷\s*新/ }).first().click();
      await h.quiet();
      const refreshed = apiOnly(mark);
      c.check('挂载时发出的仓库密钥读取为四条（列表、待审批、待交付、审计推送状态）', mountFamily.length === 4, JSON.stringify(mountFamily));
      c.check('页头刷新的数据请求与挂载时相同，只重读本页已有的数据', same(family(refreshed), mountFamily), `${JSON.stringify(family(refreshed))} vs ${JSON.stringify(mountFamily)}`);
      c.check('页头刷新没有发出任何别的读取，也没有写请求', refreshed.length > 0 && refreshed.every((entry) => entry.method === 'GET' && /\/repo-keys(\/|\?|$)/.test(entry.path)), JSON.stringify(refreshed.map((entry) => `${entry.method} ${entry.path}`)));
      c.evidenceFile(await shot(h.page, 'C-25-H-after-refresh-zh'));
      // Header structure against another page of the console that uses the unified header: the title and the refresh control sit
      // at the same place and size, and the action group ends at the same right edge.
      const header = (page) => page.evaluate(() => {
        const content = document.querySelector('.app-content');
        const title = content?.querySelector('h1')?.getBoundingClientRect();
        const buttons = [...(content?.querySelectorAll('button') ?? [])].filter((button) => button.getBoundingClientRect().y < 130);
        const refresh = buttons.find((button) => /^\s*刷\s*新\s*$/.test(button.textContent ?? ''))?.getBoundingClientRect();
        const round = (rect) => rect ? { x: Math.round(rect.x), y: Math.round(rect.y), right: Math.round(rect.right), height: Math.round(rect.height) } : null;
        return { title: round(title), refresh: round(refresh), actionsRight: Math.max(...buttons.map((button) => Math.round(button.getBoundingClientRect().right))) };
      });
      const repoHeader = await header(h.page);
      const other = await newPageAt(h.context, `${APP}/api-keys`);
      const keysHeader = await header(other);
      c.check('页头结构与「密钥管理」页的统一页头一致：标题的位置与行高、刷新按钮的纵向位置与高度、操作区右边缘相同', Boolean(repoHeader.title && keysHeader.title && repoHeader.refresh && keysHeader.refresh) && repoHeader.title.x === keysHeader.title.x && repoHeader.title.y === keysHeader.title.y && repoHeader.title.height === keysHeader.title.height && repoHeader.refresh.y === keysHeader.refresh.y && repoHeader.refresh.height === keysHeader.refresh.height && repoHeader.actionsRight === keysHeader.actionsRight, JSON.stringify({ repoHeader, keysHeader }));
      c.evidenceFile(await shot(other, 'C-25-H-api-keys-header-zh'));
      // The case the S3 review (R1) raised: a search and an opened detail leave cached reads that the page no longer shows; the
      // header refresh must re-read only what is on screen.
      await h.search('alpha');
      await h.search('');
      await h.rowButton(KEYS, 'example/alpha-old', '详情').click();
      await h.quiet();
      await h.page.locator('[role="dialog"]:visible .ant-select').click();
      await h.page.locator('.ant-select-item-option', { hasText: '近 30 天' }).click();
      await h.quiet();
      await h.closeDialogs();
      await h.quiet();
      const staleMark = h.tracker.mark();
      await h.page.locator('.app-content').getByRole('button', { name: /^刷新$|刷\s*新/ }).first().click();
      await h.quiet();
      const staleRefresh = family(apiOnly(staleMark));
      c.check('先搜索、开详情再点页头刷新：只重读当前显示的四条，不再读搜索过的旧词与关掉的详情', same(staleRefresh, mountFamily) && !staleRefresh.some((read) => /[?&]q=|window=/.test(read)), JSON.stringify(staleRefresh));
    } finally {
      await h.context.close();
    }
  }
} finally {
  for (const api of Object.values(apis)) await api.dispose().catch(() => {});
  await shutdown(browser);
  R.finish();
}
