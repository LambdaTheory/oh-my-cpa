# OAuth reauthorization runner

## Provider coverage and OMC jobs

| Canonical ID | Alias | Flow / code kind | Behavior |
| --- | --- | --- | --- |
| claude | anthropic | redirect_otp / otp | Email and six-digit OTP; callback 54545 |
| codex | openai | redirect_otp / otp | Email and OTP; CPA owns PKCE; callback 1455 |
| antigravity | | redirect_browser / none | Human Google login; callback 51121 or CPA status |
| devin | | redirect_manual_callback / none | No forwarder; tunnel to CPA callback port or separate submit |
| kimi | | device_code / device | Verification page, user code, expiry and CPA status |
| kimi-ai | | device_code / device | Separate international Kimi accounts; same device flow |
| xai | grok | device_code / device | Same device flow |
| meta | | device_code / device | Same device flow |

Gemini, Vertex, api-keys.* and unknown providers fail before auth-url. Device/browser/manual-callback flows never read CODE_FILE, stdin or the OMC code endpoint. Device flows never submit callbacks.

With OMC_BASE_URL configured, start logs in once and uses the session cookie and same-origin header for paths beneath /api/v1:

1. GET /management/reauth/jobs/next?provider=canonical. A 204 returns idle without starting CPA. Use one worker at a time: next selects the pending job; oauth_started reserves it. This is a one-shot CLI, not a scheduler.
2. Request CPA auth-url; only Claude/Codex/Antigravity send is_webui=true.
3. POST /management/reauth/jobs/{id}/status with oauth_started and registry flow. Device reports also contain user_code, verification_url and expires_in (integer 1–86400 seconds).
4. Automated OTP polls GET /management/reauth/jobs/{id}/code; 204 means wait. MANUAL OTP leaves browser input to the operator, and a code-endpoint outage permits CODE_FILE/stdin input.
5. Submit redirect callbacks through the same OMC session and require positive CPA status before settling OMC. Read GET /management/reauth/jobs/{id}; if the state is code_received, consume the pending code through /code unless this run already consumed it, then report code_submitted to enter oauth_submitted. This also applies to MANUAL OTP and file fallback: the settlement code is discarded, never printed or entered in the browser. OMC enforces CodeConsumed; a lost TakeCode response can be reconciled without fabricating it.
6. Report succeeded. OMC permits this directly from waiting_code when no contact code has arrived, but rejects it from code_received. If AcceptCode races the state read and causes HTTP 409, read the new state and settle that transition once before reporting again. Unchanged conflicts are errors. The accepted report enters verifying; only OMC's later independent quota check can mark the job succeeded.

A transport/5xx outage before claiming warns and preserves standalone file/stdin/manual behavior. Code-endpoint outages warn and permit file/stdin fallback. Authentication, permission and conflict rejections never silently bypass OMC. Before confirmed CPA success, failures after reservation attempt a failed report with the allowlisted runner_failed reason; an unaccepted report warns that operator inspection is needed. After confirmed CPA success, an expired/unavailable OMC code or rejected status write exits nonzero with the failed operation, observed state and safe HTTP error, preserving the CPA outcome without reporting failed. Inspect that job before rerunning OAuth. Failed auth-url/reservation leaves the pending job unclaimed. OMC requires a direct local/private peer in its configured runner CIDRs and rejects forwarding identity headers.

Device flow requires CPA flow=device, a user code and expiry. Queries are removed from verification URLs before display, navigation and reporting; only HTTPS without credentials, fragments, nondefault ports or markup is accepted. Enter the separately displayed code on the page. TIMEOUT_MS bounds the entire run, and device expiry can shorten it.

~~~sh
PROVIDER=codex BROWSER_CHANNEL=chrome HEADLESS=false LOGIN_EMAIL=operator@example.invalid CODE_FILE=/protected/code pnpm --ignore-workspace start
PROVIDER=antigravity BROWSER_CHANNEL=chrome HEADLESS=false pnpm --ignore-workspace start
PROVIDER=kimi BROWSER_CHANNEL=chrome HEADLESS=false pnpm --ignore-workspace start
PROVIDER=devin BROWSER_CHANNEL=chrome HEADLESS=false pnpm --ignore-workspace start
PROVIDER=devin pnpm --ignore-workspace submit < /protected/callback-url
~~~

Devin needs CPA callback reachability/tunneling or a full callback pasted into submit in a second terminal using the same CPA/OMC/provider configuration. Browser/manual-callback start also polls CPA, so separate submission can complete it. A callback HTTP 409 is accepted only after CPA independently confirms completion.

Success output contains provider, state and ok/success; raw upstream messages are omitted because they may echo credentials. Failure, cancellation, expiry, unknown status and timeout exit nonzero. Ctrl+C/SIGTERM or browser closure cancels locally and attempts an OMC failure report unless CPA success was already confirmed; this does not revoke grants or delete CPA pending sessions.

Real acceptance remains outstanding for OTP (Claude or Codex), browser (Antigravity), device (Kimi/xAI/Meta) and manual callback (Devin with reachability). Use disposable accounts, check real page selectors and challenges, confirm CPA completion plus the intended credential, and separately confirm OMC quota verification. No real provider, Feishu or production credentials were used in this adaptation.

