-- Model groups: an operator-named, ordered list of model names kept for one purpose
-- (力工组, 审查组). The group is what the console edits and what a caller later asks
-- about by name, so the name is the identity a reader resolves. It is unique under
-- NOCASE because two groups whose names differ only in ASCII case are a mistake, not
-- a distinction anyone can hold in their head.
--
-- `is_preset` marks the five groups a new deployment starts with. It is not a
-- privilege - a preset group renames and deletes exactly like any other - it is what
-- lets "restore the default groups" tell which of the original five still exists,
-- including one that was renamed rather than deleted.
--
-- Members are their own table because one model may sit in several groups, each with
-- its own order. `position` orders members within one group only, starting at 1; the
-- unique (group_id, position) pair is what keeps two members from claiming one slot,
-- and the (group_id, model) primary key is what makes adding a model twice a refusal
-- rather than a hidden duplicate. A member row has no meaning without its group, so
-- deleting a group deletes its members with it.
CREATE TABLE model_groups (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE CHECK (length(name) BETWEEN 1 AND 40),
    is_preset INTEGER NOT NULL DEFAULT 0 CHECK (is_preset IN (0, 1)),
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL
);
CREATE TABLE model_group_members (
    group_id TEXT NOT NULL REFERENCES model_groups(id) ON DELETE CASCADE,
    model TEXT NOT NULL CHECK (length(model) BETWEEN 1 AND 256),
    position INTEGER NOT NULL CHECK (position >= 1),
    added_at_ms INTEGER NOT NULL,
    PRIMARY KEY (group_id, model),
    UNIQUE (group_id, position)
);

-- The five groups a new deployment starts with, empty and ready to fill. They are
-- seeded here rather than on first read so a fresh database and an upgraded one end
-- up identical, and so no bootstrap path exists that runs once and could be missed.
-- Their ids are fixed rather than generated because "restore the default groups" has
-- to recognise one that was renamed, and only a stable identity can do that.
INSERT INTO model_groups (id, name, is_preset, created_at_ms, updated_at_ms) VALUES
    ('preset-labor',        '力工组', 1, unixepoch() * 1000, unixepoch() * 1000),
    ('preset-frontend',     '前端组', 1, unixepoch() * 1000, unixepoch() * 1000),
    ('preset-orchestrator', '编排组', 1, unixepoch() * 1000, unixepoch() * 1000),
    ('preset-review',       '审查组', 1, unixepoch() * 1000, unixepoch() * 1000),
    ('preset-followup',     '跟进组', 1, unixepoch() * 1000, unixepoch() * 1000);
