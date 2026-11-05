# ADR 0119: Audit events reach the platform from an outbox written with the audit row

- Status: Accepted
- Date: 2026-11-05

## Context

The audit trail is the console's own record of who did what. A company audit platform
(`LambdaTheory/audit-logger-agent`) wants a copy of the actions that matter, so a reviewer
there sees them beside every other agent's work. Its integration guide fixes the shape of
that exchange: `POST /v1/ingest` with a batch envelope, an ISO 8601 UTC `ts` that must not
change when the same event is retried, a `trace_id` naming the thing the action was about,
a canonical gRPC `status`, and a `result_summary` that is a redacted digest.

Three failure modes decide the design. The platform can be down for hours. Its ingest can
accept some events of a batch and refuse others. And a business action must not become slow
or fail because a second system is unavailable — but an action that the trail records and
the platform never hears about is a silent gap in exactly the record the platform exists to
keep.

## Decision

1. **The queue row is written in the audit row's transaction.** `AuditEvent.Push` makes
   `RecordAuditEvent` open a transaction that writes `audit_events` and
   `audit_push_events` together. If the queue row cannot be written, the audit row does not
   exist and the action fails. The pairing is therefore exact in both directions: an action
   the console let through is an event the platform is owed.

2. **The payload is built once and stored as bytes.** `auditpush.Build` produces the event
   at the moment the action happens, when the actor, the trace and the status are known.
   What is stored is what is sent, including `ts`. A retry replays the original bytes, so
   the platform's own deduplication sees an identical event rather than a second one.

3. **Delivery is a separate loop with a visible backlog.** `internal/auditpush.Pusher`
   runs on its own interval, sends oldest-first in batches of `OMCPA_AUDIT_RETRY_MAX_BATCH`,
   and settles a batch per event from the platform's `202` answer. A network failure
   reschedules with backoff; a contract rejection (`400`/`413`/`415`) or an individually
   refused event is quarantined with the platform's reason rather than retried forever.
   Backlog, quarantine count and last success are logged and readable through
   `repository.AuditPushStatusOf`. Nothing in the request path waits on any of it.

4. **Off unless a deployment names a platform.** `OMCPA_AUDIT_INGEST_URL` is the switch; it
   also controls retry, timeout and batch size. Demo mode clears it, so the public
   demonstration never sends anything anywhere.

## Consequences

The event names the row it is about, which is why the repository lets a caller supply an
application or binding id: the service pre-generates the id so the platform's trace and the
console's row agree. Where a pending application is deduplicated onto an existing row, the
event's trace is repointed at the row that exists (`AuditPushEvent.WithTraceID`) before it
is queued.

`audit_push_events` grows at the audit trail's own rate and is never pruned: a sent row is
what answers "did the platform receive this". Operators who care about the backlog read the
log line the pusher emits, which carries pending, quarantined, earliest pending and last
success.

Quarantined events need a person: the payload was built for a contract that has since
changed, and the fix is a decision about that event, not an automatic replay of bytes the
platform already refused.
