import fs from 'node:fs/promises';
import { createWarmupFixture } from '../warmup-fixture.mjs';
import { fulfillFixture } from '../browser-guard.mjs';
import { settleLayout, until } from '../harness.mjs';
import { probeLending } from '../lending-fixtures.mjs';

const KNOWN_COOLDOWN_REASON = JSON.stringify({ error: { code: 'credential_quota', message: 'Fixture quota exhausted' } });

const files = Array.from({ length: 12 }, (_, index) => {
  // auth-12 is the reset-credit credential, and it is Codex: the redemption action exists
  // only there, so a fixture that borrowed another provider would prove nothing.
  const provider = index === 11 ? 'codex' : ['codex', 'claude', 'kimi', 'xai'][index % 4];
  return {
    name: `credential-${String(index + 1).padStart(2, '0')}.json`,
    auth_index: `auth-${String(index + 1).padStart(2, '0')}`,
    type: provider,
    provider,
    disabled: false,
    unavailable: false,
    runtime_only: false,
    email: `operator-${index + 1}@example.test`,
    success: 20 - index,
    failed: index % 3,
    priority: index === 0 ? 0 : 1,
    weight: 1,
    note: index % 2 === 0 ? 'Production credential' : undefined,
  };
});

function quotaFor(index) {
  const sixWindows = index === 3;
  const windows = Array.from({ length: sixWindows ? 6 : 2 }, (_, windowIndex) => ({
    id: `auth-${String(index + 1).padStart(2, '0')}-window-${windowIndex}`,
    label: sixWindows ? `Model group ${Math.floor(windowIndex / 2) + 1} · Window ${windowIndex % 2 + 1}` : `Window ${windowIndex + 1}`,
    // The grouped provider answers with a period rather than a kind, which is what the row
    // has to read; the flat records keep the explicit kind.
    kind: sixWindows ? undefined : (windowIndex === 0 ? 'five_hour' : 'weekly'),
    period_hours: windowIndex % 2 === 0 ? 5 : 168,
    scope: sixWindows ? 'group' : 'standard',
    used_percent: 20 + windowIndex * 5,
    remaining_percent: 80 - windowIndex * 5,
    reset_at_ms: Date.now() + (windowIndex + 1) * 3_600_000,
    // Only the first credential carries an estimate, so the record beside it is the same
    // row without one. The figures are four-digit dollars: the widest reading a row has to fit.
    ...(index === 0 ? {
      usage: { from_ms: Date.now() - 3_600_000, to_ms: Date.now(), requests: 420, priced_requests: 420, tokens: 61_250_000, cost_nanos: 308_500_000_000 },
      ...(windowIndex === 1 ? { capacity_unavailable: 'low_usage' } : {}),
      capacity: { tokens: 245_000_000, cost_nanos: 1_234_000_000_000, error_percent: 2.5, basis: windowIndex === 0 ? 'current_cycle' : 'previous_cycle', observed_at_ms: Date.now() - 86_400_000 },
    } : index === 1 ? { capacity_unavailable: 'scope_unknown' } : {}),
  }));
  const cooldown = index === 8 || index === 9;
  const unsupported = index === 10;
  const credits = index === 11;
  return {
    auth_index: files[index].auth_index,
    name: files[index].name,
    type: files[index].type,
    provider: files[index].provider,
    disabled: false,
    status: cooldown ? 'cooldown' : unsupported ? 'idle' : 'healthy',
    observed_at_ms: Date.now(),
    plan: { plan_type: 'pro', plan_label: 'Pro', tier: 'premium', ...(index === 3 ? { subscription_active: false } : {}) },
    windows,
    active_cooldown: cooldown ? { is_active: true, reason: index === 9 ? KNOWN_COOLDOWN_REASON : 'Fixture unexpected upstream cooldown', recover_at_ms: Date.now() + 900_000 } : undefined,
    // The bank holds two credits while upstream considers none of them applicable right
    // now. That combination is exactly the reported defect: the action must still exist.
    reset_credits: credits ? { available_count: 2, applicable_available_count: 0 } : undefined,
    // auth-06 is a Claude credential holding unspent grants beside a spent one that ends
    // sooner; auth-02 holds only a spent grant, which must leave its row without a badge.
    ...(index === 5 ? {
      reset_grants: [
        { id: 'grant-live-late', resets_total: 2, resets_left: 2, ends_at_ms: Date.now() + 6 * 86_400_000 },
        { id: 'grant-live-soon', resets_total: 1, resets_left: 1, ends_at_ms: Date.now() + 2 * 86_400_000 + 1_800_000 },
        { id: 'grant-spent', resets_total: 1, resets_left: 0, ends_at_ms: Date.now() + 3_600_000 },
      ],
    } : index === 1 ? {
      reset_grants: [{ id: 'grant-spent', resets_total: 1, resets_left: 0, ends_at_ms: Date.now() + 86_400_000 }],
    } : {}),
    recommendation: { status: cooldown ? 'cooldown' : 'healthy', priority: cooldown ? 'high' : 'none', action: cooldown ? 'clear_cooldown' : 'none', reason: '' },
    capabilities: {
      refresh_supported: !unsupported,
      clear_cooldown_supported: cooldown,
      reset_credit_supported: credits,
    },
    ...(index === 9 ? { error: 'Fixture quota refresh failed' } : {}),
    quota_exceeded: false,
  };
}

const quota = Array.from({ length: files.length }, (_, index) => quotaFor(index));

