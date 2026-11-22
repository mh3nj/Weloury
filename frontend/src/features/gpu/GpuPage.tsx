import { useCallback, useEffect, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { UsageBar } from "@/components/ui/UsageBar";
import { Button } from "@/components/ui/Button";
import { formatBytes } from "@/lib/format";
import type { GPUInfo, GPUUsage } from "@/types/system";

/**
 * GPU telemetry (spec-adjacent: the task-manager-style capability set).
 * Polls gpu.list once for the adapter inventory and gpu.usage per
 * adapter on a 2s cadence - the same live-poll shape OverviewPage uses
 * for CPU/memory. Every telemetry field is best-effort backend-side
 * (temperature/fan need vendor driver WMI namespaces and are simply
 * absent when the machine doesn't expose them), so this page renders
 * "not reported" rather than an error for missing fields.
 */

const POLL_MS = 2000;

interface AdapterSample {
  info: GPUInfo;
  usage: GPUUsage | null;
  error: string | null;
}

export function GpuPage({ client }: { client: FwpClient }) {
  const [adapters, setAdapters] = useState<GPUInfo[] | null>(null);
  const [samples, setSamples] = useState<Record<number, AdapterSample>>({});
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    client
      .request<GPUInfo[]>("gpu.list")
      .then((v) => !cancelled && setAdapters(v ?? []))
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
    };
  }, [client]);

  const poll = useCallback(() => {
    if (!adapters) return;
    let alive = true;
    const next: Record<number, AdapterSample> = {};
    void Promise.all(
      adapters.map(async (_, index): Promise<[number, AdapterSample]> => {
        try {
          const usage = await client.request<GPUUsage>("gpu.usage", { index });
          return [index, { info: adapters[index], usage, error: null }];
        } catch (err) {
          return [index, { info: adapters[index], usage: null, error: err instanceof Error ? err.message : String(err) }];
        }
      }),
    ).then((entries) => {
      if (!alive) return;
      for (const [index, sample] of entries) next[index] = sample;
      setSamples(next);
    });
    return () => {
      alive = false;
    };
  }, [adapters, client]);

  useEffect(() => {
    if (!adapters) return;
    const cleanup = poll();
    const id = setInterval(poll, POLL_MS);
    return () => {
      cleanup?.();
      clearInterval(id);
    };
  }, [adapters, poll]);

  return (
    <div className="flex flex-col gap-4">
      <Panel>
        <PanelHeader>
          <h2 className="text-sm font-semibold">GPUs</h2>
          {adapters && <span className="text-xs text-text-muted">{adapters.length} adapter(s) · live every {POLL_MS / 1000}s</span>}
        </PanelHeader>
        {error && <p className="p-4 text-sm text-text-secondary">Couldn't load GPU info: {error}</p>}
        {!error && !adapters && <p className="p-4 text-sm text-text-muted">Loading...</p>}
        {adapters && adapters.length === 0 && (
          <p className="p-4 text-sm text-text-muted">
            No graphics adapters reported by this machine (common in VMs without GPU passthrough).
          </p>
        )}
      </Panel>

      {adapters?.map((gpu, index) => {
        const sample = samples[index];
        const usage = sample?.usage ?? null;
        const memTotal = usage?.memory_total_bytes || gpu.total_memory_bytes || 0;
        const memUsed = usage?.memory_used_bytes ?? 0;
        const memPercent = memTotal > 0 ? (memUsed / memTotal) * 100 : 0;
        // utilization_percent is nullable (adapter present, no telemetry
        // source - e.g. driver counters absent); nil renders "not
        // reported" instead of a lying 0%.
        const util = usage?.utilization_percent;

        return (
          <Panel key={index}>
            <PanelHeader>
              <div className="min-w-0">
                <h3 className="truncate text-sm font-semibold">{gpu.name}</h3>
                <p className="text-xs text-text-muted">
                  {[gpu.vendor, gpu.driver_version && `driver ${gpu.driver_version}`, gpu.pci_id].filter(Boolean).join(" · ") || "-"}
                </p>
              </div>
              <Button variant="ghost" onClick={poll}>
                Refresh
              </Button>
            </PanelHeader>
            <div className="grid grid-cols-1 gap-4 p-4 sm:grid-cols-2 lg:grid-cols-4">
              <Metric label="Utilization" value={util != null ? `${util.toFixed(0)}%` : "not reported"}>
                {util != null && <UsageBar percent={util} />}
              </Metric>
              <Metric
                label="Memory"
                value={
                  memTotal > 0
                    ? `${formatBytes(memUsed)} / ${formatBytes(memTotal)}`
                    : memUsed > 0
                      ? formatBytes(memUsed)
                      : "not reported"
                }
              >
                {memTotal > 0 && <UsageBar percent={memPercent} />}
              </Metric>
              <Metric
                label="Temperature"
                value={usage?.temperature_celsius ? `${usage.temperature_celsius.toFixed(0)} °C` : "not reported"}
              />
              <Metric label="Fan" value={usage?.fan_percent ? `${usage.fan_percent.toFixed(0)}%` : "not reported"} />
              <Metric
                label="Power"
                value={
                  usage?.power_watts
                    ? usage.power_limit_watts
                      ? `${usage.power_watts.toFixed(1)} W / ${usage.power_limit_watts.toFixed(0)} W limit`
                      : `${usage.power_watts.toFixed(1)} W`
                    : "not reported"
                }
              />
              <Metric label="Core clock" value={usage?.clock_mhz ? `${usage.clock_mhz.toLocaleString()} MHz` : "not reported"} />
            </div>
            {sample?.error && <p className="px-4 pb-3 text-xs text-status-degraded">Live data unavailable: {sample.error}</p>}
          </Panel>
        );
      })}
    </div>
  );
}

function Metric({ label, value, children }: { label: string; value: string; children?: React.ReactNode }) {
  return (
    <div>
      <p className="text-xs text-text-muted">{label}</p>
      <p className="tabular mt-0.5 text-sm font-medium text-text-primary">{value}</p>
      {children && <div className="mt-2">{children}</div>}
    </div>
  );
}
