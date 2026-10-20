# ADR 0104: Per-account quota refresh scheduling and 429 backoff

- Status: Accepted; provider-wide probe serialization was superseded by ADR 0107's per-credential coordination.
- Date: 2026-10-14
- Amends: ADR 0065's quota refresh boundary and ADR 0103's observation timeline.

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

The passive observation stream (ADR 0103) already reads a credential's true window
from its own request headers. It is fresher than any refresh for a busy credential,
but it does not record a model-scoped window, the plan, or the closed cycles the
capacity estimate needs.

## Decision

The application runs exactly one quota refresh loop under the process lifetime context.
`OMCPA_QUOTA_SCHEDULER_MODE=interval` is the default and runs ADR 0094's stored-preference
policy. `OMCPA_QUOTA_SCHEDULER_MODE=adaptive` runs `internal/quota.Scheduler`; it does not
read the `omc_quota_auto_refresh` preference. Every minute the adaptive mode evaluates each
refreshable, enabled credential and reads it on a per-account cadence derived from how recently
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
read twice inside its provider's credential floor. The floor defaults to ten
minutes for Claude, five minutes for Codex and Antigravity, and the shared
`OMCPA_QUOTA_CREDENTIAL_FLOOR` (ten minutes) for every other provider.
`OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER` overrides the named defaults as a
`provider=duration` list.

When a credential is Claude or Codex and its standard window already has a passive
observation newer than its snapshot, the interval is widened to a floor (default
sixty minutes): the passive stream keeps that window current, so the active read
exists only for the model-scoped windows and the plan.

`SafeApiCall` turns an upstream 429 into a typed `quota.RateLimitError` carrying the
`Retry-After` header CPA relays from the upstream (delay-seconds or an HTTP date,
bounded to a day), or an exponential wait (five minutes doubling to six hours) when
none is present. `RefreshCredentialQuota` propagates it. Both background modes, the manual
`POST /management/quota/refresh` endpoint and the MCP `quota_refresh` tool all
share one in-memory registry: a 429 parks the credential and its provider, and
while parked each path answers `rate_limited_backoff` with a retry time instead of
probing again. Credential warmup's consuming requests also respect the park and wait without
probing. A successful quota read clears its own credential park; a provider park is released
only by the read whose 429 created it, so a sibling whose attempt was already in flight cannot
erase a fresh refusal.

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

Both modes are best-effort, like the pricing and release loops: losing the selected loop leaves
readings stale, never breaks request capture or the HTTP server. They perform outbound probes,
so neither starts in demo mode or when disabled by
`OMCPA_QUOTA_SCHEDULER_ENABLED=false`.

The backoff is in memory only. A restart clears it, and the worst a cleared state
can do is probe a rate-limited upstream once more before it is parked again - an
acceptable cost against holding a second piece of durable state that a single
replica can reconstruct.

The credential floor is the hard "at least this long between two reads" rule, so a
floor above the watched tier's five minutes makes that cadence unreachable. The
floor is therefore resolved per provider. Codex and Antigravity default to five
minutes, so their watched tier actually reaches its five-minute cadence; Claude
keeps the shared ten minutes, so its watched tier degrades to a ten-minute read;
and a provider the map does not name falls back to
`OMCPA_QUOTA_CREDENTIAL_FLOOR` (ten minutes). `OMCPA_QUOTA_CREDENTIAL_FLOOR_BY_PROVIDER`
overrides the named defaults, and a provider it does not name keeps its default.
Both are configuration, not code.

`quota_snapshots` remains the current normalized reading and the basis of the
capacity estimate; `quota_observations` is append-only history from two sources.
The overlay is a read-time projection, so it cannot leak into the snapshot or the
estimate.
