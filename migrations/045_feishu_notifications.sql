-- Feishu (Lark) notifications (lane C of the first batch).
--
-- The bot reuses one long connection, answers read-only queries and denoises
-- notifications. Nothing here is a CPA credential: the module never writes CPA,
-- and its only secret is the self-built app secret, which is stored AES-GCM
-- encrypted in feishu_secrets and is never returned by the API, written to a log,
-- or made readable through the operator's read-only SQL path.
--
-- feishu_settings holds non-secret web settings only. The app secret lives in a
-- separate table precisely so that table can be classified as hidden while these
-- settings stay readable. has_secret is derived from LENGTH(ciphertext) > 0, never
-- from reading the blob.
--
-- feishu_pending_items is the structured "something needs a human" list. id is a
-- stable hash of the alert kind, the credential key or provider, and the scope, so
-- the same ongoing problem never re-alerts after a restart.
--
-- feishu_notifications is the delivery outbox plus the sent acknowledgements. A
-- row is marked sent only after Feishu confirms delivery, so a transient send
-- failure is retried rather than lost. state is pending, queued (held during quiet
-- hours) or sent.
--
-- feishu_channel_tracks backs monitor_down: when a channel was first seen and when
-- it last produced valid data, so an outage is timed from its real beginning across
-- restarts, including a collector-level failure that yields no account at all.
--
-- feishu_state is the single-row module memory: whether the one-time startup
-- summary has been sent (so a settings reload never resends it) and when the newest
-- scheduled digest went out.
CREATE TABLE IF NOT EXISTS feishu_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    enabled INTEGER NOT NULL DEFAULT 0,
    app_id TEXT NOT NULL DEFAULT '',
    chat_id TEXT NOT NULL DEFAULT '',
    summary_times_json TEXT NOT NULL DEFAULT '["10:00","17:00"]',
    timezone TEXT NOT NULL DEFAULT '',
    quiet_start TEXT NOT NULL DEFAULT '23:00',
    quiet_end TEXT NOT NULL DEFAULT '08:00',
    tight_remaining_percent REAL NOT NULL DEFAULT 20,
    channel_unavailable_after_ms INTEGER NOT NULL DEFAULT 1800000,
    monitor_down_after_ms INTEGER NOT NULL DEFAULT 1800000,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS feishu_secrets (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    app_secret_ciphertext BLOB,
    app_secret_nonce BLOB,
    app_secret_updated_at_ms INTEGER,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS feishu_pending_items (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL DEFAULT '',
    provider TEXT NOT NULL DEFAULT '',
    cred_key TEXT NOT NULL DEFAULT '',
    alias TEXT NOT NULL DEFAULT '',
    since_ms INTEGER,
    last_seen_ms INTEGER,
    action TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'open',
    notified_at_ms INTEGER,
    resolved_at_ms INTEGER,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS feishu_notifications (
    id TEXT PRIMARY KEY,
    kind TEXT NOT NULL DEFAULT '',
    item_ids_json TEXT NOT NULL DEFAULT '[]',
    alerts_json TEXT NOT NULL DEFAULT '[]',
    pending_json TEXT NOT NULL DEFAULT '[]',
    tight_json TEXT NOT NULL DEFAULT '[]',
    state TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt_at_ms INTEGER,
    sent_at_ms INTEGER,
    occurred_at_ms INTEGER,
    created_at_ms INTEGER NOT NULL DEFAULT 0,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_feishu_notifications_state
    ON feishu_notifications(state, next_attempt_at_ms);

CREATE TABLE IF NOT EXISTS feishu_channel_tracks (
    provider TEXT PRIMARY KEY,
    first_seen_ms INTEGER,
    last_valid_ms INTEGER,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS feishu_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    bootstrapped INTEGER NOT NULL DEFAULT 0,
    last_summary_at_ms INTEGER,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

-- feishu_account_signals is the safe, enum-only record of the newest collection
-- outcome per credential. A quota snapshot exists only for a successful read, so
-- this is the only durable evidence that a credential failed authentication: the
-- recommendation's explicit needs_reauth classification is not persisted in the
-- snapshot. state is a fixed vocabulary word (healthy, auth_invalid, transport,
-- parse, unknown); no raw upstream error, token or other secret is ever stored.
-- One row per credential is upserted in place, so the Feishu report reads the
-- freshest collection result without growing the database with history.
CREATE TABLE IF NOT EXISTS feishu_account_signals (
    auth_index TEXT PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT '',
    state TEXT NOT NULL DEFAULT 'unknown',
    observed_at_ms INTEGER NOT NULL DEFAULT 0,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);
