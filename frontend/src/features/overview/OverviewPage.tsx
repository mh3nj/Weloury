import { useEffect, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { useEventStream, type StreamStatus } from "@/hooks/useEventStream";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { StatusDot } from "@/components/ui/StatusDot";
import { UsageBar } from "@/components/ui/UsageBar";
import { formatBytes, formatPercent, formatUptime } from "@/lib/format";
import type { CPUInfo, CPUUsage, MemoryInfo, SystemInfo } from "@/types/system";

interface VersionInfo {
  version: string;
  commit: string;
  build_date: string;
  protocol_version: number;
}

interface HeartbeatPayload {
  sequence: number;
}

/**
 * Weloury's actual system overview (spec section 5): identity, CPU, and
 * memory at a glance. `ConnectionPanel` below is Phase 1's original
 * end-to-end IPC proof, kept - but renamed and restructured this
 * phase: it used to be named `DiagnosticsPanel`, which collided with
 * the actual spec section 23 Diagnostics feature (a different
 * component, in features/settings/, with a genuinely different
 * purpose - a system-wide diagnostic snapshot). It also used to show
 * a full-width panel with a "Send ping" button and a raw JSON event
 * ticker at the same visual weight as the CPU/Memory panels, on the
 * one screen a user sees every time they open Weloury - exactly the
 * "visual clutter" and information-hierarchy problem spec section 20
 * warns against, and it read as leftover debug output on what's
 * supposed to be "restrained, precise, trustworthy" software (section
 * 21). The underlying liveness check is still genuinely useful - kept
 * as a compact, always-visible status line, with the raw ping/event
 * detail collapsed behind an explicit toggle instead of always shown.
 */
export function OverviewPage({ client }: { client: FwpClient }) {
  const [system, setSystem] = useState<SystemInfo | null>(null);
  const [cpu, setCPU] = useState<CPUInfo | null>(null);
  const [cpuUsage, setCPUUsage] = useState<CPUUsage | null>(null);
  const [cpuUsageError, setCPUUsageError] = useState<string | null>(null);
  const [memory, setMemory] = useState<MemoryInfo | null>(null);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();

    client
      .request<SystemInfo>("system.info", undefined, controller.signal)
      .then((v) => !cancelled && setSystem(v))
      .catch(() => {});
    client
      .request<CPUInfo>("system.cpu_info", undefined, controller.signal)
      .then((v) => !cancelled && setCPU(v))
      .catch(() => {});
    client
      .request<MemoryInfo>("system.memory", undefined, controller.signal)
      .then((v) => !cancelled && setMemory(v))
      .catch(() => {});
    client
      .request<CPUUsage>("system.cpu_usage", undefined, controller.signal)
      .then((v) => !cancelled && setCPUUsage(v))
      .catch((err) => {
        if (!cancelled) setCPUUsageError(err instanceof Error ? err.message : String(err));
      });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [client]);

  return (
    <div className="grid max-w-3xl gap-4">
      <Panel>
        <PanelHeader>
          <h2 className="text-sm font-semibold">System</h2>
        </PanelHeader>
        <div className="grid grid-cols-2 gap-3 p-4 text-sm">
          <Field label="Hostname" value={system?.hostname} />
          <Field label="OS" value={system?.os_version || system?.os} />
          <Field label="Kernel" value={system?.kernel_version} />
          <Field label="Uptime" value={system ? formatUptime(system.uptime_seconds) : undefined} />
        </div>
      </Panel>

      <Panel>
        <PanelHeader>
          <h2 className="text-sm font-semibold">CPU</h2>
          {cpuUsage && (
            <span className="tabular text-xs text-text-secondary">{formatPercent(cpuUsage.overall_percent)}</span>
          )}
        </PanelHeader>
        <div className="p-4">
          <div className="grid grid-cols-2 gap-3 text-sm">
            <Field label="Model" value={cpu?.model_name} />
            <Field
              label="Cores"
              value={cpu ? `${cpu.physical_cores} physical / ${cpu.logical_cores} logical` : undefined}
            />
          </div>
          {cpuUsage ? (
            <div className="mt-3">
              <UsageBar percent={cpuUsage.overall_percent} />
            </div>
          ) : cpuUsageError ? (
            <p className="mt-3 text-xs text-text-muted">Live usage unavailable: {cpuUsageError}</p>
          ) : (
            <p className="mt-3 text-xs text-text-muted">Sampling...</p>
          )}
        </div>
      </Panel>

      <Panel>
        <PanelHeader>
          <h2 className="text-sm font-semibold">Memory</h2>
          {memory && (
            <span className="tabular text-xs text-text-secondary">
              {formatBytes(memory.used_bytes)} / {formatBytes(memory.total_bytes)}
            </span>
          )}
        </PanelHeader>
        <div className="p-4">
          {memory ? (
            <UsageBar percent={(memory.used_bytes / memory.total_bytes) * 100} />
          ) : (
            <p className="text-xs text-text-muted">Loading...</p>
          )}
        </div>
      </Panel>

      <ConnectionPanel client={client} />
    </div>
  );
}

