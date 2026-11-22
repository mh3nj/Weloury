import { useEffect, useMemo, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { StatusDot } from "@/components/ui/StatusDot";
import { formatBytes, formatPercent } from "@/lib/format";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";
import { useSort, SortableTh } from "@/hooks/useSort";
import { ContainerDetailPanel } from "./ContainerDetailPanel";
import type { Container, ContainerAvailability } from "@/types/containers";

/**
 * Container discovery (spec section 15). Weloury remains fully useful on a
 * machine without Docker installed or running - that's the expected,
 * common case, not an error state, so this page checks
 * containers.availability first and shows a plain explanation rather
 * than a broken-looking empty table when it's unavailable.
 */
export function ContainersPage({ client }: { client: FwpClient }) {
  const [availability, setAvailability] = useState<ContainerAvailability | null>(null);
  const [containers, setContainers] = useState<Container[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  function load() {
    const controller = new AbortController();
    client
      .request<ContainerAvailability>("containers.availability", undefined, controller.signal)
      .then((avail) => {
        setAvailability(avail);
        if (!avail.available) {
          setContainers([]);
          return undefined;
        }
        return client.request<Container[]>("containers.list", undefined, controller.signal).then((v) => {
          setContainers(v ?? []);
        });
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    return () => controller.abort();
  }

  useEffect(load, [client]);

  const selected = useMemo(() => containers?.find((c) => c.id === selectedId) ?? null, [containers, selectedId]);

  type SortKey = "name" | "image" | "state" | "cpu" | "memory";
  const sortExtractors = useMemo(
    () => ({
      name: (c: Container) => c.name.toLowerCase(),
      image: (c: Container) => c.image.toLowerCase(),
      state: (c: Container) => c.state,
      cpu: (c: Container) => c.cpu_percent ?? -1,
      memory: (c: Container) => c.memory_usage_bytes ?? -1,
    }),
    [],
  );
  const { sorted, sort, toggleSort } = useSort<SortKey, Container>(containers ?? [], sortExtractors, "name", "asc");

  return (
    <div className="flex h-full gap-4">
      <Panel className="flex min-w-0 flex-1 flex-col">
        <PanelHeader>
          <h2 className="text-sm font-semibold">Containers</h2>
          {containers && availability?.available && (
            <span className="text-xs text-text-muted">{containers.length} found</span>
          )}
        </PanelHeader>

        {error && <p className="p-4 text-sm text-text-secondary">Couldn't check container runtime: {error}</p>}

        {!error && availability && !availability.available && (
          <div className="flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
            <p className="text-sm text-text-secondary">Docker isn't available on this machine.</p>
            <p className="max-w-sm text-xs text-text-muted">{availability.reason}</p>
            <p className="max-w-sm text-xs text-text-muted">
              Weloury works fine without it - this screen will populate automatically once Docker is
              installed and running.
            </p>
          </div>
        )}

        {!error && availability?.available && !containers && <p className="p-4 text-sm text-text-muted">Loading...</p>}

        {!error && availability?.available && containers && containers.length === 0 && (
          <p className="p-4 text-sm text-text-muted">No containers found (Docker is running, but nothing's there).</p>
        )}

        {!error && availability?.available && containers && containers.length > 0 && (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <table className="w-full text-left text-sm">
              <thead className="sticky top-0 bg-surface-1">
                <tr className="border-b border-border text-xs text-text-muted">
                  <SortableTh label="Name" sortKey="name" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Image" sortKey="image" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Status" sortKey="state" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="CPU" sortKey="cpu" sort={sort} onToggle={toggleSort} align="right" />
                  <SortableTh label="Memory" sortKey="memory" sort={sort} onToggle={toggleSort} align="right" />
                </tr>
              </thead>
              <tbody>
                {sorted.map((c) => (
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
                    <td className="px-4 py-2 font-mono text-xs text-text-secondary">{c.image}</td>
                    <td className="px-4 py-2">
                      <StatusDot status={c.state === "running" ? "healthy" : "unknown"} label={c.status} />
                    </td>
                    <td className="tabular px-4 py-2 text-right text-text-secondary">
                      {c.cpu_percent != null ? formatPercent(c.cpu_percent) : "-"}
                    </td>
                    <td className="tabular px-4 py-2 text-right text-text-secondary">
                      {c.memory_usage_bytes != null ? formatBytes(c.memory_usage_bytes) : "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      {selected && (
        <ContainerDetailPanel
          client={client}
          container={selected}
          onClose={() => setSelectedId(null)}
          onChanged={load}
        />
      )}
    </div>
  );
}
