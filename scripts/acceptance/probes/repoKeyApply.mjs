import { settleLayout } from '../harness.mjs';

export async function repoKeyApply({ base, page, check }) {
  // This is an in-memory approval link, not a credential or a request to a live gateway.
  const token = 'probe-approval-link';
  const path = `**/management/repo-keys/apply-link/${token}`;
  let application = {
    id: 'probe-application', repo: 'example/probe-repository', repo_has_remote: true,
    agent: 'probe-agent', device: 'probe-device', purpose: 'Verify the standalone approval screen',
    kind: 'issue', status: 'pending', source: 'agent', public_key_fingerprint: 'probe-fingerprint-'.repeat(8),
    ciphertext_ready: false, created_at_ms: Date.UTC(2026, 0, 1),
  };
  const decisions = [];
  await page.route(path, route => route.fulfill({ json: {
    application,
    repo: { repo: application.repo, repo_has_remote: true, state: 'none', stops: [], usage: {
      window: '7d', from_ms: 0, to_ms: 0, requests: 0, failed: 0, total_tokens: 0, cost_nanos: 0, last_used_ms: 0, models: [],
    } },
    public_key_changed: false, impact: 'issue', approval_expired: false,
  } }));
  await page.route(`${path}/reject`, async route => {
    decisions.push({ method: route.request().method(), body: JSON.parse(route.request().postData()) });
    application = { ...application, status: 'rejected', reject_reason: decisions.at(-1).body.reason };
    await route.fulfill({ json: { application } });
  });

  await page.goto(`${base}/repo-key-apply/${token}`, { waitUntil: 'domcontentloaded' });
  const screen = page.getByTestId('repo-key-apply-page');
  const reason = screen.getByLabel('Reason (required to reject, optional to approve)', { exact: true });
  const reject = screen.getByRole('button', { name: 'Reject', exact: true });
  const approve = screen.getByRole('button', { name: 'Approve this request', exact: true });
  await reason.waitFor({ state: 'visible' });
  check('the standalone approval screen renders the repository and applicant',
    (await screen.innerText()).includes(application.repo) && (await screen.innerText()).includes('probe-agent @ probe-device'));
  check('the approval link stands outside the console shell', await page.locator('.ant-layout-sider, .app-breadcrumb').count() === 0);
  check('rejecting needs a reason while approval is available', await reject.isDisabled() && await approve.isEnabled());

  await settleLayout(page);
  const geometry = await page.evaluate(() => ({ width: innerWidth, scroll: document.documentElement.scrollWidth }));
  check('the approval facts and long fingerprint fit a phone', geometry.scroll <= geometry.width, JSON.stringify(geometry));
  await reason.fill('The request is for the wrong repository.');
  await reject.click();
  const outcome = screen.getByTestId('repo-key-apply-outcome');
  await outcome.waitFor({ state: 'visible' });
  check('the decision replaces the controls with its status and reason',
    (await outcome.innerText()).includes('Rejected') && (await outcome.innerText()).includes('The request is for the wrong repository.')
      && await reason.count() === 0 && await approve.count() === 0);
  check('the screen sends exactly one rejection with the entered reason',
    decisions.length === 1 && decisions[0].method === 'POST' && decisions[0].body.reason === 'The request is for the wrong repository.', JSON.stringify(decisions));
}
