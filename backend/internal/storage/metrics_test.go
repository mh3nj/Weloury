package storage

import (
	"context"
	"testing"
	"time"

	"weloury/internal/metrics"
)

func TestMetricsRepositoryInsertBatchAndQuery(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewMetricsRepository(db)

	base := time.Now().UTC().Truncate(time.Second)
	batch := []metrics.Aggregate{
		{Metric: "cpu", Scope: "", BucketTime: base, Min: 10, Max: 30, Avg: 20, SampleCount: 5},
		{Metric: "cpu", Scope: "", BucketTime: base.Add(time.Minute), Min: 15, Max: 35, Avg: 25, SampleCount: 5},
		{Metric: "disk", Scope: "/", BucketTime: base, Min: 50, Max: 55, Avg: 52, SampleCount: 5},
	}
	if err := repo.InsertBatch(ctx, batch); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	cpuPoints, err := repo.Query(ctx, "cpu", "", base.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("Query cpu: %v", err)
	}
	if len(cpuPoints) != 2 {
		t.Fatalf("len(cpuPoints) = %d, want 2", len(cpuPoints))
	}
	if !cpuPoints[0].BucketTime.Equal(base) || cpuPoints[0].Avg != 20 {
		t.Errorf("cpuPoints[0] = %+v, want BucketTime=%v Avg=20", cpuPoints[0], base)
	}
	// Oldest first.
	if cpuPoints[1].BucketTime.Before(cpuPoints[0].BucketTime) {
		t.Errorf("expected oldest-first ordering, got %+v then %+v", cpuPoints[0], cpuPoints[1])
	}

	diskPoints, err := repo.Query(ctx, "disk", "/", base.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("Query disk: %v", err)
	}
	if len(diskPoints) != 1 || diskPoints[0].Avg != 52 {
		t.Errorf("diskPoints = %+v, want one point with Avg=52", diskPoints)
	}

	// A different scope for the same metric name must not be conflated
	// with disk at "/" - this is the whole point of Scope existing.
	otherScope, err := repo.Query(ctx, "disk", "/data", base.Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("Query disk /data: %v", err)
	}
	if len(otherScope) != 0 {
		t.Errorf("len(otherScope) = %d, want 0 (no data was ever inserted for that scope)", len(otherScope))
	}
}

func TestMetricsRepositoryQueryRespectsLimit(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewMetricsRepository(db)

	base := time.Now().UTC().Truncate(time.Second)
	var batch []metrics.Aggregate
	for i := 0; i < 10; i++ {
		batch = append(batch, metrics.Aggregate{
			Metric: "cpu", BucketTime: base.Add(time.Duration(i) * time.Minute),
			Min: 1, Max: 1, Avg: 1, SampleCount: 1,
		})
	}
	if err := repo.InsertBatch(ctx, batch); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	points, err := repo.Query(ctx, "cpu", "", base.Add(-time.Hour), 3)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("len(points) = %d, want 3 (limit respected)", len(points))
	}
}

func TestMetricsRepositoryQueryRespectsSince(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewMetricsRepository(db)

	base := time.Now().UTC().Truncate(time.Second)
	old := metrics.Aggregate{Metric: "cpu", BucketTime: base.Add(-time.Hour), Min: 1, Max: 1, Avg: 1, SampleCount: 1}
	recent := metrics.Aggregate{Metric: "cpu", BucketTime: base, Min: 2, Max: 2, Avg: 2, SampleCount: 1}
	if err := repo.InsertBatch(ctx, []metrics.Aggregate{old, recent}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	points, err := repo.Query(ctx, "cpu", "", base.Add(-time.Minute), 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 1 || points[0].Avg != 2 {
		t.Errorf("points = %+v, want only the recent aggregate", points)
	}
}

func TestMetricsRepositoryInsertBatchEmptyIsNoop(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewMetricsRepository(db)

	if err := repo.InsertBatch(ctx, nil); err != nil {
		t.Fatalf("InsertBatch(nil): %v", err)
	}
}

func TestMetricsRepositoryPruneOlderThan(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewMetricsRepository(db)

	base := time.Now().UTC().Truncate(time.Second)
	old := metrics.Aggregate{Metric: "cpu", BucketTime: base.Add(-48 * time.Hour), Min: 1, Max: 1, Avg: 1, SampleCount: 1}
	recent := metrics.Aggregate{Metric: "cpu", BucketTime: base, Min: 2, Max: 2, Avg: 2, SampleCount: 1}
	if err := repo.InsertBatch(ctx, []metrics.Aggregate{old, recent}); err != nil {
		t.Fatalf("InsertBatch: %v", err)
	}

	removed, err := repo.PruneOlderThan(ctx, base.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}

	remaining, err := repo.Query(ctx, "cpu", "", base.Add(-72*time.Hour), 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Avg != 2 {
		t.Errorf("remaining = %+v, want only the recent aggregate to survive pruning", remaining)
	}
}
