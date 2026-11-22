import { useEffect, useMemo, useRef, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { useEventStream } from "@/hooks/useEventStream";
import { useSort, SortableTh } from "@/hooks/useSort";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import type { EventSeverity, WelouryEvent } from "@/types/observability";

const SEVERITY_CLASS: Record<EventSeverity, string> = {
  info: "text-text-secondary",
  warning: "text-status-degraded",
  critical: "text-status-unhealthy",
};

/**
 * Browses Weloury's persisted event history (spec section 10: every
 * subsystem - process, service, health, ...- publishes onto one bus,
 * which internal/app durably records via its event-persistence bridge).
 * events.recent serves "what happened before I opened this screen";
 * new events arrive live over the same stream every other feature uses
 * and are prepended without needing a re-fetch, since an Event is
 * already the full record - no follow-up query needed the way a health
 * check's state transition benefits from one (see HealthPage).
 */
export function EventsPage({ client }: { client: FwpClient }) {
  const [events, setEvents] = useState<WelouryEvent[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const { events: streamed } = useEventStream<Omit<WelouryEvent, "payload"> & { payload?: unknown }>(client);
  const seenIds = useRef<Set<string>>(new Set());

  useEffect(() => {
    const controller = new AbortController();
    client
      .request<WelouryEvent[]>("events.recent", { limit: 200 }, controller.signal)
      .then((v) => {
        const list = v ?? [];
        seenIds.current = new Set(list.map((e) => e.id));
        setEvents(list);
      })
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    return () => controller.abort();
  }, [client]);

  useEffect(() => {
    if (streamed.length === 0) return;
    setEvents((prev) => {
      if (!prev) return prev;
      const additions: WelouryEvent[] = [];
      for (const s of streamed) {
        const payload = s.payload as WelouryEvent | undefined;
        if (!payload?.id || seenIds.current.has(payload.id)) continue;
        seenIds.current.add(payload.id);
        additions.push(payload);
      }
      if (additions.length === 0) return prev;
      return [...additions.reverse(), ...prev];
    });
  }, [streamed]);

  const filtered = useMemo(() => {
    if (!events) return [];
    const q = search.trim().toLowerCase();
    if (!q) return events;
    return events.filter((e) => e.type.toLowerCase().includes(q) || e.source.toLowerCase().includes(q));
  }, [events, search]);

  type SortKey = "timestamp" | "type" | "source";
  const sortExtractors = useMemo(
    () => ({
      timestamp: (e: WelouryEvent) => new Date(e.timestamp).getTime(),
      type: (e: WelouryEvent) => e.type.toLowerCase(),
      source: (e: WelouryEvent) => e.source.toLowerCase(),
    }),
    [],
  );
  // Default sort: newest first - the natural reading order for an
  // event history, and what the prepends already assume.
  const { sorted, sort, toggleSort } = useSort<SortKey, WelouryEvent>(filtered, sortExtractors, "timestamp", "desc");

  return (
    <Panel className="flex h-full flex-col">
      <PanelHeader>
        <h2 className="text-sm font-semibold">Events</h2>
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="Filter by type or source..."
          className="w-56 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-xs text-text-primary placeholder:text-text-muted focus:outline-none"
        />
      </PanelHeader>

      {error && <p className="p-4 text-sm text-text-secondary">Couldn't load events: {error}</p>}
      {!error && !events && <p className="p-4 text-sm text-text-muted">Loading...</p>}

      {events && (
        <div className="min-h-0 flex-1 overflow-y-auto">
          {filtered.length === 0 && (
            <p className="p-4 text-center text-sm text-text-muted">
              {events.length === 0 ? "No events recorded yet." : "No events match your filter."}
            </p>
          )}
          <table className="w-full text-left text-sm">
            <thead className="sticky top-0 bg-surface-1">
              <tr className="border-b border-border text-xs text-text-muted">
                <SortableTh label="Time" sortKey="timestamp" sort={sort} onToggle={toggleSort} />
                <SortableTh label="Type" sortKey="type" sort={sort} onToggle={toggleSort} />
                <SortableTh label="Source" sortKey="source" sort={sort} onToggle={toggleSort} />
                <th className="px-4 py-2 font-medium">Payload</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((e) => (
                <tr key={e.id} className="border-b border-border last:border-0 align-top">
                  <td className="whitespace-nowrap px-4 py-2 text-xs text-text-muted">
                    {new Date(e.timestamp).toLocaleString()}
                  </td>
                  <td className={`px-2 py-2 font-mono text-xs ${SEVERITY_CLASS[e.severity]}`}>{e.type}</td>
                  <td className="px-2 py-2 text-xs text-text-secondary">{e.source}</td>
                  <td className="max-w-md truncate px-4 py-2 font-mono text-xs text-text-muted">
                    {e.payload ? JSON.stringify(e.payload) : ""}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="border-t border-border px-4 py-2 text-xs text-text-muted">
        {filtered.length} of {events?.length ?? 0} events
      </div>
    </Panel>
  );
}
