# Ollama quota monitoring

This document describes the first version of Ollama account monitoring (lane B of
`docs/omc-first-batch-spec.md`). It covers the storage, the collection service, the
HTTP API, the security boundary and how the module is wired into the running
process. The console surface is the Ollama account drawer described under
[Integration](#integration).

## What it does

The collector reads each account's quota from one of two fixed upstream paths. A
stored API key is preferred: the client calls `https://ollama.com/api/balance` with a
Bearer header and reads the JSON balance. When the account has no key, or the key path
fails, the client falls back to `https://ollama.com/settings` with the account's
session cookie and parses the HTML. This feature stores several Ollama accounts, each
with an API key and/or cookie the operator pastes in, and reports the last reading and
the newest collection status.

Ollama is **not** in CPA's control chain. Disabling collection here only stops the
collector; it never disables inference and never claims to. The reading is a
monitor-only observation, decoupled from CPA credential status.

## Storage

Migration `044_ollama_accounts.sql` adds one table, `ollama_accounts`; migration
`058_ollama_api_keys.sql` adds the optional API key columns:

- `id` — a stable opaque UUID minted by the server, never derived from an email,
  label or credential.
- `label` — the operator's display name.
- `enabled` — gates collection only, never CPA.
- `cookie_ciphertext`, `cookie_nonce`, `cookie_updated_at_ms` — the AES-GCM encrypted
  session cookie and its version. Replacing the cookie raises the version strictly, so a
  scrape that read an older cookie cannot overwrite the reading of a newer one.
- `api_key_ciphertext`, `api_key_nonce`, `api_key_updated_at_ms` — the optional
  AES-GCM encrypted API key, its nonce and its own version. The columns are nullable,
  with no default and no backfill: an account that predates the migration keeps working
  on the cookie path, and "never had a key" stays distinguishable from an empty value.
  Replacing the key raises only the key version, so the cookie and its version are
  untouched.
- `updated_at_ms` — ordinary metadata: when the account row was last updated. It is
  deliberately not a generation: a label edit or an enable toggle does not invalidate a
  reading that is already due or in flight.
- `status`, `last_success_at_ms`, `last_observed_at_ms`, `newest_error`,
  `newest_error_at_ms` — the newest collection outcome. `last_observed_at_ms` moves
  on every attempt, success or failure, so "never collected" is distinguishable from
  "collected and failed".
- `next_attempt_at_ms`, `attempt_count` — the persisted retry schedule.
- `quota_snapshot_json`, `quota_observed_at_ms` — the newest safe quota reading,
  stored separately from both credentials. It is a `quota.NormalizedQuota` with
  `auth_index` `ollama:<id>` and provider `ollama`. An operational read selects this
  column without ever touching either credential.

The plaintext API key and cookie are never written to the database, a log, an error,
an audit row or an API response. Ordinary account reads project only `has_api_key`
and `has_cookie`; deleting the row removes both credentials with it.

## Collection

The collector (`internal/ollama`) runs once on start and then every
`ollama.DefaultInterval` (15 minutes). Its collaborators are injected functions, so
the integration owner wires the repository and the cipher in, and tests inject a
clock, a fetch and a store.

One `ollama.Collector` is the single per-account refresh entry point, shared by the
periodic pass, the create/update triggers and the console's manual refresh. A scrape
that is already running is joined rather than duplicated, at most two scrapes run at
once across every entry point, and a persisted backoff suppresses all of them. A
scrape result carries the version of the credential its winning path read, and the
store writes it only while that credential still matches and - for an automatic result
- the account is still enabled. The design is deliberately simple rather than a full
lifecycle state machine: label edits and enable toggles do not invalidate a reading,
and this lane does not track an enable generation.

The credential preference is fixed per attempt:

1. If an API key is stored, decrypt it and call the balance endpoint. A successful
   reading is final; it carries only the key version for the write guard. Key material
   is cleared immediately after the request.
2. Otherwise, or after any key-path failure (transport, auth, rate limit, parse), decrypt
   the session cookie and read the settings page. A cookie success is final and carries
   only the cookie version; the earlier key failure is not merged into it.
3. If both paths fail, the persisted error names the key path and the cookie path so the
   operator knows which credential to fix. It never contains either secret.

The account's stored key therefore never leaks through a DTO, log, error, snapshot or
audit row; only its presence flag and its version are metadata.

- Only enabled accounts are fetched on the periodic pass; a disabled account is
  skipped, and turning it off also drops any pending backoff. An explicit manual
  refresh may still read a paused account once, without changing `enabled`. An
  automatic result that finishes while the account is disabled is refused, so it does
  not rewrite the status, error, snapshot or retry schedule; a manual refresh of the
  paused account is written.
- Adding an enabled account, changing its API key or cookie, or bringing a paused
  account back on triggers a scrape at once, without waiting for the cadence. A
  label-only edit sends no request, and a created-disabled account stays untouched. The
  triggers run on the service's long-lived context, so a trigger that outlives its
  request is not cancelled.
- A trigger runs one round. A credential change that lands while a scrape is already
  running is **not** dropped to the cadence: the running scrape read the old credential
  version, the store refuses its result, and the account is left as one pending marker
  so the worker takes the newest credential in one more round. Repeated changes to the
  same account collapse into that single marker rather than one scrape per change. A
  live 429, a pause or a shutdown stops the follow-up, and the account's enable state is
  never turned into a generation.
- **Accepted race.** A disable and a quick resume that both land under one in-flight
  scrape are not tracked: that scrape may write its reading to the resumed account,
  because its credentials are unchanged and the account is enabled again when it finishes.
  This is accepted for a self-hosted deployment of a few trusted operators reading
  quota; it is not a security boundary and does not claim to be one.
- The targets are the fixed balance URL and the fixed settings URL. There is no
  operator input that can point the collector at another address. Redirects are not
  followed on either path, so a redirect can neither be mistaken for a login page nor
  carry a credential off-domain.
- A `401`, `403`, a redirect or a login page is an `auth_invalid` outcome with no
  backoff: only the operator can supply a new API key or cookie.
- A timeout, a `429` or any other transport failure is a `transport_error`; a page
  with neither two valid USD amounts nor a legacy numeric window is a `parse_error`. Both back off. A `429`'s
  `Retry-After` is honoured when present; otherwise the wait doubles from
  `DefaultBackoffBase` (5 minutes) up to `DefaultBackoffMax` (6 hours). A manual
  refresh, or a first/update-credential trigger, never bypasses a live backoff.
- The preferred source is the balance endpoint's `included` object: remaining is
  `included.balance_usd`, used is `allowance_usd - included.balance_usd` clamped at
  zero, and total is their sum. Both amounts may be JSON numbers or decimal strings;
  a missing amount stays absent rather than becoming zero, and a negative, non-finite
  or oversized amount is refused. `purchased.balance_usd` is a separate credit balance
  and is not added to the included figures. `included.period.until` is parsed but not
  persisted because the shared quota shape has no reset field.
- The cookie fallback source is the response DOM's **Usage credits** wallet. The parser
  cuts a text slice from the heading — an optional plan badge such as `max` is
  allowed — to the first section close or next heading, and reads only inside it;
  DOM ancestors never widen the scope. The balance is the first amount-only span
  in the slice, and the used figure is either a "Monthly credits used" span pair
  or the older "used this month" wording. `internal/ollama/credits.go` uses the
  existing HTML parser dependency, not the old flattened-text balance search.
  Only complete USD amounts are accepted; paths, mixed characters, other
  currencies, negative values and unsupported decimal precision are refused.
  There is no cleanup that extracts digits from damaged strings, and a missing
  used figure stays absent instead of becoming zero.
- The safe snapshot's `ollama_credits` object carries `source=balance_api` or
  `source=settings_usage_credits`, `remaining_cents`, `used_cents`, `total_cents`,
  optional `used_percent`, `refill_text`, and `models` (`model`, `requests`). The
  total is remaining + used; a percentage requires positive total and used amounts. A
  known zero is stored as zero, distinct from an absent field; the console describes it
  in words.
- The balance endpoint publishes neither monthly model request counts nor refill
  text, so the key path leaves those fields absent. The console accepts both known
  sources and shows the USD amounts from `balance_api`; an omitted model count renders
  as "not reported" for that field, never as zero and never as a generic missing-data
  label for the whole card.
- Monthly refill text such as "Monthly credits refill in 2 weeks" stays verbatim.
  It never becomes a reset timestamp, duration, capacity or depletion prediction.
  The separate Models used this month section supplies per-model request counts.
- Legacy percentage parsing still supports older upstream pages, but a recognized
  Usage credits card never falls back to it. Old snapshots and the legacy balance
  string do not populate the current USD console view. Other providers are unchanged.
- On success the safe snapshot and the auth index `ollama:<id>` are persisted. The
  shared observation timeline is written only when a window states a real duration,
  which Ollama does not today. A failure keeps the last good snapshot and only
  advances `last_observed_at_ms`; the console displays data missing while the latest
  collection status is unsuccessful, so "collected and failed" is distinguishable from
  "never collected".

## Source evidence and validation limits

Official Ollama documentation describes [Balance](https://docs.ollama.com/api/balance)
(`included.balance_usd`, `allowance_usd`, UTC period) and
[Cloud usage](https://docs.ollama.com/api/cloud-usage) (`group_by=model`, request counts).
The balance endpoint requires a Bearer API key; a session cookie is not accepted there
and returns `401`. This module therefore prefers a stored API key for
`https://ollama.com/api/balance` and keeps the session cookie only as the fallback for
the settings page. The balance mapping preserves the included-versus-purchased
distinction; the usage API's rolling 24h/7d/30d ranges are not relabelled as this
month's complete model list.

Issue #86 describes the settings page but supplies no captured original HTML or
verified cookie-authenticated quota endpoint. The cookie fallback targets labelled DOM
facts in the existing fixed settings response; it does not invent an endpoint or rely on
plan prices. Tests use synthetic JSON, HTML, cookies, model names and loopback
transports. No authenticated live call is made by the suite: the documented `200` for a
valid key and `401` for a cookie are the only evidence for that boundary, and a live
`200` for either path can only be proven after deployment. Matching actual upstream
markup and amounts therefore remains a live acceptance step; if the card is absent,
client-rendered only or its amounts are damaged, collection reports a parse error and
the UI says data missing.

## HTTP API

All routes sit behind the existing console session authentication and same-origin
guard, exactly like the other management writes; the read-only MCP key reaches none
of them. Wire fields are `snake_case`.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/ollama/accounts` | List accounts; each carries `has_api_key` and `has_cookie`, never either secret. |
| POST | `/api/v1/ollama/accounts` | Add an account; `cookie` is required and `api_key` is optional. Both are write-only. An enabled account is collected at once. |
| GET | `/api/v1/ollama/accounts/{id}` | One account's metadata, including `refreshing` and the live `retry_at_ms`. |
| PUT | `/api/v1/ollama/accounts/{id}` | Update `label`, `enabled`, `cookie` and/or `api_key`; omitted credentials keep the stored ones, and a present empty `api_key` is refused. A credential change or a resume triggers a scrape; a label-only edit does not. |
| DELETE | `/api/v1/ollama/accounts/{id}` | Remove an account and both stored credentials. |
| POST | `/api/v1/ollama/accounts/{id}/refresh` | Force one collection now. Synchronous and bounded; coalesces with a collection already running. |
| GET | `/api/v1/ollama/quota` | The newest stored snapshots, read-only; never triggers a scrape. |

Account DTO (never includes the API key or cookie):

```json
{
  "id": "…",
  "label": "work",
  "enabled": true,
  "has_cookie": true,
  "has_api_key": true,
  "status": "ok",
  "last_success_at_ms": 0,
  "last_observed_at_ms": 0,
  "newest_error": "",
  "next_attempt_at_ms": 0,
  "attempt_count": 0,
  "created_at_ms": 0,
  "updated_at_ms": 0,
  "refreshing": false,
  "retry_at_ms": 0
}
```

`refreshing` is true while a scrape for the account is in flight; `retry_at_ms` is the
earliest instant a refresh is allowed again (a persisted backoff or the manual gap,
whichever is later) and is omitted when nothing is pending.

### Manual refresh

`POST /api/v1/ollama/accounts/{id}/refresh` is a console session write; the read-only
MCP key reaches none of these routes and a demo refuses it. It is synchronous and
answers after the scrape finishes. The whole call - waiting for a concurrency permit,
the scrape and the reload - shares one budget (`RefreshTimeout`, 20 seconds by
default) that is rooted in the collector's process context, not the request: a client
that disconnects does not truncate a scrape, no second timeout is stacked on top of
the first, and a request held behind a saturated service returns by the deadline with
`refreshed` false instead of queueing and then scraping. Before the collector's
process context is installed (the instant before the listener accepts, or a service
that was never started) the route answers `503`. It answers:

```json
{
  "account": { "…": "the metadata above, after the attempt" },
  "quota": { "…": "the newest safe snapshot, when one exists" },
  "refreshed": true,
  "retry_at_ms": 0
}
```

- `refreshed` is true only when a scrape actually ran for the request and its reading
  was written. A request that coalesces onto a scrape already in flight returns that
  scrape's real outcome; it is never reported as a fresh success that did not happen.
- The manual refresh runs one round and does not chase a change that lands under it. If
  the API key or cookie it read was replaced while it ran (so the store refused the old
  credential version), it answers the existing `503` ("the account changed while it
  ran") instead of returning an old success; the next click or the next cadence reads
  the new credential.
- When a persisted backoff or the manual minimum gap still applies, the response is
  `429` with `refreshed` false and `retry_at_ms` naming when the account is free, so the
  console disables the button instead of hammering `ollama.com`.
- A missing account is `404`. With the collector switched off (`OMCPA_OLLAMA_COLLECTOR_ENABLED=false`)
  or in a demo the route is `503` and no fallback collector is started.
- Refreshing a paused account is a deliberate one-off read: it does not change `enabled`,
  which only gates the periodic pass.

## Security boundary

- The API key and cookie are encrypted at rest with the same master-key `Cipher`
  (AES-GCM) the repository already uses for CPA credentials. Each has its own nonce
  and version column.
- DTOs expose only `has_api_key` and `has_cookie`; list, detail and update responses
  are covered by a test that asserts the key, nonce and cookie text are absent. The
  write-only request field is `api_key`; it is never echoed.
- Neither secret is logged or embedded in an error string. A final failure names the
  key path and/or the cookie path, never the material.
- Audit details carry only a boolean such as `api_key_changed`, never the value.
- `ollama_accounts` is not classified as readable, so the Agent's raw SQL path
  refuses it even before the classification registry is updated; both encrypted
  credentials are unreadable there.

## Integration

The module is wired into the running process:

- `api.RegisterOllamaRoutes(v1, handler)` is called inside the authenticated
  `/api/v1` group in `internal/api/handler.go`.
- `handler.BuildOllamaService()` returns the collector; `app.Run` builds it once,
  hands the same instance back through `handler.SetOllamaService` and starts its
  `Run` under the process lifetime context. A demo never builds it, and
  `OMCPA_OLLAMA_COLLECTOR_ENABLED=false` switches it off without removing accounts
  (the manual refresh then answers `503`). `OMCPA_OLLAMA_COLLECTOR_INTERVAL`,
  `OMCPA_OLLAMA_TIMEOUT`, `OMCPA_OLLAMA_BACKOFF_BASE` and `OMCPA_OLLAMA_BACKOFF_MAX`
  override the module defaults.
- `TABLE_LIFECYCLES` (`internal/repository/lifecycle.go`) declares
  `ollama_accounts` permanent, and the readable/hidden classification
  (`internal/repository/readonly_query.go`) hides it from the operator query path
  because it carries the encrypted API key and cookie.
- The newest safe reading reaches the console through `GET /api/v1/ollama/quota`
  and the read-only summary capability `ollama_accounts_status`; the Feishu report
  source also includes it.
- The console surface is implemented and unified into the main OAuth workspace:
  - Ollama accounts project directly into `OAuthManagementPage.tsx` table records,
    filter tabs (`CREDENTIAL_PROVIDERS.ollama`), counts, search, and USD credit
    views alongside CPA credentials.
  - Accessible row checkboxes participate in mixed-source batch refresh, deletion
    and enable/disable. Each action uses its source's existing API; Ollama IDs never
    enter CPA auth-file or quota-refresh requests. Enable/disable changes collection,
    not inference. CPA-only download, model rules, cooldown clear and credit redeem
    remain unavailable for Ollama.
  - Page, filtered and all quota refresh scopes include enabled Ollama accounts,
    even before a first snapshot exists. Their targets use the dedicated per-account
    refresh endpoint, while CPA targets keep the bounded CPA batch endpoint.
    Disabled records are skipped by default with visible counts and reasons.
    Individual manual refresh remains available for a paused account.
  - Friendly labels are used in rows, management lists and details; opaque IDs and
    index prefixes remain internal targeting keys rather than display fallbacks.
  - The row switch controls periodic collection for the Ollama account (`api.updateOllamaAccount`),
    not CPA inference routing.
  - The standalone account drawer `web/src/components/ollama/OllamaManagementDrawer.tsx`
    and modal `web/src/components/ollama/OllamaAccountEditorModal.tsx` allow full management.
  - The list, management drawer and detail drawer share
    `web/src/components/ollama/OllamaCreditsView.tsx`: USD amounts, labelled calculated
    usage, relative monthly refill text and monthly model requests. Both
    `settings_usage_credits` and `balance_api` snapshots are rendered; a field the
    balance endpoint does not publish is reported as not reported rather than making
    the populated amounts look missing. Stored historical amounts are hidden when the
    newest collection failed; they are retained in storage. The list/refresh routes
    decode stored snapshots through a typed DTO allowlist.

## Provenance and licensing

The settings-page parsing approach is informed by the retired CPA-buddy (cpab) Ollama
collector, which credits `jacklee-code/ollama-cloud-quota-monitor` and
`Wei-Shaw/sub2api`. That lineage is LGPL-3.0-only. This package is a fresh
implementation that reuses only the approach, not the source text. The balance
derivation — remaining from `included.balance_usd`, used as
`allowance_usd - included.balance_usd`, and purchased credit kept separate — is ported
from the MIT-licensed `steipete/CodexBar` (`Plugins/ollama-api.ts`). The actual
obligations and their review status are recorded in
[Third-party notices](../THIRD_PARTY_NOTICES.md); a rewrite is not asserted there to
waive any obligation.

## Synthetic browser evidence

The existing Ollama browser scenario captures the shared USD view in the
[desktop workspace](images/ollama-usd/desktop.png),
[phone workspace](images/ollama-usd/phone.png) and
[detail drawer](images/ollama-usd/details.png). The figures are synthetic fixture
values, not a reading from a real account.
