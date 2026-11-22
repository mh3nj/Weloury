import { useCallback, useEffect, useMemo, useState } from "react";
import type { FwpClient } from "@/lib/ipc/client";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import { ConfirmDialog } from "@/components/ui/ConfirmDialog";
import { FOCUS_RING, onActivateKey } from "@/lib/a11y";
import { useSort, SortableTh } from "@/hooks/useSort";
import type { StartupEntry } from "@/types/system";

const LOCATION_LABEL: Record<string, string> = {
  "registry-run": "Registry (Run key)",
  "startup-folder": "Startup folder",
  "xdg-autostart": "XDG autostart",
  "systemd-user": "systemd (user)",
  "launch-agent": "LaunchAgent",
  "launch-daemon": "LaunchDaemon",
};

type SortKey = "name" | "location" | "scope" | "enabled";

type PendingRemove = StartupEntry | null;

/**
 * The Startup manager: every OS startup location the platform exposes,
 * with enable/disable (in place - Task Manager's own mechanism, i.e.
 * the StartupApproved registry blob on Windows, launchctl overrides on
 * macOS, the Hidden flag / systemctl on Linux), removal, and adding
 * custom entries that launch any executable at login. system-scope
 * entries (HKLM, /Library, /etc/xdg) are listed but not toggleable -
 * acting on them needs elevation and the backend refuses with an error
 * rather than self-elevating (SECURITY.md).
 */
