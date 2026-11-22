import { useEffect, useMemo, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { StatusDot } from "@/components/ui/StatusDot";
import { Button } from "@/components/ui/Button";
import { formatBytes } from "@/lib/format";
import { useSort, SortableTh } from "@/hooks/useSort";
import type { NetworkConnection, NetworkInterface } from "@/types/system";

/**
 * Network inspection (spec section 13) plus the live-throughput view the
 * spec's network section implies: per-interface upload/download rate
 * charts fed by the metrics engine's rolling network_rx/network_tx
 * series (5s samples, ~30min window - see internal/metrics), and the
 * full socket table with per-process aggregation. Not packet capture -
 * socket metadata and counter rates only, per the backend's contract.
 */

interface Sample {
  value: number;
  timestamp: string;
}

const REFRESH_MS = 5000;

export function NetworkPage({ client }: { client: FwpClient }) {
  const [interfaces, setInterfaces] = useState<NetworkInterface[] | null>(null);
  const [connections, setConnections] = useState<NetworkConnection[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [listeningOnly, setListeningOnly] = useState(false);
  const [tick, setTick] = useState(0);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    Promise.all([
      client.request<NetworkInterface[]>("system.network_interfaces", undefined, controller.signal),
      client.request<NetworkConnection[]>("system.network_connections", undefined, controller.signal),
    ])
      .then(([ifaces, conns]) => {
        if (cancelled) return;
        setInterfaces(ifaces ?? []);
        setConnections(conns ?? []);
      })
      .catch((err) => !cancelled && setError(err instanceof Error ? err.message : String(err)));
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [client, tick]);

  // The active interfaces' rolling throughput samples. metrics.current
  // tells us which series exist; each gets its own rolling query. One
  // extra round trip per interface but the datasets are tiny (≤360
  // points each) and the cadence is 5s.
  const [rxByIface, setRxByIface] = useState<Record<string, Sample[]>>({});
  const [txByIface, setTxByIface] = useState<Record<string, Sample[]>>({});

  useEffect(() => {
    if (!interfaces) return;
    let cancelled = false;
    const names = interfaces.filter((i) => i.is_up && !i.is_loopback).map((i) => i.name);
    void Promise.all(
      names.map(async (name) => {
        try {
          const [rx, tx] = await Promise.all([
            client.request<Sample[]>("metrics.rolling", { metric: "network_rx", scope: name }),
            client.request<Sample[]>("metrics.rolling", { metric: "network_tx", scope: name }),
          ]);
          return { name, rx, tx };
        } catch {
          return { name, rx: [] as Sample[], tx: [] as Sample[] };
        }
      }),
    ).then((rows) => {
      if (cancelled) return;
      const rx: Record<string, Sample[]> = {};
      const tx: Record<string, Sample[]> = {};
      for (const r of rows) {
        rx[r.name] = r.rx ?? [];
        tx[r.name] = r.tx ?? [];
      }
      setRxByIface(rx);
      setTxByIface(tx);
    });
    return () => {
      cancelled = true;
    };
  }, [client, interfaces, tick]);

  useEffect(() => {
    const id = setInterval(() => setTick((t) => t + 1), REFRESH_MS);
    return () => clearInterval(id);
  }, []);

  const filtered = useMemo(() => {
    if (!connections) return [];
    const q = search.trim().toLowerCase();
    return connections.filter((c) => {
      if (listeningOnly && !(c.state === "LISTEN" || c.protocol.startsWith("udp"))) return false;
      if (!q) return true;
      return (
        String(c.local_port).includes(q) ||
        c.local_address.toLowerCase().includes(q) ||
        (c.process_name ?? "").toLowerCase().includes(q) ||
        (c.remote_address ?? "").toLowerCase().includes(q)
      );
    });
  }, [connections, search, listeningOnly]);

  // Per-process aggregation of the (unfiltered) socket table - "what's
  // using the network" at a glance: owning processes sorted by socket
  // count, each expandable into its connections via the filter below.
  const byProcess = useMemo(() => {
    if (!connections) return [];
    const m = new Map<string, { name: string; pid: number; count: number; listening: number }>();
    for (const c of connections) {
      const key = `${c.process_name ?? "?"}:${c.pid ?? 0}`;
      const entry = m.get(key) ?? { name: c.process_name ?? "unknown", pid: c.pid ?? 0, count: 0, listening: 0 };
      entry.count += 1;
      if (c.state === "LISTEN") entry.listening += 1;
      m.set(key, entry);
    }
    return [...m.values()].sort((a, b) => b.count - a.count).slice(0, 12);
  }, [connections]);

  type SortKey = "protocol" | "local" | "remote" | "state" | "process";
  const sortExtractors = useMemo(
    () => ({
      protocol: (c: NetworkConnection) => c.protocol,
      local: (c: NetworkConnection) => `${c.local_address}:${c.local_port}`,
      remote: (c: NetworkConnection) => (c.remote_address ? `${c.remote_address}:${c.remote_port}` : ""),
      state: (c: NetworkConnection) => c.state ?? "",
      process: (c: NetworkConnection) => (c.process_name ?? "").toLowerCase(),
    }),
    [],
  );
  const { sorted, sort, toggleSort } = useSort<SortKey, NetworkConnection>(filtered, sortExtractors);

  return (
    <div className="flex h-full flex-col gap-4 overflow-y-auto pr-1">
      <ThroughputCharts interfaces={interfaces} rxByIface={rxByIface} txByIface={txByIface} />

      <Panel>
        <PanelHeader>
          <h2 className="text-sm font-semibold">Interfaces</h2>
          <Button variant="ghost" onClick={() => setTick((t) => t + 1)}>
            Refresh
          </Button>
        </PanelHeader>
        {error && <p className="p-4 text-sm text-text-secondary">Couldn't load network info: {error}</p>}
        {!error && !interfaces && <p className="p-4 text-sm text-text-muted">Loading...</p>}
        {interfaces && interfaces.length > 0 && (
          <div className="grid grid-cols-1 gap-3 p-4 sm:grid-cols-2 lg:grid-cols-3">
            {interfaces.map((iface) => (
              <div key={iface.name} className="rounded-md border border-border p-3">
                <div className="flex items-center gap-2">
                  <StatusDot status={iface.is_up ? "healthy" : "unknown"} label={iface.name} />
                  {iface.is_loopback && <span className="text-xs text-text-muted">loopback</span>}
                </div>
                <div className="mt-1 space-y-0.5 text-xs text-text-secondary">
                  {iface.addresses.map((a) => (
                    <div key={a} className="font-mono">
                      {a}
                    </div>
                  ))}
                  {iface.hardware_addr && <div className="text-text-muted">{iface.hardware_addr}</div>}
                </div>
              </div>
            ))}
          </div>
        )}
      </Panel>

      {byProcess.length > 0 && (
        <Panel>
          <PanelHeader>
            <h2 className="text-sm font-semibold">Sockets by process</h2>
            <span className="text-xs text-text-muted">top {byProcess.length} by open socket count</span>
          </PanelHeader>
          <div className="flex flex-wrap gap-2 p-4">
            {byProcess.map((p) => (
              <button
                key={`${p.name}:${p.pid}`}
                onClick={() => setSearch(p.name)}
                title="Click to filter the connection table to this process"
                className="rounded-md border border-border bg-surface-2 px-2.5 py-1.5 text-left text-xs hover:border-border-strong"
              >
                <span className="block font-medium text-text-primary">{p.name}</span>
                <span className="tabular block text-text-muted">
                  {p.count} socket{p.count === 1 ? "" : "s"}
                  {p.listening > 0 ? ` · ${p.listening} listening` : ""}
                </span>
              </button>
            ))}
          </div>
        </Panel>
      )}

      <Panel className="flex min-h-0 flex-col">
        <PanelHeader className="flex-wrap gap-2">
          <h2 className="text-sm font-semibold">Connections</h2>
          <div className="flex items-center gap-2">
            <input
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              placeholder="Filter by port, address, or process..."
              className="w-64 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-xs text-text-primary placeholder:text-text-muted focus:outline-none"
            />
            <label className="flex items-center gap-1.5 text-xs text-text-secondary">
              <input type="checkbox" checked={listeningOnly} onChange={(e) => setListeningOnly(e.target.checked)} />
              Listening only
            </label>
          </div>
        </PanelHeader>

        {connections && filtered.length === 0 && (
          <p className="p-4 text-center text-sm text-text-muted">
            {connections.length === 0 ? "No connections found." : "No connections match your filter."}
          </p>
        )}

        {connections && filtered.length > 0 && (
          <div className="max-h-[420px] overflow-y-auto">
            <table className="w-full text-left text-sm">
              <thead className="sticky top-0 bg-surface-1">
                <tr className="border-b border-border text-xs text-text-muted">
                  <SortableTh label="Protocol" sortKey="protocol" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Local" sortKey="local" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Remote" sortKey="remote" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="State" sortKey="state" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Process" sortKey="process" sort={sort} onToggle={toggleSort} />
                </tr>
              </thead>
              <tbody>
                {sorted.map((c, i) => (
                  <tr key={i} className="border-b border-border last:border-0">
                    <td className="px-4 py-1.5 font-mono text-xs text-text-secondary">{c.protocol}</td>
                    <td className="px-4 py-1.5 font-mono text-xs text-text-primary">
                      {c.local_address}:{c.local_port}
                    </td>
                    <td className="px-4 py-1.5 font-mono text-xs text-text-secondary">
                      {c.remote_address ? `${c.remote_address}:${c.remote_port}` : "-"}
                    </td>
                    <td className="px-4 py-1.5 text-xs text-text-secondary">{c.state || "-"}</td>
                    <td className="px-4 py-1.5 text-xs text-text-secondary">
                      {c.process_name ? `${c.process_name} (${c.pid})` : "-"}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <div className="border-t border-border px-4 py-2 text-xs text-text-muted">{filtered.length} shown</div>
          </div>
        )}
      </Panel>
    </div>
  );
}

/** A compact SVG area/line chart for a rate series, scaled to its own max. */
function RateChart({ rx, tx, label }: { rx: Sample[]; tx: Sample[]; label: string }) {
  const W = 260;
  const H = 64;
  const points = useMemo(() => {
    const n = Math.max(rx.length, tx.length);
    if (n === 0) return null;
    const max = Math.max(1, ...rx.map((s) => s.value), ...tx.map((s) => s.value));
    const toPath = (samples: Sample[]) =>
      samples
        .map((s, i) => {
          const x = (i / Math.max(1, n - 1)) * W;
          const y = H - (s.value / max) * (H - 6) - 2;
          return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
        })
        .join(" ");
    return { rxPath: toPath(rx), txPath: toPath(tx), max };
  }, [rx, tx]);

  const latestRx = rx.length ? rx[rx.length - 1].value : 0;
  const latestTx = tx.length ? tx[tx.length - 1].value : 0;

  return (
    <div className="rounded-md border border-border p-3">
      <div className="flex items-baseline justify-between">
        <p className="truncate text-xs font-medium text-text-primary">{label}</p>
        <p className="tabular text-xs text-text-muted">
          <span className="text-status-healthy">↓ {formatBytes(latestRx)}/s</span>{" "}
          <span className="text-status-degraded">↑ {formatBytes(latestTx)}/s</span>
        </p>
      </div>
      <svg viewBox={`0 0 ${W} ${H}`} className="mt-2 h-16 w-full" preserveAspectRatio="none" role="img" aria-label={`${label} throughput chart`}>
        {points ? (
          <>
            <path d={points.rxPath} fill="none" stroke="rgb(var(--status-healthy))" strokeWidth="1.5" />
            <path d={points.txPath} fill="none" stroke="rgb(var(--status-degraded))" strokeWidth="1.5" />
          </>
        ) : (
          <text x={W / 2} y={H / 2} textAnchor="middle" fill="rgb(var(--text-muted))" fontSize="10">
            collecting samples…
          </text>
        )}
      </svg>
      {points && <p className="tabular mt-1 text-[10px] text-text-muted">peak ↓ {formatBytes(points.max)}/s</p>}
    </div>
  );
}

function ThroughputCharts({
  interfaces,
  rxByIface,
  txByIface,
}: {
  interfaces: NetworkInterface[] | null;
  rxByIface: Record<string, Sample[]>;
  txByIface: Record<string, Sample[]>;
}) {
  const active = (interfaces ?? []).filter((i) => i.is_up && !i.is_loopback);
  return (
    <Panel>
      <PanelHeader>
        <h2 className="text-sm font-semibold">Live throughput</h2>
        <span className="text-xs text-text-muted">
          <span className="text-status-healthy">↓ download</span> · <span className="text-status-degraded">↑ upload</span> · 5s samples, last ~30 min
        </span>
      </PanelHeader>
      <div className="grid grid-cols-1 gap-3 p-4 md:grid-cols-2 xl:grid-cols-3">
        {active.map((iface) => (
          <RateChart key={iface.name} label={iface.name} rx={rxByIface[iface.name] ?? []} tx={txByIface[iface.name] ?? []} />
        ))}
        {active.length === 0 && <p className="text-sm text-text-muted">No non-loopback interface is currently up.</p>}
      </div>
    </Panel>
  );
}
