import { settleLayout, until } from '../harness.mjs';
import { fulfillFixture } from '../browser-guard.mjs';
import fs from 'node:fs';

const PREDICTION_RESPONSE = {
  generated_at_ms: Date.parse('2026-10-20T08:00:00Z'),
  duration_ms: 7,
  omitted: 0,
  providers: [
    {
      provider: 'codex',
      items: [
        {
          credential_label: 'operator@example.com',
          auth_index: 'codex-operator',
          provider: 'codex',
          window_kind: 'weekly',
          window_id: 'weekly',
          window_scope: 'standard',
          used_percent: 65,
          window_start_ms: Date.parse('2026-10-14T08:00:00Z'),
          remaining_percent: 35,
          uniform_projected_percent_at_reset: 75.83,
          reset_at_ms: Date.parse('2026-10-21T08:00:00Z'),
          observed_at_ms: Date.parse('2026-10-20T07:55:00Z'),
          data_age_seconds: 300,
          burn_rate_per_hour: 4.2,
          projected_percent_at_reset: 110,
          projected_tokens_at_reset: 125000,
          projected_cost_nanos_at_reset: 125000000,
          exhaustion_eta_ms: Date.parse('2026-10-21T06:00:00Z'),
          verdict: 'will_exhaust',
          evidence: 'projection',
          reasons: ['no_price'],
        },
      ],
    },
  ],
};

