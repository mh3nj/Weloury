import { useEffect, useState, type ReactNode } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { StatusDot } from "@/components/ui/StatusDot";
import { formatBytes, formatPercent } from "@/lib/format";
import type { Container } from "@/types/containers";

interface ContainerDetailPanelProps {
  client: FwpClient;
  container: Container;
  onClose: () => void;
  onChanged: () => void;
}

export function ContainerDetailPanel({ client, container, onClose, onChanged }: ContainerDetailPanelProps) {
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [logs, setLogs] = useState<string[] | null>(null);
  const [logsError, setLogsError] = useState<string | null>(null);
  const running = container.state === "running";

  useEffect(() => {
    setLogs(null);
    setLogsError(null);
    const controller = new AbortController();
    client
      .request<string[]>("containers.logs", { id: container.id, tail: 100 }, controller.signal)
      .then((v) => setLogs(v ?? []))
      .catch((err) => setLogsError(err instanceof Error ? err.message : String(err)));
    return () => controller.abort();
  }, [client, container.id]);

  async function runAction(action: "start" | "stop" | "restart") {
    setBusy(true);
    setActionError(null);
    try {
      await client.request(`containers.${action}`, { id: container.id });
      onChanged();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel className="flex w-[420px] shrink-0 flex-col">
      <PanelHeader>
        <h2 className="truncate text-sm font-semibold">{container.name}</h2>
        <button onClick={onClose} className="text-text-muted hover:text-text-primary" aria-label="Close">
          ✕
        </button>
      </PanelHeader>

      <div className="flex flex-col gap-4 overflow-y-auto p-4 text-sm">
        <Row label="State" value={<StatusDot status={running ? "healthy" : "unknown"} label={container.status} />} />
        <Row label="Image" value={<span className="font-mono text-xs">{container.image}</span>} />
        {container.ports && <Row label="Ports" value={<span className="font-mono text-xs">{container.ports}</span>} />}
        <Row label="Created" value={new Date(container.created_at).toLocaleString()} />

        {running && (
          <div className="grid grid-cols-2 gap-3">
            <Row label="CPU" value={container.cpu_percent != null ? formatPercent(container.cpu_percent) : "-"} />
            <Row
              label="Memory"
              value={container.memory_usage_bytes != null ? formatBytes(container.memory_usage_bytes) : "-"}
            />
          </div>
        )}

        {actionError && <p className="text-xs text-status-unhealthy">{actionError}</p>}

        <div className="flex flex-wrap gap-2 border-t border-border pt-4">
          {running ? (
            <>
              <Button variant="secondary" onClick={() => runAction("restart")} disabled={busy}>
                Restart
              </Button>
              <Button variant="danger" onClick={() => runAction("stop")} disabled={busy}>
                Stop
              </Button>
            </>
          ) : (
            <Button variant="primary" onClick={() => runAction("start")} disabled={busy}>
              Start
            </Button>
          )}
        </div>

        <div className="border-t border-border pt-4">
          <h3 className="mb-2 text-xs font-semibold uppercase tracking-wide text-text-muted">
            Logs (last 100 lines)
          </h3>
          {logsError && <p className="text-xs text-text-secondary">Couldn't load logs: {logsError}</p>}
          {!logsError && !logs && <p className="text-xs text-text-muted">Loading...</p>}
          {logs && logs.length === 0 && <p className="text-xs text-text-muted">No log output.</p>}
          {logs && logs.length > 0 && (
            <pre className="max-h-64 overflow-y-auto rounded-md bg-surface-1 p-2 font-mono text-xs text-text-secondary">
              {logs.join("\n")}
            </pre>
          )}
        </div>
      </div>
    </Panel>
  );
}

function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div>
      <div className="text-xs text-text-muted">{label}</div>
      <div className="mt-0.5 text-text-primary">{value}</div>
    </div>
  );
}
