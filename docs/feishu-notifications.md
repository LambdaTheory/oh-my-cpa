# Feishu Notifications (lane C, first batch)

Lane C of the OMC first batch absorbs the retired CPA-buddy (cpab) Feishu bot as
an OMC capability: it reuses the bot long connection, answers read-only queries
from OMC's newest quota data, and denoises notifications so a problem is announced
once and recovery is not nagged. It does **not** call a language model, hold an
Agent session, or trigger an upstream quota refresh.

This document is the lane-C contract: the interface the code exposes, the
migration it adds, the integration wiring the owner still owes, the new dependency,
and what remains unverified. The approved scope is in
[docs/omc-first-batch-spec.md](omc-first-batch-spec.md) §4.

## Code map

| Path | Responsibility |
| --- | --- |
| `internal/feishu/` | The bot: report DTO, policy, renderer, sender seam, dedup, service lifecycle, websocket source. |
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
  `feishu.NewPlainReportRenderer(loc)` is the production hook a designer later
  replaces.
- `SenderFactory func(Resolved) Sender` — defaults to the official SDK sender.
- `SourceFactory func(Resolved) EventSource` — defaults to the Lark websocket.
- `Now func() time.Time`, `EvalInterval time.Duration`.

`ReportRenderer` is the card hook: the shipped `PlainReportRenderer` emits a
header plus markdown elements in schema 2.0, and `CardSimple` is the degraded card
the sender retries when the full card is rejected. No layout is invented here; the
visual design is a later designer's change to that interface.

Lifecycle methods:

- `Start(ctx) error` — launches the long connection and the classification loop; a
  no-op when already started or when the configuration is not ready.
- `Stop()` — cancels the connection and the loop, waits for both, and cancels
  in-flight query tasks. Idempotent.
- `Reload(ctx, web Config, overlay Overlay)` — applies new settings without a
  redeploy. Only a real connection-identity change (app id, app secret, or the
  enabled/ready state) reconnects; the target group is **not** connection identity,
  so changing it applies to the next send on the existing connection, and a
  policy-only change (quiet hours, digest times, thresholds) applies on the next
  pass. It never resends the one-time startup summary.
- `StatusSnapshot(ctx) Status` — the read-only console state (enabled, ready,
  degraded, source, forced-off, connected, open items, pending/queued sends, last
  summary, last error).
- `SendTest(ctx) error` — sends one status card to the target group; called only on
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
instant, and the zero value means unknown. `AuthFailed` reports that the newest
unrefuted collection evidence is an authentication failure; only a valid reading
observed after the auth failure clears it. Only a window with `Scope == "account"`
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
  The cached `unavailable` flag is never an auth failure: the stored details carry
  no error/status text, so a generic unavailable cannot be told apart from an
  exhausting or transiently broken credential. It reads as `stale` at worst,
  fresh or old, and never manufactures a credential problem;
- the newest stored quota snapshot per credential, which carries the last
  successful reading and its real observed instant;
- the newest safe collection signal from `feishu_account_signals`, which is the
  only auth-failure evidence: `auth_invalid` observed within `DefaultFreshWindow`
  is a credential problem, a stale `auth_invalid` neither alerts nor recovers, and
  `transport`/`parse`/`unknown` is never a credential problem. The signal records
  the explicit `needs_reauth` outcome the quota layer already computes, so this
  layer never re-parses a raw error for a 401;
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

Not immediate: a transient error, an ordinary threshold, a single account's 429, a
model- or group-scoped exhaustion, and every recovery. An open problem is
announced once (`ItemID`/`PendingItem.NotifiedAt`); it re-alerts only after it
resolves and recurs. A credential problem resolves only on positive evidence — a
valid reading for that credential observed after the auth failure, with no unrefuted
auth failure remaining; a credential that merely stops reading as invalid (unknown,
stale, or absent from the report) is not treated as recovered, so the item is
retained rather than falsely closed. Quiet hours (default 23:00–08:00) persist immediate alerts as
queued notifications; at the quiet end the still-open ones are merged into a single
persisted notification (`KindAlertBatch`) and delivered as one card, dropping any
whose problem recovered, so a fully recovered batch sends nothing. The batch keeps
its identity across a reload or restart, so the same persisted queue cannot be sent
twice, and a failed send is retried rather than lost. A scheduled digest (default
10:00 and 17:00) is sent only when there are open items or a channel is tight (best
usable account-window remaining below `TightRemainingPercent`). Tightness is judged
only from accounts whose newest reading is still fresh, so a stale sample can neither
warn that a channel is nearly out nor read a maxed window as "0 remaining"; when no
report has been produced, `has` is false and no tight channel is added, but real open
items are still summarized. The one-time startup summary is persisted across process
restarts and settings reloads.

Delivery is outbox-based: a notification is persisted before sending and marked
sent only after a successful send, so a failed send is retried with a bounded
backoff rather than lost.

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
   /api/v1/feishu/settings`, `GET /api/v1/feishu/status` and `POST
   /api/v1/feishu/test`. The settings route calls `Reload` after a save.
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

At a scheduled minute, one or more `will_exhaust` or `under_used` windows are combined
into one card titled `OMC 额度预测 · HH:MM`. The service records that minute in the existing
summary timestamp, so `maybeScheduledSummary` cannot queue a second message. With no
finding, prediction is silent and the ordinary digest runs unchanged. Quiet hours defer
delivery through the same outbox; failed sends use the same bounded retry schedule.

Each row shows the credential label, window, current used percentage, projected percentage
at reset and verdict. Projected cost and tokens appear only when priced usage is sufficient.
The footer states the newest data time and that the result is a projection, not a guarantee.

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
  test routes. Its browser behaviour has not been exercised from this worktree.
- The live Feishu long connection and the real card rendering have not been
  exercised against Feishu from this worktree.
- `feishu_pending_items` and `feishu_notifications` are bounded by the module's own
  reconciliation and prune rather than by an age horizon: a long-lived resolved item
  is cleaned up by the module, not by `RunLifecycle`.
- None of this is production-verified.
