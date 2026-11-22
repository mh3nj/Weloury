package scheduler

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"

	"weloury/internal/eventbus"
)

// settledGoroutineCount returns runtime.NumGoroutine() once it has held
// steady across a few consecutive samples - mirrors
// internal/automation's identical helper and the same reasoning: a
// single raw reading is flaky against goroutines merely mid-exit, not
// genuinely leaked.
func settledGoroutineCount(t *testing.T) int {
	t.Helper()
	var last int
	stable := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		n := runtime.NumGoroutine()
		if n == last {
			stable++
			if stable >= 3 {
				return n
			}
		} else {
			stable = 0
		}
		last = n
		time.Sleep(20 * time.Millisecond)
	}
	return last
}

// fakeRepository is a complete in-memory Repository - same
// dependency-inversion payoff internal/health's fakeRepository
// demonstrates, letting Engine be tested without internal/storage.
type fakeRepository struct {
	mu    sync.Mutex
	tasks map[string]Task
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{tasks: make(map[string]Task)}
}

func (r *fakeRepository) List(context.Context) ([]Task, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Task, 0, len(r.tasks))
	for _, t := range r.tasks {
		out = append(out, t)
	}
	return out, nil
}

func (r *fakeRepository) Upsert(_ context.Context, t Task) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[t.ID] = t
	return nil
}

func (r *fakeRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, id)
	return nil
}

func (r *fakeRepository) get(id string) (Task, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, ok := r.tasks[id]
	return t, ok
}

// fakeClock lets tests control "now" deterministically instead of
// sleeping through real intervals.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock { return &fakeClock{now: start} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
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

func TestEngineCreateOnceTaskFiresAtRunAt(t *testing.T) {
	repo := newFakeRepository()
	bus := eventbus.New()
	defer bus.Close()
	e := New(bus, repo, nil)
	fc := newFakeClock(time.Now())
	e.clock = fc

	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeTaskDue), 4)
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	runAt := fc.Now().Add(2 * time.Second)
	task, err := e.Create(context.Background(), Task{Name: "reminder", Type: Once, RunAt: runAt})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Not due yet.
	select {
	case evt := <-ch:
		t.Fatalf("unexpected early fire: %v", evt)
	case <-time.After(150 * time.Millisecond):
	}

	fc.advance(3 * time.Second)

	select {
	case evt := <-ch:
		if evt.Type != TypeTaskDue {
			t.Errorf("evt.Type = %q, want %q", evt.Type, TypeTaskDue)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TypeTaskDue")
	}

	waitFor(t, time.Second, func() bool {
		got, ok := e.Get(task.ID)
		return ok && !got.Enabled // Once tasks disable themselves after firing
	})

	cancel()
	e.Wait()
}

func TestEngineIntervalTaskRefiresAndAdvances(t *testing.T) {
	repo := newFakeRepository()
	bus := eventbus.New()
	defer bus.Close()
	e := New(bus, repo, nil)
	fc := newFakeClock(time.Now())
	e.clock = fc

	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeTaskDue), 8)
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	task, err := e.Create(context.Background(), Task{Name: "poll", Type: Interval, IntervalSeconds: MinIntervalSeconds})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	firstNextRun := task.NextRunAt

	fc.advance(time.Duration(MinIntervalSeconds+1) * time.Second)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for first fire")
	}

	waitFor(t, time.Second, func() bool {
		got, ok := e.Get(task.ID)
		return ok && got.Enabled && got.NextRunAt.After(firstNextRun)
	})

	fc.advance(time.Duration(MinIntervalSeconds+1) * time.Second)
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for second fire")
	}

	cancel()
	e.Wait()
}