export function StartupPage({ client }: { client: FwpClient }) {
  const [entries, setEntries] = useState<StartupEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [pendingRemove, setPendingRemove] = useState<PendingRemove>(null);
  const [showAdd, setShowAdd] = useState(false);

  // Add-custom-entry form state: name + exe are required; args is a
  // free-text field split on whitespace (quotable paths win over a
  // clever parser here - the OS command line is the truth anyway).
  const [newName, setNewName] = useState("");
  const [newExe, setNewExe] = useState("");
  const [newArgs, setNewArgs] = useState("");

  const load = useCallback(() => {
    setError(null);
    const controller = new AbortController();
    client
      .request<StartupEntry[]>("startup.list", undefined, controller.signal)
      .then((v) => setEntries(v ?? []))
      .catch((err) => setError(err instanceof Error ? err.message : String(err)));
    return () => controller.abort();
  }, [client]);

  useEffect(() => load(), [client, load]);

  const sortExtractors = useMemo(
    () => ({
      name: (e: StartupEntry) => e.name.toLowerCase(),
      location: (e: StartupEntry) => LOCATION_LABEL[e.location] ?? e.location,
      scope: (e: StartupEntry) => e.scope,
      enabled: (e: StartupEntry) => (e.enabled ? 0 : 1),
    }),
    [],
  );
  const { sorted, sort, toggleSort } = useSort<SortKey, StartupEntry>(
    entries ?? [],
    sortExtractors,
    "name",
    "asc",
  );

  async function toggle(entry: StartupEntry) {
    setBusyId(entry.id);
    setActionError(null);
    try {
      await client.request("startup.set_enabled", { id: entry.id, enabled: !entry.enabled });
      load();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusyId(null);
    }
  }

  async function remove() {
    if (!pendingRemove) return;
    setBusyId(pendingRemove.id);
    setActionError(null);
    try {
      await client.request("startup.remove", { id: pendingRemove.id });
      setPendingRemove(null);
      load();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusyId(null);
    }
  }

  async function addEntry() {
    setActionError(null);
    const args = newArgs.trim().length > 0 ? newArgs.trim().split(/\s+/) : [];
    try {
      await client.request("startup.add", { name: newName.trim(), exe: newExe.trim(), args });
      setShowAdd(false);
      setNewName("");
      setNewExe("");
      setNewArgs("");
      load();
    } catch (err) {
      setActionError(err instanceof Error ? err.message : String(err));
    }
  }

  const addFormValid = newName.trim().length > 0 && newExe.trim().length > 0;

  return (
    <div className="flex flex-col gap-4">
      <Panel className="flex min-h-0 flex-1 flex-col">
        <PanelHeader className="flex-wrap gap-2">
          <h2 className="text-sm font-semibold">Startup</h2>
          <div className="flex items-center gap-2">
            {entries && (
              <span className="text-xs text-text-muted">
                {entries.filter((e) => e.enabled).length} of {entries.length} enabled
              </span>
            )}
            <Button variant="ghost" onClick={() => setShowAdd((s) => !s)}>
              {showAdd ? "Cancel" : "Add custom entry"}
            </Button>
            <Button variant="ghost" onClick={load}>
              Refresh
            </Button>
          </div>
        </PanelHeader>

        {showAdd && (
          <div className="flex flex-col gap-2 border-b border-border p-4">
            <p className="text-xs text-text-muted">
              Registers a program to launch at login (per-user). Pick any executable - it runs with your
              normal user privileges when you sign in.
            </p>
            <div className="flex flex-wrap gap-2">
              <input
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="Name (e.g. My backup tool)"
                className="w-56 rounded-md border border-border bg-surface-2 px-2.5 py-1 text-xs text-text-primary placeholder:text-text-muted focus:outline-none"
              />
              <input
                value={newExe}
                onChange={(e) => setNewExe(e.target.value)}
                placeholder="Executable path (e.g. C:\\tools\\backup.exe)"
                className="min-w-72 flex-1 rounded-md border border-border bg-surface-2 px-2.5 py-1 font-mono text-xs text-text-primary placeholder:text-text-muted focus:outline-none"
              />
              <input
                value={newArgs}
                onChange={(e) => setNewArgs(e.target.value)}
                placeholder="Arguments (optional, space-separated)"
                className="min-w-64 flex-1 rounded-md border border-border bg-surface-2 px-2.5 py-1 font-mono text-xs text-text-primary placeholder:text-text-muted focus:outline-none"
              />
              <Button variant="primary" onClick={addEntry} disabled={!addFormValid}>
                Add startup entry
              </Button>
            </div>
          </div>
        )}

        {error && <p className="p-4 text-sm text-text-secondary">Couldn't load startup entries: {error}</p>}
        {actionError && <p className="px-4 pt-3 text-xs text-status-unhealthy">{actionError}</p>}
        {!error && !entries && <p className="p-4 text-sm text-text-muted">Loading...</p>}

        {entries && entries.length === 0 && (
          <p className="p-4 text-sm text-text-muted">
            No startup entries found in any OS launch location.
          </p>
        )}

        {entries && entries.length > 0 && (
          <div className="min-h-0 flex-1 overflow-y-auto">
            <table className="w-full text-left text-sm">
              <thead className="sticky top-0 bg-surface-1">
                <tr className="border-b border-border text-xs text-text-muted">
                  <SortableTh label="Name" sortKey="name" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Location" sortKey="location" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Scope" sortKey="scope" sort={sort} onToggle={toggleSort} />
                  <SortableTh label="Enabled" sortKey="enabled" sort={sort} onToggle={toggleSort} />
                  <th className="px-4 py-2 text-right font-medium">Actions</th>
                </tr>
              </thead>
              <tbody>
                {sorted.map((e) => (
                  <StartupRow
                    key={e.id}
                    entry={e}
                    busy={busyId === e.id}
                    onToggle={() => toggle(e)}
                    onRemove={() => setPendingRemove(e)}
                  />
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      <ConfirmDialog
        open={pendingRemove !== null}
        title={`Remove "${pendingRemove?.name ?? ""}" from startup?`}
        description="This deletes the underlying registration (registry value, autostart file, or launchd plist). The program itself is not uninstalled."
        confirmLabel="Remove entry"
        destructive
        onConfirm={remove}
        onCancel={() => setPendingRemove(null)}
      />
    </div>
  );
}

function StartupRow({
  entry,
  busy,
  onToggle,
  onRemove,
}: {
  entry: StartupEntry;
  busy: boolean;
  onToggle: () => void;
  onRemove: () => void;
}) {
  const mutable = entry.scope === "user";
  return (
    <tr
      tabIndex={0}
      onKeyDown={onActivateKey(onToggle)}
      className={`border-b border-border last:border-0 hover:bg-surface-2 ${FOCUS_RING}`}
    >
      <td className="px-4 py-2">
        <div className="text-text-primary">{entry.name}</div>
        {entry.command && (
          <div className="max-w-md truncate font-mono text-[10px] text-text-muted" title={entry.command}>
            {entry.command}
          </div>
        )}
      </td>
      <td className="px-4 py-2 text-text-secondary">{LOCATION_LABEL[entry.location] ?? entry.location}</td>
      <td className="px-4 py-2 text-text-secondary">
        {entry.scope === "system" ? (
          <span className="rounded bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium" title="Requires elevation to change">
            system
          </span>
        ) : (
          "user"
        )}
      </td>
      <td className="px-4 py-2">
        <button
          onClick={onToggle}
          disabled={!mutable || busy}
          title={mutable ? (entry.enabled ? "Click to disable" : "Click to enable") : "System-scope entries need an elevated process"}
          className={`rounded px-2 py-0.5 text-xs font-medium ${FOCUS_RING} ${
            entry.enabled
              ? "bg-accent/15 text-accent"
              : "bg-surface-2 text-text-muted"
          } ${!mutable || busy ? "cursor-not-allowed opacity-50" : "hover:opacity-80"}`}
        >
          {busy ? "…" : entry.enabled ? "Enabled" : "Disabled"}
        </button>
      </td>
      <td className="px-4 py-2 text-right">
        <button
          onClick={onRemove}
          disabled={!mutable || busy}
          className={`text-xs text-text-muted hover:text-status-unhealthy ${FOCUS_RING} rounded ${
            !mutable || busy ? "cursor-not-allowed opacity-40 hover:text-text-muted" : ""
          }`}
        >
          Remove
        </button>
      </td>
    </tr>
  );
}
