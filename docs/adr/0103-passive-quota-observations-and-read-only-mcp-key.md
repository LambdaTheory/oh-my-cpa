# ADR 0103: Passive quota observations and a read-only MCP key

- Status: Accepted
- Date: 2026-10-13
- Amends: ADR 0065's quota refresh boundary and ADR 0070's MCP credential model.

## Context

Upstreams publish rate-limit state on every response, not only on a quota refresh: Codex
sends `x-codex-{primary,secondary}-used-percent`, `-window-minutes` and `-reset-at`, and
Anthropic sends `anthropic-ratelimit-unified-{5h,7d}-utilization`, `-reset` and `-status`.
Oh My CPA already keeps the diagnostic part of those headers on the request record
(`usage_events.response_headers`, migrations/038), but only as an opaque blob that is
discarded with the request-record retention, and it never reads the quota fields out of it.

The normalized `quota_snapshots` table cannot stand in for this. It is overwritten by each
refresh and answers "what does this credential read now", not "what did its five-hour window
read at 09:00, and did the percentage move or the reset instant change". A refresh may not
run for hours, and its historical evidence is deliberately not durable.

At the same time, the only external credential is the CPA management key, which is
administrator-equivalent and also signs in to the console. An agent trusted with read-only
fleet analytics therefore receives full authority, because no lesser credential exists.

## Decision

Parse the provider rate-limit headers into a new append-only table, `quota_observations`
(migrations/041), inside the same transaction that stores the request record
(`repository.CommitUsageDecoded`). A row is written only for a window whose length,
percentage and reset are present and in range; a malformed or missing reading is dropped,
never fabricated, and a credential-less event writes nothing. The parser is a pure function
in `internal/quota/passive.go` so its rules are testable without a database.

Rows deduplicate per credential and window: the newest row for an `(auth_index, window_kind)`
pair is probed through the `(auth_index, window_kind, observed_at_ms DESC)` index, and a write
happens only when the percentage or reset instant changed, or when the heartbeat interval
(fifteen minutes) has passed since the newest row. A batch is processed oldest-first so a
later event cannot mask an earlier one. Steady-state volume is then one row per credential and
window per fifteen minutes, not one per request.

Retained request records that predate the stream are backfilled through a watermark keyed by
name in `usage_aggregation_checkpoints`; the maintenance loop sweeps a bounded id range at
startup and on each fold tick until it reaches the live tail. Observations roll at a fixed
365-day horizon through the existing lifecycle engine (`TABLE_LIFECYCLES`), independent of the
operator's request-record retention, because an observation is the only record of a window
once its request has rolled.

The read path is one new read-only capability, `quota_observations_list`, with an optional
credential, provider, window, time-window and limit filter. It refuses a limit above 1000 and
returns `tool_result_too_large` rather than silently truncating a matching history.

An optional `OMCPA_MCP_READONLY_KEY` is accepted as a second MCP bearer credential. It
reaches only capabilities declared `read`, with a principal distinct from the operator's, and
cannot sign in, approve an operation, or run a write or destructive tool. A configured key
that is shorter than sixteen characters or equal to the management key fails start-up, so
"optional but set" always means what it says. Read handlers mask the local part of an
email-shaped account label (credential and provider display names) for read-only callers;
the console and the management key are unchanged.

## Consequences

`quota_snapshots` stays the current normalized reading; `quota_observations` is append-only
history and the two are not conflated. A later read can reconstruct how a window moved across
a week without a refresh having run, and the evidence survives request-record retention for a
year.

The stream adds one index probe and one insert per window per request with headers, and no
write for a request without them. The backfill is bounded per tick and idempotent through
`UNIQUE(event_key, window_kind)`.

The read-only key narrows, never widens, access: it is a second credential for the same
endpoint, and a caller that needs a write still uses the management key. Masking is a
tool-output projection, not a storage change, so it cannot leak through the console or the
database. Both the new tool and the read-only key are recorded in
`docs/agent-capabilities.md`.
