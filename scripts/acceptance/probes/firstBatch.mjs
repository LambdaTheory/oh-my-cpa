import { CONDITION_TIMEOUT_MS, until, settleLayout } from '../harness.mjs';
import { oauthManagementFixtures } from './oauthManagement.mjs';
import { chartDashboard, chartTokenHeatmap } from './dashboardFixtures.mjs';

/**
 * The first-batch OMC management surfaces on the development server.
 *
 * Three claims only a real engine can establish, each on the surface the operator
 * actually drives:
 *
 *   - Lane A opens the account-auto-management drawer from the OAuth workspace: an
 *     empty roster is offered the gateway's live credentials for a one-time batch
 *     adoption, an unverified provider joins as monitor-only while an uncontrollable
 *     candidate is disabled and left out of the adopted set,
 *     and a planned account exposes both the retry cancellation and the hand-filled
 *     recovery time.
 *   - Lane B adds and edits an Ollama account: the cookie is accepted on write,
 *     omitted on an untouched edit, never echoed back, and a paused account with no
 *     snapshot reads as an unknown quota rather than a fabricated zero.
 *   - Lane C keeps the Feishu panel inert when the environment forces it off, leaves
 *     the stored secret in place when the field is blank, and proves the test card is
 *     a two-step action that sends nothing until the operator confirms.
 *
 * The routes are the handler-certified wire shapes: the account-automation DTOs
 * (`internal/api/account_automation.go`), the Ollama account/quota DTOs
 * (`internal/api/ollama_accounts.go`) and the Feishu settings/status/test DTOs
 * (`internal/api/feishu.go`). No request leaves the console's own origin; the test
 * card POST is fulfilled locally and never reaches Feishu.
 */

// ── Lane A: account auto-management ──────────────────────────────────────────

const VERIFIED_CLAUDE = 'cand-claude-a';
const VERIFIED_CODEX = 'cand-codex-b';
const MONITOR_ONLY_UNVERIFIED = 'cand-unverified-c';
const BLOCKED_UNCONTROLLABLE = 'cand-runtime-only-d';

const automationSettings = (overrides = {}) => ({
  web_enabled: false,
  enabled: false,
  source: 'default_off',
  master_from_env: false,
  accounts: 0,
  ...overrides,
});

/**
 * Loads a console route and settles the dev server before any assertion.
 *
 * The Vite development server may reload the document once while it optimizes the
 * OAuth workspace's modules on their first request. Reloading once ourselves lands
 * that optimization on a load we own; the second load is served from the same graph,
 * so the reload cannot arrive in the middle of a flow and drop the drawer.
 */
async function gotoConsole(page, base, route, readySelector) {
  await page.goto(`${base}${route}`, { waitUntil: 'domcontentloaded' });
  await page.locator(readySelector).waitFor({ state: 'visible' });
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator(readySelector).waitFor({ state: 'visible' });
}

/** The automation drawer, re-opened after a reload if it was dropped. */
async function openAutomationDrawer(page) {
  await page.locator('.oauth-management-page').waitFor({ state: 'visible' });
  const drawer = page.locator('.ant-drawer-open').filter({ hasText: 'Account Automation' });
  if (await drawer.count() === 0) {
    await page.getByTestId('oauth-management-automation-open').click();
    await drawer.waitFor({ state: 'visible' });
  }
  return drawer;
}

async function dismissNotification(page, title, label) {
  const notification = page.locator('.omc-toast').filter({ hasText: title });
  await notification.waitFor({ state: 'visible', timeout: CONDITION_TIMEOUT_MS });
  await until(async () => {
    const className = await notification.getAttribute('class') ?? '';
    return !/ant-notification-fade-(?:appear|enter)/.test(className);
  }, { label: `${label} enter motion to settle` });
  await notification.getByRole('button', { name: 'Close', exact: true }).click();
  await notification.waitFor({ state: 'detached', timeout: CONDITION_TIMEOUT_MS });
}

function candidate(authIndex, { provider, name, label, disabled, controllable, adopted, adoptable, autoDisableSupported, reason }) {
  return {
    auth_index: authIndex,
    provider,
    name,
    account_label: label,
    disabled,
    controllable,
    adopted,
    adoptable,
    adoption_blocked_reason: reason || undefined,
    auto_disable_supported: autoDisableSupported,
  };
}

/** One module-scoped state per scenario, so a full-catalog run cannot bleed between them. */
const automationState = {
  phase: 'first',
  patches: {},
  removed: [],
  settings: automationSettings(),
  adopt: null,
  cancel: null,
  manualReset: null,
};

