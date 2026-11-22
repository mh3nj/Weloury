package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sync"
	"testing"
	"time"

	"weloury/internal/eventbus"
	"weloury/internal/scheduler"
)

// fakeClock lets tests control "now" deterministically instead of
// sleeping through real intervals - mirrors internal/scheduler's
// identical helper (same clock interface, same reasoning).
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

// settledGoroutineCount returns runtime.NumGoroutine() once it has held
// steady across a few consecutive samples, rather than a single raw
// reading. A single reading is flaky against goroutines that are merely
// mid-exit (e.g. between a deferred wg.Done() and the goroutine function
// actually returning, or the Go runtime's own background workers) but
// not genuinely leaked - waiting for stability filters that out without
// weakening what the assertion actually checks for.
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
// demonstrates.
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

func (r *fakeRepository) Upsert(_ context.Context, rule Rule) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	existing, ok := r.statuses[rule.ID]
	if !ok {
		existing = Status{}
	}
	existing.Rule = rule
	r.statuses[rule.ID] = existing
	return nil
}

func (r *fakeRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.statuses, id)
	return nil
}

func (r *fakeRepository) UpdateRunResult(_ context.Context, id string, result RunResult) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.statuses[id]
	if !ok {
		return ErrNotFound
	}
	s.LastRunStatus = result.Status
	s.LastRunMessage = result.Message
	ranAt := result.RanAt
	s.LastRunAt = &ranAt
	s.RunCount++
	s.Enabled = result.Enabled
	s.ConsecutiveFailures = result.ConsecutiveFailures
	r.statuses[id] = s
	return nil
}

