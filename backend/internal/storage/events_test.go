package storage

import (
	"context"
	"testing"
	"time"

	"weloury/internal/eventbus"
)

func TestEventRepositoryInsertAndRecent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewEventRepository(db)

	e := eventbus.Event{
		ID:        "evt-1",
		Timestamp: time.Now().UTC(),
		Type:      "test.event",
		Source:    "unit-test",
		Severity:  eventbus.SeverityInfo,
	}
	if err := repo.Insert(ctx, e); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := repo.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(got) != 1 || got[0].ID != "evt-1" {
		t.Fatalf("Recent = %+v, want single event evt-1", got)
	}
}

func TestEventRepositoryPruneOlderThan(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewEventRepository(db)

	old := eventbus.Event{ID: "old", Timestamp: time.Now().Add(-48 * time.Hour), Type: "t", Source: "s", Severity: eventbus.SeverityInfo}
	recent := eventbus.Event{ID: "recent", Timestamp: time.Now(), Type: "t", Source: "s", Severity: eventbus.SeverityInfo}
	if err := repo.Insert(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := repo.Insert(ctx, recent); err != nil {
		t.Fatal(err)
	}

	n, err := repo.PruneOlderThan(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}

	remaining, err := repo.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 || remaining[0].ID != "recent" {
		t.Fatalf("remaining = %+v, want only 'recent'", remaining)
	}
}
