import { until } from '../harness.mjs';

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
          used_percent: 65,
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
  await page.route('**/omc/api/v1/management/dashboard/predictions', async (route) => {
    reads += 1;
    await route.fulfill({ json: PREDICTION_RESPONSE });
  });
  await page.route('**/omc/api/v1/feishu/prediction-test', async (route) => {
    sends += 1;
    await route.fulfill({ json: { sent: false, reason: 'no_findings' } });
  });

  await page.goto(`${base}/dashboard`, { waitUntil: 'domcontentloaded' });
  const panel = page.getByTestId('dashboard-quota-prediction');
  await panel.waitFor({ timeout: 20_000 });
  await panel.getByText('operator@example.com').waitFor();
  check('the dashboard groups and renders the operator prediction row',
    await panel.locator('[data-verdict="will_exhaust"]').count() === 1
      && (await panel.innerText()).includes('65.0%')
      && (await panel.innerText()).includes('110.0%')
      && (await panel.innerText()).includes('7 ms'));

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
}