func TestEngineCatchesUpMissedTaskOnStart(t *testing.T) {
	repo := newFakeRepository()
	bus := eventbus.New()
	defer bus.Close()

	past := time.Now().Add(-time.Hour)
	_ = repo.Upsert(context.Background(), Task{
		ID: "task_missed", Name: "missed", Type: Once,
		RunAt: past, Enabled: true, NextRunAt: past,
		CreatedAt: past, UpdatedAt: past,
	})

	e := New(bus, repo, nil)
	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeTaskDue), 4)
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != TypeTaskDue {
			t.Errorf("evt.Type = %q, want %q", evt.Type, TypeTaskDue)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected the missed task to fire once on startup")
	}

	// No duplicate firing.
	select {
	case evt := <-ch:
		t.Fatalf("unexpected second fire of a Once task: %v", evt)
	case <-time.After(300 * time.Millisecond):
	}

	cancel()
	e.Wait()
}

func TestEngineIntervalTaskCatchesUpOnceNotRepeatedly(t *testing.T) {
	repo := newFakeRepository()
	bus := eventbus.New()
	defer bus.Close()

	// An interval task that missed many intervals while "offline."
	longAgo := time.Now().Add(-time.Hour)
	_ = repo.Upsert(context.Background(), Task{
		ID: "task_interval", Name: "poll", Type: Interval,
		IntervalSeconds: MinIntervalSeconds, Enabled: true, NextRunAt: longAgo,
		CreatedAt: longAgo, UpdatedAt: longAgo,
	})

	e := New(bus, repo, nil)
	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeTaskDue), 8)
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("expected one catch-up fire")
	}

	// Confirm the rescheduled NextRunAt is close to "now + interval",
	// not "longAgo + interval" repeated in a burst - i.e. only one
	// event arrives promptly.
	select {
	case evt := <-ch:
		t.Fatalf("unexpected burst fire: %v", evt)
	case <-time.After(300 * time.Millisecond):
	}

	got, ok := e.Get("task_interval")
	if !ok {
		t.Fatal("expected task to still exist")
	}
	if got.NextRunAt.Before(time.Now().Add(MinIntervalSeconds - 2*time.Second)) {
		t.Errorf("NextRunAt = %v, want rescheduled from now, not from the original missed schedule", got.NextRunAt)
	}

	cancel()
	e.Wait()
}

func TestEngineSetEnabledPausesAndResumes(t *testing.T) {
	repo := newFakeRepository()
	e := New(nil, repo, nil)
	fc := newFakeClock(time.Now())
	e.clock = fc

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	task, err := e.Create(context.Background(), Task{Name: "poll", Type: Interval, IntervalSeconds: MinIntervalSeconds})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := e.SetEnabled(context.Background(), task.ID, false); err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	got, _ := e.Get(task.ID)
	if got.Enabled {
		t.Fatal("expected task to be disabled after SetEnabled(false)")
	}

	fc.advance(time.Duration(MinIntervalSeconds*10) * time.Second) // well past due while paused

	updated, err := e.SetEnabled(context.Background(), task.ID, true)
	if err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if !updated.Enabled {
		t.Fatal("expected task to be enabled")
	}
	if updated.NextRunAt.Before(fc.Now()) {
		t.Errorf("NextRunAt = %v, want rescheduled forward from resume time, not left in the past", updated.NextRunAt)
	}

	cancel()
	e.Wait()
}

