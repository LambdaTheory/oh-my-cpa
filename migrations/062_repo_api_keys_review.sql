-- Repo API Keys, review round 1 (T9): the fields the approval link, the approval
-- page and the re-enable-as-approval flow need.
--
-- 1. `repo_key_bindings.key_sealed` keeps one sealed copy of the key a repository
--    owns. CPA has no disabled state, so a stop removes the key from the gateway;
--    re-enabling it afterwards (AC-31: "恢复后不产生新 key") needs the same value
--    back, and nothing else in OMC could produce it. The copy is sealed with the
--    deployment cipher (internal/appcrypto), never plaintext, and it is cleared
--    when a key is stopped as leaked - such a key is never re-enabled (AC-31).
--
-- 2. `repo_key_applications.request_kind` marks an application that asks to put a
--    stopped key back in service. It is a request, not a state change: approval
--    goes through the same DecideRepoKeyApplication path (AC-31 "与申请同一个
--    审批动作"). `target_binding_id` names the stopped key it asks about, and
--    `decision_reason` records the approver's reason for the approval record
--    (AC-21 批准原因).
--
-- 3. A stop is permanent for a leaked key, so the sealed copy is dropped there.

ALTER TABLE repo_key_bindings ADD COLUMN key_sealed TEXT NOT NULL DEFAULT '';

ALTER TABLE repo_key_applications ADD COLUMN request_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE repo_key_applications ADD COLUMN target_binding_id TEXT NOT NULL DEFAULT '';
ALTER TABLE repo_key_applications ADD COLUMN decision_reason TEXT NOT NULL DEFAULT '';

-- 4. A console plaintext reveal is a hand-out without an application (AC-20: the
--    console fallback is recorded as a delivery too). The old column required an
--    application id, so the table is rebuilt with the reference optional: NULL
--    means "the console handed the key out itself". No record is dropped.
CREATE TABLE repo_key_deliveries_new (
    id TEXT PRIMARY KEY,
    instance_id TEXT NOT NULL,
    application_id TEXT REFERENCES repo_key_applications(id) ON DELETE CASCADE,
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
INSERT INTO repo_key_deliveries_new (id, instance_id, application_id, binding_id, key_fingerprint,
    key_mask, agent_name, device_name, public_key_fingerprint, kind, operator, delivered_at_ms)
SELECT id, instance_id, application_id, binding_id, key_fingerprint,
    key_mask, agent_name, device_name, public_key_fingerprint, kind, operator, delivered_at_ms
FROM repo_key_deliveries;
DROP TABLE repo_key_deliveries;
ALTER TABLE repo_key_deliveries_new RENAME TO repo_key_deliveries;
CREATE INDEX idx_repo_key_deliveries_binding
    ON repo_key_deliveries(instance_id, binding_id, delivered_at_ms DESC);
