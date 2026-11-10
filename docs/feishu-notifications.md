# Feishu Notifications (lane C, first batch)

Lane C of the OMC first batch absorbs the retired CPA-buddy (cpab) Feishu bot as
an OMC capability: it reuses the bot long connection, answers read-only queries
from OMC's newest quota data, and denoises notifications so a problem is announced
once and recovery is not nagged. Credential/window/reset identities remain tracked
through forecast and low quota; the first actual exhaustion triggers one actionable
push. It does **not** call a language model, hold an
Agent session, or trigger an upstream quota refresh.

This document is the lane-C contract: the interface the code exposes, the
migration it adds, the integration wiring the owner still owes, the new dependency,
and what remains unverified. The approved scope is in
[docs/omc-first-batch-spec.md](omc-first-batch-spec.md) §4.

## Code map

| Path | Responsibility |
| --- | --- |
| `internal/feishu/` | The bot: report DTO, policy, renderer, sender seam, dedup, service lifecycle, websocket source, and the scheduled quota prediction digest in `prediction_digest.go`. |
| `internal/repository/feishu_notifications.go` | Persistence for the settings, the encrypted secret and the runtime document. |
| `internal/api/feishu.go` | The settings/status/test routes and the `feishu.Store` adapter. |
| `migrations/045_feishu_notifications.sql` | The schema. |

The package never imports `internal/api`, `internal/quota` or `internal/domain`.
It answers from a minimal internal DTO (`feishu.Report`) so it cannot trigger a
refresh and cannot widen the facade DTO allowlist.

## Migration 045

`feishu_secrets` stores the self-built app secret AES-GCM encrypted beside its
nonce. `feishu_settings` holds non-secret web settings only, so `has_secret` is
derived from `LENGTH(ciphertext) > 0` and the secret is never read by an ordinary
settings read. `feishu_pending_items` is the structured problem list.
`feishu_notifications` is the delivery outbox plus the sent acknowledgements (a row
is marked `sent` only after Feishu confirms delivery). `feishu_channel_tracks`
backs `monitor_down`. `feishu_state` is the single-row module memory (startup
summary sent, last scheduled digest). `feishu_account_signals` is the newest safe
collection outcome per credential: one upserted row carrying only a fixed state word
(`healthy`, `auth_invalid`, `transport`, `parse`, `unknown`) and the instant the
outcome was observed. It exists because a quota snapshot is written only for a
successful read, so an auth failure — whose `needs_reauth` recommendation is not
persisted in the snapshot — would otherwise have no durable evidence.

The encrypted-secret table is classified as **hidden** from the operator's read-only
SQL path in `internal/repository/readonly_query.go`; the other Feishu tables,
including `feishu_account_signals`, are classified readable, and every table declares
its retention in `internal/repository/lifecycle.go`.

## Configuration contract

`feishu.Config` is the web-configured settings **without** the secret:

```
Enabled, AppID, HasSecret, ChatID, SummaryTimes []string, Timezone,
QuietStart, QuietEnd, TightRemainingPercent,
ChannelUnavailableAfter, MonitorDownAfter time.Duration
```

`feishu.Overlay` is the environment override supplied by the config/integration
layer: `ForceDisabled bool`, and non-nil `Enabled`, `AppID`, `AppSecret`, `ChatID`
pointers. The plaintext secret travels only here.

`resolveConfig(web, overlay) feishu.Resolved` combines and validates them. It never
returns an error: an unusable configuration yields `Degraded: true` with a
`Reason`, `Enabled: false`, and the module stays loaded but inert, so it can never
fail process startup. A `ForceDisabled` overlay wins over the web switch; `Source`
reports `env_disabled`, `env_enabled`, `web` or `default_off`.

## Service contract

`feishu.NewService(feishu.Options) *feishu.Service` builds the bot. Every external
seam is injectable so a test constructs one that never reaches Feishu:

- `Source feishu.ReportSource` — `ReadReport(ctx) (Report, bool)`, read-only.
- `Store feishu.Store` — `Load/Save(ctx, *State)`. `feishu.NewMemoryStore()` is the
  in-memory implementation.
- `Renderer feishu.ReportRenderer` — `Text`, `Card`, `CardSimple`.
  `feishu.NewPlainReportRenderer(loc)` supplies the bounded decision card.
