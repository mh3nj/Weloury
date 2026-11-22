package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"weloury/internal/eventbus"
	"weloury/internal/scheduler"
)

// DefaultMaxConcurrentExecutions bounds how many rules may be executing
// their actions at once, regardless of how many events arrive at once -
// the automation equivalent of internal/health's
// DefaultMaxConcurrentChecks, same reasoning: a burst of triggering
// events (e.g. many health checks failing together during a real
// incident) must not be able to spawn unbounded goroutines.
const DefaultMaxConcurrentExecutions = 8

// defaultActionTimeout bounds how long a single Action is given to
// complete before it's treated as failed and the next action in the
// rule runs anyway - see Engine.executeActions for why a failed action
// does not abort the remaining ones. Engine.actionTimeout is seeded
// from this in New and is what executeActions actually reads - kept
// injectable (the same pattern as clock) specifically so a test can
// prove a hanging command really gets killed without waiting out a
// real 30 seconds to observe it - see
// TestEngineActionTimeoutKillsAHangingCommand.
const defaultActionTimeout = 30 * time.Second

// Engine subscribes to every event on the bus, matches each one against
// every enabled Rule's Trigger, evaluates Conditions, and executes
// Actions for whichever rules match - see the package doc comment for
// the safety properties this maintains.
type Engine struct {
	sys           sysCapabilities
	launcher      processLauncher
	bus           *eventbus.Bus
	repo          Repository
	logger        *slog.Logger
	clock         clock
	actionTimeout time.Duration

	sem chan struct{}
	wg  sync.WaitGroup

	mu    sync.Mutex
	rules map[string]*trackedRule
}

// New constructs an Engine. sys, bus, and repo must be non-nil; logger
// may be nil.
func New(sys sysCapabilities, bus *eventbus.Bus, repo Repository, logger *slog.Logger) *Engine {
	return &Engine{
		sys:           sys,
		launcher:      execLauncher{},
		bus:           bus,
		repo:          repo,
		logger:        logger,
		clock:         realClock{},
		actionTimeout: defaultActionTimeout,
		sem:           make(chan struct{}, DefaultMaxConcurrentExecutions),
		rules:         make(map[string]*trackedRule),
	}
}

// Start loads every persisted rule and subscribes to the bus, both
// synchronously, before returning - deliberately not from inside the
// consuming goroutine. Subscribing there would reopen exactly the race
// Phase 4 found and fixed in internal/app's event-persistence bridge
// (see persistence.go's doc comment there): if this Engine's own
// subscription were established late, an event published between
// Engine.Start returning and the subscription actually registering
// could silently never reach any rule. Start does not block on the
// consuming goroutine itself - call Wait after canceling ctx.
func (e *Engine) Start(ctx context.Context) error {
	statuses, err := e.repo.List(ctx)
	if err != nil {
		return fmt.Errorf("load automation rules: %w", err)
	}

	e.mu.Lock()
	for _, st := range statuses {
		e.rules[st.ID] = newTrackedRule(st)
	}
	e.mu.Unlock()

	ch, unsubscribe := e.bus.Subscribe(context.Background(), nil, eventbus.DefaultSubscriberBuffer)

	e.wg.Add(1)
	go e.consumeEvents(ctx, ch, unsubscribe)
	return nil
}

// Wait blocks until the event-consuming goroutine and every in-flight
// action execution it spawned have exited.
func (e *Engine) Wait() { e.wg.Wait() }

func (e *Engine) consumeEvents(ctx context.Context, ch <-chan eventbus.Event, unsubscribe func()) {
	defer e.wg.Done()
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, open := <-ch:
			if !open {
				return
			}
			e.handleEvent(ctx, evt)
		}
	}
}

