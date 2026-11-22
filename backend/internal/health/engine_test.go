package health

import (
	"context"
	"sync"
	"testing"
	"time"

	"weloury/internal/eventbus"
	"weloury/internal/system"
)

// fakeRepository is a complete in-memory Repository, letting Engine be
// tested without any real persistence layer - notably, without
// depending on internal/storage (and therefore modernc.org/sqlite,
// unreachable in this sandbox - see DEVELOPMENT.md). This is the same
// dependency-inversion payoff described on Repository's doc comment,
// exercised here rather than just asserted there.
type fakeRepository struct {
	mu       sync.Mutex
	statuses map[string]Status
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{statuses: make(map[string]Status)}
}

func (r *fakeRepository) List(context.Context) ([]Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.statuses))
	for _, s := range r.statuses {
		out = append(out, s)
	}
	return out, nil
}

func (r *fakeRepository) Upsert(_ context.Context, c Check) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.statuses[c.ID]
	if !ok {
		existing = Status{State: StateUnknown}
	}
	existing.Check = c
	r.statuses[c.ID] = existing
	return nil
}

func (r *fakeRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.statuses, id)
	return nil
}

func (r *fakeRepository) UpdateResult(_ context.Context, id string, result Result) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.statuses[id]
	if !ok {
		return ErrNotFound
	}
	s.State = result.State
	s.Message = result.Message
	switch result.State {
	case StateHealthy:
		t := result.CheckedAt
		s.LastSuccessAt = &t
		s.FailureCount = 0
	case StateDegraded, StateUnhealthy:
		t := result.CheckedAt
		s.LastFailureAt = &t
		s.FailureCount++
	}
	r.statuses[id] = s
	return nil
}

func (r *fakeRepository) get(id string) (Status, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.statuses[id]
	return s, ok
}

// waitFor polls cond until it returns true or the timeout elapses,
// failing the test on timeout. Used instead of a fixed sleep so tests
// aren't flaky under load, and instead of a longer real ticker interval
// so they aren't slow - Engine runs the first evaluation of every check
// immediately on start (see runLoop), so "waiting" here is really just
// waiting for that first goroutine scheduling to happen, not for a
// ticker to fire.
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

func newTestEngine(sys sysCapabilities, bus *eventbus.Bus) (*Engine, *fakeRepository) {
	repo := newFakeRepository()
	return New(sys, bus, repo, nil), repo
}

