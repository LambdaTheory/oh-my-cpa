CREATE TABLE reset_announcements (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source IN ('manual', 'status_rss', 'x_api')),
    expected_reset_at_ms INTEGER,
    scope TEXT NOT NULL CHECK (scope IN ('all', 'model', 'credential')),
    model TEXT NOT NULL DEFAULT '',
    auth_index TEXT NOT NULL DEFAULT '',
    confidence TEXT NOT NULL CHECK (confidence IN ('confirmed', 'probable', 'unverified')),
    original_text TEXT NOT NULL CHECK (length(original_text) BETWEEN 1 AND 4000),
    registered_at_ms INTEGER NOT NULL,
    created_at_ms INTEGER NOT NULL,
    CHECK ((scope = 'all' AND model = '' AND auth_index = '')
        OR (scope = 'model' AND model <> '' AND auth_index = '')
        OR (scope = 'credential' AND model = '' AND auth_index <> ''))
);
CREATE INDEX idx_reset_announcements_registered ON reset_announcements(registered_at_ms DESC, id DESC);
