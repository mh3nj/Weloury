import { useEffect, useState, type ReactNode } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { StatusDot } from "@/components/ui/StatusDot";
import type { ServiceDetail } from "@/types/system";

const STATE_MAP: Record<string, "healthy" | "degraded" | "unhealthy" | "unknown"> = {
  running: "healthy",
  stopped: "unknown",
  failed: "unhealthy",
};

interface ServiceDetailPanelProps {
  client: FwpClient;
  name: string;
  onClose: () => void;
  onChanged: () => void;
}

type PendingAction = "stop" | "restart" | null;

/**
 * Stop and Restart route through ConfirmDialog - both can disrupt
 * something currently depending on the service; Start and toggling
 * autostart don't, since neither immediately breaks something already
 * running. This mirrors ProcessDetailPanel's confirmation gating logic,
 * scaled to what's actually risky for a service rather than copying
 * process semantics wholesale.
 */
export function ServiceDetailPanel({ client, name, onClose, onChanged }: ServiceDetailPanelProps) {
  const [detail, setDetail] = useState<ServiceDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<PendingAction>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function load() {
    const controller = new AbortController();
    setDetail(null);
    setError(null);
    client
      .request<ServiceDetail>("service.detail", { name }, controller.signal)
      .then(setDetail)
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    return () => controller.abort();
  }

  useEffect(load, [client, name]);

  async function runAction(method: string, extraParams: Record<string, unknown> = {}) {
    setBusy(true);
    setActionError(null);
    try {
      await client.request(method, { name, ...extraParams });
      load();
      onChanged();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function confirmPending() {
    if (!pending) return;
    const method = pending === "stop" ? "service.stop" : "service.restart";
    setPending(null);
    await runAction(method);
  }

  return (
    <Panel className="w-96 shrink-0">
      <PanelHeader>
        <h2 className="truncate text-sm font-semibold">{name}</h2>
        <button onClick={onClose} className="text-text-muted hover:text-text-primary" aria-label="Close">
          ✕
        </button>
      </PanelHeader>

      {error && <p className="p-4 text-sm text-text-secondary">Couldn't load service: {error}</p>}
      {!error && !detail && <p className="p-4 text-sm text-text-muted">Loading...</p>}

      {detail && (
        <div className="flex flex-col gap-4 p-4 text-sm">
          <Row label="Display name" value={detail.display_name || "-"} />
          <Row label="Description" value={detail.description || "-"} />
          <Row label="State" value={<StatusDot status={STATE_MAP[detail.state] ?? "unknown"} label={detail.state || "unknown"} />} />
          <Row label="Startup" value={detail.startup_mode || "unknown"} />
          {detail.pid > 0 && <Row label="PID" value={String(detail.pid)} />}
          {detail.dependencies && detail.dependencies.length > 0 && (
            <Row label="Depends on" value={<span className="text-xs">{detail.dependencies.join(", ")}</span>} />
          )}

          {actionError && <p className="text-xs text-status-unhealthy">{actionError}</p>}

          <div className="flex flex-wrap gap-2 border-t border-border pt-4">
            <Button variant="secondary" onClick={() => runAction("service.start")} disabled={busy}>
              Start
            </Button>
            <Button variant="secondary" onClick={() => setPending("stop")} disabled={busy}>
              Stop
            </Button>
            <Button variant="secondary" onClick={() => setPending("restart")} disabled={busy}>
              Restart
            </Button>
            <Button
              variant="ghost"
              onClick={() => runAction("service.set_enabled", { enabled: detail.startup_mode !== "enabled" })}
              disabled={busy}
            >
              {detail.startup_mode === "enabled" ? "Disable autostart" : "Enable autostart"}
            </Button>
          </div>
        </div>
      )}

      <ConfirmDialog
        open={pending !== null}
        title={pending === "stop" ? `Stop ${name}?` : `Restart ${name}?`}
        description={
          pending === "stop"
            ? "Anything currently depending on this service may be affected."
            : "The service will be stopped and started again; there will be a brief interruption."
        }
        confirmLabel={pending === "stop" ? "Stop" : "Restart"}
        destructive={pending === "stop"}
        onConfirm={confirmPending}
        onCancel={() => setPending(null)}
      />
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
