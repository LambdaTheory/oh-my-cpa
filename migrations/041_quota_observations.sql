-- Passive quota observations: the quota windows an upstream publishes on a
-- request's own response headers, kept as history beside the request records.
--
-- This is deliberately not quota_snapshots: a snapshot is the last normalized
-- reading of a credential the console can overwrite, while an observation is one
-- upstream statement at one instant that is never rewritten. It lets a later
-- read answer "which window moved at 09:00 and what did the reset look like"
-- without a refresh having to run, and it survives the request records' 90-day
-- retention for a full year.
--
-- Values are only written when the upstream header parsed and was in range; a
-- malformed or missing window produces no row rather than a fabricated zero.
CREATE TABLE IF NOT EXISTS quota_observations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    auth_index TEXT NOT NULL,
    provider TEXT NOT NULL,
    window_kind TEXT NOT NULL,
    window_minutes INTEGER NOT NULL,
    used_percent REAL NOT NULL,
    reset_at_ms INTEGER,
    status TEXT NOT NULL DEFAULT '',
    -- observed_at_ms is the request's own timestamp (usage_events.timestamp_ms);
    -- created_at_ms is when this row was written. They differ for a backfilled
    -- observation and when CPA's queue delivers late.
    observed_at_ms INTEGER NOT NULL,
    event_key TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL,
    -- One observation per window per request: a decode retry or a repeated
    -- backfill pass cannot double-record the same upstream statement.
    UNIQUE(event_key, window_kind)
);

-- Serves the read tool's window filter and the dedup probe for the newest row
-- per credential and window.
CREATE INDEX IF NOT EXISTS idx_quota_observations_credential
    ON quota_observations(auth_index, window_kind, observed_at_ms DESC);

-- The backfill watermark. It shares usage_aggregation_checkpoints, which is a
-- watermark keyed by name, so a new key needs no new table.
INSERT INTO usage_aggregation_checkpoints
    (name, last_usage_event_id, created_at_ms, updated_at_ms)
VALUES
    ('quota_observations', 0, unixepoch() * 1000, unixepoch() * 1000);
