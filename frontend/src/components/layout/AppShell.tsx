import type { PropsWithChildren } from "react";
import type { ViewId } from "@/types/nav";
import type { ConnectionState } from "@/hooks/useBackendConnection";
import type { ThemeSelection } from "@/hooks/useTheme";
import { Sidebar } from "./Sidebar";
import { TopBar } from "./TopBar";

interface AppShellProps {
  active: ViewId;
  onSelect: (view: ViewId) => void;
  connection: ConnectionState;
  theme: ThemeSelection["mode"];
  onToggleTheme: () => void;
  onOpenCommandPalette: () => void;
}

export function AppShell({
  active,
  onSelect,
  connection,
  theme,
  onToggleTheme,
  onOpenCommandPalette,
  children,
}: PropsWithChildren<AppShellProps>) {
  return (
    <div className="flex h-screen w-screen overflow-hidden text-text-primary">
      <Sidebar active={active} onSelect={onSelect} />
      <div className="flex min-w-0 flex-1 flex-col">
        <TopBar
          connection={connection}
          theme={theme}
          onToggleTheme={onToggleTheme}
          onOpenCommandPalette={onOpenCommandPalette}
        />
        <main className="min-h-0 flex-1 overflow-y-auto p-6">{children}</main>
      </div>
    </div>
  );
}
