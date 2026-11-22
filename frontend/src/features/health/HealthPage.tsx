import { useEffect, useMemo, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { useEventStream } from "@/hooks/useEventStream";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { StatusDot } from "@/components/ui/StatusDot";
import { Button } from "@/components/ui/Button";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";
import { HealthCheckFormDialog } from "./HealthCheckFormDialog";
import { HealthCheckDetailPanel } from "./HealthCheckDetailPanel";
import { CHECK_TYPE_META } from "./checkTypeMeta";
import type { HealthCheckInput, HealthCheckStatus } from "@/types/observability";

const STATE_MAP: Record<string, "healthy" | "degraded" | "unhealthy" | "unknown"> = {
  HEALTHY: "healthy",
  DEGRADED: "degraded",
  UNHEALTHY: "unhealthy",
  UNKNOWN: "unknown",
};

/**
 * Health check engine (spec section 9). Live state transitions arrive
 * over the event stream (health.check_passed/health.check_failed -
 * see backend/internal/health/events.go) and trigger a re-fetch rather
 * than trying to reconcile individual fields from the event payload:
 * List() is cheap (small, in-memory-backed) and a full refresh can never
 * drift out of sync with the backend's actual state the way incremental
 * patching could.
 */
export function HealthPage({ client }: { client: FwpClient }) {
  const [checks, setChecks] = useState<HealthCheckStatus[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<HealthCheckStatus | undefined>(undefined);
  const { events } = useEventStream(client, [
    "health.check_passed",
    "health.check_failed",
    "health.disk_threshold_exceeded",
    "health.memory_threshold_exceeded",
  ]);
  const lastEventCount = events.length;

  function load() {
    const controller = new AbortController();
    client
      .request<HealthCheckStatus[]>("health.list", undefined, controller.signal)
      .then((v) => setChecks(v ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    return () => controller.abort();
  }

  useEffect(load, [client]);
  // Re-fetch whenever a new transition event arrives - see doc comment above.
  useEffect(() => {
    if (lastEventCount === 0) return;
    return load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lastEventCount]);

  const selected = useMemo(() => checks?.find((c) => c.id === selectedId) ?? null, [checks, selectedId]);

  function openCreate() {
    setEditing(undefined);
    setFormOpen(true);
  }

  function openEdit(check: HealthCheckStatus) {
    setEditing(check);
    setFormOpen(true);
  }

  async function handleSubmit(input: HealthCheckInput) {
    if (editing) {
      await client.request("health.update", { id: editing.id, ...input });
    } else {
      await client.request("health.create", input);
    }
    setFormOpen(false);
    load();
  }

  return (
    <div className="flex h-full gap-4">
      <Panel className="flex min-w-0 flex-1 flex-col">
        <PanelHeader>
          <h2 className="text-sm font-semibold">Health</h2>
          <Button variant="primary" onClick={openCreate}>
            New check
          </Button>
        </PanelHeader>

        {error && <p className="p-4 text-sm text-text-secondary">Couldn't load health checks: {error}</p>}
        {!error && !checks && <p className="p-4 text-sm text-text-muted">Loading...</p>}

        {checks && checks.length === 0 && (
          <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
            <p className="text-sm text-text-secondary">No health checks configured yet.</p>
            <p className="max-w-sm text-xs text-text-muted">
              Watch a process, a service, a port, an endpoint, a command, or a resource threshold - and get
              notified the moment it stops looking healthy.
            </p>
            <Button variant="primary" className="mt-2" onClick={openCreate}>
              New check
            </Button>
          </div>
        )}

        {checks && checks.length > 0 && (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <table className="w-full text-left text-sm">
              <thead className="sticky top-0 bg-surface-1">
                <tr className="border-b border-border text-xs text-text-muted">
                  <th className="px-4 py-2 font-medium">Name</th>
                  <th className="px-4 py-2 font-medium">Type</th>
                  <th className="px-4 py-2 font-medium">State</th>
                  <th className="px-4 py-2 text-right font-medium">Failures</th>
                </tr>
              </thead>
              <tbody>
                {checks.map((c) => (
                  <tr
                    key={c.id}
                    tabIndex={0}
                    aria-selected={selectedId === c.id}
                    onClick={() => setSelectedId(c.id)}
                    onKeyDown={onActivateKey(() => setSelectedId(c.id))}
                    className={`cursor-pointer border-b border-border last:border-0 hover:bg-surface-2 ${FOCUS_RING} ${
                      selectedId === c.id ? "bg-accent/15" : ""
                    }`}
                  >
                    <td className="px-4 py-2 text-text-primary">{c.name}</td>
                    <td className="px-4 py-2 text-text-secondary">{CHECK_TYPE_META[c.type].label}</td>
                    <td className="px-4 py-2">
                      <StatusDot status={STATE_MAP[c.state] ?? "unknown"} label={c.state} />
                    </td>
                    <td className="tabular px-4 py-2 text-right text-text-secondary">
                      {c.failure_count > 0 ? c.failure_count : "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {selected && (
        <HealthCheckDetailPanel
          client={client}
          check={selected}
          onClose={() => setSelectedId(null)}
          onEdit={() => openEdit(selected)}
          onChanged={load}
        />
      )}

      <HealthCheckFormDialog
        open={formOpen}
        existing={editing}
        onCancel={() => setFormOpen(false)}
        onSubmit={handleSubmit}
      />
    </div>
  );
}
