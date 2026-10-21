CREATE TABLE quota_refresh_attempts (
    auth_index TEXT PRIMARY KEY,
    outcome TEXT NOT NULL CHECK (outcome IN ('success', 'failed', 'rate_limited')),
    attempted_at_ms INTEGER NOT NULL,
    http_status INTEGER NOT NULL DEFAULT 0,
    retry_at_ms INTEGER NOT NULL DEFAULT 0
);
