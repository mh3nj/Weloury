import type { ThemeSelection } from "@/hooks/useTheme";
import type { FwpClient } from "@/lib/ipc/client";
import { DiagnosticsPanel } from "./DiagnosticsPanel";
import { DesktopBehaviorPanel } from "./DesktopBehaviorPanel";
import { ThemeEditor } from "./ThemeEditor";
import { setActiveCustomTheme } from "./themeLibrary";

interface SettingsPageProps {
  client: FwpClient;
  selection: ThemeSelection;
  onThemeChange: (selection: ThemeSelection) => void;
}

export function SettingsPage({ client, selection, onThemeChange }: SettingsPageProps) {
  function handleThemeChange(next: ThemeSelection) {
    // The active-custom-theme marker is managed here (not inside the
    // editor) so switching back to a built-in theme always clears it,
    // whichever UI element initiated the switch.
    if (next.mode !== "custom") setActiveCustomTheme(null);
    onThemeChange(next);

    // Mirror the mode to Weloury core so the desktop shell (which reads
    // the same config file for its own themed surfaces) agrees across
    // restarts. Best-effort: the local application is immediate, this is
    // the cross-session record.
    void client.request("settings.update", {
      theme: next.mode === "custom" ? "dark" : next.mode,
    }).catch(() => {
      // Backend unreachable - the theme still applies locally.
    });
  }

  return (
    <div className="max-w-3xl">
      <ThemeEditor selection={selection} onApplySelection={handleThemeChange} />

      <div className="mt-4">
        <DesktopBehaviorPanel client={client} />
      </div>

      <DiagnosticsPanel client={client} />
    </div>
  );
}