func (e *Engine) handleEvent(ctx context.Context, evt eventbus.Event) {
	// Defense-in-depth mirror of Trigger.validate's identical check
	// (validate.go): every persisted rule's EventType is already
	// guaranteed not to start with this prefix, so no TriggerEvent match
	// below could ever fire from one of this package's own events
	// regardless. This additional check costs one string comparison and
	// means a future bypass of validation (a direct database edit, a
	// migration that doesn't revalidate) can't reintroduce a
	// self-triggering loop - see the package doc comment.
	if strings.HasPrefix(string(evt.Type), automationEventPrefix) {
		return
	}

	var payload map[string]any
	if len(evt.Payload) > 0 {
		_ = json.Unmarshal(evt.Payload, &payload) // best-effort; a Condition simply won't find its field if this fails
	}

	e.mu.Lock()
	var matched []*trackedRule
	for _, tr := range e.rules {
		if ruleMatchesEvent(tr.snapshotRule(), evt) {
			matched = append(matched, tr)
		}
	}
	e.mu.Unlock()

	for _, tr := range matched {
		e.tryRun(ctx, tr, payload)
	}
}

func ruleMatchesEvent(rule Rule, evt eventbus.Event) bool {
	if !rule.Enabled {
		return false
	}
	switch rule.Trigger.Type {
	case TriggerEvent:
		return eventbus.Type(rule.Trigger.EventType) == evt.Type
	case TriggerSchedule:
		if evt.Type != scheduler.TypeTaskDue {
			return false
		}
		var due struct {
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(evt.Payload, &due); err != nil {
			return false
		}
		return due.TaskID == rule.Trigger.ScheduleID
	default:
		return false
	}
}

// tryRun evaluates conditions, then checks cooldown, and - if both
// pass - executes the rule's actions in a bounded-concurrency
// goroutine. Every outcome (skip or execution) is recorded and
// published; see trackedRule.recordOutcome and Engine's doc comment on
// TypeRuleExecuted.
//
// Conditions before cooldown, deliberately - not the reverse, and not
// arbitrary: handleEvent's caller-side matched list is filtered only by
// Trigger.Type (see ruleMatchesEvent), not by Conditions, so every rule
// sharing an EventType with the incoming event reaches tryRun
// regardless of whether its Conditions would ever match this
// particular event's payload. A rule already cooling down from its own
// prior, unrelated execution would, checked in the other order, report
// "skipped_cooldown" for an event its Conditions never matched in the
// first place - true in the narrow sense that it IS cooling down, but
// misleading about *why this event* didn't fire it, and noisy: found
// by crash-testing several check-specific rules sharing one
// health.check_failed trigger, where every rule's TypeRuleExecuted
// entry for every *other* rule's matching check ended up reported as a
// cooldown skip instead of the conditions mismatch it actually was.
// Checking conditions first makes "skipped_conditions" mean what it
// says for every event that genuinely wasn't this rule's concern, and
// confines "skipped_cooldown" to events that would otherwise have
// fired the rule again too soon - the only case cooldown exists to
// describe. This changes nothing about whether a rule ever executes
// (checkCooldown and evaluateConditions are both pure reads with no
// side effects, and recordOutcome doesn't touch lastRunAt/runCount for
// either skip status) - only which accurate reason gets reported when
// both would independently apply.
func (e *Engine) tryRun(ctx context.Context, tr *trackedRule, payload map[string]any) {
	// Single fetch of both rule config and cooldown status - checkCooldown
	// already reads tr.rule and tr.lastRunAt together under one lock
	// cycle; a separate snapshotRule() call for the conditions check
	// below would cost a second lock cycle for no benefit and open a
	// narrow window for tr.rule to change between the two reads that
	// the original single-fetch code never had.
	rule, coolingDown, cooldownReason := tr.checkCooldown(e.clock.Now())

	if ok, reason := evaluateConditions(rule.Conditions, payload); !ok {
		tr.recordOutcome(RunSkippedConditions, reason, e.clock.Now())
		e.publishExecuted(rule, RunSkippedConditions, reason)
		return
	}

	if coolingDown {
		tr.recordOutcome(RunSkippedCooldown, cooldownReason, e.clock.Now())
		e.publishExecuted(rule, RunSkippedCooldown, cooldownReason)
		return
	}

	select {
	case e.sem <- struct{}{}:
	case <-ctx.Done():
		return
	}

	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		defer func() { <-e.sem }()

		status, message := e.executeActions(ctx, rule)
		now := e.clock.Now()
		updatedRule, consecutiveFailures, autoDisabled := tr.recordOutcome(status, message, now)

		if e.repo != nil {
			persistCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			result := RunResult{
				Status:              status,
				Message:             message,
				RanAt:               now,
				Enabled:             updatedRule.Enabled,
				ConsecutiveFailures: consecutiveFailures,
			}
			if err := e.repo.UpdateRunResult(persistCtx, rule.ID, result); err != nil && e.logger != nil {
				e.logger.Warn("failed to persist automation rule run result", "rule_id", rule.ID, "error", err)
			}
			cancel()
		}

		e.publishExecuted(rule, status, message)
		if autoDisabled {
			if e.logger != nil {
				e.logger.Warn("automation rule auto-disabled after repeated failures",
					"rule_id", updatedRule.ID, "name", updatedRule.Name, "consecutive_failures", consecutiveFailures)
			}
			e.publishAutoDisabled(updatedRule, consecutiveFailures)
		}
	}()
}

