# Claude OAuth reauthorization runner — Phase 1 spike report

Date: 2026-10-20

Implementation commit: this report's HEAD commit.

Manual takeover timing fix: this report's HEAD commit.

System Chrome launch fix: this report's HEAD commit.

Logged-out Claude email step: this report's HEAD commit.

## Result

The offline proof succeeds: Playwright completes the synthetic email and verification-code pages, observes the outgoing loopback callback request even though no server is listening on `localhost:54545`, and parses `fixture-code` plus `fixture-state`. The live CPA/Anthropic flow is implemented but was not executed because this worktree received no test Claude account, one-time code, CPA endpoint, or management key. No OMC product code was changed.

## Manual takeover timing fix

The initial automated flow started reading stdin or `CODE_FILE` as soon as a verification-code input appeared. That timing was unsuitable for real accounts because email delivery can lag behind the page and the provider can require email/password entry, CAPTCHA, 2FA, device approval, or consent before redirecting.

`MANUAL=true` now disables verification-input discovery and all stdin/`CODE_FILE` reads. The runner opens CPA's authorization URL, tells the operator to complete login and verification in the browser, captures the callback, verifies its state, submits it to CPA or OMC, and polls CPA status. Real-account characterization should use `MANUAL=true HEADLESS=false`.

The automated path remains available. Its `CODE_FILE` read still starts only after a verification-code input is visible, but a missing or empty file is now polled until it contains a code or `TIMEOUT_MS` expires with a clear error; stdin behavior is unchanged.

## Logged-out Claude email step

Automated mode now detects visible email inputs using type, name, id, autocomplete, and placeholder attributes. When one appears, it requires `LOGIN_EMAIL`, fills the field, and clicks a visible Continue, Next, Sign in, 继续, or 下一步 button; an empty `LOGIN_EMAIL` fails immediately instead of leaving the runner waiting for a verification field that cannot appear.

After the email step, the runner continues racing the verification-code input against the OAuth callback. A visible password input stops the flow with `password/SSO required; manual takeover needed` and directs the operator to `MANUAL=true`; password, SSO, CAPTCHA, and 2FA automation remain out of scope. `MANUAL=true` still performs no email or code interaction, and `CODE_FILE` still waits until its file exists and contains a non-empty value.

The fixture now exercises the production `runLiveFlow` path against a fake CPA and a two-page Email + Continue, then Verification code + Verify flow. Tests assert that `LOGIN_EMAIL` reached the fixture server, an existing-session code page skips the email requirement, the callback was captured and submitted, missing `LOGIN_EMAIL` reports the configuration error, the password page requests manual takeover, and manual mode leaves the email input untouched.

## Google/Antigravity browser rejection fix

The runner can now select the system-installed Google Chrome with `BROWSER_CHANNEL=chrome`; an empty value preserves Playwright's bundled Chromium. Browser contexts also accept the opt-in `IGNORE_HTTPS_ERRORS` and `USER_AGENT` settings, while continuing to preserve the existing headless, viewport, and persistent-profile behavior.

Every launch omits Playwright's `--enable-automation` default argument and passes `--disable-blink-features=AutomationControlled`. The recommended Google/Antigravity diagnostic configuration is `BROWSER_CHANNEL=chrome`, `HEADLESS=false`, `MANUAL=true`, and `IGNORE_HTTPS_ERRORS=true`, with the TLS exception returned to its default `false` after certificate interception has been ruled out.

These changes reduce the most direct Playwright automation signals but do not defeat Google risk controls. Google can still reject a login because of account reputation, network origin, device history, challenge state, or signals outside this runner; the spike intentionally does not use stealth plugins or fingerprint-forging libraries.

## Commands and observed results

Run from `tools/reauth-runner/`:

