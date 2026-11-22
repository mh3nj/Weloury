import { useEffect, useMemo, useRef, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { StatusDot } from "@/components/ui/StatusDot";
import { Button } from "@/components/ui/Button";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";
import { useVirtualRows } from "@/hooks/useVirtualRows";
import { useSort, SortableTh, type SortState } from "@/hooks/useSort";
import { formatBytes, formatBytesPerSec, formatPercent } from "@/lib/format";
import { ProcessDetailPanel } from "./ProcessDetailPanel";
import { ProcessTree } from "./ProcessTree";
import type { ProcessInfo, ProcessTreeNode } from "@/types/system";

const STATUS_MAP: Record<string, "healthy" | "degraded" | "unhealthy" | "unknown"> = {
  running: "healthy",
  sleeping: "unknown",
  idle: "unknown",
  disk_sleep: "degraded",
  stopped: "degraded",
  zombie: "unhealthy",
  dead: "unhealthy",
};

type ViewMode = "list" | "tree";

type SortKey = "pid" | "name" | "user" | "cpu" | "memory" | "disk";

/**
 * Process discovery and management. List view is virtualized (see
 * hooks/useVirtualRows.ts) and sorted client-side - the full filtered
 * set is what gets sorted, so a column sort stays correct as the user
 * scrolls and the virtual window slides. CPU and Disk columns carry the
 * task-manager-style per-process telemetry the backend now includes in
 * the bulk listing (null on platforms that don't measure - rendered as
 * "-"). Selecting a row opens ProcessDetailPanel, the only place a
 * destructive action (terminate/force kill) can be triggered, and only
 * after ConfirmDialog.
 */
export function ProcessesPage({ client }: { client: FwpClient }) {
  const [viewMode, setViewMode] = useState<ViewMode>("list");
  const [processes, setProcesses] = useState<ProcessInfo[] | null>(null);
  const [treeRoots, setTreeRoots] = useState<ProcessTreeNode[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [selectedPid, setSelectedPid] = useState<number | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    setError(null);

    const method = viewMode === "tree" ? "process.tree" : "process.list";
    client
      .request<ProcessInfo[] | ProcessTreeNode[]>(method, undefined, controller.signal)
      .then((v) => {
        if (cancelled) return;
        if (viewMode === "tree") {
          setTreeRoots((v as ProcessTreeNode[]) ?? []);
        } else {
          setProcesses((v as ProcessInfo[]) ?? []);
        }
      })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)));

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [client, viewMode, refreshKey]);

  const filtered = useMemo(() => {
    if (!processes) return [];
    const q = query.trim().toLowerCase();
    if (!q) return processes;
    return processes.filter(
      (p) => p.name.toLowerCase().includes(q) || String(p.pid).includes(q) || p.user.toLowerCase().includes(q),
    );
  }, [processes, query]);

  const sortExtractors = useMemo(
    () => ({
      pid: (p: ProcessInfo) => p.pid,
      name: (p: ProcessInfo) => p.name.toLowerCase(),
      user: (p: ProcessInfo) => p.user.toLowerCase(),
      cpu: (p: ProcessInfo) => p.cpu_percent ?? -1,
      memory: (p: ProcessInfo) => p.memory_bytes,
      disk: (p: ProcessInfo) => p.disk_io_bytes_per_sec ?? -1,
    }),
    [],
  );
  const { sorted, sort, toggleSort } = useSort<SortKey, ProcessInfo>(filtered, sortExtractors, "memory", "desc");

  function refresh() {
    setRefreshKey((k) => k + 1);
  }

  return (
    <div className="flex h-full gap-4">
      <Panel className="flex min-w-0 flex-1 flex-col">
        <PanelHeader>
          <div className="flex items-center gap-3">
            <h2 className="text-sm font-semibold">Processes</h2>
            <div className="flex overflow-hidden rounded-md border border-border text-xs">
              <button
                onClick={() => setViewMode("list")}
                className={`px-2.5 py-1 ${viewMode === "list" ? "bg-surface-2 text-text-primary" : "text-text-muted"}`}
              >
                List
              </button>
              <button
                onClick={() => setViewMode("tree")}
                className={`px-2.5 py-1 ${viewMode === "tree" ? "bg-surface-2 text-text-primary" : "text-text-muted"}`}
              >
                Tree
              </button>
            </div>
          </div>
          <div className="flex items-center gap-2">
            {viewMode === "list" && (
              <input
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="Filter by name, PID, or user..."
                className="w-56 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-xs text-text-primary placeholder:text-text-muted focus:outline-none"
              />
            )}
            <Button variant="ghost" onClick={refresh}>
              Refresh
            </Button>
          </div>
        </PanelHeader>

        {error && <p className="p-4 text-sm text-text-secondary">Couldn't load processes: {error}</p>}

        {viewMode === "list" ? (
          <ListView processes={processes} filtered={sorted} sort={sort} onToggle={toggleSort} onSelect={setSelectedPid} selectedPid={selectedPid ?? undefined} />
        ) : (
          <div className="min-h-0 flex-1 overflow-y-auto">
            {!treeRoots && !error && <p className="p-4 text-sm text-text-muted">Loading...</p>}
            {treeRoots && (
              <ProcessTree roots={treeRoots} onSelect={setSelectedPid} selectedPid={selectedPid ?? undefined} />
            )}
          </div>
        )}
      </Panel>

      {selectedPid !== null && (
        <ProcessDetailPanel
          client={client}
          pid={selectedPid}
          onClose={() => setSelectedPid(null)}
          onTerminated={refresh}
        />
      )}
    </div>
  );
}

