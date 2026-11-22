import { useEffect, useMemo, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { StatusDot } from "@/components/ui/StatusDot";
import { Button } from "@/components/ui/Button";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";
import { useSort, SortableTh } from "@/hooks/useSort";
import { ServiceDetailPanel } from "./ServiceDetailPanel";
import type { ServiceInfo } from "@/types/system";

const STATE_MAP: Record<string, "healthy" | "degraded" | "unhealthy" | "unknown"> = {
  running: "healthy",
  stopped: "unknown",
  failed: "unhealthy",
};

/**
 * Service discovery and management (spec section 7). Deliberately built
 * as a second, later pass after process management (see CHANGELOG) - the
 * three platforms' service models (systemd, launchd, the Windows SCM)
 * are genuinely different, most visibly here in StartupMode, which is
 * frequently "unknown" on macOS (see ServiceDetailPanel and
 * ARCHITECTURE.md for why) rather than something this page hides or
 * fakes.
 */
export function ServicesPage({ client }: { client: FwpClient }) {
  const [services, setServices] = useState<ServiceInfo[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [query, setQuery] = useState("");
  const [selectedName, setSelectedName] = useState<string | null>(null);
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    setError(null);
    client
      .request<ServiceInfo[]>("service.list", undefined, controller.signal)
      .then((v) => !cancelled && setServices(v ?? []))
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [client, refreshKey]);

  const filtered = useMemo(() => {
    if (!services) return [];
    const q = query.trim().toLowerCase();
    if (!q) return services;
    return services.filter(
      (s) => s.name.toLowerCase().includes(q) || s.display_name.toLowerCase().includes(q),
    );
  }, [services, query]);

  type SortKey = "name" | "state" | "startup" | "pid";
  const sortExtractors = useMemo(
    () => ({
      name: (s: ServiceInfo) => (s.display_name || s.name).toLowerCase(),
      state: (s: ServiceInfo) => s.state,
      startup: (s: ServiceInfo) => s.startup_mode,
      pid: (s: ServiceInfo) => s.pid,
    }),
    [],
  );
  const { sorted, sort, toggleSort } = useSort<SortKey, ServiceInfo>(filtered, sortExtractors, "name", "asc");

  function refresh() {
    setRefreshKey((k) => k + 1);
  }

  return (
    <div className="flex h-full gap-4">
      <Panel className="flex min-w-0 flex-1 flex-col">
        <PanelHeader>
          <h2 className="text-sm font-semibold">Services</h2>
          <div className="flex items-center gap-2">
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter by name..."
              className="w-56 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-xs text-text-primary placeholder:text-text-muted focus:outline-none"
            />
            <Button variant="ghost" onClick={refresh}>
              Refresh
            </Button>
          </div>
        </PanelHeader>

        {error && <p className="p-4 text-sm text-text-secondary">Couldn't load services: {error}</p>}
        {!error && !services && <p className="p-4 text-sm text-text-muted">Loading...</p>}

        {services && (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <table className="w-full text-left text-sm">
              <thead className="sticky top-0 bg-surface-1">
                <tr className="border-b border-border text-xs text-text-muted">
                  <SortableTh label="Name" sortKey="name" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="State" sortKey="state" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Startup" sortKey="startup" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="PID" sortKey="pid" sort={sort} onToggle={toggleSort} align="right" />
                </tr>
              </thead>
              <tbody>
                {sorted.map((s) => (
                  <tr
                    key={s.name}
                    tabIndex={0}
                    aria-selected={selectedName === s.name}
                    onClick={() => setSelectedName(s.name)}
                    onKeyDown={onActivateKey(() => setSelectedName(s.name))}
                    className={`cursor-pointer border-b border-border last:border-0 hover:bg-surface-2 ${FOCUS_RING} ${
                      selectedName === s.name ? "bg-accent/15" : ""
                    }`}
                  >
                    <td className="px-4 py-2">
                      <div className="text-text-primary">{s.display_name || s.name}</div>
                      {s.display_name && s.display_name !== s.name && (
                        <div className="text-xs text-text-muted">{s.name}</div>
                      )}
                    </td>
                    <td className="px-4 py-2">
                      <StatusDot status={STATE_MAP[s.state] ?? "unknown"} label={s.state || "unknown"} />
                    </td>
                    <td className="px-4 py-2 text-text-secondary">{s.startup_mode}</td>
                    <td className="tabular px-4 py-2 text-right text-text-secondary">{s.pid > 0 ? s.pid : "-"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {filtered.length === 0 && (
              <p className="p-4 text-center text-sm text-text-muted">
                {services.length === 0 ? "No services reported." : "No services match your filter."}
              </p>
            )}
            <div className="border-t border-border px-4 py-2 text-xs text-text-muted">
              {filtered.length} of {services.length} services
            </div>
          </div>
        )}
      </Panel>

      {selectedName !== null && (
        <ServiceDetailPanel
          client={client}
          name={selectedName}
          onClose={() => setSelectedName(null)}
          onChanged={refresh}
        />
      )}
    </div>
  );
}
