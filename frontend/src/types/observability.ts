// TypeScript mirror of backend/internal/health, internal/metrics,
// internal/logs, and internal/eventbus's JSON-serialized types. Field
// names match the Go struct tags exactly (snake_case) - kept in sync by
// hand, same convention as types/system.ts.

// ---- internal/health ----

export type HealthState = "UNKNOWN" | "HEALTHY" | "DEGRADED" | "UNHEALTHY";

export type HealthCheckType =
  | "process_exists"
  | "service_state"
  | "tcp_port"
  | "http_endpoint"
  | "command"
  | "disk_threshold"
  | "memory_threshold"
  | "cpu_threshold";

export interface RetryPolicy {
  max_retries: number;
  retry_delay_seconds: number;
}

/** The shape health.create/health.update accept - see backend's healthCheckParams. */
export interface HealthCheckInput {
  name: string;
  type: HealthCheckType;
  target?: string;
  command_args?: string[];
  threshold?: number;
  interval_seconds: number;
  timeout_seconds: number;
  retry: RetryPolicy;
}

/** health.Status - a check's configuration plus its live-tracked outcome. */
export interface HealthCheckStatus extends HealthCheckInput {
  id: string;
  created_at: string;
  updated_at: string;
  state: HealthState;
  message: string;
  last_success_at?: string;
  last_failure_at?: string;
  failure_count: number;
}

// ---- internal/metrics ----

export type MetricName = "cpu" | "memory" | "disk" | "network_rx" | "network_tx";

export interface MetricCurrent {
  metric: string;
  scope?: string;
  value: number;
  timestamp: string;
}

export interface MetricSample {
  metric: string;
  scope: string;
  value: number;
  timestamp: string;
}

export interface MetricPoint {
  timestamp: string;
  avg: number;
  min: number;
  max: number;
}

// ---- internal/logs ----

export type LogLevel = "DEBUG" | "INFO" | "WARN" | "ERROR";

export interface LogEntry {
  seq: number;
  timestamp: string;
  level: LogLevel;
  message: string;
  attrs?: Record<string, string>;
}

// ---- internal/eventbus ----

export type EventSeverity = "info" | "warning" | "critical";

export interface WelouryEvent {
  id: string;
  timestamp: string;
  type: string;
  source: string;
  severity: EventSeverity;
  payload?: unknown;
}