export async function oauthManagement({ base, page, check }) {
  const oauthStarts = [];
  const redeemPosts = [];
  page.on('request', (request) => {
    if (request.url().includes('/api/v1/management/oauth/start')) oauthStarts.push(request.url());
    // Redemption spends a real entitlement, so the probe records every request rather than
    // letting the mocked network hide an action that fired without confirmation.
    if (request.method() === 'POST' && request.url().includes('/management/quota/redeem-credit')) redeemPosts.push(request.url());
  });

  await page.goto(`${base}/oauth-management?density=compact`, { waitUntil: 'domcontentloaded' });
  await page.locator('[data-testid="oauth-credential-record"]').first().waitFor({ state: 'visible', timeout: 20_000 });
  const providerTabs = await page.locator('.oauth-management-page .ant-tabs-tab').allInnerTexts();
  check(
    'provider strip has one All tab and no authorization-only Anthropic duplicate',
    providerTabs.filter((label) => /^All\b/.test(label.trim())).length === 1
      && providerTabs.some((label) => /^Claude\b/.test(label.trim()))
      && !providerTabs.some((label) => /^Anthropic\b/.test(label.trim())),
    providerTabs.join(' | '),
  );


  check('the collection presents a single overview with no density switch', (await page.locator('.oauth-management-page .ant-segmented').count()) === 0);
  const compactVisible = await page.evaluate(() => {
    const rows = [...document.querySelectorAll('[data-testid="oauth-credential-record"]')];
    return rows.filter((row) => {
      const rect = row.getBoundingClientRect();
      return rect.top >= 0 && rect.bottom <= window.innerHeight && rect.width > 0;
    }).length;
  });
  console.log(`OAuth density: ${page.viewportSize().width}x${page.viewportSize().height}, compact=${compactVisible}`);
  await page.screenshot({ path: 'tmp/oauth-management-compact-desktop.png' });
  check(
    'oauth management shows at least six complete records at 1440x900',
    compactVisible >= 6,
    `visible=${compactVisible}`,
  );

  const firstRecord = page.getByTestId('oauth-credential-record').first();
  check('explicit zero priority and default weight remain visible',
    (await firstRecord.innerText()).includes('Priority 0') && (await firstRecord.innerText()).includes('Weight 1'));

  // The row answers "can this credential serve the next request" without a click, so the
  // credential's own family carries both bars, and its way into the rest is one Details link.
  const twoWindowRow = page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-01"]');
  check(
    'a compact record bars its five-hour and weekly windows',
    (await twoWindowRow.locator('[data-quota-compact-window="five_hour"]').count()) === 1
      && (await twoWindowRow.locator('[data-quota-compact-window="weekly"]').count()) === 1
      && (await twoWindowRow.locator('[data-quota-compact-window] .meter').count()) === 2,
    (await twoWindowRow.innerText()).replace(/\n/g, ' | '),
  );
  const plainRow = page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-02"]');
  const compactHeight = (row) => row.locator('[data-quota-density="compact"]').evaluate((body) => body.getBoundingClientRect().height);
  const [estimatedHeight, plainHeight] = [await compactHeight(twoWindowRow), await compactHeight(plainRow)];
  const compactEstimates = await twoWindowRow.locator('[data-quota-compact-capacity]').evaluateAll((cells) => cells.map((cell) => ({
    text: cell.textContent,
    clipped: cell.scrollWidth > cell.clientWidth + 1,
  })));
  check(
    'a compact record carries each window\'s estimated capacity without growing or clipping it',
    compactEstimates.length === 2
      && compactEstimates.every((cell, index) => cell.text === (index === 0 ? '≈$1,230' : 'Prev ≈$1,230') && !cell.clipped)
      && Math.abs(estimatedHeight - plainHeight) <= 0.5
      && (await plainRow.locator('[data-quota-compact-capacity]').count()) === 0,
    `estimates=${JSON.stringify(compactEstimates)} heights=${estimatedHeight}/${plainHeight}`,
  );
  check(
    'a compact record reaches the full reading through its own Details action',
    (await twoWindowRow.getByRole('button', { name: /^(Details|详情):/i }).count()) === 1,
  );

  const groupRecord = page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-04"]');
  const groupLabels = await groupRecord
    .locator('[data-quota-compact-window]')
    .evaluateAll((cells) => cells.map((cell) => cell.getAttribute('data-quota-window-label') ?? ''));
  check(
    'a compact record never shows a model family it does not belong to',
    groupLabels.length === 2
      && groupLabels.every((label) => label.startsWith('Model group 1'))
      && !groupLabels.some((label) => label.includes('Model group 2') || label.includes('Model group 3')),
    groupLabels.join(' | '),
  );
  check('an explicitly inactive subscription is visible in its compact reading', await groupRecord.locator('[data-subscription-inactive]').isVisible());
  await groupRecord.getByRole('button', { name: /^(Details|详情):/i }).click();
  const detailPanel = page.locator('.ant-drawer-open');
  await detailPanel.locator('[data-quota-density="expanded"]').waitFor();
  check('an explicitly inactive subscription is visible in credential details', await detailPanel.locator('[data-subscription-inactive]').isVisible());
  check('quota tab shows every model group without unrelated configuration',
    (await detailPanel.locator('.meter').count()) === 6
      && (await detailPanel.innerText()).includes('Model group 3 · Window 2')
      && !(await detailPanel.locator('#note').isVisible()));
  const labels = await detailPanel.locator('[class*=progress-label-row]').allInnerTexts();
  check('quota windows stay grouped in source order', labels[0]?.includes('Model group 1') && labels[1]?.includes('Model group 1') && labels[2]?.includes('Model group 2'), labels.join(' | '));
  await settleLayout(page);
  await page.screenshot({ path: 'tmp/oauth-management-quota-drawer.png' });
  await detailPanel.getByRole('tab', { name: 'Configuration', exact: true }).click();
  await detailPanel.locator('#note').fill('Keep this draft while checking quota');
  await detailPanel.getByRole('button', { name: 'Add mapping', exact: true }).click();
  await detailPanel.getByRole('textbox', { name: 'Upstream model 1', exact: true }).fill('gpt-5');
  await detailPanel.getByRole('textbox', { name: 'Client alias 1', exact: true }).fill('credential-gpt');
  await detailPanel.getByRole('textbox', { name: 'Display name 1', exact: true }).fill('Credential GPT');
  await detailPanel.getByRole('checkbox', { name: 'Force mapping', exact: true }).check();
  const aliasEntry = detailPanel.locator('li').filter({ has: page.getByRole('textbox', { name: 'Client alias 1', exact: true }) });
  await aliasEntry.scrollIntoViewIfNeeded();
  await settleLayout(page);
  await page.screenshot({ path: 'tmp/oauth-credential-aliases-desktop.png' });
  const desktopViewport = page.viewportSize();
  await page.setViewportSize({ width: 390, height: 844 });
  await settleLayout(page);
  await aliasEntry.scrollIntoViewIfNeeded();
  check('credential alias fields stay inside the phone drawer', await aliasEntry.evaluate((node) => node.scrollWidth <= node.clientWidth && [...node.querySelectorAll('input')].every((input) => input.getBoundingClientRect().right <= window.innerWidth)));
  await page.screenshot({ path: 'tmp/oauth-credential-aliases-phone.png' });
  await page.setViewportSize(desktopViewport);
  await settleLayout(page);
  await detailPanel.getByRole('tab', { name: 'Quota', exact: true }).click();
  check('configuration fields stay outside the quota tab', !(await detailPanel.locator('#note').isVisible()));
  await detailPanel.getByRole('tab', { name: /Configuration/ }).click();
  check('switching tabs preserves an unsaved configuration draft',
    (await detailPanel.locator('#note').inputValue()) === 'Keep this draft while checking quota');
  check('credential aliases share the guarded configuration draft',
    await detailPanel.getByRole('textbox', { name: 'Client alias 1', exact: true }).inputValue() === 'credential-gpt'
      && await detailPanel.getByRole('checkbox', { name: 'Force mapping', exact: true }).isChecked());
  await detailPanel.getByRole('tab', { name: 'Quota', exact: true }).click();
  await detailPanel.locator('.ant-drawer-close').click();
  const discard = page.locator('.ant-modal-confirm');
  await discard.waitFor();
  check('closing from quota still guards a dirty configuration tab', await discard.isVisible());
  await discard.getByRole('button', { name: /Confirm/ }).click();
  await detailPanel.waitFor({ state: 'hidden' });

  const unsupportedRow = page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-11"]');
  check(
    'an unsupported live probe still renders its observed primary window',
    (await unsupportedRow.locator('[data-quota-unsupported="true"]').count()) > 0
      && (await unsupportedRow.getByText('80%').count()) > 0,
    `unsupported=${await unsupportedRow.locator('[data-quota-unsupported="true"]').count()}`,
  );
  const cooldownRow = page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-10"]');
  check('a known credential_quota reason is consolidated into the CPA cooldown status',
    await cooldownRow.getByText('CPA Cooldown', { exact: true }).isVisible()
      && !(await cooldownRow.innerText()).includes('credential_quota')
      && await cooldownRow.locator('[class*="rec-banner-danger"]').count() === 0);
  await cooldownRow.getByText('CPA Cooldown', { exact: true }).hover();
  const cooldownTooltip = page.getByRole('tooltip').filter({ hasText: 'CPA 429 rate limit protection active' });
  await cooldownTooltip.waitFor({ state: 'visible' });
  check('the consolidated cooldown status explains the known condition', (await cooldownTooltip.innerText()).includes('CPA 429 rate limit protection active'));
  await cooldownRow.getByRole('button', { name: /^(Details|详情):/i }).click();
  await detailPanel.locator('[data-quota-density="expanded"]').waitFor();
  check('the quota drawer retains raw cooldown evidence and refresh failure diagnostics',
    (await detailPanel.innerText()).includes(KNOWN_COOLDOWN_REASON)
      && (await detailPanel.innerText()).includes('Fixture quota refresh failed'));
  await detailPanel.locator('.ant-drawer-close').click();
  await detailPanel.waitFor({ state: 'hidden' });
  const unexpectedCooldownRow = page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-09"]');
  check('an unexpected cooldown reason remains readable in the compact row',
    await unexpectedCooldownRow.getByText('Fixture unexpected upstream cooldown', { exact: true }).isVisible());
  await cooldownRow.getByRole('button', { name: /More actions|更多操作/i }).click();
  const cooldownItem = page.getByRole('menuitem', { name: /Clear Cooldown|清除冷却/i });
  check('an active cooldown stays actionable through the row menu', await cooldownItem.isEnabled());
  await page.keyboard.press('Escape');

  const grantRow = page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-06"]');
  const grantBadge = grantRow.locator('[data-reset-grants]');
  const grantBadgeText = (await grantBadge.count()) === 1 ? await grantBadge.innerText() : '';
  check(
    'a Claude row reports its unspent grant resets and the soonest live expiry',
    /Available 3/.test(grantBadgeText) && /Earliest: 2d( \dh)? left/.test(grantBadgeText),
    grantBadgeText,
  );
  check(
    'a Claude row whose grants are all spent carries no reset badge',
    (await plainRow.locator('[data-reset-grants]').count()) === 0,
  );
  await grantRow.scrollIntoViewIfNeeded();
  await settleLayout(page);
  await grantRow.screenshot({ path: 'tmp/oauth-management-claude-grant-row.png' });

  const creditRow = page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-12"]');
  check(
    'a row reports the banked reset credits it holds',
    (await creditRow.getByText(/Available 2|可用 2 个/).count()) > 0,
  );
  check(
    'the row itself offers no reset-credit action',
    (await creditRow.getByRole('button', { name: /Reset quota|重置额度/i }).count()) === 0,
    'spending a credit is irreversible and belongs in the Drawer beside the expiries it consumes',
  );
  await creditRow.getByRole('button', { name: /More actions|更多操作/i }).click();
  check(
    'the row menu offers no reset-credit action either',
    (await page.locator('.ant-dropdown-menu').getByRole('menuitem', { name: /Reset quota|重置额度/i }).count()) === 0,
  );
  await page.keyboard.press('Escape');

  await creditRow.getByRole('button', { name: /^Details:/ }).click();
  await page.locator('.ant-drawer-open [data-quota-density="expanded"]').waitFor();
  const drawerRedeem = page.locator('.ant-drawer-open').getByRole('button', { name: /Reset quota|重置额度/i });
  check('the Drawer carries the reset-credit action', (await drawerRedeem.count()) === 1);

  await drawerRedeem.click();
  const redeemConfirm = page.locator('.ant-popconfirm');
  await redeemConfirm.waitFor();
  check('the reset action asks for confirmation before spending a credit', await redeemConfirm.isVisible());
  await redeemConfirm.getByRole('button', { name: /Cancel|取消/i }).click();
  await redeemConfirm.waitFor({ state: 'hidden' });
  check('cancelling spends nothing', redeemPosts.length === 0, `posts=${redeemPosts.length}`);

  await drawerRedeem.click();
  const confirmed = page.locator('.ant-popconfirm');
  await confirmed.waitFor();
  await confirmed.getByRole('button', { name: /Confirm|确定/i }).click();
  await page.getByText(/Credit redeemed successfully|积分重置成功/).waitFor({ timeout: 10_000 });
  check('confirming in the Drawer issues exactly one redemption request', redeemPosts.length === 1, `posts=${redeemPosts.length}`);

  await page.locator('.ant-drawer-open .ant-drawer-close').click();
  await page.locator('.ant-drawer-open').waitFor({ state: 'hidden' });

  await page.goto(`${base}/oauth-management`, { waitUntil: 'domcontentloaded' });
  await page.locator('[data-testid="oauth-credential-record"]').first().waitFor({ state: 'visible', timeout: 20_000 });
  await page.getByRole('button', { name: /OAuth sign-in/i }).click();
  await page.locator('[data-testid="oauth-connect-panel"]').waitFor({ state: 'visible', timeout: 5000 });
  await settleLayout(page);
  const picker = page.locator('#oauth-connect-provider');
  await picker.click();
  await page.getByTitle('Codex OAuth', { exact: true }).last().click();
  check('selecting a Connect provider makes no authorization request', oauthStarts.length === 0, `starts=${oauthStarts.length}`);
  await page.locator('[data-oauth-start="codex"]').click();
  await until(() => oauthStarts.length > 0, { label: 'the explicit OAuth start request' });
  check('Start authorization issues exactly one request', oauthStarts.length === 1, `starts=${oauthStarts.length}`);
  await page.getByRole('button', { name: 'Cancel Authorization', exact: true }).waitFor({ state: 'visible' });
  // Minimize the active session through the drawer's explicit dismissal control.
  await page.locator('.ant-drawer-open .ant-drawer-close').click();
  await page.locator('.ant-drawer-open').waitFor({ state: 'hidden', timeout: 5000 });
  check(
    'a minimized authorization remains visible in the workspace strip',
    (await page.getByTestId('oauth-session-pill').count()) > 0,
    `sessions=${await page.getByTestId('oauth-session-pill').count()}`,
  );

  await page.goto(`${base}/oauth?provider=codex`, { waitUntil: 'domcontentloaded' });
  await page.waitForURL('**/oauth-management**', { timeout: 10_000 });
  await page.locator('[data-testid="oauth-connect-panel"]').waitFor({ state: 'visible', timeout: 10_000 });
  check(
    'old provider-specific sign-in URL opens Connect without an automatic start',
    oauthStarts.length === 1,
    `starts=${oauthStarts.length}`,
  );
  await page.keyboard.press('Escape');

  // A status read that never reaches CPA must not end the attempt: the operator may
  // still be completing sign-in, and a terminal panel offers only a Retry that opens a
  // second upstream session. These fixtures answer this attempt's first status read
  // with 502 and its second with success, so the poll armed before the failure has to
  // land the completion on its own.
  const startsBeforeBlip = oauthStarts.length;
  await page.getByRole('button', { name: /OAuth sign-in|OAuth 登录/i }).click();
  const blipPanel = page.locator('[data-testid="oauth-connect-panel"]');
  await blipPanel.waitFor({ state: 'visible', timeout: 10_000 });
  // The panel keeps the provider a previous block selected, so the provider is chosen
  // through the same selector the operator uses rather than through the tile grid.
  await blipPanel.locator('#oauth-connect-provider').click();
  await page.getByTitle('Meta Muse OAuth', { exact: true }).last().click();
  await page.locator('[data-oauth-start="meta"]').click();
  await blipPanel.locator('[data-oauth-user-code]').waitFor({ state: 'visible', timeout: 10_000 });
  check(
    'the device attempt opens exactly one authorization session',
    oauthStarts.length === startsBeforeBlip + 1,
    `starts=${oauthStarts.length - startsBeforeBlip}`,
  );

  await blipPanel.getByRole('button', { name: /Check Authorization Status|检查授权状态/i }).click();
  await blipPanel.locator('[data-oauth-status-read-failure]').waitFor({ state: 'visible', timeout: 10_000 });
  const cancelWhileUnread = await blipPanel.getByRole('button', { name: /Cancel Authorization|取消授权/i }).count();
  const retryWhileUnread = await blipPanel.getByRole('button', { name: /^Retry$|^重试$/ }).count();
  check(
    'a status read that fails before CPA answers leaves the attempt waiting and cancellable',
    cancelWhileUnread === 1 && retryWhileUnread === 0,
    `cancel=${cancelWhileUnread} retry=${retryWhileUnread}`,
  );

  const blipCompleted = await blipPanel
    .getByRole('button', { name: /Sign in another account|登录其他账号/i })
    .waitFor({ state: 'visible', timeout: 15_000 })
    .then(() => true)
    .catch(() => false);
  check(
    'the poll armed before the failed read still completes the attempt',
    blipCompleted,
    `completed=${blipCompleted}`,
  );
  const completionToast = page.locator('.omc-toast').filter({
    has: page.getByRole('button', { name: /View credentials|查看凭证/i }),
  });
  await completionToast.waitFor({ state: 'visible', timeout: 10_000 });
  check(
    'workspace completion reports once through its credential-aware toast',
    await page.locator('.omc-toast').count() === 1,
    `toasts=${await page.locator('.omc-toast').count()}`,
  );
  await page.keyboard.press('Escape');

  for (const width of [375, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await page.goto(`${base}/oauth-management?density=compact`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="oauth-credential-record"]').first().waitFor({ state: 'visible', timeout: 20_000 });
    const layout = await page.evaluate(() => {
      const controls = [...document.querySelectorAll('[data-testid="oauth-credential-record"] button, [data-testid="oauth-credential-record"] input')];
      const outside = controls.filter((control) => {
        const rect = control.getBoundingClientRect();
        return rect.width > 0 && (rect.left < -1 || rect.right > window.innerWidth + 1);
      }).length;
      return {
        overflow: Math.max(0, document.documentElement.scrollWidth - window.innerWidth),
        controls: controls.length,
        outside,
      };
    });
    check(
      `oauth management reflows without horizontal overflow at ${width}px`,
      layout.overflow === 0 && layout.controls > 0 && layout.outside === 0,
      JSON.stringify(layout),
    );
    const phoneEstimates = await page.locator('[data-auth-index="auth-01"] [data-quota-compact-capacity]').evaluateAll((cells) => cells.map((cell) => {
      const rect = cell.getBoundingClientRect();
      return rect.width > 0 && rect.left >= 0 && rect.right <= window.innerWidth + 1 && cell.scrollWidth <= cell.clientWidth + 1;
    }));
    check(
      `a phone row shows each estimated capacity whole at ${width}px`,
      phoneEstimates.length === 2 && phoneEstimates.every(Boolean),
      JSON.stringify(phoneEstimates),
    );
    await page.screenshot({ path: `tmp/oauth-management-overview-${width}.png` });
    await page.getByTestId('oauth-credential-record').first().getByRole('button', { name: /^Details:/ }).click();
    const phoneDrawer = page.locator('.ant-drawer-open');
    await phoneDrawer.getByRole('tab', { name: 'Quota', exact: true }).waitFor();
    await settleLayout(page);
    const drawerOverflow = await phoneDrawer.evaluate((drawer) => drawer.querySelector('.ant-drawer-body').scrollWidth - drawer.querySelector('.ant-drawer-body').clientWidth);
    check(`tabbed quota drawer has no horizontal overflow at ${width}px`, drawerOverflow <= 1, `overflow=${drawerOverflow}`);
    // Recorded usage and the estimate wrap onto their own lines here; neither may leave the panel.
    const drawerEstimates = await phoneDrawer.evaluate((drawer) => {
      const panel = drawer.querySelector('.ant-drawer-body').getBoundingClientRect();
      return [...drawer.querySelectorAll('[data-quota-capacity="estimated"] > span')].map((part) => {
        const rect = part.getBoundingClientRect();
        return rect.width > 0 && rect.left >= panel.left - 1 && rect.right <= panel.right + 1;
      });
    });
    check(
      `the quota drawer keeps recorded usage and the estimate inside the panel at ${width}px`,
      drawerEstimates.length === 5 && drawerEstimates.every(Boolean)
        && (await phoneDrawer.locator('[data-quota-capacity-hint]').isVisible())
        && (await phoneDrawer.locator('[data-quota-capacity-basis="previous_cycle"]').innerText()).includes('Previous cycle estimate'),
      JSON.stringify(drawerEstimates),
    );
    await page.screenshot({ path: `tmp/oauth-management-drawer-${width}.png` });
    await phoneDrawer.locator('.ant-drawer-close').click();
    await phoneDrawer.waitFor({ state: 'hidden' });
    const filterToggle = page.getByRole('button', { name: /Filters|筛选|篩選|Penapis/i }).first();
    check(
      `oauth management exposes one mobile filter disclosure at ${width}px`,
      await filterToggle.isVisible() && (await page.locator('#oauth-connect-provider').count()) === 0,
    );
    await filterToggle.click();
    check(
      `the mobile filter disclosure reveals the collection controls at ${width}px`,
      (await page.locator('.oauth-management-page .ant-select').count()) >= 3,
    );
  }

  await page.goto(`${base}/oauth-management`, { waitUntil: 'domcontentloaded' });
  await page.getByTestId('oauth-credential-record').first().waitFor();
  const filters = page.getByRole('button', { name: 'More filters', exact: true });
  if (await filters.getAttribute('aria-expanded') !== 'true') await filters.click();
  await page.getByRole('combobox', { name: 'Sort', exact: true }).click();
  const savedSort = page.waitForResponse((response) => response.url().endsWith('/preferences/oauth_management_view_v1')
    && response.request().method() === 'PUT' && response.ok());
  await page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: 'Priority (High to Low)' }).click();
  await savedSort;
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.getByRole('combobox', { name: 'Sort', exact: true }).waitFor();
  check('OAuth sort control retains its selection after reload',
    (await page.locator('#oauth-collection-filters').innerText()).includes('Priority (High to Low)'));
  await page.goto(`${base}/oauth-management`, { waitUntil: 'domcontentloaded' });
  await page.getByRole('combobox', { name: 'Sort', exact: true }).waitFor();
  await until(async () => (await page.getByTestId('oauth-credential-record').first().getAttribute('data-auth-index')) === 'auth-02',
    { label: 'saved priority sort after reload' });
  check('OAuth priority sorting survives a fresh entry and orders the collection',
    (await page.locator('#oauth-collection-filters').innerText()).includes('Priority (High to Low)')
      && (await page.getByTestId('oauth-credential-record').first().getAttribute('data-auth-index')) === 'auth-02');
  await checkResetAnnouncementLayout(page, check);
}

async function checkResetAnnouncementLayout(page, check) {
  const notices = [];
  const now = Date.now();
  const localAdvice = {
    verdict: 'keep_for_emergency', evidence: 'projection', reasons: ['quota_near_exhaustion'],
    remaining_resets: 2, earliest_expiry_ms: now + 120 * 3_600_000, used_percent: 95,
    reset_observed_at_ms: now, quota_observed_at_ms: now,
    local_evidence: { source: 'local_observation', verdict: 'keep_for_emergency', evidence: 'projection', reasons: ['quota_near_exhaustion'], quota_reset_at_ms: now + 3_600_000 },
    announcements: [],
  };
  const quotaHandler = async route => {
    const advice = notices.length ? {
      ...localAdvice, verdict: 'use_before_expiry', reasons: ['announcement_reset_soon', 'announcement_local_conflict'],
      announcements: notices.map(notice => ({ ...notice, influences_advice: true, has_conflict: true })),
    } : localAdvice;
    await route.fulfill({ json: { quotas: quota.map(item => item.auth_index === 'auth-01' ? { ...item, reset_advice: advice } : item), total: quota.length, summary: {} } });
  };
  const handler = async (route) => {
    if (route.request().method() === 'POST') {
      const input = route.request().postDataJSON();
      const item = { ...input, id: 'probe-announcement', registered_at_ms: Date.now(), created_at_ms: Date.now(), status: 'active' };
      notices.push(item);
      await route.fulfill({ status: 201, json: item });
    } else { await route.fulfill({ json: { items: notices, total: notices.length } }); }
  };
  await page.route('**/management/quota/reset-announcements*', handler);
  await page.route('**/management/quota', quotaHandler);
  try {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.reload({ waitUntil: 'domcontentloaded' });
    const record = page.getByTestId('oauth-credential-record').filter({ has: page.locator('[data-quota-body="auth-01"]') });
    await record.getByText('Keep for recovery', { exact: true }).waitFor();
    await record.scrollIntoViewIfNeeded();
    await page.screenshot({ path: 'tmp/reset-advice-before-desktop.png' });
    const region = page.getByRole('region', { name: 'Active reset announcements' });
    await region.getByRole('button', { name: 'Register reset announcement' }).click();
    const dialog = page.getByRole('dialog', { name: 'Register reset announcement' });
    await dialog.waitFor();
    await dialog.getByLabel('Provider identifier').fill('codex');
    const future = new Date(now + 2 * 3_600_000);
    const localTime = future.toISOString().slice(0, 16);
    await dialog.locator('input[type="datetime-local"]').fill(localTime);
    await dialog.getByRole('combobox', { name: 'Confidence' }).click();
    await page.locator('.ant-select-dropdown:visible').getByText('Confirmed', { exact: true }).click();
    await dialog.getByLabel('Original announcement').fill('Public announcement for layout verification');
    for (const width of [1440, 320]) {
      await page.setViewportSize({ width, height: 900 });
      await settleLayout(page);
      check('announcement form fits viewport ' + width, await dialog.evaluate((element) => {
        const rect = element.getBoundingClientRect();
        return rect.left >= 0 && rect.right <= window.innerWidth && element.scrollWidth <= element.clientWidth + 1;
      }));
    }
    await dialog.getByRole('button', { name: 'Register', exact: true }).click();
    await region.getByText('Public announcement for layout verification').waitFor();
    await page.locator('.omc-toast').filter({ hasText: 'Announcement registered' }).waitFor({ state: 'hidden' });
    await region.scrollIntoViewIfNeeded();
    await settleLayout(page);
    check('announcement result fits the phone viewport', await region.evaluate((element) => element.scrollWidth <= element.clientWidth + 1));
    await record.getByText('Use before expiry', { exact: true }).waitFor();
    check('registering a notice refreshes the row advice without reload', await record.locator('[data-reset-advice="use_before_expiry"]').count() === 1);
    for (const width of [1440, 320]) {
      await page.setViewportSize({ width, height: 900 });
      await record.scrollIntoViewIfNeeded();
      await settleLayout(page);
      await page.screenshot({ path: 'tmp/reset-advice-after-' + width + '.png' });
      await record.getByRole('button', { name: /^Details:/ }).click();
      const detail = page.locator('[data-reset-advice-detail="true"]');
      await detail.getByText('Conflicting evidence', { exact: true }).waitFor();
      check('advice retains both sources at width ' + width,
        await detail.getByRole('region', { name: 'Local observation' }).count() === 1
        && await detail.getByRole('region', { name: 'Announcement evidence' }).count() === 1);
      check('advice fits viewport ' + width, await detail.evaluate(element => element.scrollWidth <= element.clientWidth + 1));
      await detail.scrollIntoViewIfNeeded();
      await page.screenshot({ path: 'tmp/reset-advice-detail-' + width + '.png' });
      await page.keyboard.press('Escape');
      await detail.waitFor({ state: 'hidden' });
    }
  } finally {
    await page.unroute('**/management/quota/reset-announcements*', handler);
    await page.unroute('**/management/quota', quotaHandler);
  }
}


