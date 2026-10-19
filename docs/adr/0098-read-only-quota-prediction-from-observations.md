# ADR 0098: Read-only quota prediction from observations

- Status: Accepted
- Date: 2026-10-19

## Context

OMC already stores normalized quota snapshots, passive and active quota observations,
and request-time usage and price evidence. Operators and read-only MCP clients need to
ask whether a window is likely to exhaust before reset or remain materially under-used.
The answer must not trigger an upstream refresh, duplicate the capacity API, or become
an automatic routing or credential-management policy.

## Decision

Add `quota_prediction` as a low-risk read for Agent and MCP. For each current window,
derive the cycle with `WindowCycleRange`, reject stale, incomplete, reset or insufficient
history as explicit unknown evidence, and estimate the current-cycle burn rate from at
least two passive or active observations. A positive trend projects used percentage and
an exhaustion instant at reset; a non-positive trend may establish under-use only after
enough of the cycle has elapsed. Missing evidence remains null with reason codes and is
never represented as zero.

Join recorded usage through `QueryCredentialWindowUsage` for standard windows and
`QueryCredentialModelWindowUsage` for model or group windows. Project tokens and cost
only when used percentage is at least five, traffic exists and at least 95 percent of
requests were priced. These values are reset-time projections, not capacity estimates.

The console and built-in Agent retain the credential label and auth index. A read-only
MCP principal receives the existing masked account label and no auth index. The result
is capped at 100 rows and 32 KiB, with `omitted` reporting truncation.

The Feishu service owns a separate prediction-digest component evaluated at the existing
configured summary times. When it queues a `will_exhaust` or `under_used` card, it claims
that local minute through the existing persisted summary timestamp, so the legacy digest
does not also send. With no prediction finding, the legacy scheduled summary proceeds
unchanged. The card uses the existing sender, outbox, retry, target chat, timezone and
quiet-hour behavior; no table or migration is added.

## Consequences

Prediction quality is bounded by observation freshness and local request coverage. It may
return unknown for a window that upstream can answer but OMC has not observed often enough,
and cost may be omitted while the percentage projection remains valid. The capability and
digest perform no upstream quota probe, LLM call, route change, disablement or model switch.
