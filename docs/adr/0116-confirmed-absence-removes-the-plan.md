# ADR 0116: Confirmed absence removes the plan; a reappearing credential is a fresh engagement

- Status: Accepted; supersedes ADR 0114's rule that a pending recovery or retry protects a missing roster row. ADR 0114's text is preserved as the record of the decision it made.
- Date: 2026-10-26

## Context

ADR 0114 required two consecutive complete gateway inventories before an absent
credential left the active roster, and it protected any row with a pending retry, a
disabled/waiting automation state, a saved cycle reset or an unconsumed manual reset.
That protection left exactly the placeholder the console must not show: a row that is
not a current gateway credential, presented beside real ones. Operators also saw a
roster count and a gateway count disagree without a way to tell which rows were real,
and a repeated display name (CPA often fills a label with an account email) looked like
a duplicate identity.

The approved requirement (the 2026-10-26 v5 decision) is that the main table shows the
gateway's current credentials only. A confirmed absence is final: the plan is
recorded in an audit snapshot and removed, because a recovery or retry that targets a
credential the gateway no longer has can never run. Two complete, successful,
authoritative inventories still gate the removal, and any failed, incomplete, degraded or
ambiguous read is never evidence of absence.

## Decision

One small inventory validator is shared by the background pass, adoption and the console's
read projection. It rejects a blank, malformed or duplicated auth index before any map is
built, so an ambiguous list cannot be read two ways. A credential without a write name is
present and not writable. A complete, successful, empty list is an empty gateway; a failed
or incomplete read is an unknown one. CPA's `/credentials` response carries no total, page
or completeness field, so an unmarked truncation cannot be detected; that stays an
external limit rather than a decoded guess.

Two consecutive complete successful inventories remove an absent plan regardless of a
pending recovery, retry, disabled/waiting state or saved reset. The removal captures an
allowlisted snapshot of the intent and plan (provider, name, state, reason, cycle and
manual recovery instants, evidence watermark, retry schedule, error, adoption batch and
timestamps - never the display label) in the same SQLite transaction as the tombstone, and
clears the executable plan and action ownership. An unwritable snapshot rolls the removal
back. Nothing is sent to the gateway. History, adoption batches, quota observations and
audit attribution remain.

A tombstoned row adopted again is a fresh engagement: a new adoption instant and batch,
the requested switch, Credit off, and no inherited plan, preference, retry, error or
action ownership. Active identical adoption stays idempotent. The same auth index observed
with a different provider or write name is refused rather than relabelled under the old
plan. Each automatic status write re-reads the row under its lock and verifies the
incarnation (intent version), the unique current index, the write name, the provider and
the plan ownership; a delayed pass that holds an older incarnation stops. An observation
keeps its real timestamp and is ineligible for the current adoption when it predates it;
it is excluded, never restamped.

The console main table is built from the inventory, joined with whatever plan uniquely
matched each credential. Managed counts, filters and batch actions describe current rows;
a plan without a current credential is a pending-absence count. During an outage every
count is unknown and retained plans are labelled last-known.

## Consequences

The roster can no longer hold a plan the gateway cannot act on, and the console cannot show
a non-credential as an account. A credential reappearing after a confirmed absence is
treated as what it may be - a different credential - so no old plan can execute against it;
the operator adopts it deliberately. Absence confirmation still belongs to one process and
one intent version, so a restart delays cleanup instead of widening it.

The rule relies on complete inventories. An upstream that silently truncates its list
without a completeness signal is not detectable by OMC; the documented mitigation is to
treat only two consecutive identical complete reads as confirmation and to keep every
failed or ambiguous read out of the decision. An unseen replacement at the same path,
index, name and provider with no distinguishing account evidence cannot be told apart from
the credential it replaced; no local identifier, UUID or email comparison is presented as
proof of continuity. A race remains where CPA replaces a credential between OMC's
inventory read and its status write; the pre-write identity check narrows it to the window
of one call and does not claim to close it.