async function verifyFullTokenCapacity({ base, page, check }) {
  const tokenQuota = structuredClone(quota);
  const previousWindow = tokenQuota[0].windows[1];
  previousWindow.capacity.tokens = 123_456_789_012_345;
  delete previousWindow.capacity.cost_nanos;
  const quotaPattern = '**/api/v1/management/quota';
  const quotaHandler = (route) => route.fulfill({ json: { quotas: tokenQuota, total: tokenQuota.length } });
  await page.route(quotaPattern, quotaHandler);
  const setTokenStyle = (style) => page.evaluate(async (value) => {
    const response = await fetch('/omc/api/v1/preferences/omc_token_style', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(value),
    });
    if (!response.ok) throw new Error('Token style preference write failed');
  }, style);
  try {
    await setTokenStyle('full');
    for (const width of [1440, 375, 320]) {
      await page.setViewportSize({ width, height: width === 1440 ? 900 : 844 });
      await page.goto(`${base}/oauth-management?density=compact`, { waitUntil: 'domcontentloaded' });
      const previousEstimate = page.locator('[data-auth-index="auth-01"] [data-quota-compact-window="weekly"] [data-quota-compact-capacity]');
      await until(async () => (await previousEstimate.innerText()) === 'Prev ≈123,456,789,012,345 tokens', {
        label: 'the previous-cycle token estimate in full-digit style',
      });
      // A wide record is a subgrid of the list, so its tracks are read from the list that owns them.
      await until(async () => page.getByTestId('oauth-credential-record').first().evaluate((element) => {
        const own = getComputedStyle(element).gridTemplateColumns;
        const tracks = own.startsWith('subgrid') ? getComputedStyle(element.parentElement.parentElement).gridTemplateColumns : own;
        // A phone record is two tracks: the facts, and the control that ends the line.
        return tracks.split(' ').length === (window.innerWidth <= 640 ? 2 : 6);
      }), { label: 'the credential row to adopt the target viewport' });
      await settleLayout(page);
      if (width === 1440) {
        // The header and the records share tracks only if every record does: one row with a wider
        // action cell must not move its own columns away from the header's.
        const columnEdges = await page.getByTestId('oauth-credential-record').evaluateAll((records) => {
          const edges = (row) => [...row.children].slice(0, 6).map((cell) => Math.round(cell.getBoundingClientRect().left));
          const header = records[0].parentElement.previousElementSibling;
          return { header: edges(header), records: records.map(edges) };
        });
        check(
          'every credential record starts its columns where the list header does',
          columnEdges.records.length > 0
            && columnEdges.records.every((row) => row.every((left, column) => left === columnEdges.header[column])),
          JSON.stringify(columnEdges),
        );
      }
      const geometry = await previousEstimate.evaluate((element) => {
        const bounds = element.getBoundingClientRect();
        const windowBounds = element.closest('[data-quota-compact-window]').getBoundingClientRect();
        const textRange = document.createRange();
        textRange.selectNodeContents(element);
        const fragments = [...textRange.getClientRects()];
        return {
          text: element.textContent,
          isContained: bounds.width > 0 && bounds.left >= windowBounds.left - 1 && bounds.right <= windowBounds.right + 1
            && element.scrollWidth <= element.clientWidth + 1
            && fragments.every((fragment) => fragment.left >= windowBounds.left - 1 && fragment.right <= windowBounds.right + 1),
          overflow: Math.max(0, document.documentElement.scrollWidth - window.innerWidth),
        };
      });
      check(
        `the full-digit previous-cycle token estimate stays readable within its window at ${width}px`,
        geometry.text === 'Prev ≈123,456,789,012,345 tokens' && geometry.isContained && geometry.overflow === 0,
        JSON.stringify(geometry),
      );
    }
  } finally {
    await page.unroute(quotaPattern, quotaHandler);
    await setTokenStyle('en-compact');
  }

}

async function verifyPluginConnections({ base, page, check, oauthStarts }) {
  // An auth-provider interface also serves API-key plugins. Their own page must
  // remain reachable without starting the interactive login method they reject.
  const plugins = [
    {
      id: 'key-bridge', configured: true, registered: true, enabled: true,
      effective_enabled: true, supports_oauth: true, oauth_provider: 'key-service',
      metadata: { name: 'Key Bridge' }, config_fields: [],
      pages: [{ path: '/v0/resource/plugins/key-bridge/console', label: 'Key credentials' }],
    },
    {
      id: 'interactive-auth', configured: true, registered: true, enabled: true,
      effective_enabled: true, supports_oauth: true, oauth_provider: 'interactive',
      metadata: { name: 'Interactive Auth' }, config_fields: [], pages: [],
    },
  ];
  await page.route('**/management/plugins', (route) => route.fulfill({ json: { plugins, total: plugins.length } }));
  await page.route('**/plugin-host/v0/resource/plugins/key-bridge/console', (route) => route.fulfill({
    contentType: 'text/html', body: '<!doctype html><title>Key credentials</title><h1>Import API key</h1>',
  }));
  let pluginFlow = 'device';
  await page.route('**/management/oauth/start', (route) => {
    const provider = route.request().postDataJSON().provider;
    if (provider !== 'interactive') return route.fallback();
    return route.fulfill({ json: {
      url: 'https://auth.example.test/authorize', state: 'plugin-session',
      ...(pluginFlow === 'device' ? { flow: 'device', user_code: 'PLUGIN-CODE' } : {}),
    } });
  });
  await page.setViewportSize({ width: 1440, height: 900 });
  const openPluginConnection = async (provider) => {
    await page.goto(`${base}/oauth-management`, { waitUntil: 'domcontentloaded' });
    await page.getByTestId('oauth-credential-record').first().waitFor({ state: 'visible' });
    await page.getByRole('button', { name: /OAuth sign-in/i }).click();
    await page.locator(`button[data-oauth-card="${provider}"]`).click();
    await page.locator('[data-testid="oauth-connect-panel"]').waitFor({ state: 'visible' });
  };
  await openPluginConnection('key-service');
  const pluginPanel = page.getByTestId('oauth-connect-panel');
  check('an API-key auth provider is plugin-managed rather than an OAuth redirect',
    (await pluginPanel.getByText('Plugin-managed', { exact: true }).count()) === 1
      && (await pluginPanel.getByText('Key Bridge', { exact: true }).count()) > 0
      && (await pluginPanel.getByText('Redirect callback', { exact: true }).count()) === 0
      && (await pluginPanel.locator('[data-oauth-start]').count()) === 0);
  const startsBeforePluginPage = oauthStarts.length;
  await pluginPanel.locator('[data-plugin-connect="key-service"]').click();
  await page.waitForURL('**/plugin-pages/key-bridge/0');
  await page.locator('[data-plugin-page="key-bridge"]').waitFor({ state: 'visible' });
  check('opening plugin credentials sends no OAuth start request', oauthStarts.length === startsBeforePluginPage);

  for (const flow of ['device', 'redirect']) {
    pluginFlow = flow;
    await openPluginConnection('interactive');
    check(`plugin ${flow} flow is not assumed before Start`,
      (await pluginPanel.getByText('Plugin-managed', { exact: true }).count()) === 1);
    await pluginPanel.locator('[data-oauth-start="interactive"]').click();
    if (flow === 'device') {
      await pluginPanel.locator('[data-oauth-user-code]').waitFor({ state: 'visible' });
      check('a real plugin device response exposes its code but no callback input',
        (await pluginPanel.getByText('Device code', { exact: true }).count()) > 0
          && (await pluginPanel.locator('[data-oauth-callback-input]').count()) === 0);
    } else {
      await pluginPanel.getByText('Redirect callback', { exact: true }).waitFor({ state: 'visible' });
      check('a legacy plugin login URL and state retain manual callback support',
        (await pluginPanel.locator('[data-oauth-callback-input]').count()) === 1);
    }
  }
}


async function checkOAuthModelRules({ page, base, check }) {
  await page.goto(`${base}/oauth-management`);
  await page.getByTestId('oauth-management-model-rules-open').first().waitFor();
  const writes = [];
  let aliasState = { claude: [{ name: 'claude-sonnet-4', alias: 'sonnet-latest', fork: true }] };
  let exclusionState = { codex: ['gpt-5-mini', 'gpt-4*'] };
  await page.route('**/management/auth-files/model-aliases', async (route) => {
    if (route.request().method() === 'PATCH') {
      const body = route.request().postDataJSON();
      writes.push(body);
      aliasState = { ...aliasState, [body.provider]: body.aliases };
      await route.fulfill({ json: { status: 'ok', provider: body.provider, aliases: body.aliases } });
    } else await route.fulfill({ json: { aliases: aliasState } });
  });
  await page.route('**/management/auth-files/excluded-models', async (route) => {
    if (route.request().method() === 'PATCH') {
      const body = route.request().postDataJSON();
      writes.push(body);
      exclusionState = { ...exclusionState, [body.provider]: body.models };
      await route.fulfill({ json: { status: 'ok', provider: body.provider, models: body.models } });
    } else await route.fulfill({ json: { excluded_models: exclusionState } });
  });
  const openRules = page.getByTestId('oauth-management-model-rules-open').first();
  await openRules.click();
  const drawer = page.getByTestId('oauth-model-rules-drawer');
  await drawer.locator('[data-alias-field="alias"]').waitFor({ state: 'visible' });
  const closeButton = drawer.getByRole('button', { name: 'Close', exact: true });
  const closeBounds = await closeButton.boundingBox();
  const drawerBounds = await drawer.boundingBox();
  check('model rules keep the close action at the trailing header edge',
    closeBounds.x > drawerBounds.x + drawerBounds.width / 2);
  // The row's removal control is the only way to drop one alias, so it has to
  // be inside the table's visible box before any sideways scroll.
  const readAliasRemoval = () => drawer.getByTestId('oauth-model-alias-panel').evaluate((panel) => {
    const scroller = panel.querySelector('.ant-table-content');
    const frame = scroller.getBoundingClientRect();
    const button = panel.querySelector('tbody tr:not(.ant-table-measure-row) td:last-child button').getBoundingClientRect();
    return { left: button.left, right: button.right, frameLeft: frame.left, frameRight: frame.right, isScrollable: scroller.scrollWidth > scroller.clientWidth + 1, scrollLeft: scroller.scrollLeft };
  });
  const aliasRemoval = drawer.getByTestId('oauth-model-alias-panel').locator('tbody tr:not(.ant-table-measure-row) td:last-child button');
  const desktopRemoval = await readAliasRemoval();
  check('the alias table fits the desktop drawer with its remove action in view',
    (await aliasRemoval.isVisible()) && !desktopRemoval.isScrollable && desktopRemoval.left >= desktopRemoval.frameLeft && desktopRemoval.right <= desktopRemoval.frameRight + 1,
    JSON.stringify(desktopRemoval));
  await page.screenshot({ path: 'tmp/oauth-model-aliases-desktop.png' });
  await page.setViewportSize({ width: 375, height: 844 });
  await settleLayout(page);
  const phoneRemoval = await readAliasRemoval();
  check('a phone keeps the alias remove action in view while the fields scroll',
    (await aliasRemoval.isVisible()) && phoneRemoval.isScrollable && phoneRemoval.scrollLeft === 0 && phoneRemoval.left >= phoneRemoval.frameLeft && phoneRemoval.right <= Math.min(phoneRemoval.frameRight, 375) + 1,
    JSON.stringify(phoneRemoval));
  await page.screenshot({ path: 'tmp/oauth-model-aliases-phone.png' });
  await page.setViewportSize({ width: 1440, height: 900 });
  await settleLayout(page);
  const provider = drawer.getByTestId('oauth-model-rules-provider');
  const identity = provider.getByTestId('oauth-model-rules-provider-identity');
  check('model rules use the provider tab name and mark in the selected control',
    (await identity.innerText()) === 'Claude' && (await identity.locator('img, [aria-hidden="true"]').count()) > 0
      && (await provider.getAttribute('data-provider')) === 'claude');
  await drawer.locator('[data-alias-field="alias"]').fill('sonnet-preview');
  await drawer.getByTestId('oauth-model-rules-tab-excluded').click();
  await drawer.getByTestId('oauth-excluded-models-rule-input').fill('claude-3*');
  await drawer.getByTestId('oauth-excluded-models-rule-add').click();
  await drawer.getByTestId('oauth-model-rules-tab-aliases').click();
  check('switching sections preserves the alias draft and marks both unsaved tabs',
    (await drawer.locator('[data-alias-field="alias"]').inputValue()) === 'sonnet-preview'
      && (await drawer.getByRole('img', { name: 'Unsaved changes', exact: true }).count()) === 2);
  await drawer.getByTestId('oauth-model-rules-save').click();
  await until(() => drawer.getByTestId('oauth-model-rules-save').isDisabled());
  check('saving aliases writes only that section while exclusion drafts remain protected',
    writes.length === 1 && writes[0].provider === 'claude' && writes[0].aliases[0].alias === 'sonnet-preview'
      && (await provider.getByRole('combobox').isDisabled()));
  await drawer.getByTestId('oauth-model-rules-tab-excluded').click();
  check('the other section still has its draft after the alias save',
    (await drawer.locator('[data-rule="claude-3*"]').count()) === 1);
  await page.goBack();
  const discard = page.locator('.ant-modal-confirm').last();
  await discard.waitFor({ state: 'visible' });
  await discard.getByRole('button', { name: 'Cancel', exact: true }).click();
  await discard.waitFor({ state: 'hidden' });
  check('declining native Back leaves the rules and draft visible', await drawer.locator('[data-rule="claude-3*"]').isVisible());
  await drawer.getByTestId('oauth-model-rules-revert').click();
  const picker = provider.getByRole('combobox');
  await picker.fill('Codex');
  const codexOption = page.locator('.ant-select-dropdown:visible .ant-select-item-option').filter({ hasText: 'Codex' });
  await codexOption.waitFor({ state: 'visible' });
  check('provider options carry tab names and brand artwork', (await codexOption.locator('img, [aria-hidden="true"]').count()) > 0);
  await codexOption.click();
  await drawer.locator('[data-model="gpt-4.1"]').waitFor({ state: 'visible' });
  const wildcardModel = drawer.locator('[data-model="gpt-4.1"]').getByRole('checkbox');
  check('catalog models covered by a wildcard cannot be unchecked independently',
    await wildcardModel.isChecked() && await wildcardModel.isDisabled());
  await drawer.getByTestId('oauth-excluded-models-rule-input').fill('*');
  await drawer.getByTestId('oauth-excluded-models-rule-add').click();
  await drawer.getByTestId('oauth-model-rules-save').click();
  await until(() => drawer.getByTestId('oauth-model-rules-save').isDisabled());
  check('global exclusion write preserves existing rules and targets the provider key',
    writes.length === 2 && writes[1].provider === 'codex' && writes[1].models.join(',') === 'gpt-5-mini,gpt-4*,*');
  for (const width of [1440, 375, 320]) {
    await page.setViewportSize({ width, height: 900 });
    await settleLayout(page);
    await until(() => drawer.evaluate((element) => element.getBoundingClientRect().left >= -1));
    const geometry = await drawer.evaluate((element) => {
      const rect = element.getBoundingClientRect();
      const body = element.querySelector('.ant-drawer-body');
      const footer = element.querySelector('.ant-drawer-footer').getBoundingClientRect();
      return { left: rect.left, right: rect.right, bodyOverflow: body.scrollWidth - body.clientWidth, footerBottom: footer.bottom };
    });
    check(`model rules ${width}px stay within the viewport and keep the footer reachable`,
      geometry.left >= -1 && geometry.right <= width + 1 && geometry.bodyOverflow <= 1 && geometry.footerBottom <= 901,
      JSON.stringify(geometry));
  }
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goBack();
  await drawer.waitFor({ state: 'hidden' });
  check('native Back closes the saved rules without leaving the OAuth workspace', page.url().includes('/oauth-management'));
  await openRules.click();
  await drawer.getByTestId('oauth-model-rules-tab-excluded').click();
  await drawer.locator('[data-rule="claude-3*"]').waitFor({ state: 'hidden' });
  check('a discarded section draft is not resurrected when the drawer reopens', (await drawer.locator('[data-rule="claude-3*"]').count()) === 0);
  await drawer.locator('.ant-drawer-close').click();
  await drawer.waitFor({ state: 'hidden' });
  await page.unroute('**/management/auth-files/model-aliases');
  await page.unroute('**/management/auth-files/excluded-models');
}

