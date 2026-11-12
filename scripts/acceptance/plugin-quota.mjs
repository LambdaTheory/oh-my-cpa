/**
 * Plugin-quota browser acceptance: the three credentials a quota plugin serves.
 *
 * Ollama, Command Code and OpenCode Go reach OMC through CPA's plugin quota path
 * (`GET /credentials` plus `POST /plugins/:id/quota`), not through a built-in
 * reader. This scenario walks one deterministic fixture through the states that
 * matter to a reader: a fresh reading per provider shape, a row-level refresh
 * that has to address the plugin by `auth_index`, the plugin's cached 200
 * (`stale`), and an old host that withholds `quota_plugin_id` so the client has
 * to resolve the plugin through `GET /credentials/quota/providers`.
 *
 * The orchestration file owns the processes and the browser; the fixture exposes
 * `fixture.setQuotaMode` / `fixture.setLegacyHost` so this module can move the
 * live gateway between those states without restarting it.
 */
const QUOTA_PLUGIN_POST_PATH = '/v8/management/plugins/ollama-quota/quota';
const QUOTA_PROVIDERS_PATH = '/v8/management/credentials/quota/providers';

// The fixture auth indexes, verbatim. The provider keys inside the disk `type`
// fields keep their interior space; these indexes do not, so a selector built
// from one can never accidentally "normalize" the other.
const OLLAMA_INDEX = 'auth-index-e2e-ollama';
const COMMAND_CODE_INDEX = 'auth-index-e2e-command-code';
const OPENCODE_INDEX = 'auth-index-e2e-opencode';

// One page, all fixtures: the default page size would split the list and a row
// missing from page one would read as a missing credential.
const QUOTA_PAGE = '/oauth-management?page_size=48';

function pluginPostFor(requests, authIndex) {
  return requests.find((entry) => {
    if (entry.method !== 'POST' || entry.path !== QUOTA_PLUGIN_POST_PATH) return false;
    try {
      return (JSON.parse(entry.body)?.auth_index ?? '') === authIndex;
    } catch {
      return false;
    }
  });
}

function compactLabels(body) {
  return body.locator('[data-quota-compact-window]').evaluateAll(
    (elements) => elements.map((element) => element.getAttribute('data-quota-window-label') ?? ''),
  );
}

