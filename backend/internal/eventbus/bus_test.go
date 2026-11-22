package eventbus

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestPublishDeliversToSubscriber(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, unsub := b.Subscribe(ctx, nil, 4)
	defer unsub()

	b.Publish(Event{Type: "test.event", Source: "unit"})

	select {
	case e := <-ch:
		if e.Type != "test.event" {
			t.Fatalf("got type %q, want %q", e.Type, "test.event")
		}
		if e.ID == "" {
			t.Fatal("expected auto-assigned event ID")
		}
		if e.Timestamp.IsZero() {
			t.Fatal("expected auto-assigned timestamp")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestFilterExcludesNonMatchingEvents(t *testing.T) {
	b := New()
	ch, unsub := b.Subscribe(context.Background(), MatchTypes("wanted"), 4)
	defer unsub()

	b.Publish(Event{Type: "unwanted"})
	b.Publish(Event{Type: "wanted"})

	select {
	case e := <-ch:
		if e.Type != "wanted" {
			t.Fatalf("got %q, want %q", e.Type, "wanted")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for filtered event")
	}

	select {
	case e := <-ch:
		t.Fatalf("received unexpected second event: %+v", e)
	case <-time.After(50 * time.Millisecond):
		// Expected: the "unwanted" event was never delivered.
	}
}

func TestContextCancelUnsubscribes(t *testing.T) {
	b := New()
	ctx, cancel := context.WithCancel(context.Background())
	ch, _ := b.Subscribe(ctx, nil, 1)
	cancel()

	deadline := time.After(time.Second)
	for {
		select {
		case _, open := <-ch:
			if !open {
				return // channel closed as expected
			}
		case <-deadline:
			t.Fatal("subscriber channel was not closed after context cancellation")
		}
	}
}

func TestManualUnsubscribeDoesNotLeakWatcherGoroutine(t *testing.T) {
	// Regression test: Subscribe's internal watcher goroutine must exit
	// when the caller invokes the returned cancel func directly, even
	// though ctx (context.Background()) never fires on its own. We can't
	// directly count goroutines reliably in a unit test, but we can
	// assert the channel closes promptly, which only happens if cancel()
	// ran its full body - the watcher's job is exactly to call cancel()
	// on ctx.Done(), so a directly-invoked cancel must produce the same
	// observable effect without waiting on the watcher.
	b := New()
	ch, unsub := b.Subscribe(context.Background(), nil, 1)
	unsub()

	select {
	case _, open := <-ch:
		if open {
			t.Fatal("expected channel to be closed immediately after manual unsubscribe")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}
}

func TestSlowSubscriberDropsRatherThanBlocks(t *testing.T) {
	b := New()
	ch, unsub := b.Subscribe(context.Background(), nil, 1)
	defer unsub()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			b.Publish(Event{Type: "flood"})
		}
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish blocked on a slow subscriber instead of dropping events")
	}

	select {
	case <-ch:
	default:
	}
}

func TestCloseClosesAllSubscribers(t *testing.T) {
	b := New()
	ch, _ := b.Subscribe(context.Background(), nil, 1)
	b.Close()

	select {
	case _, open := <-ch:
		if open {
			t.Fatal("expected channel to be closed after Bus.Close")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for channel close")
	}

	// Publish after Close must be a safe no-op, not a panic.
	b.Publish(Event{Type: "after-close"})
}

func TestConcurrentPublishAndSubscribeIsRaceFree(t *testing.T) {
	// Exercised primarily under `go test -race`. Multiple goroutines
	// subscribe/unsubscribe and publish concurrently; the assertion is
	// simply that nothing panics or deadlocks within the deadline.
	b := New()
	var wg sync.WaitGroup

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				ctx, cancel := context.WithCancel(context.Background())
				_, unsub := b.Subscribe(ctx, nil, 2)
				b.Publish(Event{Type: "concurrent"})
				unsub()
				cancel()
			}
		}()
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Publish(Event{Type: "concurrent"})
			}
		}()
	}

	waitDone := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent publish/subscribe did not complete in time (possible deadlock)")
	}
}
