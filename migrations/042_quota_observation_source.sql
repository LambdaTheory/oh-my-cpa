-- Active observations and window identity.
--
-- The passive stream reads a quota window from a request's own response headers.
-- An active observation is the result of a refresh the process asked for, which
-- is the only way to learn about a quiet credential, a model-scoped window, or
-- the end of a cycle the traffic never closed. Source, scope and model let the
-- two share one timeline without being confused for one another.
--
-- source defaults to 'passive' and scope to 'standard' so the rows written
-- before this migration keep their original meaning without a rewrite.
ALTER TABLE quota_observations ADD COLUMN source TEXT NOT NULL DEFAULT 'passive';
ALTER TABLE quota_observations ADD COLUMN scope TEXT NOT NULL DEFAULT 'standard';
ALTER TABLE quota_observations ADD COLUMN model TEXT NOT NULL DEFAULT '';
