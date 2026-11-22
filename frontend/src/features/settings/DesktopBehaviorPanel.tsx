import { useEffect, useState } from "react";
import { Panel, PanelHeader } from "@/components/ui/Panel";
import { Button } from "@/components/ui/Button";
import type { FwpClient } from "@/lib/ipc/client";

/**
 * Desktop-shell settings: close-to-tray and run-at-startup.
 *
 * Each option lives in two places, on purpose: Weloury core's config file
 * (persisted via settings.update - the durable record the settings UI
 * shows after restart) and the Tauri shell's live state / the OS's real
 * autostart registration (applied via Tauri commands at save time -
 * see apps/desktop/src/main.rs and src/autostart.rs). Saving applies
 * immediately; nothing waits for a relaunch.
 *
 * When the frontend runs standalone in a browser (development), the
 * Tauri module import fails and the shell-specific half is disabled
 * with an honest note rather than pretending to save.
 */

type StartupMode = "none" | "plain" | "tray";

interface ShellCommands {
  getCloseToTray: () => Promise<boolean>;
  setCloseToTray: (enabled: boolean) => Promise<void>;
  getAutostart: () => Promise<StartupMode>;
  setAutostart: (mode: StartupMode) => Promise<void>;
}

async function resolveShellCommands(): Promise<ShellCommands | null> {
  try {
    const core = await import("@tauri-apps/api/core");
    return {
      getCloseToTray: () => core.invoke<boolean>("get_close_to_tray"),
      setCloseToTray: (enabled: boolean) => core.invoke<void>("set_close_to_tray", { enabled }),
      getAutostart: () => core.invoke<StartupMode>("get_autostart"),
      setAutostart: (mode: StartupMode) => core.invoke<void>("set_autostart", { mode }),
    };
  } catch {
    return null; // not running inside Tauri
  }
}

async function loadBackendSettings(client: FwpClient) {
  return client.request<{
    theme: string;
    close_to_tray: boolean;
    run_at_startup: boolean;
    run_at_startup_in_tray: boolean;
  }>("settings.get");
}

function persistStartupChoice(client: FwpClient, mode: StartupMode) {
  // settings.update treats each field as "only if provided"; enabling
  // one clears the other backend-side (they're mutually exclusive there).
  const params =
    mode === "plain"
      ? { run_at_startup: true }
      : mode === "tray"
        ? { run_at_startup_in_tray: true }
        : { run_at_startup: false, run_at_startup_in_tray: false };
  return client.request("settings.update", params);
}

function persistCloseToTray(client: FwpClient, enabled: boolean) {
  return client.request("settings.update", { close_to_tray: enabled });
}

export function DesktopBehaviorPanel({ client }: { client: FwpClient }) {
  const [shell, setShell] = useState<ShellCommands | null>(null);
  const [closeToTray, setCloseToTray] = useState(true);
  const [startup, setStartup] = useState<StartupMode>("none");
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [savedAt, setSavedAt] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const commands = await resolveShellCommands();
      if (cancelled) return;
      setShell(commands);

      try {
        const settings = await loadBackendSettings(client);
        if (cancelled) return;
        setCloseToTray(settings.close_to_tray);
        setStartup(
          settings.run_at_startup ? "plain" : settings.run_at_startup_in_tray ? "tray" : "none",
        );
      } catch {
        // Backend unreachable - leave the defaults shown; saving will
        // surface the error properly.
      }

      if (commands) {
        try {
          const [tray, auto] = await Promise.all([commands.getCloseToTray(), commands.getAutostart()]);
          if (cancelled) return;
          setCloseToTray(tray);
          setStartup(auto);
        } catch {
          // Shell commands exist but one failed (e.g. an older shell
          // binary); the backend-loaded values remain the display truth.
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [client]);

  async function handleSave(nextCloseToTray: boolean, nextStartup: StartupMode) {
    setSaving(true);
    setError(null);
    try {
      await persistCloseToTray(client, nextCloseToTray);
      await persistStartupChoice(client, nextStartup);
      if (shell) {
        await shell.setCloseToTray(nextCloseToTray);
        await shell.setAutostart(nextStartup);
      }
      setCloseToTray(nextCloseToTray);
      setStartup(nextStartup);
      setSavedAt(new Date().toLocaleTimeString());
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  const startupOptions: { mode: StartupMode; label: string; hint: string }[] = [
    { mode: "none", label: "Off", hint: "Weloury only starts when you open it" },
    { mode: "plain", label: "Run at startup", hint: "Starts with the window visible" },
    { mode: "tray", label: "Run at startup in tray", hint: "Starts minimized to the system tray" },
  ];

  return (
    <Panel>
      <PanelHeader>
        <h2 className="text-sm font-semibold">Desktop</h2>
      </PanelHeader>
      <div className="flex flex-col gap-4 p-4">
        <div className="flex items-center justify-between gap-4">
          <div>
            <p className="text-sm text-text-primary">Close to tray</p>
            <p className="text-xs text-text-muted">
              Closing the window keeps Weloury running in the system tray. Turn off to exit when the window closes.
            </p>
          </div>
          <Button
            variant={closeToTray ? "primary" : "secondary"}
            disabled={saving}
            onClick={() => void handleSave(!closeToTray, startup)}
            aria-pressed={closeToTray}
          >
            {closeToTray ? "On" : "Off"}
          </Button>
        </div>

        <div className="border-t border-border pt-4">
          <p className="text-sm text-text-primary">Startup</p>
          <p className="text-xs text-text-muted">Choose when Weloury starts with your computer - only one option can be active.</p>
          <div className="mt-2 flex flex-col gap-1.5">
            {startupOptions.map((option) => (
              <label
                key={option.mode}
                className={`flex cursor-pointer items-center gap-3 rounded-md border px-3 py-2 transition-colors ${
                  startup === option.mode ? "border-accent bg-surface-2" : "border-border hover:border-border-strong"
                }`}
              >
                <input
                  type="radio"
                  name="weloury-startup-mode"
                  className="accent-[rgb(var(--accent))]"
                  checked={startup === option.mode}
                  onChange={() => void handleSave(closeToTray, option.mode)}
                  disabled={saving}
                />
                <span>
                  <span className="block text-sm text-text-primary">{option.label}</span>
                  <span className="block text-xs text-text-muted">{option.hint}</span>
                </span>
              </label>
            ))}
          </div>
          {!shell && (
            <p className="mt-2 text-xs text-status-degraded">
              Running outside the desktop shell - saving persists the preference, but the tray/autostart behavior only
              applies in the installed app.
            </p>
          )}
        </div>

        {error && <p className="text-xs text-status-unhealthy">Saving failed: {error}</p>}
        {savedAt && !error && <p className="text-xs text-text-muted">Saved at {savedAt}. Changes are applied immediately.</p>}
      </div>
    </Panel>
  );
}