function automationListBody(state) {
  const adopted = state.phase !== 'first';
  const body = {
    settings: { ...state.settings, accounts: adopted ? 4 : 0 },
    accounts: adopted
      ? [
        {
          auth_index: VERIFIED_CLAUDE,
          provider: 'claude',
          name: VERIFIED_CLAUDE,
          account_label: `${VERIFIED_CLAUDE}: a@example.test`,
          is_adopted: true,
          is_enabled: true,
          state: 'waiting',
          monitor_reason: 'backoff',
          auto_disable_supported: true,
          disabled_by_automation: true,
          recovery_at_ms: Date.now() - 60_000,
          recovery_status: 'overdue',
          attempt_count: 2,
          next_attempt_at_ms: Date.now() + 300_000,
          has_weekly_evidence: true,
          weekly_used_percent: 100,
          weekly_reset_at_ms: Date.now() + 86_400_000,
          intent_version: 3,
        },
        {
          auth_index: VERIFIED_CODEX,
          provider: 'codex',
          name: 'codex-credential-with-a-very-long-descriptive-file-name-for-layout.json',
          account_label: 'b-with-a-very-long-account-name-for-ellipsis@example.test',
          is_adopted: true,
          is_enabled: true,
          state: state.manualReset ? 'waiting' : 'monitoring',
          monitor_reason: state.manualReset ? 'recovery_scheduled' : 'missing_reset',
          auto_disable_supported: true,
          disabled_by_automation: false,
          recovery_at_ms: state.manualReset?.reset_at_ms,
          recovery_status: state.manualReset ? 'scheduled' : undefined,
          attempt_count: 0,
          has_weekly_evidence: true,
          weekly_used_percent: 100,
          intent_version: 4,
        },
        {
          auth_index: MONITOR_ONLY_UNVERIFIED,
          provider: 'antigravity',
          name: 'Unverified C',
          account_label: 'c@example.test',
          is_adopted: true,
          is_enabled: true,
          state: 'monitoring',
          monitor_reason: 'unverified_account_weekly_capability',
          auto_disable_supported: false,
          disabled_by_automation: false,
          // A stale timestamp from an older server is history without an active
          // recovery status and must not render as an expected recovery.
          recovery_at_ms: Date.now() - 86_400_000,
          attempt_count: 0,
          has_weekly_evidence: false,
          intent_version: 1,
        },
        {
          auth_index: 'historical-idle',
          provider: 'claude',
          account_label: 'Idle account',
          is_adopted: true,
          is_enabled: false,
          state: 'idle',
          monitor_reason: 'switch_off',
          auto_disable_supported: true,
          disabled_by_automation: false,
          attempt_count: 0,
          has_weekly_evidence: false,
          intent_version: 1,
        },
      ]
      : [],
    candidates: [
      candidate(VERIFIED_CLAUDE, { provider: 'claude', name: VERIFIED_CLAUDE, label: `${VERIFIED_CLAUDE}: a@example.test`, disabled: false, controllable: true, adopted, adoptable: true, autoDisableSupported: true }),
      candidate(VERIFIED_CODEX, { provider: 'codex', name: 'codex-credential-with-a-very-long-descriptive-file-name-for-layout.json', label: 'b@example.test', disabled: true, controllable: true, adopted, adoptable: true, autoDisableSupported: true }),
      candidate(MONITOR_ONLY_UNVERIFIED, { provider: 'antigravity', name: 'Unverified C', label: 'c@example.test', disabled: false, controllable: true, adopted, adoptable: true, autoDisableSupported: false }),
      candidate(BLOCKED_UNCONTROLLABLE, { provider: 'antigravity', name: '', label: 'runtime-only@example.test', disabled: false, controllable: false, adopted: false, adoptable: false, autoDisableSupported: false, reason: 'no_controllable_credential' }),
    ],
  };
  body.accounts = body.accounts.filter(account => !automationState.removed.includes(account.auth_index)).map(account => ({ burn_credit: false, removed: false, ...account, ...(automationState.patches[account.auth_index] ?? {}) }));
  body.candidates = body.candidates.map(candidate => ({ ...candidate, adopted: body.accounts.some(account => account.auth_index === candidate.auth_index) }));
  return body;
}

export function accountAutomationFixtures() {
  return [
    [(url, method) => method === 'PUT' && /account-automation[/]accounts[/][^/]+$/.test(url.pathname), (url, method, request) => {
      const key = decodeURIComponent(url.pathname.split('/').at(-1));
      automationState.patches[key] = { ...automationState.patches[key], ...JSON.parse(request.postData()) };
      return automationListBody(automationState).accounts.find(account => account.auth_index === key);
    }],
    [(url, method) => method === 'POST' && url.pathname.endsWith('/account-automation/unadopt'), (url, method, request) => {
      const keys = JSON.parse(request.postData()).auth_indexes;
      automationState.removed.push(...keys);
      return { removed: keys.length };
    }],
    [(url, method) => method === 'POST' && /account-automation[/]accounts[/][^/]+[/]unadopt$/.test(url.pathname), url => {
      const key = decodeURIComponent(url.pathname.split('/').at(-2));
      const account = automationListBody(automationState).accounts.find(account => account.auth_index === key);
      automationState.removed.push(key);
      return { ...account, is_adopted: false, removed: true };
    }],
    ...oauthManagementFixtures.routes,
    [
      (url, method) => method === 'GET' && url.pathname.endsWith('/account-automation/settings'),
      () => automationListBody(automationState).settings,
    ],
    [
      (url, method) => method === 'GET' && url.pathname.endsWith('/account-automation'),
      () => automationListBody(automationState),
    ],
    [
      (url, method) => method === 'POST' && url.pathname.endsWith('/account-automation/adopt'),
      (url, method, request) => {
        automationState.adopt = JSON.parse(request.postData() ?? '{}');
        // The roster exists after the one-time selection, so the drawer's refetch can
        // show the planned accounts the second half of the scenario drives.
        automationState.phase = 'adopted';
        return { adopted: (automationState.adopt.auth_indexes ?? []).length };
      },
    ],
    [
      (url, method) => method === 'POST' && /\/account-automation\/accounts\/[^/]+\/cancel$/.test(url.pathname),
      url => {
        automationState.cancel = decodeURIComponent(url.pathname.split('/').at(-2));
        return automationListBody(automationState).accounts[0];
      },
    ],
    [
      (url, method) => method === 'POST' && /\/account-automation\/accounts\/[^/]+\/manual-reset$/.test(url.pathname),
      (url, method, request) => {
        automationState.manualReset = JSON.parse(request.postData() ?? '{}');
        return automationListBody(automationState).accounts[1];
      },
    ],
  ];
}

