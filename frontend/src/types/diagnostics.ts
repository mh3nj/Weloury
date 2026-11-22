// TypeScript mirror of backend/internal/diagnostics's JSON-serialized
// types. Field names match the Go struct tags exactly (snake_case) -
// kept in sync by hand, same convention as types/system.ts.

import type { CPUInfo, DiskInfo, MemoryInfo, NetworkConnection, NetworkInterface, ServiceInfo, SystemInfo } from "./system";
import type { WelouryEvent, HealthCheckStatus } from "./observability";

export interface SystemSection {
  info?: SystemInfo;
  cpu?: CPUInfo;
  memory?: MemoryInfo;
  error?: string;
}

export interface StorageSection {
  disks?: DiskInfo[];
  error?: string;
}

export interface NetworkSection {
  interfaces?: NetworkInterface[];
  listening_ports?: NetworkConnection[];
  error?: string;
}

export interface ServicesSection {
  not_running?: ServiceInfo[];
  error?: string;
}

export interface HealthSection {
  failing?: HealthCheckStatus[];
}

export interface DiagnosticsReport {
  generated_at: string;
  system: SystemSection;
  storage: StorageSection;
  network: NetworkSection;
  services: ServicesSection;
  health: HealthSection;
  recent_issues: WelouryEvent[];
}
