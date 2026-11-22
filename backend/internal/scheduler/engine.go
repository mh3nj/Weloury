package scheduler

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

// scanInterval is how often Engine checks which tasks are due. One
// second is far finer-grained than any task's minimum interval
// (MinIntervalSeconds is 10) so "due" always means "within a second of
// intended," while staying cheap: a scan is just an in-memory
// NextRunAt-vs-now comparison across however many tasks exist, which
// for a local desktop tool's realistic task count is negligible work
// even every second. This is why scheduler.Engine uses one shared loop
// instead of one goroutine per task the way internal/health does - each
// health check's variable timeout/retry execution genuinely warranted
// its own goroutine; a task becoming due is just a comparison, not work
// worth a dedicated goroutine per task.
const scanInterval = time.Second

// Engine tracks every Task in memory, persists changes through
// Repository, and publishes TypeTaskDue on the bus when a task becomes
// due.
//
// Missed-execution policy (spec section 12: "Handle missed executions
// deterministically. Prevent duplicate execution after application
// restart."): Start's first scan happens immediately, before the
// regular ticker begins, so any task whose NextRunAt is already in the
// past - because Weloury wasn't running when it was due - fires exactly
// once at startup, not once per interval that elapsed while offline. An
// Interval task's next NextRunAt is always computed from the actual
// firing time, never from the original missed schedule, so a task that
// missed many intervals while Weloury was closed catches up once and then
// resumes its normal cadence, rather than bursting through every missed
// occurrence. Duplicate execution after a restart is prevented by
// persisting the new NextRunAt/LastRunAt *before* publishing
// TypeTaskDue (see fire, below) - durability before the side effect, so
// the worst a crash between the two can do is miss one publish, never
// replay one that already happened.
type Engine struct {
	bus    *eventbus.Bus
	repo   Repository
	logger *slog.Logger
	clock  clock

	wg sync.WaitGroup

	mu    sync.Mutex
	tasks map[string]*Task
}

// New constructs an Engine. repo must be non-nil; bus may be nil (used
// by tests that don't care about the published event); logger may be
// nil.
func New(bus *eventbus.Bus, repo Repository, logger *slog.Logger) *Engine {
	return &Engine{
		bus:    bus,
		repo:   repo,
		logger: logger,
		clock:  realClock{},
		tasks:  make(map[string]*Task),
	}
}

// Start loads every persisted task and begins the scan loop, scoped to
// ctx. Does not block - call Wait after canceling ctx.
func (e *Engine) Start(ctx context.Context) error {
	tasks, err := e.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("load scheduled tasks: %w", err)
	}

	e.mu.Lock()
	for i := range tasks {
		t := tasks[i]
		e.tasks[t.ID] = &t
	}
	e.mu.Unlock()

	e.wg.Add(1)
	go e.runLoop(ctx)
	return nil
}

// Wait blocks until the scan loop has exited.
func (e *Engine) Wait() { e.wg.Wait() }

func (e *Engine) runLoop(ctx context.Context) {
	defer e.wg.Done()

	e.scanOnce(ctx) // catch-up scan - see Engine's doc comment

	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.scanOnce(ctx)
		}
	}
}

func (e *Engine) scanOnce(ctx context.Context) {
	now := e.clock.Now()

	e.mu.Lock()
	var due []*Task
	for _, t := range e.tasks {
		if t.Enabled && !t.NextRunAt.IsZero() && !t.NextRunAt.After(now) {
			due = append(due, t)
		}
	}
	e.mu.Unlock()

	for _, t := range due {
		e.fire(ctx, t, now)
	}
}

