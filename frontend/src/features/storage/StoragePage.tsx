import { useCallback, useEffect, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { UsageBar } from "@/components/ui/UsageBar";
import { StatusDot } from "@/components/ui/StatusDot";
import { formatBytes, formatCelsius } from "@/lib/format";
import type { DiskHealth, DiskInfo } from "@/types/system";

const DRIVE_TYPE_LABEL: Record<string, string> = {
  internal: "Internal",
  removable: "Removable",
  network: "Network drive",
  optical: "Optical",
  ramdisk: "RAM disk",
  unknown: "Unknown type",
};

const HEALTH_STATUS_MAP: Record<string, "healthy" | "degraded" | "unhealthy" | "unknown"> = {
  healthy: "healthy",
  warning: "degraded",
  failing: "unhealthy",
  unknown: "unknown",
};

function HealthBar({ percent }: { percent: number }) {
  // Health is the inverse of usage: a high number is good, so the color
  // thresholds read top-down rather than the way UsageBar does.
  const clamped = Math.max(0, Math.min(100, percent));
  const colorClass =
    clamped >= 80 ? "bg-status-healthy" : clamped >= 50 ? "bg-status-degraded" : "bg-status-unhealthy";
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-2">
      <div className={`h-full rounded-full ${colorClass}`} style={{ width: `${clamped}%` }} />
    </div>
  );
}

/**
 * Storage: mounted volumes (system.disks) joined with physical-drive SMART
 * health (disk.health), the same split Task Manager draws between volumes
 * and drives.
 *
 * Health is read straight from the drive whenever smartctl is available
 * (it ships inside the app on Windows, and is expected on PATH on Linux and
 * macOS): that gives the vendor's own wear figure, temperature, and power-on
 * hours as a real measurement. When only the administrator-gated Windows
 * storage counters answer, health is a derived estimate and is labeled as
 * one. A drive whose SMART data genuinely can't be read shows "not reported"
 * rather than a fabricated number.
 */