export async function accountAutomation({ base, page, check }) {
  Object.assign(automationState, { phase: 'first', adopt: null, cancel: null, manualReset: null, patches: {}, removed: [] });
  await gotoConsole(page, base, '/oauth-management', '.oauth-management-page');
  const drawer = await openAutomationDrawer(page);
  const checkStatAlignment = async (surface, label) => {
    const stats = await surface.getByTestId('automation-stats').locator(':scope > div').evaluateAll(elements => elements.map(element => {
      const label = element.querySelector('span');
      const value = element.querySelector('strong');
      return { top: element.getBoundingClientRect().top, valueTop: value.getBoundingClientRect().top, labelHeight: label.getBoundingClientRect().height, lineHeight: parseFloat(getComputedStyle(label).lineHeight) };
    }));
    check(label + ' statistic labels stay on one line with aligned values', stats.length === 6 && stats.every(stat => stat.labelHeight <= stat.lineHeight + 1 && stats.filter(other => Math.abs(other.top - stat.top) < 1).every(other => Math.abs(other.valueTop - stat.valueTop) < 1)), JSON.stringify(stats));
  };
  const batch = drawer.getByRole('button', { name: 'Batch Operations', exact: true });
  await until(() => batch.isEnabled(), { label: 'live inventory ready' });
  await batch.click();
  const dialog = page.locator('.ant-modal:visible').filter({ hasText: 'Batch Operations' });
  const submit = dialog.getByRole('button', { name: 'Confirm Adoption', exact: true });
  check('first adoption starts empty with immediate automation enabled', await submit.isDisabled() && await dialog.getByRole('checkbox', { name: /immediately/ }).isChecked());
  check('uncontrollable credentials cannot be selected', await dialog.getByRole('checkbox', { name: 'runtime-only@example.test', exact: true }).isDisabled());
  check('monitor-only adoption names its capability limit', (await dialog.innerText()).includes('Capability unverified; monitor-only after adoption'));
  check('adoption labels hide standalone and prefixed internal indexes', !(await dialog.innerText()).includes(VERIFIED_CLAUDE) && await dialog.getByRole('checkbox', { name: 'a@example.test', exact: true }).count() === 1);
  await dialog.getByRole('button', { name: 'Select all', exact: true }).click();
  await dialog.getByRole('textbox').fill('Synthetic browser batch');
  await page.screenshot({ path: 'tmp/issue-69/batch-adopt-desktop-en.png' });
  await submit.click();
  await until(() => automationState.adopt !== null, { label: 'adoption request' });
  check('batch adoption sends eligible indexes and operator options', automationState.adopt.auth_indexes.length === 3 && !automationState.adopt.auth_indexes.includes(BLOCKED_UNCONTROLLABLE) && automationState.adopt.enable && automationState.adopt.note === 'Synthetic browser batch');
  const cards = drawer.getByTestId('automation-account');
  await until(async () => await cards.count() === 4, { label: 'refreshed roster' });
  const card = label => drawer.getByRole('article', { name: label, exact: true });
  const longName = 'b-with-a-very-long-account-name-for-ellipsis@example.test';
  check('the managed roster retains friendly account labels without internal indexes', await card('a@example.test').count() === 1 && !(await cards.allTextContents()).join('').includes(VERIFIED_CLAUDE));
  check('cleanup is silent and footer only reports roster and inventory totals', (await drawer.innerText()).includes('Adopted 4 / Gateway 4 credentials') && !(await drawer.innerText()).includes('absent from gateway'));
  check('every account has exactly one status', await cards.evaluateAll(elements => elements.every(element => element.querySelectorAll('.status-label').length === 1)));
  check('monitor-only reason is singular', (await card('c@example.test').innerText()).includes('Capability unverified'));
  check('stale completed recovery timestamps stay hidden', !(await card('c@example.test').innerText()).includes('Recovery at:'));
  await page.setViewportSize({ width: 1440, height: 1050 });
  await settleLayout(page);
  const geometry = await cards.evaluateAll(elements => elements.map(element => {
    const rect = testid => { const r = element.querySelector('[data-testid="' + testid + '"]').getBoundingClientRect(); return { x: r.x, right: r.right }; };
    const label = element.querySelector('strong');
    return { identity: rect('automation-identity'), status: rect('automation-status'), actions: rect('automation-actions'), label: { width: label.clientWidth, scroll: label.scrollWidth, title: label.title, overflow: getComputedStyle(label).textOverflow } };
  }));
  check('desktop columns align across all rows without overlap', geometry.every(row => Math.abs(row.identity.x - geometry[0].identity.x) < 1 && Math.abs(row.status.x - geometry[0].status.x) < 1 && Math.abs(row.actions.x - geometry[0].actions.x) < 1 && row.identity.right <= row.status.x && row.status.right <= row.actions.x), JSON.stringify(geometry));
  check('long identity ellipsizes while preserving full accessible title', geometry[1].label.scroll > geometry[1].label.width && geometry[1].label.overflow === 'ellipsis' && geometry[1].label.title === longName);
  const logos = await drawer.getByTestId('automation-identity').locator('img').evaluateAll(images => images.map(image => ({ source: image.src, loaded: image.complete && image.naturalWidth > 0 })));
  await until(async () => await drawer.getByTestId('automation-identity').locator('img').evaluateAll(images => images.every(image => image.complete && image.naturalWidth > 0)), { label: 'local provider artwork loaded' });
  check('provider artwork uses bundled Codex Claude and Antigravity SVGs', ['codex', 'claude', 'antigravity'].every(provider => logos.some(logo => logo.source.toLowerCase().includes(provider + '-color.svg'))), JSON.stringify(logos));
  await until(async () => await page.locator('.omc-toast').count() === 0, { label: 'feedback clear for capture' });
  await checkStatAlignment(drawer, 'Desktop EN');
  await page.screenshot({ path: 'tmp/issue-69/automation-desktop-en.png' });
  await card('a@example.test').getByRole('button', { name: 'Credit exemption for a@example.test', exact: true }).click();
  await until(async () => await card('a@example.test').getByRole('button', { name: 'Credit exemption for a@example.test', exact: true }).getAttribute('aria-pressed') === 'true', { label: 'Credit patch refetched' });
  check('Credit writes only its independent field and preserves error state', JSON.stringify(automationState.patches[VERIFIED_CLAUDE]) === JSON.stringify({ burn_credit: true }) && (await card('a@example.test').innerText()).includes('Error'));
  await card('a@example.test').getByRole('button', { name: 'Details', exact: true }).click();
  check('details disclose overdue recovery and retry', (await card('a@example.test').innerText()).includes('Recovery time passed; waiting to retry') && (await card('a@example.test').innerText()).includes('Next attempt:'));
  await card(longName).getByRole('button', { name: 'Details', exact: true }).click();
  await card(longName).getByRole('button', { name: 'Set Manual Reset', exact: true }).click();
  const reset = page.locator('.ant-modal:visible').filter({ hasText: 'Set One-time Recovery Time' });
  await reset.locator('input[type="datetime-local"]').fill('2099-01-01T10:00');
  await reset.getByRole('button', { name: /^(OK|Confirm)$/ }).click();
  await until(() => automationState.manualReset !== null, { label: 'manual recovery submitted' });
  check('manual reset remains a future absolute instant', automationState.manualReset.reset_at_ms > Date.now());
  await until(async () => await card(longName).getByRole('button', { name: 'Unadopt', exact: true }).isEnabled(), { label: 'manual reset refresh complete' });
  await card('a@example.test').getByRole('button', { name: 'Cancel Retry', exact: true }).click();
  await page.locator('.ant-modal-confirm:visible').getByRole('button', { name: 'OK', exact: true }).click();
  await until(() => automationState.cancel === VERIFIED_CLAUDE, { label: 'retry cancellation recorded' });
  await page.setViewportSize({ width: 390, height: 844 });
  await settleLayout(page);
  const phoneGeometry = await cards.evaluateAll(elements => elements.map(element => {
    const r = element.getBoundingClientRect();
    const actions = element.querySelector('[data-testid="automation-actions"]').getBoundingClientRect();
    const identity = element.querySelector('[data-testid="automation-identity"]').getBoundingClientRect();
    const status = element.querySelector('[data-testid="automation-status"]').getBoundingClientRect();
    return { left: r.left, right: r.right, scroll: element.scrollWidth, width: element.clientWidth, stacked: actions.top >= status.bottom && status.top >= identity.bottom };
  }));
  check('phone rows stack identity state and reachable actions without horizontal clipping', phoneGeometry.every(row => row.left >= 0 && row.right <= 390 && row.scroll <= row.width + 1 && row.stacked), JSON.stringify(phoneGeometry));
  await card('a@example.test').scrollIntoViewIfNeeded();
  await until(async () => await page.locator('.omc-toast').count() === 0, { label: 'feedback clear before phone capture' });
  await page.screenshot({ path: 'tmp/issue-69/automation-mobile-details-en.png' });
  await card('a@example.test').getByRole('button', { name: 'Details', exact: true }).click();
  await card(longName).getByRole('button', { name: 'Details', exact: true }).click();
  await drawer.locator('.ant-drawer-body').evaluate(element => { element.scrollTop = 0; });
  await settleLayout(page);
  await checkStatAlignment(drawer, 'Phone EN');
  await page.screenshot({ path: 'tmp/issue-69/automation-mobile-en.png' });
  await batch.scrollIntoViewIfNeeded(); await batch.click();
  await dialog.locator('.ant-segmented-item').filter({ hasText: 'Unadopt' }).click();
  check('unadopt dialog explicitly preserves gateway state', (await dialog.innerText()).includes('Gateway credentials keep their current enabled or disabled state'));
  await dialog.getByRole('checkbox', { name: 'Idle account', exact: true }).check();
  await page.screenshot({ path: 'tmp/issue-69/batch-unadopt-mobile-en.png' });
  await dialog.getByRole('button', { name: 'Confirm Unadopt', exact: true }).click();
  await until(async () => await cards.count() === 3, { label: 'batch unadopt refresh' });
  check('unadopt removed only the selected membership', automationState.removed.length === 1 && automationState.removed[0] === 'historical-idle');
  await drawer.locator('.ant-drawer-close').click();
  await drawer.waitFor({ state: 'hidden' });
  const more = page.locator('.terminal-page-head .terminal-page-actions').getByRole('button', { name: 'More', exact: true });
  await more.waitFor({ state: 'visible' });
  await more.click();
  const automationEntry = page.getByTestId('oauth-management-automation-open-mobile');
  check('account automation and Ollama management remain reachable from the phone toolbar', await automationEntry.isVisible() && await page.getByTestId('oauth-management-ollama-open-mobile').isVisible());
  await automationEntry.click();
  await drawer.waitFor({ state: 'visible' });
  check('the phone automation entry opens the managed roster', await card('a@example.test').isVisible());
  await page.setViewportSize({ width: 1440, height: 1050 });
  await page.evaluate(() => localStorage.setItem('omc-lang', 'zh-CN'));
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator('.oauth-management-page').waitFor({ state: 'visible' });
  await page.getByTestId('oauth-management-automation-open').click();
  const chinese = page.locator('.ant-drawer-open').filter({ hasText: '账号自动管理' });
  await chinese.getByRole('article', { name: 'a@example.test', exact: true }).waitFor({ state: 'visible' });
  await settleLayout(page);
  await checkStatAlignment(chinese, 'Desktop ZH');
  await page.screenshot({ path: 'tmp/issue-69/automation-desktop-zh.png' });
  await page.setViewportSize({ width: 390, height: 844 });
  await settleLayout(page);
  await checkStatAlignment(chinese, 'Phone ZH');
  await page.screenshot({ path: 'tmp/issue-69/automation-mobile-zh.png' });
}


