-- 0002_health_checks.sql
--
-- Health check engine (Phase 4, spec section 9). One row per configured
-- check, holding both its configuration and its current live status —
-- see internal/health/repository.go's doc comment on why there is no
-- separate per-result history table: every state transition is already
-- durably recorded via the events table (0001_init.sql) through
-- internal/app's existing event-persistence bridge, since
-- internal/health.Engine publishes a bus event on every transition.

CREATE TABLE health_checks (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    type                TEXT NOT NULL,
    target              TEXT NOT NULL DEFAULT '',
    command_args        TEXT NOT NULL DEFAULT '[]', -- JSON array; "command" checks only
    threshold           REAL NOT NULL DEFAULT 0,
    interval_seconds    INTEGER NOT NULL,
    timeout_seconds     INTEGER NOT NULL,
    max_retries         INTEGER NOT NULL DEFAULT 0,
    retry_delay_seconds INTEGER NOT NULL DEFAULT 0,
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL,

    -- Live status columns, owned exclusively by UpdateResult (see
    -- internal/health.Repository's doc comment) -- Upsert never
    -- touches these, so editing a check's configuration never resets
    -- its observed history.
    state               TEXT NOT NULL DEFAULT 'UNKNOWN',
    message             TEXT NOT NULL DEFAULT '',
    last_success_at     TIMESTAMP,
    last_failure_at     TIMESTAMP,
    failure_count       INTEGER NOT NULL DEFAULT 0
);
