package app

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"strings"
	"testing"
	"time"

	"weloury/internal/config"
	"weloury/internal/eventbus"
	"weloury/internal/metrics"
	"weloury/internal/storage"
)

// retentionTestApp builds just enough of an App to exercise retention.go
// in isolation - a real migrated SQLite database (via the sandbox's
// CGO-backed stub driver, same as every other storage-touching test in
// this codebase - see DEVELOPMENT.md), a real EventRepository and
// MetricsRepository against it, and a buffer-backed logger so warnings
// can be asserted on. Deliberately does not go through New()/Run(): no
// IPC server, no engines, no background goroutines - this exists to
// test pruneOnce/runRetentionPruner alone, not the whole app.
func retentionTestApp(t *testing.T, eventsDays, metricsDays int) (*App, *sql.DB, *bytes.Buffer) {
	t.Helper()

	db, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("storage.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))

	a := &App{
		cfg: config.Config{
			EventsRetentionDays:  eventsDays,
			MetricsRetentionDays: metricsDays,
		},
		logger:      logger,
		events:      storage.NewEventRepository(db),
		metricsRepo: storage.NewMetricsRepository(db),
	}
	return a, db, &logBuf
}

func insertEventAt(t *testing.T, a *App, id string, ts time.Time) {
	t.Helper()
	err := a.events.Insert(context.Background(), eventbus.Event{
		ID:        id,
		Timestamp: ts,
		Type:      "test.event",
		Source:    "retention-test",
		Severity:  eventbus.SeverityInfo,
	})
	if err != nil {
		t.Fatalf("insert event %s: %v", id, err)
	}
}

func insertAggregateAt(t *testing.T, a *App, ts time.Time) {
	t.Helper()
	err := a.metricsRepo.InsertBatch(context.Background(), []metrics.Aggregate{{
		Metric:      "cpu.usage_percent",
		Scope:       "system",
		BucketTime:  ts,
		Min:         1,
		Max:         2,
		Avg:         1.5,
		SampleCount: 1,
	}})
	if err != nil {
		t.Fatalf("insert aggregate at %s: %v", ts, err)
	}
}

func countRows(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	// table is always one of the two fixed literals below, never
	// user input, so this is not a SQL-injection concern.
	if err := db.QueryRow(`SELECT COUNT(*) FROM ` + table + `;`).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// TestPruneOncePrunesExpiredEventsAndMetrics is the core behavior: rows
// older than the configured retention window are deleted, rows within it
// are kept, for both events and metric aggregates.
func TestPruneOncePrunesExpiredEventsAndMetrics(t *testing.T) {
	a, db, _ := retentionTestApp(t, 30, 14)

	insertEventAt(t, a, "old", time.Now().Add(-60*24*time.Hour))
	insertEventAt(t, a, "recent", time.Now())
	insertAggregateAt(t, a, time.Now().Add(-20*24*time.Hour))
	insertAggregateAt(t, a, time.Now())

	a.pruneOnce(context.Background())

	if n := countRows(t, db, "events"); n != 1 {
		t.Errorf("events remaining = %d, want 1 (only the recent one)", n)
	}
	if n := countRows(t, db, "metric_aggregates"); n != 1 {
		t.Errorf("metric_aggregates remaining = %d, want 1 (only the recent one)", n)
	}
}

// TestPruneOnceZeroOrNegativeRetentionDisablesPruning covers the "<=0
// disables" guard on both branches: with EventsRetentionDays=0 and
// MetricsRetentionDays=-1, pruneOnce must not delete anything at all,
// no matter how old the rows are.
func TestPruneOnceZeroOrNegativeRetentionDisablesPruning(t *testing.T) {
	a, db, _ := retentionTestApp(t, 0, -1)

	ancient := time.Now().Add(-10 * 365 * 24 * time.Hour)
	insertEventAt(t, a, "ancient", ancient)
	insertAggregateAt(t, a, ancient)

	a.pruneOnce(context.Background())

	if n := countRows(t, db, "events"); n != 1 {
		t.Errorf("events remaining = %d, want 1 (pruning disabled, nothing should be deleted)", n)
	}
	if n := countRows(t, db, "metric_aggregates"); n != 1 {
		t.Errorf("metric_aggregates remaining = %d, want 1 (pruning disabled, nothing should be deleted)", n)
	}
}

// TestPruneOnceOneRetentionDisabledOneEnabled checks the two guards are
// independent: disabling event retention must not also suppress metrics
// retention, and vice versa is covered implicitly by the "both enabled"
// test above.
func TestPruneOnceOneRetentionDisabledOneEnabled(t *testing.T) {
	a, db, _ := retentionTestApp(t, 0, 14)

	old := time.Now().Add(-30 * 24 * time.Hour)
	insertEventAt(t, a, "old-event", old)
	insertAggregateAt(t, a, old)

	a.pruneOnce(context.Background())

	if n := countRows(t, db, "events"); n != 1 {
		t.Errorf("events remaining = %d, want 1 (event retention disabled)", n)
	}
	if n := countRows(t, db, "metric_aggregates"); n != 0 {
		t.Errorf("metric_aggregates remaining = %d, want 0 (metrics retention enabled and exceeded)", n)
	}
}

// TestPruneOnceLogsWarningOnErrorWithoutPanicking exercises the error
// path on both branches: PruneOlderThan failing must be logged as a
// warning and must not panic or otherwise abort pruneOnce, which is
// called from an unattended background ticker with nothing else
// watching for a panic.
func TestPruneOnceLogsWarningOnErrorWithoutPanicking(t *testing.T) {
	a, db, logBuf := retentionTestApp(t, 30, 14)

	// Force both PruneOlderThan calls to fail by closing the
	// database out from under them, a real (if unusual) failure mode
	// - e.g. the database file becoming unavailable mid-sweep.
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("pruneOnce panicked on a database error: %v", r)
			}
		}()
		a.pruneOnce(context.Background())
	}()

	got := logBuf.String()
	if !strings.Contains(got, "event retention sweep failed") {
		t.Errorf("log output missing event sweep failure warning; got: %s", got)
	}
	if !strings.Contains(got, "metrics retention sweep failed") {
		t.Errorf("log output missing metrics sweep failure warning; got: %s", got)
	}
}

