-- 0003_metric_aggregates.sql
--
-- Metrics engine (Phase 4, spec section 17). Deliberately holds only
-- aggregated buckets (min/avg/max per series per flush interval), never
-- raw per-sample rows -- see internal/metrics's package doc comment and
-- spec section 17's explicit "do not write every metric sample directly
-- to SQLite independently." Raw samples live only in
-- internal/metrics.Engine's in-memory rolling window (buffer.go); this
-- table is fed by its periodic flush.

CREATE TABLE metric_aggregates (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    bucket_time  TIMESTAMP NOT NULL,
    metric       TEXT NOT NULL,
    -- scope disambiguates multiple series sharing one metric name --
    -- e.g. a disk series keyed by mount point, or a network series
    -- keyed by interface name. Empty string for machine-wide series
    -- (cpu, memory).
    scope        TEXT NOT NULL DEFAULT '',
    min_value    REAL NOT NULL,
    max_value    REAL NOT NULL,
    avg_value    REAL NOT NULL,
    sample_count INTEGER NOT NULL
);

-- Covers Query's WHERE metric = ? AND scope = ? ORDER BY bucket_time,
-- the only access pattern this table serves besides PruneOlderThan
-- (which scans/deletes by bucket_time alone).
CREATE INDEX idx_metric_aggregates_series ON metric_aggregates (metric, scope, bucket_time);
CREATE INDEX idx_metric_aggregates_bucket_time ON metric_aggregates (bucket_time);
