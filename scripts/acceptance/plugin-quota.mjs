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
 * Round 2 extends the same fixture into the boundary cases from `cases.md`:
 * sparse payloads (C-06), the overview API contract (C-15/C-16), a failed read
 * that keeps the last good snapshot (C-25), a 502 with no cache (C-09), batch
 * dedupe and per-item isolation (C-17), an unmatched provider (C-21), the
 * `quota_plugin_id` priority over a second claiming plugin (C-22), legacy
 * resolution through `GET /plugins` (C-19) and the 429 backoff window where a
 * second refresh must not reach the plugin at all (C-18/C-08).
 *
 * The orchestration file owns the processes and the browser; the fixture exposes
 * `fixture.setQuotaMode` / `fixture.setLegacyHost` / `fixture.setDecoyPlugin` so
 * this module can move the live gateway between those states without restarting
 * it.
 */
const QUOTA_PLUGIN_POST_PATH = '/v8/management/plugins/ollama-quota/quota';
const QUOTA_PLUGIN_PREFIX = '/v8/management/plugins/';
const QUOTA_PROVIDERS_PATH = '/v8/management/credentials/quota/providers';
const PLUGINS_PATH = '/v8/management/plugins';
const CREDENTIALS_PATH = '/v8/management/credentials';

// The fixture auth indexes, verbatim. The provider keys inside the disk `type`
// fields keep their interior space; these indexes do not, so a selector built
// from one can never accidentally "normalize" the other.
const OLLAMA_INDEX = 'auth-index-e2e-ollama';
const COMMAND_CODE_INDEX = 'auth-index-e2e-command-code';
const OPENCODE_INDEX = 'auth-index-e2e-opencode';
const OLLAMA_KEY_INDEX = 'auth-index-e2e-ollama-key';
const RATE_LIMITED_INDEX = 'auth-index-e2e-rate-limited';
const UPSTREAM_ERROR_INDEX = 'auth-index-e2e-upstream-error';
const UNMATCHED_INDEX = 'auth-index-e2e-unmatched';

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

function pluginPostsFor(requests, authIndex) {
  return requests.filter((entry) => {
    if (entry.method !== 'POST' || !entry.path.startsWith(QUOTA_PLUGIN_PREFIX)) return false;
    if (!entry.path.endsWith('/quota')) return false;
    try {
      return (JSON.parse(entry.body)?.auth_index ?? '') === authIndex;
    } catch {
      return false;
    }
  });
}

