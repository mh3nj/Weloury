package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"weloury/internal/eventbus"
)

// DefaultMaxConcurrentChecks bounds how many checks may be executing at
// once, regardless of how many are configured - spec section 9: "Health
// checks must run concurrently but must have bounded resource usage."
// Enforced via Engine.sem, a buffered channel used as a counting
// semaphore.
const DefaultMaxConcurrentChecks = 8

// Engine schedules and executes every configured Check, tracks each
// one's live Status in memory, persists both configuration and results
// via Repository, and publishes transition events on the bus. The zero
// value is not usable; construct with New.
type Engine struct {
	sys    sysCapabilities
	bus    *eventbus.Bus
	repo   Repository
	logger *slog.Logger
	clock  clock

	sem chan struct{}
	wg  sync.WaitGroup

	mu      sync.Mutex
	checks  map[string]*trackedCheck
	baseCtx context.Context
}

// New constructs an Engine. sys, repo, and logger must be non-nil; bus
// may be nil (Engine simply won't publish events - used by tests that
// don't care about the bus).
func New(sys sysCapabilities, bus *eventbus.Bus, repo Repository, logger *slog.Logger) *Engine {
	return &Engine{
		sys:    sys,
		bus:    bus,
		repo:   repo,
		logger: logger,
		clock:  realClock{},
		sem:    make(chan struct{}, DefaultMaxConcurrentChecks),
		checks: make(map[string]*trackedCheck),
	}
}

// Start loads every persisted check from Repository and begins
// scheduling it, using ctx as the parent lifecycle context for every
// check's execution goroutine - canceling ctx stops all of them. Start
// itself does not block; call Wait to block until every check goroutine
// has actually exited (mirrors internal/app.App.Run's wg.Wait()
// pattern, one level down).
//
// Start must be called, and must return, before any concurrent call to
// Create/Update/Delete/RunNow can be relied upon to schedule its check -
// see internal/app's composition root for how this ordering is
// guaranteed in practice (Start is called synchronously before the IPC
// server begins accepting requests).
func (e *Engine) Start(ctx context.Context) error {
	e.mu.Lock()
	e.baseCtx = ctx
	e.mu.Unlock()

	statuses, err := e.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("load health checks: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	for _, st := range statuses {
		e.startLocked(st.Check, st)
	}
	return nil
}

// Wait blocks until every check's execution goroutine has exited -
// call after canceling the context passed to Start.
func (e *Engine) Wait() { e.wg.Wait() }

// startLocked registers and starts scheduling c, using initial as its
// starting live status (StateUnknown/zero values for a brand new check,
// or a reloaded prior status on Start). Caller must hold e.mu.
func (e *Engine) startLocked(c Check, initial Status) {
	base := e.baseCtx
	if base == nil {
		// Defensive fallback only - see Start's doc comment on the
		// ordering internal/app guarantees in normal operation. A
		// check started this way would never be canceled by the
		// application's normal shutdown path, so this indicates a
		// programming error upstream, not a supported mode.
		base = context.Background()
	}
	checkCtx, cancel := context.WithCancel(base)
	tc := newTrackedCheck(c, initial, cancel)
	e.checks[c.ID] = tc
	e.wg.Add(1)
	go e.runLoop(checkCtx, tc)
}

func (e *Engine) runLoop(ctx context.Context, tc *trackedCheck) {
	defer e.wg.Done()

	// Run once immediately rather than waiting a full interval for the
	// first result - the same "don't make the user wait" reasoning
	// internal/app's retention pruner already applies to its own
	// startup sweep (see retention.go's runRetentionPruner).
	e.runOnce(ctx, tc)

	interval := time.Duration(tc.snapshotCheck().IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = defaultIntervalSeconds * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.runOnce(ctx, tc)
		}
	}
}

// runOnce acquires a semaphore slot (bounding system-wide concurrent
// check execution - see DefaultMaxConcurrentChecks), evaluates tc's
// check with retries, updates its in-memory and persisted state, and
// publishes a transition event if its state actually changed.
func (e *Engine) runOnce(ctx context.Context, tc *trackedCheck) {
	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() { <-e.sem }()

	c := tc.snapshotCheck()
	state, msg := e.runWithRetries(ctx, c)
	if ctx.Err() != nil {
		// The check (or the whole engine) was stopped mid-evaluation -
		// don't record a result for work that was actually abandoned.
		return
	}

	result := Result{State: state, Message: msg, CheckedAt: e.clock.Now()}
	prev := tc.applyResult(result)

	if e.repo != nil {
		persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := e.repo.UpdateResult(persistCtx, c.ID, result); err != nil && e.logger != nil {
			e.logger.Warn("failed to persist health check result", "check_id", c.ID, "check_name", c.Name, "error", err)
		}
		cancel()
	}

	e.publishTransition(c, prev, result)
}

