# ADR 0110: Reset announcement evidence and source admission

- Status: Accepted
- Date: 2026-10-20

## Context

Operators need to retain public reset announcements before automatic sources exist.
The read-only reset-advice decision is recorded separately in ADR 0109, introduced by
[PR #52](https://github.com/LambdaTheory/oh-my-cpa/pull/52). Ingestion must evolve without
changing the stored evidence shape or using source identity as an advice rule.

## Decision

Store announcements in an independent append-only-by-interface table, introduced by
migration 049. The record carries provider, source, optional expected reset instant,
all/model/credential scope with target, confidence, original text, registration time
and creation time. Scope targets are explicit so future readers cannot accidentally
apply a model- or credential-specific notice provider-wide.

Only the console session can create or list records through typed facade endpoints.
The raw table is hidden from the SQL capability. This release has no update, delete,
reject, Agent/MCP registration, CPA mutation or automatic collection path. Invalid input
is a parameter error; no rejected row is stored. Active/expired are read-time projections
using the expected reset boundary; untimed records remain active and history is retained.

## Source extension point

The database and response type admit manual, status_rss and x_api. The create boundary
currently accepts only manual, including direct repository calls. A future source
adapter must be admitted explicitly and normalize into the same record; it must not
add source-dependent advice rules. Reserved values alone grant no collection authority.

Confidence is operator-stated evidence strength. Unverified or untimed announcements
must remain evidence only; only timed confirmed/probable evidence may contribute to
future advice integration. An announcement is never an observed quota reset and must
not mutate quota_snapshots or alter existing prediction conclusions.

## Consequences

The store remains usable offline and independent of upstream availability. Untimed
records have no invented expiry, so future curation requires a deliberate contract
change. Free text is bounded to 4,000 Unicode characters and rendered as plain text;
operators enter public announcements, not credentials. Pagination remains bounded to
100 rows and shares one clock with expiration filtering.