// ── Lane B: Ollama accounts ──────────────────────────────────────────────────

const OLLAMA_ACCOUNT_ID = 'ollama-fixture-1';
const OLLAMA_PAUSED_ID = 'ollama-fixture-2';
const OLLAMA_COOKIE = 'fixture-session-cookie-value';

const ollamaState = {
  created: null,
  createdAccount: null,
  updated: null,
  putCount: 0,
  cpaToggleCalls: 0,
};

function ollamaAccountList() {
  const base = [
    {
      id: OLLAMA_ACCOUNT_ID,
      label: 'My Ollama',
      enabled: true,
      has_cookie: true,
      status: 'ok',
      last_success_at_ms: Date.now() - 60_000,
      attempt_count: 0,
      created_at_ms: Date.now() - 86_400_000,
      updated_at_ms: Date.now() - 60_000,
    },
    {
      id: OLLAMA_PAUSED_ID,
      label: 'Paused Ollama',
      enabled: false,
      has_cookie: true,
      status: 'unknown',
      attempt_count: 1,
      created_at_ms: Date.now() - 3_600_000,
      updated_at_ms: Date.now() - 3_600_000,
    },
  ];
  return ollamaState.createdAccount ? [...base, ollamaState.createdAccount] : base;
}

function ollamaQuota() {
  return {
    accounts: [
      {
        account_id: OLLAMA_ACCOUNT_ID,
        label: 'My Ollama',
        enabled: true,
        status: 'ok',
        observed_at_ms: Date.now() - 60_000,
        quota: {
          auth_index: `ollama:${OLLAMA_ACCOUNT_ID}`,
          name: 'My Ollama',
          type: 'ollama',
          provider: 'ollama',
          disabled: false,
          status: 'healthy',
          observed_at_ms: Date.now() - 60_000,
          windows: [{ id: 'weekly', label: '7d', scope: 'standard', used_percent: 45.5, remaining_percent: 54.5 }],
          recommendation: { status: 'healthy', priority: 'none', action: 'none', reason: '' },
          capabilities: { refresh_supported: false, clear_cooldown_supported: false, reset_credit_supported: false },
          quota_exceeded: false,
        },
      },
      // No snapshot: the first version must show an unknown quota, never a fabricated 0 or 100.
      {
        account_id: OLLAMA_PAUSED_ID,
        label: 'Paused Ollama',
        enabled: false,
        status: 'unknown',
      },
    ],
  };
}