function pluginQuotaPosts(requests) {
  return requests.filter((entry) => entry.method === 'POST'
    && entry.path.startsWith(QUOTA_PLUGIN_PREFIX)
    && entry.path.endsWith('/quota'));
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

  // ---- The quota API, from the same signed-in browser context the reader uses ----
  const api = {
    get: (path) => page.request.get(`${appURL}/api/v1${path}`),
    post: (path, data) => page.request.post(`${appURL}/api/v1${path}`, { data }),
    put: (path, data) => page.request.put(`${appURL}/api/v1${path}`, { data }),
  };
  const readJSON = async (response) => ({ status: response.status(), body: await response.json() });
  const getOverview = async () => readJSON(await api.get('/management/quota'));
  const overviewItem = (payload, authIndex) => (payload?.quotas ?? []).find((item) => item.auth_index === authIndex);
  const refreshCredential = async (authIndex) => readJSON(
    await api.post('/management/quota/refresh', { auth_index: authIndex }),
  );
  const refreshBatch = async (authIndexes) => readJSON(
    await api.post('/management/quota/refresh', { auth_indexes: authIndexes }),
  );

  // The scheduler ticks once a minute and a never-read credential is due on the
  // first tick. Turning the policy off keeps every later assertion about the
  // plugin POST count attributable to this scenario's own refreshes.
  const policyResponse = await api.put('/preferences/omc_quota_auto_refresh', {
    interval_minutes: 0,
    refresh_on_reset: false,
  });
  check(
    'plugin-quota scenario switches the automatic refresh policy off',
    policyResponse.status() === 200,
    `status=${policyResponse.status()}`,
  );

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

  // ---- C-06: sparse payloads render absence, never an invented zero ----
  fakeCpa.fixture.setQuotaMode('missing');
  try {
    mark = fakeCpa.requests.length;
    await refreshRow(COMMAND_CODE_INDEX);
    await waitForPluginPost(COMMAND_CODE_INDEX, mark);
    await checkEventually(
      'C-06 CMC without subscription/summary shows neither a plan nor a zero balance',
      async () => {
        const text = await commandCode.innerText();
        const labels = await compactLabels(commandCode);
        return !text.includes('Pro')
          && !/Credit 余额|Credit balance/.test(text)
          && !/0 credits/i.test(text)
          && (await commandCode.getAttribute('data-quota-window-count')) === '2'
          && labels.length === 2;
      },
      { detail: async () => (await commandCode.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
    );

    mark = fakeCpa.requests.length;
    await refreshRow(OPENCODE_INDEX);
    await waitForPluginPost(OPENCODE_INDEX, mark);
    await checkEventually(
      'C-06 OPC keeps only its weekly window and prints -- for the missing share',
      async () => {
        const labels = await compactLabels(openCode);
        const text = await openCode.innerText();
        return (await openCode.getAttribute('data-quota-window-count')) === '1'
          && labels.length === 1
          && labels[0].includes('OpenCode Go · Weekly')
          && !labels.some((label) => /Rolling|Monthly/.test(label))
          && text.includes('--')
          && !/Credit 余额|Credit balance/.test(text)
          && !text.includes('0%');
      },
      { detail: async () => (await openCode.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
    );

    mark = fakeCpa.requests.length;
    await refreshRow(OLLAMA_KEY_INDEX);
    await waitForPluginPost(OLLAMA_KEY_INDEX, mark);
    const ollamaKey = row(OLLAMA_KEY_INDEX).locator('[data-quota-body]');
    await checkEventually(
      'C-06 Ollama api_key keeps its balance and shows the answered-without-windows state',
      async () => {
        const text = await ollamaKey.innerText();
        return text.includes('8.75 USD')
          && text.includes('上游已应答但未返回配额窗口')
          && text.includes('未发布用量')
          && (await ollamaKey.getAttribute('data-quota-window-count')) === '0'
          && !text.includes('0%');
      },
      { detail: async () => (await ollamaKey.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
    );

    mark = fakeCpa.requests.length;
    await refreshRow(OLLAMA_INDEX);
    await waitForPluginPost(OLLAMA_INDEX, mark);
    await checkEventually(
      'C-06 Ollama cookie credential without groups shows the empty state and no progress bar',
      async () => {
        const text = await ollama.innerText();
        return text.includes('12.34 USD')
          && text.includes('上游已应答但未返回配额窗口')
          && (await ollama.getAttribute('data-quota-window-count')) === '0'
          && !text.includes('0%');
      },
      { detail: async () => (await ollama.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
    );
  } finally {
    fakeCpa.fixture.setQuotaMode('ok');
  }

  // C-06 left the three snapshots in their sparse shapes; the overview contract
  // below describes the full readings, so read them once more before asserting.
  const restored = await refreshBatch([COMMAND_CODE_INDEX, OPENCODE_INDEX, OLLAMA_INDEX, OLLAMA_KEY_INDEX]);
  check(
    'C-15 fixture is back to full readings before the overview contract check',
    restored.status === 200
      && (restored.body?.results ?? []).length === 4
      && (restored.body?.results ?? []).every((result) => result.outcome === 'success'),
    JSON.stringify((restored.body?.results ?? []).map((result) => result.outcome)),
  );

  // ---- C-15: the overview carries the raw provider keys and no credential secrets ----
  const overviewResponse = await getOverview();
  const overview = overviewResponse.body;
  const overviewText = JSON.stringify(overview);
  const cmcItem = overviewItem(overview, COMMAND_CODE_INDEX);
  const opcItem = overviewItem(overview, OPENCODE_INDEX);
  const ollamaItem = overviewItem(overview, OLLAMA_INDEX);
  const ollamaKeyItem = overviewItem(overview, OLLAMA_KEY_INDEX);
  check(
    'C-15 overview returns the three plugin credentials with their raw provider keys',
    overviewResponse.status === 200
      && cmcItem?.provider === 'openai-compatible-command code'
      && cmcItem?.type === 'openai-compatible-command code'
      && opcItem?.provider === 'openai-compatible-opencode'
      && opcItem?.type === 'openai-compatible-opencode'
      && ollamaItem?.provider === 'ollama'
      && ollamaItem?.type === 'ollama'
      && ollamaKeyItem?.provider === 'ollama',
    JSON.stringify([cmcItem?.provider, cmcItem?.type, opcItem?.provider, opcItem?.type, ollamaItem?.provider]),
  );
  check(
    'C-15 all three plugin credentials advertise refresh support',
    cmcItem?.capabilities?.refresh_supported === true
      && opcItem?.capabilities?.refresh_supported === true
      && ollamaItem?.capabilities?.refresh_supported === true,
    JSON.stringify([cmcItem?.capabilities, opcItem?.capabilities, ollamaItem?.capabilities]),
  );
  check(
    'C-15 overview keeps the plan and balance per provider shape',
    cmcItem?.plan?.plan_label === 'Pro'
      && cmcItem?.plan?.credits?.balance === '321 credits'
      && ollamaItem?.plan?.credits?.balance === '12.34 USD'
      && ollamaKeyItem?.plan?.credits?.balance === '8.75 USD'
      && (opcItem?.plan?.plan_label ?? '') === ''
      && opcItem?.plan?.credits == null,
    JSON.stringify({ cmc: cmcItem?.plan, opc: opcItem?.plan, ollama: ollamaItem?.plan, key: ollamaKeyItem?.plan }),
  );
  const allWindowLabels = [cmcItem, opcItem, ollamaItem].flatMap((item) => (item?.windows ?? []).map((window) => window.label));
  const opcRolling = (opcItem?.windows ?? []).filter((window) => /Rolling|Monthly/.test(window.label ?? ''));
  check(
    'C-15 window labels never leak bucket ids and OPC rolling/monthly carry no invented period',
    allWindowLabels.length >= 6
      && allWindowLabels.every((label) => !label.includes('bucket_0_0'))
      && opcRolling.length === 2
      && opcRolling.every((window) => window.period_hours == null),
    JSON.stringify({ allWindowLabels, opcRolling: opcRolling.map((window) => ({ label: window.label, period_hours: window.period_hours })) }),
  );
  const secretMarkers = ['api_key', 'cookie', 'base_url', 'access_token', 'Bearer '];
  check(
    'C-15 overview response carries no credential secret fields',
    secretMarkers.every((marker) => !overviewText.includes(marker)),
    secretMarkers.filter((marker) => overviewText.includes(marker)).join(', '),
  );

  // ---- C-16: one manual refresh addresses the plugin and writes back its observation ----
  mark = fakeCpa.requests.length;
  const single = await refreshCredential(COMMAND_CODE_INDEX);
  const singlePost = pluginPostFor(fakeCpa.requests.slice(mark), COMMAND_CODE_INDEX);
  const cmcObservedAt = single.body?.quota?.observed_at_ms;
  check(
    'C-16 single refresh answers with the plugin reading and its observation time',
    single.status === 200
      && single.body?.status === 'ok'
      && typeof cmcObservedAt === 'number' && cmcObservedAt > 0
      && single.body?.quota?.plan?.plan_label === 'Pro'
      && single.body?.quota?.plan?.credits?.balance === '321 credits'
      && (single.body?.quota?.windows ?? []).length === 2,
    JSON.stringify({ status: single.status, observed_at_ms: cmcObservedAt }),
  );
  check(
    'C-16 single refresh sends exactly one plugin POST carrying only the auth index',
    pluginPostsFor(fakeCpa.requests.slice(mark), COMMAND_CODE_INDEX).length === 1
      && Boolean(singlePost)
      && JSON.stringify(Object.keys(JSON.parse(singlePost.body)).sort()) === JSON.stringify(['auth_index']),
    singlePost?.body ?? 'missing POST',
  );
  const afterSingle = await getOverview();
  const cmcAfterSingle = overviewItem(afterSingle.body, COMMAND_CODE_INDEX);
  check(
    'C-16 overview replaces the snapshot with the fresh observation',
    cmcAfterSingle?.observed_at_ms === cmcObservedAt
      && cmcAfterSingle?.last_refresh?.outcome === 'success',
    JSON.stringify({ observed_at_ms: cmcAfterSingle?.observed_at_ms, expected: cmcObservedAt, last_refresh: cmcAfterSingle?.last_refresh }),
  );

  // ---- C-23: the overview and page polling never read a provider ----
  // Both the page's own minute poll and two explicit reads run inside this
  // window. With the automatic policy off, nothing may reach the plugin.
  fakeCpa.fixture.setQuotaMode('ok');
  const beforeIdle = await getOverview();
  const idleBefore = {
    cmc: overviewItem(beforeIdle.body, COMMAND_CODE_INDEX)?.observed_at_ms,
    opc: overviewItem(beforeIdle.body, OPENCODE_INDEX)?.observed_at_ms,
    ollama: overviewItem(beforeIdle.body, OLLAMA_INDEX)?.observed_at_ms,
  };
  mark = fakeCpa.requests.length;
  await new Promise((resolve) => setTimeout(resolve, 62000));
  const afterIdle = await getOverview();
  const idleRequests = fakeCpa.requests.slice(mark);
  const idleIdle = {
    cmc: overviewItem(afterIdle.body, COMMAND_CODE_INDEX)?.observed_at_ms,
    opc: overviewItem(afterIdle.body, OPENCODE_INDEX)?.observed_at_ms,
    ollama: overviewItem(afterIdle.body, OLLAMA_INDEX)?.observed_at_ms,
  };
  check(
    'C-23 overview and page polling make no plugin POST and no provider read',
    pluginQuotaPosts(idleRequests).length === 0
      && !idleRequests.some((entry) => entry.method === 'POST')
      && idleRequests.every((entry) => entry.path === CREDENTIALS_PATH),
    JSON.stringify(idleRequests.map(({ method, path }) => ({ method, path }))),
  );
  check(
    'C-23 overview polling does not move the stored observation times',
    JSON.stringify(idleIdle) === JSON.stringify(idleBefore),
    JSON.stringify({ before: idleBefore, after: idleIdle }),
  );

  // ---- C-25: a failed refresh keeps the last good snapshot (never a zero) ----
  const beforeFailure = await getOverview();
  const cmcBeforeFailure = overviewItem(beforeFailure.body, COMMAND_CODE_INDEX);
  const cmcT1 = cmcBeforeFailure?.observed_at_ms;
  fakeCpa.fixture.setQuotaMode('upstream-error');
  try {
    mark = fakeCpa.requests.length;
    const failedSingle = await refreshCredential(COMMAND_CODE_INDEX);
    check(
      'C-25 failed refresh reports HTTP 502 and one plugin attempt',
      failedSingle.status === 200
        && /HTTP 502/.test(failedSingle.body?.quota?.error ?? '')
        && pluginPostsFor(fakeCpa.requests.slice(mark), COMMAND_CODE_INDEX).length === 1,
      JSON.stringify({ status: failedSingle.status, error: failedSingle.body?.quota?.error }),
    );
    const afterFailure = await getOverview();
    const cmcAfterFailure = overviewItem(afterFailure.body, COMMAND_CODE_INDEX);
    check(
      'C-25 failed refresh keeps the old snapshot values and observation time',
      cmcAfterFailure?.observed_at_ms === cmcT1
        && cmcAfterFailure?.plan?.credits?.balance === '321 credits'
        && JSON.stringify((cmcAfterFailure?.windows ?? []).map((window) => [window.label, window.remaining_percent]))
          === JSON.stringify((cmcBeforeFailure?.windows ?? []).map((window) => [window.label, window.remaining_percent]))
        && cmcAfterFailure?.last_success_at_ms === cmcT1,
      JSON.stringify({ observed_at_ms: cmcAfterFailure?.observed_at_ms, expected: cmcT1, plan: cmcAfterFailure?.plan, windows: cmcAfterFailure?.windows }),
    );
    check(
      'C-25 overview records the failed attempt without a zero reading',
      cmcAfterFailure?.last_refresh?.outcome === 'failed'
        && cmcAfterFailure?.last_refresh?.http_status === 502
        && !JSON.stringify(cmcAfterFailure?.windows ?? []).includes('"remaining_percent":0'),
      JSON.stringify(cmcAfterFailure?.last_refresh),
    );
    await openQuotaPage();
    await checkEventually(
      'C-25 row shows the failed refresh beside the marked last-successful reading',
      async () => {
        const text = await row(COMMAND_CODE_INDEX).locator('[data-quota-body]').innerText();
        return text.includes('刷新失败')
          && text.includes('HTTP 502')
          && text.includes('上次成功读数：')
          && !text.includes('上次成功读数：未知')
          && text.includes('321 credits');
      },
      { timeoutMs: 15000, detail: async () => (await row(COMMAND_CODE_INDEX).locator('[data-quota-body]').innerText()).replace(/\s+/g, ' ').slice(0, 300) },
    );
  } finally {
    fakeCpa.fixture.setQuotaMode('ok');
  }

  // ---- C-09: a 502 with no cache reads as a failure, never as zero ----
  mark = fakeCpa.requests.length;
  const upstreamFailure = await refreshCredential(UPSTREAM_ERROR_INDEX);
  check(
    'C-09 502 refresh answers with a failure status and no quota figures',
    upstreamFailure.status === 200
      && upstreamFailure.body?.quota?.status === 'error'
      && /HTTP 502/.test(upstreamFailure.body?.quota?.error ?? '')
      && (upstreamFailure.body?.quota?.windows ?? []).length === 0
      && upstreamFailure.body?.quota?.plan == null
      && pluginPostsFor(fakeCpa.requests.slice(mark), UPSTREAM_ERROR_INDEX).length === 1,
    JSON.stringify({ status: upstreamFailure.status, quota: upstreamFailure.body?.quota }),
  );
  await openQuotaPage();
  const upstreamRow = row(UPSTREAM_ERROR_INDEX).locator('[data-quota-body]');
  await checkEventually(
    'C-09 502 row shows refresh failure and never-succeeded reading without invented numbers',
    async () => {
      const text = await upstreamRow.innerText();
      return text.includes('刷新失败')
        && text.includes('HTTP 502')
        && text.includes('上次成功读数：未知')
        && text.includes('最近一次读取配额失败')
        && (await upstreamRow.getAttribute('data-quota-window-count')) === '0'
        && !text.includes('%')
        && !/Credit 余额|USD|credits/.test(text);
    },
    { timeoutMs: 15000, detail: async () => (await upstreamRow.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
  );

  // ---- C-17: the batch runs per item, deduplicates, and isolates one failure ----
  mark = fakeCpa.requests.length;
  const batch = await refreshBatch([
    COMMAND_CODE_INDEX,
    ` ${COMMAND_CODE_INDEX} `,
    OPENCODE_INDEX,
    UPSTREAM_ERROR_INDEX,
    OLLAMA_INDEX,
  ]);
  const batchResults = batch.body?.results ?? [];
  check(
    'C-17 batch returns one ordered result per unique credential',
    batch.status === 200
      && batchResults.length === 4
      && JSON.stringify(batchResults.map((result) => result.auth_index))
        === JSON.stringify([COMMAND_CODE_INDEX, OPENCODE_INDEX, UPSTREAM_ERROR_INDEX, OLLAMA_INDEX]),
    JSON.stringify(batchResults.map((result) => ({ auth_index: result.auth_index, outcome: result.outcome }))),
  );
  check(
    'C-17 batch reports each item outcome and isolates the 502 failure',
    batchResults[0]?.outcome === 'success'
      && batchResults[0]?.quota?.plan?.credits?.balance === '321 credits'
      && batchResults[1]?.outcome === 'success'
      && batchResults[2]?.outcome === 'failed'
      && /HTTP 502/.test(batchResults[2]?.error ?? '')
      && batchResults[3]?.outcome === 'success',
    JSON.stringify(batchResults.map((result) => ({ auth_index: result.auth_index, outcome: result.outcome, error: result.error }))),
  );
  check(
    'C-17 batch sends one plugin POST per unique credential',
    pluginPostsFor(fakeCpa.requests.slice(mark), COMMAND_CODE_INDEX).length === 1
      && pluginPostsFor(fakeCpa.requests.slice(mark), OPENCODE_INDEX).length === 1
      && pluginPostsFor(fakeCpa.requests.slice(mark), UPSTREAM_ERROR_INDEX).length === 1
      && pluginPostsFor(fakeCpa.requests.slice(mark), OLLAMA_INDEX).length === 1
      && pluginQuotaPosts(fakeCpa.requests.slice(mark)).length === 4,
    JSON.stringify(pluginQuotaPosts(fakeCpa.requests.slice(mark)).map((entry) => entry.body)),
  );
  const afterBatch = await getOverview();
  const upstreamAfterBatch = overviewItem(afterBatch.body, UPSTREAM_ERROR_INDEX);
  check(
    'C-17 the failed item gets no zero snapshot and no last-success time',
    upstreamAfterBatch?.last_refresh?.outcome === 'failed'
      && upstreamAfterBatch?.last_success_at_ms == null
      && (upstreamAfterBatch?.windows ?? []).length === 0,
    JSON.stringify({ last_refresh: upstreamAfterBatch?.last_refresh, last_success_at_ms: upstreamAfterBatch?.last_success_at_ms }),
  );

  // ---- C-21: an unmatched provider reports an error and reaches no plugin ----
  mark = fakeCpa.requests.length;
  const unmatched = await refreshCredential(UNMATCHED_INDEX);
  check(
    'C-21 unmatched provider fails with a clear error and no plugin POST',
    unmatched.status === 200
      && unmatched.body?.quota?.status === 'error'
      && /no enabled plugin provides quota/.test(unmatched.body?.quota?.error ?? '')
      && pluginPostsFor(fakeCpa.requests.slice(mark), UNMATCHED_INDEX).length === 0
      && pluginQuotaPosts(fakeCpa.requests.slice(mark)).length === 0,
    JSON.stringify({ status: unmatched.status, error: unmatched.body?.quota?.error, posts: pluginQuotaPosts(fakeCpa.requests.slice(mark)).length }),
  );
  check(
    'C-21 resolution consults the plugin list and the provider registry before refusing',
    fakeCpa.requests.slice(mark).some((entry) => entry.method === 'GET' && entry.path === PLUGINS_PATH)
      && fakeCpa.requests.slice(mark).some((entry) => entry.method === 'GET' && entry.path === QUOTA_PROVIDERS_PATH),
    JSON.stringify(fakeCpa.requests.slice(mark).map(({ method, path }) => ({ method, path }))),
  );
  const afterUnmatched = await getOverview();
  const unmatchedItem = overviewItem(afterUnmatched.body, UNMATCHED_INDEX);
  check(
    'C-21 the unmatched credential gains no snapshot',
    unmatchedItem?.last_refresh?.outcome === 'failed'
      && unmatchedItem?.last_success_at_ms == null
      && (unmatchedItem?.windows ?? []).length === 0,
    JSON.stringify({ last_refresh: unmatchedItem?.last_refresh, windows: unmatchedItem?.windows }),
  );

  // ---- C-22: quota_plugin_id wins over a second plugin claiming the provider ----
  fakeCpa.fixture.setDecoyPlugin(true);
  try {
    mark = fakeCpa.requests.length;
    const decoyRefresh = await refreshCredential(COMMAND_CODE_INDEX);
    const decoySlice = fakeCpa.requests.slice(mark);
    check(
      'C-22 quota_plugin_id addresses ollama-quota and never the decoy plugin',
      decoyRefresh.status === 200
        && decoyRefresh.body?.quota?.plan?.credits?.balance === '321 credits'
        && pluginPostsFor(decoySlice, COMMAND_CODE_INDEX).length === 1
        && pluginPostFor(decoySlice, COMMAND_CODE_INDEX)?.path === QUOTA_PLUGIN_POST_PATH
        && decoySlice.every((entry) => !entry.path.includes('/plugins/decoy-quota/')),
      JSON.stringify(decoySlice.map(({ method, path }) => ({ method, path }))),
    );
    check(
      'C-22 a credential with quota_plugin_id skips provider discovery',
      decoySlice.every((entry) => entry.path !== QUOTA_PROVIDERS_PATH),
      JSON.stringify(decoySlice.map(({ method, path }) => ({ method, path }))),
    );
  } finally {
    fakeCpa.fixture.setDecoyPlugin(false);
  }

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

    // C-19: the same old host still matches Ollama through the plugin list's
    // quota_provider, so that credential never needs the providers registry.
    mark = fakeCpa.requests.length;
    await refreshRow(OLLAMA_INDEX);
    await waitForPluginPost(OLLAMA_INDEX, mark);
    const legacyOllama = fakeCpa.requests.slice(mark);
    check(
      'C-19 legacy Ollama resolves through GET /plugins and skips the providers registry',
      legacyOllama.some((entry) => entry.method === 'GET' && entry.path === PLUGINS_PATH)
        && legacyOllama.every((entry) => entry.path !== QUOTA_PROVIDERS_PATH)
        && pluginPostsFor(legacyOllama, OLLAMA_INDEX).length === 1,
      JSON.stringify(legacyOllama.map(({ method, path }) => ({ method, path }))),
    );
  } finally {
    fakeCpa.fixture.setLegacyHost(false);
    fakeCpa.fixture.setQuotaMode('ok');
  }

  // ---- C-18 / C-08: a 429 parks the credential; the window refuses before the plugin ----
  // This runs last: the 429 parks the whole `ollama` provider for the fixture's
  // Retry-After (120s), so every later Ollama read would be refused at the API
  // layer. No phase after this one reads an Ollama credential.
  mark = fakeCpa.requests.length;
  const firstLimited = await refreshCredential(RATE_LIMITED_INDEX);
  check(
    'C-18 first 429 answers rate_limited_backoff with a future retry window',
    firstLimited.status === 429
      && firstLimited.body?.error === 'rate_limited_backoff'
      && typeof firstLimited.body?.retry_after_ms === 'number'
      && firstLimited.body.retry_after_ms > 100000
      && firstLimited.body.retry_after_ms <= 125000
      && typeof firstLimited.body?.retry_at_ms === 'number'
      && firstLimited.body.retry_at_ms > Date.now(),
    JSON.stringify(firstLimited.body),
  );
  check(
    'C-18 first 429 sends exactly one plugin POST',
    pluginPostsFor(fakeCpa.requests.slice(mark), RATE_LIMITED_INDEX).length === 1,
    JSON.stringify(pluginPostsFor(fakeCpa.requests.slice(mark), RATE_LIMITED_INDEX).map((entry) => entry.body)),
  );
  const afterFirstLimited = await getOverview();
  const limitedItem = overviewItem(afterFirstLimited.body, RATE_LIMITED_INDEX);
  check(
    'C-18 overview records rate_limited with HTTP 429 and a retry instant',
    limitedItem?.last_refresh?.outcome === 'rate_limited'
      && limitedItem?.last_refresh?.http_status === 429
      && typeof limitedItem?.last_refresh?.retry_at_ms === 'number'
      && limitedItem.last_refresh.retry_at_ms > Date.now(),
    JSON.stringify(limitedItem?.last_refresh),
  );
  check(
    'C-18 a never-successful rate-limited credential has no last success and no quota',
    limitedItem?.last_success_at_ms == null
      && (limitedItem?.windows ?? []).length === 0
      && limitedItem?.plan == null,
    JSON.stringify({ last_success_at_ms: limitedItem?.last_success_at_ms, windows: limitedItem?.windows, plan: limitedItem?.plan }),
  );

  const secondLimited = await refreshCredential(RATE_LIMITED_INDEX);
  check(
    'C-18 a refresh inside the backoff window is refused with the same code',
    secondLimited.status === 429
      && secondLimited.body?.error === 'rate_limited_backoff'
      && typeof secondLimited.body?.retry_after_ms === 'number'
      && secondLimited.body.retry_after_ms > 0,
    JSON.stringify(secondLimited.body),
  );
  check(
    'C-18 the refused refresh inside the window sends no second plugin POST',
    pluginPostsFor(fakeCpa.requests.slice(mark), RATE_LIMITED_INDEX).length === 1,
    JSON.stringify(fakeCpa.requests.slice(mark)
      .filter((entry) => entry.method === 'POST')
      .map((entry) => entry.path)),
  );

  // ---- C-08: the 429 row reads as rate limited, with no invented zero ----
  await openQuotaPage();
  const limitedRow = row(RATE_LIMITED_INDEX).locator('[data-quota-body]');
  await checkEventually(
    'C-08 429 row shows the rate-limited state and never-succeeded reading',
    async () => {
      const text = await limitedRow.innerText();
      return text.includes('已限流')
        && text.includes('HTTP 429')
        && text.includes('上次成功读数：未知')
        && text.includes('最近一次读取配额失败')
        && (await limitedRow.getAttribute('data-quota-window-count')) === '0'
        && !text.includes('%')
        && !/Credit 余额|USD|credits/.test(text);
    },
    { timeoutMs: 15000, detail: async () => (await limitedRow.innerText()).replace(/\s+/g, ' ').slice(0, 300) },
  );
}