function oauthWarmupFixtureRoute() {
  const credentials = new Map();
  const settings = createWarmupFixture();
  return [(url) => url.pathname.includes('/management/quota/warmup/'), (url, method, body) => {
    const match = url.pathname.match(/\/warmup\/credentials\/([^/]+)(?:\/run)?$/);
    if (!match) return settings(url, method, body);
    const authIndex = decodeURIComponent(match[1]);
    const file = files.find(entry => entry.auth_index === authIndex);
    if (!file || !['codex', 'claude'].includes(file.provider)) return { status: 404, json: { code: 'resource_not_found', error: 'resource_not_found' } };
    if (!credentials.has(authIndex)) credentials.set(authIndex, createWarmupFixture({ authIndex, provider: file.provider, name: file.name }));
    return credentials.get(authIndex)(url, method, body);
  }];
}

const DAY = 86_400_000;

// The lending projection the OAuth header summarises and the lending page renders. It is built
// from the same auth indexes the quota fixture uses, with one card already right and one that
// needs a change, plus two unpooled credentials for the fold.
function lendingFixture() {
  const now = Date.now();
  const card = (authIndex, displayName, options) => ({
    auth_index: authIndex,
    name: `${displayName}.json`,
    display_name: displayName,
    role: 'suggested',
    window_id: authIndex + '-window-1',
    window_kind: 'weekly',
    reset_accuracy: 'exact',
    observed_at_ms: now,
    fresh_until_ms: now + 7 * DAY,
    priority_is_default: false,
    can_edit_priority: true,
    reason_codes: [],
    is_comparable: true,
    ...options,
  });
  const cards = [
    card('auth-01', 'credential-01', {
      tier: 1, remaining_percent: 90, reset_at_ms: now + 4 * DAY,
      priority: 10, priority_known: true, suggested_priority: 10, needs_change: false,
    }),
    card('auth-05', 'credential-05', {
      tier: 2, remaining_percent: 30, reset_at_ms: now + 2 * DAY,
      priority: 10, priority_known: true, suggested_priority: -10, needs_change: true,
    }),
  ];
  return {
    generated_at_ms: now,
    pools: [{
      id: 'codex:pro',
      provider: 'codex',
      plan_type: 'pro',
      plan_label: 'Pro',
      window_kind: 'weekly',
      state: 'advised',
      valid_until_ms: now + 2 * DAY,
      observed_at_ms: now,
      fresh_until_ms: now + 7 * DAY,
      is_reference: false,
      is_balanced: false,
      needs_change_count: 1,
      hold_count: 0,
      reason_codes: [],
      cards,
      outside_cards: [],
    }],
    unpooled_count: 2,
    unpooled: [
      { auth_index: 'auth-03', name: 'credential-03.json', reason_codes: ['plan_type_unknown'] },
      { auth_index: 'auth-04', name: 'credential-04.json', reason_codes: ['no_long_window'] },
    ],
  };
}

const lending = lendingFixture();

export const oauthManagementFixtures = {
  files,
  quota,
  get routes() { return [
    oauthWarmupFixtureRoute(),
    [(url, method) => method === 'GET' && url.pathname.endsWith('/management/auth-files/safe-fields'), url => {
      const file = files.find(file => file.name === url.searchParams.get('name'));
      return { name:file?.name, priority:file?.priority, weight:file?.weight, note:file?.note??'', disable_cooling:false, websockets:false, using_api:false, model_aliases:[], excluded_models:[] };
    }],
    [(url, method) => method === 'GET' && url.pathname.endsWith('/management/auth-files/models'), () => ({models:[]})],
    [(url) => url.pathname.endsWith('/management/auth-files'), () => ({ files, total: files.length })],
    [(url) => url.pathname.endsWith('/management/quota'), () => ({
      summary: { total_credentials: quota.length, healthy_count: quota.length, warning_count: 0, exhausted_count: 0, cooldown_count: 0, attention_count: 0 },
      quotas: quota,
      total: quota.length,
      lending,
    })],
    // Confirming a redemption is mocked here: the real call spends an entitlement, so the
    // probe only proves the request is issued once, from the enabled action.
    [(url, method) => method === 'POST' && url.pathname.endsWith('/management/quota/redeem-credit'), () => ({ status: 'ok', quota: quota[11] })],
    [(url) => url.pathname.endsWith('/management/auth-files/model-aliases'), () => ({ aliases: { claude: [{ name: 'claude-sonnet-4', alias: 'sonnet-latest', fork: true }] } })],
    [(url) => url.pathname.endsWith('/management/auth-files/excluded-models'), () => ({ excluded_models: { codex: ['gpt-5-mini', 'gpt-4*'] } })],
    [(url) => url.pathname.endsWith('/management/auth-files/provider-models'), (url) => ({
      provider: url.searchParams.get('provider'), available: true,
      models: url.searchParams.get('provider') === 'codex'
        ? [{ id: 'gpt-5', display_name: 'GPT-5' }, { id: 'gpt-5-mini' }, { id: 'gpt-4.1' }]
        : [{ id: 'claude-sonnet-4', display_name: 'Claude Sonnet 4' }],
    })],
    [(url) => url.pathname.endsWith('/management/plugins'), () => ({ plugins: [], total: 0 })],
    [(url, method) => method === 'POST' && url.pathname.endsWith('/management/oauth/start'), () => ({
      url: 'https://auth.example.test/authorize',
      state: 'probe-state',
      session_id: 'probe-state',
      provider: 'codex',
      flow: 'redirect',
    })],
    [(url) => url.pathname.endsWith('/management/oauth/status'), () => ({ status: 'wait', message: 'waiting' })],
  ]; },
};

/**
 * The probe scenario's routes: the shared fixtures plus one attempt whose first
 * status read fails before CPA answers.
 *
 * The counter lives in this closure rather than in the shared fixture table because
 * the overlay scenario spreads that table too, and a blip consumed by a different
 * scenario would make this probe assert nothing.
 */
export function oauthManagementProbeRoutes() {
  let statusReads = 0;
  return [
    [
      (url, method) => method === 'POST' && url.pathname.endsWith('/management/oauth/start'),
      (url, method, request) => {
        const provider = JSON.parse(request.postData() ?? '{}').provider;
        if (provider !== 'meta') {
          return { url: 'https://auth.example.test/authorize', state: 'probe-state', session_id: 'probe-state', provider, flow: 'redirect' };
        }
        return {
          url: 'https://auth.example.test/device',
          state: 'probe-blip',
          session_id: 'probe-blip',
          provider,
          flow: 'device',
          user_code: 'PROBE-CODE-1',
        };
      },
    ],
    [
      (url, method) => method === 'GET' && url.pathname.endsWith('/management/oauth/status')
        && url.searchParams.get('state') === 'probe-blip',
      () => {
        statusReads += 1;
        // The probe's own check and the poll armed three seconds after the start response
        // can arrive in either order, and both are failures of this fixture: only the read
        // after them answers success, so the completion is always the surviving poll's.
        return statusReads <= 2 ? { status: 502, json: { error: 'gateway unavailable' } } : { status: 'ok' };
      },
    ],
    ...oauthManagementFixtures.routes,
  ];
}


async function verifyAuthorizationOutcomes({ base, page, check }) {
  for (const outcome of ['new', 'ambiguous', 'refresh-failed']) {
    let hasCompleted = false;
    const newFile = { ...files[0], name: 'completion-new.json', auth_index: 'completion-new' };
    const filesHandler = async (route) => {
      if (hasCompleted && outcome === 'refresh-failed') {
        await fulfillFixture(route, { status: 502, json: { error: 'completion list unavailable' } });
        return;
      }
      const currentFiles = hasCompleted && outcome === 'new' ? [...files, newFile] : files;
      await route.fulfill({ json: { files: currentFiles, total: currentFiles.length } });
    };
    const startHandler = async (route) => route.fulfill({ json: {
      provider: 'codex', flow: 'device', url: 'https://auth.example.test/device',
      user_code: 'COMPLETION-CODE', state: `completion-${outcome}`, session_id: `completion-${outcome}`,
    } });
    const statusHandler = async (route) => {
      hasCompleted = true;
      await route.fulfill({ json: { status: 'ok' } });
    };
    await page.route('**/management/auth-files', filesHandler);
    await page.route('**/management/oauth/start', startHandler);
    await page.route('**/management/oauth/status?*', statusHandler);
    try {
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.goto(`${base}/oauth-management`, { waitUntil: 'domcontentloaded' });
      await page.getByTestId('oauth-credential-record').first().waitFor();
      await page.getByRole('button', { name: /OAuth sign-in|OAuth 登录/i }).click();
      const panel = page.getByTestId('oauth-connect-panel');
      await panel.locator('#oauth-connect-provider').click();
      await page.getByTitle('Codex OAuth', { exact: true }).last().click();
      await panel.locator('[data-oauth-start="codex"]').click();
      await panel.getByRole('button', { name: /Check Authorization Status|检查授权状态/i }).waitFor();
      await panel.getByRole('button', { name: /Check Authorization Status|检查授权状态/i }).click();
      const expectedTitle = outcome === 'new' ? /one new credential was confirmed|确认一个新的凭证/
        : outcome === 'ambiguous' ? /could not be identified uniquely|无法唯一确认/
          : /refreshing the credential list failed|刷新凭证列表失败/;
      const completionToast = page.locator('.omc-toast').filter({ hasText: expectedTitle });
      await completionToast.waitFor();
      check(`${outcome} authorization has exactly one outcome toast`, await page.locator('.omc-toast').count() === 1);
      await completionToast.getByRole('button', { name: /View credentials|查看凭证/i }).click();
      await completionToast.waitFor({ state: 'detached' });
      await panel.waitFor({ state: 'hidden' });
      check(`${outcome} completion action reveals the provider collection`,
        new URL(page.url()).searchParams.get('provider') === 'codex');
      if (outcome === 'new') {
        check('the confirmed credential remains visible after the completion action',
          await page.locator('[data-auth-index="completion-new"]').isVisible());
      }
    } finally {
      await page.unroute('**/management/auth-files', filesHandler);
      await page.unroute('**/management/oauth/start', startHandler);
      await page.unroute('**/management/oauth/status?*', statusHandler);
    }
  }
}


