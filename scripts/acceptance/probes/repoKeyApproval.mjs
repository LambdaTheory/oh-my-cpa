import { settleLayout } from '../harness.mjs';

const CREATED_AT_MS = Date.parse('2026-11-06T08:00:00Z');
const FINGERPRINT = `x25519:sha256:${'a1'.repeat(32)}`;

export function repoKeyApprovalFixtures() {
  return [
    [(url, method) => method === 'GET' && /\/management\/repo-keys\/apply-link\/layout-(pending|approved)-\d+$/.test(url.pathname), (url) => {
      const token = url.pathname.split('/').at(-1);
      const isApproved = token.includes('-approved-');
      return {
        application: {
          id: token,
          repo: 'example/approval-layout',
          repo_has_remote: true,
          agent: 'layout-agent',
          device: 'layout-device',
          purpose: 'Develop and review the repository integration using the shared repository key.',
          kind: 'issue',
          status: isApproved ? 'approved' : 'pending',
          source: 'agent',
          public_key_fingerprint: FINGERPRINT,
          ciphertext_ready: isApproved,
          created_at_ms: CREATED_AT_MS,
          ...(isApproved ? { decided_by: 'layout-operator', decided_at_ms: CREATED_AT_MS + 60_000, decision_reason: 'Repository and caller verified.' } : {}),
        },
        repo: {
          repo: 'example/approval-layout', repo_has_remote: true, state: 'none', stops: [],
          usage: { window: '7d', from_ms: CREATED_AT_MS - 7 * 86_400_000, to_ms: CREATED_AT_MS, requests: 0, failed: 0, total_tokens: 0, cost_nanos: 0, last_used_ms: 0, models: [] },
        },
        public_key_changed: false,
        impact: 'issue',
        approval_expired: false,
      };
    }],
  ];
}

async function checkReachable(page, locator, check, label) {
  // DOM presence and Playwright visibility do not establish that a clipped flex child can be read.
  await locator.evaluate((node) => node.scrollIntoView({ block: 'center', inline: 'nearest' }));
  await settleLayout(page);
  const geometry = await locator.evaluate((node) => {
    const box = node.getBoundingClientRect();
    const pane = node.closest('main').getBoundingClientRect();
    const hit = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
    return {
      top: box.top, bottom: box.bottom, left: box.left, right: box.right, width: box.width, height: box.height,
      paneTop: pane.top, paneBottom: pane.bottom,
      viewportWidth: innerWidth, viewportHeight: innerHeight,
      isHit: Boolean(hit && node.contains(hit)),
    };
  });
  check(label,
    geometry.width > 0 && geometry.height > 0
      && geometry.top >= Math.max(0, geometry.paneTop) - 1
      && geometry.bottom <= Math.min(geometry.viewportHeight, geometry.paneBottom) + 1
      && geometry.left >= 0 && geometry.right <= geometry.viewportWidth
      && geometry.isHit,
    JSON.stringify(geometry));
}

export async function repoKeyApproval({ base, page, check }) {
  for (const viewport of [{ width: 1440, height: 900 }, { width: 375, height: 844 }]) {
    await page.setViewportSize(viewport);
    for (const status of ['pending', 'approved']) {
      const label = `${status} repository approval at ${viewport.width}px`;
      await page.goto(`${base}/repo-key-apply/layout-${status}-${viewport.width}`, { waitUntil: 'domcontentloaded' });
      const screen = page.getByTestId('repo-key-apply-page');
      const card = screen.getByRole('region', { name: 'Approve a repository key request', exact: true });
      const facts = card.locator('dl dd');
      await card.getByText('example/approval-layout', { exact: true }).waitFor();
      await settleLayout(page);

      const paneGeometry = await screen.locator('main').evaluate((pane) => {
        const box = pane.getBoundingClientRect();
        return { top: box.top, bottom: box.bottom, height: box.height, viewportHeight: innerHeight, overflow: pane.scrollWidth - pane.clientWidth };
      });
      check(`${label} gives the content a viewport-sized scroll pane`,
        paneGeometry.top >= 0 && paneGeometry.bottom <= paneGeometry.viewportHeight + 1
          && paneGeometry.height > paneGeometry.viewportHeight / 2 && paneGeometry.overflow <= 1,
        JSON.stringify(paneGeometry));
      check(`${label} renders all six applicant facts`, await facts.count() === 6);
      await checkReachable(page, card.getByRole('heading', { level: 1 }), check, `${label} keeps its heading reachable`);
      for (let index = 0; index < 6; index += 1) {
        await checkReachable(page, facts.nth(index), check, `${label} keeps applicant fact ${index + 1} readable`);
      }

      if (status === 'pending') {
        await checkReachable(page, card.getByRole('textbox', { name: 'Reason (required to reject, optional to approve)', exact: true }), check, `${label} keeps the reason field reachable`);
        for (const name of ['Reject', 'Approve this request']) {
          await checkReachable(page, card.getByRole('button', { name, exact: true }), check, `${label} lets ${name} win hit-testing`);
        }
      } else {
        const outcome = card.getByTestId('repo-key-apply-outcome');
        await outcome.waitFor();
        await checkReachable(page, outcome, check, `${label} keeps its approved outcome readable`);
        check(`${label} replaces the approval controls with the settled result`,
          (await outcome.innerText()).includes('Approved') && await card.getByRole('button', { name: 'Approve this request', exact: true }).count() === 0);
      }
      await checkReachable(page, card.getByRole('link', { name: 'Repository keys', exact: true }), check, `${label} keeps the console link reachable`);
    }
  }
}