export function ollamaAccountFixtures() {
  return [
    ...oauthManagementFixtures.routes,
    [(url, method) => method === 'GET' && url.pathname.endsWith('/ollama/accounts'), () => ({ accounts: ollamaAccountList() })],
    [(url, method) => method === 'GET' && url.pathname.endsWith('/ollama/quota'), () => ollamaQuota()],
    [
      (url, method) => method === 'POST' && url.pathname.endsWith('/ollama/accounts'),
      (url, method, request) => {
        ollamaState.created = JSON.parse(request.postData() ?? '{}');
        ollamaState.createdAccount = {
          id: 'ollama-fixture-3',
          label: ollamaState.created.label,
          enabled: ollamaState.created.enabled ?? true,
          has_cookie: true,
          status: 'ok',
          attempt_count: 0,
          created_at_ms: Date.now(),
          updated_at_ms: Date.now(),
        };
        return ollamaState.createdAccount;
      },
    ],
    [
      (url, method) => method === 'POST' && url.pathname.endsWith('/management/auth-files/status'),
      () => {
        ollamaState.cpaToggleCalls += 1;
        return { status: 'ok' };
      },
    ],
    [
      (url, method) => method === 'PUT' && /\/ollama\/accounts\/[^/]+$/.test(url.pathname),
      (url, method, request) => {
        ollamaState.updated = JSON.parse(request.postData() ?? '{}');
        ollamaState.putCount += 1;
        return { ...ollamaAccountList()[0], label: ollamaState.updated.label ?? 'My Ollama', enabled: ollamaState.updated.enabled ?? true };
      },
    ],
  ];
}