async function verifyWorkspaceScale({ base, page, check }) {
  const scaleFiles = Array.from({ length: 48 }, (_, index) => ({
    ...files[index % files.length],
    name: `scale-${String(index).padStart(2, '0')}.json`,
    auth_index: `scale-${index}`,
  }));
  const scaleQuota = scaleFiles.map((file, index) => ({
    ...quotaFor(index % files.length),
    name: file.name,
    auth_index: file.auth_index,
    capabilities: { refresh_supported: index < 23 },
  }));
  const reads = { files: 0, quota: 0, models: 0 };
  const batches = [];
  let activeBatches = 0;
  let peakBatches = 0;
  const filesHandler = async (route) => {
    reads.files += 1;
    await route.fulfill({ json: { files: scaleFiles, total: 48 } });
  };
  const quotaHandler = async (route) => {
    reads.quota += 1;
    await route.fulfill({ json: { quotas: scaleQuota, total: 48 } });
  };
  const pendingBatches = [];
  const batchHandler = async (route) => {
    const indexes = route.request().postDataJSON().auth_indexes;
    batches.push(indexes);
    activeBatches += 1;
    peakBatches = Math.max(peakBatches, activeBatches);
    if (batches.length === 1) await new Promise(resolve => pendingBatches.push(resolve));
    activeBatches -= 1;
    for (const index of indexes) {
      const item = scaleQuota.find((entry) => entry.auth_index === index);
      item.last_success_at_ms = item.observed_at_ms;
      item.last_refresh = {
        outcome: index === 'scale-7' ? 'failed' : index === 'scale-13' ? 'rate_limited' : 'success',
        attempted_at_ms: Date.now(),
        ...(index === 'scale-7' ? { http_status: 483 } : {}),
        ...(index === 'scale-13' ? { http_status: 429, retry_at_ms: Date.now() + 120_000 } : {}),
      };
    }
    const quotas = indexes
      .filter((index) => index !== 'scale-13')
      .map((index) => ({ ...scaleQuota.find((item) => item.auth_index === index),
        ...(index === 'scale-7' ? { status: 'error', error: `HTTP 483: Fixture upstream failure (refresh ${Math.ceil(batches.length / 3)})` } : {}),
      }));
    await route.fulfill({ json: { status: 'ok', quotas, total: indexes.length, results: indexes.map((index) => {
      const quota = quotas.find((item) => item.auth_index === index);
      if (index === 'scale-13') return {
        auth_index: index, name: 'operator-2@example.test', outcome: 'rate_limited', retry_at_ms: Date.now() + 120_000, retry_after_ms: 120_000,
      };
      if (index === 'scale-7') return {
        auth_index: index, name: quota.name, outcome: 'failed', error: quota.error, quota,
      };
      return { auth_index: index, name: quota.name, outcome: 'success', quota };
    }) } });
  };
  const countModels = (request) => {
    if (request.url().includes('/auth-files/models')) reads.models += 1;
  };
  page.on('request', countModels);
  await page.route('**/management/auth-files', filesHandler);
  await page.route('**/management/quota', quotaHandler);
  await page.route('**/management/quota/refresh', batchHandler);
  try {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto(`${base}/oauth-management?page_size=48&density=compact`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="oauth-credential-record"][data-auth-index="scale-47"]').waitFor();
    await settleLayout(page);
    check('48 credentials share collection queries without per-row model reads',
      reads.files <= 2 && reads.quota <= 2 && reads.models === 0, JSON.stringify(reads));
    await page.getByRole('button', { name: /Refresh quota \(23\)/ }).click();
    await until(() => pendingBatches.length > 0, {label:'held quota batch'});
    check('quota refresh starts only one batch while the first response is held', activeBatches === 1);
    pendingBatches.splice(0).forEach(release => release());
    // This fixture run has failures, so the outcome is the report toast that lists each
    // target's reason, and it is the only toast the run raises.
    await page.getByTestId('quota-operation-report').waitFor();
    const report = await page.getByTestId('quota-operation-report').innerText();
    check('a failing refresh raises one report toast and nothing beside it',
      (await page.locator('.omc-toast').count()) === 1,
      `toasts=${await page.locator('.omc-toast').count()}`);
    check('23 eligible quota targets use sequential 10/10/3 batches exactly once',
      batches.map((batch) => batch.length).join('/') === '10/10/3'
        && new Set(batches.flat()).size === 23 && peakBatches === 1,
      JSON.stringify({ sizes: batches.map((batch) => batch.length), peakBatches }));
    check('the refresh report separates success, rate limit, failure and skipped targets',
      report.includes('21 succeeded, 1 rate limited, 1 failed, 25 skipped')
        && report.includes('Succeeded') && report.includes('Rate limited')
        && report.includes('Failed') && report.includes('Skipped')
        && report.includes('Fixture upstream failure') && report.includes('operator-2@example.test')
        && report.includes('retry at') && !report.includes('rate_limited_backoff')
        && !report.includes('scale-13'), report);
    await page.screenshot({ path: 'tmp/oauth-quota-refresh-report.png' });
    const failedRow = page.locator('[data-testid="oauth-credential-record"][data-auth-index="scale-7"]');
    await until(async () => (await failedRow.innerText()).includes('Refresh failed'), { label: 'failed attempt reaches the stored overview' });
    check('a failed refresh preserves the reading and changes its row and summary',
      (await failedRow.innerText()).includes('HTTP 483')
        && (await failedRow.innerText()).includes('Last successful reading')
        && !(await failedRow.locator('[data-quota-body]').innerText()).includes('Normal')
        && (await page.locator('[data-quota-summary="refresh_failed"]').innerText()).includes('1')
        && (await page.locator('[data-quota-summary="rate_limited"]').innerText()).includes('1'));
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      await failedRow.scrollIntoViewIfNeeded();
      await settleLayout(page);
      const detailFits = await failedRow.locator('[data-quota-body]').evaluate((body) => Array.from(body.querySelectorAll('span')).filter((span) => span.textContent.includes('Last successful reading')).every((span) => span.scrollWidth <= span.clientWidth + 1));
      check(`last successful reading is fully visible at ${width}px`, detailFits);
      check(`failed refresh at ${width}px stays within the viewport`, await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth));
      await page.screenshot({ path: `tmp/quota-refresh-status-${width}.png`, fullPage: true });
    }
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.evaluate(() => window.scrollTo(0, 0));
    await settleLayout(page);
    // Exercise the real copy action without depending on the machine's clipboard permission.
    await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', {
      configurable: true, value: { writeText: async () => {} },
    }));
    await page.getByRole('button', { name: /OAuth sign-in|OAuth 登录/i }).click();
    const copyPanel = page.getByTestId('oauth-connect-panel');
    await copyPanel.locator('#oauth-connect-provider').click();
    await page.getByTitle('Codex OAuth', { exact: true }).last().click();
    await copyPanel.locator('[data-oauth-start="codex"]').click();
    const copyLink = copyPanel.getByRole('button', { name: /Copy Link|复制链接/i });
    await copyLink.waitFor();
    // Retention, not pointer geometry: dispatch the burst in one task so none of the four
    // ordinary notices can expire before the oldest-report eviction would happen.
    await copyLink.evaluate((button) => {
      for (let i = 0; i < 4; i += 1) button.click();
    });
    await until(async () => await page.locator('.omc-toast-success').count() === 4,
      { label: 'four independent copy acknowledgements are present together' });
    check('four ordinary notifications cannot evict the persistent quota report',
      await page.getByTestId('quota-operation-report').isVisible()
        && await page.getByTestId('quota-operation-report').evaluate((notice) => !notice.className.includes('-leave')));
    const closeCopyPanel = page.locator('.ant-drawer-open .ant-drawer-close');
    await closeCopyPanel.focus();
    await closeCopyPanel.press('Enter');
    await copyPanel.waitFor({ state: 'hidden' });
    // Dismiss the ordinary notices so the next refresh's count proves replacement, not expiry.
    await page.locator('.omc-toast-success .ant-notification-notice-close').evaluateAll((buttons) => {
      for (const button of buttons) button.click();
    });
    await until(async () => await page.locator('.omc-toast-success').count() === 0,
      { label: 'copy acknowledgements are dismissed' });
    const previousBatches = batches.length;
    // The persistent report can cover the toolbar; keyboard activation keeps it
    // present while proving that the next outcome replaces it.
    const refreshAgain = page.getByRole('button', { name: /Refresh quota \(23\)/ });
    await refreshAgain.focus();
    await refreshAgain.press('Enter');
    await until(() => batches.length === previousBatches + 3, { label: 'the second refresh completes every batch' });
    await until(async () => (await page.getByTestId('quota-operation-report').innerText()).includes('Fixture upstream failure (refresh 2)'),
      { label: 'the replacement report has the settled refresh outcome' });
    check('another refresh replaces the report instead of stacking it',
      await page.getByTestId('quota-operation-report').count() === 1 && await page.locator('.omc-toast').count() === 1);
    await page.getByTestId('quota-operation-report').locator('.ant-notification-notice-close').click();
    await page.getByTestId('quota-operation-report').waitFor({ state: 'detached' });
    for (const width of [1440, 390]) {
      await page.setViewportSize({ width, height: 1000 });
      const summary = page.locator('[data-quota-summary="refresh_failed"]').locator('..');
      await summary.scrollIntoViewIfNeeded();
      await settleLayout(page);
      await summary.screenshot({ path: `tmp/quota-refresh-summary-${width}.png` });
    }
    await page.setViewportSize({ width: 1440, height: 900 });
    // A hard navigation ends the copy fixture's synthetic authorization before the scale sessions.
    await page.goto(`${base}/oauth-management?page_size=48&density=compact`, { waitUntil: 'domcontentloaded' });
    await page.getByTestId('oauth-credential-record').first().waitFor();
    const starts = [];
    const pollReads = {};
    const pendingPolls = {};
    let hasConcurrentPoll = false;
    const startHandler = async (route) => {
      const provider = route.request().postDataJSON().provider;
      starts.push(provider);
      await route.fulfill({ json: { provider, flow: 'redirect',
        url: 'https://auth.example.test/authorize', state: `scale-${provider}`, session_id: `scale-${provider}` } });
    };
    const heldPolls = [];
    const statusHandler = async (route) => {
      const state = new URL(route.request().url()).searchParams.get('state');
      pollReads[state] = (pollReads[state] ?? 0) + 1;
      pendingPolls[state] = (pendingPolls[state] ?? 0) + 1;
      if (pendingPolls[state] > 1) hasConcurrentPoll = true;
      await new Promise(resolve => heldPolls.push(resolve));
      pendingPolls[state] -= 1;
      await route.fulfill({ json: { status: 'wait' } });
    };
    await page.route('**/management/oauth/start', startHandler);
    await page.route('**/management/oauth/status?*', statusHandler);
    for (const [provider, label] of [['codex', 'Codex OAuth'], ['anthropic', 'Anthropic OAuth']]) {
      await page.getByRole('button', { name: /OAuth sign-in/ }).click();
      await page.locator('#oauth-connect-provider').click();
      await page.getByTitle(label, { exact: true }).last().click();
      await page.locator(`[data-oauth-start="${provider}"]`).click();
      await page.locator(`[data-oauth-card="${provider}"]`).getByRole('button', { name: /Cancel Authorization/ }).waitFor();
      await page.locator('.ant-drawer-open .ant-drawer-close').click();
      await page.locator('.ant-drawer-open').waitFor({ state: 'hidden' });
    }
    await page.getByTestId('oauth-session-pill').first().getByRole('button').click();
    await page.locator('.ant-drawer-open .ant-drawer-close').click();
    await page.locator('.ant-drawer-open').waitFor({ state: 'hidden' });
    await until(() => heldPolls.length >= 2, {label:'both minimized authorization polls held'});
    check('48 records retain two independent minimized sessions without duplicate checkers',
      starts.join('/') === 'codex/anthropic' && !hasConcurrentPoll
        && pollReads['scale-codex'] >= 1 && pollReads['scale-anthropic'] >= 1
        && (await page.getByTestId('oauth-session-pill').count()) === 2,
      JSON.stringify({ starts, pollReads, hasConcurrentPoll }));
    console.log(`OAuth scale: ${JSON.stringify({ reads, sizes: batches.map((batch) => batch.length), peakBatches, starts, pollReads })}`);
    heldPolls.splice(0).forEach(release => release());
    // Hard navigation ends these synthetic attempts before removing their fixtures.
    await page.goto(`${base}/oauth-management?density=compact`);
    await page.unroute('**/management/oauth/start', startHandler);
    await page.unroute('**/management/oauth/status?*', statusHandler);
  } finally {
    pendingBatches.splice(0).forEach(release => release());
    page.off('request', countModels);
    await page.unroute('**/management/auth-files', filesHandler);
    await page.unroute('**/management/quota', quotaHandler);
    await page.unroute('**/management/quota/refresh', batchHandler);
  }
}

async function verifyVertexImportDialog({ page, check }) {
  const openImport = page.getByRole('button', { name: 'Import Vertex key', exact: true });
  await openImport.click();
  const dialog = page.getByRole('dialog', { name: 'Import a Vertex service account key' });
  await dialog.waitFor();
  check('Vertex import starts with no key and cannot submit', await dialog.getByRole('button', { name: 'Import', exact: true }).isDisabled());
  await dialog.locator('input[type="file"]').setInputFiles({
    name: 'service-account-fixture.json', mimeType: 'application/json',
    buffer: Buffer.from(JSON.stringify({ project_id: 'fixture-project', private_key: 'SYNTHETIC-KEY-FIXTURE', client_email: 'fixture@fixture-project.iam.gserviceaccount.com' })),
  });
  await dialog.getByText('fixture-project', { exact: true }).waitFor();
  check('Vertex import presents identity without rendering key contents', (await dialog.innerText()).includes('fixture@fixture-project.iam.gserviceaccount.com') && !(await dialog.innerText()).includes('SYNTHETIC-KEY-FIXTURE'));
  await settleLayout(page);
  await page.screenshot({ path: 'tmp/vertex-import-desktop.png' });
  const desktopViewport = page.viewportSize();
  await page.setViewportSize({ width: 390, height: 844 });
  await settleLayout(page);
  check('the Vertex import dialog fits a phone viewport', await dialog.evaluate((node) => node.scrollWidth <= node.clientWidth && node.getBoundingClientRect().right <= window.innerWidth));
  await page.screenshot({ path: 'tmp/vertex-import-phone.png' });
  await dialog.getByRole('textbox', { name: 'Location', exact: true }).fill('us-central1.evil.test');
  check('an invalid Vertex region prevents submitting', await dialog.getByRole('button', { name: 'Import', exact: true }).isDisabled());
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await dialog.waitFor({ state: 'hidden' });
  await page.setViewportSize(desktopViewport);
  await openImport.click();
  await dialog.waitFor();
  check('reopening Vertex import discards the selected key', await dialog.getByText('fixture-project', { exact: true }).count() === 0 && await dialog.getByRole('button', { name: 'Import', exact: true }).isDisabled());
  await page.evaluate(() => {
    const readFile = File.prototype.text;
    File.prototype.text = function () {
      if (this.name !== 'delayed-service-account-fixture.json') return readFile.call(this);
      return new Promise((resolve) => {
        window.__releaseVertexFile = () => resolve(JSON.stringify({ project_id: 'delayed-project', private_key: 'SYNTHETIC-DELAYED-KEY' }));
      });
    };
  });
  await dialog.locator('input[type="file"]').setInputFiles({
    name: 'delayed-service-account-fixture.json', mimeType: 'application/json', buffer: Buffer.from('{}'),
  });
  await until(async () => await page.evaluate(() => typeof window.__releaseVertexFile === 'function'), { label: 'the held Vertex file read' });
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await dialog.waitFor({ state: 'hidden' });
  await openImport.click();
  await dialog.waitFor();
  await page.evaluate(() => window.__releaseVertexFile());
  await settleLayout(page);
  check('a file read from a cancelled Vertex dialog cannot repopulate its next opening', await dialog.getByText('delayed-project', { exact: true }).count() === 0 && await dialog.getByRole('button', { name: 'Import', exact: true }).isDisabled());
  await dialog.getByRole('button', { name: 'Cancel', exact: true }).click();
  await dialog.waitFor({ state: 'hidden' });
}

export async function oauthVertexImport({base, page, check, step}) {
  await page.goto(`${base}/oauth-management`);
  await page.getByTestId('oauth-credential-record').first().waitFor();
  await step('Vertex file lifecycle', () => verifyVertexImportDialog({page,check}));
}
export async function oauthModelRules(fixtures) {
  await fixtures.step('model alias and exclusion writes', () => checkOAuthModelRules(fixtures));
}
export async function oauthTokenCapacity(fixtures) {
  await fixtures.page.goto(`${fixtures.base}/oauth-management`);
  await fixtures.page.getByTestId('oauth-credential-record').first().waitFor();
  await fixtures.step('capacity formats and phone bounds', () => verifyFullTokenCapacity(fixtures));
}
export async function oauthAuthorizationOutcomes(fixtures) {
  await fixtures.step('authorization completion and read failures', () => verifyAuthorizationOutcomes(fixtures));
}
export async function oauthWorkspaceScale(fixtures) {
  await fixtures.step('batch quotas and cancellation', () => verifyWorkspaceScale(fixtures));
}
export async function oauthPluginConnections(fixtures) {
  const oauthStarts = [];
  fixtures.page.on('request', request => { if (request.url().includes('/management/oauth/start')) oauthStarts.push(request.url()); });
  await fixtures.step('key and interactive plugin connections', () => verifyPluginConnections({...fixtures,oauthStarts}));
}

