-- +goose up

-- Defense-in-depth NOT NULL enforcement for project_id columns using the
-- ADD CONSTRAINT CHECK ... NOT VALID + VALIDATE CONSTRAINT pattern.
--
-- Migration 006 already issued ALTER COLUMN ... SET NOT NULL on these
-- columns. SET NOT NULL takes ACCESS EXCLUSIVE and a full sequential
-- scan to validate, which is risky on large tables. Going forward the
-- preferred pattern is:
--
--     ALTER TABLE t ADD CONSTRAINT c CHECK (col IS NOT NULL) NOT VALID;
--     ALTER TABLE t VALIDATE CONSTRAINT c;
--
-- ADD CONSTRAINT NOT VALID takes only a brief ACCESS EXCLUSIVE for the
-- catalog change; VALIDATE CONSTRAINT runs under SHARE UPDATE EXCLUSIVE
-- so concurrent INSERT/UPDATE/DELETE keep working during validation.
--
-- This migration installs a redundant CHECK on top of the existing NOT
-- NULL. Redundancy is intentional — it documents the recommended
-- pattern for similar future columns and provides a second-layer guard
-- against schema-corruption (e.g. an aborted goose down/up cycle that
-- left the column nullable).
--
-- For databases past migration 006 the VALIDATE step is essentially a
-- no-op (no NULLs exist). For databases that somehow lost the NOT NULL
-- (manual rollback, etc.) this loudly fails on validation, surfacing
-- the schema drift instead of letting it persist.

ALTER TABLE files
    ADD CONSTRAINT files_project_id_not_null
    CHECK (project_id IS NOT NULL) NOT VALID;
ALTER TABLE files VALIDATE CONSTRAINT files_project_id_not_null;

ALTER TABLE embeddings
    ADD CONSTRAINT embeddings_project_id_not_null
    CHECK (project_id IS NOT NULL) NOT VALID;
ALTER TABLE embeddings VALIDATE CONSTRAINT embeddings_project_id_not_null;

-- +goose down

ALTER TABLE embeddings DROP CONSTRAINT IF EXISTS embeddings_project_id_not_null;
ALTER TABLE files DROP CONSTRAINT IF EXISTS files_project_id_not_null;