func (r *fakeRepository) get(id string) (Status, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.statuses[id]
	return s, ok
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

func newTestEngine(sys sysCapabilities, bus *eventbus.Bus) (*Engine, *fakeRepository, *fakeLauncher) {
	repo := newFakeRepository()
	launcher := &fakeLauncher{}
	e := New(sys, bus, repo, nil)
	e.launcher = launcher
	return e, repo, launcher
}

func startEngine(t *testing.T, e *Engine) (context.Context, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return ctx, cancel
}

func TestEngineEventTriggerFiresOnMatchingEvent(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, _, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeRuleExecuted), 4)
	defer unsub()

	_, err := e.Create(context.Background(), Rule{
		Name:    "on custom test event",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.something_happened"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bus.Publish(eventbus.Event{Type: "test.something_happened", Source: "test"})

	select {
	case evt := <-ch:
		if evt.Type != TypeRuleExecuted {
			t.Errorf("evt.Type = %q, want %q", evt.Type, TypeRuleExecuted)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for TypeRuleExecuted")
	}

	if len(sys.startedServicesSnapshot()) != 1 || sys.startedServicesSnapshot()[0] != "nginx" {
		t.Errorf("startedServices = %v, want [nginx]", sys.startedServicesSnapshot())
	}
}

func TestEngineDoesNotFireOnNonMatchingEventType(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, _, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	_, err := e.Create(context.Background(), Rule{
		Name:    "on X",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.x"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bus.Publish(eventbus.Event{Type: "test.y", Source: "test"})

	time.Sleep(150 * time.Millisecond)
	if len(sys.startedServicesSnapshot()) != 0 {
		t.Errorf("expected no action for a non-matching event type, got startedServices = %v", sys.startedServicesSnapshot())
	}
}

func TestEngineConditionsNarrowMatch(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, _, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	_, err := e.Create(context.Background(), Rule{
		Name:       "on check X failing",
		Trigger:    Trigger{Type: TriggerEvent, EventType: "test.check_failed"},
		Conditions: []Condition{{Field: "check_id", Equals: "chk_target"}},
		Actions:    []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	otherPayload, _ := json.Marshal(map[string]string{"check_id": "chk_other"})
	bus.Publish(eventbus.Event{Type: "test.check_failed", Source: "test", Payload: otherPayload})
	time.Sleep(150 * time.Millisecond)
	if len(sys.startedServicesSnapshot()) != 0 {
		t.Fatalf("expected no action for a non-matching condition, got %v", sys.startedServicesSnapshot())
	}

	targetPayload, _ := json.Marshal(map[string]string{"check_id": "chk_target"})
	bus.Publish(eventbus.Event{Type: "test.check_failed", Source: "test", Payload: targetPayload})
	waitFor(t, time.Second, func() bool { return len(sys.startedServicesSnapshot()) == 1 })
}

func TestEngineScheduleTriggerFiresOnMatchingTaskDue(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, _, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	_, err := e.Create(context.Background(), Rule{
		Name:    "on my schedule",
		Trigger: Trigger{Type: TriggerSchedule, ScheduleID: "task_target"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	otherPayload, _ := json.Marshal(map[string]string{"task_id": "task_other"})
	bus.Publish(eventbus.Event{Type: scheduler.TypeTaskDue, Source: "scheduler.engine", Payload: otherPayload})
	time.Sleep(150 * time.Millisecond)
	if len(sys.startedServicesSnapshot()) != 0 {
		t.Fatalf("expected no action for a non-matching schedule id, got %v", sys.startedServicesSnapshot())
	}

	targetPayload, _ := json.Marshal(map[string]string{"task_id": "task_target"})
	bus.Publish(eventbus.Event{Type: scheduler.TypeTaskDue, Source: "scheduler.engine", Payload: targetPayload})
	waitFor(t, time.Second, func() bool { return len(sys.startedServicesSnapshot()) == 1 })
}

func TestEngineCooldownSkipsRapidRefire(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, _, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeRuleExecuted), 8)
	defer unsub()

	_, err := e.Create(context.Background(), Rule{
		Name:            "cooldown test",
		Trigger:         Trigger{Type: TriggerEvent, EventType: "test.flap"},
		Actions:         []Action{{Type: ActionStartService, ServiceName: "nginx"}},
		CooldownSeconds: 3600,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bus.Publish(eventbus.Event{Type: "test.flap", Source: "test"})
	first := <-ch
	if first.Type != TypeRuleExecuted {
		t.Fatalf("unexpected first event type %q", first.Type)
	}
	waitFor(t, time.Second, func() bool { return len(sys.startedServicesSnapshot()) == 1 })

	bus.Publish(eventbus.Event{Type: "test.flap", Source: "test"})
	select {
	case <-ch: // the skip itself still publishes TypeRuleExecuted
	case <-time.After(time.Second):
		t.Fatal("expected a skipped-cooldown TypeRuleExecuted event")
	}

	// The action must not have run a second time.
	if len(sys.startedServicesSnapshot()) != 1 {
		t.Errorf("startedServices = %v, want still just 1 (second trigger should have been cooled down)", sys.startedServicesSnapshot())
	}
}

// TestEngineZeroCooldownStillHasAFloor is Phase 8 hardening for the gap
// documented on minRuleCooldownSeconds: CooldownSeconds: 0 (the default
// both this package and the frontend form ship) means "no cooldown at
// all" by the field's own explicit design, which leaves spec section
// 11's own flagship example - "WHEN process X crashes THEN restart
// process X" - with nothing bounding how fast a restart loop could
// spin if the restarted process crashes again immediately. This proves
// the floor actually applies even when CooldownSeconds is explicitly
// left at 0, not just when a rule author set a real value: two rapid
// firings with no configured cooldown must still only execute the
// action once, not twice, exactly like TestEngineCooldownSkipsRapidRefire
// above but with the field left unset instead of set to an hour.
func TestEngineZeroCooldownStillHasAFloor(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, _, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeRuleExecuted), 8)
	defer unsub()

	_, err := e.Create(context.Background(), Rule{
		Name:    "zero cooldown floor test",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.crashloop"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
		// CooldownSeconds deliberately left at its zero value - the
		// point of this test is that the floor applies even then.
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bus.Publish(eventbus.Event{Type: "test.crashloop", Source: "test"})
	first := <-ch
	if first.Type != TypeRuleExecuted {
		t.Fatalf("unexpected first event type %q", first.Type)
	}
	waitFor(t, time.Second, func() bool { return len(sys.startedServicesSnapshot()) == 1 })

	// Immediately re-fire, simulating the crashed-again-instantly case.
	bus.Publish(eventbus.Event{Type: "test.crashloop", Source: "test"})
	select {
	case <-ch: // the skip itself still publishes TypeRuleExecuted
	case <-time.After(time.Second):
		t.Fatal("expected a skipped-cooldown TypeRuleExecuted event even with CooldownSeconds unset")
	}

	if len(sys.startedServicesSnapshot()) != 1 {
		t.Errorf("startedServices = %v, want still just 1 (the floor should have caught the immediate re-fire)", sys.startedServicesSnapshot())
	}

	// And confirm the floor isn't permanent: after it elapses, the rule
	// can fire again - this is a rate limit, not a one-shot latch.
	time.Sleep(1100 * time.Millisecond)
	bus.Publish(eventbus.Event{Type: "test.crashloop", Source: "test"})
	waitFor(t, time.Second, func() bool { return len(sys.startedServicesSnapshot()) == 2 })
}

func TestEngineSelfTriggerPreventionAtValidation(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	e, _, _ := newTestEngine(&fakeSys{}, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	_, err := e.Create(context.Background(), Rule{
		Name:    "self-trigger attempt",
		Trigger: Trigger{Type: TriggerEvent, EventType: "automation.rule_executed"},
		Actions: []Action{{Type: ActionNotify, Message: "hi"}},
	})
	if err == nil {
		t.Fatal("expected validation to reject a trigger on automation's own event namespace")
	}
}

func TestEngineDefenseInDepthIgnoresAutomationEvents(t *testing.T) {
	// Even if a rule somehow existed with an automation.* EventType
	// (bypassing validation, e.g. injected directly into the fake
	// repository the way a corrupted database row might), Engine must
	// still refuse to act on its own events - see handleEvent's
	// defense-in-depth check.
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	repo := newFakeRepository()
	_ = repo.Upsert(context.Background(), Rule{
		ID: "rule_bad", Name: "bypassed rule", Enabled: true,
		Trigger: Trigger{Type: TriggerEvent, EventType: "automation.rule_executed"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})

	launcher := &fakeLauncher{}
	e := New(sys, bus, repo, nil)
	e.launcher = launcher
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { cancel(); e.Wait() }()

	bus.Publish(eventbus.Event{Type: TypeRuleExecuted, Source: "automation.engine"})
	time.Sleep(150 * time.Millisecond)

	if len(sys.startedServicesSnapshot()) != 0 {
		t.Errorf("expected the bypassed rule to never fire, got startedServices = %v", sys.startedServicesSnapshot())
	}
}

func TestEngineFailedActionStillReportsFailedStatus(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{serviceErr: errors.New("service manager unavailable")}
	e, repo, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	rule, err := e.Create(context.Background(), Rule{
		Name:    "will fail",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.fail"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bus.Publish(eventbus.Event{Type: "test.fail", Source: "test"})

	waitFor(t, time.Second, func() bool {
		st, ok := e.Get(rule.ID)
		return ok && st.LastRunStatus == RunFailed
	})

	persisted, ok := repo.get(rule.ID)
	if !ok || persisted.LastRunStatus != RunFailed {
		t.Errorf("persisted status = %+v, want LastRunStatus=RunFailed", persisted)
	}
}

func TestEngineUpdatePreservesRunHistory(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, _, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	rule, err := e.Create(context.Background(), Rule{
		Name:    "original",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.go"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	bus.Publish(eventbus.Event{Type: "test.go", Source: "test"})
	waitFor(t, time.Second, func() bool {
		st, _ := e.Get(rule.ID)
		return st.RunCount == 1
	})

	updated, err := e.Update(context.Background(), rule.ID, Rule{
		Name:    "renamed",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.go"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.RunCount != 1 {
		t.Errorf("RunCount after Update = %d, want preserved value 1", updated.RunCount)
	}
	if updated.Name != "renamed" {
		t.Errorf("Name after Update = %q, want renamed", updated.Name)
	}
}

func TestEngineDeleteStopsMatching(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, repo, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	rule, err := e.Create(context.Background(), Rule{
		Name:    "to delete",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.go"},
		Actions: []Action{{Type: ActionStartService, ServiceName: "nginx"}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := e.Delete(context.Background(), rule.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := e.Get(rule.ID); ok {
		t.Error("expected rule to be gone after Delete")
	}
	if _, ok := repo.get(rule.ID); ok {
		t.Error("expected repository record to be gone after Delete")
	}

	bus.Publish(eventbus.Event{Type: "test.go", Source: "test"})
	time.Sleep(150 * time.Millisecond)
	if len(sys.startedServicesSnapshot()) != 0 {
		t.Error("expected a deleted rule to never fire")
	}

	if err := e.Delete(context.Background(), rule.ID); err != ErrNotFound {
		t.Errorf("second Delete err = %v, want ErrNotFound", err)
	}
}

func TestEngineValidationRejectsBadConfig(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	e, _, _ := newTestEngine(&fakeSys{}, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	cases := []Rule{
		{Name: "", Trigger: Trigger{Type: TriggerEvent, EventType: "x"}, Actions: []Action{{Type: ActionNotify, Message: "hi"}}},
		{Name: "x", Trigger: Trigger{Type: TriggerEvent, EventType: ""}, Actions: []Action{{Type: ActionNotify, Message: "hi"}}},
		{Name: "x", Trigger: Trigger{Type: TriggerEvent, EventType: "y"}, Actions: nil},
		{Name: "x", Trigger: Trigger{Type: "bogus"}, Actions: []Action{{Type: ActionNotify, Message: "hi"}}},
		{Name: "x", Trigger: Trigger{Type: TriggerEvent, EventType: "y"}, Actions: []Action{{Type: ActionRestartProcess}}}, // missing process_name/launch_command
	}
	for i, c := range cases {
		if _, err := e.Create(context.Background(), c); err == nil {
			t.Errorf("case %d: expected validation error, got none", i)
		}
	}
}

func TestEngineWaitReturnsAfterContextCancel(t *testing.T) {
	e, _, _ := newTestEngine(&fakeSys{}, eventbus.New())
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	for i := 0; i < 5; i++ {
		if _, err := e.Create(context.Background(), Rule{
			Name: "x", Trigger: Trigger{Type: TriggerEvent, EventType: "test.go"},
			Actions: []Action{{Type: ActionNotify, Message: "hi"}},
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

// TestEngineGoroutinesDoNotLeakAcrossCreateExecuteDeleteCycles is the
// automation counterpart to the goroutine-leak verification already
// done for internal/ipc's event streams and internal/health's check
// lifecycle (see CHANGELOG.md, Phase 8) - a gap that work explicitly
// named as not yet independently checked.
//
// Engine's design means most of this should structurally be a
// non-event: Create/Update/Delete never spawn a goroutine (see their
// code - only tryRun does, one per matched rule execution, bounded by
// e.sem), so creating and deleting many rules should never move the
// goroutine count at all. The real question is the bounded-concurrency
// execution path itself: a burst of triggering events larger than
// DefaultMaxConcurrentExecutions must not leave any of those per-
// execution goroutines behind once every execution completes, across
// repeated bursts (not just one, to catch a slow cumulative leak a
// single cycle wouldn't reveal).
func TestEngineGoroutinesDoNotLeakAcrossCreateExecuteDeleteCycles(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, repo, _ := newTestEngine(sys, bus)
	fc := newFakeClock(time.Now())
	e.clock = fc
	ctx, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	baseline := settledGoroutineCount(t)

	const ruleCount = 50 // well above DefaultMaxConcurrentExecutions (8)
	ids := make([]string, 0, ruleCount)
	for i := 0; i < ruleCount; i++ {
		status, err := e.Create(ctx, Rule{
			Name:    fmt.Sprintf("leak-check-%d", i),
			Trigger: Trigger{Type: TriggerEvent, EventType: "test.leak_check"},
			Actions: []Action{{Type: ActionCreateEvent, Message: "fired"}},
		})
		if err != nil {
			t.Fatalf("Create rule %d: %v", i, err)
		}
		ids = append(ids, status.ID)
	}

	if n := settledGoroutineCount(t); n != baseline {
		t.Errorf("goroutine count after creating %d rules = %d, want unchanged baseline %d (Create must not spawn goroutines)", ruleCount, n, baseline)
	}

	// Fire several bursts, each triggering all 50 rules at once - far
	// more than the semaphore's 8 slots, so this genuinely exercises
	// queuing/blocking on e.sem, not just a burst small enough to run
	// entirely in parallel.
	for burst := 0; burst < 5; burst++ {
		if burst > 0 {
			// Clear every rule's cooldown floor (minRuleCooldownSeconds)
			// deterministically rather than sleeping through it - see
			// checkCooldown. Without this, bursts fired back-to-back
			// against a fake clock that never advances would all but
			// the first be skipped for cooldown, which is correct
			// production behavior but would make this test either
			// flaky (racing real time) or misleadingly slow (sleeping
			// for real).
			fc.advance(2 * time.Second)
		}
		bus.Publish(eventbus.Event{Type: "test.leak_check", Source: "test"})

		wantRunCount := burst + 1
		waitFor(t, 3*time.Second, func() bool {
			for _, id := range ids {
				st, ok := repo.get(id)
				if !ok || st.RunCount < wantRunCount {
					return false
				}
			}
			return true
		})

		if n := settledGoroutineCount(t); n != baseline {
			t.Fatalf("burst %d: goroutine count = %d, want baseline %d (a per-execution goroutine leaked)", burst, n, baseline)
		}
	}

	for _, id := range ids {
		if err := e.Delete(ctx, id); err != nil {
			t.Fatalf("Delete %s: %v", id, err)
		}
	}

	if n := settledGoroutineCount(t); n != baseline {
		t.Errorf("goroutine count after deleting all rules = %d, want baseline %d", n, baseline)
	}

	cancel()
	e.Wait()
	// baseline was captured after Start already ran, so it includes
	// two goroutines Start's subscription is responsible for:
	// consumeEvents itself, and the watcher goroutine internal to
	// eventbus.Bus.Subscribe that calls unsubscribe when ctx is
	// canceled (see bus.go's Subscribe). e.Wait() only guarantees the
	// former has exited; settledGoroutineCount's stabilization window
	// is what makes waiting for the latter (not otherwise
	// synchronized on) reliable here rather than a race.
	if n := settledGoroutineCount(t); n != baseline-2 {
		t.Errorf("goroutine count after Wait() = %d, want %d (consumeEvents and the bus watcher goroutine should both have exited)", n, baseline-2)
	}
}

// TestEngineCircuitBreakerAutoDisablesAfterConsecutiveFailures is the
// core behavior test for MaxConsecutiveFailures (validate.go): a rule
// whose action keeps genuinely failing must be auto-disabled after
// exactly that many consecutive failures, must publish
// TypeRuleAutoDisabled exactly once when it trips, must stop matching
// events once disabled, and a skip in the middle of the streak (a
// cooldown skip here) must neither extend nor reset it - mirroring how
// checkCooldown itself already treats skips.
func TestEngineCircuitBreakerAutoDisablesAfterConsecutiveFailures(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, repo, launcher := newTestEngine(sys, bus)
	launcher.err = errors.New("simulated launch failure")
	fc := newFakeClock(time.Now())
	e.clock = fc

	autoDisabledEvents := make(chan eventbus.Event, 8)
	watchCh, unwatch := bus.Subscribe(context.Background(), func(evt eventbus.Event) bool {
		return evt.Type == TypeRuleAutoDisabled
	}, 8)
	defer unwatch()
	go func() {
		for evt := range watchCh {
			autoDisabledEvents <- evt
		}
	}()

	ctx, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	status, err := e.Create(ctx, Rule{
		Name:    "flaky restart",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.circuit"},
		Actions: []Action{{Type: ActionRestartProcess, ProcessName: "flaky", LaunchCommand: []string{"flaky"}}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := status.ID

	fire := func(wantRunCount int) {
		t.Helper()
		fc.advance(2 * time.Second) // clear the cooldown floor deterministically
		bus.Publish(eventbus.Event{Type: "test.circuit", Source: "test"})
		waitFor(t, 3*time.Second, func() bool {
			st, ok := repo.get(id)
			return ok && st.RunCount >= wantRunCount
		})
	}

	// First execution: checkCooldown never blocks a rule's very first
	// run (lastRunAt is nil), so the skip check belongs after this one,
	// not before it.
	fire(1)
	if st, _ := repo.get(id); st.ConsecutiveFailures != 1 || st.LastRunStatus != RunFailed {
		t.Fatalf("first failure: got ConsecutiveFailures=%d LastRunStatus=%v, want 1/RunFailed", st.ConsecutiveFailures, st.LastRunStatus)
	}

	// A same-instant re-fire now must be skipped by the cooldown floor
	// - must not count as, or interfere with, the failure streak.
	bus.Publish(eventbus.Event{Type: "test.circuit", Source: "test"})
	time.Sleep(50 * time.Millisecond) // let it actually get skipped, not just not-yet-processed
	if st, _ := repo.get(id); st.RunCount != 1 || st.ConsecutiveFailures != 1 {
		t.Fatalf("a same-instant re-fire should have been skipped, not executed: RunCount=%d ConsecutiveFailures=%d, want 1/1", st.RunCount, st.ConsecutiveFailures)
	}

	for i := 2; i < MaxConsecutiveFailures; i++ {
		fire(i)
		st, ok := repo.get(id)
		if !ok {
			t.Fatalf("rule missing from repository after failure %d", i)
		}
		if st.LastRunStatus != RunFailed {
			t.Fatalf("failure %d: LastRunStatus = %v, want RunFailed", i, st.LastRunStatus)
		}
		if st.ConsecutiveFailures != i {
			t.Fatalf("failure %d: ConsecutiveFailures = %d, want %d", i, st.ConsecutiveFailures, i)
		}
		if !st.Enabled {
			t.Fatalf("failure %d: rule was disabled before reaching MaxConsecutiveFailures (%d)", i, MaxConsecutiveFailures)
		}
	}

	// The MaxConsecutiveFailures-th failure must trip the breaker.
	fire(MaxConsecutiveFailures)
	st, ok := e.Get(id)
	if !ok {
		t.Fatal("rule missing after tripping the breaker")
	}
	if st.ConsecutiveFailures != MaxConsecutiveFailures {
		t.Errorf("ConsecutiveFailures = %d, want %d", st.ConsecutiveFailures, MaxConsecutiveFailures)
	}
	if st.Enabled {
		t.Error("Enabled = true after tripping the breaker, want false")
	}

	select {
	case evt := <-autoDisabledEvents:
		var payload ruleAutoDisabledPayload
		if err := json.Unmarshal(evt.Payload, &payload); err != nil {
			t.Fatalf("unmarshal TypeRuleAutoDisabled payload: %v", err)
		}
		if payload.RuleID != id || payload.ConsecutiveFailures != MaxConsecutiveFailures {
			t.Errorf("TypeRuleAutoDisabled payload = %+v, want RuleID=%q ConsecutiveFailures=%d", payload, id, MaxConsecutiveFailures)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("TypeRuleAutoDisabled was not published when the breaker tripped")
	}

	// A disabled rule must stop matching entirely - one more publish
	// must not change RunCount at all, not even to record another
	// failure.
	fc.advance(2 * time.Second)
	bus.Publish(eventbus.Event{Type: "test.circuit", Source: "test"})
	time.Sleep(100 * time.Millisecond)
	if st, _ := repo.get(id); st.RunCount != MaxConsecutiveFailures {
		t.Errorf("RunCount after a post-trip event = %d, want unchanged %d (a disabled rule must not execute)", st.RunCount, MaxConsecutiveFailures)
	}
	select {
	case evt := <-autoDisabledEvents:
		t.Errorf("TypeRuleAutoDisabled published a second time (%+v) - should only fire once, on the transition", evt)
	default:
	}
}

// TestEngineCircuitBreakerResetsOnUpdateAndOnSuccess covers the two
// ways a tripped (or nearly tripped) streak is meant to clear: editing
// the rule via Update (see trackedRule.setRule's doc comment), and a
// genuine RunSucceeded (see trackedRule.recordOutcome).
func TestEngineCircuitBreakerResetsOnUpdateAndOnSuccess(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, repo, launcher := newTestEngine(sys, bus)
	launcher.err = errors.New("simulated launch failure")
	fc := newFakeClock(time.Now())
	e.clock = fc
	ctx, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	rule := Rule{
		Name:    "flaky restart",
		Trigger: Trigger{Type: TriggerEvent, EventType: "test.circuit_reset"},
		Actions: []Action{{Type: ActionRestartProcess, ProcessName: "flaky", LaunchCommand: []string{"flaky"}}},
	}
	status, err := e.Create(ctx, rule)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := status.ID

	fire := func(wantRunCount int) {
		t.Helper()
		fc.advance(2 * time.Second)
		bus.Publish(eventbus.Event{Type: "test.circuit_reset", Source: "test"})
		waitFor(t, 3*time.Second, func() bool {
			st, ok := repo.get(id)
			return ok && st.RunCount >= wantRunCount
		})
	}

	// Build up a streak short of tripping the breaker.
	for i := 1; i <= 3; i++ {
		fire(i)
	}
	if st, _ := e.Get(id); st.ConsecutiveFailures != 3 {
		t.Fatalf("ConsecutiveFailures before Update = %d, want 3", st.ConsecutiveFailures)
	}

	// Update - even one that doesn't touch Actions/Trigger at all -
	// must reset the streak (see setRule's doc comment). Enabled must
	// be carried forward explicitly: unlike Create, Update does not
	// force it to true - it's the caller's field to set, same as a
	// real "edit rule" form would submit the current toggle state.
	rule.Name = "flaky restart (edited)"
	rule.Enabled = true
	if _, err := e.Update(ctx, id, rule); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if st, _ := e.Get(id); st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after Update = %d, want 0", st.ConsecutiveFailures)
	}

	// Now drive it right up to the edge of tripping again...
	for i := 1; i < MaxConsecutiveFailures; i++ {
		fire(3 + i)
	}
	if st, _ := e.Get(id); st.ConsecutiveFailures != MaxConsecutiveFailures-1 {
		t.Fatalf("ConsecutiveFailures = %d, want %d", st.ConsecutiveFailures, MaxConsecutiveFailures-1)
	}

	// ...then fix the underlying problem and confirm a genuine success
	// resets the streak too, without needing an Update.
	launcher.err = nil
	fire(3 + MaxConsecutiveFailures)
	st, ok := e.Get(id)
	if !ok {
		t.Fatal("rule missing after recovery")
	}
	if st.LastRunStatus != RunSucceeded {
		t.Fatalf("LastRunStatus after fixing the launcher = %v, want RunSucceeded", st.LastRunStatus)
	}
	if st.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after a success = %d, want 0", st.ConsecutiveFailures)
	}
	if !st.Enabled {
		t.Error("Enabled = false after a success one step short of tripping, want true (it never should have tripped)")
	}
}

// TestEngineCooldownDoesNotMaskAMismatchedConditionsSkip is a
// regression test for a real behavioral finding from crash-testing
// (see CHANGELOG.md): tryRun evaluates Conditions before cooldown, not
// the reverse, specifically so a rule already cooling down from its
// own prior execution still reports RunSkippedConditions - accurately
// - for an event its Conditions never matched in the first place,
// rather than the misleading RunSkippedCooldown a cooldown-first check
// would report for the exact same event. handleEvent's matched list is
// filtered only by Trigger.Type (see ruleMatchesEvent), so tryRun is
// where Conditions actually get enforced - this scenario is entirely
// ordinary, not a contrived edge case: any two rules (or, as here, the
// same rule evaluated against two different events) sharing one
// Trigger.EventType but different Conditions hit it on every event
// that isn't "theirs."
func TestEngineCooldownDoesNotMaskAMismatchedConditionsSkip(t *testing.T) {
	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, _, _ := newTestEngine(sys, bus)
	_, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	ch, unsub := bus.Subscribe(context.Background(), eventbus.MatchTypes(TypeRuleExecuted), 8)
	defer unsub()

	_, err := e.Create(context.Background(), Rule{
		Name:            "conditions-vs-cooldown ordering",
		Trigger:         Trigger{Type: TriggerEvent, EventType: "test.check_failed"},
		Conditions:      []Condition{{Field: "check_id", Equals: "check-A"}},
		Actions:         []Action{{Type: ActionCreateEvent, Message: "matched"}},
		CooldownSeconds: 3600, // long enough that the second event below is unambiguously still within it
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// First event: matches Conditions, executes for real, starting the
	// cooldown window.
	bus.Publish(eventbus.Event{Type: "test.check_failed", Source: "test", Payload: json.RawMessage(`{"check_id":"check-A"}`)})
	first := readRuleExecuted(t, ch)
	if first.Status != RunSucceeded {
		t.Fatalf("first execution status = %v, want RunSucceeded", first.Status)
	}

	// Second event: same Trigger.EventType (so it still reaches
	// tryRun), but a check_id Conditions was never going to match - and
	// the rule also happens to be cooling down right now from the
	// first event. The correct, accurate report is
	// RunSkippedConditions: this event was never this rule's concern,
	// independent of cooldown.
	bus.Publish(eventbus.Event{Type: "test.check_failed", Source: "test", Payload: json.RawMessage(`{"check_id":"check-B"}`)})
	second := readRuleExecuted(t, ch)
	if second.Status != RunSkippedConditions {
		t.Errorf("second execution status = %v, want RunSkippedConditions (got message: %q) - cooldown must not shadow a genuine conditions mismatch", second.Status, second.Message)
	}
}

// readRuleExecuted reads one TypeRuleExecuted event off ch and decodes
// its payload, failing the test if none arrives within a second.
func readRuleExecuted(t *testing.T, ch <-chan eventbus.Event) ruleExecutedPayload {
	t.Helper()
	select {
	case evt := <-ch:
		var p ruleExecutedPayload
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			t.Fatalf("unmarshal TypeRuleExecuted payload: %v", err)
		}
		return p
	case <-time.After(time.Second):
		t.Fatal("expected a TypeRuleExecuted event, got none within 1s")
		return ruleExecutedPayload{}
	}
}

// TestEngineActionTimeoutPreventsSemaphoreStarvation is the
// integration-level counterpart to
// TestActionRunCommandRespectsContextDeadline: it proves the full
// wiring (Engine.actionTimeout → executeActions →
// context.WithTimeout → actionRunCommand → a real hanging process)
// holds together, not just that actionRunCommand alone respects a
// deadline handed to it directly.
//
// This is the scenario the missing test coverage actually mattered
// for: DefaultMaxConcurrentExecutions bounds how many rules can run at
// once specifically so a burst of triggering events can't spawn
// unbounded goroutines - but a bound is only actually a *safety*
// property if whatever holds a slot is guaranteed to eventually
// release it. A run_command action wired to a command that hangs
// indefinitely, with no per-action timeout, would hold its semaphore
// slot forever; enough such rules (as few as
// DefaultMaxConcurrentExecutions of them) would permanently exhaust
// automation's entire execution capacity - every other rule queuing
// forever behind a full semaphore, silently, with no error, until the
// whole process restarts. This test creates one more hanging rule than
// there are semaphore slots and confirms every single one still
// completes (with a timeout failure, as expected) well within the
// test's bound - proving the semaphore can never actually starve this
// way.
func TestEngineActionTimeoutPreventsSemaphoreStarvation(t *testing.T) {
	sleepBin, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no `sleep` binary available in this environment")
	}

	bus := eventbus.New()
	defer bus.Close()
	sys := &fakeSys{}
	e, repo, _ := newTestEngine(sys, bus)
	e.actionTimeout = 100 * time.Millisecond // real production value is 30s; see defaultActionTimeout
	ctx, cancel := startEngine(t, e)
	defer func() { cancel(); e.Wait() }()

	const ruleCount = DefaultMaxConcurrentExecutions + 1 // one more than the semaphore has slots for
	ids := make([]string, 0, ruleCount)
	for i := 0; i < ruleCount; i++ {
		eventType := fmt.Sprintf("test.hang_%d", i)
		status, err := e.Create(ctx, Rule{
			Name:    fmt.Sprintf("hanging-command-%d", i),
			Trigger: Trigger{Type: TriggerEvent, EventType: eventType},
			Actions: []Action{{Type: ActionRunCommand, CommandArgs: []string{sleepBin, "30"}}},
		})
		if err != nil {
			t.Fatalf("Create rule %d: %v", i, err)
		}
		ids = append(ids, status.ID)
		bus.Publish(eventbus.Event{Type: eventbus.Type(eventType), Source: "test"})
	}

	// If the semaphore could starve, at least one of these would never
	// reach RunCount 1 and this would time out at 3s despite every
	// command sleeping for 30s.
	waitFor(t, 3*time.Second, func() bool {
		for _, id := range ids {
			st, ok := repo.get(id)
			if !ok || st.RunCount < 1 {
				return false
			}
		}
		return true
	})

	for i, id := range ids {
		st, _ := repo.get(id)
		if st.LastRunStatus != RunFailed {
			t.Errorf("rule %d: LastRunStatus = %v, want RunFailed (a 30s sleep against a 100ms action timeout)", i, st.LastRunStatus)
		}
	}
}