export async function ollamaAccounts({ base, page, check }) {
  ollamaState.created = null;
  ollamaState.createdAccount = null;
  ollamaState.updated = null;
  ollamaState.putCount = 0;
  ollamaState.cpaToggleCalls = 0;

  await gotoConsole(page, base, '/oauth-management', '.oauth-management-page');
  await page.getByTestId('oauth-management-ollama-open').click();

  const drawer = page.locator('.ant-drawer:visible').filter({ hasText: 'Ollama Accounts & Quota' });
  await drawer.waitFor({ state: 'visible' });
  check('the OAuth workspace opens the Ollama management drawer', await drawer.count() === 1);
  await until(async () => (await drawer.getByText('My Ollama', { exact: true }).count()) > 0, { label: 'the account list to render' });

  check('a stored cookie is reported as encrypted and never as its value',
    await drawer.getByText('Encrypted', { exact: true }).count() === 2 && !(await drawer.innerText()).includes(OLLAMA_COOKIE));
  check('a collected account shows its real used percent', (await drawer.innerText()).includes('45.5%'));
  check('a paused account with no snapshot reads as an unknown quota', await drawer.getByText('Not reported', { exact: true }).count() >= 1);

  // Edit: the cookie field starts blank, and saving without touching it omits it entirely.
  await drawer.getByRole('button', { name: 'Edit', exact: true }).first().click();
  const editModal = page.locator('.ant-modal:visible').filter({ hasText: 'Edit Ollama Account' });
  await editModal.waitFor({ state: 'visible' });
  const editCookie = editModal.locator('input[type="password"]');
  check('editing an account never prefills the stored cookie', await editCookie.inputValue() === '');
  await editModal.getByRole('button', { name: 'Save', exact: true }).click();
  await until(() => ollamaState.updated !== null, { label: 'the untouched-edit save to reach the gateway' });
  check('saving an untouched edit omits the cookie instead of clearing it',
    !('cookie' in (ollamaState.updated ?? {})) && ollamaState.updated.label === 'My Ollama', JSON.stringify(ollamaState.updated));

  // Add: a new account requires both a label and a cookie, and the cookie reaches the wire.
  await until(async () => (await page.locator('.ant-modal:visible').count()) === 0, { label: 'the edit modal to close' });
  await drawer.getByRole('button', { name: 'Add Ollama Account', exact: true }).click();
  const addModal = page.locator('.ant-modal:visible').filter({ hasText: 'Add Ollama Account' });
  await addModal.waitFor({ state: 'visible' });
  const addLabel = addModal.locator('input[type="text"]');
  const addCookie = addModal.locator('input[type="password"]');
  check('a new account cannot be saved without both a label and a cookie',
    await addLabel.inputValue() === '' && await addModal.getByRole('button', { name: 'Save', exact: true }).isDisabled());
  await addLabel.fill('New Ollama');
  await addCookie.fill(OLLAMA_COOKIE);
  await addModal.getByRole('button', { name: 'Save', exact: true }).click();
  await until(() => ollamaState.created !== null, { label: 'the new account to reach the gateway' });
  check('the add carries the label and the cookie to the write-only endpoint',
    ollamaState.created.label === 'New Ollama' && ollamaState.created.cookie === OLLAMA_COOKIE, JSON.stringify(ollamaState.created));
  await until(async () => (await drawer.getByText('New Ollama', { exact: true }).count()) > 0, { label: 'the new account to appear' });
  check('the typed cookie is never echoed back into the page', !(await drawer.innerText()).includes(OLLAMA_COOKIE));

  // Close drawer and verify Ollama accounts appear inside the main OAuth workspace table and provider filter
  await drawer.locator('.ant-drawer-close').click();
  await page.locator('.ant-drawer-open').waitFor({ state: 'hidden' });

  // Check Ollama provider tab exists (in Ant Design Tabs, each tab header is a role="tab")
  const ollamaTab = page.locator('[role="tab"]').filter({ hasText: 'Ollama' });
  check('the provider tabs list Ollama as a top-level provider option', await ollamaTab.count() > 0);
  await ollamaTab.click();

  // Ollama rows render in the workspace table
  const ollamaRow = page.locator('[data-identity^="ollama:"]').first();
  await ollamaRow.waitFor({ state: 'visible' });
  check('Ollama account renders as a unified credential record in the workspace', await ollamaRow.count() > 0);
  check('Ollama row shows real 55% remaining weekly quota derived from 45.5% used', (await ollamaRow.innerText()).includes('55%'));

  check('Ollama row has an accessible batch checkbox', await ollamaRow.getByRole('checkbox', { name: 'Select My Ollama' }).count() === 1);

  // Verify row switch toggles Ollama scraper collection (PUT /ollama/accounts), never CPA credential status (POST /auth-files/status)
  const previousPutCount = ollamaState.putCount;
  const previousCpaCount = ollamaState.cpaToggleCalls;
  const rowSwitch = ollamaRow.locator('button[role="switch"]');
  await rowSwitch.click();
  await until(() => ollamaState.putCount > previousPutCount, { label: 'Ollama collection toggle to call updateOllamaAccount' });
  check('toggling Ollama row calls PUT /ollama/accounts with enabled update', ollamaState.updated.enabled === false);
  check('toggling Ollama row never calls CPA credential status endpoint', ollamaState.cpaToggleCalls === previousCpaCount);

  // Verify action menu strictly isolates CPA actions
  const moreBtn = ollamaRow.locator('.ant-btn-icon-only').first();
  await moreBtn.click();
  const dropdownMenu = page.locator('.ant-dropdown:visible');
  await dropdownMenu.waitFor({ state: 'visible' });
  const menuText = await dropdownMenu.innerText();
  check('Ollama action menu hides CPA models, download and cooldown clear',
    !menuText.includes('Models') && !menuText.includes('Download') && !menuText.includes('Clear Cooldown'));
  check('Ollama action menu provides Refresh quota, Edit and Delete',
    menuText.includes('Refresh quota') && menuText.includes('Edit') && menuText.includes('Delete'));
  // Close menu by clicking elsewhere
  await page.keyboard.press('Escape');

  // The operator's mixed inventory: 10 enabled CPA, 2 disabled CPA and one monitor.
  const mixedFiles = oauthManagementFixtures.files.map((file, index) => ({
    ...file, disabled: index >= 10,
    name: index === 0 ? `${file.auth_index}: Synthetic account.json` : file.name,
    email: index === 0 ? `${file.auth_index}: synthetic@example.test` : file.email,
  }));
  const mixedQuotas = oauthManagementFixtures.quota.map((quota) => ({
    ...quota, status: 'healthy', error: undefined,
    capabilities: { ...quota.capabilities, refresh_supported: true },
  }));
  const monitor = { ...ollamaAccountList()[0], label: `ollama:${OLLAMA_ACCOUNT_ID}: Legacy account` };
  let quotaRefreshIndexes = [];
  let monitorRefreshes = 0;
  await page.route('**/management/auth-files', (route) => route.fulfill({ json: { files: mixedFiles, total: 12 } }));
  await page.route('**/management/quota', (route) => route.fulfill({ json: { quotas: mixedQuotas, total: 12, summary: {} } }));
  await page.route('**/ollama/accounts', (route) => route.fulfill({ json: { accounts: [monitor] } }));
  const monitorQuota = {
    ...ollamaQuota().accounts[0].quota,
    plan: { plan_type: 'legacy', plan_label: 'Legacy subscription', tier: 'standard', credits: { balance: '$0' } },
  };
  await page.route('**/ollama/quota', (route) => route.fulfill({ json: { accounts: [{ account_id: monitor.id, enabled: true, quota: monitorQuota }] } }));
  await page.route('**/management/quota/refresh', (route) => {
    quotaRefreshIndexes.push(...route.request().postDataJSON().auth_indexes);
    const quotas = mixedQuotas.filter((quota) => quotaRefreshIndexes.includes(quota.auth_index));
    return route.fulfill({ json: {
      status: 'ok', quotas, total: quotas.length,
      results: quotas.map((quota) => ({ auth_index: quota.auth_index, name: quota.name, outcome: 'success', quota })),
    } });
  });
  await page.route(`**/ollama/accounts/${monitor.id}/refresh`, (route) => {
    monitorRefreshes += 1;
    return route.fulfill({ json: { account: monitor, quota: monitorQuota, refreshed: true } });
  });
  await page.goto(`${base}/oauth-management?page_size=24`, { waitUntil: 'domcontentloaded' });
  await until(async () => await page.getByTestId('oauth-credential-record').count() === 13, { label: 'the thirteen-entry inventory' });
  const refreshAll = page.getByRole('button', { name: 'Refresh quota (11)', exact: true });
  await refreshAll.waitFor({ state: 'visible' });
  check('refresh states eleven targets and explains the two disabled skips',
    (await page.getByTestId('quota-refresh-skips').innerText()).includes('2 skipped: Credential is disabled (2)'));
  const monitorRow = page.locator(`[data-identity="ollama:${monitor.id}"]`);
  check('legacy subscription money and opaque row labels are hidden',
    !(await monitorRow.innerText()).includes('$0') && !(await monitorRow.innerText()).includes(monitor.id)
      && !(await page.getByTestId('oauth-credential-record').first().innerText()).includes('auth-01:'));
  await monitorRow.getByRole('checkbox', { name: 'Select Legacy account' }).check();
  await refreshAll.click();
  await until(() => quotaRefreshIndexes.length === 10 && monitorRefreshes === 1, { label: 'source-aware unified refresh requests' });
  await until(() => refreshAll.isEnabled(), { label: 'the unified refresh to settle' });
  check('the default refresh never sends a monitor or disabled entry to CPA',
    JSON.stringify(quotaRefreshIndexes) === JSON.stringify(mixedFiles.slice(0, 10).map((file) => file.auth_index)));
  check('quota refresh preserves the monitor selection', await monitorRow.getByRole('checkbox').isChecked());
  await page.locator('.omc-toast').waitFor({ state: 'detached', timeout: CONDITION_TIMEOUT_MS });
  check('refresh acknowledgements are dismissed before capture', await page.locator('.omc-toast').count() === 0);
  await settleLayout(page);
  await page.screenshot({ path: 'tmp/credential-mixed-refresh-desktop.png' });
  await monitorRow.getByRole('button', { name: 'More actions: Legacy account', exact: true }).click();
  await page.getByRole('menuitem', { name: 'Refresh quota', exact: true }).click();
  await until(() => monitorRefreshes === 2, { label: 'individual monitor active refresh' });
  await until(() => monitorRow.getByRole('button', { name: 'More actions: Legacy account', exact: true }).isEnabled(), { label: 'individual refresh settled' });
  await dismissNotification(page, 'Ollama quota refreshed successfully', 'the individual Ollama acknowledgement');
  check('the individual acknowledgement is dismissed before capture', await page.locator('.omc-toast').count() === 0);
  await monitorRow.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'tmp/credential-ollama-selection-desktop.png' });
  await page.setViewportSize({ width: 375, height: 900 });
  await settleLayout(page);
  await refreshAll.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'tmp/credential-mixed-refresh-phone.png' });
  const skipLayout = await page.getByTestId('quota-refresh-skips').evaluate((node) => {
    const rect = node.getBoundingClientRect();
    return rect.left >= 0 && rect.right <= window.innerWidth && node.scrollWidth <= node.clientWidth;
  });
  check('the skipped-count explanation fits its full phone row', skipLayout);
  await monitorRow.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'tmp/credential-ollama-selection-phone.png' });
  check('the mixed workspace fits a phone without horizontal overflow', await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
  await page.locator('.terminal-page-head .terminal-page-actions').getByRole('button', { name: 'More', exact: true }).click();
  const ollamaEntry = page.getByTestId('oauth-management-ollama-open-mobile');
  await ollamaEntry.click();
  const phoneDrawer = page.locator('.ant-drawer-open').filter({ hasText: 'Ollama Accounts' });
  await phoneDrawer.waitFor({ state: 'visible' });
  await settleLayout(page);
  await page.screenshot({ path: 'tmp/ollama-management-phone.png' });
  check('the Ollama management drawer remains usable from the phone toolbar', await phoneDrawer.evaluate((node) => {
    const rect = node.getBoundingClientRect();
    return rect.left >= 0 && rect.right <= window.innerWidth;
  }));
}

