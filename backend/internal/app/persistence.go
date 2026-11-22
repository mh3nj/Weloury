package app

import (
	"context"
	"time"

	"weloury/internal/eventbus"
)

// runEventPersistence subscribes to every event on the bus and writes each
// one to SQLite via a.events, bridging the in-memory bus to durable
// history for the Events screen (Phase 4+) and automation rule evaluation
// (Phase 5).
//
// ch and unsubscribe come from a Subscribe call Run makes itself,
// synchronously, before starting this function's goroutine - not from a
// Subscribe call made here. Subscribing inside this function looked
// equivalent but was not: Run publishes TypeBackendStarted immediately
// after starting every background goroutine, and there is no ordering
// guarantee that this goroutine has been scheduled and reached its own
// Subscribe call before that Publish happens on Run's goroutine. When it
// loses that race, TypeBackendStarted is silently never persisted - the
// Events screen is missing its own startup event, every single run, with
// no error anywhere to point at why. Subscribe is synchronous internally
// (it registers into the bus's subscriber map under a mutex before
// returning - see eventbus.Bus.Subscribe), so calling it from Run's own
// goroutine before Publish removes the race entirely rather than
// narrowing it.
func (a *App) runEventPersistence(ctx context.Context, ch <-chan eventbus.Event, unsubscribe func()) {
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			// Drain whatever is already buffered before returning, so an
			// event published in the same instant as shutdown (e.g.
			// TypeBackendStopping, published by Run immediately before it
			// cancels this goroutine's context) is not silently lost to
			// a race between the two select cases below becoming ready
			// simultaneously.
			for {
				select {
				case e, open := <-ch:
					if !open {
						return
					}
					a.persistEvent(e)
				default:
					return
				}
			}
		case e, open := <-ch:
			if !open {
				return
			}
			a.persistEvent(e)
		}
	}
}

func (a *App) persistEvent(e eventbus.Event) {
	insertCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.events.Insert(insertCtx, e); err != nil {
		a.logger.Warn("failed to persist event", "event_type", e.Type, "error", err)
	}
}
