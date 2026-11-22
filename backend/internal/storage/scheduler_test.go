package storage

import (
	"context"
	"testing"
	"time"

	"weloury/internal/scheduler"
)

func newTestTask(id string) scheduler.Task {
	now := time.Now().UTC().Truncate(time.Second)
	return scheduler.Task{
		ID:              id,
		Name:            "test task",
		Type:            scheduler.Interval,
		IntervalSeconds: 60,
		Enabled:         true,
		NextRunAt:       now.Add(time.Minute),
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestSchedulerRepositoryUpsertAndList(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewSchedulerRepository(db)

	task := newTestTask("task_1")
	if err := repo.Upsert(ctx, task); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	got := list[0]
	if got.ID != task.ID || got.Name != task.Name || got.Type != task.Type || got.IntervalSeconds != task.IntervalSeconds {
		t.Errorf("got = %+v, want fields matching %+v", got, task)
	}
	if !got.Enabled {
		t.Error("expected Enabled to round-trip true")
	}
	if !got.NextRunAt.Equal(task.NextRunAt) {
		t.Errorf("NextRunAt = %v, want %v", got.NextRunAt, task.NextRunAt)
	}
	if got.LastRunAt != nil {
		t.Errorf("LastRunAt = %v, want nil (never run)", got.LastRunAt)
	}
}

func TestSchedulerRepositoryOnceTaskRunAtRoundTrips(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewSchedulerRepository(db)

	runAt := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	task := newTestTask("task_once")
	task.Type = scheduler.Once
	task.IntervalSeconds = 0
	task.RunAt = runAt
	task.NextRunAt = runAt

	if err := repo.Upsert(ctx, task); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !list[0].RunAt.Equal(runAt) {
		t.Errorf("RunAt = %v, want %v", list[0].RunAt, runAt)
	}
}

func TestSchedulerRepositoryUpdatePreservesID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewSchedulerRepository(db)

	task := newTestTask("task_1")
	if err := repo.Upsert(ctx, task); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	task.LastRunAt = &now
	task.Enabled = false
	if err := repo.Upsert(ctx, task); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1 (upsert, not insert)", len(list))
	}
	if list[0].Enabled {
		t.Error("expected Enabled=false to persist")
	}
	if list[0].LastRunAt == nil || !list[0].LastRunAt.Equal(now) {
		t.Errorf("LastRunAt = %v, want %v", list[0].LastRunAt, now)
	}
}

func TestSchedulerRepositoryDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewSchedulerRepository(db)

	task := newTestTask("task_1")
	if err := repo.Upsert(ctx, task); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Delete(ctx, task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("len(list) = %d, want 0 after Delete", len(list))
	}
}
