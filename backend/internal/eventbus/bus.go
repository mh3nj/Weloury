package eventbus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultSubscriberBuffer is the number of events buffered per subscriber
// before new events are dropped for that subscriber. A slow subscriber
// must never be allowed to block Publish for the rest of the system.
const DefaultSubscriberBuffer = 64

// Filter decides whether an Event should be delivered to a subscriber. A
// nil Filter matches every event.
type Filter func(Event) bool

// MatchTypes returns a Filter that matches any of the given types. An
// empty types list matches nothing; pass a nil Filter directly to Subscribe
// if you want everything.
func MatchTypes(types ...Type) Filter {
	set := make(map[Type]struct{}, len(types))
	for _, t := range types {
		set[t] = struct{}{}
	}
	return func(e Event) bool {
		_, ok := set[e.Type]
		return ok
	}
}

type subscriber struct {
	id      uint64
	ch      chan Event
	filter  Filter
	dropped atomic.Uint64
}

// Bus is a bounded, non-blocking, in-process publish/subscribe event bus.
//
// Design notes:
//   - Publish never blocks on a slow subscriber. Each subscriber has a
//     bounded buffered channel; if it is full, the event is dropped for
//     that subscriber and a per-subscriber drop counter is incremented
//     rather than the publisher stalling.
//   - Subscribe is scoped to a context.Context: when ctx is canceled the
//     subscription is automatically removed and its channel closed. The
//     watcher goroutine that implements this exits either when ctx is
//     canceled or when the caller calls the returned cancel function
//     directly - either path unblocks it, so it never leaks even when ctx
//     is context.Background() and only the returned func is ever used.
//   - The zero value is not usable; construct with New.
type Bus struct {
	mu          sync.RWMutex
	subscribers map[uint64]*subscriber
	nextID      atomic.Uint64
	closed      atomic.Bool
}

// New creates a ready-to-use Bus.
func New() *Bus {
	return &Bus{subscribers: make(map[uint64]*subscriber)}
}

// Subscribe registers a new subscriber and returns a receive-only channel
// of events matching filter (nil matches everything), plus a cancel
// function that removes the subscription and closes the channel. ctx must
// not be nil; pass context.Background() for a subscription with no
// automatic expiry, and call the returned cancel func when done.
func (b *Bus) Subscribe(ctx context.Context, filter Filter, bufferSize int) (<-chan Event, func()) {
	if bufferSize <= 0 {
		bufferSize = DefaultSubscriberBuffer
	}
	sub := &subscriber{
		id:     b.nextID.Add(1),
		ch:     make(chan Event, bufferSize),
		filter: filter,
	}

	b.mu.Lock()
	b.subscribers[sub.id] = sub
	b.mu.Unlock()

	stopWatch := make(chan struct{})
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			close(stopWatch)
			b.mu.Lock()
			if _, ok := b.subscribers[sub.id]; ok {
				delete(b.subscribers, sub.id)
				close(sub.ch)
			}
			b.mu.Unlock()
		})
	}

	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-stopWatch:
		}
	}()

	return sub.ch, cancel
}

// Publish delivers event to every matching subscriber. It never blocks:
// subscribers that cannot keep up have the event dropped for them and
// their drop counter incremented rather than stalling the publisher.
// Publish is a safe no-op after Close.
func (b *Bus) Publish(event Event) {
	if b.closed.Load() {
		return
	}
	if event.ID == "" {
		event.ID = newEventID()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subscribers {
		if sub.filter != nil && !sub.filter(event) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
			sub.dropped.Add(1)
		}
	}
}

// Close shuts the bus down, closing every subscriber channel. Publish
// becomes a no-op after Close returns. Close is idempotent and safe to
// call even if some subscriptions are still active; their channels are
// simply closed out from under them, which is the same signal a normal
// unsubscribe gives.
func (b *Bus) Close() {
	if !b.closed.CompareAndSwap(false, true) {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, sub := range b.subscribers {
		close(sub.ch)
		delete(b.subscribers, id)
	}
}

func newEventID() string {
	var buf [16]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