// runWithRetries evaluates c once, then retries up to c.Retry.MaxRetries
// times - spaced c.Retry.RetryDelaySeconds apart - as long as each
// attempt keeps coming back UNHEALTHY. It stops retrying (returning
// immediately) the moment an attempt is anything other than UNHEALTHY:
// DEGRADED and UNKNOWN are not "hard failures" the retry policy exists
// to smooth over a transient blip in - see outcome's doc comment in
// checkers.go.
func (e *Engine) runWithRetries(ctx context.Context, c Check) (State, string) {
	timeout := time.Duration(c.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeoutSeconds * time.Second
	}

	o := e.attempt(ctx, c, timeout)
	for attempt := 0; o.state == StateUnhealthy && attempt < c.Retry.MaxRetries; attempt++ {
		if delay := time.Duration(c.Retry.RetryDelaySeconds) * time.Second; delay > 0 {
			select {
			case <-ctx.Done():
				return o.state, o.message
			case <-time.After(delay):
			}
		}
		o = e.attempt(ctx, c, timeout)
	}
	return o.state, o.message
}

func (e *Engine) attempt(ctx context.Context, c Check, timeout time.Duration) outcome {
	attemptCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return evaluate(attemptCtx, e.sys, c)
}

// publishTransition fires the appropriate bus event(s) for a check whose
// state actually changed - see TypeCheckPassed/TypeCheckFailed's doc
// comments in events.go for why this only fires on transitions, not
// every evaluation.
func (e *Engine) publishTransition(c Check, prev State, r Result) {
	if e.bus == nil || prev == r.State {
		return
	}

	payload, err := json.Marshal(eventPayload{
		CheckID: c.ID, Name: c.Name, Type: string(c.Type),
		State: r.State, Message: r.Message, Target: c.Target, Threshold: c.Threshold,
	})
	if err != nil {
		return
	}

	switch r.State {
	case StateUnhealthy:
		e.bus.Publish(eventbus.Event{Type: TypeCheckFailed, Source: "health.engine", Severity: eventbus.SeverityWarning, Payload: payload})
		switch c.Type {
		case CheckDiskThreshold:
			e.bus.Publish(eventbus.Event{Type: TypeDiskThresholdExceeded, Source: "health.engine", Severity: eventbus.SeverityCritical, Payload: payload})
		case CheckMemoryThreshold:
			e.bus.Publish(eventbus.Event{Type: TypeMemoryThresholdExceeded, Source: "health.engine", Severity: eventbus.SeverityCritical, Payload: payload})
		}
	case StateHealthy:
		e.bus.Publish(eventbus.Event{Type: TypeCheckPassed, Source: "health.engine", Severity: eventbus.SeverityInfo, Payload: payload})
	}
}