export async function dashboardQuotaPrediction({ base, page, check }) {
  let reads = 0;
  let sends = 0;
  let hasReadError = false;
  const sample = PREDICTION_RESPONSE.providers[0].items[0];
  const predictions = { ...PREDICTION_RESPONSE, providers: [
    ...PREDICTION_RESPONSE.providers,
    { provider: 'claude', items: [{ ...sample, provider: 'claude', credential_label: 'stale@example.com', verdict: 'unknown', reasons: ['stale_reading'], data_age_seconds: 90000, projected_percent_at_reset: null, uniform_projected_percent_at_reset: null, remaining_percent: null }] },
    { provider: 'antigravity', items: [{ ...sample, provider: 'antigravity', credential_label: 'gravity@example.com' }] },
  ] };
  await page.route('**/omc/api/**/preferences', (route) => route.fulfill({
    json: { preferences: { omc_dashboard_layout: [{ id: 'quota_prediction', span: 2, hidden: false }] }, time_zone: { server_timezone: 'UTC' } },
  }));
  await page.route('**/omc/api/v1/management/dashboard/predictions', async (route) => {
    reads += 1;
    await fulfillFixture(route, hasReadError ? { status: 500, json: { error: 'Prediction read failed' } } : { json: predictions });
  });
  await page.route('**/omc/api/v1/feishu/prediction-test', async (route) => {
    sends += 1;
    await route.fulfill({ json: { sent: false, reason: 'no_findings' } });
  });

  await page.goto(`${base}/dashboard`, { waitUntil: 'domcontentloaded' });
  const panel = page.getByTestId('dashboard-quota-prediction');
  await panel.waitFor({ timeout: 20_000 });
  await panel.locator('summary').first().waitFor();
  check('providers initially disclose only summaries',
    await panel.locator('details[open]').count() === 0
      && await panel.getByText('operator@example.com').isHidden());
  const codexSummary = panel.getByRole('region', { name: 'Codex', exact: true }).locator('summary');
  await codexSummary.focus();
  await page.keyboard.press('Enter');
  await panel.getByText('operator@example.com').waitFor();
  await page.keyboard.press('Space');
  await until(async () => await panel.getByText('operator@example.com').isHidden(), { label: 'keyboard collapse' });
  for (const summary of await panel.locator('summary').all()) await summary.click();
  await panel.getByText('operator@example.com').waitFor();
  check('the dashboard groups and renders the operator prediction row',
    await panel.locator('[data-verdict="will_exhaust"]').count() === 2
      && (await panel.innerText()).includes('65.0%')
      && (await panel.innerText()).includes('110.0%')
      && (await panel.innerText()).includes('7 ms'));
  const groups = await panel.locator('section').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('aria-label')));
  check('provider headings follow dashboard order', groups.join(',') === 'Antigravity,Claude,Codex', groups.join(','));
  check('unknown stale evidence remains visible', await panel.locator('[data-verdict="unknown"]').count() === 1
    && await panel.getByText(/Quota reading is stale|额度读数已过期/).count() === 1);

  const cell = page.locator('[data-widget="quota_prediction"]');
  await until(async () => await cell.getAttribute('data-span') === '2', { label: 'the prediction span-2 arrangement' });
  async function checkGeometry(label) {
    await settleLayout(page);
    const geometry = await panel.evaluate((node) => {
      const bounds = node.getBoundingClientRect();
      const facts = [...node.querySelectorAll('dd[title]')];
      return {
        width: bounds.width,
        overflow: node.scrollWidth > node.clientWidth + 1,
        documentOverflow: document.documentElement.scrollWidth > window.innerWidth + 1,
        facts: facts.length,
        fullValues: facts.every((fact) => fact.title === fact.textContent && fact.scrollWidth <= fact.clientWidth + 1 && fact.scrollHeight <= fact.clientHeight + 1),
        rowsInside: [...node.querySelectorAll('li')].every((row) => row.getBoundingClientRect().right <= bounds.right + 1),
      };
    });
    check(`${label} fits its container and preserves complete reset/projection/ETA values`,
      !geometry.overflow && !geometry.documentOverflow && geometry.rowsInside && geometry.facts >= 6 && geometry.fullValues, JSON.stringify(geometry));
  }
  await checkGeometry('desktop span 2');
  fs.mkdirSync('tmp/issue-78', { recursive: true });
  for (const summary of await panel.locator('summary').all()) await summary.click();
  await until(async () => panel.locator('img').evaluateAll((images) => images.every((image) => image.complete && image.naturalWidth > 0)), { label: 'provider artwork decoded' });
  await panel.evaluate((node) => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: 'tmp/issue-78/desktop-collapsed.png' });
  await codexSummary.click();
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'tmp/issue-78/desktop.png' });
  for (const summary of await panel.locator('summary').all()) {
    if (!await summary.evaluate((node) => node.parentElement.open)) await summary.click();
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await until(async () => (await panel.boundingBox()).width < 390, { label: 'the mobile prediction container' });
  await checkGeometry('mobile span 2');
  for (const group of await panel.locator('section').all()) {
    if (await group.getAttribute('aria-label') !== 'Codex') await group.locator('summary').click();
  }
  await page.setViewportSize({ width: 390, height: 1500 });
  await settleLayout(page);
  await panel.evaluate((node) => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: 'tmp/issue-78/phone.png' });
  await page.setViewportSize({ width: 390, height: 844 });

  const initialReads = reads;
  await panel.getByRole('button', { name: /刷新额度预测|Refresh quota prediction/ }).click();
  await until(() => reads > initialReads, { label: 'the manual prediction refresh' });
  check('manual refresh reads the stored prediction endpoint again', reads === initialReads + 1, `reads=${reads}`);

  await panel.getByRole('button', { name: /发送预测摘要到飞书|Send prediction summary to Feishu/ }).click();
  await page.getByRole('button', { name: /发送摘要|Send summary/ }).click();
  await until(() => sends === 1, { label: 'the confirmed prediction send' });
  check('a confirmed manual send posts exactly once', sends === 1);
  await page.getByText(/没有可发送的预测发现|No prediction findings to send/).waitFor();
  check('a no-findings result is reported as an informational toast', sends === 1);

  hasReadError = true;
  await panel.getByRole('button', { name: /刷新额度预测|Refresh quota prediction/ }).click();
  await panel.getByText(/Prediction read failed/).waitFor();
  check('a failed refresh retains the last rows and disables send',
    await panel.getByText('operator@example.com').count() === 1
      && await panel.getByRole('button', { name: /发送预测摘要到飞书|Send prediction summary to Feishu/ }).isDisabled());
}
