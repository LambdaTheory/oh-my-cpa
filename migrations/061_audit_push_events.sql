-- Outbox for the team audit platform (PRD 4.8, AC-32, AC-37).
--
-- `audit_events` remains the operator's source of truth. This table is only the copy
-- the platform is owed: one row per event, written in the same transaction as the
-- audit record, replayed by the pusher in insertion order. `payload_json` holds the
-- exact bytes sent, because the platform's contract forbids a retry from regenerating
-- the timestamp, trace id or business fields.
--
-- state:
--   pending     - waiting for a send (or a retry) and due at next_attempt_ms
--   sent        - confirmed by the platform (accepted), never rewritten
--   quarantined - refused with 400/413/415, or retry disabled; kept for diagnosis and
--                 excluded from replay so one bad event cannot block the queue
CREATE TABLE audit_push_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    -- 0 when the queue row was written by a path that had no audit_events row.
    audit_event_id INTEGER NOT NULL DEFAULT 0,
    created_at_ms INTEGER NOT NULL,
    trace_id TEXT NOT NULL,
    span_id TEXT NOT NULL,
    tool_name TEXT NOT NULL,
    event TEXT NOT NULL CHECK (event IN ('tool.end', 'tool.error')),
    payload_json TEXT NOT NULL,
    state TEXT NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'sent', 'quarantined')),
    attempts INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    last_attempt_ms INTEGER,
    next_attempt_ms INTEGER NOT NULL DEFAULT 0,
    sent_at_ms INTEGER
);
CREATE INDEX idx_audit_push_events_due ON audit_push_events(state, next_attempt_ms, id);
