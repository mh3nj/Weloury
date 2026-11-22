// Package storage owns Weloury core's SQLite persistence: opening the
// database, running migrations, and the repositories built on top of it.
package storage

import (
	"context"
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver
)

// Open opens (creating if necessary) the SQLite database at path, applies
// Weloury's standard pragmas, runs pending migrations, and returns a ready
// to use *sql.DB.
//
// Weloury uses exactly one physical connection for the process
// (SetMaxOpenConns(1)), not a pool. This is deliberate, not merely
// "good enough for a local tool": database/sql applies pragmas - notably
// `PRAGMA busy_timeout`, below - to whichever single physical connection
// happens to execute that statement, not to every connection the pool
// might open later under concurrent load. A pool sized above 1 can
// silently hand a second, third, ... physical connection to a concurrent
// caller that never received busy_timeout, which then fails immediately
// with "database is locked" the instant it contends with a write instead
// of waiting - exactly the failure this project's own integration test
// (internal/app/app_test.go) caught under real concurrent load from the
// health/metrics/event-persistence subsystems all writing shortly after
// startup. A single connection makes that entire failure mode
// structurally impossible rather than merely unlikely. WAL mode
// (enabled below) is still worth it even with one connection: it's also
// what keeps a writer from blocking a reader that opens a *separate*
// short-lived connection (e.g. a future read-only tool inspecting the
// database file directly), and it's more crash-resilient than the
// default rollback journal.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %s: %w", path, err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0) // never force-recycle the one connection we have

	// Applied as plain SQL rather than DSN query parameters so this does
	// not depend on any particular driver's DSN pragma syntax. Safe to
	// apply once here specifically because MaxOpenConns(1) above
	// guarantees this is the only physical connection that will ever
	// exist for this *sql.DB - see Open's doc comment.
	pragmas := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA foreign_keys = ON;`,
		`PRAGMA busy_timeout = 5000;`,
	}
	for _, p := range pragmas {
		if _, err := db.ExecContext(ctx, p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("apply pragma %q: %w", p, err)
		}
	}

	if err := Migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return db, nil
}
