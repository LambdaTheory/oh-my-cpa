CREATE TABLE reauth_jobs (
    id TEXT PRIMARY KEY,
    auth_index TEXT NOT NULL,
    provider TEXT NOT NULL,
    flow TEXT NOT NULL,
    code_kind TEXT NOT NULL,
    user_code TEXT NOT NULL DEFAULT '',
    verification_url TEXT NOT NULL DEFAULT '',
    expires_at_ms INTEGER NOT NULL DEFAULT 0,
    account_label TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('pending','oauth_started','waiting_code','waiting_browser','waiting_callback','waiting_device','code_received','oauth_submitted','verifying','succeeded','failed','expired')),
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    last_error TEXT NOT NULL DEFAULT '',
    cycle_closed_at_ms INTEGER NOT NULL DEFAULT 0,
    next_attempt_at_ms INTEGER NOT NULL DEFAULT 0,
    notification TEXT NOT NULL DEFAULT '',
    message_id TEXT NOT NULL DEFAULT '',
    code_consumed INTEGER NOT NULL DEFAULT 0
);
-- Terminal failures own the invalid episode until a healthy read closes it.
CREATE UNIQUE INDEX idx_reauth_jobs_episode ON reauth_jobs(auth_index) WHERE cycle_closed_at_ms = 0;
CREATE UNIQUE INDEX idx_reauth_jobs_contact ON reauth_jobs((1)) WHERE state IN ('oauth_started','waiting_code','waiting_browser','waiting_callback','waiting_device','code_received','oauth_submitted','verifying');
CREATE UNIQUE INDEX idx_reauth_jobs_message ON reauth_jobs(message_id) WHERE message_id <> '';
CREATE INDEX idx_reauth_jobs_pending ON reauth_jobs(state, created_at_ms);