- `SenderFactory func(Resolved) Sender` — defaults to the official SDK sender.
- `SourceFactory func(Resolved) EventSource` — defaults to the Lark websocket.
- `Now func() time.Time`, `EvalInterval time.Duration`.

`ReportRenderer` is the card hook: the shipped `PlainReportRenderer` emits a
header plus markdown elements in schema 2.0, and `CardSimple` is the degraded card
the sender retries when the full card is rejected. Both variants preserve the
conclusion, three-point bound and single console button.

Lifecycle methods:

- `Start(ctx) error` — launches the long connection and the classification loop; a
  no-op when already started or when the configuration is not ready.
- `Stop()` — cancels the connection and the loop, waits for both, and cancels
  in-flight query tasks. Idempotent.
- `Reload(ctx, web Config, overlay Overlay)` — applies new settings without a
  redeploy. Only a real connection-identity change (app id, app secret, or the
  enabled/ready state) reconnects; the recipient is **not** connection identity,
  so changing it applies to the next send on the existing connection, and a
  policy-only change (quiet hours, digest times, thresholds) applies on the next
  pass. The low-quota trigger is fixed at 10%; legacy stored thresholds do not
  change it. It never resends the one-time startup summary.
- `StatusSnapshot(ctx) Status` — the read-only console state (enabled, ready,
  degraded, source, forced-off, connected, open items, pending/queued sends, last
  summary, last error).
- `SendTest(ctx) error` — sends one status card to the configured person; called only on
  an explicit operator action.
- `HandleMessage(ctx, Inbound) error` and `HandleCardAction(ctx, CardAction) error`
  implement `feishu.EventHandler`; the websocket source adapts SDK events onto them.

`feishu.Options.Source`, `SenderFactory` and `SourceFactory` are the injection
points the required tests use; no test opens a Feishu connection.

## Report DTO

`feishu.Report` → `ProviderReport` → `AccountReport` → `Window`. An
`AccountReport` carries the opaque `Key`, the display `Label`, `Disabled`, the
credential `State`, `OK` (a valid numeric reading in the stored snapshot), the `Windows`,
`CooldownUntil` (latest non-model cooldown) and the `LastSuccessAt`/`ObservedAt`
times. `MetadataObservedAt` is when the credential's disable/identity metadata was
last synced from the discovery path; it is a cache timestamp, never a reading
instant, and the zero value means unknown. `AuthFailed` retains unresolved
historical authentication ownership; only positive recovery clears it. Current
credential risk requires `HasFreshAuthFailure(now)` against the separate
`AuthSignalObservedAt`, never the quota observation or report aggregation clock.
`AttemptedAt`, `AttemptOutcome` and `AttemptReason` come from the independent latest
`quota_refresh_attempts` record, with bounded enums and no raw reason summary. A
latched authentication signal is not the latest attempt. Legacy non-authentication
collection signals carry `CollectionSignalObservedAt` solely as distinct monitoring
failure events; they do not fill `AttemptedAt` or renew quota/authentication evidence.
Only a window with `Scope == "account"`
decides whole-account availability; a model- or group-scoped window running out does
not. A window without a reported percentage is "not reported", never zero.
`IsUnmonitored` excludes an account without quota-reader evidence from channel
availability, monitoring downtime and tightness, not from independently evidenced
credential-invalid notifications. Built-in provider capabilities establish the reader;
an unknown plugin needs a prior numeric snapshot because discovery does not persist
plugin quota advertisements. Such unread plugin credentials remain visible to queries.

`feishu.ReportSource` is implemented by `internal/api/feishu_report_source.go` and
is fully **stored-only**: `ReadReport` never calls the management gateway, so a query
or a tick answers even when CPA is unreachable, and a query never triggers a
management read or a refresh. It reads:

