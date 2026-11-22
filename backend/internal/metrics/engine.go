package metrics

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"weloury/internal/system"
)

// sysCapabilities is the minimal slice of system.Provider the metrics
// engine needs - the same interface-segregation approach
// internal/health's sysCapabilities uses, for the same reason: it keeps
// Engine's dependency honest and lets tests fake exactly four methods
// instead of system.Provider's full surface. system.Provider satisfies
// this automatically.
type sysCapabilities interface {
	CPUUsage(ctx context.Context, sampleWindow time.Duration) (system.CPUUsage, error)
	MemoryInfo(ctx context.Context) (system.MemoryInfo, error)
	Disks(ctx context.Context) ([]system.DiskInfo, error)
	NetworkIOCounters(ctx context.Context) ([]system.NetIOCounters, error)
}

// Default collection/flush intervals - spec section 5: "different
// metrics should have appropriate collection intervals." CPU and memory
// are cheap, in-memory-only reads (Linux: a /proc read; Darwin/Windows:
// a syscall) sampled every 5s; disk usage requires a syscall per mounted
// filesystem and changes slowly, so it's sampled every 30s; network
// throughput needs to be sampled often enough for a meaningful rate, so
// it matches CPU/memory at 5s. flushInterval controls how often the
// in-memory accumulator is aggregated and persisted - see buffer.go.
const (
	defaultCPUMemoryInterval = 5 * time.Second
	defaultDiskInterval      = 30 * time.Second
	defaultNetworkInterval   = 5 * time.Second
	defaultFlushInterval     = 60 * time.Second

	// defaultRollingCapacity bounds each series' in-memory rolling
	// window. At the 5s intervals above, 360 samples is 30 minutes -
	// long enough for a genuinely useful "recent trend" view without
	// the buffer growing without bound (spec section 26: "log
	// streaming should not accumulate unlimited memory," the same
	// principle applied here to metric sampling).
	defaultRollingCapacity = 360

	// cpuUsageSampleWindow mirrors internal/app/methods.go's constant
	// of the same name and internal/health/checkers.go's - the same
	// short, UI-appropriate window for a CPUUsage call, reused a third
	// time here since a metrics engine sample is exactly the same kind
	// of call.
	cpuUsageSampleWindow = 300 * time.Millisecond
)

// Engine runs the metrics engine's collection and flush loops. The zero
// value is not usable; construct with New.
type Engine struct {
	sys    sysCapabilities
	repo   Repository
	logger *slog.Logger
	buf    *buffer
	wg     sync.WaitGroup

	cpuMemoryInterval time.Duration
	diskInterval      time.Duration
	networkInterval   time.Duration
	flushInterval     time.Duration

	netMu           sync.Mutex
	prevNetCounters map[string]system.NetIOCounters
	prevNetTime     time.Time
}

// New constructs an Engine. sys and repo must be non-nil; logger may be
// nil (a nil logger simply means collection errors go unlogged, used by
// tests that don't care).
func New(sys sysCapabilities, repo Repository, logger *slog.Logger) *Engine {
	return &Engine{
		sys:               sys,
		repo:              repo,
		logger:            logger,
		buf:               newBuffer(defaultRollingCapacity),
		cpuMemoryInterval: defaultCPUMemoryInterval,
		diskInterval:      defaultDiskInterval,
		networkInterval:   defaultNetworkInterval,
		flushInterval:     defaultFlushInterval,
	}
}

// Start begins every collection and flush loop, all lifetime-scoped to
// ctx. It does not block - call Wait to block until every loop has
// exited after canceling ctx.
func (e *Engine) Start(ctx context.Context) {
	e.wg.Add(4)
	go e.runLoop(ctx, e.cpuMemoryInterval, e.collectCPUMemory)
	go e.runLoop(ctx, e.diskInterval, e.collectDisk)
	go e.runLoop(ctx, e.networkInterval, e.collectNetwork)
	go e.runFlushLoop(ctx)
}

// Wait blocks until every loop started by Start has exited.
func (e *Engine) Wait() { e.wg.Wait() }

// runLoop is the shared shape of every collector: sample once
// immediately (so Current() has data right away rather than after a
// full interval - same reasoning as internal/health's runLoop and
// internal/app's retention pruner), then on every tick until ctx is
// canceled.
func (e *Engine) runLoop(ctx context.Context, interval time.Duration, collect func(context.Context)) {
	defer e.wg.Done()
	collect(ctx)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collect(ctx)
		}
	}
}

func (e *Engine) collectCPUMemory(ctx context.Context) {
	sampleCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	now := time.Now().UTC()

	if usage, err := e.sys.CPUUsage(sampleCtx, cpuUsageSampleWindow); err == nil {
		e.buf.record(Sample{Metric: CPU, Value: usage.OverallPercent, Timestamp: now})
	} else {
		e.logCollectionError("cpu", err)
	}

	if mem, err := e.sys.MemoryInfo(sampleCtx); err == nil {
		e.buf.record(Sample{Metric: Memory, Value: percentUsed(mem.UsedBytes, mem.TotalBytes), Timestamp: now})
	} else {
		e.logCollectionError("memory", err)
	}
}

