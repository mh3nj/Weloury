/**
 * A horizontal usage bar with semantic coloring at high utilization.
 * Deliberately not a generic "progress bar" component: Weloury only ever
 * uses this to show "how full is this resource", so the color thresholds
 * are baked in rather than left to every call site to reinvent slightly
 * differently.
 */
export function UsageBar({ percent }: { percent: number }) {
  const clamped = Math.max(0, Math.min(100, percent));
  const colorClass =
    clamped >= 90 ? "bg-status-unhealthy" : clamped >= 75 ? "bg-status-degraded" : "bg-accent";

  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2">
      <div className={`h-full rounded-full ${colorClass}`} style={{ width: `${clamped}%` }} />
    </div>
  );
}
