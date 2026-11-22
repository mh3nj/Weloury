package scheduler

import "weloury/internal/eventbus"

// TypeTaskDue fires once each time a Task becomes due - see Engine's
// doc comment for exactly when. Defined here per eventbus.Type's doc
// comment convention (domain packages define their own event types
// alongside their own code) - mirrors internal/health/events.go and
// internal/logs's TypeEntry.
const TypeTaskDue eventbus.Type = "scheduler.task_due"

// taskDuePayload is TypeTaskDue's JSON payload.
type taskDuePayload struct {
	TaskID string `json:"task_id"`
	Name   string `json:"name"`
}
