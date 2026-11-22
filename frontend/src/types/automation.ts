// TypeScript mirror of backend/internal/scheduler and
// internal/automation's JSON-serialized types. Field names match the Go
// struct tags exactly (snake_case) - kept in sync by hand, same
// convention as types/system.ts and types/observability.ts.

// ---- internal/scheduler ----

export type ScheduleType = "once" | "interval";

/** The shape scheduler.create/scheduler.update accept - see backend's schedulerTaskParams. */
export interface ScheduleInput {
  name: string;
  type: ScheduleType;
  /** RFC3339 with an explicit offset, e.g. "2026-08-25T17:00:00-07:00" - required for type "once". */
  run_at?: string;
  /** Required for type "interval". */
  interval_seconds?: number;
}

/** scheduler.Task. */
export interface ScheduledTask extends ScheduleInput {
  id: string;
  enabled: boolean;
  next_run_at: string;
  last_run_at?: string;
  created_at: string;
  updated_at: string;
}

// ---- internal/automation ----

export type TriggerType = "event" | "schedule";

export interface Trigger {
  type: TriggerType;
  /** Required when type is "event" - a bus event Type to match, e.g. "health.check_failed". */
  event_type?: string;
  /** Required when type is "schedule" - a ScheduledTask id. */
  schedule_id?: string;
}

export interface Condition {
  /** A top-level key in the triggering event's JSON payload - no nested paths. */
  field: string;
  equals: string;
}

export type ActionType =
  | "restart_process"
  | "stop_process"
  | "start_service"
  | "restart_service"
  | "run_command"
  | "create_event"
  | "notify";

export interface Action {
  type: ActionType;
  /** restart_process, stop_process. */
  process_name?: string;
  /** restart_process - argv, never a shell string. */
  launch_command?: string[];
  /** start_service, restart_service. */
  service_name?: string;
  /** run_command - argv, never a shell string. */
  command_args?: string[];
  /** create_event, notify. */
  message?: string;
}

export type RunStatus = "succeeded" | "failed" | "skipped_cooldown" | "skipped_conditions" | "";

/** The shape automation.create/automation.update accept - see backend's automationRuleParams. */
export interface AutomationRuleInput {
  name: string;
  enabled: boolean;
  trigger: Trigger;
  conditions?: Condition[];
  actions: Action[];
  cooldown_seconds?: number;
}

/** automation.Status - a rule's configuration plus its live-tracked last run. */
export interface AutomationRuleStatus extends AutomationRuleInput {
  id: string;
  created_at: string;
  updated_at: string;
  last_run_at?: string;
  last_run_status: RunStatus;
  last_run_message?: string;
  run_count: number;
  /**
   * Consecutive failed runs since the last success (or since creation).
   * The backend auto-disables a rule (enabled flips to false) once this
   * reaches MAX_CONSECUTIVE_FAILURES - see backend's
   * automation.MaxConsecutiveFailures doc comment for the full
   * reasoning. Surfaced here so the UI can show a rule approaching that
   * threshold, not just one that already tripped it.
   */
  consecutive_failures: number;
}

/** Mirrors backend automation.MaxConsecutiveFailures (validate.go) - kept
 *  in sync by hand, same convention as the rest of this file. Used only
 *  to show "N of MAX" / an approaching-threshold indicator; the backend
 *  is the sole authority on when a rule actually gets disabled. */
export const MAX_CONSECUTIVE_FAILURES = 5;
