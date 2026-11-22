-- 0001_init.sql
--
-- Initial schema: persisted event history.
--
-- Weloury's event bus (internal/eventbus) is purely in-memory and does not
-- persist anything itself; the application layer (internal/app) subscribes
-- to the bus and writes every event here so the Events screen (Phase 4+)
-- has history across restarts, and so automation rules (Phase 5) can be
-- evaluated against what actually happened rather than only live traffic.
--
-- Deliberately minimal for Phase 1: tables for health checks, automation
-- rules, schedules, and metric aggregates are added in the migrations that
-- introduce the code exercising them (Phase 4/5), not speculatively here.

CREATE TABLE events (
    id         TEXT PRIMARY KEY,
    timestamp  TIMESTAMP NOT NULL,
    type       TEXT NOT NULL,
    source     TEXT NOT NULL,
    severity   TEXT NOT NULL,
    payload    TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX idx_events_timestamp ON events (timestamp);
CREATE INDEX idx_events_type ON events (type);
