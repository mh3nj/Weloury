import { useEffect, useMemo, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { useEventStream } from "@/hooks/useEventStream";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { StatusDot } from "@/components/ui/StatusDot";
import { Button } from "@/components/ui/Button";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";
import { AutomationRuleFormDialog } from "./AutomationRuleFormDialog";
import { AutomationRuleDetailPanel } from "./AutomationRuleDetailPanel";
import type { AutomationRuleInput, AutomationRuleStatus, RunStatus } from "@/types/automation";

const RUN_STATUS_MAP: Record<RunStatus, { status: "healthy" | "degraded" | "unhealthy" | "unknown"; label: string }> = {
  succeeded: { status: "healthy", label: "Succeeded" },
  failed: { status: "unhealthy", label: "Failed" },
  skipped_cooldown: { status: "degraded", label: "Skipped" },
  skipped_conditions: { status: "degraded", label: "Skipped" },
  "": { status: "unknown", label: "Never run" },
};

/**
 * Automation rule engine (spec section 11). Live updates arrive via
 * automation.rule_executed (backend/internal/automation/events.go),
 * which fires on every execution attempt - including skips - not just
 * transitions the way health's events do (see that event's doc comment
 * for why); a re-fetch on any such event is enough to keep this list
 * current, the same "re-fetch rather than reconcile" approach HealthPage
 * uses.
 */
export function AutomationPage({ client }: { client: FwpClient }) {
  const [rules, setRules] = useState<AutomationRuleStatus[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<AutomationRuleStatus | undefined>(undefined);
  const { events } = useEventStream(client, ["automation.rule_executed"]);
  const lastEventCount = events.length;

  function load() {
    const controller = new AbortController();
    client
      .request<AutomationRuleStatus[]>("automation.list", undefined, controller.signal)
      .then((v) => setRules(v ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    return () => controller.abort();
  }

  useEffect(load, [client]);
  useEffect(() => {
    if (lastEventCount === 0) return;
    return load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastEventCount]);

  const selected = useMemo(() => rules?.find((r) => r.id === selectedId) ?? null, [rules, selectedId]);

  function openCreate() {
    setEditing(undefined);
    setFormOpen(true);
  }

  function openEdit(rule: AutomationRuleStatus) {
    setEditing(rule);
    setFormOpen(true);
  }

  async function handleSubmit(input: AutomationRuleInput) {
    if (editing) {
      await client.request("automation.update", { id: editing.id, ...input });
    } else {
      await client.request("automation.create", input);
    }
    setFormOpen(false);
    load();
  }

  return (
    <div className="flex h-full gap-4">
      <Panel className="flex min-w-0 flex-1 flex-col">
        <PanelHeader>
          <h2 className="text-sm font-semibold">Automation</h2>
          <Button variant="primary" onClick={openCreate}>
            New rule
          </Button>
        </PanelHeader>

        {error && <p className="p-4 text-sm text-text-secondary">Couldn't load automation rules: {error}</p>}
        {!error && !rules && <p className="p-4 text-sm text-text-muted">Loading...</p>}

        {rules && rules.length === 0 && (
          <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
            <p className="text-sm text-text-secondary">No automation rules yet.</p>
            <p className="max-w-sm text-xs text-text-muted">
              React automatically to an event - a health check failing, a schedule firing - by restarting a
              process, restarting a service, running a command, or logging what happened.
            </p>
            <Button variant="primary" className="mt-2" onClick={openCreate}>
              New rule
            </Button>
          </div>
        )}

        {rules && rules.length > 0 && (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <table className="w-full text-left text-sm">
              <thead className="sticky top-0 bg-surface-1">
                <tr className="border-b border-border text-xs text-text-muted">
                  <th className="px-4 py-2 font-medium">Name</th>
                  <th className="px-4 py-2 font-medium">Trigger</th>
                  <th className="px-4 py-2 font-medium">Enabled</th>
                  <th className="px-4 py-2 font-medium">Last run</th>
                  <th className="px-4 py-2 text-right font-medium">Runs</th>
                </tr>
              </thead>
              <tbody>
                {rules.map((r) => {
                  const runStatus = RUN_STATUS_MAP[r.last_run_status] ?? RUN_STATUS_MAP[""];
                  return (
                    <tr
                      key={r.id}
                      tabIndex={0}
                      aria-selected={selectedId === r.id}
                      onClick={() => setSelectedId(r.id)}
                      onKeyDown={onActivateKey(() => setSelectedId(r.id))}
                      className={`cursor-pointer border-b border-border last:border-0 hover:bg-surface-2 ${FOCUS_RING} ${
                        selectedId === r.id ? "bg-accent/15" : ""
                      }`}
                    >
                      <td className="px-4 py-2 text-text-primary">{r.name}</td>
                      <td className="px-4 py-2 text-text-secondary">
                        {r.trigger.type === "event" ? r.trigger.event_type : "on schedule"}
                      </td>
                      <td className="px-4 py-2 text-text-secondary">{r.enabled ? "Yes" : "Paused"}</td>
                      <td className="px-4 py-2">
                        <StatusDot status={runStatus.status} label={runStatus.label} />
                      </td>
                      <td className="tabular px-4 py-2 text-right text-text-secondary">{r.run_count}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {selected && (
        <AutomationRuleDetailPanel
          client={client}
          rule={selected}
          onClose={() => setSelectedId(null)}
          onEdit={() => openEdit(selected)}
          onChanged={load}
        />
      )}

      <AutomationRuleFormDialog
        client={client}
        open={formOpen}
        existing={editing}
        onCancel={() => setFormOpen(false)}
        onSubmit={handleSubmit}
      />
    </div>
  );
}