export async function runPluginQuotaAcceptance({
  appURL,
  page,
  check,
  checkEventually,
  fakeCpa,
}) {
  const row = (authIndex) => page.locator(`[data-testid="oauth-credential-record"][data-auth-index="${authIndex}"]`);

  const openQuotaPage = async () => {
    await page.goto(`${appURL}${QUOTA_PAGE}`, { waitUntil: 'domcontentloaded' });
    await page.locator('.oauth-management-page').first().waitFor({ state: 'visible', timeout: 15000 });
    await row(OLLAMA_INDEX).waitFor({ state: 'visible', timeout: 15000 });
  };

  // A row's own refresh item, which is the manual path the console offers. It is
  // also the only path that tells us the credential's own auth index reached the
  // plugin; a page-level refresh would blur which record caused the POST.
  const refreshRow = async (authIndex) => {
    await row(authIndex).getByRole('button', { name: /^(更多操作|More actions):/i }).click();
    await page.getByRole('menuitem', { name: /^(刷新额度|Refresh quota)/i }).first().click();
  };

  const waitForPluginPost = (authIndex, mark) => checkEventually(
    `plugin-quota refresh of ${authIndex} posts to the quota plugin`,
    () => Boolean(pluginPostFor(fakeCpa.requests.slice(mark), authIndex)),
    {
      timeoutMs: 15000,
      detail: () => JSON.stringify(fakeCpa.requests.slice(mark)
        .filter((entry) => entry.path.includes('/quota'))
        .map(({ method, path, body }) => ({ method, path, body }))),
    },
  );

  const openDrawer = async (authIndex) => {
    await row(authIndex).getByRole('button', { name: /^(详情|Details):/i }).click();
    const body = page.locator('.ant-drawer-open [data-quota-density="expanded"]').first();
    await body.waitFor({ state: 'visible', timeout: 10000 });
    return body;
  };

  const closeDrawer = async () => {
    await page.locator('.ant-drawer-open .ant-drawer-close').click();
    await page.locator('.ant-drawer-open').waitFor({ state: 'hidden', timeout: 10000 });
  };

  await openQuotaPage();
  check(
    'plugin-quota lists Ollama, Command Code and OpenCode Go as separate credentials',
    (await row(OLLAMA_INDEX).isVisible())
      && (await row(COMMAND_CODE_INDEX).isVisible())
      && (await row(OPENCODE_INDEX).isVisible()),
  );

  // ---- The plugin's cached 200: stale marks, reason and old observation ----
  // This runs before any fresh read of the same credential: the stored latest
  // snapshot is chosen by observed_at_ms DESC, and a stale reading carries the
  // plugin's older read time, so a fresh snapshot taken earlier would outrank it
  // and the row would fall back to that newer reading. The stale state is a
  // steady state only while no later successful read exists.
  fakeCpa.fixture.setQuotaMode('stale');
  let mark = fakeCpa.requests.length;
  await refreshRow(COMMAND_CODE_INDEX);
  await waitForPluginPost(COMMAND_CODE_INDEX, mark);
  const staleMarker = row(COMMAND_CODE_INDEX).locator('[data-plugin-quota-stale="true"]');
  await checkEventually(
    'plugin-quota stale answer marks the CMC row as a cached reading',
    async () => (await staleMarker.count()) === 1,
    { timeoutMs: 15000 },
  );
  const staleTitle = (await staleMarker.first().getAttribute('title')) ?? '';
  check(
    'plugin-quota stale marker names the cached reading and the plugin reason',
    /上次成功读取|last successful reading/i.test(staleTitle) && staleTitle.includes('upstream timeout'),
    `title="${staleTitle}"`,
  );
  const staleText = await row(COMMAND_CODE_INDEX).locator('[data-quota-body]').innerText();
  check(
    'plugin-quota stale reading keeps its figures and no longer reads as just-read',
    staleText.includes('321 credits')
      && staleText.includes('80%')
      && staleText.includes('55%')
      && !/刚刚|just now/i.test(staleText),
    staleText.replace(/\s+/g, ' ').slice(0, 300),
  );
  const staleDrawer = await openDrawer(COMMAND_CODE_INDEX);
  const staleDrawerAudit = await staleDrawer.evaluate((body) => ({
    credits: body.querySelectorAll('[data-plugin-quota-stale="true"]').length,
    windows: body.querySelectorAll('[data-quota-window-stale="true"]').length,
    notes: [...body.querySelectorAll('[data-quota-window-note="true"]')].map((note) => note.textContent),
  }));
  check(
    'plugin-quota drawer repeats the cached-reading mark, window marks and reason',
    staleDrawerAudit.credits === 1
      && staleDrawerAudit.windows >= 1
      && staleDrawerAudit.notes.some((note) => (note ?? '').includes('upstream timeout')),
    JSON.stringify(staleDrawerAudit),
  );
  await closeDrawer();
  fakeCpa.fixture.setQuotaMode('ok');

  // ---- Command Code: plan, credits balance and the 5h/7d windows ----
  mark = fakeCpa.requests.length;
  await refreshRow(COMMAND_CODE_INDEX);
  await waitForPluginPost(COMMAND_CODE_INDEX, mark);
  const commandCode = row(COMMAND_CODE_INDEX).locator('[data-quota-body]');
  await checkEventually(
    'plugin-quota CMC row shows the plan, the credits balance and both windows',
    async () => {
      const text = await commandCode.innerText();
      const labels = await compactLabels(commandCode);
      return text.includes('Pro')
        && text.includes('321 credits')
        && !text.includes('$321')
        && text.includes('80%')
        && text.includes('55%')
        && labels.length === 2
        && labels.some((label) => label.includes('Command Code · 5-hour session'))
        && labels.some((label) => label.includes('Command Code · Weekly'));
    },
    { detail: async () => (await commandCode.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
  );

  // ---- OpenCode Go: three windows, no plan and no balance ----
  mark = fakeCpa.requests.length;
  await refreshRow(OPENCODE_INDEX);
  await waitForPluginPost(OPENCODE_INDEX, mark);
  const openCode = row(OPENCODE_INDEX).locator('[data-quota-body]');
  await checkEventually(
    'plugin-quota OPC row keeps the three windows without a plan or balance',
    async () => {
      const text = await openCode.innerText();
      const labels = await compactLabels(openCode);
      return (await openCode.getAttribute('data-quota-window-count')) === '3'
        && labels.length === 2
        && labels.every((label) => label.startsWith('OpenCode Go · '))
        && !labels.some((label) => /5-hour|30 days/i.test(label))
        && (await openCode.locator('[data-credit-balance="true"]').count()) === 0
        && !/Credit 余额|Credit balance/.test(text);
    },
    { detail: async () => (await openCode.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
  );

  const openCodeDrawer = await openDrawer(OPENCODE_INDEX);
  const openCodeWindows = await openCodeDrawer.evaluate((body) => ({
    declared: Number(body.getAttribute('data-quota-window-count') ?? '0'),
    rendered: body.querySelectorAll('.meter').length,
    text: body.innerText,
  }));
  check(
    'plugin-quota OPC drawer lists all three windows',
    openCodeWindows.declared === 3 && openCodeWindows.rendered === 3 && ['0%', '42%', '21%'].every((share) => openCodeWindows.text.includes(share)),
    JSON.stringify(openCodeWindows),
  );
  const openCodeNote = await openCodeDrawer.locator('[data-quota-window-note="true"]').first().innerText();
  check(
    'plugin-quota OPC rolling window keeps its rate-limit note',
    openCodeNote.includes('upstream reports this window rate limited'),
    `note="${openCodeNote}"`,
  );
  await closeDrawer();

  // ---- Ollama: USD balance beside the cookie windows ----
  mark = fakeCpa.requests.length;
  await refreshRow(OLLAMA_INDEX);
  await waitForPluginPost(OLLAMA_INDEX, mark);
  const ollama = row(OLLAMA_INDEX).locator('[data-quota-body]');
  await checkEventually(
    'plugin-quota Ollama row shows the USD balance and both windows',
    async () => {
      const text = await ollama.innerText();
      const labels = await compactLabels(ollama);
      return text.includes('12.34 USD')
        && text.includes('60%')
        && text.includes('80%')
        && labels.some((label) => label.includes('Ollama · 5-hour session'))
        && labels.some((label) => label.includes('Ollama · Weekly'));
    },
    { detail: async () => (await ollama.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
  );

  // ---- An old host: no quota_plugin_id, resolved through quota providers ----
  fakeCpa.fixture.setQuotaMode('ok');
  fakeCpa.fixture.setLegacyHost(true);
  try {
    await openQuotaPage();
    check(
      'plugin-quota legacy host still lists all three credentials',
      (await row(OLLAMA_INDEX).isVisible())
        && (await row(COMMAND_CODE_INDEX).isVisible())
        && (await row(OPENCODE_INDEX).isVisible()),
    );
    mark = fakeCpa.requests.length;
    await refreshRow(COMMAND_CODE_INDEX);
    await checkEventually(
      'plugin-quota legacy host resolves the plugin through GET /credentials/quota/providers',
      () => fakeCpa.requests.slice(mark).some((entry) => entry.method === 'GET' && entry.path === QUOTA_PROVIDERS_PATH),
      { timeoutMs: 15000 },
    );
    await waitForPluginPost(COMMAND_CODE_INDEX, mark);
    const legacyPost = pluginPostFor(fakeCpa.requests.slice(mark), COMMAND_CODE_INDEX);
    check(
      'plugin-quota legacy fallback still addresses the plugin by auth index only',
      Boolean(legacyPost)
        && JSON.stringify(Object.keys(JSON.parse(legacyPost.body)).sort()) === JSON.stringify(['auth_index']),
      legacyPost?.body ?? 'missing POST',
    );
    await checkEventually(
      'plugin-quota legacy fallback still renders the CMC reading',
      async () => (await row(COMMAND_CODE_INDEX).locator('[data-quota-body]').innerText()).includes('321 credits'),
      { timeoutMs: 15000 },
    );
  } finally {
    fakeCpa.fixture.setLegacyHost(false);
    fakeCpa.fixture.setQuotaMode('ok');
  }
}