// executeActions runs every action in order, sequentially - order
// matters (a rule author writes actions in the sequence they're meant to
// happen, e.g. "stop" before "start"), and running them concurrently
// would also multiply this one rule's contribution to
// DefaultMaxConcurrentExecutions in a way that undermines what that bound
// is for. A failed action does not abort the remaining ones: the
// clearest case for continuing is a rule like
// [restart_process, create_event] where the create_event action exists
// specifically to report what happened, including a failure - aborting
// before it runs would silence the one signal a failure most needs.
// Overall RunStatus is RunFailed if any action failed, RunSucceeded only
// if every action succeeded.
func (e *Engine) executeActions(ctx context.Context, rule Rule) (RunStatus, string) {
	messages := make([]string, 0, len(rule.Actions))
	anyFailed := false

	for i, a := range rule.Actions {
		actionCtx, cancel := context.WithTimeout(ctx, e.actionTimeout)
		result := executeAction(actionCtx, e.sys, e.launcher, e.bus, rule, a)
		cancel()

		messages = append(messages, fmt.Sprintf("%d: %s: %s", i+1, a.Type, result.message))
		if !result.ok {
			anyFailed = true
		}
	}

	status := RunSucceeded
	if anyFailed {
		status = RunFailed
	}
	return status, strings.Join(messages, " | ")
}

func (e *Engine) publishExecuted(rule Rule, status RunStatus, message string) {
	if e.bus == nil {
		return
	}
	payload, err := json.Marshal(ruleExecutedPayload{RuleID: rule.ID, Name: rule.Name, Status: status, Message: message})
	if err != nil {
		return
	}
	severity := eventbus.SeverityInfo
	if status == RunFailed {
		severity = eventbus.SeverityWarning
	}
	e.bus.Publish(eventbus.Event{Type: TypeRuleExecuted, Source: "automation.engine", Severity: severity, Payload: payload})
}

// publishAutoDisabled fires TypeRuleAutoDisabled - see that constant's
// doc comment for why this is a separate event from TypeRuleExecuted
// rather than folded into it.
func (e *Engine) publishAutoDisabled(rule Rule, consecutiveFailures int) {
	if e.bus == nil {
		return
	}
	payload, err := json.Marshal(ruleAutoDisabledPayload{RuleID: rule.ID, Name: rule.Name, ConsecutiveFailures: consecutiveFailures})
	if err != nil {
		return
	}
	e.bus.Publish(eventbus.Event{Type: TypeRuleAutoDisabled, Source: "automation.engine", Severity: eventbus.SeverityWarning, Payload: payload})
}

