package storage

import (
	"context"
	"testing"
	"time"

	"weloury/internal/automation"
)

func newTestRule(id string) automation.Rule {
	now := time.Now().UTC().Truncate(time.Second)
	return automation.Rule{
		ID:      id,
		Name:    "test rule",
		Enabled: true,
		Trigger: automation.Trigger{Type: automation.TriggerEvent, EventType: "health.check_failed"},
		Conditions: []automation.Condition{
			{Field: "check_id", Equals: "chk_target"},
		},
		Actions: []automation.Action{
			{Type: automation.ActionNotify, Message: "something happened"},
		},
		CooldownSeconds: 30,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func TestAutomationRepositoryUpsertAndList(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewAutomationRepository(db)

	rule := newTestRule("rule_1")
	if err := repo.Upsert(ctx, rule); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	got := list[0]
	if got.ID != rule.ID || got.Name != rule.Name || got.CooldownSeconds != rule.CooldownSeconds {
		t.Errorf("got = %+v, want fields matching %+v", got, rule)
	}
	if got.Trigger != rule.Trigger {
		t.Errorf("Trigger = %+v, want %+v (JSON round-trip)", got.Trigger, rule.Trigger)
	}
	if len(got.Conditions) != 1 || got.Conditions[0] != rule.Conditions[0] {
		t.Errorf("Conditions = %+v, want %+v", got.Conditions, rule.Conditions)
	}
	if len(got.Actions) != 1 || got.Actions[0].Type != rule.Actions[0].Type || got.Actions[0].Message != rule.Actions[0].Message {
		t.Errorf("Actions = %+v, want %+v", got.Actions, rule.Actions)
	}
	if got.LastRunStatus != "" {
		t.Errorf("LastRunStatus = %q, want empty (never run)", got.LastRunStatus)
	}
}

func TestAutomationRepositoryComplexActionRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewAutomationRepository(db)

	rule := newTestRule("rule_1")
	rule.Actions = []automation.Action{
		{Type: automation.ActionRestartProcess, ProcessName: "worker", LaunchCommand: []string{"/usr/bin/worker", "--flag", "value"}},
		{Type: automation.ActionCreateEvent, Message: "restarted worker"},
	}
	if err := repo.Upsert(ctx, rule); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := list[0].Actions
	if len(got) != 2 {
		t.Fatalf("len(Actions) = %d, want 2", len(got))
	}
	if got[0].ProcessName != "worker" || len(got[0].LaunchCommand) != 3 || got[0].LaunchCommand[2] != "value" {
		t.Errorf("Actions[0] = %+v", got[0])
	}
}

func TestAutomationRepositoryUpsertPreservesRunHistoryOnConfigUpdate(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewAutomationRepository(db)

	rule := newTestRule("rule_1")
	if err := repo.Upsert(ctx, rule); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	ranAt := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdateRunResult(ctx, rule.ID, automation.RunResult{
		Status: automation.RunFailed, Message: "boom", RanAt: ranAt,
		Enabled: true, ConsecutiveFailures: 1,
	}); err != nil {
		t.Fatalf("UpdateRunResult: %v", err)
	}

	rule.Name = "renamed"
	if err := repo.Upsert(ctx, rule); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := list[0]
	if got.Name != "renamed" {
		t.Errorf("Name = %q, want renamed", got.Name)
	}
	if got.LastRunStatus != automation.RunFailed || got.RunCount != 1 {
		t.Errorf("run history was reset by a config-only Upsert: got LastRunStatus=%v RunCount=%d, want RunFailed/1 preserved", got.LastRunStatus, got.RunCount)
	}
}

func TestAutomationRepositoryUpdateRunResultIncrementsCount(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewAutomationRepository(db)

	rule := newTestRule("rule_1")
	if err := repo.Upsert(ctx, rule); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	t1 := time.Now().UTC().Truncate(time.Second)
	if err := repo.UpdateRunResult(ctx, rule.ID, automation.RunResult{
		Status: automation.RunSucceeded, Message: "ok", RanAt: t1, Enabled: true,
	}); err != nil {
		t.Fatalf("UpdateRunResult 1: %v", err)
	}
	t2 := t1.Add(time.Minute)
	if err := repo.UpdateRunResult(ctx, rule.ID, automation.RunResult{
		Status: automation.RunSucceeded, Message: "ok again", RanAt: t2, Enabled: true,
	}); err != nil {
		t.Fatalf("UpdateRunResult 2: %v", err)
	}

	list, _ := repo.List(ctx)
	got := list[0]
	if got.RunCount != 2 {
		t.Errorf("RunCount = %d, want 2", got.RunCount)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(t2) {
		t.Errorf("LastRunAt = %v, want %v", got.LastRunAt, t2)
	}
	if got.LastRunMessage != "ok again" {
		t.Errorf("LastRunMessage = %q, want %q", got.LastRunMessage, "ok again")
	}
}

func TestAutomationRepositoryUpdateRunResultNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewAutomationRepository(db)

	err := repo.UpdateRunResult(ctx, "nonexistent", automation.RunResult{
		Status: automation.RunSucceeded, Message: "ok", RanAt: time.Now(), Enabled: true,
	})
	if err != automation.ErrNotFound {
		t.Errorf("err = %v, want automation.ErrNotFound", err)
	}
}

func TestAutomationRepositoryDelete(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewAutomationRepository(db)

	rule := newTestRule("rule_1")
	if err := repo.Upsert(ctx, rule); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := repo.Delete(ctx, rule.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("len(list) = %d, want 0 after Delete", len(list))
	}
}

// TestAutomationRepositoryUpdateRunResultPersistsCircuitBreakerState
// verifies the two columns 0006_automation_circuit_breaker.sql added
// actually round-trip through UpdateRunResult and List - the specific
// gap a version of this repository built before automation.RunResult
// existed would have (silently dropping Enabled/ConsecutiveFailures on
// every persist, since the old signature had nowhere to carry them).
func TestAutomationRepositoryUpdateRunResultPersistsCircuitBreakerState(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	repo := NewAutomationRepository(db)

	rule := newTestRule("rule_1")
	if err := repo.Upsert(ctx, rule); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Simulate the exact sequence a real circuit-breaker trip produces:
	// four ordinary failures, then a fifth that Engine would have
	// flagged by setting Enabled to false - UpdateRunResult itself
	// doesn't decide this (Engine does, in trackedRule.recordOutcome),
	// it only needs to persist whatever it's told faithfully.
	for i := 1; i <= 4; i++ {
		if err := repo.UpdateRunResult(ctx, rule.ID, automation.RunResult{
			Status: automation.RunFailed, Message: "boom", RanAt: time.Now(),
			Enabled: true, ConsecutiveFailures: i,
		}); err != nil {
			t.Fatalf("UpdateRunResult %d: %v", i, err)
		}
	}
	if err := repo.UpdateRunResult(ctx, rule.ID, automation.RunResult{
		Status: automation.RunFailed, Message: "boom", RanAt: time.Now(),
		Enabled: false, ConsecutiveFailures: 5,
	}); err != nil {
		t.Fatalf("UpdateRunResult 5 (trip): %v", err)
	}

	list, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := list[0]
	if got.ConsecutiveFailures != 5 {
		t.Errorf("ConsecutiveFailures = %d, want 5", got.ConsecutiveFailures)
	}
	if got.Enabled {
		t.Error("Enabled = true, want false (circuit breaker trip should have persisted)")
	}
	if got.RunCount != 5 {
		t.Errorf("RunCount = %d, want 5", got.RunCount)
	}

	// A subsequent success must reset the persisted streak, mirroring
	// trackedRule.recordOutcome's in-memory reset.
	if err := repo.UpdateRunResult(ctx, rule.ID, automation.RunResult{
		Status: automation.RunSucceeded, Message: "recovered", RanAt: time.Now(),
		Enabled: true, ConsecutiveFailures: 0,
	}); err != nil {
		t.Fatalf("UpdateRunResult (recovery): %v", err)
	}
	list, err = repo.List(ctx)
	if err != nil {
		t.Fatalf("List after recovery: %v", err)
	}
	got = list[0]
	if got.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures after recovery = %d, want 0", got.ConsecutiveFailures)
	}
	if !got.Enabled {
		t.Error("Enabled after recovery = false, want true")
	}
}