func (e *Engine) collectDisk(ctx context.Context) {
	sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	disks, err := e.sys.Disks(sampleCtx)
	if err != nil {
		e.logCollectionError("disk", err)
		return
	}
	now := time.Now().UTC()
	for _, d := range disks {
		if d.TotalBytes == 0 {
			continue
		}
		e.buf.record(Sample{Metric: Disk, Scope: d.MountPoint, Value: percentUsed(d.UsedBytes, d.TotalBytes), Timestamp: now})
	}
}

// collectNetwork samples cumulative byte counters and, if a prior sample
// exists, records a bytes/sec rate for every interface present in both
// readings - see system.NetIOCounters's doc comment on why the rate is
// computed here rather than in internal/system.
func (e *Engine) collectNetwork(ctx context.Context) {
	sampleCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	counters, err := e.sys.NetworkIOCounters(sampleCtx)
	if err != nil {
		e.logCollectionError("network", err)
		return
	}
	now := time.Now().UTC()

	e.netMu.Lock()
	defer e.netMu.Unlock()

	if !e.prevNetTime.IsZero() {
		if elapsed := now.Sub(e.prevNetTime).Seconds(); elapsed > 0 {
			for _, c := range counters {
				prev, ok := e.prevNetCounters[c.Name]
				if !ok {
					continue // first time seeing this interface; no prior reading to diff against
				}
				e.buf.record(Sample{Metric: NetworkRx, Scope: c.Name, Value: rateOrZero(prev.RxBytes, c.RxBytes, elapsed), Timestamp: now})
				e.buf.record(Sample{Metric: NetworkTx, Scope: c.Name, Value: rateOrZero(prev.TxBytes, c.TxBytes, elapsed), Timestamp: now})
			}
		}
	}

	prevMap := make(map[string]system.NetIOCounters, len(counters))
	for _, c := range counters {
		prevMap[c.Name] = c
	}
	e.prevNetCounters = prevMap
	e.prevNetTime = now
}

func (e *Engine) runFlushLoop(ctx context.Context) {
	defer e.wg.Done()
	ticker := time.NewTicker(e.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Flush once more on the way out, using a fresh context
			// (ctx is already canceled), so the last partial interval
			// of samples isn't silently dropped on shutdown.
			e.flush(context.Background())
			return
		case <-ticker.C:
			e.flush(ctx)
		}
	}
}

func (e *Engine) flush(ctx context.Context) {
	aggregates := e.buf.drainForFlush(time.Now().UTC())
	if len(aggregates) == 0 {
		return
	}
	flushCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := e.repo.InsertBatch(flushCtx, aggregates); err != nil && e.logger != nil {
		e.logger.Warn("failed to persist metric aggregates", "count", len(aggregates), "error", err)
	}
}

// Current returns the latest sample for every active series - the data
// an Overview-style live dashboard polls or the frontend renders
// straight from a metrics.current FWP call, with no DB round-trip.
func (e *Engine) Current() []Current { return e.buf.current() }

// Rolling returns every in-memory sample for one series, oldest first -
// see buffer.rolling.
func (e *Engine) Rolling(metric Name, scope string) []Sample { return e.buf.rolling(metric, scope) }

// Query returns persisted historical aggregates for one series via
// Repository.
func (e *Engine) Query(ctx context.Context, metric Name, scope string, since time.Time, limit int) ([]Point, error) {
	aggregates, err := e.repo.Query(ctx, string(metric), scope, since, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Point, len(aggregates))
	for i, a := range aggregates {
		out[i] = Point{Timestamp: a.BucketTime, Avg: a.Avg, Min: a.Min, Max: a.Max}
	}
	return out, nil
}

func percentUsed(used, total uint64) float64 {
	if total == 0 {
		return 0
	}
	return float64(used) / float64(total) * 100
}

// rateOrZero computes a bytes/sec rate from two cumulative counter
// readings. A decreasing counter (cur < prev) means the counter wrapped
// or was reset between readings - see NetIOCounters's doc comment -
// and is reported as zero for this tick rather than a nonsensical
// negative or enormous positive rate; the next tick's reading resumes
// normal computation from the new baseline automatically.
func rateOrZero(prev, cur uint64, elapsedSeconds float64) float64 {
	if cur < prev {
		return 0
	}
	return float64(cur-prev) / elapsedSeconds
}

// logCollectionError logs a collector failure at Debug level for the
// expected, permanent "not available on this platform" case (Darwin/
// Windows CPUUsage and NetworkIOCounters - see internal/system) and at
// Warn for anything else, so a genuine, unexpected failure is visible
// without every collection tick on an unsupported platform spamming the
// log at Warn forever.
func (e *Engine) logCollectionError(series string, err error) {
	if e.logger == nil {
		return
	}
	if errors.Is(err, system.ErrUnavailable) {
		e.logger.Debug("metric collection unavailable on this platform", "series", series, "error", err)
		return
	}
	e.logger.Warn("metric collection failed", "series", series, "error", err)
}
