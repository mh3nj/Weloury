package metrics

import (
	"context"
	"sync"
	"testing"
	"time"

	"weloury/internal/system"
)

// fakeSys implements sysCapabilities entirely in memory - mirrors
// internal/health's fakeSys in spirit, letting Engine be tested without
// a real OS provider. All fields are read under fakeSys.mu so a test can
// mutate them (e.g. change network counters between collectNetwork
// calls) from the test goroutine while collectors run concurrently.
type fakeSys struct {
	mu sync.Mutex

	cpu    system.CPUUsage
	cpuErr error

	mem    system.MemoryInfo
	memErr error

	disks    []system.DiskInfo
	disksErr error

	netCounters []system.NetIOCounters
	netErr      error
}

func (f *fakeSys) CPUUsage(context.Context, time.Duration) (system.CPUUsage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cpu, f.cpuErr
}
func (f *fakeSys) MemoryInfo(context.Context) (system.MemoryInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mem, f.memErr
}
func (f *fakeSys) Disks(context.Context) ([]system.DiskInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.disks, f.disksErr
}
func (f *fakeSys) NetworkIOCounters(context.Context) ([]system.NetIOCounters, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.netCounters, f.netErr
}

func (f *fakeSys) setNetCounters(c []system.NetIOCounters) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.netCounters = c
}

// fakeRepository is a complete in-memory Repository - same
// dependency-inversion payoff as internal/health's fakeRepository,
// exercised here so Engine's flush/query path is tested without
// internal/storage/modernc.org/sqlite.
type fakeRepository struct {
	mu         sync.Mutex
	aggregates []Aggregate
	insertErr  error
}

func (r *fakeRepository) InsertBatch(_ context.Context, aggs []Aggregate) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insertErr != nil {
		return r.insertErr
	}
	r.aggregates = append(r.aggregates, aggs...)
	return nil
}

func (r *fakeRepository) Query(_ context.Context, metric, scope string, since time.Time, limit int) ([]Aggregate, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []Aggregate
	for _, a := range r.aggregates {
		if a.Metric == metric && a.Scope == scope && !a.BucketTime.Before(since) {
			out = append(out, a)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (r *fakeRepository) PruneOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kept []Aggregate
	var removed int64
	for _, a := range r.aggregates {
		if a.BucketTime.Before(cutoff) {
			removed++
			continue
		}
		kept = append(kept, a)
	}
	r.aggregates = kept
	return removed, nil
}

func (r *fakeRepository) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.aggregates)
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !cond() {
		t.Fatal("condition not met within timeout")
	}
}

func newTestEngine(sys sysCapabilities) (*Engine, *fakeRepository) {
	repo := &fakeRepository{}
	e := New(sys, repo, nil)
	// Tests use short intervals so they don't have to wait out the
	// 5s/30s/60s production defaults - see the doc comment on Engine's
	// interval fields in engine.go for why these are plain unexported
	// fields rather than a public Options type: only same-package tests
	// need to override them.
	e.cpuMemoryInterval = 20 * time.Millisecond
	e.diskInterval = 20 * time.Millisecond
	e.networkInterval = 20 * time.Millisecond
	e.flushInterval = 50 * time.Millisecond
	return e, repo
}

func TestEngineCollectsCPUAndMemoryImmediately(t *testing.T) {
	sys := &fakeSys{cpu: system.CPUUsage{OverallPercent: 42}, mem: system.MemoryInfo{TotalBytes: 100, UsedBytes: 60}}
	e, _ := newTestEngine(sys)

	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)

	waitFor(t, time.Second, func() bool {
		cur := e.Current()
		found := 0
		for _, c := range cur {
			if c.Metric == string(CPU) && c.Value == 42 {
				found++
			}
			if c.Metric == string(Memory) && c.Value == 60 {
				found++
			}
		}
		return found == 2
	})

	cancel()
	e.Wait()
}

func TestEngineCollectsPerMountDiskUsage(t *testing.T) {
	sys := &fakeSys{disks: []system.DiskInfo{
		{MountPoint: "/", TotalBytes: 100, UsedBytes: 50},
		{MountPoint: "/data", TotalBytes: 200, UsedBytes: 20},
	}}
	e, _ := newTestEngine(sys)

	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)

	waitFor(t, time.Second, func() bool {
		root := e.Rolling(Disk, "/")
		data := e.Rolling(Disk, "/data")
		return len(root) > 0 && len(data) > 0 &&
			root[len(root)-1].Value == 50 && data[len(data)-1].Value == 10
	})

	cancel()
	e.Wait()
}

func TestEngineComputesNetworkThroughputFromTwoSamples(t *testing.T) {
	sys := &fakeSys{netCounters: []system.NetIOCounters{{Name: "eth0", RxBytes: 1000, TxBytes: 500}}}
	e, _ := newTestEngine(sys)
	e.networkInterval = 30 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)

	// First tick establishes a baseline (no rate yet). Advance the
	// counters and wait for a rate sample to appear.
	time.Sleep(40 * time.Millisecond)
	sys.setNetCounters([]system.NetIOCounters{{Name: "eth0", RxBytes: 2000, TxBytes: 500}})

	waitFor(t, time.Second, func() bool {
		samples := e.Rolling(NetworkRx, "eth0")
		for _, s := range samples {
			if s.Value > 0 {
				return true
			}
		}
		return false
	})

	cancel()
	e.Wait()
}

