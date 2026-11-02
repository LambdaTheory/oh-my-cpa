import { until, settleLayout } from '../harness.mjs';

/**
 * Model groups in the console: one page to name a group, one to order its members, and one to
 * choose them from the gateway's directory.
 *
 * The claims that need a browser are the ones no component test can see: the rail entry is a
 * destination beside the model square, a phone width keeps every one of the three surfaces inside
 * the content pane, and the order a reload shows is the order the server stored. The fixture is
 * stateful for the same reason - "add three models, move one, reload" is a journey across three
 * routes and a write, and a table of static responses would answer the reload with the order the
 * test started from rather than the one it saved.
 */

const PRESETS = [
  { id: 'preset-labor', name: '力工组' },
  { id: 'preset-frontend', name: '前端组' },
  { id: 'preset-orchestrator', name: '编排组' },
  { id: 'preset-review', name: '审查组' },
  { id: 'preset-followup', name: '跟进组' },
];

const DIRECTORY = (() => {
  const ids = ['gpt-5', 'claude-sonnet-5-5', 'gemini-3-pro', 'kimi-k2'];
  return {
    models: ids.map((id) => ({ id, call_point: id, vision: 'unknown' })),
    providers: [],
    routes: [],
    partial: [],
    model_info: {},
    metadata_updated_at: '2026-11-02',
  };
})();

/** An in-memory deployment: the writes the pages send are what the next read answers. */
function modelGroupsFixture() {
  const now = 1;
  const clone = (value) => JSON.parse(JSON.stringify(value));
  const groups = PRESETS.map((preset) => ({
    ...preset, is_preset: true, created_at_ms: now, updated_at_ms: now, members: [],
  }));
  let nextCreated = 1;
  const find = (id) => groups.find((group) => group.id === id);
  const groupIdFromMembersPath = (pathname) => decodeURIComponent(pathname.split('/').slice(-2)[0]);
  const ordered = () => {
    // Presets first in their declared order, then the operator's own by name; the same order the
    // repository sorts, so a restored preset appears where the console would list it.
    const rank = new Map(PRESETS.map((preset, index) => [preset.id, index]));
    return [...groups].sort((left, right) => {
      if (left.is_preset !== right.is_preset) return left.is_preset ? -1 : 1;
      if (left.is_preset) return rank.get(left.id) - rank.get(right.id);
      return left.name.localeCompare(right.name);
    });
  };

  return [
    [(url, method) => method === 'GET' && url.pathname.endsWith('/management/model-groups'), () => ({ groups: ordered().map(clone) })],
    [(url, method) => method === 'POST' && url.pathname.endsWith('/management/model-groups/restore-defaults'), () => {
      const created = [];
      for (const preset of PRESETS) {
        if (groups.some((group) => group.id === preset.id)) continue;
        const group = { ...preset, is_preset: true, created_at_ms: now, updated_at_ms: now, members: [] };
        groups.push(group);
        created.push(clone(group));
      }
      const skipped = PRESETS.filter((preset) => !created.some((group) => group.id === preset.id)).map((preset) => preset.name);
      return { created, skipped };
    }],
    [(url, method) => method === 'POST' && url.pathname.endsWith('/management/model-groups'), (url, method, request) => {
      const name = JSON.parse(request.postData()).name.trim();
      const group = { id: `created-${nextCreated++}`, name, is_preset: false, created_at_ms: now, updated_at_ms: now, members: [] };
      groups.push(group);
      return clone(group);
    }],
    [(url, method) => method === 'POST' && /\/management\/model-groups\/[^/]+\/members$/.test(url.pathname), (url, method, request) => {
      const group = find(groupIdFromMembersPath(url.pathname));
      const added = [];
      const skipped = [];
      for (const model of JSON.parse(request.postData()).models) {
        if (group.members.some((member) => member.model === model)) {
          skipped.push(model);
          continue;
        }
        group.members.push({ model, position: group.members.length + 1, added_at_ms: now });
        added.push(model);
      }
      return { group: clone(group), added, skipped };
    }],
    [(url, method) => method === 'PUT' && /\/management\/model-groups\/[^/]+\/order$/.test(url.pathname), (url, method, request) => {
      const group = find(groupIdFromMembersPath(url.pathname));
      const addedAt = new Map(group.members.map((member) => [member.model, member.added_at_ms]));
      group.members = JSON.parse(request.postData()).models.map((model, index) => ({
        model, position: index + 1, added_at_ms: addedAt.get(model) ?? now,
      }));
      return clone(group);
    }],
    [(url, method) => method === 'DELETE' && /\/management\/model-groups\/[^/]+\/members\/[^/]+$/.test(url.pathname), (url) => {
      const parts = url.pathname.split('/');
      const group = find(decodeURIComponent(parts[parts.length - 3]));
      const model = decodeURIComponent(parts[parts.length - 1]);
      group.members = group.members
        .filter((member) => member.model !== model)
        .map((member, index) => ({ ...member, position: index + 1 }));
      return clone(group);
    }],
    [(url, method) => method === 'PATCH' && /\/management\/model-groups\/[^/]+$/.test(url.pathname), (url, method, request) => {
      const group = find(decodeURIComponent(url.pathname.split('/').pop()));
      group.name = JSON.parse(request.postData()).name.trim();
      group.updated_at_ms = now;
      return clone(group);
    }],
    [(url, method) => method === 'DELETE' && /\/management\/model-groups\/[^/]+$/.test(url.pathname), (url) => {
      const id = decodeURIComponent(url.pathname.split('/').pop());
      const index = groups.findIndex((group) => group.id === id);
      if (index >= 0) groups.splice(index, 1);
      return { status: 204, json: null };
    }],
    [(url, method) => method === 'GET' && url.pathname.endsWith('/management/model-square'), () => DIRECTORY],
  ];
}

