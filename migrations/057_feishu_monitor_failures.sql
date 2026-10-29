-- Count distinct collection attempts, rather than repeated reads of one snapshot.
ALTER TABLE feishu_channel_tracks ADD COLUMN consecutive_failures INTEGER NOT NULL DEFAULT 0;
ALTER TABLE feishu_channel_tracks ADD COLUMN last_attempt_at_ms INTEGER;