export function StoragePage({ client }: { client: FwpClient }) {
  const [disks, setDisks] = useState<DiskInfo[] | null>(null);
  const [healths, setHealths] = useState<DiskHealth[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [healthError, setHealthError] = useState<string | null>(null);

  const load = useCallback(() => {
    setError(null);
    setHealthError(null);
    const controller = new AbortController();
    client
      .request<DiskInfo[]>("system.disks", undefined, controller.signal)
      .then((v) => setDisks(v ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    // Health is optional per platform; its failure degrades the cards to
    // "not reported", it does not blank the whole page.
    client
      .request<DiskHealth[]>("disk.health", undefined, controller.signal)
      .then((v) => setHealths(v ?? []))
      .catch((err) => {
        setHealths([]);
        setHealthError(err instanceof Error ? err.message : String(err));
      });
    return () => controller.abort();
  }, [client]);

  useEffect(() => load(), [client, load]);

  // Match a health row to a volume. Windows names volumes "C:\" and drives
  // "\\.\PHYSICALDRIVE0", so the two rarely join directly: each drive card
  // stands on its own, and this only annotates a volume when its device
  // string is contained in a health device (Linux /dev/sda covers /dev/sda1,
  // macOS disk0 covers disk0s1).
  function healthFor(disk: DiskInfo): DiskHealth | null {
    if (!healths) return null;
    const dev = disk.device.toLowerCase();
    for (const h of healths) {
      const hd = h.device.toLowerCase();
      const base = (s: string) => s.replace(/[^a-z0-9]/g, "");
      if (hd === dev || base(hd) === base(dev)) return h;
      if (dev.startsWith(hd) && hd.length > 4) return h;
    }
    return null;
  }

  const unclaimed = (healths ?? []).filter(
    (h) => !disks?.some((d) => healthFor(d)?.device === h.device),
  );

  // True when no drive anywhere reported a SMART percentage and the health
  // call errored: the machine likely has no reachable SMART source, so the
  // page explains how to turn real health on instead of leaving blank cells.
  const smartAbsent =
    healthError !== null ||
    (healths !== null &&
      healths.length > 0 &&
      healths.every((h) => !h.health_percent && h.status === "unknown"));

  return (
    <div className="flex flex-col gap-4">
      <Panel>
        <PanelHeader>
          <h2 className="text-sm font-semibold">Storage</h2>
          {disks && <span className="text-xs text-text-muted">{disks.length} volume(s) · {healths?.length ?? 0} drive(s)</span>}
          <button onClick={load} className="ml-auto text-xs text-text-muted hover:text-text-primary">
            Refresh
          </button>
        </PanelHeader>
        {error && <p className="p-4 text-sm text-text-secondary">Couldn't load disk usage: {error}</p>}
        {!error && !disks && <p className="p-4 text-sm text-text-muted">Loading...</p>}
        {disks && disks.length === 0 && <p className="p-4 text-sm text-text-muted">No mounted filesystems reported.</p>}
      </Panel>

      {smartAbsent && (
        <p className="rounded-md border border-border bg-surface-1 px-3 py-2 text-xs text-text-secondary">
          Drive health needs a SMART source the app can reach. On Windows and macOS smartctl is
          bundled with the app; on Linux install smartmontools (<span className="font-mono">sudo apt install smartmontools</span>
          {" "}or your distro's equivalent). Some USB and RAID bridges hide SMART entirely, and those
          drives will stay "not reported".
        </p>
      )}

      {disks?.map((d) => {
        const percent = d.total_bytes > 0 ? (d.used_bytes / d.total_bytes) * 100 : 0;
        const title = [d.volume_name || null, d.mount_point].filter(Boolean).join(" (") + (d.volume_name ? ")" : "");
        const badges = [
          d.is_system_drive ? "System" : null,
          d.drive_type ? DRIVE_TYPE_LABEL[d.drive_type] ?? d.drive_type : null,
          d.bus_type || null,
          d.filesystem || null,
        ].filter(Boolean);
        const health = healthFor(d);

        return (
          <Panel key={d.mount_point}>
            <PanelHeader>
              <div className="flex min-w-0 items-center gap-2">
                <h3 className="truncate text-sm font-semibold">{title}</h3>
                {badges.map((b) => (
                  <span key={b} className="shrink-0 rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium text-text-secondary">
                    {b}
                  </span>
                ))}
              </div>
            </PanelHeader>
            <div className="p-4">
              <div className="flex items-center justify-between text-xs text-text-secondary">
                <span className="tabular">{formatBytes(d.used_bytes)} used</span>
                <span className="tabular font-medium">{percent.toFixed(0)}%</span>
                <span className="tabular">{formatBytes(d.free_bytes)} free</span>
              </div>
              <div className="mt-2">
                <UsageBar percent={percent} />
              </div>

              {health && <HealthDetail health={health} />}
              <p className="mt-3 font-mono text-[10px] text-text-muted">{d.device}</p>
            </div>
          </Panel>
        );
      })}

      {/* Physical drives with no mounted volume (unpartitioned or a drive
          with no letters assigned) render on their own so the page still
          lists everything the machine has. */}
      {unclaimed.map((h) => (
        <Panel key={h.device}>
          <PanelHeader>
            <div className="flex min-w-0 items-center gap-2">
              <h3 className="truncate text-sm font-semibold">{h.model || h.device}</h3>
              <span className="shrink-0 rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium text-text-secondary">
                No volumes mounted
              </span>
            </div>
          </PanelHeader>
          <div className="p-4">
            {h.total_bytes ? (
              <p className="text-xs text-text-secondary tabular">
                {formatBytes(h.total_bytes)} · {h.bus_type || "unknown transport"}
              </p>
            ) : null}
            <HealthDetail health={h} />
          </div>
        </Panel>
      ))}
    </div>
  );
}

function HealthDetail({ health }: { health: DiskHealth }) {
  const pct = health.health_percent;
  const hasPercent = pct != null && pct > 0;
  const sourceLabel = !hasPercent
    ? null
    : health.health_source === "smart"
      ? health.estimated
        ? "From the drive's SMART data (calculated)"
        : "From the drive's SMART data"
      : "Estimated from Windows storage counters";

  return (
    <div className="mt-4 rounded-md border border-border bg-surface-0 p-3">
      <div className="flex items-start justify-between gap-3">
        <div>
          <p className="text-[11px] font-medium uppercase tracking-wide text-text-muted">Drive health</p>
          <div className="mt-1 flex items-center gap-2">
            <StatusDot status={HEALTH_STATUS_MAP[health.status] ?? "unknown"} label={health.status} />
          </div>
        </div>
        <div className="text-right">
          <span className="tabular text-xl font-semibold text-text-primary">
            {hasPercent && pct != null ? `${pct.toFixed(0)}%` : "n/a"}
          </span>
          {sourceLabel && <p className="text-[10px] text-text-muted">{sourceLabel}</p>}
        </div>
      </div>
      <div className="mt-2">
        {hasPercent ? <HealthBar percent={pct ?? 0} /> : (
          <p className="text-[11px] text-text-muted">
            This drive's health isn't reported by its controller or transport.
          </p>
        )}
      </div>
      <div className="mt-3 grid grid-cols-2 gap-3 text-xs sm:grid-cols-3">
        <Metric label="Temperature" value={health.temperature_celsius ? formatCelsius(health.temperature_celsius) : "not reported"} />
        <Metric label="Power-on hours" value={health.power_on_hours ? health.power_on_hours.toLocaleString() : "not reported"} />
        <Metric label="Power cycles" value={health.power_cycles ? health.power_cycles.toLocaleString() : "not reported"} />
      </div>
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-text-muted">{label}</p>
      <p className="tabular mt-0.5 font-medium text-text-primary">{value}</p>
    </div>
  );
}
