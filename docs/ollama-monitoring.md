# Ollama quota monitoring

This document describes the first version of Ollama account monitoring (lane B of
`docs/omc-first-batch-spec.md`). It covers the storage, the collection service, the
HTTP API, the security boundary and how the module is wired into the running
process. The console surface is the Ollama account drawer described under
[Integration](#integration).

## What it does

Ollama Cloud publishes no quota API. The only known way to read an account's quota
is to request the fixed settings page `https://ollama.com/settings` with the
account's session cookie and parse the HTML. This feature stores several Ollama
accounts, each with a cookie the operator pastes in, and reports the last reading
and the newest collection status.

Ollama is **not** in CPA's control chain. Disabling collection here only stops the
scraper; it never disables inference and never claims to. The reading is a
monitor-only observation, decoupled from CPA credential status.

## Storage

Migration `044_ollama_accounts.sql` adds one table, `ollama_accounts`:

- `id` — a stable opaque UUID minted by the server, never derived from an email,
  label or cookie.
- `label` — the operator's display name.
- `enabled` — gates collection only, never CPA.
- `cookie_ciphertext`, `cookie_nonce`, `cookie_updated_at_ms` — the AES-GCM encrypted
  session cookie and its version. Replacing the cookie raises the version strictly, so a
  scrape that read an older cookie cannot overwrite the reading of a newer one.
- `updated_at_ms` — ordinary metadata: when the account row was last updated. It is
  deliberately not a generation: a label edit or an enable toggle does not invalidate a
  reading that is already due or in flight.
- `status`, `last_success_at_ms`, `last_observed_at_ms`, `newest_error`,
  `newest_error_at_ms` — the newest collection outcome. `last_observed_at_ms` moves
  on every attempt, success or failure, so "never collected" is distinguishable from
  "collected and failed".
- `next_attempt_at_ms`, `attempt_count` — the persisted retry schedule.
- `quota_snapshot_json`, `quota_observed_at_ms` — the newest safe quota reading,
  stored separately from the encrypted cookie. It is a `quota.NormalizedQuota` with
  `auth_index` `ollama:<id>` and provider `ollama`. An operational read selects this
  column without ever touching the cookie.

The plaintext cookie is never written to the database, a log, an error, an audit
row or an API response.

## Collection

The collector (`internal/ollama`) runs once on start and then every
`ollama.DefaultInterval` (15 minutes). Its collaborators are injected functions, so
the integration owner wires the repository and the cipher in, and tests inject a
clock, a fetch and a store.

One `ollama.Collector` is the single per-account refresh entry point, shared by the
periodic pass, the create/update triggers and the console's manual refresh. A scrape
that is already running is joined rather than duplicated, at most two scrapes run at
once across every entry point, and a persisted backoff suppresses all of them. A
scrape result carries the cookie version it read, and the store writes it only while
the cookie still matches and - for an automatic result - the account is still enabled.
The design is deliberately simple rather than a full lifecycle state machine: label
edits and enable toggles do not invalidate a reading, and this lane does not track an
enable generation.

- Only enabled accounts are fetched on the periodic pass; a disabled account is
  skipped, and turning it off also drops any pending backoff. An explicit manual
  refresh may still read a paused account once, without changing `enabled`. An
  automatic result that finishes while the account is disabled is refused, so it does
  not rewrite the status, error, snapshot or retry schedule; a manual refresh of the
  paused account is written.
- Adding an enabled account, changing its cookie, or bringing a paused account back on
  triggers a scrape at once, without waiting for the cadence. A label-only edit sends
  no request, and a created-disabled account stays untouched. The triggers run on the
  service's long-lived context, so a trigger that outlives its request is not cancelled.
- A trigger runs one round. A cookie change that lands while a scrape is already running
  is **not** dropped to the cadence: the running scrape read the old cookie, the store
  refuses its result, and the account is left as one pending marker so the worker takes
  the newest cookie in one more round. Repeated changes to the same account collapse
  into that single marker rather than one scrape per change. A live 429, a pause or a
  shutdown stops the follow-up, and the account's enable state is never turned into a
  generation.
- **Accepted race.** A disable and a quick resume that both land under one in-flight
  scrape are not tracked: that scrape may write its reading to the resumed account,
  because its cookie is unchanged and the account is enabled again when it finishes.
  This is accepted for a self-hosted deployment of a few trusted operators reading
  quota; it is not a security boundary and does not claim to be one.
- The target is the fixed settings URL. There is no operator input that can point
  the collector at another address, and redirects are not followed, so a redirect
  can neither be mistaken for a login page nor carry the cookie off-domain.
- A `401`, `403`, a redirect or a login page is an `auth_invalid` outcome with no
  backoff: only the operator can supply a new cookie.
- A timeout, a `429` or any other transport failure is a `transport_error`; a page
  that parses to no numeric window is a `parse_error`. Both back off. A `429`'s
  `Retry-After` is honoured when present; otherwise the wait doubles from
  `DefaultBackoffBase` (5 minutes) up to `DefaultBackoffMax` (6 hours). A manual
  refresh, or a first/update-cookie trigger, never bypasses a live backoff.
- Only a numeric percentage makes a window real. Missing values are never filled
  with `0` or `100`, and a value outside `[0,100]` (including a negative such as
  `-1%`) or a non-numeric value is refused rather than clamped to a fabricated
  reading, so the window is left unreported. A relative reset ("Resets in 3h") is
  marked `derived`; an absolute stamp is `exact`. The window duration is left
  unknown, because the page does not state one and it must not be inferred as "5h".
- On success the safe snapshot and the auth index `ollama:<id>` are persisted. The
  shared observation timeline is written only when a window states a real duration,
  which Ollama does not today. A failure keeps the last good snapshot and only
  advances `last_observed_at_ms`, so "collected and failed" is distinguishable from
  "never collected".

## HTTP API

All routes sit behind the existing console session authentication and same-origin
guard, exactly like the other management writes; the read-only MCP key reaches none
of them. Wire fields are `snake_case`.

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/v1/ollama/accounts` | List accounts; each carries `has_cookie`, never the cookie. |
| POST | `/api/v1/ollama/accounts` | Add an account; `cookie` is required. An enabled account is scraped at once. |
| GET | `/api/v1/ollama/accounts/{id}` | One account's metadata, including `refreshing` and the live `retry_at_ms`. |
| PUT | `/api/v1/ollama/accounts/{id}` | Update `label`, `enabled` and/or `cookie`; an omitted cookie keeps the stored one. A cookie change or a resume triggers a scrape; a label-only edit does not. |
| DELETE | `/api/v1/ollama/accounts/{id}` | Remove an account. |
| POST | `/api/v1/ollama/accounts/{id}/refresh` | Force one scrape now. Synchronous and bounded; coalesces with a scrape already running. |
| GET | `/api/v1/ollama/quota` | The newest stored snapshots, read-only; never triggers a scrape. |

Account DTO (never includes the cookie):

```json
{
  "id": "…",
  "label": "work",
  "enabled": true,
  "has_cookie": true,
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
  the cookie was replaced while it ran (so the store refused the old-cookie reading),
  it answers the existing `503` ("the account changed while it ran") instead of
  returning an old success; the next click or the next cadence reads the new cookie.
- When a persisted backoff or the manual minimum gap still applies, the response is
  `429` with `refreshed` false and `retry_at_ms` naming when the account is free, so the
  console disables the button instead of hammering `ollama.com`.
- A missing account is `404`. With the collector switched off (`OMCPA_OLLAMA_COLLECTOR_ENABLED=false`)
  or in a demo the route is `503` and no fallback collector is started.
- Refreshing a paused account is a deliberate one-off read: it does not change `enabled`,
  which only gates the periodic pass.

## Security boundary

- The cookie is encrypted at rest with the same master-key `Cipher` (AES-GCM) the
  repository already uses for CPA credentials.
- DTOs expose `has_cookie` and never the secret; list, detail and update responses
  are covered by a test that asserts the cookie text is absent.
- The cookie is never logged or embedded in an error string.
- `ollama_accounts` is not classified as readable, so the Agent's raw SQL path
  refuses it even before the classification registry is updated; the encrypted
  cookie is unreadable there.

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
  because it carries the encrypted cookie.
- The newest safe reading reaches the console through `GET /api/v1/ollama/quota`
  and the read-only summary capability `ollama_accounts_status`; the Feishu report
  source also includes it.
- The console surface is implemented and unified into the main OAuth workspace:
  - Ollama accounts project directly into `OAuthManagementPage.tsx` table records,
    filter tabs (`CREDENTIAL_PROVIDERS.ollama`), counts, search, and session/weekly quota
    views alongside CPA credentials.
  - Action protections ensure CPA batch actions and single actions (download JSON,
    model rules, CPA quota refresh, cooldown clear, credit redeem) do not operate on
    Ollama rows.
  - The row switch controls periodic collection for the Ollama account (`api.updateOllamaAccount`),
    not CPA inference routing.
  - The standalone account drawer `web/src/components/ollama/OllamaManagementDrawer.tsx`
    and modal `web/src/components/ollama/OllamaAccountEditorModal.tsx` allow full management.

## Provenance and licensing

The parsing approach is informed by the retired CPA-buddy (cpab) Ollama collector,
which credits `jacklee-code/ollama-cloud-quota-monitor` and `Wei-Shaw/sub2api`. That
lineage is LGPL-3.0-only. This package is a fresh implementation that reuses only
the approach, not the source text. The actual obligation and its review status are
recorded in [Third-party notices](../THIRD_PARTY_NOTICES.md); a rewrite is not
asserted there to waive any obligation.
