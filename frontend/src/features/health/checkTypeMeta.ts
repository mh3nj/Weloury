import type { HealthCheckType } from "@/types/observability";

export interface CheckTypeMeta {
  label: string;
  needsTarget: boolean;
  needsCommand: boolean;
  needsThreshold: boolean;
  targetLabel?: string;
  targetPlaceholder?: string;
}

/**
 * One entry per health.CheckType (backend/internal/health/types.go) -
 * drives which fields HealthCheckFormDialog shows and how
 * HealthCheckDetailPanel labels a check's target. Kept as its own module
 * (rather than inlined in the dialog) since both the create/edit form and
 * the detail panel need the same "what does this type mean" metadata.
 */
export const CHECK_TYPE_META: Record<HealthCheckType, CheckTypeMeta> = {
  process_exists: {
    label: "Process exists",
    needsTarget: true,
    needsCommand: false,
    needsThreshold: false,
    targetLabel: "Process name",
    targetPlaceholder: "e.g. my-worker",
  },
  service_state: {
    label: "Service running",
    needsTarget: true,
    needsCommand: false,
    needsThreshold: false,
    targetLabel: "Service name",
    targetPlaceholder: "e.g. nginx",
  },
  tcp_port: {
    label: "TCP port open",
    needsTarget: true,
    needsCommand: false,
    needsThreshold: false,
    targetLabel: "Host:port",
    targetPlaceholder: "127.0.0.1:5432",
  },
  http_endpoint: {
    label: "HTTP endpoint",
    needsTarget: true,
    needsCommand: false,
    needsThreshold: false,
    targetLabel: "URL",
    targetPlaceholder: "http://localhost:8080/health",
  },
  command: {
    label: "Command exits 0",
    needsTarget: false,
    needsCommand: true,
    needsThreshold: false,
  },
  disk_threshold: {
    label: "Disk usage threshold",
    needsTarget: true,
    needsCommand: false,
    needsThreshold: true,
    targetLabel: "Mount point",
    targetPlaceholder: "/",
  },
  memory_threshold: {
    label: "Memory usage threshold",
    needsTarget: false,
    needsCommand: false,
    needsThreshold: true,
  },
  cpu_threshold: {
    label: "CPU usage threshold",
    needsTarget: false,
    needsCommand: false,
    needsThreshold: true,
  },
};

export const CHECK_TYPE_ORDER: HealthCheckType[] = [
  "process_exists",
  "service_state",
  "tcp_port",
  "http_endpoint",
  "command",
  "disk_threshold",
  "memory_threshold",
  "cpu_threshold",
];
