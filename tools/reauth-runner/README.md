# Claude OAuth reauthorization runner (Phase 1 spike)

This directory is an independent Node.js 20+ / TypeScript / Playwright runner. It proves that a browser can capture CPA's loopback OAuth redirect from navigation events even when `localhost:54545` is unreachable from the container. It does not change or run inside the OMC product.

The runner never reads email. In the recommended `MANUAL` mode, the operator completes login and enters the one-time code directly in the browser. The optional automated mode can instead read the code from stdin or `CODE_FILE`. Management keys are read only from environment variables and are never intentionally printed or persisted.

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
| `CODE_FILE` | no | automated mode only; path containing the one-time code, polled until it exists and is non-empty or `TIMEOUT_MS` expires; without it, the runner reads stdin |
| `TIMEOUT_MS` | no | request, browser, callback, and status deadline; defaults to `180000` |
| `OMC_BASE_URL` | no | OMC base including any sub-path, such as `http://omc:8080/omc`; selects OMC callback submission |
| `OMC_MANAGEMENT_KEY` | with `OMC_BASE_URL` | used once to establish an OMC session before callback submission |

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

The optional automated flow waits until a verification-code input appears and then reads the code from stdin:

```sh
export CPA_BASE_URL=http://cpa:8317
export CPA_MANAGEMENT_KEY='runtime-only-value'
printf '%s\n' '123456' | pnpm start
```

With a code file:

```sh
CODE_FILE=/run/secrets/claude-code pnpm start
```

The file may be created or populated after the provider displays the code input. The runner polls until it contains a non-empty value, bounded by `TIMEOUT_MS`; stdin behavior is unchanged.

`start` requests `GET /v8/management/oauth/auth-url?provider=claude&is_webui=true`, opens the returned URL with a persistent Chromium profile, and watches every page for callback requests/navigations. Manual mode leaves all page interaction to the operator; automated mode fills a visible verification-code input after it appears. The runner then submits the captured URL to CPA (or OMC when configured) and polls CPA status. Its structured output contains the provider, state, terminal status, and non-secret status message; it does not print the callback authorization code.

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
- Google may still classify a headed system-Chrome session as unsafe or automated based on signals outside the runner's browser launch configuration. Do not add stealth plugins or fingerprint-forging libraries; use manual takeover and follow the provider's supported recovery path.
- Automating a consumer login can be restricted by provider terms or policy. Complete legal/security review before productizing this spike.
- In automated mode, `CODE_FILE` polling begins only after the code field appears and ends when the file is non-empty or `TIMEOUT_MS` expires. External code delivery, mailbox access, retries, scheduling, concurrency control, and Phase 2 OMC jobs are intentionally out of scope.
- A persistent profile improves session continuity but is sensitive local state. Restrict filesystem access and avoid concurrent use.
