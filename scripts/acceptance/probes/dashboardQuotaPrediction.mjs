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
          expected_used_percent: 85.7,
          lasts_until_reset: false,
          reading_stale: false,
          unavailable_reason: '',
          window_start_ms: Date.parse('2026-10-14T08:00:00Z'),
          remaining_percent: 35,
          remaining_tokens: 125000,
          capacity_error_percent: 1.2,
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
    { provider: 'claude', items: [{
      ...sample, provider: 'claude', credential_label: 'stale@example.com',
      expected_used_percent: null, lasts_until_reset: null, reading_stale: true,
      unavailable_reason: 'reading_failed', verdict: 'unknown', reasons: ['stale_reading'],
      observed_at_ms: PREDICTION_RESPONSE.generated_at_ms - 90_000_000, data_age_seconds: 90000,
      exhaustion_eta_ms: null, projected_percent_at_reset: null,
      uniform_projected_percent_at_reset: null, remaining_percent: null,
    }] },
    { provider: 'antigravity', items: [{ ...sample, provider: 'antigravity', credential_label: 'gravity@example.com' }] },
    { provider: 'ollama', items: [{
      ...sample, provider: 'ollama', credential_label: 'ollama@example.test',
      expected_used_percent: null, lasts_until_reset: null, unavailable_reason: 'window_boundary_unknown',
      verdict: 'unknown', used_percent: 95, reset_at_ms: null,
      exhaustion_eta_ms: null, reasons: ['boundary_unknown'], remaining_tokens: null,
    }] },
    { provider: 'step', items: [{
      ...sample, provider: 'step', credential_label: 'step@example.test',
      lasts_until_reset: true, verdict: 'on_track', used_percent: 25, exhaustion_eta_ms: null, reasons: [],
    }] },
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
  const providerSummaries = panel.locator('section > details > summary');
  const codexGroup = panel.getByRole('region', { name: 'Codex', exact: true });
  const codexSummary = codexGroup.locator(':scope > details > summary');
  await codexSummary.waitFor();
  check('providers initially disclose only summaries',
    await providerSummaries.count() === 5
      && await codexSummary.count() === 1
      && await panel.locator('details[open]').count() === 0
      && await panel.getByText('operator@example.com').isHidden());
  await codexSummary.focus();
  await page.keyboard.press('Enter');
  await panel.getByText('operator@example.com').waitFor();
  await page.keyboard.press('Space');
  await until(async () => await panel.getByText('operator@example.com').isHidden(), { label: 'keyboard collapse' });
  for (const summary of await providerSummaries.all()) await summary.click();
  await panel.getByText('operator@example.com').waitFor();
  const codexRow = codexGroup.locator('li');
  const expectedETA = await page.evaluate((value) => {
    const date = new Date(value);
    const pad = (number) => String(number).padStart(2, '0');
    return `${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
  }, sample.exhaustion_eta_ms);
  check('the dashboard groups and renders the four-field operator prediction row',
    await panel.locator('li[data-lasts="no"]').count() === 2
      && await panel.locator('li[data-lasts="yes"]').count() === 1
      && await panel.locator('li[data-lasts="unknown"]').count() === 2
      && (await codexRow.locator('b').allTextContents()).join('|') === '65.0%|85.7%|20.7 pts under'
      && await codexRow.getByText('Actual', { exact: true }).isVisible()
      && await codexRow.getByText('Expected', { exact: true }).isVisible()
      && await codexRow.getByText('Deviation', { exact: true }).isVisible()
      && await codexRow.getByText(expectedETA, { exact: true }).isVisible()
      && await codexRow.getByText('Cannot last', { exact: true }).isVisible()
      && await panel.getByTestId('prediction-summary').innerText() === '2 windows cannot last until reset · 2 undecidable'
      && (await codexSummary.innerText()).includes('1 credentials · 1 cannot last')
      && await panel.getByText('Computed in 7 ms', { exact: true }).isVisible());
  const stepRow = panel.getByRole('region', { name: 'Step', exact: true }).locator('li');
  check('a sustainable window has an explicit conclusion and no exhaustion estimate',
    (await stepRow.locator('b').allTextContents()).join('|') === '25.0%|85.7%|60.7 pts under'
      && await stepRow.getByText('Lasts until reset', { exact: true }).isVisible()
      && await stepRow.getByText('Will not exhaust', { exact: true }).isVisible());
  const groups = await panel.locator('section').evaluateAll((nodes) => nodes.map((node) => node.getAttribute('aria-label')));
  check('provider headings follow dashboard order', groups.join(',') === 'Antigravity,Claude,Codex,Ollama,Step', groups.join(','));
  const staleRow = panel.getByRole('region', { name: 'Claude', exact: true }).locator('li');
  const boundaryRow = panel.getByRole('region', { name: 'Ollama', exact: true }).locator('li');
  const readingFailure = 'The latest quota read failed; check credential auth and upstream status, then retry.';
  const boundaryUnknown = 'The window boundaries are unknown, so progress cannot be calculated; check whether upstream returns a reset time. Quotas without a fixed period belong here.';
  check('unknown evidence retains distinct stale-reading and boundary guidance without fabricated values',
    (await staleRow.locator('b').allTextContents()).join('|') === '65.0%|Cannot be calculated|—'
      && await staleRow.getByText('Undecidable', { exact: true }).isVisible()
      && await staleRow.getByText('Stale', { exact: true }).isVisible()
      && await staleRow.getByText('The reading is stale, so it is not estimated.', { exact: true }).isVisible()
      && await staleRow.getByText('Cannot be estimated', { exact: true }).isVisible()
      && (await boundaryRow.locator('b').allTextContents()).join('|') === '95.0%|Cannot be calculated|—'
      && await boundaryRow.getByText('Undecidable', { exact: true }).isVisible()
      && await boundaryRow.locator(':scope > p').filter({ hasText: boundaryUnknown }).isVisible()
      && await boundaryRow.getByText('Cannot be estimated', { exact: true }).isVisible());
  const diagnostics = panel.locator('li > details');
  check('diagnostics initially remain collapsed independently of provider groups',
    await diagnostics.count() === 5 && await panel.locator('li > details[open]').count() === 0);
  await settleLayout(page);
  const staleDiagnostics = staleRow.locator('details');
  const reasonTrigger = staleDiagnostics.locator('summary');
  const detailedReason = staleDiagnostics.locator('p').filter({ hasText: 'Quota reading is stale' });
  check('detailed reasons are hidden before keyboard disclosure',
    await detailedReason.isHidden() && await staleDiagnostics.getByText(readingFailure, { exact: true }).isHidden());
  await reasonTrigger.focus();
  await page.keyboard.press('Enter');
  await detailedReason.waitFor();
  check('detailed reasons and reading-failure guidance are reachable by keyboard',
    await staleDiagnostics.evaluate((node) => node.open)
      && await detailedReason.isVisible()
      && await staleDiagnostics.getByText(readingFailure, { exact: true }).isVisible());
  await page.keyboard.press('Space');
  await until(async () => await detailedReason.isHidden(), { label: 'keyboard collapse diagnostics' });
  check('keyboard collapse hides diagnostics without closing its provider group',
    !await staleDiagnostics.evaluate((node) => node.open)
      && await staleRow.isVisible()
      && await panel.locator('section > details[open]').count() === 5);
  await page.keyboard.press('Tab');
  await page.mouse.move(0, 0);

  const cell = page.locator('[data-widget="quota_prediction"]');
  await until(async () => await cell.getAttribute('data-span') === '2', { label: 'the prediction span-2 arrangement' });
  async function checkGeometry(label) {
    await settleLayout(page);
    const geometry = await panel.evaluate((node) => {
      const bounds = node.getBoundingClientRect();
      const facts = [...node.querySelectorAll('p span[title]')];
      const readings = [...node.querySelectorAll('li b')];
      return {
        width: bounds.width,
        overflow: node.scrollWidth > node.clientWidth + 1,
        documentOverflow: document.documentElement.scrollWidth > window.innerWidth + 1,
        facts: facts.length,
        fullValues: facts.every((fact) => fact.title === fact.textContent && fact.scrollWidth <= fact.clientWidth + 1 && fact.scrollHeight <= fact.clientHeight + 1),
        readings: readings.length,
        fullReadings: readings.every((reading) => reading.scrollWidth <= reading.clientWidth + 1 && reading.scrollHeight <= reading.clientHeight + 1),
        rowsInside: [...node.querySelectorAll('li')].every((row) => row.getBoundingClientRect().right <= bounds.right + 1),
      };
    });
    check(`${label} fits its container and preserves actual, expected, deviation and reset values`,
      !geometry.overflow && !geometry.documentOverflow && geometry.rowsInside
        && geometry.facts >= 5 && geometry.fullValues
        && geometry.readings === 15 && geometry.fullReadings, JSON.stringify(geometry));
  }
  await checkGeometry('desktop span 2');
  fs.mkdirSync('tmp/issue-78', { recursive: true });
  for (const summary of await providerSummaries.all()) await summary.click();
  await until(async () => panel.locator('img').evaluateAll((images) => images.every((image) => image.complete && image.naturalWidth > 0)), { label: 'provider artwork decoded' });
  await panel.evaluate((node) => node.scrollIntoView({ block: 'start' }));
  await page.screenshot({ path: 'tmp/issue-78/desktop-collapsed.png' });
  await codexSummary.click();
  await panel.scrollIntoViewIfNeeded();
  await page.screenshot({ path: 'tmp/issue-78/desktop.png' });
  for (const summary of await providerSummaries.all()) {
    if (!await summary.evaluate((node) => node.parentElement.open)) await summary.click();
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await until(async () => (await panel.boundingBox()).width < 390, { label: 'the mobile prediction container' });
  await checkGeometry('mobile span 2');
  for (const group of await panel.locator('section').all()) {
    if (await group.getAttribute('aria-label') !== 'Codex') await group.locator(':scope > details > summary').click();
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
