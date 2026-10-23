# Configuration & Operations

What an operator needs after `docs/install.md`: the settings Oh My CPA reads, the
constraints a deployment must respect, and how the parts that outlive a page visit
behave. Database backup, restore and maintenance from the host have their own runbook,
`docs/ops/sqlite-operations.md`.

## Prebuilt Dokploy updates

The optional prebuilt service uses branch `deploy/prebuilt` and Compose Path
`./deploy/dokploy/compose.prebuilt.yml`. It inherits the source service's runtime
environment, external network, non-root/read-only configuration and `omc-data`
volume. Keep the existing Dokploy service/project identity so an update retains
that volume. The source entrypoint `./deploy/dokploy/compose.yml` remains available.

Every source update requires a fresh Mac preparation from the full source SHA:
`node scripts/prebuilt-web.mjs prepare <source-sha> <new-directory> [fork-tag]`,
then `node scripts/prebuilt-web.mjs verify <directory>` and image acceptance.
Publication is separately invoked with
`node scripts/prebuilt-web.mjs publish <directory>`. Never edit generated digests
or repair a stale bundle in place. A race, divergent source or unknown deployment
commit requires investigation and a new preparation, not a forced push.

System Information reports the manifest's source commit, not the generated deploy
commit. Optional fork tags are fixed during preparation; `OMCPA_BUILD_VERSION`
applies only to the source build path. Keep `OMCPA_VERSION` unset to retain the
`fork` comparison version. Dokploy builds only Go/runtime layers and needs no
frontend compiler; build dependencies still need network access or cached inputs.
See `docs/install.md` for canonical origin requirements and full commands, and
ADR 0115 for the deployment-only generated-assets exception.

## Native deployments

Native releases support Darwin, Windows, Linux and FreeBSD on amd64/arm64. The
executable embeds the SPA and IANA time-zone database, but HTTPS still uses OS
certificate roots. The packaged environment template binds loopback and stores
SQLite in `./data`; run under a dedicated user/service account, preserve its working
directory and restrict environment/data access with Unix permissions or Windows ACLs.
Container uid/gid `10001:10001` is not a requirement for native installs. Leave
`OMCPA_VERSION` unset to retain the injected release tag. Stop OMC before replacing
the executable, keep the master key and data directory, and back up both before an
upgrade. Only a matching data backup can undo forward migrations. The download,
checksum and host configuration steps are in `docs/install.md`.

## Fork build identity

System Information reads immutable `build_identity` metadata from the binary,
separate from upstream release observations and runtime `OMCPA_VERSION`.
The Dokploy Compose service builds from the root Dockerfile and automatically
uses checkout HEAD/ref files for the source revision and ref. Unknown metadata is
shown as unknown; a known revision without a supplied tag is `dev+<12-character SHA>`.
`OMCPA_BUILD_VERSION` is an optional Compose build input naming an actual fork tag,
not a runtime environment override. The upstream comparison version remains
`fork`, including tagged fork builds. Release checks only observe upstream releases;
they do not approve or apply a fork upgrade. See `docs/releasing.md`.

## Settings

Everything is an environment variable. The binary also reads `.env` from its working
directory (`OMCPA_ENV_FILE` names another file); real environment variables win.
`internal/config/config.go` is authoritative, and a malformed value stops start-up with
a message naming the variable.

### Required

| Variable | Purpose |
| --- | --- |
| `OMCPA_CPA_BASE_URL` | CPA's address, for example `http://127.0.0.1:8317` |
| `OMCPA_CPA_MANAGEMENT_KEY` | CPA's plaintext management key. Also the console's sign-in password |
| `OMCPA_MASTER_KEY` | At-rest encryption key: a 64-character hex key (`openssl rand -hex 32`) or at least 32 bytes |

