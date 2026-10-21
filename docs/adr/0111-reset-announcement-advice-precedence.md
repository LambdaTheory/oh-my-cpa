# ADR 0111: Announcement precedence in reset advice

- Status: Accepted; supersedes ADR 0109's emergency priority when a qualifying announcement applies
- Date: 2026-10-21

## Context

The operator can register public reset notices (ADR 0110), while local entitlement
advice (ADR 0109) reserves a reset for recovery when quota use reaches 90 percent.
A credible upcoming reset changes the usefulness of retaining that entitlement.
An announcement still cannot prove that an individual credential received a grant.

## Decision

Match active announcements by exact provider and explicit scope. Credential scope
matches the stored auth index internally; model scope requires an exact persisted
model-scope quota window. Do not infer membership from provider or model-family names.

A confirmed or probable announcement whose expected reset is strictly in the future
and at most the configured advice horizon away changes the verdict to
use_before_expiry only when local advice is known, applicable remaining entitlements
are positive, and the stored credential is enabled and available. This also overrides
keep_for_emergency. The resulting evidence remains projection. Unknown/zero local
capacity retains the local verdict with notice_without_local_capacity. Unverified,
untimed and outside-horizon notices remain evidence only; expired notices are excluded.

Preserve the original local verdict, evidence, reasons and reset boundary under
local_evidence, alongside the original top-level counts and observation timestamps.
Each announcement names its source and confidence, whether it influences advice,
and whether its reset time or recommendation conflicts with local evidence. Neither
source replaces a measured reading. Source transport does not imply confidence.

Console quota overview/detail, Agent and MCP share this operation. The console uses
handwritten DTOs on existing routes; capability output retains its row and byte
bounds. Announcement projections omit raw text and credential targets, so read-only
MCP never acquires an auth index through an announcement. Advice does not change
quota_prediction, routing, credential state, redemption eligibility or CPA state.

## Consequences

The quota workspace can show a concise suggestion and explain conflicting sources
without presenting a reset as already observed. Model notices without an exact
stored window remain unmatched, even if the credential might support that model.
Local eligibility and freshness remain prerequisites; an announcement cannot repair
missing or stale entitlement evidence. ADRs 0109 and 0110 remain historical records
of their original implementations.