- the safe credential metadata the discovery sync persisted in
  `discovered_resources` (auth index, name, driver and the cached `disabled`/
  `unavailable` flags). That disable flag is a cache from the last sync, so its
  age travels with the account as `MetadataObservedAt` and is never presented as
  live; a disabled credential is reported as disabled, not as an active failure.
  A row discovery already marked `missing` is never read. A row more than
  `CredentialMetadataStaleAfter` (seven days) behind the newest `last_seen_at`
  in the batch is **behind the sweep**: it stays in the stored-only report, it
  is kept out of the current diagnostic block, and the exclusion is logged at
  Warn with the auth indexes and count. It is not deleted on age alone: one
  skipped sweep, a parse failure or a credential the quota scheduler still reads
  cannot prove CPA removed it. When every row is equally old, the sweep itself
  stopped and no row is marked behind. The newest instant is the sweep's own
  progress signal. This rule reads only the stored sweep; no automatic discovery
  schedule advances `last_seen_at`, so between manual discovery runs a removed
  credential may remain in the report, but its age keeps it out of current
  diagnostics. The absolute age bound is computed from the report time, so it
  works even when the whole batch is old.
  The cached `unavailable` flag is never an auth failure: the stored details carry
  no error/status text, so a generic unavailable cannot be told apart from an
  exhausting or transiently broken credential. It reads as `stale` at worst,
  fresh or old, and never manufactures a credential problem;
- the newest stored quota snapshot per credential, which carries the last
  successful reading and its real observed instant;
- the newest safe collection signal from `feishu_account_signals`: an explicit
  `auth_invalid` within `DefaultFreshWindow` is a credential problem, while a stale
  or untimed one neither alerts nor recovers. `transport`/`parse`/`unknown` is never
  a credential problem. A newer numeric success refutes an older auth signal;
  the last numeric snapshot's explicit `needs_reauth` status can also carry dated
  authentication evidence. This layer never re-parses a raw error for a 401;
- the independent latest refresh attempt, read in one batch for the selected
  credentials, so later non-auth failures remain visible even when the auth signal
  intentionally retains an older unresolved rejection;
- correlated cooldowns, and the Ollama module's persisted readings.

A credential's observation time is its stored instant, never "now". A credential
with no stored snapshot reports "never read" and `GeneratedAt` stays zero. When no
stored credential metadata exists at all, the channel is still named from the
persisted observation timeline with a collector-level error, so `monitor_down` can be
timed from its persisted first-seen instant; that states the limit of a fully stored
source rather than reaching for CPA inline.
Only a numeric snapshot establishes `LastSuccessAt`; a successful response without
numeric quota does not invent a valid reading. Re-reading a cached snapshot preserves
its stored observation time instead of advancing last-valid time to the poll instant.

## Denoise rules

Immediate, once per episode:

- a credential is invalid (`credential_invalid`): a fresh stored `auth_invalid`
  collection signal, i.e. an explicit authentication failure within
  `DefaultFreshWindow`;
- every enabled account of a channel is unusable and the earliest recovery is
  later than `ChannelUnavailableAfter` (`channel_unavailable`);
- no valid data for longer than `MonitorDownAfter`, including a collector-level
  failure that yields no account at all (`monitor_down`).

Monitoring downtime uses the newest real success, falling back to the persisted
first applicable observation before any success. Missing and nonpositive timestamps
are absent evidence: a supported unread channel first observed now cannot immediately
prove the default 30-minute threshold, while a persisted established outage remains
eligible after restart. Disabled and unmonitored accounts neither establish nor age
a track; explicitly stopping all monitoring for a channel clears its track and retires
its held monitoring episode without a recovery push. An auth-only failure remains
covered by the credential policy, including when its signal has become stale.
Several monitoring episodes opened in the same nonquiet cycle reuse `KindAlertBatch`
for one card with separate item identities. A single episode keeps its ordinary
notification identity; the startup digest excludes episodes opened by that same tick.

Fresh credential windows with at most 10% remaining and fresh abnormal readings
open persistent
`quota_low`, `quota_exhausted` and `reading_missing` episodes. Scope is explicit;
model/group exhaustion never claims whole-account unavailability. Untimed and
long-stale missing data retains the monitoring grace above. Positive fresh numeric
evidence resolves an episode without a recovery push. A fresh non-auth collection
attempt may appear as a collection-health diagnostic, but does not create a second
missing-data pending incident alongside the monitoring policy. See the card contract below.

