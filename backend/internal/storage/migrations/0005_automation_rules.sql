-- 0005_automation_rules.sql
--
-- Automation rule engine (Phase 5, spec section 11). One row per rule,
-- holding its Trigger/Conditions/Actions configuration (each serialized
-- as JSON -- their shapes are nested and variable-by-type in a way a
-- flat relational schema would fight rather than help, unlike
-- health_checks' single-check-type-per-row fields) plus its live
-- run-tracking columns. Same "no per-execution history table" design as
-- health_checks -- see automation.Repository's doc comment.

CREATE TABLE automation_rules (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    enabled           INTEGER NOT NULL DEFAULT 1,
    trigger           TEXT NOT NULL, -- JSON: automation.Trigger
    conditions        TEXT NOT NULL DEFAULT '[]', -- JSON: []automation.Condition
    actions           TEXT NOT NULL, -- JSON: []automation.Action
    cooldown_seconds  INTEGER NOT NULL DEFAULT 0,
    created_at        TIMESTAMP NOT NULL,
    updated_at        TIMESTAMP NOT NULL,

    -- Live run-tracking columns, owned exclusively by UpdateRunResult
    -- (see automation.Repository's doc comment) -- Upsert never touches
    -- these, so editing a rule's configuration never resets its
    -- observed run history.
    last_run_at       TIMESTAMP,
    last_run_status   TEXT NOT NULL DEFAULT '',
    last_run_message  TEXT NOT NULL DEFAULT '',
    run_count         INTEGER NOT NULL DEFAULT 0
);