export async function oauthWindowWarmup({ page, base, check }) {
  const fixture = createWarmupFixture({ authIndex: 'auth-02', provider: 'claude' });
  const consumingRequests = [];
  const handler = async route => {
    const request = route.request();
    if (request.method() === 'POST') consumingRequests.push(JSON.parse(request.postData()));
    const result = fixture(new URL(request.url()), request.method(), request.postData());
    await fulfillFixture(route, { status: result.status, contentType: 'application/json', body: JSON.stringify(result.json) });
  };
  const fieldHandler = async route => {
    const input = route.request().postDataJSON();
    await route.fulfill({ json: { status: 'ok', file: { ...files[1], note: input.note }, fields: { name: files[1].name, note: input.note, disable_cooling: false, websockets: false, using_api: false, excluded_models: [], model_aliases: [] } } });
  };
  await page.route('**/management/quota/warmup/**', handler);
  await page.route('**/management/auth-files/fields', fieldHandler);
  const checkRuleAlignment = async (surface, label) => {
    const geometry = await surface.getByRole('switch').evaluateAll(switches => switches.map(control => {
      const switchBox = control.getBoundingClientRect();
      const labelBox = document.querySelector(`label[for="${CSS.escape(control.id)}"]`).getBoundingClientRect();
      const bodyBox = control.parentElement.nextElementSibling.getBoundingClientRect();
      return { switchLeft: switchBox.left, labelLeft: labelBox.left, bodyLeft: bodyBox.left,
        centreOffset: Math.abs(switchBox.y + switchBox.height / 2 - labelBox.y - labelBox.height / 2) };
    }));
    check(`${label} aligns switches, labels and supporting controls`, geometry.length === 2
      && geometry.every(row => row.centreOffset <= 1 && Math.abs(row.labelLeft - row.bodyLeft) <= 1)
      && Math.abs(geometry[0].switchLeft - geometry[1].switchLeft) <= 1
      && Math.abs(geometry[0].labelLeft - geometry[1].labelLeft) <= 1, JSON.stringify(geometry));
  };
  try {
    await page.goto(`${base}/oauth-management`);
    await page.getByTestId('oauth-warmup-settings-open').click();
    const drawer = page.locator('.ant-drawer-content-wrapper').filter({ has: page.locator('.ant-drawer-title').filter({ hasText: 'Warmup settings' }) });
    await drawer.waitFor({ state: 'visible' });
    await drawer.getByText('The five-hour window runs 06:00–11:00 (UTC)', { exact: true }).waitFor();
    for (const reducedMotion of ['no-preference', 'reduce']) {
      await page.emulateMedia({ reducedMotion });
      const transition = await drawer.evaluate(element => {
        const style = getComputedStyle(element);
        return { property: style.transitionProperty, duration: style.transitionDuration };
      });
      if (reducedMotion === 'reduce') {
        check('warmup drawer has no transition under reduced motion', transition.property === 'none' && transition.duration === '0s', JSON.stringify(transition));
      } else {
        check('warmup drawer animates position without interpolating viewport dimensions', transition.property === 'transform', JSON.stringify(transition));
      }
      for (const width of [1440, 320]) {
        await page.setViewportSize({ width, height: 900 });
        await settleLayout(page);
        const geometry = await drawer.evaluate(element => {
          const bounds = element.getBoundingClientRect(); const body = element.querySelector('.ant-drawer-body');
          return { left: bounds.left, right: bounds.right, overflow: body.scrollWidth - body.clientWidth };
        });
        await checkRuleAlignment(drawer, `settings ${width}px (${reducedMotion})`);
        check(`warmup settings ${width}px stay in the viewport (${reducedMotion})`, geometry.left >= -1 && geometry.right <= width + 1 && geometry.overflow <= 1, JSON.stringify(geometry));
        await fs.mkdir('tmp/warmup-proof', { recursive: true });
        await page.screenshot({ path: `tmp/warmup-proof/settings-${width}-${reducedMotion}.png` });
      }
    }
    await drawer.getByRole('tab', { name: 'Claude', exact: true }).click();
    await checkRuleAlignment(drawer, 'Claude settings phone');
    await page.setViewportSize({ width: 1440, height: 900 });
    await settleLayout(page);
    await checkRuleAlignment(drawer, 'Claude settings desktop');
    await drawer.getByRole('switch', { name: 'Warm up daily at a set time' }).click();
    await page.goBack();
    const confirmation = page.getByRole('dialog').filter({ hasText: 'Unsaved' });
    await confirmation.waitFor({ state: 'visible' });
    check('Back asks before discarding an unsaved warmup policy', await drawer.isVisible());
    await confirmation.getByRole('button', { name: 'Cancel', exact: true }).click();
    await confirmation.waitFor({ state: 'hidden' });
    await drawer.getByRole('button', { name: 'Cancel', exact: true }).click();
    await page.goBack();
    await drawer.waitFor({ state: 'hidden' });
    check('Back closes a reverted warmup editor without leaving OAuth management', page.url().includes('/oauth-management'));
    const record = page.getByTestId('oauth-credential-record').filter({ hasText: 'credential-02.json' });
    await record.getByRole('button', { name: /^Details:/ }).click();
    const warmup = page.getByTestId('credential-warmup');
    check('quota tab does not mix in warmup controls', await warmup.count() === 0);
    await page.getByRole('tab', { name: 'Window warmup', exact: true }).click();
    await warmup.getByRole('button', { name: 'Warm up now', exact: true }).waitFor();
    await page.evaluate(() => Object.defineProperty(window.crypto, 'randomUUID', { configurable: true, value: undefined }));
    await warmup.getByRole('button', { name: 'Warm up now', exact: true }).click();
    const manualConfirmation = page.locator('.ant-modal-confirm');
    await manualConfirmation.waitFor();
    check('manual warmup names the selected model and does not consume before confirmation', consumingRequests.length === 0 && (await manualConfirmation.innerText()).includes('claude-haiku-test'));
    await manualConfirmation.getByRole('button', { name: 'Cancel', exact: true }).click();
    await manualConfirmation.waitFor({ state: 'hidden' });
    check('cancelling manual warmup sends no consuming request', consumingRequests.length === 0);
    await warmup.getByRole('button', { name: 'Warm up now', exact: true }).click();
    await manualConfirmation.waitFor();
    await manualConfirmation.getByRole('button', { name: 'Warm up now', exact: true }).click();
    await until(() => consumingRequests.length === 1, { label: 'one confirmed warmup request' });
    await warmup.getByTestId('warmup-last-result').getByText('Quota window started', { exact: true }).waitFor();
    check('manual warmup sends a confirmed revision-bound id without arbitrary upstream fields', consumingRequests[0].confirmed === true && typeof consumingRequests[0].request_id === 'string' && Object.keys(consumingRequests[0]).sort().join(',') === 'confirmed,request_id,revision');
    await warmup.getByText('Custom', { exact: true }).click();
    await warmup.getByRole('switch', { name: 'Warm up as soon as a window resets' }).waitFor();
    const detail = page.locator('.ant-drawer-content-wrapper').filter({ has: warmup });
    await detail.getByRole('tab', { name: 'Configuration' }).click();
    const note = detail.getByRole('textbox', { name: 'Note' });
    await note.waitFor();
    await until(() => detail.getByRole('textbox', { name: 'Prefix' }).isEnabled(), { label: 'safe credential fields loaded' });
    await note.fill('Keep the warmup draft');
    const savedFields = page.waitForResponse(response => response.url().endsWith('/management/auth-files/fields') && response.request().method() === 'PATCH');
    await detail.getByRole('button', { name: 'Save Fields', exact: true }).click();
    await savedFields;
    await detail.getByRole('tab', { name: /Window warmup/ }).click();
    await until(() => warmup.getByRole('button', { name: 'Save', exact: true }).isEnabled(), { label: 'warmup draft remains unsaved after field save' });
    check('credential field save leaves the warmup draft and detail drawer open', await detail.isVisible());
    await detail.locator('.ant-drawer-close').click();
    const discardWarmup = page.locator('.ant-modal-confirm');
    await discardWarmup.waitFor();
    check('preserved warmup draft still requires an explicit discard decision', (await discardWarmup.innerText()).includes('Discard unsaved changes?'));
    await discardWarmup.getByRole('button', { name: 'Cancel', exact: true }).click();
    await warmup.getByRole('button', { name: 'Cancel', exact: true }).click();
    await warmup.getByText('Custom', { exact: true }).click();
    await checkRuleAlignment(warmup, 'credential desktop');
    await page.screenshot({ path: 'tmp/warmup-proof/credential-1440.png' });
    await page.setViewportSize({ width: 320, height: 900 });
    await settleLayout(page);
    await warmup.scrollIntoViewIfNeeded();
    const detailBody = page.locator('.ant-drawer-body').filter({ has: warmup });
    await checkRuleAlignment(warmup, 'credential phone');
    check('credential warmup controls fit the phone drawer', await detailBody.evaluate(element => element.scrollWidth <= element.clientWidth + 1));
    await page.screenshot({ path: 'tmp/warmup-proof/credential-320.png' });

  } finally { await page.unroute('**/management/quota/warmup/**', handler); await page.unroute('**/management/auth-files/fields', fieldHandler); }
}

/**
 * The OAuth page keeps only a one-line entry, and the lending page carries the complete advice.
 *
 * The entry is a link inside the header's existing summary strip, so the page does not grow; the
 * page is reached from the mount module's path, shows the advised pool, folds the unpooled list
 * twice, and works when opened directly and refreshed (with no back control because there is no
 * history behind it).
 */
export async function quotaLending({ base, page, context, check }) {
  const backName = /Go back|返回上一页/i;
  await page.goto(base + '/oauth-management', { waitUntil: 'domcontentloaded' });
  await page.locator('[data-testid="oauth-credential-record"]').first().waitFor({ state: 'visible', timeout: 20000 });

  check('the OAuth page no longer renders the lending panel', (await page.locator('[data-lending-view="page"]').count()) === 0);
  const entry = page.locator('[data-lending-entry]');
  const entryCount = await entry.count();
  check('the OAuth header carries exactly one lending entry', entryCount === 1, 'entries=' + entryCount);
  const href = (await entry.getAttribute('href')) || '';
  check('the entry points at the mounted lending path', href.endsWith('/quota-lending'), 'href=' + href);
  const inline = await entry.evaluate((node) => ({
    inHead: node.closest('.terminal-page-head') !== null,
    height: node.getBoundingClientRect().height,
  }));
  check('the entry stays one inline line inside the page header', inline.inHead && inline.height <= 24, JSON.stringify(inline));

  await entry.click();
  await page.waitForURL(/\/quota-lending$/);
  await page.locator('[data-lending-view="page"]').waitFor({ state: 'visible', timeout: 20000 });
  check('the entry opens the lending page', page.url().endsWith('/quota-lending'), page.url());
  check(
    'the page shows the advised pool',
    (await page.locator('[data-lending-pool]').count()) === 1
      && (await page.locator('[data-lending-pool]').getAttribute('data-lending-state')) === 'advised',
  );
  check('the back control appears after an in-app navigation', (await page.getByRole('button', { name: backName }).count()) === 1);

  const summary = page.locator('[data-lending-unpooled-summary]');
  check(
    'the unpooled list starts folded',
    (await summary.getAttribute('aria-expanded')) === 'false'
      && (await page.locator('[data-lending-unpooled-names]').count()) === 0,
  );
  await summary.click();
  const firstGroup = page.locator('[data-lending-unpooled-group-toggle]').first();
  await firstGroup.click();
  check('opening a reason reveals only its credential names', (await page.locator('[data-lending-unpooled-names] li').count()) === 1);

  await page.getByRole('button', { name: backName }).click();
  await page.waitForURL(/\/oauth-management$/);
  check('the back control returns to the OAuth page', !page.url().includes('/quota-lending'), page.url());

  await page.goto(base + '/quota-lending', { waitUntil: 'domcontentloaded' });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator('[data-lending-view="page"]').waitFor({ state: 'visible', timeout: 20000 });
  check(
    'a direct open and refresh render the page without a back control',
    (await page.locator('[data-lending-pool]').count()) === 1
      && (await page.getByRole('button', { name: backName }).count()) === 0,
  );

  await verifyLendingStates({ base, page, context, check });
}

/**
 * The secondary page's real states, the folds, history, phone width, English copy, the visible
 * clock, the way to the priority field and the read-only surface.
 *
 * A mutable route serves `probeLending(state)` so one page reload moves the whole workspace; the
 * shared OAuth fixtures stay underneath for the credential rows.
 */
