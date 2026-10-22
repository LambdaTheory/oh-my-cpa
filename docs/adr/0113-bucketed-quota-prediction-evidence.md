# ADR 0113: Bucketed quota prediction evidence

- Status: Accepted
- Date: 2026-10-22
- Amends: ADR 0098's observation sampling and evidence thresholds; its capability
  boundary, privacy model and result bounds remain in force.

## Context

ADR 0098 established read-only quota prediction from stored passive and active
observations. Its reader stopped at 5,000 raw rows per window, so a busy Codex
weekly window (about 18,200 readings over seven days) was truncated and reported
as incomplete history instead of being projected. The same path accepted two
points as a fit and exposed no sample size, covered span or largest silence, so
an operator could not tell a well-sampled projection from two adjacent ticks.

The raw observation table, its dedup writer and the capacity, reset-advice and
Feishu flows are not the problem. The defect is the shape in which history is
read and the evidence threshold applied to it.

## Decision

Read one complete window identity—`auth_index`, `provider`, `window_kind`,
`scope` and `model`—as fixed five-minute buckets inside the current cycle.
Filtering and aggregation happen in SQL. Each bucket carries its newest real
reading (time, percentage, reset instant and source), the minimum used
percentage, the minimum and maximum reset instants, its row count, and an ordered
within-bucket reset flag. Empty buckets are not synthesized: a gap stays a gap
rather than becoming a zero or a carried-forward sample. Rows sharing one instant
are first reduced to the effective reading in the prediction collapse's order
(active over passive, then newest id), so two sources at one instant cannot create
a reset out of order.

The ordered flag is computed before the lossy min/last summary. A sequence such as
10 → 80 → 20 within one bucket would otherwise summarize to min 20, last 20 and
look monotonic against a previous bucket at 10. Comparing each ordered row with its
predecessor preserves that drop. The same comparison marks a reset instant moving
earlier by more than one second. A normal monotonic rise does not trip either
condition.

The 5,000 limit counts buckets, not raw observations. A seven-day window therefore
uses about 2,016 buckets; overflow still sets `has_more` and is reported as
incomplete history by the operations layer. This reverses the truncation failure
without changing the observation table, its indexes or the write path.

Reset detection stays layered rather than wholly relocated. The repository records
the ordered within-bucket flag. The operations layer compares adjacent buckets (a
bucket minimum more than one percentage point below the previous bucket's last
reading, or a minimum reset instant more than one second earlier than the previous
bucket's last reset), and also compares the current bucket's last stored reading
with the current snapshot that will replace that bucket in the fitted sample. The
pure calculation layer keeps `predictionHasReset` as its existing cross-point
check. Any hit marks `has_mid_cycle_reset`, which yields `mid_cycle_reset` unknown
and no cross-reset fit.

A fresh, valid 100% current reading is a fact before history is consulted. It
returns `will_exhaust`/`fact` with the observation as the exhaustion instant even
when history is incomplete or a mid-cycle reset was detected. Stale or error
status, missing or invalid percentages, and unknown or expired boundaries still
prevent the fact.

Projections now require at least three deduplicated points, coverage of at least
`max(30 minutes, 5% of the cycle)`, and no adjacent gap larger than
`max(30 minutes, coverage / 2)`. Failures emit `fewer_than_three_buckets`,
`insufficient_coverage` or `observation_gap`. The former
`fewer_than_two_observations` constant remains for stored consumers but is no
longer emitted by this path.

`WindowPrediction` and `QuotaPredictionItem` gain `sample_count`,
`coverage_seconds` and `max_gap_seconds`. The interval fields remain null when
the sample has fewer than two points; they are never written as zero. A
`sample_count` of zero means the sample was not evaluated because a fact or an
earlier validation return settled the result; it does not assert that no history
exists. These fields are additive and do not change the 100-row or 32 KiB bounds.

## Consequences

High-traffic windows are sampled by time rather than by row count, so the
prediction no longer depends on request volume. A long cycle now needs evidence
across a meaningful share of that cycle: a weekly window cannot be projected from
a single half-hour burst. Reset evidence is preserved where ordering still exists,
including a transient spike that recovers inside one bucket and a snapshot that
replaces a bucket whose stored reading had already peaked. The result discloses how
much evidence supported the projection, and a fresh 100% reading remains a fact
when surrounding history is imperfect.

This does not add a migration, change stored semantics, refresh quota, or alter
routing, risk bands or reset advice. It does not supersede ADR 0098's capability
boundary, privacy model or result bounds; those remain in force.