This directory is an independent Node.js 20+ / TypeScript / Playwright runner. It proves that a browser can capture CPA's loopback OAuth redirect from navigation events even when `localhost:54545` is unreachable from the container. It does not change or run inside the OMC product.

The runner never accesses a mailbox. In `MANUAL` mode, the operator completes login and enters the one-time code directly in the browser. In automated mode, the runner can fill a configured account email, then read the verification code from stdin or `CODE_FILE`. Management keys and the login email are read only from environment variables and are never intentionally printed or persisted.

## Install and verify

Run commands from this directory so the package stays outside the repository's root workspace:

```sh
pnpm --ignore-workspace install --frozen-lockfile
pnpm --ignore-workspace typecheck
pnpm --ignore-workspace test
pnpm --ignore-workspace fixture
```

Expected fixture output:

```json
{"code":"fixture-code","state":"fixture-state"}
```

The fixture starts a local email/code form, submits to `http://localhost:54545/callback`, and captures the outgoing request without waiting for that destination to load.

## Environment

| Variable | Required | Purpose |
| --- | --- | --- |
| `CPA_BASE_URL` | live commands | CPA origin, for example `http://cpa:8317`; do not append `/v8/management` |
| `CPA_MANAGEMENT_KEY` | live commands | CPA management bearer token |
| `PROVIDER` | no | OAuth provider; defaults to `claude` |
| `HEADLESS` | no | `true` by default; use `false` for a visible browser or Xvfb |
| `MANUAL` | no | `false` by default; when `true`, the runner leaves login, verification code, CAPTCHA, 2FA, and device checks to the operator and only waits for the callback |
| `BROWSER_CHANNEL` | no | Playwright browser channel; empty uses the bundled Chromium, while `chrome` uses the system-installed Google Chrome |
| `IGNORE_HTTPS_ERRORS` | no | `false` by default; set `true` only when diagnosing a trusted local certificate-interception environment |
| `USER_AGENT` | no | Optional browser User-Agent override; empty leaves the browser default unchanged |
| `PROFILE_DIR` | no | persistent Chromium profile; defaults to an OS temporary directory |
| `LOGIN_EMAIL` | when automated login shows an email field | account email entered into a visible email/username field before the runner clicks Continue, Next, Sign in, 继续, or 下一步; ignored when `MANUAL=true` |
| `CODE_FILE` | no | standalone/fallback automated OTP only; file must contain exactly six digits; otherwise read stdin |
| `TIMEOUT_MS` | no | total start deadline; defaults to `180000`; device expiry may shorten it |
| `OMC_BASE_URL` | no | OMC base including sub-path, such as `http://omc:8080/omc`; enables job claim and callback submission |
| `OMC_MANAGEMENT_KEY` | with `OMC_BASE_URL` | establishes one session for job reads, status writes and callback submission |

Keep `PROFILE_DIR` on a protected volume: the browser profile can contain authenticated session state. Do not commit it, include it in support bundles, or share it between unrelated operators.

## Commands

For a real-account test, use manual takeover so the operator can complete every provider-controlled step in the visible browser:

```sh
export CPA_BASE_URL=http://cpa:8317
export CPA_MANAGEMENT_KEY='runtime-only-value'
MANUAL=true HEADLESS=false pnpm start
```

The runner prints `MANUAL 模式：请在浏览器完成登录和验证码`, waits for a callback URL, validates its state, submits it to CPA or OMC, and polls CPA status. It does not look for a verification-code input and does not read stdin or `CODE_FILE` in this mode.

### Google login reports an unsafe or automated browser

When Google blocks the bundled Playwright Chromium during Antigravity OAuth, prefer the system-installed Google Chrome and complete the flow manually:

```sh
BROWSER_CHANNEL=chrome
HEADLESS=false
MANUAL=true
IGNORE_HTTPS_ERRORS=true
pnpm --ignore-workspace start
```

`IGNORE_HTTPS_ERRORS=true` is intended only to rule out TLS interception by a trusted local proxy; leave it at its default `false` when certificates validate normally. `USER_AGENT` can override the browser User-Agent for diagnostics, but the recommended Chrome configuration normally relies on Chrome's own value. The runner removes Playwright's `--enable-automation` default argument and disables Blink's `AutomationControlled` feature, but Google can still reject the session based on account, network, device, or other risk signals; these settings are not a bypass guarantee.

For a Claude session with no existing login state, set `LOGIN_EMAIL`. The automated flow fills a visible email/username input, clicks the visible continuation button, waits for the verification-code input, and then reads the code from stdin:

```sh
export CPA_BASE_URL=http://cpa:8317
export CPA_MANAGEMENT_KEY='runtime-only-value'
export LOGIN_EMAIL='operator@example.com'
printf '%s\n' '123456' | pnpm start
```

With a code file:

```sh
LOGIN_EMAIL='operator@example.com' CODE_FILE=/run/secrets/claude-code pnpm start
```