func TestRateOrZeroHandlesCounterReset(t *testing.T) {
	if got := rateOrZero(1000, 500, 5); got != 0 {
		t.Errorf("rateOrZero(1000, 500, 5) = %v, want 0 for a decreasing counter", got)
	}
	if got := rateOrZero(1000, 2000, 5); got != 200 {
		t.Errorf("rateOrZero(1000, 2000, 5) = %v, want 200", got)
	}
}

func TestEngineFlushesToRepository(t *testing.T) {
	sys := &fakeSys{cpu: system.CPUUsage{OverallPercent: 10}, mem: system.MemoryInfo{TotalBytes: 100, UsedBytes: 10}}
	e, repo := newTestEngine(sys)

	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)

	waitFor(t, 2*time.Second, func() bool { return repo.count() > 0 })

	cancel()
	e.Wait()
}

func TestEngineFinalFlushOnShutdown(t *testing.T) {
	sys := &fakeSys{cpu: system.CPUUsage{OverallPercent: 10}, mem: system.MemoryInfo{TotalBytes: 100, UsedBytes: 10}}
	repo := &fakeRepository{}
	e := New(sys, repo, nil)
	// A flush interval longer than the test itself would ever wait for
	// on its own - the only way repo gets data is the shutdown flush.
	e.cpuMemoryInterval = 10 * time.Millisecond
	e.diskInterval = time.Hour
	e.networkInterval = time.Hour
	e.flushInterval = time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)
	time.Sleep(50 * time.Millisecond) // let a couple of CPU/memory samples land
	cancel()
	e.Wait()

	if repo.count() == 0 {
		t.Error("expected the shutdown flush to persist pending samples even though flushInterval never elapsed")
	}
}

func TestEngineQueryReturnsPersistedPoints(t *testing.T) {
	repo := &fakeRepository{aggregates: []Aggregate{
		{Metric: "cpu", BucketTime: time.Now().Add(-time.Minute), Min: 1, Max: 5, Avg: 3, SampleCount: 4},
	}}
	e := New(&fakeSys{}, repo, nil)

	points, err := e.Query(context.Background(), CPU, "", time.Now().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(points) != 1 || points[0].Avg != 3 {
		t.Errorf("points = %+v, want one point with Avg=3", points)
	}
}

func TestEngineWaitReturnsAfterContextCancel(t *testing.T) {
	e, _ := newTestEngine(&fakeSys{})
	ctx, cancel := context.WithCancel(context.Background())
	e.Start(ctx)
	cancel()

	done := make(chan struct{})
	go func() { e.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return promptly after context cancellation - possible goroutine leak")
	}
}

func TestBufferAggregateComputesMinMaxAvg(t *testing.T) {
	b := newBuffer(10)
	now := time.Now()
	for _, v := range []float64{10, 20, 30} {
		b.record(Sample{Metric: CPU, Value: v, Timestamp: now})
	}
	aggs := b.drainForFlush(now)
	if len(aggs) != 1 {
		t.Fatalf("len(aggs) = %d, want 1", len(aggs))
	}
	a := aggs[0]
	if a.Min != 10 || a.Max != 30 || a.Avg != 20 || a.SampleCount != 3 {
		t.Errorf("aggregate = %+v, want Min=10 Max=30 Avg=20 SampleCount=3", a)
	}
}

func TestBufferDrainClearsAccumulatorButNotRolling(t *testing.T) {
	b := newBuffer(10)
	now := time.Now()
	b.record(Sample{Metric: CPU, Value: 5, Timestamp: now})

	first := b.drainForFlush(now)
	if len(first) != 1 {
		t.Fatalf("len(first) = %d, want 1", len(first))
	}

	// Nothing new recorded -- a second drain immediately after should
	// produce no aggregate (pending is empty).
	second := b.drainForFlush(now)
	if len(second) != 0 {
		t.Fatalf("len(second) = %d, want 0 (accumulator was drained)", len(second))
	}

	// But the rolling window must still have the original sample --
	// flushing must never wipe out what Current()/Rolling() serve.
	rolling := b.rolling(CPU, "")
	if len(rolling) != 1 {
		t.Fatalf("rolling window len = %d, want 1 (flush must not clear the rolling window)", len(rolling))
	}
}

func TestBufferRingEvictsOldestBeyondCapacity(t *testing.T) {
	b := newBuffer(3)
	now := time.Now()
	for i := 0; i < 5; i++ {
		b.record(Sample{Metric: CPU, Value: float64(i), Timestamp: now})
	}
	rolling := b.rolling(CPU, "")
	if len(rolling) != 3 {
		t.Fatalf("len(rolling) = %d, want 3 (capacity-bounded)", len(rolling))
	}
	if rolling[0].Value != 2 || rolling[2].Value != 4 {
		t.Errorf("rolling = %+v, want oldest-first [2 3 4]", rolling)
	}
}
