-- Ollama quota accounts (lane B of the first batch).
--
-- Ollama publishes no quota API, so a quota reading is a screen scrape of
-- https://ollama.com/settings carrying the account's session cookie. The cookie
-- is a high-value secret: it is stored AES-GCM encrypted beside its nonce and is
-- never returned by the API, written to a log, or made readable to the Agent's
-- raw SQL path. The encrypted cookie and the safe quota snapshot live in
-- separate columns so an operational read selects the snapshot without ever
-- touching the secret.
--
-- id is a stable, opaque UUID minted by the server. It is deliberately not
-- derived from the account's email, label or cookie, so the identity survives an
-- upstream rename and a rotating cookie.
--
-- enabled controls collection only. Ollama is not in CPA's control chain, so this
-- switch must not be read as a CPA credential enable/disable.
CREATE TABLE IF NOT EXISTS ollama_accounts (
    id TEXT PRIMARY KEY,
    label TEXT NOT NULL DEFAULT '',
    -- enabled gates the scraper, never CPA.
    enabled INTEGER NOT NULL DEFAULT 1,
    -- The cookie is mandatory while an account exists and is stored encrypted;
    -- the plaintext is never written here.
    cookie_ciphertext BLOB NOT NULL,
    cookie_nonce BLOB NOT NULL,
    cookie_updated_at_ms INTEGER NOT NULL DEFAULT 0,
    -- status is the newest collection outcome: ok, auth_invalid, transport_error
    -- or parse_error. It is descriptive, not a CPA credential status.
    status TEXT NOT NULL DEFAULT '',
    last_success_at_ms INTEGER,
    -- last_observed_at_ms is when the newest attempt completed, success or not,
    -- so the console can tell "never collected" from "collected and failed".
    last_observed_at_ms INTEGER,
    -- newest_error is the scrubbed message of the newest failure. It never
    -- carries the cookie.
    newest_error TEXT NOT NULL DEFAULT '',
    newest_error_at_ms INTEGER,
    -- next_attempt_at_ms is the persisted backoff after a timeout or an upstream
    -- rate limit; it survives a restart so the scraper does not hammer upstream.
    next_attempt_at_ms INTEGER,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    -- quota_snapshot_json is the newest safe quota reading (a quota.NormalizedQuota
    -- with auth_index "ollama:<id>"). It carries no credential material and is the
    -- only quota state this module persists.
    quota_snapshot_json TEXT NOT NULL DEFAULT '',
    quota_observed_at_ms INTEGER,
    created_at_ms INTEGER NOT NULL DEFAULT 0,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_ollama_accounts_collection
    ON ollama_accounts(enabled, next_attempt_at_ms);
