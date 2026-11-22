package metrics

import (
	"sort"
	"sync"
	"time"
)

// seriesKey identifies one metric series in-memory - see Name's doc
// comment on Metric+Scope.
type seriesKey struct {
	metric Name
	scope  string
}

// ringBuffer is a bounded, oldest-first buffer of raw Sample, used only
// for the "rolling" query (spec section 17: "current values, rolling
// values") - samples are evicted by capacity, never explicitly cleared.
// This is deliberately a *different* buffer from the flush accumulator
// (see seriesState below): the rolling window needs to keep showing the
// last N samples indefinitely, while the flush accumulator needs to be
// emptied every flush cycle - conflating the two would mean every flush
// wipes out the rolling window a chart is reading from.
type ringBuffer struct {
	samples  []Sample
	capacity int
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{samples: make([]Sample, 0, capacity), capacity: capacity}
}

func (r *ringBuffer) add(s Sample) {
	if len(r.samples) >= r.capacity {
		copy(r.samples, r.samples[1:])
		r.samples[len(r.samples)-1] = s
	} else {
		r.samples = append(r.samples, s)
	}
}

func (r *ringBuffer) latest() (Sample, bool) {
	if len(r.samples) == 0 {
		return Sample{}, false
	}
	return r.samples[len(r.samples)-1], true
}

// seriesState is everything buffer tracks for one series: the rolling
// window plus the accumulator awaiting the next flush.
type seriesState struct {
	rolling *ringBuffer
	pending []Sample
}

// aggregate reduces every currently-pending sample into one Aggregate
// bucketed at bucketTime, or false if nothing has arrived since the last
// flush - a series with no new samples produces no row for this bucket
// rather than a synthetic repeat of its last value (see engine.go's
// flush and ARCHITECTURE.md's "Metrics engine" section).
func (s *seriesState) aggregate(bucketTime time.Time) (Aggregate, bool) {
	if len(s.pending) == 0 {
		return Aggregate{}, false
	}
	min, max, sum := s.pending[0].Value, s.pending[0].Value, 0.0
	for _, sample := range s.pending {
		if sample.Value < min {
			min = sample.Value
		}
		if sample.Value > max {
			max = sample.Value
		}
		sum += sample.Value
	}
	return Aggregate{
		BucketTime:  bucketTime,
		Min:         min,
		Max:         max,
		Avg:         sum / float64(len(s.pending)),
		SampleCount: len(s.pending),
	}, true
}

// buffer is the concurrency-safe collection of every series' rolling
// window and pending-flush accumulator, keyed by seriesKey.
type buffer struct {
	mu       sync.Mutex
	series   map[seriesKey]*seriesState
	rollingN int
}

func newBuffer(rollingN int) *buffer {
	return &buffer{series: make(map[seriesKey]*seriesState), rollingN: rollingN}
}

func (b *buffer) record(s Sample) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := seriesKey{metric: s.Metric, scope: s.Scope}
	st, ok := b.series[key]
	if !ok {
		st = &seriesState{rolling: newRingBuffer(b.rollingN)}
		b.series[key] = st
	}
	st.rolling.add(s)
	st.pending = append(st.pending, s)
}

// current returns the latest sample for every series that has at least
// one, in Name/Scope order for stable output.
func (b *buffer) current() []Current {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Current, 0, len(b.series))
	for key, st := range b.series {
		s, ok := st.rolling.latest()
		if !ok {
			continue
		}
		out = append(out, Current{Metric: string(key.metric), Scope: key.scope, Value: s.Value, Timestamp: s.Timestamp})
	}
	sortCurrent(out)
	return out
}

func sortCurrent(out []Current) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Metric != out[j].Metric {
			return out[i].Metric < out[j].Metric
		}
		return out[i].Scope < out[j].Scope
	})
}

// rolling returns every in-memory sample for one series, oldest first -
// the fast, DB-free path for "show me the last few minutes" without
// waiting for (or querying) a flush.
func (b *buffer) rolling(metric Name, scope string) []Sample {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.series[seriesKey{metric: metric, scope: scope}]
	if !ok {
		return nil
	}
	out := make([]Sample, len(st.rolling.samples))
	copy(out, st.rolling.samples)
	return out
}

// drainForFlush aggregates every series' pending samples into one
// Aggregate each (bucketed at bucketTime) and clears every pending
// accumulator - but never touches the rolling windows - atomically with
// respect to concurrent record calls: a sample that arrives mid-drain
// either lands in this flush's aggregate or the next one's, never lost
// and never double-counted.
func (b *buffer) drainForFlush(bucketTime time.Time) []Aggregate {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Aggregate, 0, len(b.series))
	for key, st := range b.series {
		agg, ok := st.aggregate(bucketTime)
		if !ok {
			continue
		}
		agg.Metric = string(key.metric)
		agg.Scope = key.scope
		out = append(out, agg)
		st.pending = st.pending[:0]
	}
	return out
}