// fire records t's firing (advancing NextRunAt, or disabling it if it
// was a one-shot Once task), persists that durably, and only then
// publishes TypeTaskDue - see Engine's doc comment on why that order,
// not the reverse, is what prevents duplicate execution.
//
// t is a pointer scanOnce collected under e.mu some time before this
// call (fire always runs after scanOnce has released the lock - see
// scanOnce). Between that collection and this call, a concurrent
// Delete or Update may have run. fire re-validates that t is still
// e.tasks[t.ID]'s live entry before touching anything, and bails out
// silently if not:
//
//   - Delete removes the map entry outright; t is now an orphaned
//     struct nothing else references. Firing it anyway would still
//     call e.repo.Upsert with t's ID, which - being an unconditional
//     INSERT ... ON CONFLICT DO UPDATE, not a conditional "update only
//     if it still exists" - would silently resurrect the row in the
//     repository forever, since nothing in e.tasks would ever be there
//     to delete it again.
//   - Update installs a brand-new *Task at e.tasks[t.ID] rather than
//     mutating the old one in place (unlike SetEnabled) specifically
//     so this check can tell the two apart by pointer identity: a
//     stale t here means Update's new configuration is what should
//     win, not a firing computed from the configuration Update just
//     replaced. Firing anyway could persist a snapshot that reverts
//     Update's change, with a NextRunAt computed from neither the old
//     nor the new schedule cleanly.
//
// Either way, skipping is safe and not a missed firing in any lasting
// sense: if the task still exists post-Update, its fresh NextRunAt
// (computed by Update, not by this call) will make it due again on its
// own on a later scan if warranted; if it's gone, there is nothing left
// to fire. See TestEngineFireSkipsTaskDeletedDuringScan and
// TestEngineFireDoesNotClobberConcurrentUpdate.
func (e *Engine) fire(_ context.Context, t *Task, now time.Time) {
	e.mu.Lock()
	if current, ok := e.tasks[t.ID]; !ok || current != t {
		e.mu.Unlock()
		return
	}
	t.LastRunAt = &now
	if t.Type == Once {
		t.Enabled = false
	} else {
		t.NextRunAt = now.Add(time.Duration(t.IntervalSeconds) * time.Second)
	}
	snapshot := *t
	e.mu.Unlock()

	persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := e.repo.Upsert(persistCtx, snapshot)
	cancel()
	if err != nil {
		if e.logger != nil {
			e.logger.Warn("failed to persist scheduled task after firing", "task_id", snapshot.ID, "error", err)
		}
		// Deliberately do not publish on a failed persist: better to
		// risk a missed firing than to publish an event this Task's
		// persisted record doesn't yet reflect - see the package doc
		// comment's "miss, never duplicate" tradeoff.
		return
	}

	if e.bus != nil {
		payload, err := json.Marshal(taskDuePayload{TaskID: snapshot.ID, Name: snapshot.Name})
		if err == nil {
			e.bus.Publish(eventbus.Event{
				Type:     TypeTaskDue,
				Source:   "scheduler.engine",
				Severity: eventbus.SeverityInfo,
				Payload:  payload,
			})
		}
	}
}

// computeInitialNextRun is the one place "when does this task next
// fire" is computed for a task whose schedule was just (re)configured -
// used by Create and Update, not by fire (which advances from the
// actual firing time instead - see Engine's doc comment).
func computeInitialNextRun(t Task, now time.Time) time.Time {
	if t.Type == Once {
		return t.RunAt
	}
	return now.Add(time.Duration(t.IntervalSeconds) * time.Second)
}