The file may be created after the provider displays the code input. The runner waits for exactly six digits, bounded by the total deadline. Use a fresh file per attempt and delete it afterward; the runner does not remove supplied files.

If the authorization page already has a login session and does not show an email field, `LOGIN_EMAIL` is not required and the runner continues waiting for the code field or callback. If an email field is visible but `LOGIN_EMAIL` is empty, the runner fails immediately with a configuration error instead of waiting. If the next page requires a password or SSO, the runner stops with `password/SSO required; manual takeover needed`; rerun with `MANUAL=true HEADLESS=false` and complete the provider-controlled flow in the browser.

For redirect OTP, start requests the canonical provider auth-url and opens it in a persistent browser profile. It watches all pages for callback requests/navigations, validates state, submits to CPA or OMC, and polls status. Manual mode leaves interaction to the operator; automated mode optionally fills email then a six-digit code. Structured output contains provider, state and terminal status without upstream messages or callback authorization codes.

Submit a callback separately. stdin is preferred because callback URLs contain a short-lived authorization code and shell arguments may be retained in process listings or history:

```sh
printf '%s\n' 'http://localhost:54545/callback?code=...&state=...' | pnpm submit
```

Poll status until a terminal response:

```sh
pnpm status -- 'oauth-state'
```

Add `--once` after the state for one status read.

## Docker and Dokploy sidecar

Build the image from this directory:

```sh
docker build -t omc-reauth-runner:spike .
docker run --rm --init --ipc=host -i \
  -e CPA_BASE_URL=http://cpa:8317 \
  -e CPA_MANAGEMENT_KEY \
  -v reauth-profile:/data/profile \
  omc-reauth-runner:spike start
```

The image uses Playwright's pinned Chromium image and runs as its unprivileged `pwuser`. Headless is the default. For a headed diagnostic session without a desktop, override the entrypoint with `xvfb-run -a pnpm` and set `HEADLESS=false`; a VNC/noVNC layer is still needed to watch or interact with the window remotely.

Example Dokploy Compose fragment:

```yaml
services:
  reauth-runner:
    build: ./tools/reauth-runner
    init: true
    ipc: host
    user: pwuser
    environment:
      CPA_BASE_URL: http://cpa:8317
      CPA_MANAGEMENT_KEY: ${CPA_MANAGEMENT_KEY}
      PROVIDER: claude
      HEADLESS: "true"
      LOGIN_EMAIL: ${CLAUDE_LOGIN_EMAIL}
      PROFILE_DIR: /data/profile
      TIMEOUT_MS: "180000"
    volumes:
      - reauth-profile:/data/profile
    networks:
      - app
    stdin_open: true
    tty: true
    restart: "no"

volumes:
  reauth-profile:

networks:
  app:
    external: true
```

Attach the sidecar to the same Docker network as CPA/OMC, but do not use `network_mode: service:...` or share a network namespace. The OAuth provider still redirects its browser tab to `localhost:54545`; the runner succeeds by observing the request/navigation URL before the unreachable page finishes, not by routing that localhost address to CPA.

## Manual real-account test

Use only a disposable/non-production Claude account and non-production CPA deployment.

1. Confirm the runner can resolve and reach CPA and `claude.ai`/Anthropic login endpoints.
2. Inject CPA/OMC keys at runtime; do not place them in Compose source, `.env` committed to Git, screenshots, or transcripts.
3. Use `MANUAL=true HEADLESS=false`; real tests should prefer manual takeover so provider-controlled login and challenge timing remains under operator control.
4. Complete email/password entry, verification-code entry, consent, CAPTCHA, 2FA, or device approval directly in the browser as required. Do not attempt to bypass those controls.
5. Observe and record whether Anthropic presents CAPTCHA, 2FA, device approval, security-key prompts, consent, or changed selectors. Do not attempt to bypass those controls.
6. Confirm the final structured status is `ok` and the expected credential appears in the non-production CPA/OMC UI.
7. Delete the code file and protected profile volume when retention is not required.

## Risks and limits

- Anthropic controls the page markup and may change it without notice. The spike uses accessible/attribute selectors and reports when no code field appears; it does not claim a stable vendor contract.
- CAPTCHA, 2FA, device verification, consent screens, split OTP fields, and bot detection can block headless automation. A human must resolve allowed challenges; the runner does not bypass them.
- Password and SSO pages are intentionally not automated. The runner reports them immediately and directs the operator to `MANUAL=true`; it does not attempt to bypass CAPTCHA, 2FA, or other provider controls.
- Google may still classify a headed system-Chrome session as unsafe or automated based on signals outside the runner's browser launch configuration. Do not add stealth plugins or fingerprint-forging libraries; use manual takeover and follow the provider's supported recovery path.
- Automating a consumer login can be restricted by provider terms or policy. Complete legal/security review before productizing this spike.
- In automated OTP mode, LOGIN_EMAIL is used only for visible email fields, and code retrieval begins after the code input appears. Mailbox access, password/SSO automation, scheduling and concurrent workers remain outside this runner.
- A persistent profile improves session continuity but is sensitive local state. Restrict filesystem access and avoid concurrent use.