| Command | Result |
| --- | --- |
| `pnpm --ignore-workspace install` | Passed; dependencies installed and `pnpm-lock.yaml` generated |
| `pnpm --ignore-workspace typecheck` | Passed |
| `pnpm --ignore-workspace test` | Passed: 20 tests, including LOGIN_EMAIL parsing and submission, existing-session bypass, missing-email failure, password/SSO manual-takeover failure, MANUAL isolation, and delayed/empty code-file coverage |
| `pnpm --ignore-workspace fixture` | Passed; printed `{"code":"fixture-code","state":"fixture-state"}` |
| `BROWSER_CHANNEL=chrome HEADLESS=false MANUAL=true pnpm --ignore-workspace start` against a local synthetic CPA | Passed; system Chrome opened the authorization page, requested it with `Chrome/155.0.0.0`, captured the synthetic loopback callback, and returned `{"provider":"claude","state":"chrome-smoke-state","status":"ok","message":"chrome smoke complete"}` |
| `MANUAL=true pnpm --ignore-workspace start` without CPA environment | Expected failure: `CPA_BASE_URL is required`; no secret value was printed |
| `docker build -t omc-reauth-runner:spike .` | Not run to completion: Docker CLI 29.5.3 is installed, but the Docker Desktop Linux engine pipe was unavailable |
| repository `pnpm test:fast` | Relevant frontend, documentation, workflow, and component checks passed; the aggregate failed because Go is absent and Windows denied symlink/GNU-tar release self-tests |
| repository `pnpm verify` | Frontend static gates passed; aggregate failed for the same missing-Go and Windows symlink/GNU-tar environment limitations; the worktree secret scan passed independently |

## Test environment

- Windows worktree under PowerShell.
- Node.js `v24.16.0` (project CI pins `22.23.2`; the runner declares Node.js 20+).
- pnpm `11.19.0`.
- Playwright `1.62.1`, using the locally installed Chromium revision for the fixture.
- Google Chrome `155.0.8059.80`, selected through Playwright's `chrome` channel for the headed start smoke test.
- Docker CLI `29.5.3`; Docker Desktop Linux engine unavailable, so the image could not be built locally.
- Go unavailable, so repository Go gates could not start.

## Callback capture

Capture is armed before navigation and attaches to the initial tab plus every popup/new tab. It listens to all three required Playwright surfaces:

- `page.on('request')`, which sees the callback even when the destination cannot connect;
- `page.on('framenavigated')`, which covers committed redirects and frame navigation;
- `context.on('page')`, which attaches both listeners to OAuth popups/new tabs.

The first HTTP(S) URL with non-empty `code` and `state` wins. `start` verifies that the captured state equals CPA's returned session state before submitting anything.

Both automated and manual fixtures use the production browser-authorization path. The automated fixture supplies `LOGIN_EMAIL` and a synthetic `CODE_FILE`; the manual fixture supplies sentinel values for both and asserts that the runner leaves the email field untouched before the test operator completes the forms.

## Selectors observed and used

Only the synthetic fixture was observed in this run:

- email: accessible label `Email`;
- continuation: role `button`, name `Continue`;
- verification code: accessible label `Verification code`;
- submission: role `button`, name `Verify`.

The live runner has not claimed vendor selectors as observed facts. Its email candidates are visible `type=email` inputs, name/id/placeholder values containing `email`, or `autocomplete=username`; its continuation buttons are accessible Continue, Next, Sign in, 继续, or 下一步 buttons. Verification candidates remain visible inputs with `autocomplete="one-time-code"`, a name/id/placeholder containing `code`, or `inputmode="numeric"`; submission uses an accessible button named `verify`, `continue`, `submit`, or `next`, otherwise Enter. These candidates must be checked against the real Anthropic page before a production design.

## CAPTCHA, 2FA, and device verification

No real Anthropic session was available, so CAPTCHA, 2FA, device approval, security-key prompts, consent, and headless bot detection were not observed. If a code input does not appear, the runner reports detected challenge text where possible and leaves the result as a failure rather than claiming completion. It contains no bypass mechanism.

## Real test prerequisites and manual work

- A disposable/non-production Claude account authorized for one OAuth attempt.
- A non-production CPA base URL and management key, injected only at runtime.
- Optional non-production OMC base URL and management key when the callback must pass through OMC's audited facade.
- Network access from the sidecar to CPA/OMC and Anthropic/Claude login hosts.
- The account email supplied as `LOGIN_EMAIL` for the automated logged-out path, or a human present for `MANUAL=true` to complete the full login flow.
- A human available to switch to manual takeover when password/SSO, CAPTCHA, 2FA, device, or consent steps appear.
- A human check that CPA status becomes `ok` and that the intended credential exists afterward.

## Unresolved questions and recommendations

- Verify the real Anthropic DOM and record only selectors actually observed in a non-production attempt.
- Determine whether headed Chromium materially reduces bot challenges in the target Dokploy environment.
- Decide whether OMC submission is mandatory for the eventual product path; this spike supports it, but start/status still originate directly at CPA as scoped.
- Before Phase 2, define profile retention, one-active-session ownership, code expiry/retry behavior, cancellation, screenshots/redaction policy, and provider-terms approval.
- Keep mailbox integration, automated code retrieval, scheduling, and background jobs out of this runner until those security and policy decisions exist.
