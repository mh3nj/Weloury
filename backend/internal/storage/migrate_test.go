package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"testing"
)

// openTestDB opens an in-memory SQLite database restricted to a single
// connection. SQLite's :memory: databases are per-connection; without
// SetMaxOpenConns(1), database/sql's pool could hand different goroutines
// different (and therefore inconsistent) in-memory databases.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMigrateCreatesEventsTable(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var name string
	err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name='events';`).Scan(&name)
	if err != nil {
		t.Fatalf("expected events table to exist: %v", err)
	}
}

// TestMigrateCreatesPhase4Tables guards against a Phase 4 migration file
// existing but never being embedded/applied - e.g. a typo in the
// filename pattern migrate.go's fs.ReadDir picks up.
func TestMigrateCreatesPhase4Tables(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{"health_checks", "metric_aggregates"} {
		var name string
		err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name = ?;`, table).Scan(&name)
		if err != nil {
			t.Errorf("expected table %q to exist: %v", table, err)
		}
	}
}

// TestMigrateCreatesPhase5Tables is TestMigrateCreatesPhase4Tables' twin
// for the scheduler/automation migrations.
func TestMigrateCreatesPhase5Tables(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	for _, table := range []string{"scheduler_tasks", "automation_rules"} {
		var name string
		err := db.QueryRowContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name = ?;`, table).Scan(&name)
		if err != nil {
			t.Errorf("expected table %q to exist: %v", table, err)
		}
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("second Migrate should be a no-op, got error: %v", err)
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations;`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	// Compared against the number of embedded migration files rather
	// than a hardcoded literal: this test's job is "re-running Migrate
	// doesn't reapply anything," not "there are exactly N migrations,"
	// and the latter would need editing every time a phase adds a
	// migration - exactly what happened when Phase 4 added
	// 0002_health_checks.sql/0003_metric_aggregates.sql alongside
	// Phase 1's 0001_init.sql.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	if count != len(entries) {
		t.Fatalf("schema_migrations count = %d, want %d (one row per migration file, re-running Migrate must not reapply)", count, len(entries))
	}
}