export function modelGroupsFixtures() {
  return modelGroupsFixture();
}

/** The member names the detail table draws, in the order it draws them. */
async function drawnOrder(page) {
  return page.locator('[data-model-group-member]').allTextContents();
}

async function openGroup(page, id) {
  await page.locator(`[data-model-group-card="${id}"]`).getByRole('button', { name: 'Open' }).click();
  await page.locator('.model-group-detail-page').waitFor({ timeout: 20_000 });
}

/** Selects candidates by the attribute the checkbox carries, then submits the count it names. */
async function addModels(page, models) {
  await page.locator('.model-group-add-page').waitFor({ timeout: 20_000 });
  await until(async () => await page.locator('[data-select-model]').count() >= models.length, { label: 'candidate models' });
  for (const model of models) await page.locator(`[data-select-model="${model}"]`).click();
  const count = models.length;
  const button = page.getByRole('button', { name: `Add ${count} models` });
  await until(async () => await button.count() === 1 && !(await button.isDisabled()), { label: 'the add button' });
  await button.click();
}

export async function modelGroups({ base, page, check }) {
  await page.goto(`${base}/model-groups`, { waitUntil: 'domcontentloaded' });
  await page.locator('.model-groups-page').waitFor({ timeout: 20_000 });
  await until(async () => await page.locator('[data-model-group-card]').count() === 5, { label: 'five preset groups' });

  const railEntry = page.locator('.app-menu [data-route-path="/model-groups"]');
  check('the rail names model groups beside the model square', await railEntry.count() === 1, await page.locator('.app-menu [data-route-path="/model-square"]').count() + ' model-square entries');
  check('the entry is the current destination, not a tab inside the model square', await railEntry.evaluate((item) => item.classList.contains('ant-menu-item-selected')) && new URL(page.url()).pathname.endsWith('/model-groups'));

  const presetNames = await page.locator('[data-model-group-card] h2').allTextContents();
  check('the five preset groups are listed', PRESETS.every((preset) => presetNames.includes(preset.name)), presetNames.join(' | '));
  check('each empty group states it holds no models', (await page.locator('[data-model-group-card]').allInnerTexts()).every((text) => text.includes('0 models')));

  // Create: the name dialog opens from the page and the new empty group appears at once.
  await page.getByRole('button', { name: 'New group' }).click();
  const nameDialog = page.locator('.ant-modal:visible');
  await nameDialog.waitFor();
  await nameDialog.getByLabel('Group name').fill('Probe Group');
  await nameDialog.getByRole('button', { name: 'New group' }).click();
  await until(async () => await page.locator('[data-model-group-card="created-1"]').count() === 1, { label: 'the created group' });
  check('the created group is empty', (await page.locator('[data-model-group-card="created-1"]').innerText()).includes('0 models'));

  // Add three models, in the order the clicks select them.
  await openGroup(page, 'created-1');
  check('an empty group leads to adding models', (await page.locator('.empty-state').innerText()).includes('This group has no models yet'));
  await page.locator('.empty-state').getByRole('button', { name: 'Add models' }).click();
  await page.locator('.model-group-add-page').waitFor();
  await until(async () => await page.locator('[data-select-model]').count() === 4, { label: 'the gateway directory as candidates' });
  const search = page.getByRole('textbox', { name: 'Search models or makers' });
  await search.fill('gpt');
  await until(async () => await page.locator('[data-select-model]').count() === 1, { label: 'the search result' });
  check('search narrows the candidates by model name', await page.locator('[data-select-model="gpt-5"]').count() === 1);
  await search.fill('');
  await until(async () => await page.locator('[data-select-model]').count() === 4, { label: 'the full candidate list' });
  await addModels(page, ['gpt-5', 'claude-sonnet-5-5', 'gemini-3-pro']);

  await until(async () => new URL(page.url()).pathname.endsWith('/model-groups/created-1'), { label: 'the add page returns to the group' });
  await until(async () => await page.locator('[data-model-group-member]').count() === 3, { label: 'three members' });
  check('added models are appended in the order they were chosen', JSON.stringify(await drawnOrder(page)) === JSON.stringify(['gpt-5', 'claude-sonnet-5-5', 'gemini-3-pro']), JSON.stringify(await drawnOrder(page)));
  check('the first member is marked preferred', (await page.locator('tbody tr').first().innerText()).includes('Preferred'));

  // Move, save, reload: the stored order is what the next read draws.
  const firstRow = page.locator('tbody tr').filter({ has: page.locator('[data-model-group-member="gpt-5"]') });
  const savedOrder = page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().endsWith('/order') && response.ok());
  await firstRow.getByRole('button', { name: 'Move gpt-5 down' }).click();
  await savedOrder;
  await page.reload({ waitUntil: 'domcontentloaded' });
  await page.locator('.model-group-detail-page').waitFor();
  await until(async () => await page.locator('[data-model-group-member]').count() === 3, { label: 'members after reload' });
  check('a reordered group keeps its order after a reload', JSON.stringify(await drawnOrder(page)) === JSON.stringify(['claude-sonnet-5-5', 'gpt-5', 'gemini-3-pro']), JSON.stringify(await drawnOrder(page)));

  // The same model in a second group, ordered differently: each group has its own positions.
  await page.goto(`${base}/model-groups`, { waitUntil: 'domcontentloaded' });
  await page.locator('.model-groups-page').waitFor();
  await openGroup(page, 'preset-labor');
  await page.locator('.empty-state').getByRole('button', { name: 'Add models' }).click();
  await addModels(page, ['gpt-5', 'gemini-3-pro']);
  await until(async () => await page.locator('[data-model-group-member]').count() === 2, { label: 'two members in the second group' });
  const geminiRow = page.locator('tbody tr').filter({ has: page.locator('[data-model-group-member="gemini-3-pro"]') });
  const savedSecond = page.waitForResponse((response) => response.request().method() === 'PUT' && response.url().endsWith('/order') && response.ok());
  await geminiRow.getByRole('button', { name: 'Move gemini-3-pro up' }).click();
  await savedSecond;
  check('the second group keeps its own order', JSON.stringify(await drawnOrder(page)) === JSON.stringify(['gemini-3-pro', 'gpt-5']), JSON.stringify(await drawnOrder(page)));
  await page.goto(`${base}/model-groups/created-1`, { waitUntil: 'domcontentloaded' });
  await page.locator('.model-group-detail-page').waitFor();
  await until(async () => await page.locator('[data-model-group-member]').count() === 3, { label: 'the first group after the second moved' });
  check('the shared model keeps the first group’s order, independent of the second', JSON.stringify(await drawnOrder(page)) === JSON.stringify(['claude-sonnet-5-5', 'gpt-5', 'gemini-3-pro']), JSON.stringify(await drawnOrder(page)));

  // A phone: every one of the three surfaces stays inside the content pane.
  for (const [route, selector] of [
    ['/model-groups', '.model-groups-page'],
    ['/model-groups/created-1', '.model-group-detail-page'],
    ['/model-groups/created-1/add', '.model-group-add-page'],
  ]) {
    await page.setViewportSize({ width: 375, height: 844 });
    await page.goto(`${base}${route}`, { waitUntil: 'domcontentloaded' });
    await page.locator(selector).waitFor({ timeout: 20_000 });
    await settleLayout(page);
    const overflow = await page.locator('.app-content').evaluate((pane) => pane.scrollWidth - pane.clientWidth);
    check(`${route} fits 375px without moving sideways`, overflow <= 1, `overflow=${overflow}`);
  }

  // Delete everything, meet the empty state, then put the preset groups back empty.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`${base}/model-groups`, { waitUntil: 'domcontentloaded' });
  await page.locator('.model-groups-page').waitFor();
  await until(async () => await page.locator('[data-model-group-card]').count() === 6, { label: 'the groups before deletion' });
  for (const id of ['preset-labor', 'preset-frontend', 'preset-orchestrator', 'preset-review', 'preset-followup', 'created-1']) {
    const card = page.locator(`[data-model-group-card="${id}"]`);
    await card.getByRole('button', { name: 'Delete' }).click();
    const confirmation = page.locator('.ant-popconfirm:visible');
    await confirmation.waitFor();
    if (id === 'preset-labor') {
      check('deleting a group says the models themselves are unaffected', (await confirmation.innerText()).includes('The models themselves are unaffected.'));
    }
    await confirmation.getByRole('button', { name: 'Delete' }).click();
    await until(async () => await card.count() === 0, { label: `${id} deleted` });
  }
  check('deleting every group draws the empty state', await page.getByText('No model groups yet').count() === 1);
  await page.getByRole('button', { name: 'Restore default groups' }).click();
  const restoreConfirmation = page.locator('.ant-popconfirm:visible');
  await restoreConfirmation.waitFor();
  check('the restore confirmation says restored groups come back empty', (await restoreConfirmation.innerText()).includes('empty'));
  await restoreConfirmation.getByRole('button', { name: 'Restore' }).click();
  await until(async () => await page.locator('[data-model-group-card]').count() === 5, { label: 'the five presets restored' });
  const restoredNames = await page.locator('[data-model-group-card] h2').allTextContents();
  check('restore recreates the five preset groups', PRESETS.every((preset) => restoredNames.includes(preset.name)), restoredNames.join(' | '));
  check('restored groups are empty', (await page.locator('[data-model-group-card]').allInnerTexts()).every((text) => text.includes('0 models')));
}
