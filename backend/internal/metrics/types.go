// Package metrics is Weloury's metrics engine (spec section 17): periodic,
// per-metric-appropriate-interval collection of CPU/memory/disk/network
// utilization, kept as an in-memory rolling window for "current"/
// "rolling" queries and periodically flushed as aggregated (min/avg/max)
// buckets to persistent storage for "historical" queries - never as raw
// per-sample rows, which spec section 17 explicitly rules out ("do not
// write every metric sample directly to SQLite independently").
//
// Engine (engine.go) owns three independent collection loops (CPU+memory,
// disk, network), each on its own interval - spec section 5: "different
// metrics should have appropriate collection intervals" - plus one flush
// loop that periodically aggregates and persists what's accumulated
// in-memory. Repository (repository.go) is a consumer-defined interface,
// the same dependency-inversion pattern internal/health uses, so Engine
// is unit-testable without internal/storage/modernc.org/sqlite - see
// DEVELOPMENT.md.
package metrics

import "time"

// Name identifies a metric series. Combined with Scope (empty for
// machine-wide series like CPU/memory; a mount point or interface name
// for per-resource series like disk/network) to form a unique series
// key - see seriesKey in buffer.go.
type Name string

const (
	CPU       Name = "cpu"
	Memory    Name = "memory"
	Disk      Name = "disk"       // scoped by mount point
	NetworkRx Name = "network_rx" // scoped by interface name, bytes/sec
	NetworkTx Name = "network_tx" // scoped by interface name, bytes/sec
)

// Sample is one raw, in-memory reading. Never persisted directly - see
// the package doc comment.
type Sample struct {
	Metric    Name      `json:"metric"`
	Scope     string    `json:"scope,omitempty"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}

// Aggregate is one flushed bucket: every raw Sample for one series that
// arrived within a bucket window, reduced to min/avg/max. This is
// exactly what Repository persists and Query returns.
type Aggregate struct {
	Metric      string
	Scope       string
	BucketTime  time.Time
	Min         float64
	Max         float64
	Avg         float64
	SampleCount int
}

// Point is one value in a Query result - Aggregate stripped down to what
// a history chart actually plots. Kept as a separate type (rather than
// reusing Aggregate) so FWP's wire shape doesn't couple to internal
// aggregation bookkeeping (SampleCount, Min/Max) that today's frontend
// has no use for but a future one might display differently.
type Point struct {
	Timestamp time.Time `json:"timestamp"`
	Avg       float64   `json:"avg"`
	Min       float64   `json:"min"`
	Max       float64   `json:"max"`
}

// Current is a snapshot of every series' latest raw sample - the
// backing data for an Overview-style live dashboard, returned without
// touching Repository at all.
type Current struct {
	Metric    string    `json:"metric"`
	Scope     string    `json:"scope,omitempty"`
	Value     float64   `json:"value"`
	Timestamp time.Time `json:"timestamp"`
}