// List returns every configured check's live status, sorted by name for
// stable, predictable UI ordering.
func (e *Engine) List() []Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Status, 0, len(e.checks))
	for _, tc := range e.checks {
		out = append(out, tc.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one check's live status by ID.
func (e *Engine) Get(id string) (Status, bool) {
	e.mu.Lock()
	tc, ok := e.checks[id]
	e.mu.Unlock()
	if !ok {
		return Status{}, false
	}
	return tc.snapshot(), true
}

// Create validates, persists, and immediately begins scheduling a new
// check.
func (e *Engine) Create(ctx context.Context, c Check) (Status, error) {
	if err := c.applyDefaultsAndValidate(); err != nil {
		return Status{}, err
	}
	now := e.clock.Now()
	c.ID = newCheckID()
	c.CreatedAt, c.UpdatedAt = now, now

	if err := e.repo.Upsert(ctx, c); err != nil {
		return Status{}, fmt.Errorf("persist check: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.startLocked(c, Status{Check: c, State: StateUnknown})
	return e.checks[c.ID].snapshot(), nil
}

// Update validates and persists a new configuration for an existing
// check, then restarts its scheduling goroutine under the new config -
// preserving its accumulated State/Message/LastSuccessAt/LastFailureAt/
// FailureCount rather than resetting them, so editing (say) a check's
// interval doesn't discard its observed history.
func (e *Engine) Update(ctx context.Context, id string, c Check) (Status, error) {
	if err := c.applyDefaultsAndValidate(); err != nil {
		return Status{}, err
	}

	e.mu.Lock()
	existing, ok := e.checks[id]
	e.mu.Unlock()
	if !ok {
		return Status{}, ErrNotFound
	}

	c.ID = id
	c.CreatedAt = existing.snapshotCheck().CreatedAt
	c.UpdatedAt = e.clock.Now()

	if err := e.repo.Upsert(ctx, c); err != nil {
		return Status{}, fmt.Errorf("persist check: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	// Re-checked under the lock in case a concurrent Delete raced this
	// Update between the unlocked section above and here.
	tc, ok := e.checks[id]
	if !ok {
		return Status{}, ErrNotFound
	}
	tc.cancel()
	preserved := tc.snapshot()
	preserved.Check = c
	e.startLocked(c, preserved)
	return e.checks[id].snapshot(), nil
}

// Delete stops and removes a check.
func (e *Engine) Delete(ctx context.Context, id string) error {
	e.mu.Lock()
	tc, ok := e.checks[id]
	if ok {
		tc.cancel()
		delete(e.checks, id)
	}
	e.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return e.repo.Delete(ctx, id)
}

// RunNow evaluates a check immediately, outside its normal schedule -
// used by the diagnostics area (spec section 23) and by a UI "run now"
// action. It goes through the same semaphore, retry, persistence, and
// event-publishing path as a scheduled run (runOnce), just triggered by
// the caller's ctx instead of the check's own ticker.
func (e *Engine) RunNow(ctx context.Context, id string) (Status, error) {
	e.mu.Lock()
	tc, ok := e.checks[id]
	e.mu.Unlock()
	if !ok {
		return Status{}, ErrNotFound
	}
	e.runOnce(ctx, tc)
	return tc.snapshot(), nil
}

// trackedCheck is Engine's in-memory record for one scheduled check:
// its current configuration, live status, and the cancel func that stops
// its execution goroutine. All fields are accessed only through its
// methods, each of which takes tc.mu - a separate lock from Engine.mu,
// since Engine.mu protects the *set* of checks (the map) while tc.mu
// protects one check's *own* mutable state, updated far more often (on
// every evaluation) than the set changes (on Create/Update/Delete).
type trackedCheck struct {
	mu     sync.Mutex
	check  Check
	cancel context.CancelFunc

	state         State
	message       string
	lastSuccessAt *time.Time
	lastFailureAt *time.Time
	failureCount  int
}

func newTrackedCheck(c Check, initial Status, cancel context.CancelFunc) *trackedCheck {
	return &trackedCheck{
		check:         c,
		cancel:        cancel,
		state:         initial.State,
		message:       initial.Message,
		lastSuccessAt: initial.LastSuccessAt,
		lastFailureAt: initial.LastFailureAt,
		failureCount:  initial.FailureCount,
	}
}

func (tc *trackedCheck) snapshot() Status {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return Status{
		Check:         tc.check,
		State:         tc.state,
		Message:       tc.message,
		LastSuccessAt: tc.lastSuccessAt,
		LastFailureAt: tc.lastFailureAt,
		FailureCount:  tc.failureCount,
	}
}

func (tc *trackedCheck) snapshotCheck() Check {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	return tc.check
}

// applyResult updates live state per the three-way rule documented on
// Repository.UpdateResult (which persistence must mirror exactly), and
// returns the state prior to this call so publishTransition can detect
// an actual change.
func (tc *trackedCheck) applyResult(r Result) State {
	tc.mu.Lock()
	defer tc.mu.Unlock()
	prev := tc.state
	tc.state = r.State
	tc.message = r.Message
	switch r.State {
	case StateHealthy:
		t := r.CheckedAt
		tc.lastSuccessAt = &t
		tc.failureCount = 0
	case StateDegraded, StateUnhealthy:
		t := r.CheckedAt
		tc.lastFailureAt = &t
		tc.failureCount++
	case StateUnknown:
		// Neither counter moves - see Repository.UpdateResult's doc
		// comment on why UNKNOWN is excluded from both.
	}
	return prev
}
