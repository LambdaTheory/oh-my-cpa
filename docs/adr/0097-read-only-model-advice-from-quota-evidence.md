# ADR 0097: Read-only model advice from quota evidence

- Status: Accepted
- Date: 2026-10-16

## Context

An external orchestrating agent needs to compare client-requested model names
without receiving credential identities or triggering an upstream quota read.
The pricing snapshot retains provider membership but not every provider-specific
route or credential model list. An alias can have several upstream targets, so
using its collapsed pricing identity would produce false certainty.

## Decision

Add `model_advice` as a low-risk read on Agent and MCP. Use the existing Model
Square projection for provider/upstream routes, and CPA's management credential
directory for explicit model membership and exclusions. These CPA reads are not
quota probes. Do not extend pricing persistence or add a migration merely to
avoid a directory read. If the directory cannot be read, or a call point has
missing provenance or several upstream targets, return `unknown` for that
model instead of guessing.

Apply the existing quota evaluator to windows relevant to each upstream model:
standard windows apply broadly, model scope matches exactly, and group scope
uses the reviewed capacity family. Cooling, parked and exhausted credentials
cannot contribute usable capacity. Choose the best fresh, unblocked credential,
not the worst member of a pool. Missing, stale, idle and unpublished readings
remain unknown evidence. A newer classified collection failure prevents the retained
successful snapshot from claiming fresh capacity. The result is an advisory ordering, not a CPA routing
decision. It contains model names, bounded reasons, counts and safe window
summaries, never credential identifiers or account data.
`recommended` names the first `ok` or `caution` model. When only `avoid` or
`unknown` models remain, it is null; a ranked blocked model is not a recommendation.

## Consequences

The capability may report many unknown models when CPA does not expose model
membership or when routes fan out to different upstream targets. It makes no
upstream quota call and does not alter the refresh cadence. The existing
32 KiB capability result limit is respected by ranking before truncation; a
default call returns at most fifty model rows.
