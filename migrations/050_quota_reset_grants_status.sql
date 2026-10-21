ALTER TABLE quota_snapshots ADD COLUMN reset_grants_status TEXT NOT NULL DEFAULT ''
    CHECK (reset_grants_status IN ('', 'available', 'unavailable', 'failed'));