A delivered notification has state `sent` and a nonzero `SentAt`; a failed send
is retried with bounded backoff. A row discarded at delivery also closes as `sent`,
but retains zero `SentAt` and is not evidence of an actual notification. Before
enqueueing and before every actual send, including
quiet-end, retry and the simplified fallback, the same per-item eligibility rule
rechecks stored evidence. Quota/authentication/reading-missing claims require a
positive observation time, the appropriate source and a positive, representable
`FreshWindowMS`; missing fields mean unknown, not eligible. Legacy queued identities
may select current stored-report evidence, but pending ownership, notification
creation and report aggregation times cannot supply or renew evidence. Re-reading
the same sample cannot extend an existing narrower freshness bound. Prediction
items retain the quota prediction window's cadence-based bound (2–24 hours), not a
new bound starting at digest creation. The operations prediction item's existing
internal `FreshWindowMS` comes from `quota.PredictionFreshWindow` and stays excluded
from JSON and JSON Schema; the Feishu adapter preserves it unchanged.

Ongoing `monitor_down` uses a separate policy re-evaluation with persisted failure
counts and last-valid/first-seen baseline, not the sample's 15-minute cutoff. Age
expiry alone never resolves a credential or channel incident. A notification with
no eligible risk closes without an actual send or delivery timestamp; explicit
status queries and daily summaries may still explain unknown data. Discarded rows
are not announcement or cooldown evidence and use creation time for bounded
retention, rather than an invented send time. Discarding also releases only the
covered episodes' undelivered notification reservations. A genuinely fresh,
qualified observation can then alert once on the still-open episode; it does not
reset `Since`, declare recovery, or replay an episode actually delivered already.
A still-pending or quiet-queued risk keeps its delivery reservation across restart,
including legacy rows without `NotifiedAt`; policy does not queue a duplicate before
quiet-end promotion. This admission check is reconstructed from the existing outbox.
Historical summary selectors and lower-severity forecasts do not reserve a new
exhaustion alert. Only actually delivered risk rows may acknowledge a missing legacy
reservation; diagnostic-only selectors cannot do so.
Delivery outcomes are reconciled before retention cleanup, and a newer reservation
cannot be cleared by an older send's outcome. Discarded morning rows release the
scheduled-minute reservation only when no other pending or delivered morning owns
it. These rules use existing timestamps and identities, without a new state value
or storage field.

## Integration

The module is wired into the running process:

1. The Feishu tables are classified in `internal/repository/readonly_query.go`
   (`feishu_secrets` hidden; settings, pending items, notifications, channel tracks,
   state and account signals readable) and declared in `TABLE_LIFECYCLES` in
   `internal/repository/lifecycle.go`.
2. The environment overlay is built in `internal/config` from `OMCPA_FEISHU_ENABLED`,
   `OMCPA_FEISHU_FORCE_DISABLED`, `OMCPA_FEISHU_APP_ID`, `OMCPA_FEISHU_APP_SECRET`
   and `OMCPA_FEISHU_CHAT_ID`, and passed to `Handler.BuildFeishuAPI`.
3. `Handler.BuildFeishuReportSource` implements `feishu.ReportSource` over OMC's
   stored data (`internal/api/feishu_report_source.go`); it never triggers a
   refresh. The collection signal is written by the shared
   `persistNormalizedQuotaSnapshot` path, so a manual refresh, an MCP refresh, the
   quota scheduler and a credit redeem all record the same safe outcome.
4. `RegisterFeishuRoutes` is called inside the authenticated `/api/v1` group in
   `internal/api/handler.go` once the API is built: `GET/PUT
   /api/v1/feishu/settings`, `GET /api/v1/feishu/status`, `POST
   /api/v1/feishu/test` and `POST /api/v1/feishu/prediction-test`. The settings route
   calls `Reload` after a save. The prediction route is an explicit operator action
   backed by the injected prediction source and sender; it never opens a second transport.
5. `app.Run` starts the bot under the process lifetime context and `Stop`s it on
   shutdown; neither a demo, a forced-off nor a degraded configuration starts it.
6. The read-only summary `feishu_notifications_status` is offered through the
   capability registry; it carries no secret and no write path.

## Daily prediction digest

The service owns a separate read-only prediction-digest component. It reads stored quota
prediction findings at configured `SummaryTimes` and never calls the Agent, an LLM or an
upstream quota endpoint. The process keeps the component loaded for every non-demo service;
it remains inert while configuration is disabled or degraded and begins evaluating after a
ready settings reload without a restart. Tests inject both its prediction source and sender.


