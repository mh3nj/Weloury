// TypeScript mirror of backend/internal/containers's JSON-serialized
// types. Field names match the Go struct tags exactly (snake_case) -
// kept in sync by hand, same convention as types/system.ts.

export interface Container {
  id: string;
  name: string;
  image: string;
  state: string; // Docker's own vocabulary verbatim: "running", "exited", "paused", ...
  status: string; // Docker's human-readable status, e.g. "Up 3 seconds"
  ports?: string; // Docker's own display string, e.g. "0.0.0.0:18080->8080/tcp"
  created_at: string;

  cpu_percent?: number;
  memory_usage_bytes?: number;
  memory_limit_bytes?: number;
  pid_count?: number;
}

export interface ContainerAvailability {
  available: boolean;
  reason?: string;
}
