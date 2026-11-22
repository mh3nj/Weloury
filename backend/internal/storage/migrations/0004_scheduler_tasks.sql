-- 0004_scheduler_tasks.sql
--
-- Scheduler (Phase 5, spec section 12). One row per task, holding both
-- its configuration and its scheduling state (NextRunAt/LastRunAt) --
-- see internal/scheduler.Repository's doc comment on why there's no
-- separate "config" vs "live state" split the way health_checks has:
-- scheduler.Engine recomputes and persists both together on every
-- change (Create/Update/SetEnabled/fire), so there's no result-recording
-- method that could drift out of sync with a config-only Upsert the way
-- health's split exists to prevent.

CREATE TABLE scheduler_tasks (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL,
    type             TEXT NOT NULL,
    run_at           TIMESTAMP,
    interval_seconds INTEGER NOT NULL DEFAULT 0,
    enabled          INTEGER NOT NULL DEFAULT 1,
    next_run_at      TIMESTAMP,
    last_run_at      TIMESTAMP,
    created_at       TIMESTAMP NOT NULL,
    updated_at       TIMESTAMP NOT NULL
);

CREATE INDEX idx_scheduler_tasks_next_run ON scheduler_tasks (enabled, next_run_at);