// TestRunRetentionPrunerSweepsOnceAtStartup verifies the documented
// "sweep once at startup so a long-idle install isn't stuck waiting a
// full interval" behavior: an expired row must be pruned promptly after
// runRetentionPruner starts, without waiting anywhere near
// retentionSweepInterval (1 hour) for the ticker to fire.
func TestRunRetentionPrunerSweepsOnceAtStartup(t *testing.T) {
	a, db, _ := retentionTestApp(t, 30, 14)
	insertEventAt(t, a, "old", time.Now().Add(-60*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.runRetentionPruner(ctx)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		if countRows(t, db, "events") == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup sweep did not prune the expired event within 5s")
		}
		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runRetentionPruner did not return within 2s of context cancellation")
	}
}

// TestRunRetentionPrunerRespectsCancellationBeforeFirstSweep makes sure
// an already-canceled context is honored rather than the pruner running
// its startup sweep regardless. pruneOnce derives its own 10s timeout
// from ctx (see pruneOnce), so a canceled parent must still make the
// database calls fail fast rather than block for the full 10s.
func TestRunRetentionPrunerRespectsCancellationBeforeFirstSweep(t *testing.T) {
	a, _, _ := retentionTestApp(t, 30, 14)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before Start is ever called

	done := make(chan struct{})
	go func() {
		defer close(done)
		a.runRetentionPruner(ctx)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runRetentionPruner did not return promptly when started with an already-canceled context")
	}
}
