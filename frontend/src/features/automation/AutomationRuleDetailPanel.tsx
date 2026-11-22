import { useState, type ReactNode } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { StatusDot } from "@/components/ui/StatusDot";
import { ACTION_TYPE_META } from "./actionTypeMeta";
import { MAX_CONSECUTIVE_FAILURES, type AutomationRuleStatus, type RunStatus } from "@/types/automation";

const RUN_STATUS_MAP: Record<RunStatus, { status: "healthy" | "degraded" | "unhealthy" | "unknown"; label: string }> = {
  succeeded: { status: "healthy", label: "Succeeded" },
  failed: { status: "unhealthy", label: "Failed" },
  skipped_cooldown: { status: "degraded", label: "Skipped (cooldown)" },
  skipped_conditions: { status: "degraded", label: "Skipped (conditions)" },
  "": { status: "unknown", label: "Never run" },
};

interface AutomationRuleDetailPanelProps {
  client: FwpClient;
  rule: AutomationRuleStatus;
  onClose: () => void;
  onEdit: () => void;
  onChanged: () => void;
}

export function AutomationRuleDetailPanel({ client, rule, onClose, onEdit, onChanged }: AutomationRuleDetailPanelProps) {
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const runStatus = RUN_STATUS_MAP[rule.last_run_status] ?? RUN_STATUS_MAP[""];

  async function confirmDelete() {
    setConfirmingDelete(false);
    setBusy(true);
    setActionError(null);
    try {
      await client.request("automation.delete", { id: rule.id });
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
        <h2 className="truncate text-sm font-semibold">{rule.name}</h2>
        <button onClick={onClose} className="text-text-muted hover:text-text-primary" aria-label="Close">
          ✕
        </button>
      </PanelHeader>

      <div className="flex flex-col gap-4 p-4 text-sm">
        <Row label="Enabled" value={rule.enabled ? "Yes" : "Paused"} />
        {!rule.enabled && rule.consecutive_failures >= MAX_CONSECUTIVE_FAILURES && (
          <p className="-mt-2 text-xs text-status-unhealthy">
            Auto-disabled after {rule.consecutive_failures} consecutive failed runs. Fix the
            underlying issue, then re-enable via Edit.
          </p>
        )}
        <Row
          label="Trigger"
          value={
            rule.trigger.type === "event"
              ? `Event: ${rule.trigger.event_type}`
              : `Schedule: ${rule.trigger.schedule_id}`
          }
        />
        {rule.conditions && rule.conditions.length > 0 && (
          <Row
            label="Conditions"
            value={
              <ul className="space-y-0.5">
                {rule.conditions.map((c, i) => (
                  <li key={i} className="font-mono text-xs">
                    {c.field} = {c.equals}
                  </li>
                ))}
              </ul>
            }
          />
        )}
        <Row
          label="Actions"
          value={
            <ol className="list-decimal space-y-0.5 pl-4">
              {rule.actions.map((a, i) => (
                <li key={i} className="text-xs">
                  {ACTION_TYPE_META[a.type].label}
                  {a.process_name ? ` - ${a.process_name}` : ""}
                  {a.service_name ? ` - ${a.service_name}` : ""}
                </li>
              ))}
            </ol>
          }
        />
        {rule.cooldown_seconds ? <Row label="Cooldown" value={`${rule.cooldown_seconds}s`} /> : null}

        <div className="border-t border-border pt-4">
          <Row label="Last run" value={<StatusDot status={runStatus.status} label={runStatus.label} />} />
          {rule.last_run_message && (
            <p className="mt-1 text-xs text-text-muted">{rule.last_run_message}</p>
          )}
        </div>
        <Row label="Last run at" value={rule.last_run_at ? new Date(rule.last_run_at).toLocaleString() : "never"} />
        <Row label="Run count" value={String(rule.run_count)} />
        {rule.consecutive_failures > 0 && (
          <Row
            label="Consecutive failures"
            value={
              <span className={rule.consecutive_failures >= MAX_CONSECUTIVE_FAILURES ? "text-status-unhealthy" : undefined}>
                {rule.consecutive_failures} of {MAX_CONSECUTIVE_FAILURES}
              </span>
            }
          />
        )}

        {actionError && <p className="text-xs text-status-unhealthy">{actionError}</p>}

        <div className="flex flex-wrap gap-2 border-t border-border pt-4">
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
        title={`Delete "${rule.name}"?`}
        description="This stops the rule immediately and removes its configuration. This can't be undone."
        confirmLabel="Delete"
        destructive
        onConfirm={confirmDelete}
        onCancel={() => setConfirmingDelete(false)}
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
