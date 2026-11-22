import { useEffect, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { ScheduleFormDialog } from "./ScheduleFormDialog";
import type { ScheduleInput, ScheduledTask } from "@/types/automation";

/**
 * Scheduler (spec section 12). Deliberately no live-updating subscription
 * here the way HealthPage has one: a task's NextRunAt/LastRunAt changing
 * is routine, expected churn (it happens every time it fires, which for
 * an interval task could be every few seconds) rather than the kind of
 * noteworthy transition health.check_passed/failed represents - polling
 * on open plus a manual refresh after any action is enough for a screen
 * whose main job is "what's scheduled," not "watch it tick."
 */
export function SchedulesPage({ client }: { client: FwpClient }) {
  const [tasks, setTasks] = useState<ScheduledTask[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<ScheduledTask | undefined>(undefined);
  const [pendingDelete, setPendingDelete] = useState<ScheduledTask | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  function load() {
    const controller = new AbortController();
    client
      .request<ScheduledTask[]>("scheduler.list", undefined, controller.signal)
      .then((v) => setTasks(v ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    return () => controller.abort();
  }

  useEffect(load, [client]);

  function openCreate() {
    setEditing(undefined);
    setFormOpen(true);
  }

  function openEdit(task: ScheduledTask) {
    setEditing(task);
    setFormOpen(true);
  }

  async function handleSubmit(input: ScheduleInput) {
    if (editing) {
      await client.request("scheduler.update", { id: editing.id, ...input });
    } else {
      await client.request("scheduler.create", input);
    }
    setFormOpen(false);
    load();
  }

  async function toggleEnabled(task: ScheduledTask) {
    setBusyId(task.id);
    try {
      await client.request("scheduler.set_enabled", { id: task.id, enabled: !task.enabled });
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusyId(null);
    }
  }

  async function confirmDelete() {
    if (!pendingDelete) return;
    const task = pendingDelete;
    setPendingDelete(null);
    setBusyId(task.id);
    try {
      await client.request("scheduler.delete", { id: task.id });
      load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusyId(null);
    }
  }

  return (
    <Panel className="flex h-full flex-col">
      <PanelHeader>
        <h2 className="text-sm font-semibold">Schedules</h2>
        <Button variant="primary" onClick={openCreate}>
          New task
        </Button>
      </PanelHeader>

      {error && <p className="p-4 text-sm text-text-secondary">Couldn't load scheduled tasks: {error}</p>}
      {!error && !tasks && <p className="p-4 text-sm text-text-muted">Loading...</p>}

      {tasks && tasks.length === 0 && (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
          <p className="text-sm text-text-secondary">No scheduled tasks yet.</p>
          <p className="max-w-sm text-xs text-text-muted">
            Schedule a one-time or repeating task, then wire it to an automation rule's trigger on the Automation
            screen.
          </p>
          <Button variant="primary" className="mt-2" onClick={openCreate}>
            New task
          </Button>
        </div>
      )}

      {tasks && tasks.length > 0 && (
        <div className="min-h-0 flex-1 overflow-y-auto">
          <table className="w-full text-left text-sm">
            <thead className="sticky top-0 bg-surface-1">
              <tr className="border-b border-border text-xs text-text-muted">
                <th className="px-4 py-2 font-medium">Name</th>
                <th className="px-4 py-2 font-medium">Type</th>
                <th className="px-4 py-2 font-medium">Next run</th>
                <th className="px-4 py-2 font-medium">Last run</th>
                <th className="px-4 py-2 text-right font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {tasks.map((t) => (
                <tr key={t.id} className="border-b border-border last:border-0">
                  <td className="px-4 py-2 text-text-primary">{t.name}</td>
                  <td className="px-4 py-2 text-text-secondary">{t.type === "once" ? "Once" : "Repeating"}</td>
                  <td className="px-4 py-2 text-text-secondary">
                    {t.enabled ? new Date(t.next_run_at).toLocaleString() : "paused"}
                  </td>
                  <td className="px-4 py-2 text-text-secondary">
                    {t.last_run_at ? new Date(t.last_run_at).toLocaleString() : "never"}
                  </td>
                  <td className="px-4 py-2">
                    <div className="flex justify-end gap-2">
                      <Button variant="ghost" onClick={() => toggleEnabled(t)} disabled={busyId === t.id}>
                        {t.enabled ? "Pause" : "Resume"}
                      </Button>
                      <Button variant="ghost" onClick={() => openEdit(t)} disabled={busyId === t.id}>
                        Edit
                      </Button>
                      <Button variant="danger" onClick={() => setPendingDelete(t)} disabled={busyId === t.id}>
                        Delete
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <ScheduleFormDialog
        open={formOpen}
        existing={editing}
        onCancel={() => setFormOpen(false)}
        onSubmit={handleSubmit}
      />

      <ConfirmDialog
        open={!!pendingDelete}
        title={`Delete "${pendingDelete?.name}"?`}
        description="This stops the task immediately and removes it. This can't be undone. If an automation rule's trigger references it, that rule will simply stop firing."
        confirmLabel="Delete"
        destructive
        onConfirm={confirmDelete}
        onCancel={() => setPendingDelete(null)}
      />
    </Panel>
  );
}
