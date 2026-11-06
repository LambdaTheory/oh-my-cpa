-- GitHub owner/repo identities are case-insensitive; submitted spelling remains
-- available to the reviewer instead of becoming a second ownership identity.
ALTER TABLE repo_key_applications ADD COLUMN repo_display TEXT NOT NULL DEFAULT '';
UPDATE repo_key_applications SET repo_display = repo_id;

-- Domain/port-qualified host prefixes and localhost remain case-sensitive, even
-- for a one-segment path. Historical single-label hosts cannot be distinguished
-- from GitHub shorthand because the old schema did not retain the host separately.
-- Existing uniqueness constraints deliberately refuse conflicting identities rather
-- than discard a key, approval or recipient record during migration.
UPDATE repo_key_bindings SET repo_id = lower(repo_id)
WHERE repo_has_remote = 1 AND instr(repo_id, '/') > 0
  AND instr(substr(repo_id, instr(repo_id, '/') + 1), '/') = 0
  AND substr(repo_id, 1, instr(repo_id, '/') - 1) NOT GLOB '*[.:]*'
  AND lower(substr(repo_id, 1, instr(repo_id, '/') - 1)) <> 'localhost';
UPDATE repo_key_applications SET repo_id = lower(repo_id)
WHERE repo_has_remote = 1 AND instr(repo_id, '/') > 0
  AND instr(substr(repo_id, instr(repo_id, '/') + 1), '/') = 0
  AND substr(repo_id, 1, instr(repo_id, '/') - 1) NOT GLOB '*[.:]*'
  AND lower(substr(repo_id, 1, instr(repo_id, '/') - 1)) <> 'localhost';
UPDATE repo_key_recipients SET repo_id = lower(repo_id)
WHERE instr(repo_id, '/') > 0
  AND instr(substr(repo_id, instr(repo_id, '/') + 1), '/') = 0
  AND substr(repo_id, 1, instr(repo_id, '/') - 1) NOT GLOB '*[.:]*'
  AND lower(substr(repo_id, 1, instr(repo_id, '/') - 1)) <> 'localhost';

CREATE UNIQUE INDEX idx_repo_key_bindings_github_identity
ON repo_key_bindings(instance_id, lower(repo_id))
WHERE status = 'active' AND repo_has_remote = 1 AND instr(repo_id, '/') > 0
  AND instr(substr(repo_id, instr(repo_id, '/') + 1), '/') = 0
  AND substr(repo_id, 1, instr(repo_id, '/') - 1) NOT GLOB '*[.:]*'
  AND lower(substr(repo_id, 1, instr(repo_id, '/') - 1)) <> 'localhost';
CREATE UNIQUE INDEX idx_repo_key_applications_github_pending
ON repo_key_applications(instance_id, lower(repo_id), agent_name)
WHERE status = 'pending' AND repo_has_remote = 1 AND instr(repo_id, '/') > 0
  AND instr(substr(repo_id, instr(repo_id, '/') + 1), '/') = 0
  AND substr(repo_id, 1, instr(repo_id, '/') - 1) NOT GLOB '*[.:]*'
  AND lower(substr(repo_id, 1, instr(repo_id, '/') - 1)) <> 'localhost';
