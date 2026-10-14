# ADR 0095: Per-account quota refresh scheduling and 429 backoff

- Status: Accepted
- Date: 2026-10-14
- Amends: ADR 0065's quota refresh boundary and ADR 0094's observation timeline.

## Context

Quota was only ever read when a person pressed Refresh or an agent called
`quota_refresh`. Nothing read a credential on its own, so a quiet credential's
reading went stale, and the capacity estimate - which uses a snapshot's cycle and
needs a reading at least five percent into a cycle to derive anything - had no
previous-cycle reference to fall back on when a cycle ended between refreshes.

Every refresh path also treated an upstream 429 as a generic failure. `SafeApiCall`
returned the response and a caller turned a non-2xx into a plain error, so a
provider that asked the deployment to slow down was retried on the next manual
press, the next agent call and, after this change, the next scheduler tick, in a
loop the upstream had explicitly refused.

The passive observation stream (ADR 0094) already reads a credential's true window
from its own request headers. It is fresher than any refresh for a busy credential,
but it does not record a model-scoped window, the plan, or the closed cycles the
capacity estimate needs.

## Decision

Run one background loop, `internal/quota.Scheduler`, started by the application
under the process lifetime context. Every minute it evaluates each refreshable,
enabled credential and reads it on a per-account cadence derived from how recently
it served a request and the highest used share among its windows:

- a credential with traffic in the last fifteen minutes and a window at or above
  eighty percent is watched (five minutes);
- one with traffic in the last fifteen minutes is active (fifteen minutes);
- one seen within two hours is normal (thirty minutes);
- a quiet one is idle (two hours).

Two one-shot rules sit above the cadence. A window whose reset has just passed is
read once, so the new cycle starts from a known point. A window within fifteen
minutes of its reset that has traffic this cycle but no recorded reading at or
above five percent is read so the next cycle has a previous-cycle figure.

The "recent request" test is one grouped read over `usage_events` per window, not
one query per credential. A single provider is serialized to one active read at a
time and a global budget (default four) bounds the fleet; a credential is never
read twice inside a configurable floor (default ten minutes).

When a credential is Claude or Codex and its standard window already has a passive
observation newer than its snapshot, the interval is widened to a floor (default
sixty minutes): the passive stream keeps that window current, so the active read
exists only for the model-scoped windows and the plan.

`SafeApiCall` turns an upstream 429 into a typed `quota.RateLimitError` carrying the
`Retry-After` header CPA relays from the upstream (delay-seconds or an HTTP date,
bounded to a day), or an exponential wait (five minutes doubling to six hours) when
none is present. `RefreshCredentialQuota` propagates it. The scheduler, the manual
`POST /management/quota/refresh` endpoint and the MCP `quota_refresh` tool all
share one in-memory registry: a 429 parks the credential and its provider, and
while parked each path answers `rate_limited_backoff` with a retry time instead of
probing again. A successful read clears the park.

Every successful refresh records each window with a stated period and used share
into the observation timeline as `source='active'` (migration 042), under a
synthetic event key that includes its scope and model. The passive stream keeps
`source='passive'`. The two share one table and one read tool, which now exposes
`source`, `scope` and `model` and can filter by source.

At read time, a standard five-hour or weekly window whose latest passive
observation is newer than its snapshot is overlaid with the header reading and
marked with its instant and source. The capacity estimate is left on the snapshot:
it is derived from the snapshot's cycle, and mixing a second reading into it would
be two observations in one estimate.

## Consequences

The scheduler is best-effort, like the pricing and release loops: losing it leaves
readings stale, never breaks request capture or the HTTP server. It performs
outbound probes, so it is disabled in demo mode and by
`OMCPA_QUOTA_SCHEDULER_ENABLED=false`.

The backoff is in memory only. A restart clears it, and the worst a cleared state
can do is probe a rate-limited upstream once more before it is parked again - an
acceptable cost against holding a second piece of durable state that a single
replica can reconstruct.

The interval floor can make the watched tier's five minutes unreachable at its
default: the floor is the hard "at least this long between two reads" rule, and a
deployment that wants the watched cadence lowers `OMCPA_QUOTA_CREDENTIAL_FLOOR`.
Both are configuration, not code.

`quota_snapshots` remains the current normalized reading and the basis of the
capacity estimate; `quota_observations` is append-only history from two sources.
The overlay is a read-time projection, so it cannot leak into the snapshot or the
estimate.