func TestEngineCreateSchedulesAndRunsImmediately(t *testing.T) {
	sys := &fakeSys{processes: []system.ProcessInfo{{PID: 1, Name: "worker"}}}
	e, _ := newTestEngine(sys, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status, err := e.Create(context.Background(), Check{
		Name:            "worker running",
		Type:            CheckProcessExists,
		Target:          "worker",
		IntervalSeconds: 60, // long enough that only the immediate first run matters
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if status.State != StateUnknown {
		t.Errorf("Create's returned status.State = %v, want UNKNOWN before the first evaluation completes", status.State)
	}

	waitFor(t, time.Second, func() bool {
		st, ok := e.Get(status.ID)
		return ok && st.State == StateHealthy
	})

	cancel()
	e.Wait()
}

func TestEngineTracksFailureCount(t *testing.T) {
	sys := &fakeSys{processes: nil} // "worker" never exists -> always UNHEALTHY
	e, repo := newTestEngine(sys, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status, err := e.Create(context.Background(), Check{
		Name: "missing worker", Type: CheckProcessExists, Target: "worker", IntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitFor(t, time.Second, func() bool {
		st, ok := e.Get(status.ID)
		return ok && st.State == StateUnhealthy && st.FailureCount == 1
	})

	persisted, ok := repo.get(status.ID)
	if !ok {
		t.Fatal("expected the check to be persisted")
	}
	if persisted.FailureCount != 1 || persisted.State != StateUnhealthy {
		t.Errorf("persisted status = %+v, want FailureCount=1 State=UNHEALTHY", persisted)
	}
	if persisted.LastFailureAt == nil {
		t.Error("expected LastFailureAt to be set")
	}

	cancel()
	e.Wait()
}

func TestEnginePublishesEventsOnlyOnTransition(t *testing.T) {
	sys := &fakeSys{processes: []system.ProcessInfo{{PID: 1, Name: "worker"}}}
	bus := eventbus.New()
	defer bus.Close()
	e, _ := newTestEngine(sys, bus)

	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeCheckPassed, TypeCheckFailed), 16)
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err := e.Create(context.Background(), Check{
		Name: "worker", Type: CheckProcessExists, Target: "worker", IntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != TypeCheckPassed {
			t.Errorf("evt.Type = %q, want %q (transition UNKNOWN -> HEALTHY)", evt.Type, TypeCheckPassed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the transition event")
	}

	// No ticker has fired again yet (interval is 60s) so no further
	// event should arrive - confirms events are transition-only, not
	// per-evaluation.
	select {
	case evt := <-ch:
		t.Fatalf("unexpected second event %v - events must only fire on state transitions", evt.Type)
	case <-time.After(150 * time.Millisecond):
	}

	cancel()
	e.Wait()
}

func TestEngineDiskThresholdPublishesSpecificEvent(t *testing.T) {
	sys := &fakeSys{disks: []system.DiskInfo{{MountPoint: "/", TotalBytes: 100, UsedBytes: 99}}}
	bus := eventbus.New()
	defer bus.Close()
	e, _ := newTestEngine(sys, bus)

	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeDiskThresholdExceeded), 4)
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_, err := e.Create(context.Background(), Check{
		Name: "root disk", Type: CheckDiskThreshold, Target: "/", Threshold: 90, IntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Type != TypeDiskThresholdExceeded {
			t.Errorf("evt.Type = %q, want %q", evt.Type, TypeDiskThresholdExceeded)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TypeDiskThresholdExceeded")
	}

	cancel()
	e.Wait()
}

func TestEngineUpdatePreservesHistory(t *testing.T) {
	sys := &fakeSys{processes: nil}
	e, _ := newTestEngine(sys, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status, err := e.Create(context.Background(), Check{
		Name: "missing worker", Type: CheckProcessExists, Target: "worker", IntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	waitFor(t, time.Second, func() bool {
		st, _ := e.Get(status.ID)
		return st.FailureCount >= 1
	})
	before, _ := e.Get(status.ID)

	updated, err := e.Update(context.Background(), status.ID, Check{
		Name: "missing worker (renamed)", Type: CheckProcessExists, Target: "worker", IntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.FailureCount != before.FailureCount {
		t.Errorf("FailureCount after Update = %d, want preserved value %d", updated.FailureCount, before.FailureCount)
	}
	if updated.Name != "missing worker (renamed)" {
		t.Errorf("Name after Update = %q, want the new name", updated.Name)
	}

	cancel()
	e.Wait()
}

func TestEngineDeleteStopsScheduling(t *testing.T) {
	sys := &fakeSys{processes: []system.ProcessInfo{{PID: 1, Name: "worker"}}}
	e, repo := newTestEngine(sys, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status, err := e.Create(context.Background(), Check{
		Name: "worker", Type: CheckProcessExists, Target: "worker", IntervalSeconds: 60,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := e.Delete(context.Background(), status.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := e.Get(status.ID); ok {
		t.Error("expected Get to report the check no longer exists after Delete")
	}
	if _, ok := repo.get(status.ID); ok {
		t.Error("expected the repository record to be removed after Delete")
	}

	// Deleting an already-deleted check is a clean not-found, not a panic.
	if err := e.Delete(context.Background(), status.ID); err != ErrNotFound {
		t.Errorf("second Delete err = %v, want ErrNotFound", err)
	}

	cancel()
	e.Wait()
}

func TestEngineRunNowBypassesSchedule(t *testing.T) {
	sys := &fakeSys{processes: []system.ProcessInfo{{PID: 1, Name: "worker"}}}
	e, _ := newTestEngine(sys, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	status, err := e.Create(context.Background(), Check{
		Name: "worker", Type: CheckProcessExists, Target: "worker", IntervalSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	waitFor(t, time.Second, func() bool {
		st, _ := e.Get(status.ID)
		return st.State == StateHealthy
	})

	sys.processes = nil // "worker" now gone, but the next scheduled run is an hour away
	updated, err := e.RunNow(context.Background(), status.ID)
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if updated.State != StateUnhealthy {
		t.Errorf("RunNow's returned state = %v, want UNHEALTHY (bypassing the hour-long schedule)", updated.State)
	}

	cancel()
	e.Wait()
}

func TestEngineValidationRejectsBadConfig(t *testing.T) {
	e, _ := newTestEngine(&fakeSys{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_ = e.Start(ctx)
	defer func() { cancel(); e.Wait() }()

	cases := []Check{
		{Name: "", Type: CheckProcessExists, Target: "x"},                      // empty name
		{Name: "x", Type: "bogus", Target: "x"},                                // unrecognized type
		{Name: "x", Type: CheckProcessExists, Target: ""},                      // missing target
		{Name: "x", Type: CheckDiskThreshold, Target: "/", Threshold: 0},       // missing threshold
		{Name: "x", Type: CheckProcessExists, Target: "x", IntervalSeconds: 1}, // below MinIntervalSeconds
		{Name: "x", Type: CheckCommand, CommandArgs: nil},                      // missing command
	}
	for i, c := range cases {
		if _, err := e.Create(context.Background(), c); err == nil {
			t.Errorf("case %d: expected validation error, got none", i)
		}
	}
}

func TestEngineWaitReturnsAfterContextCancel(t *testing.T) {
	e, _ := newTestEngine(&fakeSys{processes: []system.ProcessInfo{{PID: 1, Name: "worker"}}}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := e.Create(context.Background(), Check{
			Name: "worker", Type: CheckProcessExists, Target: "worker", IntervalSeconds: 60,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	cancel()
	done := make(chan struct{})
	go func() { e.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait() did not return promptly after context cancellation - possible goroutine leak")
	}
}
