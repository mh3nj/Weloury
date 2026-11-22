// TypeScript mirror of backend/internal/system's JSON-serialized types.
// Field names match the Go struct tags exactly (snake_case) - kept in
// sync by hand, same convention as types/fwp.ts.

export interface SystemInfo {
  hostname: string;
  os: string;
  os_version: string;
  kernel_version: string;
  architecture: string;
  uptime_seconds: number;
  boot_time: string; // RFC 3339, as encoded by Go's encoding/json for time.Time
}

export interface CPUInfo {
  model_name: string;
  physical_cores: number;
  logical_cores: number;
  clock_mhz: number;
}

export interface CPUUsage {
  overall_percent: number;
  per_core_percent: number[];
}

export interface MemoryInfo {
  total_bytes: number;
  used_bytes: number;
  available_bytes: number;
  swap_total_bytes: number;
  swap_used_bytes: number;
}

export interface DiskInfo {
  device: string;
  mount_point: string;
  filesystem: string;
  total_bytes: number;
  free_bytes: number;
  used_bytes: number;
  volume_name?: string;
  drive_type?: string;
  bus_type?: string;
  is_system_drive?: boolean;
}

/** Mirror of backend system.DiskHealth - physical-drive SMART-class health. */
export interface DiskHealth {
  device: string;
  model?: string;
  media_type?: string;
  bus_type?: string;
  status: "healthy" | "warning" | "failing" | "unknown";
  health_percent?: number;
  estimated?: boolean;
  /** Where health_percent came from: "smart" (direct), "wmi", or absent. */
  health_source?: "smart" | "wmi";
  temperature_celsius?: number;
  power_on_hours?: number;
  power_cycles?: number;
  total_bytes?: number;
}

/** Mirror of backend system.StartupEntry - one OS startup-location item. */
export interface StartupEntry {
  id: string;
  name: string;
  command?: string;
  location: string;
  scope: "user" | "system";
  enabled: boolean;
}

export interface GPUInfo {
  name: string;
  vendor?: string;
  driver_version?: string;
  total_memory_bytes?: number;
  pci_id?: string;
  index?: number;
}

export interface GPUUsage {
  /** null/absent = adapter present but no telemetry source ("not reported"). */
  utilization_percent?: number | null;
  memory_used_bytes?: number;
  memory_total_bytes?: number;
  temperature_celsius?: number;
  fan_percent?: number;
  power_watts?: number;
  power_limit_watts?: number;
  clock_mhz?: number;
}

export interface NetworkInterface {
  name: string;
  hardware_addr: string;
  addresses: string[];
  is_up: boolean;
  is_loopback: boolean;
}

export interface NetworkConnection {
  protocol: string; // "tcp" | "tcp6" | "udp" | "udp6"
  local_address: string;
  local_port: number;
  remote_address?: string;
  remote_port?: number;
  state?: string; // e.g. "LISTEN", "ESTABLISHED"; empty for UDP
  pid?: number;
  process_name?: string;
}

export interface ProcessInfo {
  pid: number;
  ppid: number;
  name: string;
  user: string;
  status: string;
  started_at: string;
  memory_bytes: number;
  threads: number;
  /** Whole-machine CPU share (Task Manager convention); null = not measured. */
  cpu_percent?: number | null;
  /** Current disk I/O rate in bytes/sec; null/undefined = not measured. */
  disk_io_bytes_per_sec?: number | null;
}

export interface ProcessDetail extends ProcessInfo {
  executable: string;
  command_line: string[] | null;
  /** Detail-level CPU% (shadows the list-level optional field on the wire). */
  cpu_percent: number;
  owner_name?: string;
}

export interface ProcessTreeNode extends ProcessInfo {
  children?: ProcessTreeNode[];
}

export interface ServiceInfo {
  name: string;
  display_name: string;
  description: string;
  state: string;
  startup_mode: string;
  pid: number;
}

export interface ServiceDetail extends ServiceInfo {
  dependencies: string[] | null;
}