// ── Lane C: Feishu settings ───────────────────────────────────────────────────

const feishuState = {
  phase: 'env',
  testPosts: 0,
  saveBody: null,
  settings: {
    enabled: false,
    app_id: 'cli_env',
    has_secret: true,
    chat_id: 'oc_env',
    summary_times: ['10:00'],
    timezone: 'Asia/Shanghai',
    quiet_start: '23:00',
    quiet_end: '08:00',
    tight_remaining_percent: 20,
    channel_unavailable_after_ms: 1_800_000,
    monitor_down_after_ms: 1_800_000,
    source: 'env_forced_off',
    degraded: false,
    forced_off: true,
  },
  status: {
    enabled: false,
    ready: false,
    degraded: false,
    source: 'env_forced_off',
    forced_off: true,
    connected: false,
    open_items: 0,
    pending_sends: 0,
    queued_sends: 0,
  },
};

export function feishuSettingsFixtures() {
  return [
    [(url) => url.pathname.endsWith('/dashboard'), () => chartDashboard],
    [(url) => url.pathname.endsWith('/dashboard/tail'), () => chartDashboard],
    [(url) => url.pathname.endsWith('/dashboard/token-heatmap'), () => chartTokenHeatmap],
    [(url, method) => method === 'GET' && url.pathname.endsWith('/feishu/settings'), () => feishuState.settings],
    [(url, method) => method === 'GET' && url.pathname.endsWith('/feishu/status'), () => feishuState.status],
    [
      (url, method) => method === 'PUT' && url.pathname.endsWith('/feishu/settings'),
      (url, method, request) => {
        feishuState.saveBody = JSON.parse(request.postData() ?? '{}');
        return feishuState.settings;
      },
    ],
    [
      (url, method) => method === 'POST' && url.pathname.endsWith('/feishu/test'),
      () => {
        feishuState.testPosts += 1;
        return { sent: true };
      },
    ],
  ];
}