// Measured against the real compiled table markup (px-4 py-2 cells,
// text-sm line-height, 1px row border) - see CHANGELOG.md's Phase 7
// entry. Must be kept in sync if that row's padding/line-height/border
// classes change; a mismatch here doesn't break rendering, it just
// makes the spacer height slightly wrong, which mostly self-corrects
// as the user scrolls (each scroll event recomputes the visible slice
// from real scrollTop, not from an assumed cumulative offset).
const ROW_HEIGHT_PX = 35;

function ListView({
  processes,
  filtered,
  sort,
  onToggle,
  onSelect,
  selectedPid,
}: {
  processes: ProcessInfo[] | null;
  filtered: ProcessInfo[];
  sort: SortState<SortKey>;
  onToggle: (key: SortKey) => void;
  onSelect: (pid: number) => void;
  selectedPid?: number;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const { startIndex, endIndex, topSpacerPx, bottomSpacerPx } = useVirtualRows(
    containerRef,
    filtered.length,
    ROW_HEIGHT_PX,
  );

  if (!processes) {
    return <p className="p-4 text-sm text-text-muted">Loading...</p>;
  }

  const visible = filtered.slice(startIndex, endIndex);

  return (
    <div ref={containerRef} className="min-h-0 flex-1 overflow-y-auto">
      <table className="w-full text-left text-sm">
        <thead className="sticky top-0 bg-surface-1">
          <tr className="border-b border-border text-xs text-text-muted">
            <SortableTh label="PID" sortKey="pid" sort={sort} onToggle={onToggle} />
            <SortableTh label="Name" sortKey="name" sort={sort} onToggle={onToggle} />
            <SortableTh label="User" sortKey="user" sort={sort} onToggle={onToggle} />
            <SortableTh label="CPU" sortKey="cpu" sort={sort} onToggle={onToggle} align="right" />
            <SortableTh label="Memory" sortKey="memory" sort={sort} onToggle={onToggle} align="right" />
            <SortableTh label="Disk" sortKey="disk" sort={sort} onToggle={onToggle} align="right" />
            <th className="px-4 py-2 font-medium">Status</th>
          </tr>
        </thead>
        <tbody>
          {topSpacerPx > 0 && (
            <tr aria-hidden="true" style={{ height: topSpacerPx }}>
              <td colSpan={7} />
            </tr>
          )}
          {visible.map((p) => (
            <tr
              key={p.pid}
              tabIndex={0}
              aria-selected={selectedPid === p.pid}
              onClick={() => onSelect(p.pid)}
              onKeyDown={onActivateKey(() => onSelect(p.pid))}
              className={`cursor-pointer border-b border-border last:border-0 hover:bg-surface-2 ${FOCUS_RING} ${
                selectedPid === p.pid ? "bg-accent/15" : ""
              }`}
            >
              <td className="tabular px-4 py-2 text-text-secondary">{p.pid}</td>
              <td className="px-4 py-2 text-text-primary">{p.name || "-"}</td>
              <td className="px-4 py-2 text-text-secondary">{p.user || "-"}</td>
              <td className="tabular px-4 py-2 text-right text-text-secondary">
                {p.cpu_percent != null ? formatPercent(p.cpu_percent) : "-"}
              </td>
              <td className="tabular px-4 py-2 text-right text-text-secondary">
                {p.memory_bytes ? formatBytes(p.memory_bytes) : "-"}
              </td>
              <td className="tabular px-4 py-2 text-right text-text-secondary">
                {p.disk_io_bytes_per_sec != null ? formatBytesPerSec(p.disk_io_bytes_per_sec) : "-"}
              </td>
              <td className="px-4 py-2">
                <StatusDot status={STATUS_MAP[p.status] ?? "unknown"} label={p.status || "unknown"} />
              </td>
            </tr>
          ))}
          {bottomSpacerPx > 0 && (
            <tr aria-hidden="true" style={{ height: bottomSpacerPx }}>
              <td colSpan={7} />
            </tr>
          )}
        </tbody>
      </table>
      {filtered.length === 0 && (
        <p className="p-4 text-center text-sm text-text-muted">
          {processes.length === 0 ? "No processes reported." : "No processes match your filter."}
        </p>
      )}
      <div className="border-t border-border px-4 py-2 text-xs text-text-muted">
        {filtered.length} of {processes.length} processes
      </div>
    </div>
  );
}
