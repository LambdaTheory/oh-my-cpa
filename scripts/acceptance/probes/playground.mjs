import { until } from '../harness.mjs';

/**
 * Playground is retired as a routed page: the product sends `/playground` to the
 * dashboard and keeps no navigation entry for it. The old scenarios asserted the
 * hidden page's streaming, inspector and phone geometry, which no route can reach
 * any more. They keep their catalog ids but now assert the shipped behaviour instead -
 * a legitimate change of claim, not a deleted scenario:
 *
 *   - the address redirects to the dashboard and renders no playground page,
 *   - the console offers no way into the retired surface,
 *   - the surviving conversation workspace (Agent) still opens from the navigation.
 *
 * The API fixtures below are kept because the Agent workspace and the scroll-smoothing
 * model popup are built on the same shared gateway reads.
 */
export function playgroundFixtures() {
  return [
    [(url, method) => method === 'GET' && url.pathname.endsWith('/usage/ingest-status'), () => ({ enabled:true, healthy:true, collector:{mode:'http_pull',captured:0,coverage_gaps:0},stats:{pending:0} })],
    [(url, method) => method === 'GET' && url.pathname.endsWith('/usage/facets'), () => ({window:{from:0,to:Date.now(),bucket_ms:60_000}, facets:[]})],
    [url => url.pathname.endsWith('/runs/active'), () => ({ run: null })],
    [url => url.pathname.endsWith('/management/api-keys'), () => ({ keys: [{ index: 0, key: 'fixture…mask', usage_fingerprint: 'playground-identity', alias: 'Test key', alias_version: 1, length: 20, fingerprint: 'legacy-identity' }], total: 1 })],
    [url => url.pathname.endsWith('/playground/models'), () => ({ models: [{ id: 'vision-alias', call_point: 'vision-alias', vision: 'unknown' }, { id: 'text-only', call_point: 'text-only', vision: 'unknown' }] })],
  ];
}

export async function playground({ base, page, check }) {
  await page.goto(`${base}/playground`, { waitUntil: 'domcontentloaded' });
  await until(() => new URL(page.url()).pathname.endsWith('/dashboard'), { label: 'the retired playground address to redirect to the dashboard' });
  check('the retired playground address redirects to the dashboard', new URL(page.url()).pathname.endsWith('/dashboard'), page.url());
  await page.locator('.dashboard-page').waitFor({ state: 'visible' });
  check('the retired playground renders no page of its own', await page.locator('[data-testid="playground-page"]').count() === 0);
  check('the console navigation offers no playground entry', await page.locator('[data-route-path="/playground"]').count() === 0);

  const agentEntry = page.locator('[data-route-path="/agent"]');
  check('the Agent workspaces entry survives the retirement', await agentEntry.count() >= 1);
  await agentEntry.first().click();
  await until(() => new URL(page.url()).pathname.endsWith('/agent'), { label: 'the Agent route to open from the navigation' });
  await page.locator('[data-testid="agent-page"]').waitFor({ state: 'visible' });
  check('the Agent workspace still opens after the retirement', await page.locator('[data-testid="agent-page"]').count() === 1);
}

export async function playgroundNarrow({ base, page, check }) {
  await page.goto(`${base}/playground`, { waitUntil: 'domcontentloaded' });
  await until(() => new URL(page.url()).pathname.endsWith('/dashboard'), { label: 'the retired playground address to redirect on a phone' });
  check('the retired playground address redirects on a phone', new URL(page.url()).pathname.endsWith('/dashboard'), page.url());
  check('no phone layout of the retired playground is rendered', await page.locator('[data-testid="playground-page"]').count() === 0);

  await page.locator('.app-header-left button').click();
  const navigation = page.locator('.mobile-nav-drawer:visible');
  await navigation.waitFor({ state: 'visible' });
  check('the phone navigation offers no playground entry', await navigation.locator('[data-route-path="/playground"]').count() === 0);
  const agentEntry = navigation.locator('[data-route-path="/agent"]');
  check('the phone navigation keeps the Agent reachable', await agentEntry.count() >= 1);
  await agentEntry.first().click();
  await until(() => new URL(page.url()).pathname.endsWith('/agent'), { label: 'the Agent route to open from the phone navigation' });
  await page.locator('[data-testid="agent-page"]').waitFor({ state: 'visible' });
  check('the Agent workspace opens on a phone after the retirement', await page.locator('[data-testid="agent-page"]').count() === 1);
}