export async function feishuSettings({ base, page, check }) {
  feishuState.phase = 'env';
  feishuState.testPosts = 0;
  feishuState.saveBody = null;

  await gotoConsole(page, base, '/omc-settings', '.omc-settings-page');
  const appId = page.getByPlaceholder('cli_a1b2c3d4...');
  await until(async () => (await page.getByText('Forced off by env', { exact: true }).count()) === 1, { label: 'the environment override to be surfaced' });
  check('an environment force-off is named on the page and disables every control',
    await appId.isDisabled()
      && await page.getByRole('button', { name: 'Send Test Card', exact: true }).isDisabled());

  // The environment stops forcing it off; the stored secret must survive a blank save.
  feishuState.phase = 'web';
  feishuState.settings = {
    ...feishuState.settings,
    enabled: true,
    app_id: 'cli_12345',
    chat_id: 'oc_chat_999',
    source: 'web',
    forced_off: false,
  };
  feishuState.status = {
    ...feishuState.status,
    enabled: true,
    ready: true,
    source: 'web',
    forced_off: false,
    connected: true,
  };
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator('.omc-settings-page').waitFor({ state: 'visible' });
  await until(async () => (await page.getByPlaceholder('cli_a1b2c3d4...').inputValue()) === 'cli_12345', { label: 'the saved connection settings to load' });

  const secret = page.locator('.omc-settings-page input[type="password"]');
  check('a stored app secret is never echoed into the form', await secret.inputValue() === '');
  await page.getByPlaceholder('cli_a1b2c3d4...').fill('cli_changed');
  await page.getByRole('button', { name: 'Save Feishu Settings', exact: true }).click();
  await until(() => feishuState.saveBody !== null, { label: 'the settings save to reach the gateway' });
  check('a blank secret field leaves the stored secret untouched instead of clearing it',
    !('app_secret' in (feishuState.saveBody ?? {})) && feishuState.saveBody.app_id === 'cli_changed', JSON.stringify(feishuState.saveBody));

  // The test card is a two-step action: opening it sends nothing, and cancelling sends nothing.
  const testButton = page.getByRole('button', { name: 'Send Test Card', exact: true });
  await testButton.click();
  const testConfirm = page.locator('.ant-modal:visible').filter({ hasText: 'Send Feishu Test Card' });
  await testConfirm.waitFor({ state: 'visible' });
  check('opening the test-card confirmation sends nothing on its own', feishuState.testPosts === 0);
  await testConfirm.getByRole('button', { name: 'Cancel', exact: true }).click();
  await until(async () => (await page.locator('.ant-modal:visible').count()) === 0, { label: 'the test-card confirmation to close' });
  check('cancelling the test-card confirmation still sends nothing', feishuState.testPosts === 0);

  await testButton.click();
  await testConfirm.waitFor({ state: 'visible' });
  await testConfirm.getByRole('button', { name: 'Confirm', exact: true }).click();
  await until(() => feishuState.testPosts === 1, { label: 'the confirmed test card to be sent' });
  check('only an explicit confirmation sends the test card, exactly once', feishuState.testPosts === 1);
  await settleLayout(page);
  await page.screenshot({ path: 'tmp/feishu-settings-desktop.png', fullPage: true });
  await page.setViewportSize({ width: 390, height: 900 });
  await page.getByText('Feishu Notifications', { exact: true }).first().scrollIntoViewIfNeeded();
  await settleLayout(page);
  check('the Feishu settings panel remains reachable without phone-width overflow',
    await page.getByPlaceholder('cli_a1b2c3d4...').isVisible()
      && await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth));
  await page.screenshot({ path: 'tmp/feishu-settings-phone.png', fullPage: true });
}
