type Status = "healthy" | "degraded" | "unhealthy" | "unknown";

const LABEL: Record<Status, string> = {
  healthy: "Healthy",
  degraded: "Degraded",
  unhealthy: "Unhealthy",
  unknown: "Unknown",
};

// Tailwind's JIT scanner needs full class names present as literal
// strings somewhere in source; a template-interpolated class name like
// `bg-status-${status}` would silently fail to generate CSS. This lookup
// map is what makes the dynamic status -> color mapping actually work.
const DOT_CLASS: Record<Status, string> = {
  healthy: "bg-status-healthy",
  degraded: "bg-status-degraded",
  unhealthy: "bg-status-unhealthy",
  unknown: "bg-status-unknown",
};

export function StatusDot({ status, label }: { status: Status; label?: string }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-text-secondary">
      <span className={`h-1.5 w-1.5 rounded-full ${DOT_CLASS[status]}`} aria-hidden="true" />
      {label ?? LABEL[status]}
    </span>
  );
}
