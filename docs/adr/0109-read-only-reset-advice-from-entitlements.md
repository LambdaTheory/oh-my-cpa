# ADR 0109: Read-only reset advice from entitlements

- Status: Accepted
- Date: 2026-10-20

## Context

Codex reset credits and Claude reset grants are already stored beside quota windows,
but operators need to distinguish expiring entitlements from reserves worth keeping
for recovery. Provider announcements may later describe grant campaigns and expiry
rules, while an individual account's observed balance remains separate evidence.
The advice must remain available to read-only clients without becoming a redemption
command or triggering upstream collection.

## Decision

Expose `reset_advice` through the shared capability registry as a low-risk read. The
pure quota evaluator consumes stored counts, grant scopes, expiry times and current
window percentages. Operations owns persistence reads, filtering, bounded output and
identity projection; Agent and MCP contain no feature-specific behavior. Read-only
MCP rejects auth-index filters and omits auth indexes from results while using the
existing masked credential labels. No HTTP route, migration or write path is added.

At 90% used or above, a remaining entitlement with known expiry yields
`keep_for_emergency`. Below that threshold, an expiry within the configurable
72-hour default horizon yields `use_before_expiry`; a later expiry yields
`no_action`. An explicitly observed zero remaining balance or fully expired grants
also yields `no_action`. Missing grants, invalid counts, unknown expiry or scope,
and missing/stale quota evidence yield `unknown` with reason codes and nullable
values. Claude counts cover only windows explicitly named in `clears`; one grant
may occur in several window rows, so counts are not additive across rows.

Evidence is `fact`, `projection` or `unknown`. A known zero remaining balance is a
fact; positive-balance advice is a projection of usefulness from current evidence,
not proof that redemption is permitted. Unknown values never become zero. Reset
snapshots have a two-hour freshness limit, matching the idle collection cadence.
Quota windows use the existing window-aware freshness bound. Passive headers may
replace quota readings but cannot refresh the independently observed entitlement.
This conservative policy can withhold advice while the provider still has a usable
grant; retaining that uncertainty is preferable to recommending a spent entitlement.

## Announcement source extension

A future announcement source should carry provider, publication/effective/expiry
times, provenance and explicit applicability conditions. A published campaign is a
fact about the announcement, not evidence that a specific credential received a
grant. Applying it to an account requires separately observed eligibility and balance;
inferences remain projections and unresolved applicability remains unknown. This
version reads no announcements and adds no scraper, feed, storage or fetch hook.
The semantic distinction leaves room for that source without changing the four
verdicts or treating an announcement as a redemption authorization.

## Consequences

The operation reads only OMC metadata, snapshots and observations, with at most 100
rows and 32 KiB returned and every removed row counted in `omitted`. Existing quota
prediction fields and verdicts remain unchanged. Supplemental collection failures
that were not recorded separately cannot be distinguished from their retained
snapshot; advice is bounded by the stored evidence and its freshness, not a live
provider guarantee.
