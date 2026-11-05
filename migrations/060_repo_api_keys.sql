-- Repository-owned caller API keys (PRD plans/prd/omc-repo-api-keys, AC-1..AC-37).
--
-- CPA holds caller keys as a plain list in its configuration document and has no
-- disabled state: a key works because it is present. Everything this feature adds
-- therefore lives on OMC's side as metadata keyed by the key's usage fingerprint
-- (`usage_events.api_group_key`), never by its position or value.
--
-- A binding is the durable identity of one key a repository owns. `status` is
-- 'active' while the key is in CPA's list and 'stopped' after it was removed; a
-- stopped row is what keeps the repository, the recipients and the usage
-- attributable after the key itself is gone (AC-10). The partial unique indexes
-- make the two product rules unrepresentable rather than merely checked: one
-- repository has at most one active key (AC-6), and one application waits at a
-- time per repository and agent (AC-28).
CREATE TABLE repo_key_bindings (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    repo_id TEXT NOT NULL CHECK (length(repo_id) BETWEEN 1 AND 256),
    repo_has_remote INTEGER NOT NULL CHECK (repo_has_remote IN (0, 1)),
    key_fingerprint TEXT NOT NULL,
    key_mask TEXT NOT NULL,
    -- 'active' is in CPA's list; 'stopped' was removed from it and is retained.
    status TEXT NOT NULL CHECK (status IN ('active', 'stopped')),
    created_at_ms INTEGER NOT NULL,
    created_by TEXT NOT NULL DEFAULT '',
    stopped_at_ms INTEGER,
    stopped_by TEXT NOT NULL DEFAULT '',
    -- leaked / rotate / misuse / other. Kept separate from the reason so the
    -- console can enforce "leaked keys are never re-enabled" (AC-31).
    stop_category TEXT NOT NULL DEFAULT '' CHECK (stop_category IN ('', 'leaked', 'rotate', 'misuse', 'other')),
    stop_reason TEXT NOT NULL DEFAULT '',
    UNIQUE (instance_id, key_fingerprint)
);
CREATE UNIQUE INDEX idx_repo_key_bindings_active_repo
    ON repo_key_bindings(instance_id, repo_id) WHERE status = 'active';

-- A caller is one agent on one device (PRD 4.1). Its public key is what a
-- delivery is encrypted to, and it is bound to this pair so a changed machine or
-- identity cannot silently inherit an old registration (AC-34).
CREATE TABLE repo_key_callers (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    agent_name TEXT NOT NULL CHECK (length(agent_name) BETWEEN 1 AND 128),
    device_name TEXT NOT NULL CHECK (length(device_name) BETWEEN 1 AND 128),
    public_key TEXT NOT NULL,
    public_key_fingerprint TEXT NOT NULL,
    key_algorithm TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    UNIQUE (instance_id, agent_name, device_name)
);

-- A recipient is a caller already accepted for one repository. Membership is what
-- makes a later application from the same caller auto-approved (AC-4/q01); the
-- first delivery is always a human decision.
CREATE TABLE repo_key_recipients (
    instance_id TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    device_name TEXT NOT NULL,
    approved_at_ms INTEGER NOT NULL,
    approved_by TEXT NOT NULL,
    PRIMARY KEY (instance_id, repo_id, agent_name, device_name)
);

-- One application is one request-for-key. `kind` is decided when the application
-- is created and re-confirmed when it is approved: issue (no key ever), reuse (an
-- active key exists) or reissue (only stopped keys exist). `status` covers the
-- human step only; delivery is recorded separately so the ciphertext stays
-- retrievable after the application left the delivery queue (AC-33).
CREATE TABLE repo_key_applications (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    repo_id TEXT NOT NULL,
    repo_has_remote INTEGER NOT NULL CHECK (repo_has_remote IN (0, 1)),
    agent_name TEXT NOT NULL CHECK (length(agent_name) BETWEEN 1 AND 128),
    device_name TEXT NOT NULL CHECK (length(device_name) BETWEEN 1 AND 128),
    purpose TEXT NOT NULL CHECK (length(purpose) BETWEEN 1 AND 500),
    kind TEXT NOT NULL CHECK (kind IN ('issue', 'reuse', 'reissue')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'rejected')),
    -- The caller-supplied public key this application will be delivered to. It is
    -- snapshotted so an approval cannot be delivered to a key registered later.
    public_key TEXT NOT NULL,
    public_key_fingerprint TEXT NOT NULL,
    key_algorithm TEXT NOT NULL,
    -- 'agent' for an MCP application, 'console' for one an operator filed.
    source TEXT NOT NULL CHECK (source IN ('agent', 'console')),
    -- Set on approval: the binding this delivery belongs to.
    binding_id TEXT NOT NULL DEFAULT '',
    key_fingerprint TEXT NOT NULL DEFAULT '',
    key_mask TEXT NOT NULL DEFAULT '',
    decided_by TEXT NOT NULL DEFAULT '',
    decided_at_ms INTEGER,
    reject_reason TEXT NOT NULL DEFAULT '',
    -- One-time authorization link. It names the same application the console
    -- queue does, so either place decides it and the other stops showing it (AC-8).
    -- Only the fingerprint of the token is stored: the link is handed out once and
    -- the database never holds a credential the console accepts (AGENTS Tier 1).
    authorization_token_hash TEXT NOT NULL,
    token_expires_at_ms INTEGER NOT NULL,
    first_delivered_at_ms INTEGER,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    UNIQUE (authorization_token_hash)
);
CREATE UNIQUE INDEX idx_repo_key_applications_pending
    ON repo_key_applications(instance_id, repo_id, agent_name) WHERE status = 'pending';
CREATE INDEX idx_repo_key_applications_kind_status
    ON repo_key_applications(instance_id, status, created_at_ms DESC);

-- One row per stop action. The binding carries only the current state, so this
-- table is what keeps every earlier stop's category, reason and operator after a
-- re-enable (PRD 4.6, AC-31). `reenabled_*` is filled on the stop being undone;
-- the row itself is never deleted.
CREATE TABLE repo_key_stops (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    binding_id TEXT NOT NULL REFERENCES repo_key_bindings(id) ON DELETE CASCADE,
    category TEXT NOT NULL CHECK (category IN ('leaked', 'rotate', 'misuse', 'other')),
    reason TEXT NOT NULL,
    stopped_by TEXT NOT NULL,
    stopped_at_ms INTEGER NOT NULL,
    reenabled_by TEXT NOT NULL DEFAULT '',
    reenabled_at_ms INTEGER,
    reenable_reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX idx_repo_key_stops_binding
    ON repo_key_stops(instance_id, binding_id, stopped_at_ms DESC);

-- Each ciphertext fetch, and the console's plaintext fallback, is one delivery.
-- This is the only record of who received a key (PRD 4.6).
CREATE TABLE repo_key_deliveries (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    application_id TEXT NOT NULL REFERENCES repo_key_applications(id) ON DELETE CASCADE,
    binding_id TEXT NOT NULL,
    key_fingerprint TEXT NOT NULL,
    key_mask TEXT NOT NULL,
    agent_name TEXT NOT NULL,
    device_name TEXT NOT NULL,
    public_key_fingerprint TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('issue', 'reuse')),
    -- approval / auto (an already-registered caller) / console_reveal.
    operator TEXT NOT NULL CHECK (operator IN ('approval', 'auto', 'console_reveal')),
    delivered_at_ms INTEGER NOT NULL
);
CREATE INDEX idx_repo_key_deliveries_binding
    ON repo_key_deliveries(instance_id, binding_id, delivered_at_ms DESC);
