import type { ActionType } from "@/types/automation";

export interface ActionTypeMeta {
  label: string;
  needsProcessName: boolean;
  needsLaunchCommand: boolean;
  needsServiceName: boolean;
  needsCommandArgs: boolean;
  needsMessage: boolean;
}

/**
 * One entry per automation.ActionType (backend/internal/automation/types.go)
 * - drives which fields the rule form shows for each action row. Mirrors
 * features/health/checkTypeMeta.ts's role for health check types.
 */
export const ACTION_TYPE_META: Record<ActionType, ActionTypeMeta> = {
  restart_process: {
    label: "Restart process",
    needsProcessName: true,
    needsLaunchCommand: true,
    needsServiceName: false,
    needsCommandArgs: false,
    needsMessage: false,
  },
  stop_process: {
    label: "Stop process",
    needsProcessName: true,
    needsLaunchCommand: false,
    needsServiceName: false,
    needsCommandArgs: false,
    needsMessage: false,
  },
  start_service: {
    label: "Start service",
    needsProcessName: false,
    needsLaunchCommand: false,
    needsServiceName: true,
    needsCommandArgs: false,
    needsMessage: false,
  },
  restart_service: {
    label: "Restart service",
    needsProcessName: false,
    needsLaunchCommand: false,
    needsServiceName: true,
    needsCommandArgs: false,
    needsMessage: false,
  },
  run_command: {
    label: "Run command",
    needsProcessName: false,
    needsLaunchCommand: false,
    needsServiceName: false,
    needsCommandArgs: true,
    needsMessage: false,
  },
  create_event: {
    label: "Create event",
    needsProcessName: false,
    needsLaunchCommand: false,
    needsServiceName: false,
    needsCommandArgs: false,
    needsMessage: true,
  },
  notify: {
    label: "Notify",
    needsProcessName: false,
    needsLaunchCommand: false,
    needsServiceName: false,
    needsCommandArgs: false,
    needsMessage: true,
  },
};

export const ACTION_TYPE_ORDER: ActionType[] = [
  "restart_process",
  "stop_process",
  "start_service",
  "restart_service",
  "run_command",
  "create_event",
  "notify",
];

/** A few common bus event types to suggest for an event trigger - not exhaustive, just a starting point; any string is accepted. */
export const COMMON_EVENT_TYPES = [
  "health.check_failed",
  "health.check_passed",
  "health.disk_threshold_exceeded",
  "health.memory_threshold_exceeded",
];