func TestEngineDeleteRemovesFromRepository(t *testing.T) {
	repo := newFakeRepository()
	e := New(nil, repo, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	task, err := e.Create(context.Background(), Task{Name: "x", Type: Interval, IntervalSeconds: MinIntervalSeconds})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := e.Delete(context.Background(), task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := e.Get(task.ID); ok {
		t.Error("expected task to be gone from Get after Delete")
	}
	if _, ok := repo.get(task.ID); ok {
		t.Error("expected task to be gone from the repository after Delete")
	}
	if err := e.Delete(context.Background(), task.ID); err != ErrNotFound {
		t.Errorf("second Delete err = %v, want ErrNotFound", err)
	}

	cancel()
	e.Wait()
}

func TestEngineValidationRejectsBadConfig(t *testing.T) {
	e := New(nil, newFakeRepository(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = e.Start(ctx)
	defer func() { cancel(); e.Wait() }()

	cases := []Task{
		{Name: "", Type: Once, RunAt: time.Now().Add(time.Hour)},
		{Name: "x", Type: Once},                         // missing RunAt
		{Name: "x", Type: Interval, IntervalSeconds: 1}, // below MinIntervalSeconds
		{Name: "x", Type: "bogus"},                      // unrecognized type
	}
	for i, c := range cases {
		if _, err := e.Create(context.Background(), c); err == nil {
			t.Errorf("case %d: expected validation error, got none", i)
		}
	}
}

func TestEngineWaitReturnsAfterContextCancel(t *testing.T) {
	e := New(nil, newFakeRepository(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := e.Create(context.Background(), Task{Name: "x", Type: Interval, IntervalSeconds: MinIntervalSeconds}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	cancel()
	done := make(chan struct{})
	go func() { e.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return promptly after context cancellation")
	}
}

// TestEngineFireSkipsTaskDeletedDuringScan is a regression test for a
// real logic bug found by reading Start/scanOnce/fire together as a
// whole rather than each in isolation: scanOnce collects, under e.mu,
// the *Task pointers currently in e.tasks that are due, then releases
// the lock before calling fire on each one. Before this fix, fire
// unconditionally re-locked and mutated+persisted whatever pointer it
// was handed, with no check that the pointer was still e.tasks[id]'s
// live entry. If a concurrent Delete removed that same task from
// e.tasks (and from the repository) in the window between scanOnce's
// collection and fire's persist, fire's later
// e.repo.Upsert(persistCtx, snapshot) call would silently resurrect the
// row in the repository - permanently, since nothing tracks it in
// e.tasks anymore to ever delete it again. This test drives that exact
// interleaving deterministically (no timing/race-detector luck needed,
// since every access here is already mutex-protected - this is a logic
// race, not a data race): capture the live pointer the way scanOnce
// would, delete the task, then call fire with the stale pointer exactly
// as a scanOnce goroutine already mid-flight would.
func TestEngineFireSkipsTaskDeletedDuringScan(t *testing.T) {
	repo := newFakeRepository()
	e := New(nil, repo, nil)
	fc := newFakeClock(time.Now())
	e.clock = fc

	task, err := e.Create(context.Background(), Task{Name: "x", Type: Interval, IntervalSeconds: MinIntervalSeconds})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The pointer scanOnce would have collected into its `due` slice
	// before releasing e.mu.
	e.mu.Lock()
	stale := e.tasks[task.ID]
	e.mu.Unlock()

	if err := e.Delete(context.Background(), task.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := repo.get(task.ID); ok {
		t.Fatal("task still in repository immediately after Delete")
	}

	// fire proceeds with the now-stale pointer, exactly as a scanOnce
	// call already in flight when Delete ran would.
	e.fire(context.Background(), stale, fc.Now())

	if _, ok := repo.get(task.ID); ok {
		t.Error("fire resurrected a task in the repository after it was deleted concurrently with the scan that found it due")
	}
	if _, ok := e.Get(task.ID); ok {
		t.Error("fire re-added a deleted task to the in-memory task map")
	}
}

// TestEngineFireDoesNotClobberConcurrentUpdate is the Update-side
// counterpart to TestEngineFireSkipsTaskDeletedDuringScan. Update
// installs a brand-new *Task into e.tasks (e.tasks[id] = &t, a fresh
// address - unlike SetEnabled, which mutates the existing pointer's
// target in place) rather than mutating the existing one, specifically
// so a concurrent scanOnce holding the *old* pointer cannot see the new
// configuration. Before this fix, fire didn't check that either: it
// would mutate and persist the *stale, pre-Update* snapshot, and
// whichever of Update's or the stale fire's e.repo.Upsert call happened
// to land last in the repository would silently win - capable of
// reverting a just-applied Update back to its old configuration with a
// bogus NextRunAt that matches neither the old nor the new schedule.
func TestEngineFireDoesNotClobberConcurrentUpdate(t *testing.T) {
	repo := newFakeRepository()
	e := New(nil, repo, nil)
	fc := newFakeClock(time.Now())
	e.clock = fc

	task, err := e.Create(context.Background(), Task{Name: "original", Type: Interval, IntervalSeconds: MinIntervalSeconds})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// The pointer scanOnce would have collected before Update ran.
	e.mu.Lock()
	stale := e.tasks[task.ID]
	e.mu.Unlock()

	updated, err := e.Update(context.Background(), task.ID, Task{
		Name: "renamed-by-update", Type: Interval, IntervalSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// fire proceeds with the stale pre-Update pointer, exactly as a
	// scanOnce call already in flight when Update ran would.
	e.fire(context.Background(), stale, fc.Now())

	got, ok := e.Get(task.ID)
	if !ok {
		t.Fatal("task missing from in-memory map after raced fire")
	}
	if got.Name != "renamed-by-update" || got.IntervalSeconds != 3600 {
		t.Errorf("Get after raced fire = %+v, want Update's configuration to have won unchanged", got)
	}
	if !got.NextRunAt.Equal(updated.NextRunAt) {
		t.Errorf("NextRunAt after raced fire = %v, want Update's computed value %v (a stale fire must not overwrite it)", got.NextRunAt, updated.NextRunAt)
	}

	persisted, ok := repo.get(task.ID)
	if !ok {
		t.Fatal("task missing from repository after raced fire")
	}
	if persisted.Name != "renamed-by-update" {
		t.Errorf("repository was clobbered by a stale concurrent fire: got name %q, want %q", persisted.Name, "renamed-by-update")
	}
}

// TestEngineCreateRacingConcurrentScan is a regression test for a real,
// narrow data race Phase 8 found and fixed (see CHANGELOG.md): Create
// used to release e.mu before finishing the read of t's fields for its
// own return value, while that same memory was already reachable from
// a concurrent scanOnce/fire call - reachable the moment a Once task's
// RunAt has already arrived by the time the very next scan happens,
// which a task created with an already-due RunAt makes into a narrow
// but real window, not a purely theoretical one.
//
// This can't force the race deterministically - Go's race detector
// only catches a race that actually manifests during a given
// execution, not every theoretically possible one. Tuned empirically,
// not guessed: a single scanner goroutine racing a single serial loop
// of Create calls essentially never caught the (deliberately
// reintroduced, to test this test) original bug even across ten
// separate runs; four concurrent scanners racing ten concurrent
// creator goroutines, repeated across 30 attempts, caught it 5/5 times
// once reintroduced, and passed cleanly 5/5 times with the real fix in
// place - genuine parallelism across actual OS threads finds the exact
// access-ordering condition the race detector needs far more reliably
// than more sequential iterations of a single interleaving pattern.
// Even so: a reintroduced version of the original bug is likely, not
// guaranteed, to be caught on any single run of this test - an honest
// description of what a regression test for a race condition can
// promise, not a gap in this test specifically.
func TestEngineCreateRacingConcurrentScan(t *testing.T) {
	for attempt := 0; attempt < 30; attempt++ {
		bus := eventbus.New()
		e := New(bus, newFakeRepository(), nil)
		fc := newFakeClock(time.Now())
		e.clock = fc

		ctx := context.Background()
		stop := make(chan struct{})
		var wg sync.WaitGroup

		// Multiple concurrent scanners, not just one - more genuine
		// parallelism (distinct OS threads actually racing, not just
		// one goroutine interleaving with another) is more likely to
		// hit the specific access ordering the race detector needs to
		// see within a reasonable number of attempts.
		for s := 0; s < 4; s++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						e.scanOnce(ctx)
					}
				}
			}()
		}

		var creators sync.WaitGroup
		for c := 0; c < 10; c++ {
			creators.Add(1)
			go func() {
				defer creators.Done()
				for i := 0; i < 5; i++ {
					if _, err := e.Create(ctx, Task{
						Name:  "race-test",
						Type:  Once,
						RunAt: fc.Now(), // already due the instant it's created
					}); err != nil {
						t.Errorf("attempt %d: Create: %v", attempt, err)
					}
				}
			}()
		}
		creators.Wait()

		close(stop)
		wg.Wait()
		bus.Close()
	}
}

// TestEngineGoroutinesDoNotLeakAcrossCreateFireDeleteCycles is
// scheduler's counterpart to the goroutine-leak verification already
// done for internal/ipc's event streams, internal/health's check
// lifecycle, and internal/automation's rule execution (see
// CHANGELOG.md, Phase 8) - a gap that work explicitly named as not yet
// independently checked.
//
// Unlike internal/health and internal/automation, scheduler.Engine has
// no per-task or per-firing goroutine at all: one shared runLoop
// goroutine services every task (see engine.go's doc comment on why),
// and fire runs synchronously inside that same goroutine, never a
// spawned one. So the only goroutine this engine can possibly leak is
// runLoop itself; this test confirms that creating, repeatedly firing
// (well past scanInterval each round, via the real ticker - the fake
// clock controls what counts as "due," not when scanOnce runs), and
// deleting many tasks never spawns anything beyond it, and that
// canceling ctx reliably reclaims it.
func TestEngineGoroutinesDoNotLeakAcrossCreateFireDeleteCycles(t *testing.T) {
	repo := newFakeRepository()
	e := New(nil, repo, nil)
	fc := newFakeClock(time.Now())
	e.clock = fc

	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	baseline := settledGoroutineCount(t)

	const taskCount = 50
	ids := make([]string, 0, taskCount)
	for i := 0; i < taskCount; i++ {
		task, err := e.Create(context.Background(), Task{
			Name: fmt.Sprintf("leak-check-%d", i), Type: Interval, IntervalSeconds: MinIntervalSeconds,
		})
		if err != nil {
			t.Fatalf("Create task %d: %v", i, err)
		}
		ids = append(ids, task.ID)
	}

	if n := settledGoroutineCount(t); n != baseline {
		t.Errorf("goroutine count after creating %d tasks = %d, want unchanged baseline %d (Create must not spawn goroutines)", taskCount, n, baseline)
	}

	// Each round advances the fake clock past every task's interval,
	// then waits for the real scan loop (ticking once per real
	// scanInterval, independent of the fake clock) to actually observe
	// them as due and fire all of them - repeated, not just once, to
	// catch a slow cumulative leak a single round wouldn't reveal.
	for round := 0; round < 3; round++ {
		fc.advance(time.Duration(MinIntervalSeconds+1) * time.Second)
		roundTime := fc.Now()
		waitFor(t, 3*time.Second, func() bool {
			for _, id := range ids {
				task, ok := e.Get(id)
				// A task that fired this round has LastRunAt at or
				// after roundTime (fire stamps it with the scan's
				// now, which is >= roundTime since we only advanced
				// the clock forward from here).
				if !ok || task.LastRunAt == nil || task.LastRunAt.Before(roundTime) {
					return false
				}
			}
			return true
		})

		if n := settledGoroutineCount(t); n != baseline {
			t.Fatalf("round %d: goroutine count = %d, want baseline %d", round, n, baseline)
		}
	}

	for _, id := range ids {
		if err := e.Delete(context.Background(), id); err != nil {
			t.Fatalf("Delete %s: %v", id, err)
		}
	}
	if n := settledGoroutineCount(t); n != baseline {
		t.Errorf("goroutine count after deleting all tasks = %d, want baseline %d", n, baseline)
	}

	cancel()
	e.Wait()
	// baseline was captured after Start already ran, so it includes
	// runLoop itself (scheduler.Engine never calls bus.Subscribe, so
	// unlike internal/automation there is no separate eventbus watcher
	// goroutine to account for here - runLoop is the only one).
	if n := settledGoroutineCount(t); n != baseline-1 {
		t.Errorf("goroutine count after Wait() = %d, want %d (runLoop should have exited)", n, baseline-1)
	}
}
