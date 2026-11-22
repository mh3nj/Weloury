import { useEffect, useMemo, useState } from "react";
import { useBackendConnection } from "@/hooks/useBackendConnection";
import { useTheme } from "@/hooks/useTheme";
import { AppShell } from "@/components/layout/AppShell";
import { CommandPalette } from "@/components/command-palette/CommandPalette";
import { OverviewPage } from "@/features/overview/OverviewPage";
import { SettingsPage } from "@/features/settings/SettingsPage";
import { PlaceholderPage } from "@/features/placeholder/PlaceholderPage";
import { StoragePage } from "@/features/storage/StoragePage";
import { ProcessesPage } from "@/features/processes/ProcessesPage";
import { ServicesPage } from "@/features/services/ServicesPage";
import { HealthPage } from "@/features/health/HealthPage";
import { LogsPage } from "@/features/logs/LogsPage";
import { EventsPage } from "@/features/events/EventsPage";
import { AutomationPage } from "@/features/automation/AutomationPage";
import { SchedulesPage } from "@/features/schedules/SchedulesPage";
import { ContainersPage } from "@/features/containers/ContainersPage";
import { NetworkPage } from "@/features/network/NetworkPage";
import { GpuPage } from "@/features/gpu/GpuPage";
import { StartupPage } from "@/features/startup/StartupPage";
import { NAV_ITEMS, IMPLEMENTED_PHASE, type ViewId } from "@/types/nav";

export default function App() {
  const [active, setActive] = useState<ViewId>("overview");
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [selection, setSelection] = useTheme();
  const connection = useBackendConnection();

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((open) => !open);
      }
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, []);

  const activeItem = useMemo(() => NAV_ITEMS.find((item) => item.id === active)!, [active]);
  const toggleTheme = () =>
    setSelection(
      selection.mode === "dark" ? { mode: "light" } : { mode: "dark" },
    );

  return (
    <AppShell
      active={active}
      onSelect={setActive}
      connection={connection}
      theme={selection.mode}
      onToggleTheme={toggleTheme}
      onOpenCommandPalette={() => setPaletteOpen(true)}
    >
      {activeItem.phase > IMPLEMENTED_PHASE ? (
        <PlaceholderPage item={activeItem} />
      ) : connection.status !== "connected" ? (
        <div className="flex h-full items-center justify-center text-sm text-text-secondary">
          {connection.status === "connecting"
            ? "Connecting to Weloury core..."
            : `Could not reach Weloury core: ${connection.status === "error" ? connection.message : ""}`}
        </div>
      ) : active === "overview" ? (
        <OverviewPage client={connection.client} />
      ) : active === "storage" ? (
        <StoragePage client={connection.client} />
      ) : active === "processes" ? (
        <ProcessesPage client={connection.client} />
      ) : active === "services" ? (
        <ServicesPage client={connection.client} />
      ) : active === "containers" ? (
        <ContainersPage client={connection.client} />
      ) : active === "network" ? (
        <NetworkPage client={connection.client} />
      ) : active === "gpu" ? (
        <GpuPage client={connection.client} />
      ) : active === "startup" ? (
        <StartupPage client={connection.client} />
      ) : active === "health" ? (
        <HealthPage client={connection.client} />
      ) : active === "logs" ? (
        <LogsPage client={connection.client} />
      ) : active === "events" ? (
        <EventsPage client={connection.client} />
      ) : active === "automation" ? (
        <AutomationPage client={connection.client} />
      ) : active === "schedules" ? (
        <SchedulesPage client={connection.client} />
      ) : active === "settings" ? (
        <SettingsPage client={connection.client} selection={selection} onThemeChange={setSelection} />
      ) : null}

      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} onNavigate={setActive} />
    </AppShell>
  );
}
