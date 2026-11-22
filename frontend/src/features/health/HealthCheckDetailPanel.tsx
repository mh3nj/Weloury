import { useState, type ReactNode } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { StatusDot } from "@/components/ui/StatusDot";
import { CHECK_TYPE_META } from "./checkTypeMeta";
import type { HealthCheckStatus } from "@/types/observability";

const STATE_MAP: Record<string, "healthy" | "degraded" | "unhealthy" | "unknown"> = {
  HEALTHY: "healthy",
  DEGRADED: "degraded",
  UNHEALTHY: "unhealthy",
  UNKNOWN: "unknown",
};

interface HealthCheckDetailPanelProps {
  client: FwpClient;
  check: HealthCheckStatus;
  onClose: () => void;
  onEdit: () => void;
  onChanged: () => void;
}

export function HealthCheckDetailPanel({ client, check, onClose, onEdit, onChanged }: HealthCheckDetailPanelProps) {
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const meta = CHECK_TYPE_META[check.type];

  async function runNow() {
    setBusy(true);
    setActionError(null);
    try {
      await client.request("health.run_now", { id: check.id });
      onChanged();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  async function confirmDelete() {
    setConfirmingDelete(false);
    setBusy(true);
    setActionError(null);
    try {
      await client.request("health.delete", { id: check.id });
      onChanged();
      onClose();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  }

  return (
    <Panel className="w-96 shrink-0">
      <PanelHeader>
        <h2 className="truncate text-sm font-semibold">{check.name}</h2>
        <button onClick={onClose} className="text-text-muted hover:text-text-primary" aria-label="Close">
          ✕
        </button>
      </PanelHeader>

      <div className="flex flex-col gap-4 p-4 text-sm">
        <Row label="Type" value={meta.label} />
        <Row label="State" value={<StatusDot status={STATE_MAP[check.state] ?? "unknown"} label={check.state} />} />
        {check.message && <Row label="Last message" value={<span className="text-xs">{check.message}</span>} />}
        {meta.needsTarget && <Row label={meta.targetLabel ?? "Target"} value={check.target || "-"} />}
        {meta.needsCommand && (
          <Row
            label="Command"
            value={<span className="font-mono text-xs">{(check.command_args ?? []).join(" ")}</span>}
          />
        )}
        {meta.needsThreshold && <Row label="Threshold" value={`${check.threshold}%`} />}
        <Row label="Interval" value={`every ${check.interval_seconds}s, ${check.timeout_seconds}s timeout`} />
        <Row
          label="Retry policy"
          value={
            check.retry.max_retries > 0
              ? `${check.retry.max_retries}× retry, ${check.retry.retry_delay_seconds}s apart`
              : "no retries"
          }
        />
        <Row label="Failure count" value={String(check.failure_count)} />
        <Row label="Last success" value={formatTimestamp(check.last_success_at)} />
        <Row label="Last failure" value={formatTimestamp(check.last_failure_at)} />

        {actionError && <p className="text-xs text-status-unhealthy">{actionError}</p>}

        <div className="flex flex-wrap gap-2 border-t border-border pt-4">
          <Button variant="secondary" onClick={runNow} disabled={busy}>
            Run now
          </Button>
          <Button variant="secondary" onClick={onEdit} disabled={busy}>
            Edit
          </Button>
          <Button variant="danger" onClick={() => setConfirmingDelete(true)} disabled={busy}>
            Delete
          </Button>
        </div>
      </div>

      <ConfirmDialog
        open={confirmingDelete}
        title={`Delete "${check.name}"?`}
        description="This stops the check immediately and removes its configuration. This can't be undone."
        confirmLabel="Delete"
        destructive
        onConfirm={confirmDelete}
        onCancel={() => setConfirmingDelete(false)}
      />
    </Panel>
  );
}

function formatTimestamp(value?: string): string {
  if (!value) return "never";
  return new Date(value).toLocaleString();
}

function Row({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div>
      <div className="text-xs text-text-muted">{label}</div>
      <div className="mt-0.5 text-text-primary">{value}</div>
    </div>
  );
}
