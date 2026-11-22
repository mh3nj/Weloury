import { useEffect, useState, type ReactNode } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { StatusDot } from "@/components/ui/StatusDot";
import { formatBytes, formatPercent } from "@/lib/format";
import type { ProcessDetail } from "@/types/system";

const STATUS_MAP: Record<string, "healthy" | "degraded" | "unhealthy" | "unknown"> = {
  running: "healthy",
  sleeping: "unknown",
  idle: "unknown",
  disk_sleep: "degraded",
  stopped: "degraded",
  zombie: "unhealthy",
  dead: "unhealthy",
};

interface ProcessDetailPanelProps {
  client: FwpClient;
  pid: number;
  onClose: () => void;
  /** Called after a successful termination so the parent can refresh its list. */
  onTerminated: () => void;
}

type PendingAction = { force: boolean } | null;

/**
 * The one place in Weloury (so far) where a destructive action actually
 * happens - every path to calling process.terminate routes through
 * ConfirmDialog first, per spec section 6: "never allow accidental
 * termination through a single careless click."
 */
export function ProcessDetailPanel({ client, pid, onClose, onTerminated }: ProcessDetailPanelProps) {
  const [detail, setDetail] = useState<ProcessDetail | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<PendingAction>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setDetail(null);
    setError(null);
    const controller = new AbortController();

    client
      .request<ProcessDetail>("process.detail", { pid }, controller.signal)
      .then((v) => !cancelled && setDetail(v))
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)));

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [client, pid]);

  async function confirmTerminate() {
    if (!pending) return;
    setBusy(true);
    setActionError(null);
    try {
      await client.request("process.terminate", { pid, force: pending.force });
      setPending(null);
      onTerminated();
      onClose();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Panel className="w-96 shrink-0">
      <PanelHeader>
        <h2 className="text-sm font-semibold">Process {pid}</h2>
        <button onClick={onClose} className="text-text-muted hover:text-text-primary" aria-label="Close">
          ✕
        </button>
      </PanelHeader>

      {error && <p className="p-4 text-sm text-text-secondary">Couldn't load process: {error}</p>}
      {!error && !detail && <p className="p-4 text-sm text-text-muted">Loading...</p>}

      {detail && (
        <div className="flex flex-col gap-4 p-4 text-sm">
          <Row label="Name" value={detail.name || "-"} />
          <Row label="Status" value={<StatusDot status={STATUS_MAP[detail.status] ?? "unknown"} label={detail.status || "unknown"} />} />
          <Row label="User" value={detail.owner_name || detail.user || "-"} />
          <Row label="PID / PPID" value={`${detail.pid} / ${detail.ppid}`} />
          <Row label="CPU" value={formatPercent(detail.cpu_percent)} />
          <Row label="Memory" value={formatBytes(detail.memory_bytes)} />
          {detail.threads > 0 && <Row label="Threads" value={String(detail.threads)} />}
          <Row label="Executable" value={<span className="break-all">{detail.executable || "-"}</span>} />
          <Row
            label="Command line"
            value={
              <span className="break-all font-mono text-xs">
                {detail.command_line && detail.command_line.length > 0 ? detail.command_line.join(" ") : "-"}
              </span>
            }
          />

          {actionError && <p className="text-xs text-status-unhealthy">{actionError}</p>}

          <div className="flex gap-2 border-t border-border pt-4">
            <Button variant="secondary" onClick={() => setPending({ force: false })} disabled={busy}>
              Terminate
            </Button>
            <Button variant="danger" onClick={() => setPending({ force: true })} disabled={busy}>
              Force kill
            </Button>
          </div>
        </div>
      )}

      <ConfirmDialog
        open={pending !== null}
        title={pending?.force ? `Force kill process ${pid}?` : `Terminate process ${pid}?`}
        description={
          pending?.force
            ? "This immediately ends the process without letting it clean up. Unsaved work in this process will be lost."
            : "This asks the process to shut down gracefully. Most applications will exit cleanly; some may ignore the request."
        }
        confirmLabel={pending?.force ? "Force kill" : "Terminate"}
        destructive={pending?.force ?? false}
        onConfirm={confirmTerminate}
        onCancel={() => setPending(null)}
      />
    </Panel>
  );
}

function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div>
      <div className="text-xs text-text-muted">{label}</div>
      <div className="tabular mt-0.5 text-text-primary">{value}</div>
    </div>
  );
}
