# Claude OAuth reauthorization runner — Phase 1 spike report

Date: 2026-10-20

Implementation commit: `ad74a143abd0fe3a2b7c7141863961bd4946f7f3`

Manual takeover timing fix: this report's HEAD commit.

## Result

The offline proof succeeds: Playwright completes the synthetic email and verification-code pages, observes the outgoing loopback callback request even though no server is listening on `localhost:54545`, and parses `fixture-code` plus `fixture-state`. The live CPA/Anthropic flow is implemented but was not executed because this worktree received no test Claude account, one-time code, CPA endpoint, or management key. No OMC product code was changed.

## Manual takeover timing fix

The initial automated flow started reading stdin or `CODE_FILE` as soon as a verification-code input appeared. That timing was unsuitable for real accounts because email delivery can lag behind the page and the provider can require email/password entry, CAPTCHA, 2FA, device approval, or consent before redirecting.

`MANUAL=true` now disables verification-input discovery and all stdin/`CODE_FILE` reads. The runner opens CPA's authorization URL, tells the operator to complete login and verification in the browser, captures the callback, verifies its state, submits it to CPA or OMC, and polls CPA status. Real-account characterization should use `MANUAL=true HEADLESS=false`.

The automated path remains available. Its `CODE_FILE` read still starts only after a verification-code input is visible, but a missing or empty file is now polled until it contains a code or `TIMEOUT_MS` expires with a clear error; stdin behavior is unchanged.

## Commands and observed results

Run from `tools/reauth-runner/`:

| Command | Result |
| --- | --- |
| `pnpm --ignore-workspace install` | Passed; dependencies installed and `pnpm-lock.yaml` generated |
| `pnpm --ignore-workspace typecheck` | Passed |
| `pnpm --ignore-workspace test` | Passed: 16 tests, retaining the original 12 and adding MANUAL/CODE_FILE configuration, manual browser takeover, and delayed/empty code-file coverage |
| `pnpm --ignore-workspace fixture` | Passed; printed `{"code":"fixture-code","state":"fixture-state"}` |
| `MANUAL=true pnpm --ignore-workspace start` without CPA environment | Expected failure: `CPA_BASE_URL is required`; no secret value was printed |
| `docker build -t omc-reauth-runner:spike .` | Not run to completion: Docker CLI 29.5.3 is installed, but the Docker Desktop Linux engine pipe was unavailable |
| repository `pnpm test:fast` | Relevant frontend, documentation, workflow, and component checks passed; the aggregate failed because Go is absent and Windows denied symlink/GNU-tar release self-tests |
| repository `pnpm verify` | Frontend static gates passed; aggregate failed for the same missing-Go and Windows symlink/GNU-tar environment limitations; the worktree secret scan passed independently |

## Test environment

- Windows worktree under PowerShell.
- Node.js `v24.16.0` (project CI pins `22.23.2`; the runner declares Node.js 20+).
- pnpm `11.19.0`.
- Playwright `1.62.1`, using the locally installed Chromium revision for the fixture.
- Docker CLI `29.5.3`; Docker Desktop Linux engine unavailable, so the image could not be built locally.
- Go unavailable, so repository Go gates could not start.

## Callback capture

Capture is armed before navigation and attaches to the initial tab plus every popup/new tab. It listens to all three required Playwright surfaces:

- `page.on('request')`, which sees the callback even when the destination cannot connect;
- `page.on('framenavigated')`, which covers committed redirects and frame navigation;
- `context.on('page')`, which attaches both listeners to OAuth popups/new tabs.

The first HTTP(S) URL with non-empty `code` and `state` wins. `start` verifies that the captured state equals CPA's returned session state before submitting anything.

The manual fixture uses the production browser-authorization path. The test itself fills and submits the fake login and verification forms while `MANUAL=true`; an intentionally absent `CODE_FILE` proves the flow does not wait for or read it before capturing the callback.

## Selectors observed and used

Only the synthetic fixture was observed in this run:

- email: accessible label `Email`;
- continuation: role `button`, name `Continue`;
- verification code: accessible label `Verification code`;
- submission: role `button`, name `Verify`.

The live runner has not claimed vendor selectors as observed facts. Its fallback candidates are visible inputs with `autocomplete="one-time-code"`, a name/id/placeholder containing `code`, or `inputmode="numeric"`; submission uses an accessible button named `verify`, `continue`, `submit`, or `next`, otherwise Enter. These candidates must be checked against the real Anthropic page before a production design.

## CAPTCHA, 2FA, and device verification

No real Anthropic session was available, so CAPTCHA, 2FA, device approval, security-key prompts, consent, and headless bot detection were not observed. If a code input does not appear, the runner reports detected challenge text where possible and leaves the result as a failure rather than claiming completion. It contains no bypass mechanism.

## Real test prerequisites and manual work

- A disposable/non-production Claude account authorized for one OAuth attempt.
- A non-production CPA base URL and management key, injected only at runtime.
- Optional non-production OMC base URL and management key when the callback must pass through OMC's audited facade.
- Network access from the sidecar to CPA/OMC and Anthropic/Claude login hosts.
- A human present to complete login, enter the one-time code through the approved channel, and handle permitted CAPTCHA/2FA/device/consent steps directly in the visible browser.
- A human check that CPA status becomes `ok` and that the intended credential exists afterward.

## Unresolved questions and recommendations

- Verify the real Anthropic DOM and record only selectors actually observed in a non-production attempt.
- Determine whether headed Chromium materially reduces bot challenges in the target Dokploy environment.
- Decide whether OMC submission is mandatory for the eventual product path; this spike supports it, but start/status still originate directly at CPA as scoped.
- Before Phase 2, define profile retention, one-active-session ownership, code expiry/retry behavior, cancellation, screenshots/redaction policy, and provider-terms approval.
- Keep mailbox integration, automated code retrieval, scheduling, and background jobs out of this runner until those security and policy decisions exist.