// List returns every rule's live status, sorted by name.
func (e *Engine) List() []Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Status, 0, len(e.rules))
	for _, tr := range e.rules {
		out = append(out, tr.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one rule's live status by ID.
func (e *Engine) Get(id string) (Status, bool) {
	e.mu.Lock()
	tr, ok := e.rules[id]
	e.mu.Unlock()
	if !ok {
		return Status{}, false
	}
	return tr.snapshot(), true
}

// Create validates, persists, and begins evaluating a new rule against
// every subsequent event.
func (e *Engine) Create(ctx context.Context, r Rule) (Status, error) {
	if err := r.applyDefaultsAndValidate(); err != nil {
		return Status{}, err
	}
	now := e.clock.Now()
	r.ID = newRuleID()
	r.CreatedAt, r.UpdatedAt = now, now
	// A rule is active immediately upon creation by default - mirrors
	// scheduler.Engine.Create's identical choice, and matches "New
	// health check" for the same reason: a UI that just finished
	// configuring something the user explicitly asked to create should
	// not require a second, separate step to make it take effect.
	// Toggling it off afterward is what Update (or a future dedicated
	// enable/disable action, if that turns out to be worth its own
	// method - see scheduler.Engine.SetEnabled for the shape it would
	// take) is for.
	r.Enabled = true

	if err := e.repo.Upsert(ctx, r); err != nil {
		return Status{}, fmt.Errorf("persist rule: %w", err)
	}

	tr := newTrackedRule(Status{Rule: r})
	e.mu.Lock()
	e.rules[r.ID] = tr
	e.mu.Unlock()
	return tr.snapshot(), nil
}

// Update validates and persists a new configuration for an existing
// rule, preserving its accumulated LastRunAt/LastRunStatus/
// LastRunMessage/RunCount.
func (e *Engine) Update(ctx context.Context, id string, r Rule) (Status, error) {
	if err := r.applyDefaultsAndValidate(); err != nil {
		return Status{}, err
	}

	e.mu.Lock()
	existing, ok := e.rules[id]
	e.mu.Unlock()
	if !ok {
		return Status{}, ErrNotFound
	}

	prior := existing.snapshot()
	r.ID = id
	r.CreatedAt = prior.CreatedAt
	r.UpdatedAt = e.clock.Now()

	if err := e.repo.Upsert(ctx, r); err != nil {
		return Status{}, fmt.Errorf("persist rule: %w", err)
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	tr, ok := e.rules[id]
	if !ok {
		return Status{}, ErrNotFound // raced a concurrent Delete
	}
	tr.setRule(r)
	return tr.snapshot(), nil
}

// Delete stops evaluating and removes a rule.
func (e *Engine) Delete(ctx context.Context, id string) error {
	e.mu.Lock()
	_, ok := e.rules[id]
	if ok {
		delete(e.rules, id)
	}
	e.mu.Unlock()
	if !ok {
		return ErrNotFound
	}
	return e.repo.Delete(ctx, id)
}

// trackedRule is Engine's in-memory record for one rule: its current
// configuration and live run-tracking state, each protected by its own
// mutex - the same two-level-locking split (Engine.mu for the set of
// rules, trackedRule.mu for one rule's own mutable state) internal/health
// uses for identical reasons.
type trackedRule struct {
	mu                  sync.Mutex
	rule                Rule
	lastRunAt           *time.Time
	lastRunStatus       RunStatus
	lastRunMessage      string
	runCount            int
	consecutiveFailures int
}

func newTrackedRule(st Status) *trackedRule {
	return &trackedRule{
		rule:                st.Rule,
		lastRunAt:           st.LastRunAt,
		lastRunStatus:       st.LastRunStatus,
		lastRunMessage:      st.LastRunMessage,
		runCount:            st.RunCount,
		consecutiveFailures: st.ConsecutiveFailures,
	}
}

func (tr *trackedRule) snapshot() Status {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return Status{
		Rule:                tr.rule,
		LastRunAt:           tr.lastRunAt,
		LastRunStatus:       tr.lastRunStatus,
		LastRunMessage:      tr.lastRunMessage,
		RunCount:            tr.runCount,
		ConsecutiveFailures: tr.consecutiveFailures,
	}
}

func (tr *trackedRule) snapshotRule() Rule {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.rule
}

// setRule installs a new Rule configuration from Update and resets
// ConsecutiveFailures to 0 - deliberately, unlike LastRunAt/
// LastRunStatus/LastRunMessage/RunCount, which a config edit never
// touches (see Repository's doc comment: those are a rule's persisted
// history, informational only). ConsecutiveFailures is different: it's
// the circuit breaker's live armed state (see MaxConsecutiveFailures),
// and the whole reason to edit a rule that has tripped it - or is close
// to - is almost always to fix and re-enable it. Leaving a near-tripped
// streak in place across that edit would silently re-trip the breaker
// on the very next hiccup instead of giving the rule the fresh
// MaxConsecutiveFailures-strike grace period an operator turning it
// back on would reasonably expect.
func (tr *trackedRule) setRule(r Rule) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.rule = r
	tr.consecutiveFailures = 0
}

// checkCooldown reports whether rule.CooldownSeconds has elapsed since
// the last *actual execution* (see recordOutcome - a skip never updates
// lastRunAt, so a run of consecutive skips can never perpetually extend
// a cooldown window or prevent a rule from ever executing again).
//
// Always enforces at least minRuleCooldownSeconds, even when
// rule.CooldownSeconds is 0 - see that constant's doc comment for why:
// this is Phase 8 hardening for a gap CooldownSeconds's own "0 means no
// cooldown at all" design otherwise leaves open.
func (tr *trackedRule) checkCooldown(now time.Time) (rule Rule, coolingDown bool, reason string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	cooldownSeconds := tr.rule.CooldownSeconds
	if cooldownSeconds < minRuleCooldownSeconds {
		cooldownSeconds = minRuleCooldownSeconds
	}
	if tr.lastRunAt != nil {
		cooldown := time.Duration(cooldownSeconds) * time.Second
		if elapsed := now.Sub(*tr.lastRunAt); elapsed < cooldown {
			return tr.rule, true, fmt.Sprintf("cooldown active: %s remaining", (cooldown - elapsed).Round(time.Second))
		}
	}
	return tr.rule, false, ""
}

// recordOutcome updates live status for every outcome (skip or
// execution), but only advances lastRunAt/runCount for an actual
// execution (RunSucceeded/RunFailed) - see checkCooldown's doc comment
// on why a skip must not, and Repository.UpdateRunResult's doc comment,
// which persistence must mirror exactly.
//
// It also owns the circuit breaker (see MaxConsecutiveFailures):
// RunSucceeded resets consecutiveFailures to 0; RunFailed increments it
// and, on reaching the threshold, flips tr.rule.Enabled to false. A
// skip touches neither, matching checkCooldown's identical reasoning -
// a skipped attempt is not evidence the action itself is failing. The
// returned Rule, consecutiveFailures, and autoDisabled let the caller
// (tryRun's execution goroutine) persist the exact post-outcome state
// and decide whether to publish TypeRuleAutoDisabled, both from one
// lock-protected read rather than a second snapshot call racing a
// concurrent Update/Delete.
func (tr *trackedRule) recordOutcome(status RunStatus, message string, now time.Time) (rule Rule, consecutiveFailures int, autoDisabled bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.lastRunStatus = status
	tr.lastRunMessage = message
	if status == RunSucceeded || status == RunFailed {
		tr.lastRunAt = &now
		tr.runCount++
	}
	switch status {
	case RunSucceeded:
		tr.consecutiveFailures = 0
	case RunFailed:
		tr.consecutiveFailures++
		if tr.consecutiveFailures >= MaxConsecutiveFailures && tr.rule.Enabled {
			tr.rule.Enabled = false
			autoDisabled = true
		}
	}
	return tr.rule, tr.consecutiveFailures, autoDisabled
}
