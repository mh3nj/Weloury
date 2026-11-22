package storage

import (
	"context"
	"testing"
	"time"

	"weloury/internal/health"
)

func newTestCheck(id string) health.Check {
	now := time.Now().UTC().Truncate(time.Second)
	return health.Check{
		ID:              id,
		Name:            "test check",
		Type:            health.CheckProcessExists,
		Target:          "worker",
		IntervalSeconds: 30,
		TimeoutSeconds:  5,
		Retry:           health.RetryPolicy{MaxRetries: 2, RetryDelaySeconds: 3},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestHealthRepositoryUpsertAndList(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewHealthRepository(db)

	c := newTestCheck("chk_1")
	if err := repo.Upsert(ctx, c); err != nil {
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
	if got.ID != c.ID || got.Name != c.Name || got.Type != c.Type || got.Target != c.Target {
		t.Errorf("got = %+v, want fields matching %+v", got, c)
	}
	if got.State != health.StateUnknown {
		t.Errorf("State = %v, want UNKNOWN for a freshly inserted check with no result yet", got.State)
	}
	if !got.CreatedAt.Equal(c.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, c.CreatedAt)
	}
}

func TestHealthRepositoryUpsertPreservesLiveStatusOnConfigUpdate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewHealthRepository(db)

	c := newTestCheck("chk_1")
	if err := repo.Upsert(ctx, c); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	checkedAt := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdateResult(ctx, c.ID, health.Result{State: health.StateUnhealthy, Message: "boom", CheckedAt: checkedAt}); err != nil {
		t.Fatalf("UpdateResult: %v", err)
	}

	// Re-upsert with a changed name/interval, simulating Engine.Update.
	c.Name = "renamed"
	c.IntervalSeconds = 60
	if err := repo.Upsert(ctx, c); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := list[0]
	if got.Name != "renamed" || got.IntervalSeconds != 60 {
		t.Errorf("config fields not updated: got = %+v", got)
	}
	if got.State != health.StateUnhealthy || got.FailureCount != 1 {
		t.Errorf("live status was reset by a config-only Upsert: got State=%v FailureCount=%d, want UNHEALTHY/1 preserved", got.State, got.FailureCount)
	}
}

func TestHealthRepositoryUpdateResultThreeWayRule(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewHealthRepository(db)

	c := newTestCheck("chk_1")
	if err := repo.Upsert(ctx, c); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	t1 := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdateResult(ctx, c.ID, health.Result{State: health.StateUnhealthy, Message: "down", CheckedAt: t1}); err != nil {
		t.Fatalf("UpdateResult 1: %v", err)
	}
	t2 := t1.Add(time.Minute)
	if err := repo.UpdateResult(ctx, c.ID, health.Result{State: health.StateUnhealthy, Message: "still down", CheckedAt: t2}); err != nil {
		t.Fatalf("UpdateResult 2: %v", err)
	}

	list, _ := repo.List(ctx)
	got := list[0]
	if got.FailureCount != 2 {
		t.Errorf("FailureCount = %d, want 2 after two consecutive failures", got.FailureCount)
	}
	if got.LastFailureAt == nil || !got.LastFailureAt.Equal(t2) {
		t.Errorf("LastFailureAt = %v, want %v", got.LastFailureAt, t2)
	}
	if got.LastSuccessAt != nil {
		t.Errorf("LastSuccessAt = %v, want nil (never succeeded)", got.LastSuccessAt)
	}

	// An UNKNOWN result must not touch either counter/timestamp.
	t3 := t2.Add(time.Minute)
	if err := repo.UpdateResult(ctx, c.ID, health.Result{State: health.StateUnknown, Message: "n/a", CheckedAt: t3}); err != nil {
		t.Fatalf("UpdateResult 3: %v", err)
	}
	list, _ = repo.List(ctx)
	got = list[0]
	if got.FailureCount != 2 {
		t.Errorf("FailureCount after an UNKNOWN result = %d, want unchanged 2", got.FailureCount)
	}
	if got.State != health.StateUnknown {
		t.Errorf("State = %v, want UNKNOWN (state itself always updates)", got.State)
	}

	// A HEALTHY result resets the failure streak.
	t4 := t3.Add(time.Minute)
	if err := repo.UpdateResult(ctx, c.ID, health.Result{State: health.StateHealthy, Message: "recovered", CheckedAt: t4}); err != nil {
		t.Fatalf("UpdateResult 4: %v", err)
	}
	list, _ = repo.List(ctx)
	got = list[0]
	if got.FailureCount != 0 {
		t.Errorf("FailureCount after recovery = %d, want 0", got.FailureCount)
	}
	if got.LastSuccessAt == nil || !got.LastSuccessAt.Equal(t4) {
		t.Errorf("LastSuccessAt = %v, want %v", got.LastSuccessAt, t4)
	}
}

func TestHealthRepositoryUpdateResultNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewHealthRepository(db)

	err := repo.UpdateResult(ctx, "nonexistent", health.Result{State: health.StateHealthy, CheckedAt: time.Now()})
	if err != health.ErrNotFound {
		t.Errorf("err = %v, want health.ErrNotFound", err)
	}
}

func TestHealthRepositoryDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewHealthRepository(db)

	c := newTestCheck("chk_1")
	if err := repo.Upsert(ctx, c); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Delete(ctx, c.ID); err != nil {
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

func TestHealthRepositoryCommandArgsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewHealthRepository(db)

	c := newTestCheck("chk_1")
	c.Type = health.CheckCommand
	c.Target = ""
	c.CommandArgs = []string{"/usr/bin/true", "--flag", "value with spaces"}
	if err := repo.Upsert(ctx, c); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := list[0].CommandArgs
	if len(got) != 3 || got[0] != "/usr/bin/true" || got[2] != "value with spaces" {
		t.Errorf("CommandArgs round-trip = %v, want %v", got, c.CommandArgs)
	}
}
