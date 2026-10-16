-- Account-level auto-management: disabling a credential only on explicit
-- account-level weekly (7d) exhaustion evidence, and enabling it directly at the
-- recorded absolute reset time.
--
-- The roster is opt-in: a credential is managed only when an operator adopts it,
-- and a credential a later migration discovery finds is not adopted by default.
-- The three tables below are the whole persisted state; the scheduler keeps no
-- cross-restart plan of its own, so a restart resumes from here.
--
-- account_automation_settings is the single-row web switch. The process-level
-- master switch is an environment variable read at startup (config.Automation),
-- and it is deliberately not stored here: an environment value is the operator's
-- out-of-band emergency control and must stay comparable against the web value
-- rather than being overwritten by it.
CREATE TABLE IF NOT EXISTS account_automation_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    web_enabled INTEGER NOT NULL DEFAULT 0,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

-- account_automation_accounts is both the adopted roster and each account's
-- current plan. One row per auth_index, so a disable and the enable that undoes
-- it describe the same credential.
--
-- The evidence watermark is the pair (disable_observed_at_ms, cycle_key): a new
-- disable needs an observation newer than the watermark and in a cycle the row
-- has not already closed. It is what stops a reset cycle from re-closing the
-- credential against the same stale reading.
--
-- next_attempt_at_ms is the persisted retry schedule: an enable that fails backs
-- off here rather than in memory, so a restart does not lose the wait.
CREATE TABLE IF NOT EXISTS account_automation_accounts (
    auth_index TEXT PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    account_label TEXT NOT NULL DEFAULT '',
    -- is_adopted is roster membership; is_enabled is the per-account switch.
    -- They are separate because an adopted account may have its auto-management
    -- turned off, and turning it back on must not re-run the one-time adoption.
    is_adopted INTEGER NOT NULL DEFAULT 0,
    is_enabled INTEGER NOT NULL DEFAULT 0,
    adopt_batch_id TEXT NOT NULL DEFAULT '',
    adopted_at_ms INTEGER,
    -- state is the account-level plan: idle, monitoring, disabled, enabling,
    -- error. It is descriptive; the scheduler recomputes it each pass.
    state TEXT NOT NULL DEFAULT 'idle',
    -- monitor_reason names why an account has no automatic control
    -- (no_weekly_evidence, missing_reset, no_controllable_credential, ...).
    monitor_reason TEXT NOT NULL DEFAULT '',
    -- disable_observed_at_ms is the observed_at_ms of the weekly reading that
    -- justified the current disable; cycle_key identifies that cycle.
    disable_observed_at_ms INTEGER,
    cycle_reset_at_ms INTEGER,
    cycle_key TEXT NOT NULL DEFAULT '',
    -- manual_reset_at_ms is a one-time absolute recovery time the operator
    -- supplied when upstream published no reset. It is consumed on use.
    manual_reset_at_ms INTEGER,
    manual_reset_consumed INTEGER NOT NULL DEFAULT 0,
    -- Retry schedule and the last outcome, persisted across restart.
    next_attempt_at_ms INTEGER,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_attempt_at_ms INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    last_error_at_ms INTEGER,
    last_disable_at_ms INTEGER,
    last_enable_at_ms INTEGER,
    -- intent_version advances on every operator intent change. The HTTP write
    -- and the background loop both read it under the same per-account lock and
    -- refuse to act on a version that changed, so a cancel that has returned
    -- cannot be followed by the background turning the credential back on.
    intent_version INTEGER NOT NULL DEFAULT 0,
    created_at_ms INTEGER NOT NULL DEFAULT 0,
    updated_at_ms INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_account_automation_accounts_state
    ON account_automation_accounts(state, is_enabled);

-- account_automation_batches is the one-time adoption snapshot: which accounts a
-- batch selection adopted, recorded so the adopted set stays auditable after the
-- roster has moved on. It is written once per adoption and never rewritten.
CREATE TABLE IF NOT EXISTS account_automation_batches (
    id TEXT PRIMARY KEY,
    created_at_ms INTEGER NOT NULL,
    selected_count INTEGER NOT NULL DEFAULT 0,
    selected_json TEXT NOT NULL DEFAULT '[]',
    note TEXT NOT NULL DEFAULT ''
);
