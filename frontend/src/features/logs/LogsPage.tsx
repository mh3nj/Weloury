import { useEffect, useRef, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { useEventStream } from "@/hooks/useEventStream";
import { useVirtualRows } from "@/hooks/useVirtualRows";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";
import type { LogEntry, LogLevel } from "@/types/observability";

const LEVELS: LogLevel[] = ["DEBUG", "INFO", "WARN", "ERROR"];

// Matches the backend's own DefaultCapacity (internal/logs/logs.go) -
// the backend itself doesn't remember more than this, so holding more
// on the frontend wouldn't represent anything the backend could still
// back up with "load older" pagination anyway. Applied to both the
// live "Follow newest" list and the paused-entries buffer below -
// spec section 8/26: log streaming must not accumulate unlimited
// memory. Before this fix, neither buffer was capped: a long-running
// session with "Follow newest" on, or a long pause under heavy log
// volume, grew both without bound.
const MAX_DISPLAYED_ENTRIES = 5000;

// Measured against the real compiled row markup (font-mono text-xs,
// px-4 py-1, 1px border) - see the process-list row height note in
// ProcessesPage.tsx for why this is measured rather than guessed.
const ROW_HEIGHT_PX = 25;

const LEVEL_CLASS: Record<LogLevel, string> = {
  DEBUG: "text-text-muted",
  INFO: "text-text-secondary",
  WARN: "text-status-degraded",
  ERROR: "text-status-unhealthy",
};

/**
 * Weloury's own application log (spec section 8). Scope note: this shows
 * Weloury core's own structured log output, not the OS system log or
 * arbitrary services' logs - see backend/internal/logs's package doc
 * comment for why that's a deliberate Phase 4 boundary, not an
 * oversight.
 *
 * Backlog comes from logs.query (paginated, newest first); new entries
 * arrive live over the event stream and are prepended when "Follow
 * newest" is on. Pause stops the live feed from being *applied* to the
 * visible list without closing the underlying subscription - resuming
 * flushes whatever arrived while paused, so nothing is silently missed,
 * only deferred.
 */
export function LogsPage({ client }: { client: FwpClient }) {
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [level, setLevel] = useState<LogLevel | "">("");
  const [search, setSearch] = useState("");
  const [paused, setPaused] = useState(false);
  const [follow, setFollow] = useState(true);
  const pendingRef = useRef<LogEntry[]>([]);
  const [pendingCount, setPendingCount] = useState(0);
  const containerRef = useRef<HTMLDivElement>(null);

  const { events: streamed } = useEventStream<LogEntry>(client, ["log.entry"]);
  const lastSeenSeq = useRef(0);

  // Query the backlog whenever a filter changes. Live streaming applies
  // its own client-side level/search filtering below rather than
  // resubscribing per filter change, since the subscription itself has
  // no server-side filter parameter (see backend's registerLogsMethods
  // doc comment: streaming is channel-only, filtering is logs.query's
  // job) and reopening the stream on every keystroke would be wasteful.
  useEffect(() => {
    setLoading(true);
    setError(null);
    const controller = new AbortController();
    client
      .request<LogEntry[]>("logs.query", { level: level || undefined, search: search || undefined }, controller.signal)
      .then((v) => {
        setEntries(v ?? []);
        setLoading(false);
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : String(err));
        setLoading(false);
      });
    return () => controller.abort();
  }, [client, level, search]);

  // Apply newly streamed entries (deduped by seq, since useEventStream's
  // buffer and a fresh logs.query backlog can overlap on reconnect).
  useEffect(() => {
    const fresh = streamed
      .map((s) => s.payload)
      .filter((e): e is LogEntry => !!e && e.seq > lastSeenSeq.current);
    if (fresh.length === 0) return;
    lastSeenSeq.current = Math.max(lastSeenSeq.current, ...fresh.map((e) => e.seq));

    const matching = fresh.filter(matchesFilter(level, search));
    if (matching.length === 0) return;

    if (paused) {
      pendingRef.current = [...matching, ...pendingRef.current].slice(0, MAX_DISPLAYED_ENTRIES);
      setPendingCount(pendingRef.current.length);
    } else if (follow) {
      setEntries((prev) => [...[...matching].reverse(), ...prev].slice(0, MAX_DISPLAYED_ENTRIES));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [streamed]);

  function resume() {
    setPaused(false);
    if (pendingRef.current.length > 0) {
      setEntries((prev) => [...pendingRef.current, ...prev].slice(0, MAX_DISPLAYED_ENTRIES));
      pendingRef.current = [];
      setPendingCount(0);
    }
  }

  function copyEntry(entry: LogEntry) {
    const line = `${entry.timestamp} [${entry.level}] ${entry.message}`;
    void navigator.clipboard?.writeText(line);
  }

  function exportVisible() {
    const text = entries
      .slice()
      .reverse()
      .map((e) => `${e.timestamp} [${e.level}] ${e.message}`)
      .join("\n");
    const blob = new Blob([text], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `weloury-logs-${new Date().toISOString().replace(/[:.]/g, "-")}.txt`;
    a.click();
    URL.revokeObjectURL(url);
  }

  const filteredCount = entries.length;
  const { startIndex, endIndex, topSpacerPx, bottomSpacerPx } = useVirtualRows(
    containerRef,
    entries.length,
    ROW_HEIGHT_PX,
  );
  const visibleEntries = entries.slice(startIndex, endIndex);

  return (
    <Panel className="flex h-full flex-col">
      <PanelHeader className="flex-wrap gap-2">
        <h2 className="text-sm font-semibold">Logs</h2>
        <div className="flex flex-wrap items-center gap-2">
          <select
            value={level}
            onChange={(e) => setLevel(e.target.value as LogLevel | "")}
            className="rounded-md border border-border bg-surface-2 px-2 py-1 text-xs text-text-primary focus:outline-none"
          >
            <option value="">All levels</option>
            {LEVELS.map((l) => (
              <option key={l} value={l}>
                {l}+
              </option>
            ))}
          </select>
          <input
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="Search message..."
            className="w-48 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-xs text-text-primary placeholder:text-text-muted focus:outline-none"
          />
          <Button variant="ghost" onClick={() => setFollow((f) => !f)}>
            {follow ? "Following" : "Follow newest"}
          </Button>
          <Button variant={paused ? "primary" : "ghost"} onClick={() => (paused ? resume() : setPaused(true))}>
            {paused ? `Resume${pendingCount > 0 ? ` (${pendingCount})` : ""}` : "Pause"}
          </Button>
          <Button variant="ghost" onClick={exportVisible} disabled={entries.length === 0}>
            Export
          </Button>
        </div>
      </PanelHeader>

      {error && <p className="p-4 text-sm text-text-secondary">Couldn't load logs: {error}</p>}
      {!error && loading && <p className="p-4 text-sm text-text-muted">Loading...</p>}

      {!error && !loading && (
        <div ref={containerRef} className="min-h-0 flex-1 overflow-y-auto font-mono text-xs">
          {entries.length === 0 && (
            <p className="p-4 text-center text-text-muted">
              {level || search ? "No log entries match your filters." : "No log entries yet."}
            </p>
          )}
          {topSpacerPx > 0 && <div style={{ height: topSpacerPx }} aria-hidden="true" />}
          {visibleEntries.map((entry) => (
            <div
              key={entry.seq}
              role="button"
              tabIndex={0}
              aria-label={`Copy log entry: ${entry.level} ${entry.message}`}
              onClick={() => copyEntry(entry)}
              onKeyDown={onActivateKey(() => copyEntry(entry))}
              title="Click to copy"
              className={`flex cursor-pointer gap-3 border-b border-border px-4 py-1 hover:bg-surface-2 ${FOCUS_RING}`}
            >
              <span className="shrink-0 text-text-muted">{new Date(entry.timestamp).toLocaleTimeString()}</span>
              <span className={`w-12 shrink-0 font-semibold ${LEVEL_CLASS[entry.level]}`}>{entry.level}</span>
              <span className="min-w-0 flex-1 truncate text-text-primary">{entry.message}</span>
            </div>
          ))}
          {bottomSpacerPx > 0 && <div style={{ height: bottomSpacerPx }} aria-hidden="true" />}
        </div>
      )}

      <div className="border-t border-border px-4 py-2 text-xs text-text-muted">{filteredCount} entries shown</div>
    </Panel>
  );
}

function matchesFilter(level: LogLevel | "", search: string) {
  const q = search.trim().toLowerCase();
  const minRank = level ? LEVEL_RANK[level] : -Infinity;
  return (entry: LogEntry) => {
    if (LEVEL_RANK[entry.level] < minRank) return false;
    if (q && !entry.message.toLowerCase().includes(q)) return false;
    return true;
  };
}

const LEVEL_RANK: Record<LogLevel, number> = { DEBUG: -4, INFO: 0, WARN: 4, ERROR: 8 };
