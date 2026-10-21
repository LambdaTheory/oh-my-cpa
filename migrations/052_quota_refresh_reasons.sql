ALTER TABLE quota_refresh_attempts ADD COLUMN reason TEXT NOT NULL DEFAULT ''
    CHECK (reason IN ('', 'timeout', 'network', 'auth', 'http', 'parse', 'unknown'));
ALTER TABLE quota_refresh_attempts ADD COLUMN reason_summary TEXT NOT NULL DEFAULT '';