function Field({ label, value }: { label: string; value?: string }) {
  return (
    <div>
      <div className="text-text-muted">{label}</div>
      <div className="tabular text-text-primary">{value ?? "-"}</div>
    </div>
  );
}

const STREAM_STATUS_DOT: Record<StreamStatus, "healthy" | "degraded" | "unknown"> = {
  open: "healthy",
  connecting: "unknown",
  reconnecting: "degraded",
};

function ConnectionPanel({ client }: { client: FwpClient }) {
  const [version, setVersion] = useState<VersionInfo | null>(null);
  const [pingResult, setPingResult] = useState<string | null>(null);
  const [pinging, setPinging] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const { events, status } = useEventStream<HeartbeatPayload | Record<string, never>>(client);

  useEffect(() => {
    let cancelled = false;
    client
      .request<VersionInfo>("system.version")
      .then((v) => {
        if (!cancelled) setVersion(v);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [client]);

  async function handlePing() {
    setPinging(true);
    try {
      const result = await client.request<{ echo: string; pong: boolean }>("system.ping", {
        echo: "hello from the frontend",
      });
      setPingResult(`pong: ${result.pong}, echo: "${result.echo}"`);
    } catch (err) {
      setPingResult(`ping failed: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setPinging(false);
    }
  }

  return (
    <Panel>
      <PanelHeader>
        <div className="flex items-center gap-3">
          <StatusDot status={STREAM_STATUS_DOT[status]} label={status === "open" ? "Connected" : status} />
          <span className="text-xs text-text-muted">
            {version ? `weloury-core ${version.version} · protocol v${version.protocol_version}` : "..."}
          </span>
        </div>
        <Button variant="ghost" onClick={() => setExpanded((v) => !v)}>
          {expanded ? "Hide details" : "Connection details"}
        </Button>
      </PanelHeader>

      {expanded && (
        <>
          <div className="flex items-center gap-3 border-b border-border p-4">
            <Button onClick={handlePing} disabled={pinging}>
              {pinging ? "Pinging..." : "Send ping"}
            </Button>
            {pingResult && <span className="text-sm text-text-secondary">{pingResult}</span>}
          </div>
          <div className="p-2">
            <div className="flex items-center justify-between px-2 pb-1">
              <span className="text-xs font-medium text-text-secondary">Live events</span>
              <span className="text-xs text-text-muted">{status}</span>
            </div>
            <ul className="max-h-48 overflow-y-auto font-mono text-xs">
              {events.length === 0 && (
                <li className="px-2 py-4 text-center text-text-muted">Waiting for the next heartbeat...</li>
              )}
              {[...events].reverse().map((e, i) => (
                <li key={i} className="px-2 py-1 text-text-secondary">
                  <span className="text-text-muted">{new Date(e.receivedAt).toLocaleTimeString()}</span> {e.channel}{" "}
                  {e.payload ? JSON.stringify(e.payload) : ""}
                </li>
              ))}
            </ul>
          </div>
        </>
      )}
    </Panel>
  );
}