### Quota decision cards

Quota notifications address one configured group ID (`oc_`) or personal Open ID
(`ou_`), retained in `chat_id` / `OMCPA_FEISHU_CHAT_ID`. Existing groups require
confirmation of their exact target before sending or answering queries. Group
recipients permit direct queries and mentions in joined groups; personal recipients
permit only that person's private messages. The OAuth group remains independent.

The default times remain 10:00 / 17:00 in the configured timezone. The earliest
configured slot always queues the daily morning fallback, including healthy or
missing-data results. Later ordinary slots stay silent. New credential windows at
or below 10% remaining, predicted exhaustion before reset, and abnormal readings
open persistent episodes. Untimed and long-stale
readings retain the existing monitoring grace. Forecast and low quota retain their
tracked cycle, while reaching exhaustion triggers one actionable push. Window IDs (or
window kinds when IDs are absent) stay stable across derived reset estimates.
Only exact reset evidence establishes a cycle: a new reading at or after the
previous boundary with a later reset opens a new cycle. Recovery sends nothing;
without a reliable cycle, positive recovery allows a subsequent recurrence to
notify again. Migration 055 persists the exact boundary in pending-item state.
Resolved episodes expire after seven days; active forecast markers retain their
last-seen time so ongoing risks stay deduplicated.
Quiet hours defer ordinary alerts through the existing retry outbox. The morning
fallback and morning prediction digest bypass quiet hours at the configured slot,
including retries; startup and morning summaries share that minute's delivery.
The process must be running during the scheduled minute to send that day's fallback.

Immediate alerts and query cards use a 24-character conclusion and two-line action
blocks with separate labeled evidence-time metadata.
Pushes contain only exhaustion, invalid credentials, unavailable channels and confirmed
monitor outages, at most three blocks; queries also include low quota, up to eight
blocks, with healthy channels folded into one line. Groups use channel plus state,
unique account counts, rounded mean remaining percentage and earliest recovery; one
account's exhausted windows use its latest reset. Missing values never become zero.
Alternative channels must be fresh and healthy; the highest total remaining share
wins, then account count, with unresolved ties and overlong names using fixed copy.
Low quota, forecasts and one-off failures do not trigger immediate pushes. They
contribute grouped omission counts when an actionable card is sent. Digest content
(morning, summary and prediction) retains its existing renderer and schedule.
Migration 057 persists consecutive failures and the last distinct attempt timestamp.
Monitor outages require three failed collection attempts and at least the configured
duration (default 30 minutes). Cached reads do not add attempts; explicit auth or
cooldown responses do not count. One valid reading resets the count and closes the
outage without a recovery push. Concurrent channel outages share one display block.
The console button uses `OMCPA_PUBLIC_URL` plus the deployment base path once;
an absent or invalid address disables it with configuration guidance. No card write
callback or table is emitted. Full and simplified cards have the same content bound.
Delivery is persisted before sending; failure logs attempts and retries from 30
seconds with exponential backoff capped at 30 minutes.
The manual status-test API returns sent=false with reason=queued_for_retry,
attempts and next_attempt_at when delivery failed but its retry was persisted.
The console shows this as queued for retry. Errors remain reserved for requests
that could not establish a persisted delivery outcome.

The settings response exposes tight_remaining_percent as a read-only fixed 10.
Writes that explicitly contain 10 are accepted; conflicting values return 400.
The console omits this field on save, while schedule, quiet hours and monitoring
durations remain editable. Recipients accept a group ID (`oc_`) or personal Open ID
(`ou_`). Migration 056 stores confirmation of the exact recipient: existing groups
remain silent until the console's review and confirmation step. Newly saved web
recipients take effect immediately. Deployment recipients are read-only; invalid
values degrade quota delivery while leaving the dedicated OAuth path independent.
Group recipients permit direct queries and mentions in any joined group; personal
recipients permit only that person's private queries. Query cards retain scoped empty-result
answers and stale-observation notices; an empty abnormal filter means no anomalies.