// List returns every task, sorted by name for stable UI ordering.
func (e *Engine) List() []Task {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Task, 0, len(e.tasks))
	for _, t := range e.tasks {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one task by ID.
func (e *Engine) Get(id string) (Task, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[id]
	if !ok {
		return Task{}, false
	}
	return *t, true
}

// Create validates, persists, and begins tracking a new task. Its
// NextRunAt is computed fresh - for Once, RunAt itself; for Interval,
// one interval from now (a newly created recurring task does not fire
// immediately on creation the way internal/health/internal/metrics'
// collectors deliberately do - an unexpected immediate fire of, say, a
// "restart my service" automation's schedule the moment someone finishes
// configuring it would be a surprising, undesirable default here).
//
// Uses defer e.mu.Unlock() specifically, not an explicit Unlock before
// the return - this was a real, if narrow, data race until Phase 8
// found and fixed it (see CHANGELOG.md): once `&t` is stored into
// e.tasks, that memory is reachable from the scheduler's own scan loop
// on a different goroutine, which can begin mutating it (via fire, if
// the new task's NextRunAt is already due by the next scan tick - rare,
// but a task created with a near-immediate run time makes the window
// real, not theoretical) before this function finishes copying t's
// fields out for its own return value. An explicit Unlock before
// `return t, nil` would let that copy happen unprotected; a deferred
// Unlock keeps the lock held until after the return value is already
// captured, matching the pattern Update and SetEnabled below already
// use (this was the one function in this file that didn't). See
// TestEngineCreateRacingConcurrentScan for the regression test.
func (e *Engine) Create(ctx context.Context, t Task) (Task, error) {
	if err := t.applyDefaultsAndValidate(); err != nil {
		return Task{}, err
	}
	now := e.clock.Now()
	t.ID = newTaskID()
	t.CreatedAt, t.UpdatedAt = now, now
	t.Enabled = true
	t.LastRunAt = nil
	t.NextRunAt = computeInitialNextRun(t, now)

	if err := e.repo.Upsert(ctx, t); err != nil {
		return Task{}, fmt.Errorf("persist task: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.tasks[t.ID] = &t
	return t, nil
}

// Update validates and persists a new configuration for an existing
// task, rescheduling it fresh from now (see Create's doc comment) and
// re-enabling it if it had auto-disabled after a Once firing, while
// preserving LastRunAt/CreatedAt for history continuity.
func (e *Engine) Update(ctx context.Context, id string, t Task) (Task, error) {
	if err := t.applyDefaultsAndValidate(); err != nil {
		return Task{}, err
	}

	e.mu.Lock()
	existing, ok := e.tasks[id]
	e.mu.Unlock()
	if !ok {
		return Task{}, ErrNotFound
	}

	now := e.clock.Now()
	t.ID = id
	t.CreatedAt = existing.CreatedAt
	t.UpdatedAt = now
	t.LastRunAt = existing.LastRunAt
	t.Enabled = true
	t.NextRunAt = computeInitialNextRun(t, now)

	if err := e.repo.Upsert(ctx, t); err != nil {
		return Task{}, fmt.Errorf("persist task: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.tasks[id]; !ok {
		return Task{}, ErrNotFound // raced a concurrent Delete
	}
	e.tasks[id] = &t
	return t, nil
}

// SetEnabled pauses or resumes a task without touching its schedule
// configuration - the lightweight toggle for "stop this from firing for
// now" that doesn't require re-specifying RunAt/IntervalSeconds the way
// Update does.
func (e *Engine) SetEnabled(ctx context.Context, id string, enabled bool) (Task, error) {
	e.mu.Lock()
	existing, ok := e.tasks[id]
	e.mu.Unlock()
	if !ok {
		return Task{}, ErrNotFound
	}

	now := e.clock.Now()
	snapshot := *existing
	snapshot.Enabled = enabled
	snapshot.UpdatedAt = now
	if enabled && snapshot.Type == Interval && snapshot.NextRunAt.Before(now) {
		// Resuming an interval task paused long enough for its schedule
		// to drift into the past: reschedule from now rather than
		// firing a burst of catch-up executions - the same "resume,
		// don't flood" policy Engine.fire applies when catching up
		// after a restart.
		snapshot.NextRunAt = now.Add(time.Duration(snapshot.IntervalSeconds) * time.Second)
	}

	if err := e.repo.Upsert(ctx, snapshot); err != nil {
		return Task{}, fmt.Errorf("persist task: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.tasks[id]
	if !ok {
		return Task{}, ErrNotFound
	}
	*t = snapshot
	return *t, nil
}

// Delete stops tracking and removes a task.
func (e *Engine) Delete(ctx context.Context, id string) error {
	e.mu.Lock()
	_, ok := e.tasks[id]
	if ok {
		delete(e.tasks, id)
	}
	e.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return e.repo.Delete(ctx, id)
}
