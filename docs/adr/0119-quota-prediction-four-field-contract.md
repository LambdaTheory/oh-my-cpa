# ADR 0119: Quota prediction answers with four fields, not a verdict vocabulary

- Status: Accepted
- Date: 2026-11-05
- Amends: ADR 0098's result vocabulary and ADR 0113's evidence reporting, without
  changing their observation sampling, capability boundary, privacy model or
  result bounds.

## Context

ADR 0098 projected each quota window and returned a verdict
(`will_exhaust` / `under_used` / `on_track` / `unknown`) with evidence, reason
codes, a burn rate and several reverse estimates (projected percentage, tokens
and cost at reset). Three readers - the console, the Feishu card and the
capability/MCP result - described the same window differently, and a caller
that only wanted to know "does this last until the reset, and if not when does it
run out" had to interpret a reason code and trust a projection.

Two things it could not answer directly: how much of the window should have been
used by the reading instant (the linear expectation), and whether the window will
last to its reset as a plain three-state fact. Feishu also pushed forecasts for
windows that would last, and callers had to filter `under_used` by hand.

## Decision

Every prediction item answers four questions with the same names and meaning on
all three surfaces:

- `used_percent` - what the reading says.
- `expected_used_percent` - the linear share of the window that had elapsed at
  the reading's own instant, one decimal place. Null when the boundary is
  unknown, the cycle has ended or the reading is stale; missing is never zero.
- `exhaustion_eta_ms` - the instant the trend reaches 100%. Equal to the reading
  instant when the window already is at 100%.
- `lasts_until_reset` - true, false, or null when the evidence cannot decide.

The decision order is fixed: disabled/removed credentials and credentials with no
window are excluded before prediction; a stale reading is undecided and gets no
ETA; a used reading of 100% cannot last; an ETA earlier than the reset cannot
last; a non-increasing trend, or an ETA at or after the reset, lasts; otherwise
the row is undecided with one of four reason classes - reading failed,
insufficient history, unknown window boundary and abnormal usage. A window with
neither a reset instant nor a period stays in the result and reports the
no-fixed-period case rather than disappearing.

The deprecated vocabulary stays for one release and is derived from the same
computation:

- `verdict` maps `lasts_until_reset` false to `will_exhaust`, true to `on_track`
  and null to `unknown`. `under_used` is retired because a caller reading a
  percentage against its expectation no longer needs a label for it.
- `reasons` keeps its previous codes for stored consumers, but the console's
  four-category copy is a console-only `unavailable_reason` and is not sent to
  MCP callers or Feishu.
- `burn_rate_per_hour`, `projected_percent_at_reset`,
  `projected_tokens_at_reset`, `projected_cost_nanos_at_reset`, `evidence`,
  `sample_count`, `coverage_seconds` and `max_gap_seconds` keep their shape so
  an existing consumer does not break; they are computed from the same result
  and cannot contradict it.

Consumers should migrate by question rather than by field:

- To ask "will it last, and when does it run out", read `lasts_until_reset` and
  `exhaustion_eta_ms`; do not derive either from `verdict` or `reasons`.
- To ask "is it ahead or behind", compare `used_percent` with
  `expected_used_percent`; the difference is meaningful only when both are
  present.
- To show why a row is undecided, use the console's `unavailable_reason` copy;
  capability and Feishu callers get null `lasts_until_reset` and no reason code.

Feishu pushes only rows whose `lasts_until_reset` is false and whose reading is
not stale, re-checks the stored roster immediately before sending, and writes
the actual and expected percentages before a sentence that states only the
exhaustion instant and the conclusion. The console shows the four fields, the
deviation, the reset time and one conclusion sentence; evidence and diagnostics
stay collapsed. Missing values render as "cannot be calculated", "cannot be
estimated" or an em dash, never as zero or "data missing".

## Consequences

The four fields are additive and the deprecated group stays for one release, so
a consumer can migrate without a flag day; the next release removes the
deprecated group, and this ADR is the migration note. There is no storage
migration: stored observations and the capacity, reset-advice and Feishu flows
keep their shapes. Static storage fields (cost, tokens, capacity estimates) are
no longer shown in the prediction panel; the credential quota page remains the
place where a disabled or windowless credential is explained.

Callers that read `verdict == "under_used"` must switch to comparing the two
percentages, and callers that read `projected_*` estimates must switch to the
ETA or the quota page's capacity reading. Old fields remain readable for one
release so this can happen per caller.