The earliest summary slot always supplies the morning fallback. Prediction
evaluation runs once per minute from stored data and only windows whose
`lasts_until_reset` is false retain a tracked episode and reach the card;
windows that last, stale readings and undecided rows are never pushed. Each row
names the provider, the credential and the window type, so two windows of one
credential stay distinguishable, then opens with the two numbers ("实际 X% ·
期望 Y%") and the sentence after them states only the exhaustion instant and
that the window will not last to the reset. The dedicated prediction layout
shows at most five windows, with the total in its opening line ("N 个窗口撑不到重置").
Its read-time note names the newest displayed prediction observation only;
individual evidence clocks and historical diagnostics remain separate. It
discloses both payload-bound and row-limit omissions as "另有 N 项，见控制台".
Non-prediction morning alerts remain visible after those rows; if all prediction
rows are removed, their omitted count is cleared without suppressing other alerts.
Immediately before every attempt, including quiet-end and retry, the digest
re-reads the stored roster and drops findings for credentials that are disabled,
removed, lack a window or have stale evidence. Forecast-only events stay silent
outside the morning digest. Historical open items are bounded diagnostic context,
never a source of current risk. Explicit manual prediction sends apply the same
findings filter, deliver only their new outbox row and never claim a scheduled
minute. No finding returns `no_findings`.

### Evidence clocks and diagnostics

Each visible risk identifies its own clock: quota observation, authentication
signal, latest collection attempt, channel availability evidence, or last valid
monitoring data. Grouped blocks explicitly label the earliest included evidence;
unknown member timestamps remain unknown. Hidden rows do not choose a card-wide
observation footer, and the newest aggregate report time cannot stand in for any
row's evidence. A healthy folded quota summary dates only its qualifying accounts.

Stale or untimed numeric data and historical authentication incidents share one
bounded diagnostic block: at most two current credentials, one line each, named
by the account display name (the discovery file extension is dropped) and dated
by the single most relevant clock — the authentication signal when present,
otherwise the quota observation. Last-success and attempt clocks stay in the
console; credentials whose presence metadata is more than seven days old never
enter the block, and open history is one anonymous count line instead of a
current row. The age bound is absolute, not relative to the newest sweep, so a
stopped discovery run cannot let a months-old row occupy a current diagnostic
slot. Old authentication
evidence means current authentication is unknown, not a new login instruction. A
fresh explicit rejection stays in the current risk section instead of duplicating
into diagnostics, and even with old numeric quota it cannot borrow that quota's
remaining share or reset as current facts. No-data/stale-only views cannot claim
healthy. Full and simplified cards preserve the same source and age semantics.

Button fields were checked against the [official JSON 2.0 button reference](https://open.feishu.cn/document/feishu-cards/card-json-v2-components/interactive-components/button).
Design evidence is `pre_render_design`: structure, missing data, single-recipient
SDK requests and retries use synthetic Go tests and fake transports. Real Feishu
mobile rendering remains `pending_real_render`; local evidence cannot certify it.

## Remaining risk

- The stored auth-invalid evidence depends on the classifier. A quota snapshot
  records only `status`, where an upstream auth failure is indistinguishable from a
  transport error; the `feishu_account_signals` row stores the classified outcome,
  and an auth failure is recognized solely from the quota layer's already-tested
  `needs_reauth` recommendation rather than a second 401 string check. A failure the
  classifier cannot place is stored as transport/unknown, never as auth, and a
  transport failure never overwrites a known auth failure. A live auth failure is
  therefore a `credential_invalid` only while its signal is within
  `DefaultFreshWindow`; once older it reads as `stale` and does not re-alert, and no
  success can be inferred. This is the documented limit of a fully stored source: it
  never fabricates a fresh auth failure and never reaches for CPA to confirm one.
- The web settings panel is implemented and wired: `web/src/components/feishu/FeishuSettingsPanel.tsx`
  is mounted by `web/src/pages/OmcSettingsPage.tsx` and calls the settings, status and
  test routes. The synthetic desktop and phone browser scenario passed, including
  phone-width overflow and confirmation-before-send.
- The live Feishu long connection and the real card rendering have not been
  exercised against Feishu from this worktree.
- `feishu_pending_items` and `feishu_notifications` are bounded by the module's own
  reconciliation and prune rather than by an age horizon: a long-lived resolved item
  is cleaned up by the module, not by `RunLifecycle`.
- None of this is production-verified.
