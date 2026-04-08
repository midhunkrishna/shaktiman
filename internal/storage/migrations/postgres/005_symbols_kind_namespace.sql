-- +goose up

-- Add 'namespace' to the symbols.kind CHECK constraint.
--
-- ADR-004 added TypeScript `internal_module → namespace` to the parser's
-- SymbolKindMap, but the original schema's CHECK constraint in
-- 001_base_schema.sql only permits {function, class, method, type,
-- interface, variable, constant}. Inserting a namespace symbol fails at
-- the DB layer, so TypeScript namespace symbols have been silently
-- dropped from the index.
--
-- Postgres auto-names inline CHECK constraints as <table>_<column>_check.
-- Drop the old constraint and add a new one with the expanded set.

ALTER TABLE symbols DROP CONSTRAINT IF EXISTS symbols_kind_check;
ALTER TABLE symbols ADD CONSTRAINT symbols_kind_check
    CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'variable', 'constant', 'namespace'));

-- +goose down

ALTER TABLE symbols DROP CONSTRAINT IF EXISTS symbols_kind_check;
ALTER TABLE symbols ADD CONSTRAINT symbols_kind_check
    CHECK (kind IN ('function', 'class', 'method', 'type', 'interface', 'variable', 'constant'));
