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

Return exactly one conclusion per credential. Count Codex credits once and each
Claude grant once when its `clears` intersects the credential's standard windows.
With fresh, valid remaining entitlements and known expiry, apply this priority:

1. Any valid standard window at or above 90% used yields `keep_for_emergency`,
   including when another window would favor spending an expiring entitlement.
2. Otherwise all standard windows must be readable and below 90%; any applicable
   entitlement expiring within the configurable 72-hour default horizon yields
   `use_before_expiry`.
3. An explicitly observed zero remaining balance or fully expired grants yields
   `no_action`; a readable positive balance entirely outside the horizon also
   yields `no_action`.
4. Missing, stale or invalid key evidence that prevents the above decision yields
   `unknown`, with reason codes and nullable values. A missing quota window blocks
   spending but does not negate a known emergency in another standard window.

The window identity and percentage identify the highest valid standard reading
(window ID breaks ties); they are evidence, not a second conclusion. The optional
window-kind filter selects credentials, and never hides another standard window
from their decision. `cooldown_active` reads the existing correlated local cooldown
record: true or false when present, null when absent. It is evidence only and adds
no upstream request or change to verdict priority.

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
version returns `announcements: []` on every credential item and reads no
announcements; it adds no scraper, feed, storage or fetch hook. Future versions
may append announcement entries and additive entry fields while preserving this
array's shape and the existing advice fields.
The semantic distinction leaves room for that source without changing the four
verdicts or treating an announcement as a redemption authorization.

## Consequences

The operation reads only OMC metadata, snapshots and observations, with at most 100
rows and 32 KiB returned and every removed row counted in `omitted`. Existing quota
prediction fields and verdicts remain unchanged. Supplemental collection failures
that were not recorded separately cannot be distinguished from their retained
snapshot; advice is bounded by the stored evidence and its freshness, not a live
provider guarantee.