async function verifyLendingStates({ base, page, context, check }) {
  const shared = oauthManagementFixtures;
  const clock = { state: 'base', mode: 'lending' };
  const quotaHandler = async (route) => {
    if (clock.mode === 'error') {
      await route.fulfill({ status: 502, json: { error: 'quota read failed' } });
      return;
    }
    const body = {
      summary: {
        total_credentials: shared.quota.length, healthy_count: shared.quota.length,
        warning_count: 0, exhausted_count: 0, cooldown_count: 0, attention_count: 0,
      },
      quotas: shared.quota,
      total: shared.quota.length,
    };
    if (clock.mode === 'lending') body.lending = probeLending(clock.state);
    if (clock.mode === 'null') body.lending = null;
    await route.fulfill({ json: body });
  };
  const writes = [];
  const onRequest = (request) => {
    if (['POST', 'PUT', 'PATCH', 'DELETE'].includes(request.method())
      && /\/(management\/(quota|lending|auth-files\/fields))/.test(new URL(request.url()).pathname)) {
      writes.push(`${request.method()} ${new URL(request.url()).pathname}`);
    }
  };
  let quotaRequests = 0;
  const countQuota = (request) => {
    if (request.method() === 'GET' && new URL(request.url()).pathname.endsWith('/management/quota')) quotaRequests += 1;
  };
  const setState = (state) => { clock.state = state; clock.mode = 'lending'; };
  const gotoOAuth = async () => {
    await page.goto(`${base}/oauth-management`, { waitUntil: 'domcontentloaded' });
    await page.locator('.terminal-page-head').first().waitFor({ timeout: 20000 });
    await page.locator('[data-testid="oauth-credential-record"]').first().waitFor({ timeout: 20000 }).catch(() => {});
  };
  const gotoLending = async () => {
    await page.goto(`${base}/quota-lending`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-lending-view="page"]').waitFor({ timeout: 20000 });
  };
  const entryState = async () => {
    const entry = page.locator('[data-lending-entry]');
    const count = await entry.count();
    if (count === 0) return { count, text: '', kind: null };
    return { count, text: (await entry.innerText()).trim(), kind: await entry.getAttribute('data-lending-entry') };
  };
  const poolState = (index = 0) => page.locator('[data-lending-pool]').nth(index).getAttribute('data-lending-state');
  const cardRow = (authIndex) => page.locator(`[data-lending-card="${authIndex}"]`);
  const action = (authIndex) => cardRow(authIndex).getAttribute('data-lending-action');
  const suggested = (authIndex) => cardRow(authIndex).locator('[data-lending-suggested]').getAttribute('data-lending-suggested');
  const goLink = (authIndex) => cardRow(authIndex).locator('[data-lending-go]');
  const sentence = () => page.locator('[data-lending-sentence]').innerText();
  const poolText = () => page.locator('[data-lending-view="page"]').innerText();
  const headerHeight = () => page.locator('.terminal-page-head').first().evaluate((node) => node.getBoundingClientRect().height);
  const openDisclosure = async (id) => {
    await page.locator(`[data-lending-disclosure="${id}"]`).waitFor({ timeout: 10000 });
    await page.locator(`[data-lending-disclosure-toggle="${id}"]`).click();
    await page.locator(`[data-lending-disclosure-body="${id}"]`).waitFor({ timeout: 10000 });
  };
  const evidenceText = () => page.locator('[data-lending-evidence-table]').innerText();

  await page.addInitScript(() => window.localStorage.setItem('omc-lang', 'zh'));
  await context.route('**/management/quota', quotaHandler);
  page.on('request', onRequest);
  page.on('request', countQuota);
  try {
    await fs.mkdir('tmp/lending-proof', { recursive: true });

    // --- The OAuth header entry: three states plus the two absences.
    setState('base');
    await gotoOAuth();
    const change = await entryState();
    check('the OAuth page renders no lending panel', (await page.locator('[data-lending-view="page"]').count()) === 0);
    check('a card to change shows the count', change.count === 1 && change.kind === 'change' && change.text === '额度拆借 · 1 张要改', JSON.stringify(change));
    check('the entry is an anchor inside the page header', await page.locator('[data-lending-entry]').evaluate((node) => node.tagName === 'A' && node.closest('.terminal-page-head') !== null));
    await settleLayout(page);
    const heightWithEntry = await headerHeight();
    await page.screenshot({ path: 'tmp/lending-proof/oauth-entry-advised.png' });

    setState('keep');
    await gotoOAuth();
    const keep = await entryState();
    check('nothing to change shows the no-change copy', keep.kind === 'keep' && keep.text === '额度拆借 · 都不用改', JSON.stringify(keep));

    setState('balanced');
    await gotoOAuth();
    const balanced = await entryState();
    check('a close ranking still reads as no change', balanced.kind === 'keep' && balanced.text === '额度拆借 · 都不用改', JSON.stringify(balanced));

    for (const [state, label] of [['single', 'no pool'], ['empty', 'empty overview']]) {
      setState(state);
      await gotoOAuth();
      const plain = await entryState();
      check(`${label} leaves the plain entry`, plain.kind === 'plain' && plain.text.trim() === '额度拆借', JSON.stringify(plain));
    }

    for (const mode of ['missing', 'null']) {
      clock.mode = mode;
      await gotoOAuth();
      check(`a ${mode} lending field hides the entry`, (await entryState()).count === 0);
      if (mode === 'missing') {
        await settleLayout(page);
        const heightWithout = await headerHeight();
        check('the entry does not add a header row', Math.abs(heightWithout - heightWithEntry) <= 1, `${heightWithEntry} vs ${heightWithout}`);
      }
    }

    // --- One change, one card already right.
    setState('base');
    await gotoLending();
    check('base: one advised pool', (await page.locator('[data-lending-pool]').count()) === 1 && (await poolState()) === 'advised');
    check('base: the header says one card needs a change', (await page.locator('[data-lending-pool-verdict]').innerText()).includes('1 张要改'));
    check('base: one row per card carries its own verdict', (await action('auth-01')) === 'keep' && (await action('auth-02')) === 'change');
    check('base: the current and suggested numbers are both printed', (await suggested('auth-01')) === '0' && (await suggested('auth-02')) === '-10');
    check('base: the sentence names the change and the card to leave alone', /credential-02/.test(await sentence()) && /-10/.test(await sentence()) && /credential-01/.test(await sentence()));
    check('base: only the changed card offers the way to the field', (await goLink('auth-02').count()) === 1 && (await goLink('auth-01').count()) === 0);
    const goHref = (await goLink('auth-02').getAttribute('href')) || '';
    check('base: the way to the field names the credential and carries no number', goHref.includes('/oauth-management?open=auth-02') && !goHref.includes('-10'), goHref);
    await page.screenshot({ path: 'tmp/lending-proof/state-base.png' });

    const requestsBeforeEvidence = quotaRequests;
    check('base: the basis starts folded', (await page.locator('[data-lending-disclosure-body="evidence"]').count()) === 0);
    await openDisclosure('evidence');
    const baseEvidence = await evidenceText();
    check('base: the basis ranks both cards', /第 1/.test(baseEvidence) && /第 2/.test(baseEvidence), baseEvidence);
    check('base: opening the basis asks for nothing', quotaRequests === requestsBeforeEvidence, `${requestsBeforeEvidence} -> ${quotaRequests}`);

    // The fold is tested on the body with two unpooled credentials, one per reason.
    const summary = page.locator('[data-lending-unpooled-summary]');
    check('unpooled: starts folded and hides names', (await summary.getAttribute('aria-expanded')) === 'false' && (await page.locator('[data-lending-unpooled-names]').count()) === 0);
    const summaryText = await summary.innerText();
    check('unpooled: the total and every reason count are visible', /未进池 2 张/.test(summaryText)
      && /套餐未知，不进任何池 1/.test(summaryText)
      && /同套餐不足两张，不成池 1/.test(summaryText), summaryText);
    await page.screenshot({ path: 'tmp/lending-proof/unpooled-folded.png' });
    await summary.click();
    const groupToggles = page.locator('[data-lending-unpooled-group-toggle]');
    check('unpooled: opening the summary shows one group per reason', (await groupToggles.count()) === 2);
    for (let index = 0; index < 2; index += 1) await groupToggles.nth(index).click();
    const names = await page.locator('[data-lending-unpooled-names] li').allInnerTexts();
    check('unpooled: names appear only after a reason is opened', names.length === 2 && new Set(names).size === 2, names.join(' | '));
    await page.screenshot({ path: 'tmp/lending-proof/unpooled-expanded.png' });

    // --- Nothing to change, and a ranking too close to act on.
    setState('keep');
    await gotoLending();
    check('keep: the pool verdict says no change', (await page.locator('[data-lending-pool-verdict]').innerText()).trim() === '不用改');
    check('keep: the sentence agrees', (await sentence()).includes('现在的设置已经合适，不用改'));
    check('keep: no card offers the way to the field', (await page.locator('[data-lending-go]').count()) === 0);
    await page.screenshot({ path: 'tmp/lending-proof/state-keep.png' });

    setState('balanced');
    await gotoLending();
    check('balanced: the sentence calls them about the same', (await sentence()).includes('两张差不多，不用改'));
    await openDisclosure('evidence');
    check('balanced: the basis gives both cards the same position and marks the tie', (await evidenceText()).includes('第 1，并列'), await evidenceText());
    await page.screenshot({ path: 'tmp/lending-proof/state-balanced.png' });

    // --- Held cards and a pool with no ranking.
    setState('hold');
    await gotoLending();
    check('hold: the collapsed line counts the held card', (await page.locator('[data-lending-hold-toggle]').innerText()).includes('不动 1 张'));
    check('hold: held rows stay hidden until asked for', (await page.locator('[data-lending-disclosure-body="hold"]').count()) === 0);
    await openDisclosure('hold');
    const heldRow = page.locator('[data-lending-hold-card="auth-03"]');
    check('hold: opening lists the card with its short reason', (await heldRow.innerText()).includes('已用完') && (await heldRow.innerText()).includes('不动'));
    check('hold: a held card has no suggested number', (await cardRow('auth-03').count()) === 0);
    await page.screenshot({ path: 'tmp/lending-proof/state-hold.png' });

    setState('noAdvice');
    await gotoLending();
    const noAdvice = page.locator('[data-lending-state="no_advice"]');
    check('no-advice: the whole pool collapses to one line', (await noAdvice.innerText()).includes('暂无建议') && (await noAdvice.innerText()).includes('2 张都不动'));
    check('no-advice: no ranking means no basis to open', (await page.locator('[data-lending-disclosure="evidence"]').count()) === 0);
    await openDisclosure('no-advice');
    check('no-advice: opening lists both held cards', (await page.locator('[data-lending-hold-card]').count()) === 2);
    await page.screenshot({ path: 'tmp/lending-proof/state-no-advice.png' });

    // --- A current number that cannot be read, and a priority this console cannot write.
    setState('unknown');
    await gotoLending();
    check('unknown: the current number is a dash and the suggestion stays', (await cardRow('auth-01').innerText()).includes('—') && (await suggested('auth-01')) === '5');
    check('unknown: the row says the current number could not be read', (await cardRow('auth-01').innerText()).includes('现在的数字读不到'));
    check('unknown: nothing is judged and nothing links to the field', (await action('auth-01')) === 'unknown' && (await page.locator('[data-lending-go]').count()) === 0);

    setState('cannotEdit');
    await gotoLending();
    check('cannot-edit: the verdict still says change', (await action('auth-01')) === 'change');
    check('cannot-edit: the row explains why there is no way to the field', (await cardRow('auth-01').innerText()).includes('这张卡的优先级不能在这里改') && (await page.locator('[data-lending-go]').count()) === 0);

    // --- A long primary name stays readable at the ends and is printed in full inside the basis.
    setState('longName');
    await gotoLending();
    const longNameNode = page.locator('[data-lending-name]').first();
    const longName = await longNameNode.getAttribute('title');
    check('long-name: the full name is kept as the title', longName === 'synthetic-credential-with-a-very-long-name', String(longName));
    check('long-name: the visible name is split for a middle ellipsis', (await longNameNode.locator('span').count()) === 2);
    await openDisclosure('evidence');
    check('long-name: the basis prints the name in full', (await page.locator('[data-lending-full-names="true"]').innerText()).includes('synthetic-credential-with-a-very-long-name'));

    // --- Same-model cards outside the pool.
    setState('outside');
    await gotoLending();
    check('outside: the reminder counts the outside cards', (await page.locator('[data-lending-outside-toggle]').innerText()).includes('这个模型还有 2 张池外的卡也在供货'));
    check('outside: the list stays folded', (await page.locator('[data-lending-outside-card]').count()) === 0);
    await openDisclosure('outside');
    const outsideRows = page.locator('[data-lending-outside-card]');
    check('outside: opening lists one row per card', (await outsideRows.count()) === 2);
    const firstOutside = await outsideRows.nth(0).innerText();
    const secondOutside = await outsideRows.nth(1).innerText();
    check('outside: a pooled card names its pool and current number', firstOutside.includes('codex · Team 300') && firstOutside.includes('现在是 0（默认）'), firstOutside);
    check('outside: a card in no pool names its provider and an unreadable number', secondOutside.includes('claude') && secondOutside.includes('现在是 读不到'), secondOutside);
    await openDisclosure('evidence');
    const outsideEvidence = await page.locator('[data-lending-disclosure-body="evidence"]').innerText();
    check('outside: the basis ties later numbers to the group maximum and points at the list', outsideEvidence.includes('低 10、20') && outsideEvidence.includes('见下面的名单') && !outsideEvidence.includes('排到所有'), outsideEvidence);
    await page.screenshot({ path: 'tmp/lending-proof/state-outside.png' });

    // --- Cards held for reasons, and pools that cannot rank at all.
    setState('limited');
    await gotoLending();
    check('limited: the healthy seat is suggested and the low one is held', (await poolState()) === 'advised' && (await action('auth-01')) === 'keep' && (await page.locator('[data-lending-hold-toggle]').innerText()).includes('不动 1 张'));
    await openDisclosure('hold');
    const limitedRow = await page.locator('[data-lending-hold-card="auth-02"]').innerText();
    check('limited: the held row names the ten-percent floor', limitedRow.includes('余量低于 10%'));
    check('limited: the note names the window and its share', limitedRow.includes('周限额剩 5%，留作应急，不参与排名'), limitedRow);
    await page.screenshot({ path: 'tmp/lending-proof/state-limited.png' });

    setState('restricted');
    await gotoLending();
    check('restricted: the pool still ranks its two healthy seats', (await poolState()) === 'advised' && (await action('auth-01')) === 'keep' && (await action('auth-02')) === 'change');
    await openDisclosure('hold');
    const restrictedRow = await page.locator('[data-lending-hold-card="auth-03"]').innerText();
    check('restricted: the low seat is held with its reason', restrictedRow.includes('余量低于 10%'));
    check('restricted: the note names the window and its share', restrictedRow.includes('周限额剩 5%，留作应急，不参与排名'), restrictedRow);
    await page.screenshot({ path: 'tmp/lending-proof/state-restricted.png' });

    for (const [state, label, copy] of [
      ['textOnly', 'text-only', '缺回满时间'],
      ['stale', 'stale', '读数过期'],
      ['noLong', 'no-long', '缺长额度窗口'],
      ['windowMismatch', 'mismatch', '额度类型不同'],
    ]) {
      setState(state);
      await gotoLending();
      check(`${label}: the pool states there is no advice`, (await poolState()) === 'no_advice');
      await openDisclosure('no-advice');
      check(`${label}: every held row names its reason`, (await page.locator('[data-lending-hold-rows]').innerText()).includes(copy));
      await page.screenshot({ path: `tmp/lending-proof/state-${label}.png` });
    }

    setState('stale');
    await gotoLending();
    await openDisclosure('no-advice');
    const stalePast = await page.locator('[data-lending-hold-card="auth-01"]').innerText();
    const staleRefilled = await page.locator('[data-lending-hold-card="auth-02"]').innerText();
    const staleMinute = await page.locator('[data-lending-hold-card="auth-03"]').innerText();
    check('stale: a long age is stated in hours', stalePast.includes('25 小时前'), stalePast);
    check('stale: a passed refill is named before the age', staleRefilled.includes('回满时刻已到') && !staleRefilled.includes('小时前'), staleRefilled);
    check('stale: a one-minute reading stays in minutes', staleMinute.includes('1 分钟前') && !staleMinute.includes('1 小时前'), staleMinute);

    setState('derived');
    await gotoLending();
    check('derived: a close ranking marks the estimate and still says no change', (await poolState()) === 'advised'
      && (await page.locator('[data-lending-reference]').count()) === 1
      && (await sentence()).includes('差不多'));
    check('derived: reset instants carry a tilde', (await cardRow('auth-01').innerText()).includes('~'));

    setState('missingVsZero');
    await gotoLending();
    check('missing-vs-zero: a reported zero renders as 0%', (await cardRow('auth-01').innerText()).includes('0%'));
    await openDisclosure('hold');
    check('missing-vs-zero: a missing share is named, not filled with a zero', (await page.locator('[data-lending-hold-card="auth-02"]').innerText()).includes('缺剩余'));

    setState('twoPools');
    await gotoLending();
    const poolOrder = () => page.locator('[data-lending-view="page"] [data-lending-pool]').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('data-lending-pool')));
    check('two-pools: the page keeps the payload pool order', JSON.stringify(await poolOrder()) === JSON.stringify(['codex:team', 'codex:pro']), JSON.stringify(await poolOrder()));
    await page.screenshot({ path: 'tmp/lending-proof/pool-order.png' });

    setState('mixed');
    await gotoLending();
    check('mixed: an advised pool precedes one with no ranking', JSON.stringify(await poolOrder()) === JSON.stringify(['codex:pro', 'codex:team'])
      && (await poolState(0)) === 'advised' && (await poolState(1)) === 'no_advice', JSON.stringify(await poolOrder()));

    setState('empty');
    await gotoLending();
    check('empty: the page states there is no pool', (await page.locator('[data-lending-pool]').count()) === 0 && (await poolText()).includes('暂无可拆借的同池凭证'));

    setState('single');
    await gotoLending();
    const singleSummary = page.locator('[data-lending-unpooled-summary]');
    check('single: the one seat is reported as not pooled', (await page.locator('[data-lending-pool]').count()) === 0
      && (await singleSummary.innerText()).includes('未进池 1 张'));
    await singleSummary.click();
    await page.locator('[data-lending-unpooled-group-toggle]').first().click();
    check('single: the reason and name are reachable', (await page.locator('[data-lending-unpooled-names] li').allInnerTexts()).includes('credential-01'));

    for (const mode of ['missing', 'null']) {
      clock.mode = mode;
      await gotoLending();
      check(`${mode}: the page degrades without a panel error`, (await page.locator('[data-lending-view="page"]').count()) === 1
        && (await page.locator('[data-lending-absent="true"]').count()) === 1);
    }

    // --- History: direct open, refresh, back/forward and the OAuth sort parameter.
    setState('base');
    await page.goto(`${base}/oauth-management?sort=reset-asc`, { waitUntil: 'domcontentloaded' });
    await page.locator('[data-testid="oauth-credential-record"]').first().waitFor({ timeout: 20000 });
    await page.locator('[data-lending-entry]').click();
    await page.waitForURL(/\/quota-lending$/);
    await page.locator('[data-lending-view="page"]').waitFor();
    check('history: an in-app open shows exactly one back control', (await page.getByRole('button', { name: /Go back|返回上一页/ }).count()) === 1);
    await page.goBack();
    await page.waitForURL(/\/oauth-management/);
    check('history: going back restores the OAuth sort parameter', new URL(page.url()).searchParams.get('sort') === 'reset-asc', page.url());
    await page.goForward();
    await page.waitForURL(/\/quota-lending$/);
    await page.reload({ waitUntil: 'domcontentloaded' });
    await page.locator('[data-lending-view="page"]').waitFor();
    check('history: forward and refresh keep the page rendered', (await page.locator('[data-lending-pool]').count()) === 1);
    const direct = await context.newPage();
    try {
      await direct.goto(`${base}/quota-lending`, { waitUntil: 'domcontentloaded' });
      await direct.locator('[data-lending-view="page"]').waitFor();
      check('history: a direct open has no back control', (await direct.getByRole('button', { name: /Go back|返回上一页/ }).count()) === 0);
    } finally {
      await direct.close();
    }

    // --- Phone width: the pool stacks and the workspace does not scroll sideways.
    setState('base');
    await gotoLending();
    for (const [width, height] of [[760, 900], [390, 844]]) {
      await page.setViewportSize({ width, height });
      await settleLayout(page);
      const geometry = await page.evaluate(() => ({
        doc: document.documentElement.scrollWidth, body: document.body.scrollWidth, viewport: window.innerWidth,
        cards: [...document.querySelectorAll('[data-lending-card]')].map((node) => Math.round(node.getBoundingClientRect().left)),
      }));
      check(`phone ${width}: no horizontal scroll`, geometry.doc <= geometry.viewport + 1 && geometry.body <= geometry.viewport + 1, JSON.stringify(geometry));
      check(`phone ${width}: the cards share one column`, geometry.cards.length >= 2 && new Set(geometry.cards).size === 1, JSON.stringify(geometry.cards));
      await page.screenshot({ path: `tmp/lending-proof/state-base-${width}.png` });
    }
    const phoneTag = await page.locator('[data-lending-read-only="true"]').first().evaluate((node) => {
      const box = node.getBoundingClientRect();
      const style = getComputedStyle(node);
      return {
        width: box.width,
        height: box.height,
        visible: style.display !== 'none' && style.visibility !== 'hidden' && box.width > 0 && box.height > 0,
        fontSize: Number.parseFloat(style.fontSize),
      };
    });
    check('phone 390: the read-only tag stays visible when the page title is hidden', phoneTag.visible === true && phoneTag.fontSize >= 13, JSON.stringify(phoneTag));
    await page.setViewportSize({ width: 390, height: 844 });
    await gotoOAuth();
    check('phone: the OAuth entry remains visible and inline', await page.locator('[data-lending-entry]').isVisible()
      && await page.locator('[data-lending-entry]').evaluate((node) => node.getBoundingClientRect().height <= 24));

    // --- The drawer block answers this card only, and its control moves to the priority field.
    await page.setViewportSize({ width: 1440, height: 900 });
    setState('base');
    await gotoOAuth();
    await page.locator('[data-testid="oauth-credential-record"][data-auth-index="auth-01"]').getByRole('button', { name: /^(Details|详情):/i }).click();
    const drawer = page.locator('.ant-drawer-open');
    const detail = drawer.locator('[data-lending-view="detail"]');
    await detail.waitFor({ timeout: 20000 });
    check('detail: the drawer answers this card without drawing the pool', (await detail.getAttribute('data-lending-detail-state')) === 'keep'
      && (await detail.locator('[data-lending-pool]').count()) === 0
      && (await detail.innerText()).includes('credential-01：建议设成 0，不用改'));
    const detailLink = detail.locator('[data-lending-detail-link]');
    check('detail: the whole-group link points at the mounted page', ((await detailLink.getAttribute('href')) || '').endsWith('/quota-lending') && (await detailLink.innerText()).includes('在额度拆借页查看整组'));
    check('detail: the panel keeps the read-only tag', (await drawer.innerText()).includes('只提示，不改路由'));
    await detail.locator('[data-lending-disclosure-toggle="evidence"]').click();
    const detailEvidence = await detail.locator('[data-lending-disclosure-body="evidence"]').innerText();
    check('detail: the basis omits the page-level read-only sentence',
      !/(不改 CPA 里任何设置|nothing in CPA is changed)/i.test(detailEvidence), detailEvidence);
    check('detail: the basis keeps the difference from reset advice',
      /(自然重置前的用法|natural reset)/i.test(detailEvidence), detailEvidence);
    await page.screenshot({ path: 'tmp/lending-proof/detail-card.png' });
    await detailLink.click();
    await page.waitForURL(/\/quota-lending$/);
    await page.locator('[data-lending-pool]').first().waitFor({ timeout: 20000 });
    check('detail: the link lands on the complete pool', (await page.locator('[data-lending-pool]').count()) === 1);

    // --- The drawer's hold note is the page's sentence, not an older hours-only form.
    for (const [state, auth, copy] of [
      ['stale', 'auth-02', '回满时刻已到'],
      ['stale', 'auth-03', '1 分钟前'],
      ['limited', 'auth-02', '周限额剩 5%，留作应急，不参与排名'],
    ]) {
      setState(state);
      await gotoOAuth();
      await page.locator(`[data-testid="oauth-credential-record"][data-auth-index="${auth}"]`).getByRole('button', { name: /^(Details|详情):/i }).click();
      const heldDrawer = page.locator('.ant-drawer-open [data-lending-view="detail"]');
      await heldDrawer.waitFor({ timeout: 20000 });
      const heldText = await heldDrawer.innerText();
      check(`detail: the held note agrees with the page (${state}/${auth})`, heldText.includes(copy) && !heldText.includes('1 小时前'), heldText);
      await page.locator('.ant-drawer-open .ant-drawer-close').click();
      await page.locator('.ant-drawer-open').waitFor({ state: 'hidden' });
    }

    // --- The way to the field: page link opens the drawer on configuration; the drawer control reopens it.
    setState('base');
    await gotoLending();
    await goLink('auth-02').click();
    await page.waitForURL(/\/oauth-management/);
    check('set-it: the link carries the credential in the address', new URL(page.url()).searchParams.get('open') === 'auth-02', page.url());
    const openDrawer = page.locator('.ant-drawer-open');
    await openDrawer.waitFor({ timeout: 20000 });
    await openDrawer.getByRole('tab', { name: '配置' }).waitFor({ timeout: 20000 });
    check('set-it: the drawer opens on the priority field', (await openDrawer.getByRole('tab', { name: '配置' }).getAttribute('aria-selected')) === 'true');
    check('set-it: the one-shot address parameter is consumed', !new URL(page.url()).searchParams.has('open'), page.url());
    await page.screenshot({ path: 'tmp/lending-proof/set-it-drawer.png' });
    await openDrawer.getByRole('tab', { name: '配额' }).click();
    const inDrawerDetail = openDrawer.locator('[data-lending-view="detail"]');
    await inDrawerDetail.waitFor({ timeout: 20000 });
    check('set-it: the changed card is the one named in the drawer', (await inDrawerDetail.getAttribute('data-lending-detail-state')) === 'change'
      && (await inDrawerDetail.innerText()).includes('credential-02：建议设成 -10'));
    await inDrawerDetail.locator('[data-lending-go="detail"]').click();
    await page.waitForFunction(() => {
      const tab = [...document.querySelectorAll('[role="tab"]')].find((node) => (node.textContent || '').includes('配置'));
      return tab?.getAttribute('aria-selected') === 'true';
    });
    check('set-it: the drawer control returns to the priority field', (await openDrawer.getByRole('tab', { name: '配置' }).getAttribute('aria-selected')) === 'true');

    // --- Review fixes: the header read-only tag, the shared disclosure style, the
    // trailing-slash nav key and the two empty states that must not read the same.
    setState('base');
    await gotoLending();
    const tagInfo = await page.evaluate(() => {
      const head = document.querySelector('.terminal-page-head');
      const host = document.querySelector('[data-lending-view="page"]');
      if (!head) return { found: false };
      const tag = [...head.querySelectorAll('*')].find((node) => node.children.length === 0 && (node.textContent || '').trim() === '只提示，不改路由');
      const title = head.querySelector('.terminal-title');
      const subtitle = head.querySelector('.terminal-subtitle');
      if (!tag || !title) return { found: false };
      const tagBox = tag.getBoundingClientRect();
      const titleBox = title.getBoundingClientRect();
      const subtitleBox = subtitle ? subtitle.getBoundingClientRect() : null;
      return {
        found: true,
        inHeader: head.contains(tag),
        sameHeading: tag.parentElement === title.parentElement,
        sameRow: tagBox.top >= titleBox.top - 1 && tagBox.bottom <= titleBox.bottom + 1,
        besideTitle: tagBox.left >= titleBox.right - 1,
        onPage: host ? host.contains(tag) : false,
        subtitleBelow: subtitleBox ? subtitleBox.top >= titleBox.bottom - 1 : false,
        tagFont: Number.parseFloat(getComputedStyle(tag).fontSize),
      };
    });
    check('header: the read-only tag is in the title row', tagInfo.found === true && tagInfo.inHeader === true && tagInfo.sameHeading === true && tagInfo.sameRow === true && tagInfo.besideTitle === true, JSON.stringify(tagInfo));
    check('header: the basis follows the title row', tagInfo.subtitleBelow === true, JSON.stringify(tagInfo));
    check('header: the read-only tag leaves the page body', tagInfo.onPage === false, JSON.stringify(tagInfo));
    check('header: the tag reads at least 13px', tagInfo.tagFont >= 13, JSON.stringify(tagInfo));

    const readDisclosure = () => page.evaluate(() => {
      const read = (selector) => {
        const node = document.querySelector(selector);
        if (!node) return null;
        const style = getComputedStyle(node);
        return {
          background: style.backgroundColor,
          color: style.color,
          font: `${style.fontFamily}|${style.fontSize}|${style.fontWeight}`,
          border: `${style.borderTopWidth}|${style.borderTopStyle}`,
        };
      };
      return { summary: read('[data-lending-unpooled-summary]'), group: read('[data-lending-unpooled-group-toggle]') };
    });
    await page.locator('[data-lending-unpooled-summary]').click();
    await page.locator('[data-lending-unpooled-group-toggle]').first().waitFor();
    const disclosure = await readDisclosure();
    check('merged: the unpooled summary is not the browser default button', disclosure.summary !== null
      && disclosure.summary.background === 'rgba(0, 0, 0, 0)' && disclosure.summary.border === '0px|none', JSON.stringify(disclosure.summary));
    check('merged: the group row shares the summary typography', disclosure.group !== null && disclosure.group.font === disclosure.summary.font, JSON.stringify(disclosure));

    // --- English copy after a new document loads with the stored language.
    await page.addInitScript(() => window.localStorage.setItem('omc-lang', 'en'));
    setState('base');
    await gotoOAuth();
    const englishEntry = await entryState();
    check('English: one change uses the singular count', englishEntry.kind === 'change' && englishEntry.text === 'Quota lending · 1 to change', JSON.stringify(englishEntry));
    await page.locator('[data-lending-entry]').click();
    await page.waitForURL(/\/quota-lending$/);
    await page.locator('[data-lending-view="page"]').waitFor();
    const englishText = await poolText();
    const englishHeader = await page.locator('.terminal-page-head').innerText();
    check('English: the page, the verdict and the read-only note are translated', englishHeader.includes('Quota lending advice')
      && englishText.includes('Set credential-02 to -10') && englishText.includes('No change')
      && englishHeader.includes('Advice only, routing unchanged'), `${englishHeader} :: ${englishText.slice(0, 300)}`);
    await page.screenshot({ path: 'tmp/lending-proof/state-base-en.png' });
    setState('twoPools');
    await gotoOAuth();
    const pluralEntry = await entryState();
    check('English: two changes use the plural count', pluralEntry.kind === 'change' && pluralEntry.text === 'Quota lending · 2 to change', JSON.stringify(pluralEntry));
    await page.screenshot({ path: 'tmp/lending-proof/entry-two-pools-en.png' });

    // --- Typography and contrast: every main-screen tag is at the secondary size or larger,
    // and every text clears WCAG AA against what it is painted on. The same measurement runs
    // in both themes; the floor is printed so a reviewer sees the number without opening a log.
    const openFolds = async () => {
      for (const id of ['evidence', 'hold', 'outside', 'no-advice']) {
        const toggle = page.locator(`[data-lending-disclosure-toggle="${id}"]`);
        if ((await toggle.count()) === 0) continue;
        const body = page.locator(`[data-lending-disclosure-body="${id}"]`);
        if ((await body.count()) === 0) await toggle.first().click();
      }
      const summary = page.locator('[data-lending-unpooled-summary]');
      if ((await summary.count()) > 0 && (await summary.getAttribute('aria-expanded')) === 'false') await summary.click();
      const groups = page.locator('[data-lending-unpooled-group-toggle]');
      const groupCount = await groups.count();
      for (let index = 0; index < groupCount; index += 1) {
        const group = groups.nth(index);
        if ((await group.getAttribute('aria-expanded')) === 'false') await group.click();
      }
    };
    const readType = () => page.evaluate(() => {
      const parseColor = (value) => {
        const rgb = value.match(/^rgba?\(([^)]+)\)$/i);
        if (rgb) {
          const parts = rgb[1].split(/[,\s/]+/).filter(Boolean);
          const alpha = parts[3];
          return {
            r: Number.parseFloat(parts[0]),
            g: Number.parseFloat(parts[1]),
            b: Number.parseFloat(parts[2]),
            a: alpha === undefined ? 1 : (alpha.endsWith('%') ? Number.parseFloat(alpha) / 100 : Number.parseFloat(alpha)),
          };
        }
        const srgb = value.match(/^color\(srgb\s+([^)]+)\)$/i);
        if (srgb) {
          const parts = srgb[1].split(/[\s/]+/).filter(Boolean);
          const alpha = parts[3];
          return {
            r: Number.parseFloat(parts[0]) * 255,
            g: Number.parseFloat(parts[1]) * 255,
            b: Number.parseFloat(parts[2]) * 255,
            a: alpha === undefined ? 1 : (alpha.endsWith('%') ? Number.parseFloat(alpha) / 100 : Number.parseFloat(alpha)),
          };
        }
        throw new Error(`unreadable colour: ${value}`);
      };
      const linear = (channel) => {
        const value = channel / 255;
        return value <= 0.03928 ? value / 12.92 : Math.pow((value + 0.055) / 1.055, 2.4);
      };
      const luminance = ({ r, g, b }) => 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
      const contrastRatio = (first, second) => {
        const a = luminance(first);
        const b = luminance(second);
        return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
      };
      const backgroundOf = (element) => {
        const layers = [];
        for (let node = element; node; node = node.parentElement) {
          const color = parseColor(getComputedStyle(node).backgroundColor);
          if (color.a > 0) layers.push(color);
          if (color.a === 1) break;
        }
        let result = { r: 255, g: 255, b: 255, a: 1 };
        for (let index = layers.length - 1; index >= 0; index -= 1) {
          const layer = layers[index];
          result = {
            r: layer.r * layer.a + result.r * (1 - layer.a),
            g: layer.g * layer.a + result.g * (1 - layer.a),
            b: layer.b * layer.a + result.b * (1 - layer.a),
            a: 1,
          };
        }
        return result;
      };
      const host = document.querySelector('.terminal-page');
      if (!host) return { error: 'no .terminal-page' };
      const readings = [];
      for (const element of host.querySelectorAll('*')) {
        if (element.closest('[aria-hidden="true"]')) continue;
        const style = getComputedStyle(element);
        if (style.display === 'none' || style.visibility === 'hidden' || Number(style.opacity) === 0) continue;
        const hasText = [...element.childNodes].some((node) => node.nodeType === Node.TEXT_NODE && (node.textContent || '').trim() !== '');
        if (!hasText) continue;
        const box = element.getBoundingClientRect();
        if (box.width === 0 || box.height === 0) continue;
        const fontSize = Number.parseFloat(style.fontSize);
        const fontWeight = Number.parseInt(style.fontWeight, 10) || 400;
        const isLarge = fontSize >= 24 || (fontSize >= 18.66 && fontWeight >= 700);
        const foreground = parseColor(style.color);
        const background = backgroundOf(element);
        readings.push({
          text: (element.textContent || '').trim().slice(0, 40),
          fontSize,
          fontWeight,
          color: style.color,
          background: `rgb(${Math.round(background.r)}, ${Math.round(background.g)}, ${Math.round(background.b)})`,
          contrast: Number(contrastRatio(foreground, background).toFixed(2)),
          required: isLarge ? 3 : 4.5,
        });
      }
      if (readings.length === 0) return { error: 'no text measured' };
      return {
        count: readings.length,
        minFont: Math.min(...readings.map((entry) => entry.fontSize)),
        minContrast: Math.min(...readings.map((entry) => entry.contrast)),
        failures: readings.filter((entry) => entry.contrast + 0.005 < entry.required),
      };
    });
    const measureTheme = async (theme, states) => {
      await page.evaluate((value) => window.localStorage.setItem('omc-theme', value), theme);
      const runs = [];
      for (const state of states) {
        setState(state);
        await gotoLending();
        await page.waitForFunction((mode) => document.documentElement.getAttribute('data-theme-mode') === mode, theme === 'omc-light' ? 'light' : 'dark');
        await openFolds();
        await settleLayout(page);
        runs.push({ state, ...(await readType()) });
      }
      return runs;
    };
    const summarizeType = (runs) => ({
      count: runs.reduce((sum, run) => sum + (run.count || 0), 0),
      minFont: Math.min(...runs.map((run) => run.minFont ?? Infinity)),
      minContrast: Math.min(...runs.map((run) => run.minContrast ?? Infinity)),
      failures: runs.flatMap((run) => (run.failures || []).map((failure) => ({ state: run.state, ...failure }))),
    });
    const typeStates = ['base', 'hold', 'outside', 'noAdvice', 'cannotEdit'];
    const lightType = summarizeType(await measureTheme('omc-light', typeStates));
    const darkType = summarizeType(await measureTheme('omc-dark', typeStates));
    await page.evaluate(() => window.localStorage.setItem('omc-theme', 'omc-light'));
    console.log(`[quota-lending] light: min font ${lightType.minFont}px, min contrast ${lightType.minContrast}:1 across ${lightType.count} text nodes`);
    console.log(`[quota-lending] dark: min font ${darkType.minFont}px, min contrast ${darkType.minContrast}:1 across ${darkType.count} text nodes`);
    check('type/light: every main-screen tag is at least 13px', lightType.minFont >= 13, `min font ${lightType.minFont}px`);
    check('type/light: every main-screen text clears WCAG AA', lightType.failures.length === 0, JSON.stringify(lightType.failures.slice(0, 6)));
    check('type/dark: every main-screen tag is at least 13px', darkType.minFont >= 13, `min font ${darkType.minFont}px`);
    check('type/dark: every main-screen text clears WCAG AA', darkType.failures.length === 0, JSON.stringify(darkType.failures.slice(0, 6)));

    // --- The visible clock: a sub-hour countdown moves without a new quota request.
    // A controlled clock keeps the minute-granularity copy deterministic: the page's own
    // 15-second tick must fire, and a real 31-second wait only races the minute boundary.
    await page.clock.install({ time: Date.now() });
    setState('subHour');
    await gotoLending();
    const clockBefore = await page.locator('[data-lending-card="auth-01"]').innerText();
    const requestsBefore = quotaRequests;
    await page.clock.fastForward(31_000);
    const clockAfter = await page.locator('[data-lending-card="auth-01"]').innerText();
    await page.clock.resume();
    check('clock: the countdown advances on the page clock', clockAfter !== clockBefore, 'the countdown stayed at one reading');
    check('clock: no extra quota request during the tick', quotaRequests === requestsBefore, `${requestsBefore} -> ${quotaRequests}`);

    // --- Read-only: no apply/write/route/entitlement control and no write request.
    setState('base');
    await gotoLending();
    const controls = await page.locator('[data-lending-view="page"] button').evaluateAll((nodes) => nodes.map((node) => (node.textContent || '').trim()));
    check('read-only: no apply, save, switch or consume control', controls.every((text) => !/apply|save|switch|consume|应用|保存|切换|消耗/i.test(text)), controls.join(' | '));
    check('read-only: no editable field in the advice', (await page.locator('[data-lending-view="page"] input, [data-lending-view="page"] textarea, [data-lending-view="page"] select').count()) === 0);
    check('read-only: the full boundary note stays inside the basis', !/(不改 CPA 里任何设置|nothing in CPA is changed)/i.test(await poolText()));
    await openDisclosure('evidence');
    check(
      'read-only: the basis names the boundary',
      /(不改 CPA 里任何设置|nothing in CPA is changed)/i.test(await page.locator('[data-lending-disclosure-body="evidence"]').innerText()),
    );
    check('read-only: no quota, lending or credential-field write was issued', writes.length === 0, writes.join(' | '));
  } finally {
    page.off('request', onRequest);
    page.off('request', countQuota);
    await context.unroute('**/management/quota', quotaHandler);
  }
}