### Serving

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_VERSION` | embedded stable tag; `v0.1.0-dev` for untagged source builds | Optional display/User-Agent version override; does not change the installed build |
| `OMCPA_LISTEN_ADDR` | `:8080` | Listen address |
| `OMCPA_BASE_PATH` | `/omc` | Sub-path the console is served under. `omc`, `/omc/` and `/omc` are equivalent; `/` serves it at the root |
| `OMCPA_DATA_DIR` | `./data` | Directory holding `oh-my-cpa.db`. Local disk only |
| `OMCPA_PUBLIC_URL` | unset | The address browsers use. An `https://` value marks the session cookie `Secure` |
| `OMCPA_TRUSTED_PROXY_CIDRS` | unset | Comma-separated CIDRs of reverse proxies whose forwarding headers may be trusted for login throttling |
| `TZ` | system zone | Server calendar; see [Time zone](#time-zone) |

### Gateway connection

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_CPA_USAGE_ADDR` | host and port of `OMCPA_CPA_BASE_URL` | `host:port` of CPA's usage stream |
| `OMCPA_REQUEST_TIMEOUT` | `15s` | Timeout for ordinary CPA management calls |
| `OMCPA_CPA_TLS_SKIP_VERIFY` | `false` | Skip certificate verification when CPA uses a private certificate |

### Usage collection

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_USAGE_INGEST_MODE` | `auto` | `auto`, `subscribe`, `resp_pull`, `http_pull` or `off` |
| `OMCPA_USAGE_IDLE_INTERVAL` | `1s` | First wait after an empty poll |
| `OMCPA_USAGE_MAX_IDLE_INTERVAL` | `10s` | Ceiling the idle backoff grows to. Keep it well below CPA's `redis-usage-queue-retention-seconds` |
| `OMCPA_USAGE_BATCH_SIZE` | `1000` | Records read per poll (1–10000) |
| `OMCPA_USAGE_RETENTION_DAYS` | `90` | Days of request records (the request list and its detail) kept, `0` to keep them all. Ninety matches the request list's longest preset. Dashboard statistics do not depend on it: they are kept permanently as usage facts |
| `OMCPA_USAGE_INBOX_RETENTION_DAYS` | `7` | Days a captured payload is kept after it was decoded into a request record, `0` to keep it as long as the record. Payloads are the largest part of the database and nothing reads them once decoded except a re-decode after a decoder fix |

### Quota refresh scheduler

When enabled, exactly one quota refresh mode runs outside demo mode:

| Mode | Policy source | Behavior |
| --- | --- | --- |
| `interval` (default) | The `omc_quota_auto_refresh` preference, including the settings page and `quota_auto_refresh_get/set` | Reads due credentials every thirty minutes by default and after observed resets; each round is capped at sixteen reads with concurrency four, and Meta is excluded |
| `adaptive` | The `OMCPA_QUOTA_*` environment variables below | Uses watched, active, normal and idle tiers, reset/window-end reads, passive-observation widening and per-provider credential floors; it does not read `omc_quota_auto_refresh` |

The adaptive scheduler reads each refreshable credential's quota from its provider on a
per-account cadence. A credential whose standard window is already current through the passive
observation stream is read no faster than `OMCPA_QUOTA_PASSIVE_FRESH_INTERVAL`. The credential
floor is the hard minimum between two active reads of one credential; it is resolved per
provider, so the watched tier's five minutes is reachable for Codex and Antigravity while
Claude stays at ten minutes.

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_QUOTA_SCHEDULER_MODE` | `interval` | Select `interval` or `adaptive`; any other value refuses startup |
| `OMCPA_QUOTA_SCHEDULER_ENABLED` | `true` | Run the background quota refresh scheduler. Off in demo mode |
| `OMCPA_QUOTA_WATCH_INTERVAL` | `5m` | Interval for a credential with recent traffic and a window at or above `OMCPA_QUOTA_WATCH_USED_PERCENT` |
| `OMCPA_QUOTA_ACTIVE_INTERVAL` | `15m` | Interval for a credential with recent traffic below the watch threshold |
| `OMCPA_QUOTA_NORMAL_INTERVAL` | `30m` | Interval for a credential seen within `OMCPA_QUOTA_NORMAL_WINDOW` |
| `OMCPA_QUOTA_IDLE_INTERVAL` | `2h` | Interval for a quiet credential |
| `OMCPA_QUOTA_WATCH_USED_PERCENT` | `80` | Window share (0–100) that promotes an active credential to watched |
| `OMCPA_QUOTA_WATCH_WINDOW` | `15m` | How recently a request counts as recent traffic for the watch and active tiers |
| `OMCPA_QUOTA_NORMAL_WINDOW` | `2h` | How recently a request keeps a credential out of the idle tier |
| `OMCPA_QUOTA_WINDOW_END_MARGIN` | `15m` | How close to a window's reset a read is forced so the next cycle has a previous-cycle reading |
| `OMCPA_QUOTA_PASSIVE_FRESH_INTERVAL` | `60m` | Floor an interval is widened to when a fresh passive observation already covers the standard window |
| `OMCPA_QUOTA_CREDENTIAL_FLOOR` | `10m` | Fallback shortest time between two active reads of the same credential, for a provider `OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER` does not name |
| `OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER` | `claude=10m,codex=5m,antigravity=5m` | Per-provider credential floors as `provider=duration` entries, overriding the named defaults. A provider not named keeps its default; an unnamed provider falls back to `OMCPA_QUOTA_CREDENTIAL_FLOOR`. A malformed entry refuses startup |
| `OMCPA_QUOTA_REFRESH_CONCURRENCY` | `4` | Maximum simultaneous active reads across credentials; the same credential has at most one active read, while different credentials of one provider may run concurrently |
| `OMCPA_QUOTA_BACKOFF_BASE` | `5m` | First wait after an upstream 429 that carried no `Retry-After`; it doubles up to the maximum |
| `OMCPA_QUOTA_BACKOFF_MAX` | `6h` | Ceiling on the 429 backoff. Must be at least the base |

An upstream 429 parks the credential and its provider for the `Retry-After` (or the
exponential wait). The manual endpoint, Agent/MCP `quota_refresh`, the selected background
loop and credential warmup's consuming requests share the park. Quota refreshes refuse with
`rate_limited_backoff` and a retry time; warmup waits without probing upstream again. A
successful quota read clears its own credential park, while a provider park is released only by
the read that created the current refusal. The state is in memory only, so a restart clears it.

The console's multi-credential refresh reports the park per credential instead of failing the
whole request. Successful siblings remain in the response, rate-limited entries carry their retry
time, and an already parked credential does not reach upstream again. The manual batch and the
background scheduler share one cancellable lock per credential: different credentials of the same
provider retain four-way concurrency, then the batch sends remaining targets one at a time after
the first rate limit settles.

### Account auto-management

Account auto-management stops using a credential on explicit account-level weekly (7d)
exhaustion evidence and starts using it again at the recorded reset. It is off by
default, and a deployment that never adopts a credential is unaffected. The three
variables below control the process, not the roster: adopting accounts, the per-account
switch, the one-time recovery time and the web switch all live in the console under
`/api/v1/account-automation` and in `account_automation_*` (migration 043).

All controllable CPA credentials can be adopted. A provider whose account-level weekly
capability is not verified receives a separate **monitor-only** badge and can be filtered
by that capability; `monitoring` remains the runtime health state for verified and
unverified providers alike. A monitor-only credential is never automatically disabled or
re-enabled. Credentials that CPA
does not expose as controllable cannot be adopted and retain an explicit reason in the
candidate list. The console reports the two inventory scopes separately as adopted
accounts and total live credentials (ADR 0108).

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_ACCOUNT_AUTOMATION_ENABLED` | unset (off) | Master switch. Unset leaves the console's web switch in force; an explicit `false` is an emergency off the console cannot override; an explicit `true` forces automation on. The console shows which source is in effect |
| `OMCPA_ACCOUNT_AUTOMATION_INTERVAL` | `1m` | How often the pass evaluates the adopted roster |
| `OMCPA_ACCOUNT_AUTOMATION_BACKOFF_BASE` | `5m` | First wait after a failed enable; it doubles up to the maximum |
| `OMCPA_ACCOUNT_AUTOMATION_BACKOFF_MAX` | `6h` | Ceiling on the enable retry backoff. Must be at least the base |
| `OMCPA_ACCOUNT_AUTOMATION_MAX_EVIDENCE_AGE` | `24h` | How old the weekly reading that justifies a disable may be |

A failed enable persists its retry instant and attempt count, so a restart resumes the
wait rather than retrying immediately. The console and read-only status project a
recovery instant only while the credential remains disabled with an active recovery
plan: a future instant is reported as `scheduled`, while a passed instant is `overdue`
and shown as waiting for retry. A confirmed automatic enable or an external re-enable
clears the consumed cycle instant; historical timestamps on completed rows are neither
displayed nor acted on. The loop performs a CPA write, so it never runs in demo mode.
See ADR 0105.

### Ollama monitoring

The Ollama collector stores several accounts and scrapes `https://ollama.com/settings`
with each account's session cookie to read its quota. The cookie is encrypted at rest
and never returned. Ollama is not in CPA's control chain: the switch below stops the
scraper, never inference. Accounts live in the console under `/api/v1/ollama/accounts`
and the newest reading under `/api/v1/ollama/quota`; the collector never runs in demo
mode. Adding an enabled account, or changing its cookie, or bringing a paused account
back on, triggers a scrape at once instead of waiting for the cadence, and the console
can force one account with `POST /api/v1/ollama/accounts/{id}/refresh`. Every one of
those paths shares the same per-account lock, global concurrency cap and persisted
backoff, so none can bypass another's rate limit.

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_OLLAMA_COLLECTOR_ENABLED` | `true` | Run the background Ollama scraper. Off in demo mode |
| `OMCPA_OLLAMA_COLLECTOR_INTERVAL` | `15m` | Cadence between collection passes |
| `OMCPA_OLLAMA_TIMEOUT` | `20s` | Per-scrape HTTP timeout |
| `OMCPA_OLLAMA_BACKOFF_BASE` | `5m` | First wait after a transport failure or 429; it doubles up to the maximum |
| `OMCPA_OLLAMA_BACKOFF_MAX` | `6h` | Ceiling on the collection backoff. Must be at least the base |

### Feishu notifications

The Feishu bot reuses one long connection to answer read-only queries and to send
denoised notifications. The app secret is encrypted at rest and never returned by
the API, a log or a database read. The bot is answered from OMC's stored data and
never triggers a refresh. Settings and status live under `/api/v1/feishu/settings`,
`/api/v1/feishu/status`, `/api/v1/feishu/test` and
`POST /api/v1/feishu/prediction-test`; the bot never runs in demo mode.

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_FEISHU_ENABLED` | unset (uses the web switch) | Tri-state: unset leaves the console's switch in force, `true` forces the bot on and `false` is an environment off the web cannot override |
| `OMCPA_FEISHU_FORCE_DISABLED` | `false` | Emergency off. Takes precedence over `OMCPA_FEISHU_ENABLED` and the web switch |
| `OMCPA_FEISHU_APP_ID` | unset | Overrides the self-built app's App ID |
| `OMCPA_FEISHU_APP_SECRET` | unset | Overrides the app secret. Held in memory only; never written to a response or a log |
| `OMCPA_FEISHU_CHAT_ID` | unset | Overrides the target group `chat_id` |
| `OMCPA_FEISHU_OAUTH_CHAT_ID` | unset | Dedicated OAuth management group; enables credential recovery only together with the contact and a usable enabled bot |
| `OMCPA_FEISHU_OAUTH_CONTACT_OPEN_ID` | unset | Fixed code contact, in `ou_...` form |
| `OMCPA_REAUTH_RUNNER_CIDRS` | loopback, RFC 1918 IPv4 and IPv6 ULA | Comma-separated CIDR allowlist for direct runner connections; narrow this to the actual Docker network and required loopback ranges |

An empty value is treated as unset, so a compose file that passes a variable through
without a value cannot pin the feature off. The console shows the effective source
(`env_disabled`, `env_enabled`, `web` or `default_off`).

### OAuth credential recovery

Recovery reuses the effective Feishu app ID, secret and enable switch. Missing either
OAuth setting disables recovery without changing ordinary alert delivery. An OAuth-only
deployment can omit the ordinary alert group. The environment force-off and demo mode
also disable recovery. No browser, mailbox reader or runner is embedded in OMC.

| Recovery flow / code kind | Canonical providers | Contact action and callback boundary |
| --- | --- | --- |
| `redirect_otp` / `otp` | `claude` (alias `anthropic`), `codex` | Browser login with CPA forwarder; reply with six digits only if email OTP is requested |
| `redirect_browser` / `none` | `antigravity` | Complete Google login in the browser; no code message |
| `redirect_manual_callback` / `none` | `devin` | Callback lands on CPA's own port without a forwarder; remote browsers need a tunnel or manual callback URL submission to the runner |
| `device_code` / `device` | `kimi`, `kimi-ai`, `xai`, `meta` | Open the verification page and enter its user code before expiry; no email OTP message |

Only these built-in OAuth providers can create jobs. Gemini, Vertex, unknown providers
and `api-keys.*` identities require key replacement and never send recovery cards.
The login registry normalizes `anthropic` to `claude`; Kimi and Kimi International
remain separate. Redirect forwarders must be reachable from the browser: remote
operators need the appropriate tunnel or the runner's manual callback submission.
OMC does not provide a callback relay or launch a browser. The bundled runner's provider
support must be checked separately; this OMC contract does not implement runner flows.

Enable the app-level `im:message.group_msg` scope and subscribe to
`im.message.receive_v1`. This permission covers every group the bot joins; keep group
membership limited and restrict the OAuth group to the contact and necessary
administrators. The OAuth path requires the exact configured chat, exact contact
`open_id`, group text message, a nonempty message ID, exactly six ASCII digits and an active
OTP waiting job. It ignores all other messages without storing or logging their bodies.
The dedicated group does not answer ordinary bot queries. Other groups retain the
existing mention-based query behavior. SDK event diagnostics use fixed categories.

Run provider-specific runners against the configured base path, normally `/omc/api/v1`. It signs in
through `/omc/api/auth/login` with the management key, then uses the resulting OMC session
cookie. The MCP read-only key is not accepted. These endpoints require a direct
connection from an allowlisted private or loopback IP; public IPs and forwarding
headers are refused. The default CIDRs are `127.0.0.0/8`, `::1/128`, `10.0.0.0/8`,
`172.16.0.0/12`, `192.168.0.0/16` and `fc00::/7`. Use the internal OMC service address
for a Dokploy sidecar and narrow the allowlist to that Docker subnet. Do not publish
runner access through a reverse proxy. Network admission never replaces authentication.

| Method and path under the API base | Contract |
| --- | --- |
| `GET /management/reauth/jobs/next?provider=<canonical>` | Matching active job, otherwise oldest matching pending job; 204 when empty or another provider owns the contact slot; non-OAuth providers return 400 |
| `GET /management/reauth/jobs/{id}` | Allowlisted state, flow/code kind and device display metadata; never an email OTP |
| `GET /management/reauth/jobs/{id}/code` | OTP flows only: one-time `{code}` response; 204 when unavailable or already consumed; always `Cache-Control: no-store` |
| `POST /management/reauth/jobs/{id}/status` | `{status,last_error?,flow?,user_code?,verification_url?,expires_in?}`; invalid transitions return 409 |
| `POST /management/reauth/jobs/{id}/dismiss` | `{confirmed_misclassification:true}`; explicitly dismiss one reviewed false-positive job, preserve history, and release its contact slot; same session/network boundary |

Authentication classification uses the source and endpoint of each failure. An upstream
401 or explicit token/session rejection can require reauthentication; ordinary 403,
payment, quota, entitlement, policy, scope, rate limits and CPA management failures do
not. A model cooldown remains separate from account authentication. Recovery requires
a new successful, recognized quota response, not merely a different error status.

For a confirmed historical false positive, stop the corresponding runner flow and
review the specific job ID and provider evidence before calling `dismiss`. The operation
appends `reauth.dismiss_misclassified`, retains the job as `failed` with
`misclassified_auth_failure`, closes only that episode, clears its pending notifications
and OTP, and resets its invalid collection signal to unknown. It refuses a completed
episode or an unwritable audit. It performs no OAuth call and makes no claim of recovery.
There is no bulk dismissal; a fresh quota read establishes the next observation.

Report `oauth_started` when the browser or device flow is ready. Device starts require
`flow=device`, `user_code`, an HTTPS `verification_url` and `expires_in`
(seconds, 1–86400). The display URL must have no embedded credentials, query or fragment;
it is never fetched by OMC. Redirect starts may omit `flow` or use `redirect`.
The recovery-specific flow labels in the matrix are also accepted, but must match the
job's provider. The job exposes the absolute deadline as `expires_at_ms`. Flow metadata
is accepted only with `oauth_started`.

OMC sends the flow-specific request card on its next background tick. Only email OTP
flows accept six-digit messages and expose `/code`; device/browser/callback flows
return 204. After consuming an OTP, report `code_submitted`. After callback/token
exchange completes, every flow reports `succeeded`, including an OTP-capable browser
login that needed no email challenge. This starts verification, not immediate recovery.
Runner failure reports `failed` with `password_required`, `sso_required`,
`code_unavailable`, `token_exchange_failed` or `runner_failed` in `last_error`.
Arbitrary errors and unknown fields are refused. Never send tokens, passwords,
callback URLs or email OTP text in a status report.

OMC waits two minutes after runner success, then re-reads the credential list and quota.
Failed verification retries after two and four minutes, for at most three checks. A
healthy reading produces a success card. Exhausted verification produces a failure card
and one final quota refresh; transport uncertainty is labeled `verification_unavailable`,
not authentication failure. Runner failures produce a failure card without automatic
OAuth retry. A browser, callback or OTP wait expires after ten minutes; a device wait follows its grant expiry; an accepted code expires after
five minutes. After consumption the runner has ten minutes per remaining OAuth step.
Expiry sends no reminder or failure card. A process restart loses unconsumed codes and
expires those tasks; durable verification deadlines survive restarts. Failed or expired
episodes are rearmed only by a healthy reading followed by a new authentication failure.

The OTP code endpoint is the sole email-code delivery response and is intended only for the
runner. Email OTPs never enter logs, audit details, database records or frontend storage. Device user codes and verification pages are persisted display metadata for the dedicated card and authenticated runner.
Task state changes, code acceptance/consumption, card sends and verification are audited
before their sensitive effects. The private `reauth_jobs` table is unavailable to
operator SQL queries. No endpoint accepts an outbound target URL.

### Update checks and demo

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_UPDATE_CHECK_ENABLED` | `true` | The six-hourly release sweep |
| `OMCPA_UPDATE_CHECK_ON_PAGE_LOAD` | `true` | The check the System Information page makes when opened |
| `OMCPA_OMC_REPO` / `OMCPA_CPA_REPO` | `WizisCool/oh-my-cpa` / `router-for-me/CLIProxyAPI` | `owner/name` the checks read, for a fork |
| `OMCPA_DEMO_MODE` | `false` | Serve the console from the built-in fixture; see [Demo mode](#demo-mode) |
| `OMCPA_UPDATER_ENABLED` | `false` | Set to `true` only after explicitly installing the socket-holding updater; see [Console updates](#console-updates) |
| `OMCPA_UPDATER_URL` | `http://oh-my-cpa-updater:8090` | Where the console looks for the updater. The default is the service and container name every install method uses |
| `OMCPA_GOOGLE_FONTS_URL` | `https://fonts.googleapis.com` | The Google Fonts stylesheet API the font library installs from, or a compatible mirror for a deployment that cannot reach Google. Requests honour `HTTPS_PROXY`; font files are downloaded only from Google's font hosts or the mirror's own site. Used only when an operator installs a family; uploading needs no network |

### MCP read-only key

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_MCP_READONLY_KEY` | unset | Optional second bearer credential for the remote MCP endpoint. When set it reaches only read capabilities and masks email-shaped account labels in its results; it cannot sign in, approve, or run a write tool. Must be at least 16 characters and different from `OMCPA_CPA_MANAGEMENT_KEY`, or start-up fails |

The MCP stdio bridge (`oh-my-cpa mcp`) reads `OMCPA_SERVER_URL`,
`OMCPA_CPA_MANAGEMENT_KEY` and the optional `OMCPA_VERSION` only; the remote MCP
endpoint accepts `OMCPA_CPA_MANAGEMENT_KEY` and, when configured,
`OMCPA_MCP_READONLY_KEY` as bearer tokens. See [Agent and MCP](#agent-and-mcp).

## Deployment constraints

- **Single collector**: The CPA usage queue is destructive. Only one collector may read
  from a CPA instance. If another service collects usage, set
  `OMCPA_USAGE_INGEST_MODE=off`.
- **Single replica**: SQLite WAL requires exclusive single-process access. Run one
  replica mounting the data directory; do not mount over network filesystems
  (NFS/CIFS).
- **Master key**: `OMCPA_MASTER_KEY` is required to decrypt stored credentials and
  payloads. Back it up securely.
- **Framed content**: The console's document declares `frame-src 'self' blob:`. It frames plugin pages from its own origin and the Agent's sandboxed canvases (ADR 0072); a reverse proxy must not replace that policy with one that allows other frame sources.
- **Network security**: Keep CPA on a private network or loopback interface. The supplied
  Compose files publish CPA and OMC only on loopback; use direct local access or an SSH
  tunnel. Public access requires HTTPS through operator-owned infrastructure.
- **Reverse proxy headers**: Set `OMCPA_TRUSTED_PROXY_CIDRS` to the CIDRs of reverse
  proxies whose forwarding headers may be trusted. Both Compose files default to an
  empty trust list. Name only actual proxy peers; leave it unset for direct clients;
  never trust a public range.
- **Gateway version**: The console requires CPA v8.0.0 or later and speaks its v8
  Management API; a gateway older than v8 is refused and every page shows upgrade
  guidance instead. CPA v8 reads an existing v7 `config.yaml` unchanged, so upgrading
  CPA needs no configuration change. The console's first configuration save converts
  such a file to the v8 layout, after keeping an encrypted copy of the original that the
  configuration page offers for download (see `docs/cpa-v8-compat.md`).
- **Gateway configuration**: Omitted request retries and maximum cooldown wait
  both mean `0`; the example's `3` and `30` apply only when explicitly configured.
  Header passthrough forwards filtered upstream response headers to clients.
  See `docs/cpa-v8-compat.md` for the other option semantics and upstream sources.
- **Base path**: Direct OMC serving supports `/omc` or `/`. The console is served at
  the prefix with a trailing slash (`/omc/`); OMC answers the bare prefix (`/omc`) with
  a 308 redirect to it. An operator-owned proxy must preserve the prefix and route the
  bare prefix as well as everything beneath it (`deploy/nginx.conf` shows both rules;
  `docs/install.md` has the Caddy equivalent); CPA keeps its separate port regardless
  of OMC's base path.
- **Container image**: Compose pulls `wiziscool/oh-my-cpa:latest` from Docker Hub by
  default; the full-stack CPA image also defaults to `:latest`. Pin `OMCPA_IMAGE` to the desired version or digest; updates recreate only
  OMC with its existing data and master key. The image runs as uid/gid `10001:10001`,
  includes its own health probe and needs no mounted deployment scripts. A health
  result of `degraded` keeps the container healthy but does not establish CPA access.
- **Existing CPA and panels**: OMC-only Compose can attach to an existing external
  Docker network. It does not replace the gateway or other panels; collection ownership
  must be resolved separately. See `docs/install.md` and `docs/install-for-agents.md`.

## Time zone

Set `TZ=Asia/Kuala_Lumpur` (or another IANA timezone) in the deployment environment to
choose the server calendar. The supplied Compose files pass `TZ` to both OMC and CPA and
default to `UTC`. For a standalone container, pass `-e TZ=Asia/Kuala_Lumpur`. Native
deployments otherwise use the operating system timezone.

In **OMC Settings → Time zone**, the server zone is selected automatically and marked
**Server time zone**. Every option includes its current UTC offset. A manual choice is
persisted for the deployment; selecting the server zone restores the deployment default.
Timestamp displays, calendar selections, daily totals and OMC service logs use this
setting without rewriting stored instants. Keep CPA and OMC deployment timezones equal:
CPA log lines without an offset are interpreted in that shared timezone. Raw log
downloads and explicit UTC pricing-tier rules retain their source semantics.

## Update checks

The System Information page reports the running and published versions of both Oh My
CPA and your gateway. It reads release metadata from `api.github.com` only — fixed host,
no operator-supplied URL — following `HTTP_PROXY`/`HTTPS_PROXY` like the price sync
does. A sweep runs every six hours; opening the page and the **Check for updates**
button also check, subject to a fifteen-minute floor per product — inside it the answer
comes from the stored index and the message says so, because the feed is one shared
per-address budget.

When either product has a newer published release, the **System Information** entry in
the navigation carries a small amber dot. It reflects the stored answer and costs no
request of its own, so it appears once a sweep or a check has found the release; an open
console re-reads that answer hourly. **Ignore this version** on the product's row clears the
dot for that one release and is remembered by the deployment, not the browser; pressing it
again restores the reminder, and a later release is flagged regardless.

The header's GitHub link shows the project's star count, read by the server from the same
host and held in memory. It costs at most one request an hour: the sweep refreshes it, and
so does a console page load when page-load checks are allowed. With both switches below
off, or until a first read has succeeded, the link simply shows no count. Once a read has
succeeded the count stays for the life of the process: a refresh that fails keeps the
previous count.

Two switches, because they answer different questions.
`OMCPA_UPDATE_CHECK_ENABLED=false` stops the sweep on an offline deployment; the page
then keeps working from the last answer it stored, and a check that fails is reported
with its reason and the time it was attempted.
`OMCPA_UPDATE_CHECK_ON_PAGE_LOAD=false` additionally stops the check the page performs
when it is opened, which is what an air-gapped or test deployment wants, since a page
visit is not an operator asking a question. The **Check for updates** button works
either way. GitHub's unauthenticated budget is 60 requests per hour for the address
making them, and the page says so when a check is refused for that reason. Release notes
are held in memory rather than stored, so after a restart the page names the versions
and links to the source while the notes themselves are unavailable — see
`docs/architecture.md` §10.

## Console updates

On a container deployment with an updater (ADR 0099), the System Information page offers
**Update now** beside a product that has a newer release. `docs/install.md` ("Upgrading")
is the operator's guide, including the one command that adds an updater to a deployment
that predates it.

The updater is the Oh My CPA image run as `oh-my-cpa updater serve`. It listens on
`:8090` inside the Docker network and publishes no port. Its settings are read from its
own environment, and an installation needs none of them:

| Variable | Default | Purpose |
| --- | --- | --- |
| `OMCPA_UPDATER_LISTEN_ADDR` | `:8090` | Listen address inside the container |
| `OMCPA_UPDATER_SOCKET` | `/var/run/docker.sock` | The Docker socket as mounted in the container |
| `OMCPA_UPDATER_OMC_CONTAINER` | found by image | The console container, when more than one Oh My CPA runs on the host |
| `OMCPA_UPDATER_CPA_CONTAINER` | found from `OMCPA_CPA_BASE_URL` | The gateway container, when it cannot be found that way |
| `OMCPA_UPDATER_LINK_NETWORK` | empty | Set by `updater install`: the network the updater keeps the console attached to. Empty when both are services of one Compose file |
| `OMCPA_UPDATER_PUBLIC_KEY` | built in | Base64 Ed25519 public key that replaces the built-in release key, for a fork that signs its own releases |

Security boundary:

- The updater holds the Docker socket, which is control of Docker on that host. The
  console does not, and stays read-only and unprivileged. Anyone who can already run
  `docker` on the host gains nothing; anyone who can reach the updater's port without
  its token gets `401`.
- The token is an HMAC of `OMCPA_MASTER_KEY`. Neither container stores it, and it is
  not logged. Changing the master key needs no step here: the updater reads the
  console's current value from Docker.
- The updater accepts a product name only. It never takes an image, a container name or
  a command from the console, updates only the two containers it identified, and
  refuses an image pinned to a version tag or digest.
- Oh My CPA is replaced only by the image a release's signed manifest names, and never
  by an older version. CLIProxyAPI is an upstream image this project cannot sign: its
  update pulls the tag the container already uses.
- A build without the release public key reports `signature_unavailable` and offers no
  console update for Oh My CPA.

Network use: applying an Oh My CPA update makes the console download
`release-manifest.json` and its `.sig` from the release's assets on `github.com`,
following redirects only to GitHub's own asset hosts over HTTPS, through the same proxy
settings as the update check. The image pull is made by the Docker daemon with its own
registry and proxy configuration.

Audit: `system.update.apply` records the admission, or the refusal with its reason,
before the updater is asked; `system.update.completed` records how that admission ended:
the job's outcome, or the updater's refusal or silence when no job started. An Oh
My CPA update ends the process that admitted it, so the next process follows the job
from start-up and records the outcome once the updater reports it finished.

## Database maintenance

Schema 45 upgrades in order through migrations 046–048. Migration 046 adds credential
warmup policy, state and receipt tables plus `quota_snapshots.warmup_json`; migration 047
adds the self-hosted `fonts` and `font_faces` library; migration 048 adds
`channel_model_prices` and `model_price_versions.channel`. Existing fork tables from
migrations 041–045 remain in place. Startup creates and smoke-tests the encrypted
pre-migration backup before applying 046; if any migration fails, its transaction rolls
back and startup stops. Returning to an older executable requires restoring that backup,
not editing `schema_migrations` or trying to reverse individual migrations.

The published Compose files and the Dokploy template keep
`OMCPA_UPDATER_ENABLED=false` by default and do not mount the Docker socket. To opt in,
set the variable to `true`, recreate OMC, then run the explicit `updater install` command
in `docs/install.md`; only the resulting updater container holds the socket.

The System Information page can truncate the WAL or rebuild the database, and refuses
the second while the first runs. Both wait for in-flight writes rather than interrupting
them, and a rebuild is declined up front when the filesystem lacks the free space SQLite
documents needing (up to twice the database file). A job cannot outlive a restart.
`docs/ops/sqlite-operations.md` §6 covers the same operations from the host.

## Agent and MCP

`/agent` sends the conversation and capability results to the CPA model selected on the
page and its upstream provider; the page states this beneath the message box, and the
choice of key, model and reasoning effort is remembered as a server-side preference.
A message may carry up to four PNG, JPEG or WebP images of at most 5 MiB each, beside up
to four text files (40 KiB together). Images are stored encrypted in `agent_documents`
for as long as their turn is part of the conversation, are sent to the model with every
round while they are among the conversation's newest eight, and are read back by the
console from `GET /api/v1/agent/images/{id}` under the console session only.

Agent runs and sessions are open-ended: OMC applies no token, round, tool-call, display-count,
run-duration or session-size ceiling. Provider context/output limits and capability payload and
security checks still apply, as do the gateway's 120-second response-header and stream-idle
timeouts and 1 MiB SSE-line limit. Completed turns send conclusions with bounded metadata to later
rounds, while the stored transcript is retained until reset or explicit replacement. A long
conversation increases SQLite and backup size, serialization work, memory and upstream request
cost. Use Stop to end active work and New conversation to release the retained transcript and
images; monitor disk and process memory on the single replica. Image admission failure attempts
to clean up only newly written image rows; database failures can prevent that best-effort cleanup.

External agents connect over MCP to the same capability registry, in one of two ways:

| Transport | Address | Credential |
| --- | --- | --- |
| Streamable HTTP, served by the console | `<console URL>/api/mcp`, for example `https://omc.example.com/omc/api/mcp` | `Authorization: Bearer <CPA management key>` |
| stdio, the `oh-my-cpa mcp` bridge process | `OMCPA_SERVER_URL` (the console URL, including any base path) | `OMCPA_CPA_MANAGEMENT_KEY` |

The HTTP endpoint needs nothing installed where the agent runs; the **Connect** action at
the top of `/agent` shows the deployment's address and copyable client
configuration. It accepts the management key as a bearer token only (a console session
cookie is refused), throttles wrong keys per client address like the login form, keeps
no MCP session, answers each request in plain JSON, and is refused in demo mode. A
reverse proxy needs no special rule beyond forwarding the base path; serve it over
HTTPS, because the endpoint cannot tell that TLS was terminated in front of it. Clients
that require OAuth discovery cannot use it and use the bridge instead.

The stdio bridge is for clients that only speak stdio. It accepts plain HTTP only for
loopback addresses, refuses redirects, opens no data directory, and prints its usage for
`oh-my-cpa mcp --help`.

There is no separate external credential: holding the management key is
administrator-equivalent, so an external agent can prepare an operation and read its
status but cannot approve it, submit secrets, or complete OAuth. A deployment that wants a
lesser credential sets `OMCPA_MCP_READONLY_KEY`; that credential reaches only read
capabilities, masks email-shaped account labels in its results, and cannot sign in or approve.
A prepared change
returns `status: "pending"` with a link to `<console URL>/authorize/<id>`, a
standalone authorization screen where the signed-in operator (signing in first if
needed) allows or denies that one operation; `omc_operation_status` reads the
outcome and can wait up to 30 seconds for the decision (`wait_seconds`). The read-only database queries and `ask_question` are offered to the built-in
Agent only. Raw SQL results and private model history are omitted from Agent session
responses and run snapshots, and query receipts have no raw-result preview; the selected
model still receives the rows and may use them in its answer or an explicit
`render_ui` display. See `docs/agent-capabilities.md`.

## Model playground

Open **Operate → Playground** under the configured base path. Select an existing client
key and a call point from the live `/v1/models` directory; if no key exists, create one
in Key management first. The page reads `GET <base>/api/v1/playground/models` and sends
explicit turns through `POST <base>/api/v1/playground/chat`; both require the normal
administrator session. No additional environment variable or database migration is
required.

Real requests consume quota under the selected key. Stop cancels the connection but does
not guarantee a refund. The single latest conversation, its parameters and its attached
images are stored as a server-side preference so a reload resumes where you left off;
starting a new conversation discards the stored turns, and image payloads too large to
store are redacted rather than saved. The stored target is the key's usage fingerprint,
never the key itself, and a key or call point that no longer exists is not reselected.
CPA and upstream logging policies still apply. PNG/JPEG/WebP inputs allow four images
per turn, 5 MiB and 40 megapixels per image, and a 32 MiB request including history.
Capability is not guessed from the model name.

The parameter panel takes a system prompt, reasoning effort, temperature, top-p, maximum
output tokens, a User-Agent, and a custom JSON request body. The User-Agent defaults to
this build's own version and is sent as a header, so an upstream sees which build called
it. The custom request body has the highest priority: its keys override the panel's
parameters and any parameter the panel does not model passes through unchanged, but the
resulting request is validated before it is sent, so an override cannot bypass the image
and parameter limits. A non-streaming body is rejected, as this page reads a streamed
answer.

Keep reverse-proxy streaming unbuffered and its read timeout above the 15-second
heartbeat interval. The dedicated stream can last ten minutes, with a 120-second
upstream first-response or idle timeout. Ordinary API timeouts are unchanged. Request
diagnostics never return credentials; copied cURL needs CPA_BASE_URL, CPA_API_KEY and
replacement image data URLs. The public demonstration shows the page and model directory
and offers one browser-local recorded example without inference (ADR 0092).

## Recovering Agent and Playground runs

Weak connections and page refreshes reattach to the original server task rather than
resend a model call or workflow. Stop explicitly cancels that task; closing the page
does not. The latest completed replay journal is retained for 15 minutes, until another
run replaces it or OMC restarts. Agent keeps its authoritative transcript; Playground
saves the recovered result into its existing latest-session preference. Results never
recovered before journal expiry may be unavailable. Agent replay journals grow with output and
have no byte ceiling or execution deadline; Playground retains its bounded journal and
30-minute wrapper deadline (the Playground handler's 10-minute deadline still applies).
An active Agent run therefore consumes memory until it finishes
or is stopped, and its completed journal remains subject to the retention window above.

Console-authenticated recovery endpoints under the configured API base are
`GET /agent/runs/active`, `GET /agent/runs/{id}`, `POST /agent/runs/{id}/cancel` and
their `/playground/runs/` counterparts. Generation POSTs carry `X-OMC-Run-ID`; clients
without that header keep direct-stream semantics. Recovery does not grant extra
capability permissions.

## Custom provider icons

Open the provider icon picker and choose **Custom** to upload a PNG/JPEG/WebP/SVG file
or paste Base64 (with or without an image Data URL prefix). Validate the preview, save a
name, then select the icon. Saved icons can be renamed or replaced; replacement updates
all assignments. Deletion is available even for an icon in use: confirmation shows its
reference count, and deleting it automatically restores affected providers to their
default icons or placeholders. The deployment stores up to 100 static icons, each at
most 512 KiB, with raster dimensions at most 1024 × 1024. Icons persist in SQLite and
are covered by the usual database backup; no separate uploads directory or external
image service is required. Plugin-owned providers retain their plugin branding. Uploads,
edits and deletion are disabled in the public demo.

The authenticated API adds `/custom-icons` (GET/POST), `/custom-icons/preview` (POST),
`/custom-icons/{id}` (PATCH/DELETE) and `/custom-icons/{id}/content` (GET/HEAD) below
the configured API base. Image content is not included in ordinary list or preference
responses.

## TPS calculation mode

OMC Settings → Display chooses whether request-record and Playground TPS includes
first-token latency. The default excludes it when the remaining window is at least
50 ms; otherwise it falls back to total latency. Including it always uses total latency.
The server-stored `omc_tps_calculation_mode` preference (`exclude_ttft` /
`include_ttft`) also supports Agent/MCP reads and writes; historical timing and usage
remain unchanged.

## Plugin pages

A trusted installed page loads through `<base>/api/v1/plugin-host/` behind the console
session; CPA need not be reachable from the browser. It can use CPA-native v0/v8 management
routes and the fixed model directory, including configuration containing client and provider
secrets. Install only plugins you trust: the page is same-origin with the console and acts
with the signed-in operator's authority, while the plugin already runs inside CPA.

The host adapts same-origin native API and resource fetches to carry the OMC session,
including pages that set `credentials: 'omit'`. This does not grant access without a
console session or change credentials on external requests. Plugin scripts that identify
their provider through direct `location.pathname` access receive the CPA-native resource
path, while the frame itself remains under the configured OMC base path.

A plugin page may ask for a management key of its own. The host compares a page's explicit
Authorization or X-Management-Key with the stored CPA management key and answers a wrong one
`401` itself; the wrong key is never sent to CPA, so it cannot count toward CPA's
failed-attempt ban (five failures ban the client address for 30 minutes, and the console
and every hosted page share one address). Only the key stored for the instance is accepted
there. CPA has a single configured key, not a list, but may also honour `MANAGEMENT_PASSWORD`
and a loopback-only `--password`; when those differ from the stored key, enter the stored one. When both
headers are absent, management calls use the stored key; resources carry no key, and
`GET /v1/models` uses only the page's client Authorization. OMC does not publish or store its management key in browser storage;
a third-party page may independently ask for and store a key the operator supplies.

API reads and writes are audited as `plugin.route_call` and responses are uncached.
Configuration-affecting native writes are serialized with console writes and require a
pre-write configuration backup; a backup failure blocks the mutation. All v8 writes and known core v0 writes are conservatively
classified for these gates; ordinary deep plugin actions such as credential synchronization
remain plugin-owned. Native config writes retain CPA's last-write semantics, so do
not concurrently edit the same configuration in a plugin and an unsaved console draft.
Cookies and Set-Cookie are excluded, redirects are not followed, and arbitrary outbound
`api-call` bridges are refused. These routes remain unavailable in demo and are not Agent/MCP
capabilities (ADR 0067).

## Demo mode

`OMCPA_DEMO_MODE` (default `false`) serves the console from a built-in fixture instead
of a CPA, so it needs no management key and no provider credential. Its storage is not
durable — the database is deleted and rebuilt on every boot — and the server refuses
sign-in flows, credential movement, plugin execution and plugin pages, gateway configuration writes and
anything that would leave the process. It opens at the site root rather than `/omc`
unless `OMCPA_BASE_PATH` says otherwise. A visitor's theme stays in their own browser:
a demonstration is shared, so it is neither sent to the server nor read from it.

```bash
pnpm build
OMCPA_DEMO_MODE=true go run ./cmd/oh-my-cpa
```

This mode is what generates the public demonstration's dataset, so it stays in use even
though the public deployment does not run it; `OMCPA_PUBLIC_URL` states that
deployment's origin. The public demo is the same console as static assets with its API
answered by a Cloudflare Worker from that generated dataset: `docs/ops/cloudflare-demo.md`
is its runbook, `docs/architecture.md` §13 and
[ADR 0021](adr/0021-the-public-demonstration-is-generated-data-behind-the-real-console.md)
record why.

## Available model directory

The authenticated `/model-square` route reads `GET /api/v1/management/model-square`
(relative to the deployment base path). The server uses the first nonempty configured
client key to read CPA's live `/v1/models`; no key returns `client_key_required` (409).
No client or upstream secret is returned. Configuration enriches only advertised IDs,
with partial conditions for unavailable model definitions or mappings. Refresh rereads
CPA; it does not make a paid model request.

Model specifications come from the bundled models.dev snapshot in
`internal/modelcatalog/snapshot.json`. Run `pnpm models:sync` when maintaining a release
to update it from models.dev's canonical model data and exact source aliases, then run
`pnpm demo:generate` because the snapshot changes the demonstration's response. There
is no runtime synchronization loop or new environment setting. The page displays the
snapshot date and unknown fields, and renders without browser catalog/CDN requests.
External reference links are user-initiated navigation. Pricing remains on its existing
OpenRouter source and is not synchronized by viewing Model Square. The page additionally
reads the existing `GET /api/v1/pricing` and `GET /api/v1/usage/facets?preset=24h` to show
each name's price and recent requests; if either fails, the directory still renders and
those cells stay blank.

## OAuth model rules

Open **Model rules** in OAuth management to edit CPA's provider-wide aliases or
model exclusions. Aliases are provider-scoped; exclusions are defaults for
OAuth/file-backed credentials without a non-empty own `excluded_models` list.
A non-empty credential list replaces, rather than extends, the provider's
`oauth.excluded-models`. A missing, empty or null credential list inherits the
current provider rules. Use the credential configuration tab to edit an override;
clearing it restores inheritance, not unrestricted availability.

Exact exclusions and `*` patterns match case-insensitively. Provider `*` excludes
all models only for inheriting credentials: with provider `["*"]` and credential
`["model-b"]`, the credential uses only `["model-b"]`, so `model-a` is not blocked
by provider `*`. Clearing that override makes the credential inherit `*` again.
Exclusions run before alias mapping; an alias cannot restore a source hidden by
the applicable rules. Deleting provider rules removes those defaults but leaves
non-empty credential overrides unchanged; it does not guarantee that models are
served again. CPA's per-credential model list is authoritative for availability.
Disable a credential explicitly when it must stop serving.

This precedence requires **CPA v8.0.33 or later**; upgrade an older gateway before
relying on credential overrides. OMC's general v8.0.0 Management API baseline is
unchanged. The behavior follows CPA's [credential override change](https://github.com/router-for-me/CLIProxyAPI/commit/69d0ff8e295aea349966b16f20e2966421bfeb18).
The upstream regression
[`TestCodexExcludedModelsCredentialPriorityAfterPatchAndReload`](https://github.com/router-for-me/CLIProxyAPI/blob/2422c6b0f5fe16495320fa29456493874c1977a0/sdk/cliproxy/service_excluded_models_priority_test.go)
checks immediate PATCH effects, reloads and restored inheritance after clearing.

The facade exposes GET/PATCH at `/api/v1/management/auth-files/model-aliases` and
`/api/v1/management/auth-files/excluded-models`. An exclusion PATCH carries
`provider` and `models`; an empty `models` array removes that provider's rules.
The provider model catalog is read at
`/api/v1/management/auth-files/provider-models?provider=codex`. Without a catalog,
the editors still accept typed names. A successful write requires CPA readback;
changes are serialized with other whole-config writes, backed up and audit logged
(`oauth_model_alias.update` or `oauth_excluded_models.update`). Demo mode allows
reads but refuses durable changes.

## Kimi international quota

The `kimi-ai` connection method uses the international Kimi account system. Its
quota observation is a server-initiated `GET` to the compiled, single-endpoint
allowlist entry `https://api.kimi.ai/coding/v1/usages` (ADR 0064). Domestic Kimi
credentials continue to use `https://api.kimi.com/coding/v1/usages`. Selection
uses credential type, provider and file name; the console does not download the
token to detect an account system or retry the token against the other host.
Neither the browser nor Agent/MCP can supply an arbitrary upstream target.

The usage document is decoded in both shapes Kimi publishes. Counted limits
(`limits[]`, or a lone `usage` object) carry absolute usage, and the newer ratio
pools under `usages` state a share instead: each pool's period is read from its own
key, with the monthly Total pool the common case for a plan without a weekly limit.
A counted limit that already describes a period wins it, because it carries the
absolute usage a share cannot, and a pool states no counts of its own. A pool key
naming no known period keeps its own name as an unclassified window rather than
being filed under a period it does not describe (ADR 0071).

## Read-only reset advice

| Variable | Default | Meaning |
| --- | --- | --- |
| `OMCPA_RESET_ADVICE_EXPIRY_WINDOW` | `72h` | Positive Go duration defining how soon a remaining reset entitlement expires for `reset_advice`; invalid, zero or negative values refuse startup |

The horizon affects the shared console/Agent/MCP read-only advice and announcement window. A fresh quota reading at or
above 90% used prioritizes retaining a reset for recovery. Entitlement snapshots older
than two hours yield unknown, even when recent passive headers exist; a current
window must also have a fresh percentage and an unexpired reset boundary. A shorter
horizon does not change refresh cadence or provider redemption rules. Restart after
changing the variable. No upstream refresh or reset redemption occurs when asking
for advice. See `docs/agent-capabilities.md` and ADR 0109.

## Automatic quota refresh

In `interval` mode, OMC Settings → Quota holds the deployment-wide automatic quota refresh
policy (the `omc_quota_auto_refresh` preference, ADR 0094). **Refresh after a quota resets** is on
by default: about a minute after a window a stored reading reported has reset, that
credential is read again once. **Scheduled quota refresh** reads every enabled credential whose reading is
older than the interval - thirty minutes by default, adjustable from 5 minutes to 24 hours,
or off - including credentials nobody has read, so the first minutes after an upgrade
read every such credential. Each automatic read is a provider request made with the credential through CPA
(`api-call`, or the plugin's quota endpoint for a plugin credential), at most sixteen per
minute; a failed reset-driven read is retried every ten minutes for an hour. Meta credentials are only read on a manual
refresh, because that read exchanges a key upstream. Turning both off restores
operator-only reads in `interval` mode. `adaptive` mode ignores these settings and follows
the environment configuration above. A demonstration never runs either mode.

## Meta quota and live subscription tiers

A Meta Muse quota refresh requires a stored credential with a valid `dca_token`.
OMC downloads that credential server-side and sends a fixed POST to
`https://api.meta.ai/muse-code/key`; runtime-only files and missing DCA tokens are
not replaced with an LLM API key. This endpoint can mint a key while reporting
quota. OMC discards returned keys and account fields, does not update the stored
credential, and records an attempt audit before downloading or calling it.
Audit unavailability refuses the operation. Batch refresh trims and deduplicates
auth indexes before auditing or exchanging keys, preserving first-seen order
and refreshing at most ten distinct credentials. The reading includes the usage window,
weekly window, plan name and explicit active/inactive state when supplied. Missing
shares are unknown, not a zero-used or fully-available quota.

xAI reads both CLI billing documents on `cli-chat-proxy.grok.com`: the credits
document (`/v1/billing?format=credits`), which carries a subscription's own window
and the period that percentage belongs to, and the metered ledger (`/v1/billing`),
which carries the account's monthly limit and spend. A subscription account
publishes nothing in the ledger, so both are read and the period stays atomic: a
window and its reset come from one document, and the ledger's figures arrive beside
it as extra usage. Subscription names come from fixed
`/v1/user?include=subscription` and `/v1/settings` requests after a successful
billing read, and API-key health fallback does not initiate them. Antigravity tiers
come from the daily host's `loadCodeAssist` endpoint, with paid tier taking
precedence over current tier. If these supplemental reads fail, usage remains
readable: xAI keeps its billing fallback, while Antigravity shows an unknown tier
rather than assuming Pro. New endpoints are individually allowlisted and neither
console nor Agent/MCP accepts a caller-chosen URL (ADR 0065, ADR 0071).

A refresh that succeeds without publishing any window is recorded as an
`unpublished` reading rather than as a credential nobody has read, and the console
says so with its own status and copy. Every provider's window parser can produce
that reading, and it is what an unrecognized upstream document shape now surfaces
as (ADR 0071).

## Credential management actions

`POST /api/v1/management/auth-files/refresh` accepts `name` and `auth_index` for
one credential. The console's selected-credential action runs bounded individual
refreshes and reports each outcome; CPA still owns its scheduled token renewal.
The response contains status only, never CPA's refreshed token payload.

`POST /api/v1/management/auth-files/vertex-import` accepts the service-account JSON
as its request body and an optional `location` query (CPA defaults to
`us-central1`). A second import for the same project replaces its credential.
The console previews the project/account and accepts files up to 64 KiB; the
backend bounds the body with the existing 16 MiB auth-file upload limit. The
response contains file name, project, account address and location, not key bytes
or CPA's filesystem path. Closing the dialog invalidates an unfinished file read
and clears its selected key; key bytes are not cached as mutation arguments.

Both actions require the console session, are refused in demo mode, and must
record an attempt audit before calling CPA. If the success audit fails after CPA
has completed the action, the successful action remains successful; the audit
failure is logged without tokens or uploaded key material.

## Daily Feishu quota prediction

The existing Feishu `SummaryTimes`, timezone, `ChatID` and quiet hours also schedule a
read-only quota prediction digest. At a configured minute OMC sends one card only when at
least one stored window is `will_exhaust` or `under_used`; that card replaces the ordinary
scheduled digest for the same local minute. With no finding, the ordinary digest behaves
as before. Disabled, incomplete or degraded Feishu configuration does not evaluate or
send prediction. Delivery uses the existing persisted outbox and retry path, with no new
setting, secret, table or migration.

## Public demonstration conversations

The public demonstration's Agent and Playground offer one browser-local recorded example
each. Send/retry/cancel do not invoke a provider or write the Worker. Other questions are
refused. The server-side demo security classification is unchanged; ADR 0092 documents replay.

## Credential window warmup

Configure provider defaults from OAuth management's Warmup settings, or a complete
credential override from its Window warmup tab. Reset mode is Off, Weekly, or Weekly and five-hour.
Daily warmup defaults off with 06:00 as its editable time in the effective OMC timezone.
Choose an available model; Claude's available Haiku models are listed first. Changing a
model never silently selects a replacement. Saving an enabled policy authorizes automatic
attempts once current billing and window checks permit them; a first manual activation
is not required. Warm up now remains an optional, separately confirmed consuming action.
Previously enabled policies begin participating on the next scheduler round after upgrade,
including credentials with no prior activation observation; defaults remain off.

Paid fallback must be affirmatively disabled by a fresh usage read. Claude uses
`extra_usage.is_enabled`; Codex uses the credits standing (published `has_credits`,
`unlimited` and balance). Missing evidence remains unknown. OMC does not change upstream
billing settings or redeem a reset credit to make warmup eligible. A zero reading with
an active fixed boundary is not an idle window. Model-scoped Sonnet, Opus and Fable
limits can pause the selected family without blocking unrelated Haiku. Fable evidence
accepts the legacy `iguana_necktie` (unless it carries dollar amounts, which make it the
cloud session credit pool) or modern model-scoped `limits`; malformed modern
evidence, including a present non-array or null `limits` field, remains unknown rather
than falling back to an apparently available legacy limit.

The background loop checks once per minute, with bounded four-way rounds of 16, and
reuses stored quota observations targeted for refresh every five minutes. Daily starts
missed by more than one hour are not replayed. If starting now would overlap the next
daily time, reset-driven requests wait, including weekly triggers. Changing the OMC
zone changes future calendar decisions, not stored instants.

The credential section always shows selected-model activation observations and paid-fallback
evidence. No activation observed is a neutral historical fact, while absent billing evidence
reads Unknown and pauses requests. Current reasons distinguish active windows, missing quota
and billing refusals. An unsupported response pauses automatic requests for the selected
model until a confirmed recheck succeeds or an available replacement model is explicitly
selected. Rechecks still obey unresolved-outcome suppression. Manual actions report an outcome
through a toast; sent requests also update the durable last result in the Window warmup tab.
Warm up now opens its confirmation on both HTTP and HTTPS deployments. Switching tabs or
saving credential fields keeps a separate warmup draft open.

A consuming request is recorded before it is sent. If pre-send revalidation refuses the
request, its provisional cycle and daily identities are restored. A temporary startup
recovery failure is logged and retried on later minute checks, not left stopped until restart. Activated, pending, skipped, failed,
executing and uncertain outcomes are distinct. After a pending completion only quota
reads are repeated; transport timeouts and restart-abandoned requests are uncertain
and are not automatically resent. Definite rate-limit refusals get at most three
retries after one, five and fifteen minutes, each preceded by fresh eligibility checks.
Policy and execution records are included in normal SQLite backups. The latest 100
receipts per credential are kept alongside the durable cycle and daily suppression.

API routes under the configured API prefix:

- `GET/PUT /management/quota/warmup/settings`: provider policy and revision.
- `GET/PUT /management/quota/warmup/credentials/{authIndex}`: effective policy and state.
- `POST /management/quota/warmup/credentials/{authIndex}/run`: confirmed, revision-bound
  manual request with caller-stable `request_id`. Reuse that id only to recover its
  original receipt; never retry an uncertain result with a new id.

These are authenticated, typed projections, not a generic request proxy. Writes require
confirmation and the current revision; no URL, prompt, request body or secret can be
supplied. The public demonstration refuses consuming and policy-write routes.

Warmup round ordering is oldest observation first. Fleets exceeding a round's capacity
remain queued rather than increasing concurrency or skipping protection.

## Manual reset announcements

On OAuth management, the quota workspace's **Active reset announcements** section
below the credential collection offers **Register reset announcement**. Enter a lowercase
provider identifier, scope, confidence and original public announcement text (1–4,000
Unicode characters). Model scope requires a model identifier; credential scope requires
its auth index. The optional expected reset time uses the displayed OMC timezone and
must be in the future. Exclude credentials, keys and tokens from announcement text.

Successful registration refreshes the announcement list and credential advice. The list shows source, confidence, scope
and expected reset time; expired entries leave the active view but remain in SQLite
and in API history. No automatic collector or background expiration task runs.
Unverified and untimed entries remain evidence, not confirmation of a quota reset.

Under the configured API prefix, GET/POST /management/quota/reset-announcements require
a console session and writes use the existing same-origin guard. POST accepts provider,
source (manual only), expected_reset_at_ms (future UTC milliseconds or null), scope
(all/model/credential), model, auth_index, confidence (confirmed/probable/unverified)
and original_text. The server assigns id, registered_at_ms and created_at_ms. GET
returns items and total, with limit (1–100, default 20), offset (default 0), and optional
status (active/expired; omitted includes history). Ordering is registration time then id,
both descending. At the expected reset instant, status becomes expired. There is no
rejection action or rejected row: invalid writes return 400 invalid_reset_announcement.

The routes never call CPA and are not Agent/MCP capabilities. The public demonstration
permits the empty list read and refuses registration. SQLite backups include the records.

### Reading reset advice in the quota workspace

Credential rows show Use before expiry, Keep for recovery or No action needed; unknown
results do not receive a certain label. Details retain the local verdict and observations
beside each matching announcement, its source, confidence, expected reset and conflict flag.
A timed confirmed/probable notice within the configured horizon overrides local emergency
advice only when a usable positive entitlement balance is known. Unknown or zero balance
and disabled/unavailable credentials retain their local advice. Unverified and untimed
notices are evidence only. Model notices require an exact persisted model-window match.
The projected advice never authorizes redemption and makes no CPA write (ADR 0111).
