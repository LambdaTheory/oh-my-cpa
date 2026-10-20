# ADR 0107: Batch quota refresh reports per-credential outcomes

- Status: Accepted
- Date: 2026-10-20
- Amends: ADR 0104's manual batch response behavior.

## Context

The console refreshes several credentials through one
`POST /management/quota/refresh` request. The endpoint used to replace the whole
batch response with HTTP 429 when any one upstream returned a rate limit. Successful
readings had already been persisted, but the browser could not distinguish them from
the rate-limited credential and reported every target in that request as failed.

## Decision

A batch refresh returns HTTP 200 after the request itself was accepted and reports one
allowlisted `results` entry per requested credential. Each entry is `success`,
`rate_limited`, or `failed`; a rate-limited entry includes `retry_at_ms` and
`retry_after_ms`, and a successful entry may carry its normalized quota. The existing
`quotas` collection remains the ordered set of returned normalized readings so current
readers can continue merging fresh and last-good data without receiving credential
material.

The batch starts with the existing global concurrency of four. Once the first
rate-limited result settles, already-running probes may finish, but the dispatcher sends
the remaining targets one at a time. Manual probes and the background scheduler acquire
the same provider lock held by the shared in-memory quota backoff registry. A parked
credential is therefore reported with its retry time without another upstream request,
and a manual batch cannot overlap the scheduler for that provider.

The console renders one persistent report grouped as succeeded, rate limited, failed,
and skipped. Rate limits are operational waits rather than failures; their copy names the
retry time and never exposes the internal `rate_limited_backoff` code. A failed target
keeps its own redacted failure reason.

## Consequences

An upstream 429 no longer discards successful sibling results or changes an otherwise
valid batch request into an HTTP failure. The single-credential endpoint and the Agent/MCP
`quota_refresh` capability retain their existing typed 429 behavior because each call has
only one target.

The provider lock is intentionally stronger than a credential-only lock. It preserves
ADR 0104's provider serialization and gives manual and background probes one coordination
boundary instead of two states that can race.
