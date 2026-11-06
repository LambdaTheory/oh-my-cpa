import { settleLayout } from '../harness.mjs';

const KEY = 'hmac:insights-probe';
const DAY_MS = 86_400_000;
const FROM = Date.UTC(2026, 0, 1);
const WINDOW = { preset: '7d', from: FROM, to: FROM + 7 * DAY_MS - 1, open_end: false, bucket: 'day', timezone: 'UTC', widened: false };

export function insightsFixtures() {
  return [
    [(url) => url.pathname.endsWith('/management/insights/cube'), () => ({
      window: WINDOW, compare_window: null, bucket_starts: Array.from({ length: 7 }, (_, day) => FROM + day * DAY_MS),
      dims: {
        keys: [{ id: KEY, alias: 'Probe team', mask: 'sk-…probe', is_client_key: true }],
        call_points: ['probe-model'], models: ['probe-model'], providers: ['probe-provider'],
        credentials: [{ auth_index: 'probe-credential', auth_type: 'apikey' }],
      },
      columns: ['bucket', 'key', 'call_point', 'model', 'provider', 'credential', 'requests', 'failures', 'input_tokens', 'output_tokens', 'reasoning_tokens', 'cached_tokens', 'cache_read_tokens', 'cache_creation_tokens', 'total_tokens', 'latency_sum_ms', 'ttft_sum_ms', 'ttft_count', 'cost_nanos', 'priced_requests', 'costed_requests'],
      rows: [[0, 0, 0, 0, 0, 0, 20, 0, 1000, 1000, 0, 0, 0, 0, 2000, 2000, 0, 0, 2_000_000_000, 20, 20]],
      previous_rows: [], coverage: { fact_requests: 20, detail_requests: 0 },
    })],
    [(url) => url.pathname.endsWith('/management/insights/rhythm'), () => ({
      window: WINDOW, cells: [{ weekday: 3, hour: 10, requests: 20, total_tokens: 2000, cost_nanos: 2_000_000_000 }],
    })],
  ];
}

export async function insights({ base, page, check }) {
  await page.goto(`${base}/insights?metric=cost`, { waitUntil: 'domcontentloaded' });
  const screen = page.getByTestId('insights-page');
  const keys = screen.getByTestId('insights-keys');
  const key = keys.locator(`button[title="${KEY}"]`);
  await key.waitFor({ state: 'visible' });
  // Axes and zero-value bars also have SVG paths; require a filled data bar with area.
  await page.waitForFunction(() => [...document.querySelectorAll('[data-testid="insights-hour-chart"] svg path[fill]:not([fill="none"]):not([stroke])')]
    .some(path => {
      const bounds = path.getBoundingClientRect();
      return bounds.width > 0 && bounds.height > 0;
    }));
  check('Insights renders the captured key and its spend', (await keys.innerText()).includes('Probe team') && (await keys.innerText()).includes('$2.00'));

  await key.click();
  const drawer = page.getByTestId('insights-key-drawer');
  await drawer.waitFor({ state: 'visible' });
  check('a key row opens a visible detail overlay with its mask and totals',
    (await drawer.innerText()).includes('sk-…probe') && (await drawer.innerText()).includes('$2.00'));
  // Native Back owns this overlay entry rather than leaving the Insights route.
  await page.goBack();
  await drawer.waitFor({ state: 'hidden' });
  check('Back closes the key overlay without leaving Insights', new URL(page.url()).pathname.endsWith('/insights') && await screen.isVisible());

  await page.setViewportSize({ width: 375, height: 844 });
  const ranking = screen.getByTestId('insights-ranking-key');
  await ranking.waitFor({ state: 'visible' });
  await settleLayout(page);
  const geometry = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth }));
  check('Insights fits a phone without horizontal page scrolling', geometry.scroll <= geometry.width, JSON.stringify(geometry));
  check('the phone keeps its key ranking instead of the desktop table', await ranking.isVisible() && !await keys.isVisible());
  await ranking.getByRole('button', { name: 'Open details', exact: true }).click();
  await drawer.waitFor({ state: 'visible' });
  const bounds = await drawer.boundingBox();
  check('the key detail remains inside the phone viewport', bounds !== null && bounds.x >= 0 && bounds.x + bounds.width <= 376, JSON.stringify(bounds));
  await page.goBack();
  await drawer.waitFor({ state: 'hidden' });
}
